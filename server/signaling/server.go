package signaling

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	defaultSessionTTL  = 5 * time.Minute
	defaultMaxSessions = 1024
	defaultWriteTTL    = 5 * time.Second
	defaultCleanupTick = 1 * time.Second
	defaultRateWindow  = 1 * time.Minute
	defaultJoinFails   = 8
	codeBytes          = 5 // 40 bits; displayed as 8 base32 chars.
)

type Role string

const (
	RoleTarget   Role = "target"
	RoleOperator Role = "operator"
)

type Message struct {
	Type      string          `json:"type"`
	Code      string          `json:"code,omitempty"`
	Role      Role            `json:"role,omitempty"`
	Approved  bool            `json:"approved,omitempty"`
	Kind      string          `json:"kind,omitempty"` // offer, answer, candidate
	Payload   json.RawMessage `json:"payload,omitempty"`
	ExpiresIn int             `json:"expires_in,omitempty"`
	Reason    string          `json:"reason,omitempty"`
}

// Server is a small rendezvous/signaling service. It never carries session
// data. After approval it only forwards SDP/ICE signaling messages.
type Server struct {
	mu          sync.Mutex
	sessions    map[string]*session
	maxSessions int
	ttl         time.Duration
	writeTTL    time.Duration
	cleanupStop chan struct{}
	cleanupOnce sync.Once
	limits      map[string]*rateLimit
}

type session struct {
	code      string
	createdAt time.Time
	target    *peer
	operator  *peer
	joined    bool
	approved  bool
}

type peer struct {
	conn *websocket.Conn
	role Role
}

type rateLimit struct {
	windowStart time.Time
	failures    int
}

