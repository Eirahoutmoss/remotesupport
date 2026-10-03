package signaling

import (
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"
)

func TestDeviceRegisterAndTechnicianConnect(t *testing.T) {
	s := NewServer()
	defer s.Close()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// Device registers (first contact sets the password).
	dev := dialServer(t, ts)
	defer dev.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, dev, Message{Type: "create", V: ProtocolVersion, DeviceID: "PC-01", Pass: "gizli123"})
	if got := readMessage(t, dev); got.Type != "created" || got.Code != "PC-01" {
		t.Fatalf("device create: %+v", got)
	}

	// Technician connects with the right id + password.
	op := dialServer(t, ts)
	defer op.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, op, Message{Type: "join", V: ProtocolVersion, DeviceID: "pc-01", Pass: "gizli123"}) // id case-insensitive
	if got := readMessage(t, op); got.Type != "joined" {
		t.Fatalf("technician join: %+v", got)
	}
	if pj := readMessage(t, dev); pj.Type != "peer_joined" {
		t.Fatalf("device peer_joined: %+v", pj)
	}
}

func TestDeviceWrongPasswordRejected(t *testing.T) {
	s := NewServer()
	defer s.Close()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	dev := dialServer(t, ts)
	defer dev.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, dev, Message{Type: "create", DeviceID: "PC-02", Pass: "dogruparola"})
	readMessage(t, dev)

	op := dialServer(t, ts)
	defer op.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, op, Message{Type: "join", DeviceID: "PC-02", Pass: "yanlis"})
	if got := readMessage(t, op); got.Type != "error" || got.Reason != "device_auth_failed" {
		t.Fatalf("expected device_auth_failed, got %+v", got)
	}
}

func TestDeviceReRegisterNeedsPassword(t *testing.T) {
	s := NewServer()
	defer s.Close()
	// First registration sets it.
	if err := s.authDevice("DEV-X", "ilkparola", true); err != nil {
		t.Fatal(err)
	}
	// A second machine claiming the same id with a wrong password is rejected.
	if err := s.authDevice("DEV-X", "baska", true); err == nil {
		t.Fatal("takeover with wrong password allowed")
	}
	// Correct password re-auths fine.
	if err := s.authDevice("DEV-X", "ilkparola", true); err != nil {
		t.Fatal(err)
	}
	// Too-short password on first contact is refused.
	if err := s.authDevice("DEV-Y", "ab", true); err == nil {
		t.Fatal("weak password accepted")
	}
}

func TestDevicePersistentSessionNotExpired(t *testing.T) {
	s := NewServer()
	defer s.Close()
	s.SetSessionTTL(20 * 1e6) // 20ms
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	dev := dialServer(t, ts)
	defer dev.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, dev, Message{Type: "create", DeviceID: "PC-03", Pass: "parolam12"})
	readMessage(t, dev)
	// Give cleanup a few ticks; a persistent device session must survive.
	op := dialServer(t, ts)
	defer op.Close(websocket.StatusNormalClosure, "")
	// small wait via a round-trip
	writeMessage(t, op, Message{Type: "join", DeviceID: "PC-03", Pass: "parolam12"})
	if got := readMessage(t, op); got.Type != "joined" {
		t.Fatalf("persistent device expired? got %+v", got)
	}
}
