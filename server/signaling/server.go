package signaling

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

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
	defaultCreates     = 20 // code creations per IP per rate window
	defaultTURNTTL     = 12 * time.Hour
	maxNameRunes       = 64
)

// ProtocolVersion is the signaling protocol spoken by this package. Clients
// send their own version in "v"; a server that predates versioning simply
// ignores the field, and a client that predates it sends 0.
//
//	v2: version/feature negotiation, operator identity in peer_joined,
//	    per-session TURN credentials ("turncred").
const ProtocolVersion = 2

// FeatureTURNCred means the server hands out short-lived TURN credentials
// after approval: an "ice" message to the target, and ICE in "approved".
const FeatureTURNCred = "turncred"

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

	// v2 fields — ignored by older peers.
	V        int       `json:"v,omitempty"`        // protocol version of the sender
	App      string    `json:"app,omitempty"`      // sender application version, for logs
	Name     string    `json:"name,omitempty"`     // operator's self-declared name (join, peer_joined)
	From     string    `json:"from,omitempty"`     // operator IP as seen by the server (peer_joined)
	Features []string  `json:"features,omitempty"` // server capabilities (created, joined)
	ICE      *ICECreds `json:"ice,omitempty"`      // TURN credentials (ice, approved)

	// Version advisories (created, joined). Informational only — the server
	// never rejects by app version (that is MinClientVersion, by protocol v).
	MinApp    string `json:"min_app,omitempty"`    // oldest app version still supported
	LatestApp string `json:"latest_app,omitempty"` // newest app version available

	// Device access (attended technician support by stable device id + password).
	DeviceID string `json:"device_id,omitempty"` // create: register/announce; join: target device
	Pass     string `json:"pass,omitempty"`      // device password (device create, operator join)
}

// ICECreds is a short-lived TURN credential in coturn "use-auth-secret"
// (TURN REST API) form: username "<expiry-unix>:<id>", password
// base64(HMAC-SHA1(secret, username)).
type ICECreds struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
	ExpiresAt  int64    `json:"expires_at"`
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
	creates     map[string]*rateLimit
	minClientV  int
	minApp      string
	latestApp   string
	turnURLs    []string
	turnSecret  []byte
	turnTTL     time.Duration
	logf        func(format string, a ...any)

	devMu      sync.Mutex
	devices    map[string]devReg // "DEV-<ID>" -> password record
	deviceFile string            // optional JSON persistence path
}

// devReg is one device's salted password hash.
type devReg struct {
	Salt []byte `json:"salt"`
	Hash []byte `json:"hash"`
}

type session struct {
	code       string
	createdAt  time.Time
	target     *peer
	operator   *peer
	joined     bool
	approved   bool
	persistent bool // device session: never TTL-expires, re-created by the device
}