func NewServer() *Server {
	s := &Server{
		sessions:    make(map[string]*session),
		maxSessions: defaultMaxSessions,
		ttl:         defaultSessionTTL,
		writeTTL:    defaultWriteTTL,
		limits:      make(map[string]*rateLimit),
		cleanupStop: make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

func (s *Server) SetSessionTTL(ttl time.Duration) {
	if ttl > 0 {
		s.mu.Lock()
		s.ttl = ttl
		s.mu.Unlock()
	}
}

func (s *Server) SetMaxSessions(n int) {
	if n > 0 {
		s.mu.Lock()
		s.maxSessions = n
		s.mu.Unlock()
	}
}

// Close stops background cleanup. It is intended for tests and graceful
// shutdown of an embedding application.
func (s *Server) Close() {
	s.cleanupOnce.Do(func() { close(s.cleanupStop) })
	s.mu.Lock()
	peers := make([]*peer, 0, len(s.sessions)*2)
	for code, sess := range s.sessions {
		delete(s.sessions, code)
		if sess.target != nil {
			peers = append(peers, sess.target)
		}
		if sess.operator != nil {
			peers = append(peers, sess.operator)
		}
	}
	s.mu.Unlock()
	for _, p := range peers {
		_ = p.conn.Close(websocket.StatusNormalClosure, "server shutdown")
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ws", s.handleWS)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var first Message
	if err := wsjson.Read(ctx, conn, &first); err != nil {
		return
	}

	ip := remoteIP(r.RemoteAddr)
	switch first.Type {
	case "create":
		s.handleCreate(r.Context(), conn)
	case "join":
		s.handleJoin(r.Context(), conn, strings.ToUpper(strings.TrimSpace(first.Code)), ip)
	default:
		_ = s.write(conn, Message{Type: "error", Reason: "expected create or join"})
	}
}

func (s *Server) handleCreate(ctx context.Context, conn *websocket.Conn) {
	s.mu.Lock()
	if len(s.sessions) >= s.maxSessions {
		s.mu.Unlock()
		_ = s.write(conn, Message{Type: "error", Reason: "server_capacity"})
		return
	}
	var code string
	for {
		code = newCode()
		if _, ok := s.sessions[code]; !ok {
			break
		}
	}
	ttl := s.ttl
	sess := &session{code: code, createdAt: time.Now(), target: &peer{conn: conn, role: RoleTarget}}
	s.sessions[code] = sess
	s.mu.Unlock()

	if err := s.write(conn, Message{Type: "created", Code: code, Role: RoleTarget, ExpiresIn: int(ttl.Seconds())}); err != nil {
		s.remove(code)
		return
	}

	for {
		var msg Message
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			s.remove(code)
			return
		}
		switch msg.Type {
		case "approve":
			s.handleApprove(conn, code, msg.Approved, msg.Reason)
		case "signal", "close":
			s.forward(code, RoleTarget, msg)
		default:
			_ = s.write(conn, Message{Type: "error", Reason: "invalid_target_message"})
		}
	}
}

func (s *Server) handleJoin(ctx context.Context, conn *websocket.Conn, code, ip string) {
	if code == "" {
		_ = s.write(conn, Message{Type: "error", Reason: "invalid_code"})
		return
	}
	if s.joinRateLimited(ip) {
		_ = s.write(conn, Message{Type: "error", Reason: "rate_limited"})
		return
	}

	s.mu.Lock()
	sess := s.sessions[code]
	now := time.Now()
	if sess == nil || sess.target == nil || sess.operator != nil || sess.joined || now.Sub(sess.createdAt) > s.ttl {
		s.mu.Unlock()
		s.recordJoinFailure(ip, now)
		_ = s.write(conn, Message{Type: "error", Reason: "invalid_or_expired_code"})
		return
	}
	sess.joined = true // code is consumed at the first successful join.
	sess.operator = &peer{conn: conn, role: RoleOperator}
	target := sess.target.conn
	s.mu.Unlock()

	if err := s.write(conn, Message{Type: "joined", Code: code, Role: RoleOperator}); err != nil {
		s.remove(code)
		return
	}
	if err := s.write(target, Message{Type: "peer_joined"}); err != nil {
		s.remove(code)
		return
	}

	for {
		var msg Message
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			s.remove(code)
			return
		}
		switch msg.Type {
		case "signal", "close":
			s.forward(code, RoleOperator, msg)
		default:
			_ = s.write(conn, Message{Type: "error", Reason: "invalid_operator_message"})
		}
	}
}

func (s *Server) handleApprove(conn *websocket.Conn, code string, ok bool, reason string) {
	s.mu.Lock()
	sess := s.sessions[code]
	if sess == nil || sess.target == nil || sess.operator == nil || !sess.joined {
		s.mu.Unlock()
		_ = s.write(conn, Message{Type: "error", Reason: "approval_not_available"})
		return
	}
	if sess.approved {
		op := sess.operator.conn
		s.mu.Unlock()
		_ = s.write(op, Message{Type: "error", Reason: "approval_already_decided"})
		return
	}
	if !ok {
		op := sess.operator.conn
		delete(s.sessions, code)
		s.mu.Unlock()
		_ = s.write(op, Message{Type: "rejected", Reason: reason})
		_ = op.Close(websocket.StatusPolicyViolation, "rejected")
		return
	}
	sess.approved = true
	op := sess.operator.conn
	s.mu.Unlock()
	_ = s.write(op, Message{Type: "approved"})
}

func (s *Server) forward(code string, from Role, msg Message) {
	s.mu.Lock()
	sess := s.sessions[code]
	if sess == nil {
		s.mu.Unlock()
		return
	}
	if !sess.approved {
		var dst *websocket.Conn
		if from == RoleTarget {
			dst = sess.target.conn
		} else {
			dst = sess.operator.conn
		}
		s.mu.Unlock()
		_ = s.write(dst, Message{Type: "error", Reason: "approval_required"})
		return
	}
	var dst *websocket.Conn
	if from == RoleTarget && sess.operator != nil {
		dst = sess.operator.conn
	}
	if from == RoleOperator && sess.target != nil {
		dst = sess.target.conn
	}
	closeSession := msg.Type == "close"
	if closeSession {
		delete(s.sessions, code)
	}
	s.mu.Unlock()

	if dst != nil {
		_ = s.write(dst, msg)
	}
	if closeSession {
		s.closePeers(sess)
	}
}

func (s *Server) remove(code string) {
	s.mu.Lock()
	sess := s.sessions[code]
	delete(s.sessions, code)
	s.mu.Unlock()
	if sess != nil {
		s.closePeers(sess)
	}
}

func (s *Server) closePeers(sess *session) {
	seen := make(map[*websocket.Conn]struct{}, 2)
	for _, p := range []*peer{sess.target, sess.operator} {
		if p == nil || p.conn == nil {
			continue
		}
		if _, ok := seen[p.conn]; ok {
			continue
		}
		seen[p.conn] = struct{}{}
		_ = p.conn.Close(websocket.StatusNormalClosure, "session closed")
	}
}

func (s *Server) write(conn *websocket.Conn, msg Message) error {
	if conn == nil {
		return errors.New("signaling: nil connection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.writeTTL)
	defer cancel()
	return wsjson.Write(ctx, conn, msg)
}

func (s *Server) cleanupLoop() {
	t := time.NewTicker(defaultCleanupTick)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.cleanupExpired()
		case <-s.cleanupStop:
			return
		}
	}
}

func (s *Server) cleanupExpired() {
	now := time.Now()
	var expired []*session
	s.mu.Lock()
	for code, sess := range s.sessions {
		if !sess.approved && now.Sub(sess.createdAt) > s.ttl {
			delete(s.sessions, code)
			expired = append(expired, sess)
		}
	}
	// Keep the rate-limit map bounded.
	for ip, limit := range s.limits {
		if now.Sub(limit.windowStart) >= defaultRateWindow {
			delete(s.limits, ip)
		}
	}
	s.mu.Unlock()
	for _, sess := range expired {
		s.closePeers(sess)
	}
}

func (s *Server) joinRateLimited(ip string) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := s.limits[ip]
	if limit == nil || now.Sub(limit.windowStart) >= defaultRateWindow {
		s.limits[ip] = &rateLimit{windowStart: now}
		return false
	}
	return limit.failures >= defaultJoinFails
}

func (s *Server) recordJoinFailure(ip string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := s.limits[ip]
	if limit == nil || now.Sub(limit.windowStart) >= defaultRateWindow {
		s.limits[ip] = &rateLimit{windowStart: now, failures: 1}
		return
	}
	limit.failures++
}

func remoteIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil && host != "" {
		return host
	}
	return addr
}

func newCode() string {
	b := make([]byte, codeBytes)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("signaling: crypto/rand failed: %v", err))
	}
	return strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), "=")
}

var ErrNotApproved = errors.New("signaling: session not approved")
