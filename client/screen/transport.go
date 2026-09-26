
package screen

import (
	"sync"

	"github.com/pion/webrtc/v4"
)

type Handler func(Frame)

type Transport struct {
	dc      *webrtc.DataChannel
	queue   *LatestQueue
	handler Handler
	ready   chan struct{}
	done    chan struct{}
	once    sync.Once
}

func NewTransport(dc *webrtc.DataChannel) *Transport {
	t := &Transport{
		dc:    dc,
		queue: NewLatestQueue(),
		ready: make(chan struct{}),
		done:  make(chan struct{}),
	}
	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		close(t.ready)
	} else {
		dc.OnOpen(func() {
			t.onceReady()
		})
	}
	go t.writeLoop()
	return t
}

func (t *Transport) onceReady() {
	select {
	case <-t.ready:
	default:
		close(t.ready)
	}
}

func (t *Transport) Send(f Frame) error {
	wire, err := EncodeFrame(f)
	if err != nil {
		return err
	}
	return t.queue.Put(wire)
}

func (t *Transport) SetHandler(fn Handler) {
	t.handler = fn
	t.dc.OnMessage(func(m webrtc.DataChannelMessage) {
		if m.IsString {
			return
		}
		frame, err := DecodeFrame(m.Data)
		if err != nil {
			return
		}
		if fn != nil {
			fn(frame)
		}
	})
}

func (t *Transport) Close() {
	t.once.Do(func() {
		close(t.done)
		t.queue.Close()
		_ = t.dc.Close()
	})
}

func (t *Transport) writeLoop() {
	select {
	case <-t.ready:
	case <-t.done:
		return
	}

	for {
		wire, err := t.queue.Get()
		if err != nil {
			return
		}
		select {
		case <-t.done:
			return
		default:
		}
		if err := t.dc.Send(wire); err != nil {
			return
		}
	}
}
