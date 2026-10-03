package clientsignaling

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	server "github.com/eirahoutmoss/remotesupport/server/signaling"
)

func TestSplitPin(t *testing.T) {
	u, p := SplitPin(" wss://203.0.113.5:8443/v1/ws#pin=abc-_123 ")
	if u != "wss://203.0.113.5:8443/v1/ws" || p != "abc-_123" {
		t.Fatalf("got %q %q", u, p)
	}
	if u, p := SplitPin("ws://h:8091/v1/ws"); u != "ws://h:8091/v1/ws" || p != "" {
		t.Fatalf("got %q %q", u, p)
	}
}

func TestPinnedWSS(t *testing.T) {
	s := server.NewServer()
	defer s.Close()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	pin := SPKIPin(ts.Certificate())
	base := "wss://" + strings.TrimPrefix(ts.URL, "https://") + "/v1/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := Dial(ctx, base+"#pin="+pin)
	if err != nil {
		t.Fatalf("pinned dial: %v", err)
	}
	c.App = "test"
	if _, _, err := c.Create(ctx); err != nil {
		t.Fatal(err)
	}
	if c.ServerV != server.ProtocolVersion {
		t.Fatalf("server v = %d", c.ServerV)
	}
	_ = c.Close(ctx)

	if _, err := Dial(ctx, base+"#pin=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err == nil || !errors.Is(err, ErrPinMismatch) && !strings.Contains(err.Error(), "pin") {
		t.Fatalf("wrong pin accepted: %v", err)
	}
	if _, err := Dial(ctx, base); err == nil {
		t.Fatal("self-signed accepted without pin")
	}
}
