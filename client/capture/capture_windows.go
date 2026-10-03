//go:build windows

package capture

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
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
	procGetCursorInfo          = user32.NewProc("GetCursorInfo")
	procGetIconInfo            = user32.NewProc("GetIconInfo")
	procDrawIconEx             = user32.NewProc("DrawIconEx")
	procSetThreadDpiCtx        = user32.NewProc("SetThreadDpiAwarenessContext")
)

// DrawCursor paints the mouse pointer into captured frames (on by default) so
// the operator sees where the remote cursor is.
var DrawCursor atomic.Bool

// Grayscale drops colour from captured frames; used by adaptive quality on
// very poor links.
var Grayscale atomic.Bool

func init() { DrawCursor.Store(true) }

type cursorInfo struct {
	CbSize uint32
	Flags  uint32
	Cursor uintptr
	X, Y   int32
}

type iconInfo struct {
	FIcon    int32
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr
	HbmColor uintptr
}

// drawCursor overlays the current pointer onto memdc, whose origin is the
// monitor's top-left corner at (originX, originY) in virtual-screen space.
func drawCursor(memdc uintptr, originX, originY int) {
	ci := cursorInfo{CbSize: uint32(unsafe.Sizeof(cursorInfo{}))}
	if r, _, _ := procGetCursorInfo.Call(uintptr(unsafe.Pointer(&ci))); r == 0 || ci.Flags&1 == 0 || ci.Cursor == 0 {
		return // CURSOR_SHOWING not set
	}
	var ii iconInfo
	hx, hy := int32(0), int32(0)
	if r, _, _ := procGetIconInfo.Call(ci.Cursor, uintptr(unsafe.Pointer(&ii))); r != 0 {
		hx, hy = int32(ii.XHotspot), int32(ii.YHotspot)
		if ii.HbmMask != 0 {
			procDeleteObject.Call(ii.HbmMask)
		}
		if ii.HbmColor != 0 {
			procDeleteObject.Call(ii.HbmColor)
		}
	}
	x := ci.X - hx - int32(originX)
	y := ci.Y - hy - int32(originY)
	procDrawIconEx.Call(memdc, uintptr(x), uintptr(y), ci.Cursor, 0, 0, 0, 0, 0x0003) // DI_NORMAL
}

var captureMu sync.Mutex

// EnumDisplayMonitors needs a C callback. syscall/windows callbacks are NEVER
// freed and the process has a hard cap (~2000). listMonitors runs on every
// captured frame (~10 fps), so creating a fresh callback each call exhausted
// the cap in ~1-2 minutes and crashed the agent. Create it exactly once and
// reuse it; the enum result is passed through package state under enumMu.
var (
	enumMu       sync.Mutex
	enumMonitors []Monitor
	enumCbOnce   sync.Once
	enumCb       uintptr
)

func monitorEnumProc(hmon, hdc, r, lparam uintptr) uintptr {
	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	ret, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
	if ret == 0 {
		return 1
	}
	enumMonitors = append(enumMonitors, Monitor{
		Index:   len(enumMonitors),
		X:       int(mi.RcMonitor.Left),
		Y:       int(mi.RcMonitor.Top),
		Width:   int(mi.RcMonitor.Right - mi.RcMonitor.Left),
		Height:  int(mi.RcMonitor.Bottom - mi.RcMonitor.Top),
		Primary: mi.DwFlags&1 != 0,
	})
	return 1
}

func listMonitors() ([]Monitor, error) {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumCbOnce.Do(func() { enumCb = windows.NewCallback(monitorEnumProc) })
	enumMonitors = enumMonitors[:0]
	ret, _, err := procEnumDisplayMonitors.Call(0, 0, enumCb, 0)
	if ret == 0 {
		if err != windows.ERROR_SUCCESS {
			return nil, err
		}
		return nil, errors.New("capture: EnumDisplayMonitors failed")
	}
	return append([]Monitor(nil), enumMonitors...), nil
}

func captureMonitor(index int, quality int) (Frame, error) {
	return captureMonitorScaled(index, quality, 0)
}

func captureMonitorScaled(index int, quality int, maxHeight int) (Frame, error) {
	img, err := captureMonitorImage(index, maxHeight)
	if err != nil {
		return Frame{}, err
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: ValidateQuality(quality)}); err != nil {
		return Frame{}, err
	}
	return Frame{Width: img.Bounds().Dx(), Height: img.Bounds().Dy(), JPEG: out.Bytes()}, nil
}

