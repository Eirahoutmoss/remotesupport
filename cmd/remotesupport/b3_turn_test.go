//go:build windows

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/eirahoutmoss/remotesupport/client/screen"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/pion/webrtc/v4"
)

// Relay-only connection through the built-in TURN server (opt-in: needs the
// company server). NEXDESK_TURNTEST=1 go test -run TurnRelay ./cmd/remotesupport
func TestTurnRelay(t *testing.T) {
	if os.Getenv("NEXDESK_TURNTEST") == "" {
		t.Skip("set NEXDESK_TURNTEST=1")
	}
	cfg := webrtc.Configuration{ICEServers: iceServers(), ICETransportPolicy: webrtc.ICETransportPolicyRelay}
	op, err := webrtcpeer.NewWithConfig(true, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Close()
	ag, err := webrtcpeer.NewWithConfig(true, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	got := make(chan string, 1)
	ag.SetControlHandler(func(m string) { got <- m })
	if err := op.CreateControl(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	offer, err := op.Offer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := ag.AcceptOffer(ctx, offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := op.AddSignal(answer); err != nil {
		t.Fatal(err)
	}
	for ctx.Err() == nil {
		if op.ConnectionState() == webrtc.PeerConnectionStateConnected {
			_ = op.SendControlText("RELAY-OK")
			select {
			case m := <-got:
				t.Logf("relay ok: %s via %s", m, op.SelectedPair())
				return
			case <-time.After(300 * time.Millisecond):
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no relay connection (state %s)", op.ConnectionState())
}

// Screen-channel throughput over the TURN relay (opt-in, needs the server).
func TestTurnThroughput(t *testing.T) {
	if os.Getenv("NEXDESK_TURNTEST") == "" {
		t.Skip("set NEXDESK_TURNTEST=1")
	}
	ice := iceServers()
	if v := os.Getenv("NEXDESK_TURNURL"); v != "" {
		ice[len(ice)-1].URLs = []string{v}
	}
	cfg := webrtc.Configuration{ICEServers: ice, ICETransportPolicy: webrtc.ICETransportPolicyRelay}
	op, _ := webrtcpeer.NewWithSettings(true, cfg, peerSettings(), nil)
	defer op.Close()
	ag, _ := webrtcpeer.NewWithSettings(true, cfg, peerSettings(), nil)
	defer ag.Close()
	var got, bytesGot int64
	op.SetScreenHandler(func(f screen.Frame) { got++; bytesGot += int64(len(f.JPEG)) })
	if err := op.CreateScreen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	offer, _ := op.Offer(ctx)
	answer, err := ag.AcceptOffer(ctx, offer)
	if err != nil {
		t.Fatal(err)
	}
	_ = op.AddSignal(answer)
	if err := ag.WaitScreen(ctx); err != nil {
		t.Fatal(err)
	}
	for op.ConnectionState() != webrtc.PeerConnectionStateConnected && ctx.Err() == nil {
		time.Sleep(50 * time.Millisecond)
	}
	jpg := append([]byte{0xff, 0xd8}, make([]byte, 60<<10)...) // ~60 KB, a 720p keyframe
	jpg = append(jpg, 0xff, 0xd9)
	start := time.Now()
	var seq uint64
	for time.Since(start) < 6*time.Second {
		if ag.ScreenIdle() {
			seq++
			_ = ag.SendScreenFrame(screen.Frame{Seq: seq, Width: 1280, Height: 720, JPEG: jpg})
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(time.Second)
	sec := time.Since(start).Seconds()
	t.Logf("relay: %d frames sent, %d received, %.1f fps of 60 KB, %.2f MB/s", seq, got, float64(got)/sec, float64(bytesGot)/sec/1e6)
}
