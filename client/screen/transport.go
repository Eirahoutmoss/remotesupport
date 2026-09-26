package screen

import (
	"errors"
	"sync"

	"github.com/pion/webrtc/v4"
)

type Handler func(Frame)

type Transport struct {
	dc      *webrtc.DataChannel
	queue   *LatestQueue
	handler Handler
	done    chan struct{}
	once    sync.Once
}

func NewTransport(dc *webrtc.DataChannel) *Transport {
	t := &Transport{
		dc:    dc,
		queue: NewLatestQueue(),
		done:  make(chan struct{}),
	}
	go t.writeLoop()
	return t
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
		if err := t.dc.Send(wire); err != nil && !errors.Is(err, webrtc.ErrDataChannelNotOpen) {
			return
		}
	}
}
