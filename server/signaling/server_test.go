package signaling

import (
	"context"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func wsTestURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	u.Scheme = "ws"
	u.Path = "/v1/ws"
	return u.String()
}

func readMessage(t *testing.T, conn *websocket.Conn) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var msg Message
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

func dialServer(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsTestURL(ts.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func writeMessage(t *testing.T, conn *websocket.Conn, msg Message) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, msg); err != nil {
		t.Fatal(err)
	}
}

func TestCreatedCodeIsSixDigits(t *testing.T) {
	s := NewServer()
	defer s.Close()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	target := dialServer(t, ts)
	defer target.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, target, Message{Type: "create"})
	created := readMessage(t, target)
	if created.Type != "created" {
		t.Fatalf("got %q", created.Type)
	}
	if !regexp.MustCompile(`^[0-9]{10}$`).MatchString(created.Code) {
		t.Fatalf("code is not ten digits: %q", created.Code)
	}
}

func TestSessionExpiresWithoutTargetTraffic(t *testing.T) {
	s := NewServer()
	defer s.Close()
	s.SetSessionTTL(50 * time.Millisecond)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	target := dialServer(t, ts)
	defer target.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, target, Message{Type: "create"})
	created := readMessage(t, target)
	if created.Type != "created" {
		t.Fatalf("got %q", created.Type)
	}

	time.Sleep(2 * defaultCleanupTick)

	operator := dialServer(t, ts)
	defer operator.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, operator, Message{Type: "join", Code: created.Code})
	got := readMessage(t, operator)
	if got.Type != "error" || got.Reason != "invalid_or_expired_code" {
		t.Fatalf("got %+v", got)
	}
}

func TestApprovalRequiredForSignalsAndDuplicateApproval(t *testing.T) {
	s := NewServer()
	defer s.Close()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	target := dialServer(t, ts)
	defer target.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, target, Message{Type: "create"})
	created := readMessage(t, target)

	operator := dialServer(t, ts)
	defer operator.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, operator, Message{Type: "join", Code: created.Code})
	if got := readMessage(t, operator); got.Type != "joined" {
		t.Fatalf("join: %+v", got)
	}
	if got := readMessage(t, target); got.Type != "peer_joined" {
		t.Fatalf("peer joined: %+v", got)
	}

	writeMessage(t, operator, Message{Type: "signal", Kind: "offer", Payload: []byte(`{}`)})
	if got := readMessage(t, operator); got.Type != "error" || got.Reason != "approval_required" {
		t.Fatalf("pre-approval signal: %+v", got)
	}

	writeMessage(t, target, Message{Type: "approve", Approved: true})
	if got := readMessage(t, operator); got.Type != "approved" {
		t.Fatalf("approval: %+v", got)
	}

	writeMessage(t, target, Message{Type: "approve", Approved: true})
	if got := readMessage(t, target); got.Type != "error" || got.Reason != "approval_already_decided" {
		t.Fatalf("duplicate approval: %+v", got)
	}
}

func TestRejectedApprovalClosesSession(t *testing.T) {
	s := NewServer()
	defer s.Close()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	target := dialServer(t, ts)
	defer target.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, target, Message{Type: "create"})
	created := readMessage(t, target)

	operator := dialServer(t, ts)
	defer operator.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, operator, Message{Type: "join", Code: created.Code})
	_ = readMessage(t, operator)
	_ = readMessage(t, target)

	writeMessage(t, target, Message{Type: "approve", Approved: false, Reason: "user rejected"})
	if got := readMessage(t, operator); got.Type != "rejected" {
		t.Fatalf("rejection: %+v", got)
	}
}

func TestJoinRateLimit(t *testing.T) {
	s := NewServer()
	defer s.Close()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for i := 0; i < defaultJoinFails; i++ {
		conn := dialServer(t, ts)
		writeMessage(t, conn, Message{Type: "join", Code: "NO-SUCH-CODE"})
		if got := readMessage(t, conn); got.Type != "error" || got.Reason != "invalid_or_expired_code" {
			t.Fatalf("attempt %d: %+v", i+1, got)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}

	conn := dialServer(t, ts)
	defer conn.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, conn, Message{Type: "join", Code: "NO-SUCH-CODE"})
	if got := readMessage(t, conn); got.Type != "error" || got.Reason != "rate_limited" {
		t.Fatalf("rate limit: %+v", got)
	}
}

func TestApprovedSessionIsNotExpiredByPendingTTL(t *testing.T) {
	s := NewServer()
	defer s.Close()
	s.SetSessionTTL(50 * time.Millisecond)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	target := dialServer(t, ts)
	defer target.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, target, Message{Type: "create"})
	created := readMessage(t, target)

	operator := dialServer(t, ts)
	defer operator.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, operator, Message{Type: "join", Code: created.Code})
	if got := readMessage(t, operator); got.Type != "joined" {
		t.Fatalf("join: %+v", got)
	}
	_ = readMessage(t, target)

	writeMessage(t, target, Message{Type: "approve", Approved: true})
	if got := readMessage(t, operator); got.Type != "approved" {
		t.Fatalf("approval: %+v", got)
	}

	time.Sleep(3 * defaultCleanupTick)

	writeMessage(t, operator, Message{Type: "signal", Kind: "candidate", Payload: []byte(`{"candidate":"x"}`)})
	if got := readMessage(t, target); got.Type != "signal" || got.Kind != "candidate" {
		t.Fatalf("approved session expired unexpectedly: %+v", got)
	}
}
