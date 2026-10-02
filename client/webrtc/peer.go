package webrtcpeer

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eirahoutmoss/remotesupport/client/screen"
	"github.com/pion/webrtc/v4"
)

type Signal struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type Peer struct {
	pc          *webrtc.PeerConnection
	mu          sync.Mutex
	control     *webrtc.DataChannel
	file        *webrtc.DataChannel
	screen      *screen.Transport
	sendSig     SignalFunc
	pending     []webrtc.ICECandidateInit
	onMessage   func(string)
	onFile      func([]byte)
	onScreen    screen.Handler
	screenReady chan struct{}
	screenOnce  sync.Once
	approved    bool
}

type SignalFunc func(context.Context, Signal) error

func New(approved bool, send SignalFunc) (*Peer, error) {
	// Public STUN servers let ICE discover each peer's public address and
	// hole-punch a DIRECT path across the internet without any relay (TURN).
	// Symmetric NAT / strict firewalls may still require a TURN server.
	cfg := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302", "stun:stun1.l.google.com:19302"}},
		},
	}
	return NewWithConfig(approved, cfg, send)
}

func NewWithConfig(approved bool, config webrtc.Configuration, send SignalFunc) (*Peer, error) {
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}
	return newFromPC(pc, approved, send), nil
}

// NewWithSettings is NewWithConfig with a custom SettingEngine (e.g. fixed ICE
// credentials for the compact serverless invite codes).
func NewWithSettings(approved bool, config webrtc.Configuration, se webrtc.SettingEngine, send SignalFunc) (*Peer, error) {
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se)).NewPeerConnection(config)
	if err != nil {
		return nil, err
	}
	return newFromPC(pc, approved, send), nil
}

func newFromPC(pc *webrtc.PeerConnection, approved bool, send SignalFunc) *Peer {
	p := &Peer{pc: pc, sendSig: send, approved: approved, screenReady: make(chan struct{})}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil || send == nil {
			return
		}
		b, err := json.Marshal(c.ToJSON())
		if err != nil {
			return
		}
		_ = send(context.Background(), Signal{Kind: "candidate", Payload: b})
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case "ctl":
			p.mu.Lock()
			p.control = dc
			h := p.onMessage
			p.mu.Unlock()
			if h != nil {
				dc.OnMessage(func(m webrtc.DataChannelMessage) {
					if m.IsString {
						h(string(m.Data))
					}
				})
			}
		case "file":
			p.mu.Lock()
			p.file = dc
			h := p.onFile
			p.mu.Unlock()
			if h != nil {
				dc.OnMessage(func(m webrtc.DataChannelMessage) { h(m.Data) })
			}
		case "screen":
			p.mu.Lock()
			if p.screen != nil {
				p.mu.Unlock()
				_ = dc.Close()
				return
			}
			t := screen.NewTransport(dc)
			p.screen = t
			h := p.onScreen
			p.mu.Unlock()
			t.SetHandler(h)
			p.screenOnce.Do(func() { close(p.screenReady) })
		default:
			_ = dc.Close()
		}
	})
	return p
}

func (p *Peer) ConnectionState() webrtc.PeerConnectionState {
	return p.pc.ConnectionState()
}

// ICEState reports the ICE connection state (diagnostics).
func (p *Peer) ICEState() webrtc.ICEConnectionState {
	return p.pc.ICEConnectionState()
}

// SelectedPair describes the nominated candidate pair, or "" (diagnostics).
func (p *Peer) SelectedPair() string {
	sctp := p.pc.SCTP()
	if sctp == nil || sctp.Transport() == nil || sctp.Transport().ICETransport() == nil {
		return ""
	}
	pair, err := sctp.Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || pair == nil || pair.Local == nil || pair.Remote == nil {
		return ""
	}
	return fmt.Sprintf("%s %s:%d ⇄ %s %s:%d", pair.Local.Typ, pair.Local.Address, pair.Local.Port, pair.Remote.Typ, pair.Remote.Address, pair.Remote.Port)
}

// PathKind reports how media is flowing over the selected ICE candidate pair:
// "relay" when a TURN relay candidate is in use on either end, "direct"
// otherwise, or "" when no pair is selected yet. Diagnostics only.
func (p *Peer) PathKind() string {
	sctp := p.pc.SCTP()
	if sctp == nil {
		return ""
	}
	dtls := sctp.Transport()
	if dtls == nil {
		return ""
	}
	ice := dtls.ICETransport()
	if ice == nil {
		return ""
	}
	pair, err := ice.GetSelectedCandidatePair()
	if err != nil || pair == nil || pair.Local == nil || pair.Remote == nil {
		return ""
	}
	if pair.Local.Typ == webrtc.ICECandidateTypeRelay || pair.Remote.Typ == webrtc.ICECandidateTypeRelay {
		return "relay"
	}
	return "direct"
}

