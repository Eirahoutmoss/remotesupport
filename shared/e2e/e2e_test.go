package e2e

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// pipe is an in-memory ordered message transport. hook, if set, may rewrite
// or duplicate outgoing messages to simulate a hostile network.
type pipe struct {
	in, out chan []byte
	hook    func(b []byte) [][]byte
}

func newPipes() (*pipe, *pipe) {
	a, b := make(chan []byte, 16), make(chan []byte, 16)
	return &pipe{in: a, out: b}, &pipe{in: b, out: a}
}

func (p *pipe) ReadMsg(ctx context.Context) ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *pipe) WriteMsg(ctx context.Context, b []byte) error {
	msgs := [][]byte{append([]byte(nil), b...)}
	if p.hook != nil {
		msgs = p.hook(msgs[0])
	}
	for _, m := range msgs {
		select {
		case p.out <- m:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

// pair runs both handshakes concurrently.
func pair(t *testing.T, ta, to Transport, codeA, codeO string) (*Channel, *Channel) {
	t.Helper()
	var a, o *Channel
	var errA, errO error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a, errA = Handshake(ctx(t), ta, RoleAgent, codeA) }()
	go func() { defer wg.Done(); o, errO = Handshake(ctx(t), to, RoleOperator, codeO) }()
	wg.Wait()
	if errA != nil || errO != nil {
		t.Fatalf("handshake: agent=%v operator=%v", errA, errO)
	}
	return a, o
}

func TestRoundTripBothDirections(t *testing.T) {
	pa, po := newPipes()
	a, o := pair(t, pa, po, "482913775", "482913775")
	if a.SAS != o.SAS || len(a.SAS) != 7 {
		t.Fatalf("SAS mismatch: %q vs %q", a.SAS, o.SAS)
	}
	for i, m := range [][]byte{[]byte("hello"), {}, bytes.Repeat([]byte{7}, 1<<20)} {
		if err := a.Send(ctx(t), m); err != nil {
			t.Fatal(err)
		}
		got, err := o.Recv(ctx(t))
		if err != nil || !bytes.Equal(got, m) {
			t.Fatalf("a->o #%d: err=%v", i, err)
		}
		if err := o.Send(ctx(t), m); err != nil {
			t.Fatal(err)
		}
		got, err = a.Recv(ctx(t))
		if err != nil || !bytes.Equal(got, m) {
			t.Fatalf("o->a #%d: err=%v", i, err)
		}
	}
}

func TestCiphertextHidesPlaintext(t *testing.T) {
	pa, po := newPipes()
	var seen [][]byte
	pa.hook = func(b []byte) [][]byte { seen = append(seen, b); return [][]byte{b} }
	a, o := pair(t, pa, po, "1", "1")
	secret := []byte("secret screen content")
	a.Send(ctx(t), secret)
	o.Recv(ctx(t))
	if bytes.Contains(seen[len(seen)-1], secret) {
		t.Fatal("plaintext visible on the wire")
	}
}

func TestWrongKeyRejected(t *testing.T) {
	pa, po := newPipes()
	a, o := pair(t, pa, po, "111111111", "222222222")
	if a.SAS == o.SAS {
		t.Log("SAS collided by chance (1e-6); keys still differ")
	}
	a.Send(ctx(t), []byte("x"))
	if _, err := o.Recv(ctx(t)); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestTamperedCiphertextRejected(t *testing.T) {
	pa, po := newPipes()
	a, o := pair(t, pa, po, "1", "1")
	pa.hook = func(b []byte) [][]byte { b[len(b)-1] ^= 1; return [][]byte{b} }
	a.Send(ctx(t), []byte("click"))
	if _, err := o.Recv(ctx(t)); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
	// Channel stays dead even if a valid message follows.
	pa.hook = nil
	a.Send(ctx(t), []byte("next"))
	if _, err := o.Recv(ctx(t)); !errors.Is(err, ErrAuth) {
		t.Fatalf("channel not poisoned: %v", err)
	}
}

func TestReplayRejected(t *testing.T) {
	pa, po := newPipes()
	a, o := pair(t, pa, po, "1", "1")
	pa.hook = func(b []byte) [][]byte { return [][]byte{b, b} } // duplicate
	a.Send(ctx(t), []byte("keydown"))
	if _, err := o.Recv(ctx(t)); err != nil {
		t.Fatalf("first copy: %v", err)
	}
	if _, err := o.Recv(ctx(t)); !errors.Is(err, ErrAuth) {
		t.Fatalf("replay accepted: %v", err)
	}
}

func TestReorderRejected(t *testing.T) {
	pa, po := newPipes()
	a, o := pair(t, pa, po, "1", "1")
	var held []byte
	pa.hook = func(b []byte) [][]byte {
		if held == nil {
			held = b
			return nil
		}
		return [][]byte{b, held}
	}
	a.Send(ctx(t), []byte("1"))
	a.Send(ctx(t), []byte("2"))
	if _, err := o.Recv(ctx(t)); !errors.Is(err, ErrAuth) {
		t.Fatalf("reordered message accepted: %v", err)
	}
}

// A relay that runs its own handshake with each side can read traffic, but
// the two SAS values then differ. This is why users compare the SAS; it is a
// UX check, not a substitute for authentication.
func TestMITMChangesSAS(t *testing.T) {
	pa, ma := newPipes() // agent <-> mitm
	mo, po := newPipes() // mitm <-> operator
	var a, o, mA, mO *Channel
	var wg sync.WaitGroup
	wg.Add(4)
	go func() { defer wg.Done(); a, _ = Handshake(ctx(t), pa, RoleAgent, "1") }()
	go func() { defer wg.Done(); mA, _ = Handshake(ctx(t), ma, RoleOperator, "1") }()
	go func() { defer wg.Done(); mO, _ = Handshake(ctx(t), mo, RoleAgent, "1") }()
	go func() { defer wg.Done(); o, _ = Handshake(ctx(t), po, RoleOperator, "1") }()
	wg.Wait()
	if a == nil || o == nil || mA == nil || mO == nil {
		t.Fatal("handshake failed")
	}
	if a.SAS == o.SAS {
		t.Fatal("MITM not visible in SAS")
	}
}

func TestHandshakeRejectsBadHello(t *testing.T) {
	cases := map[string][]byte{
		"short":     []byte("RS1O"),
		"magic":     append([]byte("XX1O"), make([]byte, 32)...),
		"same role": append([]byte("RS1A"), make([]byte, 32)...),
		"bad role":  append([]byte("RS1Z"), make([]byte, 32)...),
	}
	for name, hello := range cases {
		pa, po := newPipes()
		go po.WriteMsg(ctx(t), hello)
		go po.ReadMsg(ctx(t))
		if _, err := Handshake(ctx(t), pa, RoleAgent, "1"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
