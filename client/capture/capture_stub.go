//go:build !windows

package capture

func listMonitors() ([]Monitor, error)                     { return nil, ErrUnsupported }
func captureMonitor(index int, quality int) (Frame, error) { return Frame{}, ErrUnsupported }
func captureMonitorScaled(index int, quality int, maxHeight int) (Frame, error) {
	return Frame{}, ErrUnsupported
}