// FingerprintSAS derives a 6-digit short authentication string from the DTLS
// certificate fingerprints exchanged in the SDP, salted with the connection
// code. Both peers sort the two fingerprints before hashing, so an honest
// exchange yields the same number on each end; a signaling MITM that swaps a
// fingerprint to intercept DTLS changes it, so comparing the number out of band
// detects that attack. It authenticates the DTLS session — not an app-layer key
// — and must be verified end to end before being relied on for security.
func (p *Peer) FingerprintSAS(code string) (string, bool) {
	l := p.pc.LocalDescription()
	r := p.pc.RemoteDescription()
	if l == nil || r == nil {
		return "", false
	}
	lf := extractFingerprint(l.SDP)
	rf := extractFingerprint(r.SDP)
	if lf == "" || rf == "" {
		return "", false
	}
	fps := []string{strings.ToLower(lf), strings.ToLower(rf)}
	sort.Strings(fps) // order-independent, so both ends compute the same value
	h := sha256.New()
	h.Write([]byte("remotesupport-sas/v1|"))
	h.Write([]byte(strings.TrimSpace(code)))
	h.Write([]byte{'|'})
	h.Write([]byte(fps[0]))
	h.Write([]byte{'|'})
	h.Write([]byte(fps[1]))
	sum := h.Sum(nil)
	n := binary.BigEndian.Uint32(sum[:4]) % 1_000_000
	return fmt.Sprintf("%03d %03d", n/1000, n%1000), true
}

func extractFingerprint(sdp string) string {
	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "a=fingerprint:") {
			return strings.TrimPrefix(line, "a=fingerprint:")
		}
	}
	return ""
}

func (p *Peer) CreateControl() error {
	if !p.approved {
		return errors.New("webrtc: local approval required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.control != nil {
		return errors.New("webrtc: control channel already exists")
	}
	dc, err := p.pc.CreateDataChannel("ctl", nil)
	if err != nil {
		return err
	}
	p.control = dc
	if p.onMessage != nil {
		h := p.onMessage
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if m.IsString {
				h(string(m.Data))
			}
		})
	}
	return nil
}

func (p *Peer) CreateFile() error {
	if !p.approved {
		return errors.New("webrtc: local approval required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file != nil {
		return errors.New("webrtc: file channel already exists")
	}
	dc, err := p.pc.CreateDataChannel("file", nil)
	if err != nil {
		return err
	}
	p.file = dc
	if p.onFile != nil {
		h := p.onFile
		dc.OnMessage(func(m webrtc.DataChannelMessage) { h(m.Data) })
	}
	return nil
}

func (p *Peer) SetFileHandler(fn func([]byte)) {
	p.mu.Lock()
	p.onFile = fn
	dc := p.file
	p.mu.Unlock()
	if dc != nil && fn != nil {
		dc.OnMessage(func(m webrtc.DataChannelMessage) { fn(m.Data) })
	}
}

func (p *Peer) FileReady() bool {
	p.mu.Lock()
	dc := p.file
	p.mu.Unlock()
	return dc != nil && dc.ReadyState() == webrtc.DataChannelStateOpen
}

func (p *Peer) SendFileData(data []byte) error {
	p.mu.Lock()
	dc := p.file
	p.mu.Unlock()
	if dc == nil {
		return errors.New("webrtc: file channel not open")
	}
	if dc.ReadyState() != webrtc.DataChannelStateOpen {
		return errors.New("webrtc: file channel is not ready")
	}
	return dc.Send(data)
}

func (p *Peer) FileBufferedAmount() uint64 {
	p.mu.Lock()
	dc := p.file
	p.mu.Unlock()
	if dc == nil {
		return 0
	}
	return dc.BufferedAmount()
}

func (p *Peer) CreateScreen() error {
	if !p.approved {
		return errors.New("webrtc: local approval required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.screen != nil {
		return errors.New("webrtc: screen channel already exists")
	}
	dc, err := p.pc.CreateDataChannel("screen", nil)
	if err != nil {
		return err
	}
	t := screen.NewTransport(dc)
	p.screen = t
	t.SetHandler(p.onScreen)
	p.screenOnce.Do(func() { close(p.screenReady) })
	return nil
}

// WaitScreen blocks until the screen transport exists on this peer.
// The offerer creates the data channel; the answerer receives it via OnDataChannel.
func (p *Peer) WaitScreen(ctx context.Context) error {
	p.mu.Lock()
	ready := p.screenReady
	readyNow := p.screen != nil
	p.mu.Unlock()
	if readyNow {
		return nil
	}
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Peer) SendScreenFrame(f screen.Frame) error {
	p.mu.Lock()
	t := p.screen
	p.mu.Unlock()
	if t == nil {
		return errors.New("webrtc: screen channel not open")
	}
	return t.Send(f)
}

// ScreenIdle reports whether the screen channel can take the next frame.
func (p *Peer) ScreenIdle() bool {
	p.mu.Lock()
	t := p.screen
	p.mu.Unlock()
	return t != nil && t.Idle()
}

func (p *Peer) SetScreenHandler(fn screen.Handler) {
	p.mu.Lock()
	p.onScreen = fn
	t := p.screen
	p.mu.Unlock()
	if t != nil {
		t.SetHandler(fn)
	}
}

func (p *Peer) Offer(ctx context.Context) (Signal, error) {
	if !p.approved {
		return Signal{}, errors.New("webrtc: local approval required")
	}
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return Signal{}, err
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return Signal{}, err
	}
	if err := waitICE(ctx, p.pc); err != nil {
		return Signal{}, err
	}
	ld := p.pc.LocalDescription()
	if ld == nil {
		return Signal{}, errors.New("webrtc: missing local description")
	}
	b, err := json.Marshal(*ld)
	if err != nil {
		return Signal{}, err
	}
	return Signal{Kind: "offer", Payload: b}, nil
}

func (p *Peer) AcceptOffer(ctx context.Context, sig Signal) (Signal, error) {
	if !p.approved {
		return Signal{}, errors.New("webrtc: local approval required")
	}
	var offer webrtc.SessionDescription
	if err := json.Unmarshal(sig.Payload, &offer); err != nil {
		return Signal{}, err
	}
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return Signal{}, err
	}
	if err := p.flushPendingCandidates(); err != nil {
		return Signal{}, err
	}
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return Signal{}, err
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return Signal{}, err
	}
	if err := waitICE(ctx, p.pc); err != nil {
		return Signal{}, err
	}
	ld := p.pc.LocalDescription()
	if ld == nil {
		return Signal{}, errors.New("webrtc: missing local description")
	}
	b, err := json.Marshal(*ld)
	if err != nil {
		return Signal{}, err
	}
	return Signal{Kind: "answer", Payload: b}, nil
}

