package capture

import (
	"bytes"
	"image"
	"testing"
)

func TestValidateQuality(t *testing.T) {
	cases := []struct{ in, want int }{
		{-10, 1}, {0, 1}, {1, 1}, {75, 75}, {100, 100}, {120, 100},
	}
	for _, tc := range cases {
		if got := ValidateQuality(tc.in); got != tc.want {
			t.Fatalf("ValidateQuality(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestMonitorBounds(t *testing.T) {
	m := Monitor{X: -1920, Y: 10, Width: 1920, Height: 1080}
	want := image.Rect(-1920, 10, 0, 1090)
	if got := m.Bounds(); got != want {
		t.Fatalf("Bounds() = %v, want %v", got, want)
	}
}

func TestFrameBytesAreOwned(t *testing.T) {
	src := []byte{1, 2, 3}
	frame := Frame{Width: 1, Height: 1, JPEG: append([]byte(nil), src...)}
	src[0] = 9
	if bytes.Equal(frame.JPEG, src) {
		t.Fatal("Frame JPEG unexpectedly aliases caller buffer")
	}
}
