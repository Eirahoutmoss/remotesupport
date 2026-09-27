package screen

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestChunkRoundTrip(t *testing.T) {
	wire := make([]byte, 150000)
	copy(wire[:4], magic)
	wire[4] = version
	binary.BigEndian.PutUint64(wire[7:15], 42)
	binary.BigEndian.PutUint32(wire[15:19], 1920)
	binary.BigEndian.PutUint32(wire[19:23], 1080)

	chunks, err := chunkFrame(wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > maxChunkSize {
			t.Fatalf("chunk is %d bytes, exceeds %d", len(chunk), maxChunkSize)
		}
	}

	// Feed the chunks through the same reassembly logic used by Transport.
	tr := &Transport{}
	var got []byte
	for _, chunk := range chunks {
		var ok bool
		got, ok = tr.acceptChunk(chunk)
		if len(got) == 0 && ok {
			t.Fatal("empty completed frame")
		}
	}
	if !bytes.Equal(got, wire) {
		t.Fatalf("reassembled frame differs: got %d bytes, want %d", len(got), len(wire))
	}
}
