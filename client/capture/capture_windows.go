//go:build windows

package capture

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	smxvirtualscreen  = 76
	smyvirtualscreen  = 77
	smcxvirtualscreen = 78
	smcyvirtualscreen = 79
	smcxscreen        = 0
	smcyscreen        = 1
	srccopy           = 0x00CC0020
	dibRGBColors      = 0
	biRGB             = 0
)

type rect struct{ Left, Top, Right, Bottom int32 }
type monitorInfo struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
}
type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}
type bitmapInfo struct {
	BmiHeader bitmapInfoHeader
	BmiColors [1]uint32
}

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	gdi32                      = windows.NewLazySystemDLL("gdi32.dll")
	procEnumDisplayMonitors    = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW        = user32.NewProc("GetMonitorInfoW")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

var captureMu sync.Mutex

func listMonitors() ([]Monitor, error) {
	var monitors []Monitor
	cb := windows.NewCallback(func(hmon, hdc uintptr, r uintptr, lparam uintptr) uintptr {
		var mi monitorInfo
		mi.CbSize = uint32(unsafe.Sizeof(mi))
		ret, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
		if ret == 0 {
			return 1
		}
		monitors = append(monitors, Monitor{
			Index:   len(monitors),
			X:       int(mi.RcMonitor.Left),
			Y:       int(mi.RcMonitor.Top),
			Width:   int(mi.RcMonitor.Right - mi.RcMonitor.Left),
			Height:  int(mi.RcMonitor.Bottom - mi.RcMonitor.Top),
			Primary: mi.DwFlags&1 != 0,
		})
		return 1
	})
	ret, _, err := procEnumDisplayMonitors.Call(0, 0, cb, 0)
	if ret == 0 {
		if err != windows.ERROR_SUCCESS {
			return nil, err
		}
		return nil, errors.New("capture: EnumDisplayMonitors failed")
	}
	return monitors, nil
}

func captureMonitor(index int, quality int) (Frame, error) {
	captureMu.Lock()
	defer captureMu.Unlock()
	quality = ValidateQuality(quality)

	monitors, err := listMonitors()
	if err != nil {
		return Frame{}, err
	}
	if index < 0 || index >= len(monitors) {
		return Frame{}, fmt.Errorf("capture: monitor index %d out of range", index)
	}
	m := monitors[index]
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	screen, _, err := procGetDC.Call(0)
	if screen == 0 {
		return Frame{}, fmt.Errorf("capture: GetDC: %w", err)
	}
	defer procReleaseDC.Call(0, screen)

	memdc, _, err := procCreateCompatibleDC.Call(screen)
	if memdc == 0 {
		return Frame{}, fmt.Errorf("capture: CreateCompatibleDC: %w", err)
	}
	defer procDeleteDC.Call(memdc)

	bmp, _, err := procCreateCompatibleBitmap.Call(screen, uintptr(m.Width), uintptr(m.Height))
	if bmp == 0 {
		return Frame{}, fmt.Errorf("capture: CreateCompatibleBitmap: %w", err)
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(memdc, bmp)
	if old == 0 {
		return Frame{}, errors.New("capture: SelectObject failed")
	}
	defer procSelectObject.Call(memdc, old)

	ok, _, err := procBitBlt.Call(memdc, 0, 0, uintptr(m.Width), uintptr(m.Height), screen, uintptr(int32(m.X)), uintptr(int32(m.Y)), srccopy)
	if ok == 0 {
		return Frame{}, fmt.Errorf("capture: BitBlt: %w", err)
	}

	hdr := bitmapInfoHeader{BiSize: uint32(unsafe.Sizeof(bitmapInfoHeader{})), BiWidth: int32(m.Width), BiHeight: -int32(m.Height), BiPlanes: 1, BiBitCount: 32, BiCompression: biRGB}
	bmi := bitmapInfo{BmiHeader: hdr}
	raw := make([]byte, m.Width*m.Height*4)
	rows, _, err := procGetDIBits.Call(memdc, bmp, 0, uintptr(m.Height), uintptr(unsafe.Pointer(&raw[0])), uintptr(unsafe.Pointer(&bmi)), dibRGBColors)
	if rows == 0 {
		return Frame{}, fmt.Errorf("capture: GetDIBits: %w", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, m.Width, m.Height))
	for y := 0; y < m.Height; y++ {
		src := raw[y*m.Width*4:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < m.Width; x++ {
			i := x * 4
			dst[i+0] = src[i+2]
			dst[i+1] = src[i+1]
			dst[i+2] = src[i+0]
			dst[i+3] = 0xff
		}
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		return Frame{}, err
	}
	return Frame{Width: m.Width, Height: m.Height, JPEG: out.Bytes()}, nil
}

var _ = procGetSystemMetrics
var _ = smxvirtualscreen
var _ = smyvirtualscreen
var _ = smcxvirtualscreen
var _ = smcyvirtualscreen
var _ = smcxscreen
var _ = smcyscreen
