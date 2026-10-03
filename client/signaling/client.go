package clientsignaling

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	server "github.com/eirahoutmoss/remotesupport/server/signaling"
)

// Client is one signaling connection. Set App and Name before Create/Join;
// after they return, ServerV and Features describe the server (0/nil for a
// server that predates protocol versioning).
type Client struct {
	conn   *websocket.Conn
	url    string
	wmu    sync.Mutex // serializes all frame writes (coder/websocket forbids concurrent writes)
	kaOnce sync.Once

	App  string // our application version, sent for the server's logs
	Name string // operator display name, shown to the target before approval

	ServerV  int
	Features []string

	// Version advisories from the server (created/joined). Empty when the
	// server advertises none. Informational — used to nudge the user to update.
	MinApp    string
	LatestApp string
}

// ErrPinMismatch is returned when a pinned wss server presents another key.
var ErrPinMismatch = errors.New("signaling: sunucu sertifikası beklenen pin ile eşleşmiyor")

// SplitPin separates "wss://host:port/v1/ws#pin=XYZ" into the dial URL and
// the pin. The fragment is never sent on the wire.
func SplitPin(raw string) (dialURL, pin string) {
	raw = strings.TrimSpace(raw)
	i := strings.IndexByte(raw, '#')
	if i < 0 {
		return raw, ""
	}
	frag := raw[i+1:]
	dialURL = raw[:i]
	if q, err := url.ParseQuery(frag); err == nil {
		pin = strings.TrimSpace(q.Get("pin"))
	}
	return dialURL, pin
}

// SPKIPin is the pin format used by the server: base64url(SHA-256(SPKI)).
func SPKIPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Dial connects to ws:// or wss:// URLs. A wss URL with #pin=… accepts only a
// certificate whose public key matches the pin (self-signed server on a bare
// IP); a wss URL without a pin uses normal CA verification.
func Dial(ctx context.Context, rawURL string) (*Client, error) {
	dialURL, pin := SplitPin(rawURL)
	var opts *websocket.DialOptions
	if strings.HasPrefix(strings.ToLower(dialURL), "wss://") && pin != "" {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // replaced by the pin check below
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 || SPKIPin(cs.PeerCertificates[0]) != pin {
					return ErrPinMismatch
				}
				return nil
			},
		}
		opts = &websocket.DialOptions{HTTPClient: &http.Client{Transport: tr, Timeout: 15 * time.Second}}
	}
	conn, _, err := websocket.Dial(ctx, dialURL, opts)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, url: dialURL}, nil
}

// HasFeature reports a capability advertised by the server.
func (c *Client) HasFeature(f string) bool {
	for _, x := range c.Features {
		if x == f {
			return true
		}
	}
	return false
}

func (c *Client) Create(ctx context.Context) (string, int, error) {
	if c.conn == nil {
		return "", 0, errors.New("signaling: client closed")
	}
	if err := c.writeMsg(ctx, server.Message{Type: "create", V: server.ProtocolVersion, App: c.App}); err != nil {
		return "", 0, err
	}
	var msg server.Message
	if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
		return "", 0, err
	}
	if msg.Type == "error" {
		return "", 0, fmt.Errorf("signaling: %s", msg.Reason)
	}
	if msg.Type != "created" || msg.Code == "" {
		return "", 0, errors.New("signaling: invalid create response")
	}
	c.ServerV, c.Features = msg.V, msg.Features
	c.MinApp, c.LatestApp = msg.MinApp, msg.LatestApp
	return msg.Code, msg.ExpiresIn, nil
}

// CreateDevice registers/announces this machine as a device by id + password,
// returning when the server accepts it. The device then serves technician
// sessions like a normal target. ExpiresIn is 0 (device sessions do not expire).
func (c *Client) CreateDevice(ctx context.Context, deviceID, pass string) error {
	if c.conn == nil {
		return errors.New("signaling: client closed")
	}
	if err := c.writeMsg(ctx, server.Message{Type: "create", V: server.ProtocolVersion, App: c.App, DeviceID: deviceID, Pass: pass}); err != nil {
		return err
	}
	var msg server.Message
	if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
		return err
	}
	if msg.Type == "error" {
		return fmt.Errorf("signaling: %s", msg.Reason)
	}
	if msg.Type != "created" {
		return errors.New("signaling: invalid device create response")
	}
	c.ServerV, c.Features = msg.V, msg.Features
	c.MinApp, c.LatestApp = msg.MinApp, msg.LatestApp
	return nil
}

