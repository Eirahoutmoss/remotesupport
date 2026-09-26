// Package protocol defines the messages exchanged between agent, operator and
// the rendezvous/relay server.
//
// Two layers exist:
//   - Control messages (JSON, websocket text frames) between a client and the
//     server: registration, pairing, errors, keepalive.
//   - Session messages (binary, end-to-end encrypted) between agent and
//     operator. The relay only ever forwards these as opaque ciphertext.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Control message types (client <-> server, plaintext JSON).
const (
	CtlRegistered = "registered" // server -> agent: {code, ttl}
	CtlPaired     = "paired"     // server -> both: peer connected
	CtlExpired    = "expired"    // server -> agent: code expired, re-register
	CtlError      = "error"      // server -> client: {msg}
	CtlPing       = "ping"       // client -> server keepalive
)

// Control is a plaintext control message.
type Control struct {
	Type string `json:"type"`
	Code string `json:"code,omitempty"`
	TTL  int    `json:"ttl,omitempty"` // seconds
	Msg  string `json:"msg,omitempty"`
}

// Server error codes carried in Control.Msg.
const (
	ErrNotFound    = "not_found"
	ErrRateLimited = "rate_limited"
)

// Session message types (inside the encrypted channel). First byte of every
// decrypted payload.
const (
	MsgHello         byte = 1 // op -> agent: Hello
	MsgApproval      byte = 2 // agent -> op: Approval
	MsgScreenInfo    byte = 3 // agent -> op: ScreenInfo
	MsgFrame         byte = 4 // agent -> op: raw JPEG bytes
	MsgInput         byte = 5 // op -> agent: InputEvent
	MsgSelectMonitor byte = 6 // op -> agent: SelectMonitor
	MsgBye           byte = 7 // either: session ends
	MsgPing          byte = 8 // either: keepalive
)

// Hello introduces the operator to the agent.
type Hello struct {
	Name string `json:"name"`
}

// Approval is the user's decision on the agent side.
type Approval struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// Monitor describes one display in virtual-screen coordinates.
type Monitor struct {
	X       int  `json:"x"`
	Y       int  `json:"y"`
	W       int  `json:"w"`
	H       int  `json:"h"`
	Primary bool `json:"primary,omitempty"`
}

// AllMonitors selects the union of all displays.
const AllMonitors = -1

// ScreenInfo lists the agent's monitors and the one currently streamed.
type ScreenInfo struct {
	Monitors []Monitor `json:"monitors"`
	Active   int       `json:"active"`
}

// SelectMonitor asks the agent to stream a different monitor.
type SelectMonitor struct {
	Index int `json:"index"`
}

// InputEvent is a mouse or keyboard event. X/Y are normalized (0..1)
// relative to the streamed monitor so the operator needs no pixel math.
type InputEvent struct {
	T string  `json:"t"` // move, down, up, wheel, keydown, keyup
	X float64 `json:"x,omitempty"`
	Y float64 `json:"y,omitempty"`
	B int     `json:"b,omitempty"` // 0 left, 1 middle, 2 right
	D int     `json:"d,omitempty"` // wheel delta (120 per notch)
	K int     `json:"k,omitempty"` // Windows virtual-key code
}

// Encode builds a session payload: type byte followed by JSON (or raw bytes
// when v is []byte).
func Encode(t byte, v any) ([]byte, error) {
	if b, ok := v.([]byte); ok {
		return append([]byte{t}, b...), nil
	}
	if v == nil {
		return []byte{t}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append([]byte{t}, b...), nil
}

// Decode splits a session payload into type and body.
func Decode(p []byte) (byte, []byte, error) {
	if len(p) == 0 {
		return 0, nil, errors.New("protocol: empty message")
	}
	return p[0], p[1:], nil
}

// Validate rejects malformed or out-of-range input events before they reach
// the input injector.
func (e InputEvent) Validate() error {
	switch e.T {
	case "move", "down", "up", "wheel":
		if e.X < 0 || e.X > 1 || e.Y < 0 || e.Y > 1 {
			return fmt.Errorf("protocol: coordinates out of range")
		}
		if e.B < 0 || e.B > 2 {
			return fmt.Errorf("protocol: invalid button %d", e.B)
		}
		if e.D < -1200 || e.D > 1200 {
			return fmt.Errorf("protocol: invalid wheel delta %d", e.D)
		}
	case "keydown", "keyup":
		if e.K < 1 || e.K > 254 {
			return fmt.Errorf("protocol: invalid key code %d", e.K)
		}
	default:
		return fmt.Errorf("protocol: unknown input type %q", e.T)
	}
	return nil
}
