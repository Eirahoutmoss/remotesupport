package webrtcpeer

import (
	"context"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestPionLoopbackPingPong(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connectedA := make(chan struct{}, 1)
	connectedB := make(chan struct{}, 1)
	pong := make(chan struct{}, 1)

	a, err := New(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := New(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	a.pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			select {
			case connectedA <- struct{}{}:
			default:
			}
		}
	})
	b.pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			select {
			case connectedB <- struct{}{}:
			default:
			}
		}
	})
	b.SetControlHandler(func(msg string) {
		if msg == "PING" {
			_ = b.SendControlText("PONG")
		}
	})
	a.SetControlHandler(func(msg string) {
		if msg == "PONG" {
			select {
			case pong <- struct{}{}:
			default:
			}
		}
	})
	if err := a.CreateControl(); err != nil {
		t.Fatal(err)
	}

	offer, err := a.Offer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := b.AcceptOffer(ctx, offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AddSignal(answer); err != nil {
		t.Fatal(err)
	}

	select {
	case <-connectedA:
	case <-ctx.Done():
		t.Fatal("operator peer did not connect")
	}
	select {
	case <-connectedB:
	case <-ctx.Done():
		t.Fatal("target peer did not connect")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := a.SendPing(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control channel did not open")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-pong:
	case <-ctx.Done():
		t.Fatal("PING/PONG did not complete")
	}
}
