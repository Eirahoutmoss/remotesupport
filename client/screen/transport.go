package screen

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// DataChannels commonly impose a maximum message size around 64 KiB. Keep
// individual SCTP/DataChannel messages comfortably below that limit and
// reassemble larger screen frames at the application layer.
const (
	chunkMagic      = "RSC1"
	chunkHeaderSize = 20
	maxChunkSize    = 60 * 1024
)

type Handler func(Frame)

type Transport struct {
	dc      *webrtc.DataChannel
	queue   *LatestQueue
	handler Handler
	ready   chan struct{}
	done    chan struct{}
	once    sync.Once

	mu             sync.Mutex
	receiveSeq     uint64
	receiveChunks  uint16
	receiveTotal   uint32
	receiveData    []byte
	receiveNextIdx uint16
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
		wire, ok := t.acceptChunk(m.Data)
		if !ok {
			return
		}
		frame, err := DecodeFrame(wire)
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
		// Back-pressure: don't pile frames into the SCTP send buffer faster
		// than the link drains it. Over a slow/relayed path the buffer grew
		// without bound and the viewer fell minutes behind (looked frozen)
		// while input still worked. While we wait, LatestQueue keeps only the
		// newest frame, so stale frames are dropped instead of queued.
		for t.dc.BufferedAmount() > maxBufferedBytes {
			select {
			case <-t.done:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
		if next, ok := t.queue.TryGet(); ok {
			wire = next // a newer frame arrived while waiting
		}
		if err := t.sendFrameChunks(wire); err != nil {
			return
		}
	}
}

// Idle reports that the previous frame has been handed to the network and
// the send buffer is nearly drained. Delta (tile) frames depend on every
// earlier frame arriving, so the producer only emits a new frame when Idle —
// LatestQueue then never has to drop one.
func (t *Transport) Idle() bool {
	return t.queue.Empty() && t.dc.BufferedAmount() < idleBufferedBytes
}

const idleBufferedBytes = 256 << 10

// maxBufferedBytes caps unsent screen data per channel (~1 MiB).
const maxBufferedBytes = 1 << 20

func chunkFrame(wire []byte) ([][]byte, error) {
	if len(wire) == 0 {
		return nil, fmt.Errorf("screen: empty wire frame")
	}
	payloadSize := maxChunkSize - chunkHeaderSize
	if payloadSize <= 0 {
		return nil, fmt.Errorf("screen: invalid chunk size")
	}
	count := (len(wire) + payloadSize - 1) / payloadSize
	if count > 0xffff {
		return nil, fmt.Errorf("screen: frame requires too many chunks")
	}
	seq := uint64(0)
	if len(wire) >= 15 {
		seq = binary.BigEndian.Uint64(wire[7:15])
	}
	chunks := make([][]byte, 0, count)
	for i, off := 0, 0; off < len(wire); i, off = i+1, off+payloadSize {
		end := off + payloadSize
		if end > len(wire) {
			end = len(wire)
		}
		msg := make([]byte, chunkHeaderSize+end-off)
		copy(msg[:4], chunkMagic)
		binary.BigEndian.PutUint64(msg[4:12], seq)
		binary.BigEndian.PutUint16(msg[12:14], uint16(i))
		binary.BigEndian.PutUint16(msg[14:16], uint16(count))
		binary.BigEndian.PutUint32(msg[16:20], uint32(len(wire)))
		copy(msg[20:], wire[off:end])
		chunks = append(chunks, msg)
	}
	return chunks, nil
}

func (t *Transport) sendFrameChunks(wire []byte) error {
	chunks, err := chunkFrame(wire)
	if err != nil {
		return err
	}
	for _, msg := range chunks {
		if err := t.dc.Send(msg); err != nil {
			return err
		}
	}
	return nil
}

func (t *Transport) acceptChunk(msg []byte) ([]byte, bool) {
	if len(msg) < chunkHeaderSize || string(msg[:4]) != chunkMagic {
		return nil, false
	}
	seq := binary.BigEndian.Uint64(msg[4:12])
	idx := binary.BigEndian.Uint16(msg[12:14])
	count := binary.BigEndian.Uint16(msg[14:16])
	total := binary.BigEndian.Uint32(msg[16:20])
	payload := msg[20:]
	if count == 0 || idx >= count || total == 0 || len(payload) == 0 {
		return nil, false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// DataChannel is ordered/reliable by default. Start a new frame whenever
	// the sequence changes or a new frame starts at chunk zero.
	if seq != t.receiveSeq || (idx == 0 && t.receiveNextIdx != 0) {
		t.receiveSeq = seq
		t.receiveChunks = count
		t.receiveTotal = total
		t.receiveData = make([]byte, 0, total)
		t.receiveNextIdx = 0
	}
	if seq != t.receiveSeq || count != t.receiveChunks || total != t.receiveTotal || idx != t.receiveNextIdx {
		return nil, false
	}
	if uint32(len(t.receiveData)+len(payload)) > total {
		t.receiveData = nil
		t.receiveNextIdx = 0
		return nil, false
	}
	t.receiveData = append(t.receiveData, payload...)
	t.receiveNextIdx++
	if t.receiveNextIdx != count {
		return nil, false
	}
	if uint32(len(t.receiveData)) != total {
		t.receiveData = nil
		t.receiveNextIdx = 0
		return nil, false
	}
	out := append([]byte(nil), t.receiveData...)
	t.receiveData = nil
	t.receiveNextIdx = 0
	return out, true
}
