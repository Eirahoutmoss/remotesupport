package integration

import (
	"context"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eirahoutmoss/remotesupport/client/signaling"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	server "github.com/eirahoutmoss/remotesupport/server/signaling"
)

func wsURL(tsURL string) string {
	u, err := url.Parse(tsURL)
	if err != nil {
		panic(err)
	}
	u.Scheme = "ws"
	u.Path = "/v1/ws"
	return u.String()
}

func waitMsg(t *testing.T, ch <-chan server.Message, typ string) server.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case msg := <-ch:
			if msg.Type == typ {
				return msg
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for signaling message %q", typ)
		}
	}
}

func TestEndToEndSignalingWebRTC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s := server.NewServer()
	defer s.Close()
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	target, err := clientsignaling.Dial(ctx, wsURL(ts.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close(context.Background())

	operator, err := clientsignaling.Dial(ctx, wsURL(ts.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close(context.Background())

	code, _, err := target.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}

	targetMsgs := make(chan server.Message, 32)
	go func() {
		for {
			msg, err := target.Read(ctx)
			if err != nil {
				return
			}
			targetMsgs <- msg
		}
	}()

	operatorMsgs := make(chan server.Message, 32)
	go func() {
		for {
			msg, err := operator.Read(ctx)
			if err != nil {
				return
			}
			operatorMsgs <- msg
		}
	}()

	if err := operator.Join(ctx, code); err != nil {
		t.Fatal(err)
	}

	// The target's local user sees the operator join and explicitly approves.
	waitMsg(t, targetMsgs, "peer_joined")
	if err := target.Approve(ctx, true, "user accepted"); err != nil {
		t.Fatal(err)
	}
	waitMsg(t, operatorMsgs, "approved")

	var opPeer *webrtcpeer.Peer
	var targetPeer *webrtcpeer.Peer
	var operatorCandidates, targetCandidates atomic.Int32

	opPeer, err = webrtcpeer.New(true, func(c context.Context, sig webrtcpeer.Signal) error {
		return operator.Signal(c, sig.Kind, sig.Payload)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer opPeer.Close()

	targetPeer, err = webrtcpeer.New(true, func(c context.Context, sig webrtcpeer.Signal) error {
		return target.Signal(c, sig.Kind, sig.Payload)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer targetPeer.Close()

	pong := make(chan struct{}, 1)
	targetPeer.SetControlHandler(func(msg string) {
		if msg == "PING" {
			_ = targetPeer.SendControlText("PONG")
		}
	})
	opPeer.SetControlHandler(func(msg string) {
		if msg == "PONG" {
			select {
			case pong <- struct{}{}:
			default:
			}
		}
	})

	if err := opPeer.CreateControl(); err != nil {
		t.Fatal(err)
	}

	// The signaling readers consume the offer/candidate messages and hand them
	// to the corresponding Pion peer.
	targetDone := make(chan struct{})
	go func() {
		defer close(targetDone)
		for {
			select {
			case msg := <-targetMsgs:
				if msg.Type != "signal" {
					continue
				}
				sig := webrtcpeer.Signal{Kind: msg.Kind, Payload: msg.Payload}
				switch sig.Kind {
				case "candidate":
					targetCandidates.Add(1)
					if err := targetPeer.AddSignal(sig); err != nil {
						t.Errorf("target candidate: %v", err)
						return
					}
				case "offer":
					answer, err := targetPeer.AcceptOffer(ctx, sig)
					if err != nil {
						t.Errorf("accept offer: %v", err)
						return
					}
					if err := target.Signal(ctx, answer.Kind, answer.Payload); err != nil {
						t.Errorf("send answer: %v", err)
						return
					}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	opDone := make(chan struct{})
	go func() {
		defer close(opDone)
		for {
			select {
			case msg := <-operatorMsgs:
				if msg.Type != "signal" {
					continue
				}
				sig := webrtcpeer.Signal{Kind: msg.Kind, Payload: msg.Payload}
				if sig.Kind == "candidate" {
					operatorCandidates.Add(1)
				}
				if err := opPeer.AddSignal(sig); err != nil {
					t.Errorf("operator signal %s: %v", sig.Kind, err)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	offer, err := opPeer.Offer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := operator.Signal(ctx, offer.Kind, offer.Payload); err != nil {
		t.Fatal(err)
	}

	connectedCtx, connectedCancel := context.WithTimeout(ctx, 10*time.Second)
	defer connectedCancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if opPeer.ConnectionState().String() == "connected" &&
			targetPeer.ConnectionState().String() == "connected" {
			break
		}
		select {
		case <-ticker.C:
		case <-connectedCtx.Done():
			t.Fatalf("WebRTC did not connect: operator=%s target=%s",
				opPeer.ConnectionState(), targetPeer.ConnectionState())
		}
	}
	if operatorCandidates.Load() == 0 || targetCandidates.Load() == 0 {
		t.Fatalf("ICE candidates were not exchanged through signaling: operator=%d target=%d", operatorCandidates.Load(), targetCandidates.Load())
	}

	if err := opPeer.SendPing(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-pong:
	case <-ctx.Done():
		t.Fatal("timed out waiting for PONG")
	}

	if err := operator.Signal(ctx, "close", nil); err != nil {
		t.Fatal(err)
	}

	targetPeer.Close()
	opPeer.Close()
	<-targetDone
	<-opDone
}
