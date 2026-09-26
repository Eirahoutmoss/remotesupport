package webrtcpeer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

type Signal struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

type SignalFunc func(context.Context, Signal) error

type Peer struct {
	pc        *webrtc.PeerConnection
	mu        sync.Mutex
	control   *webrtc.DataChannel
	sendSig   SignalFunc
	pending   []webrtc.ICECandidateInit
	onMessage func(string)
	approved  bool
}

func New(approved bool, send SignalFunc) (*Peer, error) {
	return NewWithConfig(approved, webrtc.Configuration{}, send)
}

func NewWithConfig(approved bool, config webrtc.Configuration, send SignalFunc) (*Peer, error) {
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}
	p := &Peer{pc: pc, sendSig: send, approved: approved}

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
		if dc.Label() != "ctl" {
			_ = dc.Close()
			return
		}
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
	})
	return p, nil
}

func (p *Peer) ConnectionState() webrtc.PeerConnectionState {
	return p.pc.ConnectionState()
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

func (p *Peer) Close() error { return p.pc.Close() }

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
