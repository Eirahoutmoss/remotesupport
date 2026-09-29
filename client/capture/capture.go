// Package capture provides platform-specific desktop capture primitives.
package capture

import (
	"errors"
	"image"
)

var ErrUnsupported = errors.New("capture: unsupported platform")

// Monitor describes a display in virtual-screen coordinates.
type Monitor struct {
	Index   int
	X, Y    int
	Width   int
	Height  int
	Primary bool
}

// Frame is one encoded desktop frame.
type Frame struct {
	Width  int
	Height int
	JPEG   []byte
}

// ListMonitors returns the currently attached displays.
func ListMonitors() ([]Monitor, error) { return listMonitors() }

// CaptureMonitor captures one display. quality is JPEG quality [1,100].
func CaptureMonitor(index int, quality int) (Frame, error) { return captureMonitor(index, quality) }

// CaptureMonitorScaled captures one display and optionally limits its output height.
func CaptureMonitorScaled(index int, quality int, maxHeight int) (Frame, error) {
	return captureMonitorScaled(index, quality, maxHeight)
}

// Capture captures the selected monitor at the requested JPEG quality.
func Capture(index int, quality int) (Frame, error) { return CaptureMonitor(index, quality) }

// ValidateQuality keeps callers from accidentally requesting pathological JPEG settings.
func ValidateQuality(q int) int {
	if q < 1 {
		return 1
	}
	if q > 100 {
		return 100
	}
	return q
}

// Bounds returns an image rectangle for a monitor.
func (m Monitor) Bounds() image.Rectangle {
	return image.Rect(m.X, m.Y, m.X+m.Width, m.Y+m.Height)
}
