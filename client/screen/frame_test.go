package screen

import (
	"bytes"
	"errors"
	"testing"
)

func testJPEG() []byte {
	return []byte{0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	want := Frame{Monitor: 2, Seq: 17, Width: 1920, Height: 1080, JPEG: testJPEG()}
	wire, err := EncodeFrame(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeFrame(wire)
	if err != nil {
		t.Fatal(err)
	}
	if got.Monitor != want.Monitor || got.Seq != want.Seq ||
		got.Width != want.Width || got.Height != want.Height ||
		!bytes.Equal(got.JPEG, want.JPEG) {
		t.Fatalf("decoded frame differs: got %+v want %+v", got, want)
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	cases := [][]byte{
		nil,
		make([]byte, headerSize-1),
		append([]byte("XXXX"), make([]byte, headerSize-4)...),
	}
	for _, data := range cases {
		if _, err := DecodeFrame(data); err == nil {
			t.Fatal("expected malformed frame rejection")
		}
	}
}

func TestDecodeRejectsLengthMismatch(t *testing.T) {
	wire, err := EncodeFrame(Frame{Width: 1, Height: 1, JPEG: testJPEG()})
	if err != nil {
		t.Fatal(err)
	}
	wire = wire[:len(wire)-1]
	if _, err := DecodeFrame(wire); err == nil {
		t.Fatal("expected length mismatch rejection")
	}
}

func TestDecodeRejectsOversizedPayload(t *testing.T) {
	wire := make([]byte, headerSize)
	copy(wire[:4], magic)
	wire[4] = version
	wire[15], wire[16], wire[17], wire[18] = 0, 0, 0, 1
	wire[19], wire[20], wire[21], wire[22] = 0, 0, 0, 1
	wire[23] = 0x00
	wire[24] = 0x20
	wire[25] = 0x00
	wire[26] = 0x01
	if _, err := DecodeFrame(wire); !errors.Is(err, ErrPayloadSize) {
		t.Fatalf("expected ErrPayloadSize, got %v", err)
	}
}

func TestLatestQueueReplacesStaleFrame(t *testing.T) {
	q := NewLatestQueue()
	defer q.Close()
	if err := q.Put([]byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := q.Put([]byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := q.Get()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("got %q, want new", got)
	}
}