// JoinDevice connects a technician to an online device by id + password.
func (c *Client) JoinDevice(ctx context.Context, deviceID, pass string) error {
	if c.conn == nil {
		return errors.New("signaling: client closed")
	}
	if err := c.writeMsg(ctx, server.Message{Type: "join", V: server.ProtocolVersion, App: c.App, Name: c.Name, DeviceID: deviceID, Pass: pass}); err != nil {
		return err
	}
	var msg server.Message
	if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
		return err
	}
	if msg.Type == "error" {
		return fmt.Errorf("signaling: %s", msg.Reason)
	}
	if msg.Type != "joined" {
		return errors.New("signaling: invalid device join response")
	}
	c.ServerV, c.Features = msg.V, msg.Features
	c.MinApp, c.LatestApp = msg.MinApp, msg.LatestApp
	return nil
}

func (c *Client) Join(ctx context.Context, code string) error {
	if c.conn == nil {
		return errors.New("signaling: client closed")
	}
	if err := c.writeMsg(ctx, server.Message{Type: "join", Code: code, V: server.ProtocolVersion, App: c.App, Name: c.Name}); err != nil {
		return err
	}
	var msg server.Message
	if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
		return err
	}
	if msg.Type == "error" {
		return fmt.Errorf("signaling: %s", msg.Reason)
	}
	if msg.Type != "joined" {
		return errors.New("signaling: invalid join response")
	}
	c.ServerV, c.Features = msg.V, msg.Features
	c.MinApp, c.LatestApp = msg.MinApp, msg.LatestApp
	return nil
}

func (c *Client) Read(ctx context.Context) (server.Message, error) {
	var msg server.Message
	if err := wsjson.Read(ctx, c.conn, &msg); err != nil {
		return server.Message{}, err
	}
	return msg, nil
}

func (c *Client) Approve(ctx context.Context, ok bool, reason string) error {
	return c.writeMsg(ctx, server.Message{Type: "approve", Approved: ok, Reason: reason})
}

func (c *Client) Signal(ctx context.Context, sigKind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.writeMsg(ctx, server.Message{Type: "signal", Kind: sigKind, Payload: b})
}

// writeMsg serializes every outbound frame. pion sends ICE candidates from its
// own goroutines while the main loop may also be writing (offer/answer,
// approve) and KeepAlive may be pinging; coder/websocket forbids concurrent
// writes, so they must all go through this mutex.
func (c *Client) writeMsg(ctx context.Context, m server.Message) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return wsjson.Write(ctx, c.conn, m)
}

// KeepAlive sends a WebSocket ping every interval until ctx ends or the
// connection closes. NAT gateways and corporate proxies drop idle TCP flows
// after a few minutes; once a session is connected the signaling channel is
// otherwise silent, yet auto-reconnect needs it to renegotiate. The server
// answers pings while it reads, so nothing changes on the server side.
// A missing pong is not treated as fatal: the target UI may be blocked in the
// approval dialog and not reading for a while.
func (c *Client) KeepAlive(ctx context.Context, interval time.Duration) {
	if c.conn == nil || interval <= 0 {
		return
	}
	c.kaOnce.Do(func() { go c.keepAliveLoop(ctx, interval) })
}

func (c *Client) keepAliveLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			c.wmu.Lock()
			err := c.conn.Ping(pctx)
			c.wmu.Unlock()
			cancel()
			if err != nil && (errors.Is(err, net.ErrClosed) || websocket.CloseStatus(err) != -1) {
				return
			}
		}
	}
}

func (c *Client) Close(ctx context.Context) error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close(websocket.StatusNormalClosure, "")
}
