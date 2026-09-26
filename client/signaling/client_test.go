package clientsignaling

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	server "github.com/eirahoutmoss/remotesupport/server/signaling"
)

func TestCreateJoinApproveSignal(t *testing.T) {
	s := server.NewServer()
	h := httptest.NewServer(s.Handler())
	defer h.Close()

	u, _ := url.Parse(h.URL)
	u.Scheme = "ws"
	u.Path = "/v1/ws"
	wsURL := u.String()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target, err := Dial(ctx, wsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close(context.Background())
	code, _, err := target.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}

	operator, err := Dial(ctx, wsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close(context.Background())
	if err := operator.Join(ctx, strings.ToLower(code)); err != nil {
		t.Fatal(err)
	}

	msg, err := target.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != "peer_joined" {
		t.Fatalf("got %q", msg.Type)
	}
	if err := target.Approve(ctx, true, ""); err != nil {
		t.Fatal(err)
	}

	msg, err = operator.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != "approved" {
		t.Fatalf("got %q", msg.Type)
	}

	payload := map[string]string{"sdp": "test"}
	if err := operator.Signal(ctx, "offer", payload); err != nil {
		t.Fatal(err)
	}
	msg, err = target.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != "signal" || msg.Kind != "offer" {
		t.Fatalf("unexpected signal: %#v", msg)
	}
}
