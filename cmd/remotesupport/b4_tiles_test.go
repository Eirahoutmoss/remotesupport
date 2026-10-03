//go:build windows

package main

import (
	"image"
	"math/rand"
	"testing"

	"github.com/eirahoutmoss/remotesupport/client/screen"
)

func TestTileDeltaRoundTrip(t *testing.T) {
	const w, h = 1280, 720
	a := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	for i := range a.Pix {
		a.Pix[i] = byte(r.Intn(40) + 100) // mild texture, JPEG friendly
	}
	enc := &tileEncoder{}
	key, skip, err := enc.encode(a, 0, 80)
	if err != nil || skip {
		t.Fatal(err, skip)
	}
	if _, ok := parseTileMeta(key); ok {
		t.Fatal("first frame must be a keyframe")
	}
	viewer.mu.Lock()
	viewer.pixels = nil
	viewer.mu.Unlock()
	setViewerFrame(screen.Frame{Width: w, Height: h, JPEG: key})

	if _, skip, _ := enc.encode(a, 0, 80); !skip {
		t.Fatal("unchanged frame should be skipped")
	}

	b := image.NewRGBA(a.Rect)
	copy(b.Pix, a.Pix)
	for y := 100; y < 140; y++ { // a "typed text" sized change
		for x := 200; x < 330; x++ {
			o := y*b.Stride + x*4
			b.Pix[o], b.Pix[o+1], b.Pix[o+2] = 250, 10, 10
		}
	}
	delta, skip, err := enc.encode(b, 0, 80)
	if err != nil || skip {
		t.Fatal(err, skip)
	}
	tm, ok := parseTileMeta(delta)
	if !ok || len(tm.tiles) == 0 || len(tm.tiles) > 6 {
		t.Fatalf("expected a small delta, got ok=%v tiles=%d", ok, len(tm.tiles))
	}
	t.Logf("keyframe %d bytes, delta %d bytes (%d tiles)", len(key), len(delta), len(tm.tiles))
	setViewerFrame(screen.Frame{Width: w, Height: h, JPEG: delta})
	viewer.mu.RLock()
	px := viewer.pixels
	viewer.mu.RUnlock()
	o := (120*w + 250) * 4 // inside the changed area: BGRA, expect strong red
	if px[o+2] < 200 || px[o+1] > 60 {
		t.Fatalf("delta not applied: BGR=%d,%d,%d", px[o], px[o+1], px[o+2])
	}
	o = (500*w + 900) * 4 // untouched area stays near the original grey
	if px[o+2] < 80 || px[o+2] > 160 {
		t.Fatalf("untouched area changed: %d", px[o+2])
	}
}
