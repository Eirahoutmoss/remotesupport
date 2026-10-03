package clientsignaling

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	server "github.com/eirahoutmoss/remotesupport/server/signaling"
)

// Pings flow while the session sits idle, and the session still works after.
func TestKeepAliveDuringIdleSession(t *testing.T) {
	s := server.NewServer()
	defer s.Close()
	h := httptest.NewServer(s.Handler())
	defer h.Close()
	ws := "ws://" + strings.TrimPrefix(h.URL, "http://") + "/v1/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tg, err := Dial(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := tg.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op, err := Dial(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := op.Join(ctx, code); err != nil {
		t.Fatal(err)
	}
	kctx, kcancel := context.WithCancel(ctx)
	defer kcancel()
	tg.KeepAlive(kctx, 20*time.Millisecond)
	op.KeepAlive(kctx, 20*time.Millisecond)

	// The apps keep a reader running; pongs are only processed by a reader.
	msgs := make(chan server.Message, 4)
	go func() {
		for {
			m, err := tg.Read(ctx)
			if err != nil {
				return
			}
			msgs <- m
		}
	}()
	go func() {
		for {
			if _, err := op.Read(ctx); err != nil {
				return
			}
		}
	}()
	if m := <-msgs; m.Type != "peer_joined" {
		t.Fatalf("got %q", m.Type)
	}
	time.Sleep(200 * time.Millisecond) // ~10 ping/pong rounds
	pctx, pcancel := context.WithTimeout(ctx, time.Second)
	defer pcancel()
	if err := tg.conn.Ping(pctx); err != nil {
		t.Fatalf("connection unhealthy after keepalive: %v", err)
	}
	_ = tg.Close(ctx)
	_ = op.conn.Close(websocket.StatusNormalClosure, "")
}