type peer struct {
	conn *websocket.Conn
	role Role
	v    int
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
		creates:     make(map[string]*rateLimit),
		devices:     make(map[string]devReg),
		cleanupStop: make(chan struct{}),
		turnTTL:     defaultTURNTTL,
		logf:        func(string, ...any) {},
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

// SetMinClientVersion rejects clients whose protocol version is below n with
// the error reason "client_outdated". 0 (default) accepts every client.
func (s *Server) SetMinClientVersion(n int) {
	s.mu.Lock()
	s.minClientV = n
	s.mu.Unlock()
}

// SetAppVersions advertises version advisories to clients: min is the oldest
// app version still supported, latest the newest available. Both are purely
// informational (the client decides what to show); empty values advertise
// nothing. This lets clients nudge users to update before MinClientVersion is
// ever raised to actually reject them.
func (s *Server) SetAppVersions(min, latest string) {
	s.mu.Lock()
	s.minApp, s.latestApp = strings.TrimSpace(min), strings.TrimSpace(latest)
	s.mu.Unlock()
}

// appVers returns the configured advisories under lock.
func (s *Server) appVers() (min, latest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.minApp, s.latestApp
}

// SetTURN enables per-session TURN credentials. secret must match coturn's
// static-auth-secret; ttl <= 0 keeps the default.
func (s *Server) SetTURN(urls []string, secret string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnURLs = nil
	for _, u := range urls {
		if u = strings.TrimSpace(u); u != "" {
			s.turnURLs = append(s.turnURLs, u)
		}
	}
	s.turnSecret = []byte(secret)
	if ttl > 0 {
		s.turnTTL = ttl
	}
}

// SetLogger receives one line per notable event (create, join, reject…).
func (s *Server) SetLogger(f func(format string, a ...any)) {
	if f != nil {
		s.mu.Lock()
		s.logf = f
		s.mu.Unlock()
	}
}

func (s *Server) features() []string {
	if len(s.turnSecret) > 0 && len(s.turnURLs) > 0 {
		return []string{FeatureTURNCred}
	}
	return nil
}

// turnCreds issues a credential; nil when TURN is not configured.
func (s *Server) turnCreds() *ICECreds {
	s.mu.Lock()
	urls, secret, ttl := append([]string(nil), s.turnURLs...), s.turnSecret, s.turnTTL
	s.mu.Unlock()
	if len(secret) == 0 || len(urls) == 0 {
		return nil
	}
	return MakeTURNCreds(urls, secret, ttl, time.Now())
}

// MakeTURNCreds builds a coturn use-auth-secret credential valid until now+ttl.
func MakeTURNCreds(urls []string, secret []byte, ttl time.Duration, now time.Time) *ICECreds {
	id := make([]byte, 4)
	_, _ = rand.Read(id)
	exp := now.Add(ttl).Unix()
	user := strconv.FormatInt(exp, 10) + ":nd" + hex.EncodeToString(id)
	m := hmac.New(sha1.New, secret)
	m.Write([]byte(user))
	return &ICECreds{URLs: urls, Username: user, Credential: base64.StdEncoding.EncodeToString(m.Sum(nil)), ExpiresAt: exp}
}

// CleanName makes a self-declared display name safe to show in a dialog:
// no control characters (no injected line breaks), at most 64 runes.
func CleanName(n string) string {
	var b strings.Builder
	count := 0
	for _, r := range strings.TrimSpace(n) {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			r = ' '
		}
		if count >= maxNameRunes {
			break
		}
		b.WriteRune(r)
		count++
	}
	return strings.TrimSpace(b.String())
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
	s.mu.Lock()
	minV, logf := s.minClientV, s.logf
	s.mu.Unlock()
	if (first.Type == "create" || first.Type == "join") && first.V < minV {
		logf("reddedildi: eski istemci ip=%s v=%d app=%q", ip, first.V, first.App)
		_ = s.write(conn, Message{Type: "error", Reason: "client_outdated", V: ProtocolVersion})
		return
	}
	switch first.Type {
	case "create":
		if s.createRateLimited(ip) {
			logf("create sınırı: ip=%s", ip)
			_ = s.write(conn, Message{Type: "error", Reason: "rate_limited"})
			return
		}
		s.handleCreate(r.Context(), conn, first, ip)
	case "join":
		s.handleJoin(r.Context(), conn, strings.ToUpper(strings.TrimSpace(first.Code)), ip, first)
	default:
		_ = s.write(conn, Message{Type: "error", Reason: "expected create or join"})
	}
}

func (s *Server) handleCreate(ctx context.Context, conn *websocket.Conn, first Message, ip string) {
	if first.DeviceID != "" {
		s.handleDeviceCreate(ctx, conn, first, ip)
		return
	}
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
	sess := &session{code: code, createdAt: time.Now(), target: &peer{conn: conn, role: RoleTarget, v: first.V}}
	s.sessions[code] = sess
	feats, logf := s.features(), s.logf
	s.mu.Unlock()
	logf("kod oluşturuldu: ip=%s v=%d app=%q", ip, first.V, first.App)

	minApp, latestApp := s.appVers()
	if err := s.write(conn, Message{Type: "created", Code: code, Role: RoleTarget, ExpiresIn: int(ttl.Seconds()), V: ProtocolVersion, Features: feats, MinApp: minApp, LatestApp: latestApp}); err != nil {
		s.remove(code)
		return
	}

	s.targetReadLoop(ctx, conn, code)
}

