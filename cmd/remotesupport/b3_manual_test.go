//go:build windows

package main

import (
	"context"
	"os"
	"testing"
	"time"

	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/pion/webrtc/v4"
)

func TestManualBlobRoundTrip(t *testing.T) {
	in := []byte(`{"type":"offer","sdp":"v=0\r\no=- 1 2 IN IP4 0.0.0.0\r\na=candidate:1 1 udp 2130706431 192.168.1.5 50000 typ host\r\n"}`)
	blob := packBlob(manualOfferTag, in)
	// WhatsApp-style line wrapping and surrounding text must not matter.
	wrapped := "NexDesk davet kodu:\n" + blob[:20] + "\n" + blob[20:] + " "
	out, ok := unpackBlob(manualOfferTag, wrapped)
	if !ok || string(out) != string(in) {
		t.Fatalf("round trip failed: %v %q", ok, out)
	}
	if _, ok := unpackBlob(manualAnswerTag, blob); ok {
		t.Fatal("offer accepted as answer")
	}
}

func TestManualHandshakeConnects(t *testing.T) {
	opSeed, agSeed := newSeed(), newSeed()
	op, err := newManualPeerSeed(opSeed)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	ag, err := newManualPeerSeed(agSeed)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	got := make(chan string, 1)
	ag.SetControlHandler(func(m string) { got <- m })
	if err := op.CreateControl(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	offer, err := op.Offer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	offerCode, err := compactPack(false, opSeed, offer.Payload)
	if err != nil {
		t.Fatal(err)
	}
	offerIn, ok := compactUnpack(false, "Davet:\n"+offerCode+" ")
	if !ok {
		t.Fatal("offer code not decoded")
	}
	answer, err := ag.AcceptOffer(ctx, webrtcpeer.Signal{Kind: "offer", Payload: offerIn})
	if err != nil {
		t.Fatal(err)
	}
	answerCode, err := compactPack(true, agSeed, answer.Payload)
	if err != nil {
		t.Fatal(err)
	}
	answerIn, ok := compactUnpack(true, answerCode)
	if !ok {
		t.Fatal("answer code not decoded")
	}
	if os.Getenv("NEXDESK_SLOWTEST") != "" {
		time.Sleep(40 * time.Second) // reply pasted by hand much later
	}
	if err := op.AddSignal(webrtcpeer.Signal{Kind: "answer", Payload: answerIn}); err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		if op.ConnectionState() == webrtc.PeerConnectionStateConnected && ag.ConnectionState() == webrtc.PeerConnectionStateConnected {
			deadline := time.After(5 * time.Second)
			for {
				_ = op.SendControlText("PING-TEST")
				select {
				case m := <-got:
					if m != "PING-TEST" {
						t.Fatalf("got %q", m)
					}
				case <-time.After(200 * time.Millisecond):
					continue
				case <-deadline:
					t.Fatal("control message never arrived")
				}
				break
			}
			t.Logf("connected; invite %d chars, reply %d chars", len(offerCode), len(answerCode))
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("peers did not connect")
}
