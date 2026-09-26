package signaling

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
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

func NewServer() *Server {
	return &Server{sessions: make(map[string]*session), maxSessions: defaultMaxSessions, ttl: defaultSessionTTL}
}

func (s *Server) SetSessionTTL(ttl time.Duration) {
	if ttl > 0 {
		s.ttl = ttl
	}
}
func (s *Server) SetMaxSessions(n int) {
	if n > 0 {
		s.maxSessions = n
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

	switch first.Type {
	case "create":
		s.handleCreate(r.Context(), conn)
	case "join":
		s.handleJoin(r.Context(), conn, strings.ToUpper(strings.TrimSpace(first.Code)))
	default:
		_ = wsjson.Write(ctx, conn, Message{Type: "error", Reason: "expected create or join"})
	}
}

func (s *Server) handleCreate(ctx context.Context, conn *websocket.Conn) {
	s.mu.Lock()
	if len(s.sessions) >= s.maxSessions {
		s.mu.Unlock()
		_ = wsjson.Write(ctx, conn, Message{Type: "error", Reason: "server_capacity"})
		return
	}
	var code string
	for {
		code = newCode()
		if _, ok := s.sessions[code]; !ok {
			break
		}
	}
	sess := &session{code: code, createdAt: time.Now(), target: &peer{conn: conn, role: RoleTarget}}
	s.sessions[code] = sess
	s.mu.Unlock()

	if err := wsjson.Write(ctx, conn, Message{Type: "created", Code: code, Role: RoleTarget, ExpiresIn: int(s.ttl.Seconds())}); err != nil {
		s.remove(code)
		return
	}

	t := time.NewTimer(s.ttl)
	defer t.Stop()
	for {
		var msg Message
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			s.remove(code)
			return
		}
		switch msg.Type {
		case "approve":
			s.handleApprove(ctx, code, msg.Approved, msg.Reason)
		case "signal", "close":
			s.forward(ctx, code, RoleTarget, msg)
		default:
			_ = wsjson.Write(ctx, conn, Message{Type: "error", Reason: "invalid_target_message"})
		}
		select {
		case <-t.C:
			s.remove(code)
			return
		default:
		}
	}
}

func (s *Server) handleJoin(ctx context.Context, conn *websocket.Conn, code string) {
	if code == "" {
		_ = wsjson.Write(ctx, conn, Message{Type: "error", Reason: "invalid_code"})
		return
	}
	s.mu.Lock()
	sess := s.sessions[code]
	if sess == nil || sess.target == nil || sess.operator != nil || sess.joined || time.Since(sess.createdAt) > s.ttl {
		s.mu.Unlock()
		_ = wsjson.Write(ctx, conn, Message{Type: "error", Reason: "invalid_or_expired_code"})
		return
	}
	sess.joined = true // code is consumed at the first successful join.
	sess.operator = &peer{conn: conn, role: RoleOperator}
	target := sess.target.conn
	s.mu.Unlock()

	if err := wsjson.Write(ctx, conn, Message{Type: "joined", Code: code, Role: RoleOperator}); err != nil {
		s.remove(code)
		return
	}
	if err := wsjson.Write(ctx, target, Message{Type: "peer_joined"}); err != nil {
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
			s.forward(ctx, code, RoleOperator, msg)
		default:
			_ = wsjson.Write(ctx, conn, Message{Type: "error", Reason: "invalid_operator_message"})
		}
	}
}

func (s *Server) handleApprove(ctx context.Context, code string, ok bool, reason string) {
	s.mu.Lock()
	sess := s.sessions[code]
	if sess == nil || sess.target == nil || sess.operator == nil || !sess.joined || sess.approved {
		s.mu.Unlock()
		return
	}
	if !ok {
		op := sess.operator.conn
		delete(s.sessions, code)
		s.mu.Unlock()
		_ = wsjson.Write(ctx, op, Message{Type: "rejected", Reason: reason})
		_ = op.Close(websocket.StatusPolicyViolation, "rejected")
		return
	}
	sess.approved = true
	op := sess.operator.conn
	s.mu.Unlock()
	_ = wsjson.Write(ctx, op, Message{Type: "approved"})
}

func (s *Server) forward(ctx context.Context, code string, from Role, msg Message) {
	s.mu.Lock()
	sess := s.sessions[code]
	if sess == nil || !sess.approved {
		s.mu.Unlock()
		return
	}
	var dst *websocket.Conn
	if from == RoleTarget && sess.operator != nil {
		dst = sess.operator.conn
	}
	if from == RoleOperator && sess.target != nil {
		dst = sess.target.conn
	}
	if msg.Type == "close" {
		delete(s.sessions, code)
	}
	s.mu.Unlock()
	if dst != nil {
		_ = wsjson.Write(ctx, dst, msg)
	}
}

func (s *Server) remove(code string) {
	s.mu.Lock()
	sess := s.sessions[code]
	delete(s.sessions, code)
	s.mu.Unlock()
	if sess != nil {
		if sess.target != nil {
			_ = sess.target.conn.Close(websocket.StatusNormalClosure, "session closed")
		}
		if sess.operator != nil {
			_ = sess.operator.conn.Close(websocket.StatusNormalClosure, "session closed")
		}
	}
}

func newCode() string {
	b := make([]byte, codeBytes)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("signaling: crypto/rand failed: %v", err))
	}
	return strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), "=")
}

var ErrNotApproved = errors.New("signaling: session not approved")