// targetReadLoop handles the target side of a session (code or device) after it
// is created: approvals and signal/close forwarding.
func (s *Server) targetReadLoop(ctx context.Context, conn *websocket.Conn, code string) {
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

func deviceKey(id string) string { return "DEV-" + strings.ToUpper(strings.TrimSpace(id)) }

// handleDeviceCreate registers (first contact) or authenticates a device by
// password, then keeps a persistent, non-expiring session the device re-creates
// after each support session, so a technician can connect by device id +
// password whenever the device is online.
func (s *Server) handleDeviceCreate(ctx context.Context, conn *websocket.Conn, first Message, ip string) {
	key := deviceKey(first.DeviceID)
	if err := s.authDevice(key, first.Pass, true); err != nil {
		s.logf("cihaz kaydı reddedildi: %s ip=%s (%v)", key, ip, err)
		_ = s.write(conn, Message{Type: "error", Reason: err.Error()})
		return
	}
	s.mu.Lock()
	if old := s.sessions[key]; old != nil { // device reconnected: drop the stale one
		delete(s.sessions, key)
		go s.closePeers(old)
	}
	sess := &session{code: key, createdAt: time.Now(), target: &peer{conn: conn, role: RoleTarget, v: first.V}, persistent: true}
	s.sessions[key] = sess
	feats, logf := s.features(), s.logf
	s.mu.Unlock()
	logf("cihaz çevrimiçi: %s ip=%s v=%d", key, ip, first.V)

	minApp, latestApp := s.appVers()
	if err := s.write(conn, Message{Type: "created", Code: first.DeviceID, Role: RoleTarget, V: ProtocolVersion, Features: feats, MinApp: minApp, LatestApp: latestApp}); err != nil {
		s.remove(key)
		return
	}
	s.targetReadLoop(ctx, conn, key)
}

// authDevice registers a new device password (register=true) or verifies an
// existing one. A known device always requires the correct password.
func (s *Server) authDevice(key, pass string, register bool) error {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	reg, ok := s.devices[key]
	if !ok {
		if !register {
			return errors.New("unknown_device")
		}
		if len(pass) < 4 {
			return errors.New("weak_or_missing_password")
		}
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return errors.New("server_error")
		}
		s.devices[key] = devReg{Salt: salt, Hash: hashPass(salt, pass)}
		s.saveDevicesLocked()
		return nil
	}
	if subtle.ConstantTimeCompare(reg.Hash, hashPass(reg.Salt, pass)) != 1 {
		return errors.New("device_auth_failed")
	}
	return nil
}

func hashPass(salt []byte, pass string) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte("nexdesk-dev/v1|"))
	h.Write([]byte(pass))
	return h.Sum(nil)
}

// SetDeviceStore enables JSON persistence of the device registry and loads it.
func (s *Server) SetDeviceStore(path string) {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	s.deviceFile = path
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	m := map[string]devReg{}
	if json.Unmarshal(b, &m) == nil {
		s.devices = m
	}
}

func (s *Server) saveDevicesLocked() {
	if s.deviceFile == "" {
		return
	}
	if b, err := json.Marshal(s.devices); err == nil {
		tmp := s.deviceFile + ".tmp"
		if os.WriteFile(tmp, b, 0600) == nil {
			_ = os.Rename(tmp, s.deviceFile)
		}
	}
}