func (p *Peer) AddSignal(sig Signal) error {
	switch sig.Kind {
	case "answer":
		var answer webrtc.SessionDescription
		if err := json.Unmarshal(sig.Payload, &answer); err != nil {
			return err
		}
		if err := p.pc.SetRemoteDescription(answer); err != nil {
			return err
		}
		return p.flushPendingCandidates()
	case "candidate":
		var c webrtc.ICECandidateInit
		if err := json.Unmarshal(sig.Payload, &c); err != nil {
			return err
		}
		if p.pc.RemoteDescription() == nil {
			p.mu.Lock()
			p.pending = append(p.pending, c)
			p.mu.Unlock()
			return nil
		}
		return p.pc.AddICECandidate(c)
	default:
		return fmt.Errorf("webrtc: unsupported signal %q", sig.Kind)
	}
}

func (p *Peer) flushPendingCandidates() error {
	p.mu.Lock()
	pending := append([]webrtc.ICECandidateInit(nil), p.pending...)
	p.pending = nil
	p.mu.Unlock()
	for _, c := range pending {
		if err := p.pc.AddICECandidate(c); err != nil {
			return err
		}
	}
	return nil
}

func (p *Peer) SendControlText(msg string) error {
	p.mu.Lock()
	dc := p.control
	p.mu.Unlock()
	if dc == nil {
		return errors.New("webrtc: control channel not open")
	}
	return dc.SendText(msg)
}

func (p *Peer) SendPing() error {
	return p.SendControlText("PING")
}

func (p *Peer) SetControlHandler(fn func(string)) {
	p.mu.Lock()
	p.onMessage = fn
	dc := p.control
	p.mu.Unlock()
	if dc != nil && fn != nil {
		dc.OnMessage(func(m webrtc.DataChannelMessage) {
			if m.IsString {
				fn(string(m.Data))
			}
		})
	}
}

func (p *Peer) Close() error {
	p.mu.Lock()
	t := p.screen
	p.screen = nil
	p.mu.Unlock()
	if t != nil {
		t.Close()
	}
	return p.pc.Close()
}

func waitICE(ctx context.Context, pc *webrtc.PeerConnection) error {
	if pc.ICEGatheringState() == webrtc.ICEGatheringStateComplete {
		return nil
	}
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if pc.ICEGatheringState() == webrtc.ICEGatheringStateComplete {
				return nil
			}
		}
	}
}
