//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/eirahoutmoss/remotesupport/client/capture"
)

func main() {
	monitors, err := capture.ListMonitors()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture-check: list monitors: %v\n", err)
		os.Exit(1)
	}
	if len(monitors) == 0 {
		fmt.Fprintln(os.Stderr, "capture-check: no monitors found")
		os.Exit(1)
	}

	outDir, err := os.MkdirTemp("", "remotesupport-capture-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture-check: create temp directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("monitors=%d\n", len(monitors))
	fmt.Printf("output=%s\n", outDir)

	for _, m := range monitors {
		fmt.Printf("monitor=%d x=%d y=%d width=%d height=%d primary=%t\n", m.Index, m.X, m.Y, m.Width, m.Height, m.Primary)
		frame, err := capture.CaptureMonitor(m.Index, 85)
		if err != nil {
			fmt.Fprintf(os.Stderr, "capture-check: monitor %d: %v\n", m.Index, err)
			os.Exit(1)
		}
		if frame.Width != m.Width || frame.Height != m.Height {
			fmt.Fprintf(os.Stderr, "capture-check: monitor %d: frame=%dx%d expected=%dx%d\n", m.Index, frame.Width, frame.Height, m.Width, m.Height)
			os.Exit(1)
		}
		if len(frame.JPEG) < 16 {
			fmt.Fprintf(os.Stderr, "capture-check: monitor %d: JPEG payload too small (%d bytes)\n", m.Index, len(frame.JPEG))
			os.Exit(1)
		}
		if frame.JPEG[0] != 0xff || frame.JPEG[1] != 0xd8 || frame.JPEG[len(frame.JPEG)-2] != 0xff || frame.JPEG[len(frame.JPEG)-1] != 0xd9 {
			fmt.Fprintf(os.Stderr, "capture-check: monitor %d: invalid JPEG markers\n", m.Index)
			os.Exit(1)
		}
		path := filepath.Join(outDir, fmt.Sprintf("monitor-%d.jpg", m.Index+1))
		if err := os.WriteFile(path, frame.JPEG, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "capture-check: monitor %d: write JPEG: %v\n", m.Index, err)
			os.Exit(1)
		}
		fmt.Printf("captured monitor=%d bytes=%d file=%s\n", m.Index, len(frame.JPEG), path)
	}

	fmt.Println("capture-check: PASS")
}
