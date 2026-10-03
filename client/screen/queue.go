package screen

import "sync"

// LatestQueue is a bounded latest-frame-wins queue.
// A producer never accumulates an unbounded backlog when the consumer is slow.
type LatestQueue struct {
	mu     sync.Mutex
	frame  []byte
	closed bool
	wake   chan struct{}
}

func NewLatestQueue() *LatestQueue {
	return &LatestQueue{wake: make(chan struct{}, 1)}
}

func (q *LatestQueue) Put(frame []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ErrClosed
	}
	q.frame = append(q.frame[:0], frame...)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return nil
}

func (q *LatestQueue) Get() ([]byte, error) {
	for {
		q.mu.Lock()
		if len(q.frame) != 0 {
			out := append([]byte(nil), q.frame...)
			q.frame = nil
			q.mu.Unlock()
			return out, nil
		}
		if q.closed {
			q.mu.Unlock()
			return nil, ErrClosed
		}
		q.mu.Unlock()
		<-q.wake
	}
}

// Empty reports whether no frame is waiting to be sent.
func (q *LatestQueue) Empty() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.frame) == 0
}

// TryGet returns the pending frame without blocking.
func (q *LatestQueue) TryGet() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.frame) == 0 {
		return nil, false
	}
	out := append([]byte(nil), q.frame...)
	q.frame = nil
	return out, true
}

func (q *LatestQueue) Close() {
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.wake)
	}
	q.mu.Unlock()
}

var ErrClosed = &closedError{}

type closedError struct{}

func (*closedError) Error() string { return "screen: queue closed" }
