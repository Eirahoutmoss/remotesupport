//go:build windows

package capture

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
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
	return captureMonitorScaled(index, quality, 0)
}

func captureMonitorScaled(index int, quality int, maxHeight int) (Frame, error) {
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

	if maxHeight > 0 && img.Bounds().Dy() > maxHeight {
		img = resizeBilinear(img, maxHeight)
	}

	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
		return Frame{}, err
	}
	return Frame{Width: img.Bounds().Dx(), Height: img.Bounds().Dy(), JPEG: out.Bytes()}, nil
}

func resizeBilinear(src *image.RGBA, dstH int) *image.RGBA {
	srcW, srcH := src.Bounds().Dx(), src.Bounds().Dy()
	dstH = maxIntCapture(1, dstH)
	dstW := maxIntCapture(1, srcW*dstH/srcH)
	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	if dstW == srcW && dstH == srcH {
		copy(dst.Pix, src.Pix)
		return dst
	}
	for y := 0; y < dstH; y++ {
		sy := (float64(y)+0.5)*float64(srcH)/float64(dstH) - 0.5
		y0 := int(math.Floor(sy))
		fy := sy - float64(y0)
		if y0 < 0 {
			y0, fy = 0, 0
		}
		y1 := y0 + 1
		if y1 >= srcH {
			y1 = srcH - 1
		}
		for x := 0; x < dstW; x++ {
			sx := (float64(x)+0.5)*float64(srcW)/float64(dstW) - 0.5
			x0 := int(math.Floor(sx))
			fx := sx - float64(x0)
			if x0 < 0 {
				x0, fx = 0, 0
			}
			x1 := x0 + 1
			if x1 >= srcW {
				x1 = srcW - 1
			}
			c00 := src.RGBAAt(x0, y0)
			c10 := src.RGBAAt(x1, y0)
			c01 := src.RGBAAt(x0, y1)
			c11 := src.RGBAAt(x1, y1)
			lerp := func(a, b uint8, t float64) uint8 { return uint8(math.Round(float64(a)*(1-t) + float64(b)*t)) }
			r0 := lerp(c00.R, c10.R, fx)
			g0 := lerp(c00.G, c10.G, fx)
			b0 := lerp(c00.B, c10.B, fx)
			r1 := lerp(c01.R, c11.R, fx)
			g1 := lerp(c01.G, c11.G, fx)
			b1 := lerp(c01.B, c11.B, fx)
			dst.SetRGBA(x, y, color.RGBA{R: lerp(r0, r1, fy), G: lerp(g0, g1, fy), B: lerp(b0, b1, fy), A: 255})
		}
	}
	return dst
}

func maxIntCapture(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ = procGetSystemMetrics
var _ = smxvirtualscreen
var _ = smyvirtualscreen
var _ = smcxvirtualscreen
var _ = smcyvirtualscreen
var _ = smcxscreen
var _ = smcyscreen
