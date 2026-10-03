//go:build !windows

package capture

import "image"

func listMonitors() ([]Monitor, error)                     { return nil, ErrUnsupported }
func captureMonitor(index int, quality int) (Frame, error) { return Frame{}, ErrUnsupported }
func captureMonitorScaled(index int, quality int, maxHeight int) (Frame, error) {
	return Frame{}, ErrUnsupported
}
func captureMonitorImage(index int, maxHeight int) (*image.RGBA, error) { return nil, ErrUnsupported }