func (s *Server) handleJoin(ctx context.Context, conn *websocket.Conn, code, ip string, first Message) {
	device := first.DeviceID != ""
	if device {
		code = deviceKey(first.DeviceID)
	}
	if code == "" {
		_ = s.write(conn, Message{Type: "error", Reason: "invalid_code"})
		return
	}
	if s.joinRateLimited(ip) {
		_ = s.write(conn, Message{Type: "error", Reason: "rate_limited"})
		return
	}
	if device {
		// Verify the device password before revealing whether it is online; a
		// bad password counts as a join failure for the rate limiter.
		if err := s.authDevice(code, first.Pass, false); err != nil {
			s.recordJoinFailure(ip, time.Now())
			s.logf("cihaz parolası reddedildi: %s ip=%s", code, ip)
			_ = s.write(conn, Message{Type: "error", Reason: "device_auth_failed"})
			return
		}
	}

	s.mu.Lock()
	sess := s.sessions[code]
	now := time.Now()
	if sess == nil || sess.target == nil || sess.operator != nil || sess.joined || now.Sub(sess.createdAt) > s.ttl {
		logf := s.logf
		s.mu.Unlock()
		s.recordJoinFailure(ip, now)
		logf("geçersiz kod denemesi: ip=%s", ip)
		_ = s.write(conn, Message{Type: "error", Reason: "invalid_or_expired_code"})
		return
	}
	sess.joined = true // code is consumed at the first successful join.
	sess.operator = &peer{conn: conn, role: RoleOperator, v: first.V}
	target := sess.target.conn
	feats, logf := s.features(), s.logf
	s.mu.Unlock()
	name := CleanName(first.Name)
	logf("koda katılındı: ip=%s v=%d app=%q ad=%q", ip, first.V, first.App, name)

	minApp, latestApp := s.appVers()
	if err := s.write(conn, Message{Type: "joined", Code: code, Role: RoleOperator, V: ProtocolVersion, Features: feats, MinApp: minApp, LatestApp: latestApp}); err != nil {
		s.remove(code)
		return
	}
	if err := s.write(target, Message{Type: "peer_joined", Code: code, Name: name, From: ip, V: first.V}); err != nil {
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
		target := sess.target.conn
		s.mu.Unlock()
		_ = s.write(target, Message{Type: "error", Reason: "approval_already_decided"})
		return
	}
	if !ok {
		op := sess.operator.conn
		target := sess.target.conn
		delete(s.sessions, code)
		s.mu.Unlock()
		_ = s.write(op, Message{Type: "rejected", Reason: reason})
		_ = op.Close(websocket.StatusPolicyViolation, "rejected")
		_ = target.Close(websocket.StatusPolicyViolation, "rejected")
		return
	}
	sess.approved = true
	op := sess.operator.conn
	opV, tgV := sess.operator.v, sess.target.v
	target := sess.target.conn
	s.mu.Unlock()
	var creds *ICECreds
	if opV >= 2 || tgV >= 2 {
		creds = s.turnCreds()
	}
	// The target learns its credential before the operator is told to send
	// an offer, so the "ice" message always precedes the first signal.
	if creds != nil && tgV >= 2 {
		_ = s.write(target, Message{Type: "ice", ICE: creds})
	}
	approved := Message{Type: "approved"}
	if opV >= 2 {
		approved.ICE = creds
	}
	_ = s.write(op, approved)
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
		if !sess.persistent && !sess.approved && now.Sub(sess.createdAt) > s.ttl {
			delete(s.sessions, code)
			expired = append(expired, sess)
		}
	}
	// Keep the rate-limit maps bounded.
	for _, m := range []map[string]*rateLimit{s.limits, s.creates} {
		for ip, limit := range m {
			if now.Sub(limit.windowStart) >= defaultRateWindow {
				delete(m, ip)
			}
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

// createRateLimited counts this create and reports whether the IP is over the
// per-minute limit. Loopback (the embedded LAN server's own UI) is exempt.
func (s *Server) createRateLimited(ip string) bool {
	if p := net.ParseIP(ip); p != nil && p.IsLoopback() {
		return false
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := s.creates[ip]
	if limit == nil || now.Sub(limit.windowStart) >= defaultRateWindow {
		s.creates[ip] = &rateLimit{windowStart: now, failures: 1}
		return false
	}
	limit.failures++
	return limit.failures > defaultCreates
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
	// 10-digit code (10 billion combinations) — 6 digits was too weak for
	// use over the internet. Single-use + short TTL + explicit consent still apply.
	n, err := rand.Int(rand.Reader, big.NewInt(10_000_000_000))
	if err != nil {
		panic(fmt.Sprintf("signaling: crypto/rand failed: %v", err))
	}
	return fmt.Sprintf("%010d", n.Int64())
}

var ErrNotApproved = errors.New("signaling: session not approved")