// captureMonitorImage grabs one display (cursor drawn, optional grayscale)
// and downscales it to maxHeight when set.
func captureMonitorImage(index int, maxHeight int) (*image.RGBA, error) {
	captureMu.Lock()
	defer captureMu.Unlock()
	// NexDesk itself is DPI-unaware, so on a 125/150 % display Windows hands
	// it scaled (logical) monitor sizes and BitBlt grabbed only the top-left
	// part of the real screen. Capture in a per-monitor-aware thread context
	// to work in physical pixels.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if procSetThreadDpiCtx.Find() == nil {
		prev, _, _ := procSetThreadDpiCtx.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
		if prev != 0 {
			defer procSetThreadDpiCtx.Call(prev)
		}
	}

	monitors, err := listMonitors()
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(monitors) {
		return nil, fmt.Errorf("capture: monitor index %d out of range", index)
	}
	m := monitors[index]

	screen, _, err := procGetDC.Call(0)
	if screen == 0 {
		return nil, fmt.Errorf("capture: GetDC: %w", err)
	}
	defer procReleaseDC.Call(0, screen)

	memdc, _, err := procCreateCompatibleDC.Call(screen)
	if memdc == 0 {
		return nil, fmt.Errorf("capture: CreateCompatibleDC: %w", err)
	}
	defer procDeleteDC.Call(memdc)

	bmp, _, err := procCreateCompatibleBitmap.Call(screen, uintptr(m.Width), uintptr(m.Height))
	if bmp == 0 {
		return nil, fmt.Errorf("capture: CreateCompatibleBitmap: %w", err)
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(memdc, bmp)
	if old == 0 {
		return nil, errors.New("capture: SelectObject failed")
	}
	defer procSelectObject.Call(memdc, old)

	ok, _, err := procBitBlt.Call(memdc, 0, 0, uintptr(m.Width), uintptr(m.Height), screen, uintptr(int32(m.X)), uintptr(int32(m.Y)), srccopy)
	if ok == 0 {
		return nil, fmt.Errorf("capture: BitBlt: %w", err)
	}
	if DrawCursor.Load() {
		drawCursor(memdc, m.X, m.Y)
	}
	gray := Grayscale.Load()

	hdr := bitmapInfoHeader{BiSize: uint32(unsafe.Sizeof(bitmapInfoHeader{})), BiWidth: int32(m.Width), BiHeight: -int32(m.Height), BiPlanes: 1, BiBitCount: 32, BiCompression: biRGB}
	bmi := bitmapInfo{BmiHeader: hdr}
	raw := make([]byte, m.Width*m.Height*4)
	rows, _, err := procGetDIBits.Call(memdc, bmp, 0, uintptr(m.Height), uintptr(unsafe.Pointer(&raw[0])), uintptr(unsafe.Pointer(&bmi)), dibRGBColors)
	if rows == 0 {
		return nil, fmt.Errorf("capture: GetDIBits: %w", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, m.Width, m.Height))
	for y := 0; y < m.Height; y++ {
		src := raw[y*m.Width*4:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < m.Width; x++ {
			i := x * 4
			if gray {
				l := uint8((uint32(src[i+2])*77 + uint32(src[i+1])*150 + uint32(src[i+0])*29) >> 8)
				dst[i+0], dst[i+1], dst[i+2] = l, l, l
			} else {
				dst[i+0] = src[i+2]
				dst[i+1] = src[i+1]
				dst[i+2] = src[i+0]
			}
			dst[i+3] = 0xff
		}
	}

	if maxHeight > 0 && img.Bounds().Dy() > maxHeight {
		img = resizeBilinear(img, maxHeight)
	}
	return img, nil
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
	// Precompute horizontal taps once; fixed-point (8-bit) weights and direct
	// Pix indexing make this several times faster than RGBAAt/SetRGBA.
	x0s := make([]int, dstW)
	x1s := make([]int, dstW)
	fxs := make([]uint32, dstW)
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
		x0s[x], x1s[x], fxs[x] = x0*4, x1*4, uint32(fx*256)
	}
	sp, ss := src.Pix, src.Stride
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
		wy := uint32(fy * 256)
		r0, r1 := sp[y0*ss:], sp[y1*ss:]
		d := dst.Pix[y*dst.Stride:]
		for x := 0; x < dstW; x++ {
			a, b, wx := x0s[x], x1s[x], fxs[x]
			for c := 0; c < 3; c++ {
				top := uint32(r0[a+c])*(256-wx) + uint32(r0[b+c])*wx
				bot := uint32(r1[a+c])*(256-wx) + uint32(r1[b+c])*wx
				d[x*4+c] = uint8((top*(256-wy) + bot*wy + 32768) >> 16)
			}
			d[x*4+3] = 255
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
