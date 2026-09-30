//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// curtainProc paints the agent privacy curtain (black + a short notice).
func curtainProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	if m == 0x000F { // WM_PAINT
		var ps paintStruct
		hdc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if hdc != 0 {
			var rc rect
			getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
			bb, _, _ := getStockObject.Call(4)
			fillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), bb)
			setBkMode.Call(hdc, 1)
			setTextColor.Call(hdc, rgb(0x8AA0B8))
			old, _, _ := selectObject.Call(hdc, uiSubtitleFont)
			wt := utf16ptr("Gizlilik modu etkin — ekranınız yalnızca yetkili destek görevlisine görünür")
			drawText.Call(hdc, uintptr(unsafe.Pointer(wt)), ^uintptr(0), uintptr(unsafe.Pointer(&rc)), uintptr(dtCenter|dtVCenter|dtSingleLine))
			selectObject.Call(hdc, old)
			endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

func ensureCurtain() {
	if curtainHwnd != 0 {
		return
	}
	class := utf16ptr("RSCurtain")
	bb, _, _ := getStockObject.Call(4)
	wc := wndclassex{CbSize: uint32(unsafe.Sizeof(wndclassex{})), LpfnWndProc: syscall.NewCallback(curtainProc), HInstance: moduleHandle(), HbrBackground: bb, LpszClassName: class}
	registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	x := systemMetric(76)
	y := systemMetric(77)
	w := systemMetric(78)
	h := systemMetric(79)
	hwnd, _, _ := createWindow.Call(0x00000008|0x00000080|0x08000000, uintptr(unsafe.Pointer(class)), 0, 0x80000000,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, moduleHandle(), 0)
	curtainHwnd = hwnd
	if hwnd != 0 {
		setWinDisplayAff.Call(hwnd, 0x00000011) // WDA_EXCLUDEFROMCAPTURE: hidden from screen capture
	}
}

func showCurtain() {
	ensureCurtain()
	if curtainHwnd != 0 {
		showWindow.Call(curtainHwnd, 8) // SW_SHOWNA
		invalidateRect.Call(curtainHwnd, 0, 1)
	}
}

func hideCurtain() {
	if curtainHwnd != 0 {
		showWindow.Call(curtainHwnd, 0)
	}
}

// clearPrivacy removes any curtain and unblocks local input. Called on
// disconnect so the agent user can never be left locked out.
func clearPrivacy() {
	if state.hwnd != 0 {
		postMessage.Call(state.hwnd, WM_APP_CURTAIN, 0, 0)
	}
	blockInput.Call(0)
	remoteCurtainOn.Store(false)
	remoteLockOn.Store(false)
}

// ---- Screen annotation (operator marks the agent screen) ----
type annotSegN struct{ x1, y1, x2, y2 float64 }
type annotSeg struct {
	x1, y1, x2, y2 int
	at             time.Time
}

var annotateMode atomic.Bool
var annotDrawing bool
var annotLastX, annotLastY float64
var annotHwnd uintptr
var annotMu sync.Mutex
var annotStrokes []annotSeg
var annotPending []annotSegN
var annotFadeOnce sync.Once

func sendAnnot(x1, y1, x2, y2 float64) {
	state.mu.Lock()
	pr := state.peer
	state.mu.Unlock()
	if pr != nil {
		_ = pr.SendControlText(fmt.Sprintf("ANNOT:%.4f,%.4f,%.4f,%.4f", x1, y1, x2, y2))
	}
}

func annotProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	if m == 0x000F { // WM_PAINT
		var ps paintStruct
		hdc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if hdc != 0 {
			var rc rect
			getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
			bb, _, _ := getStockObject.Call(4)
			fillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), bb) // black = colorkey = transparent
			pen, _, _ := createPen.Call(0, 4, rgb(0xFF3B30))
			old, _, _ := selectObject.Call(hdc, pen)
			annotMu.Lock()
			strokes := append([]annotSeg(nil), annotStrokes...)
			annotMu.Unlock()
			for _, sg := range strokes {
				moveToEx.Call(hdc, uintptr(sg.x1), uintptr(sg.y1), 0)
				lineTo.Call(hdc, uintptr(sg.x2), uintptr(sg.y2))
			}
			selectObject.Call(hdc, old)
			deleteObject.Call(pen)
			endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

func ensureAnnotOverlay() {
	if annotHwnd != 0 {
		return
	}
	class := utf16ptr("RSAnnot")
	bb, _, _ := getStockObject.Call(4)
	wc := wndclassex{CbSize: uint32(unsafe.Sizeof(wndclassex{})), LpfnWndProc: syscall.NewCallback(annotProc), HInstance: moduleHandle(), HbrBackground: bb, LpszClassName: class}
	registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	x := systemMetric(76)
	y := systemMetric(77)
	w := systemMetric(78)
	h := systemMetric(79)
	ex := uintptr(0x80000 | 0x20 | 0x8 | 0x80 | 0x08000000) // LAYERED|TRANSPARENT|TOPMOST|TOOLWINDOW|NOACTIVATE
	hwnd, _, _ := createWindow.Call(ex, uintptr(unsafe.Pointer(class)), 0, 0x80000000, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, moduleHandle(), 0)
	annotHwnd = hwnd
	if hwnd != 0 {
		setLayeredWinAttr.Call(hwnd, 0x00000000, 0, 0x1) // LWA_COLORKEY: black transparent
	}
}

func drainAnnot() {
	mon, ok := remoteTargetMonitor()
	if !ok {
		annotMu.Lock()
		annotPending = nil
		annotMu.Unlock()
		return
	}
	vl := int(systemMetric(76))
	vt := int(systemMetric(77))
	now := time.Now()
	annotMu.Lock()
	pend := annotPending
	annotPending = nil
	for _, pp := range pend {
		annotStrokes = append(annotStrokes, annotSeg{
			x1: mon.X + int(pp.x1*float64(mon.Width)) - vl, y1: mon.Y + int(pp.y1*float64(mon.Height)) - vt,
			x2: mon.X + int(pp.x2*float64(mon.Width)) - vl, y2: mon.Y + int(pp.y2*float64(mon.Height)) - vt, at: now})
	}
	if len(annotStrokes) > 2000 {
		annotStrokes = annotStrokes[len(annotStrokes)-2000:]
	}
	annotMu.Unlock()
	ensureAnnotOverlay()
	if annotHwnd != 0 {
		showWindow.Call(annotHwnd, 8)
		invalidateRect.Call(annotHwnd, 0, 1)
	}
	startAnnotFade()
}

func clearAnnot() {
	annotMu.Lock()
	annotStrokes = nil
	annotMu.Unlock()
	if annotHwnd != 0 {
		invalidateRect.Call(annotHwnd, 0, 1)
	}
}

func startAnnotFade() {
	annotFadeOnce.Do(func() {
		go func() {
			defer logCrash("annotFade")
			t := time.NewTicker(500 * time.Millisecond)
			defer t.Stop()
			for range t.C {
				annotMu.Lock()
				before := len(annotStrokes)
				cutoff := time.Now().Add(-4 * time.Second)
				kept := annotStrokes[:0]
				for _, sg := range annotStrokes {
					if sg.at.After(cutoff) {
						kept = append(kept, sg)
					}
				}
				annotStrokes = kept
				changed := len(annotStrokes) != before
				annotMu.Unlock()
				if changed && annotHwnd != 0 {
					invalidateRect.Call(annotHwnd, 0, 1)
				}
			}
		}()
	})
}

// ---------------------------------------------------------------------------
// Branding (white-label) + embedded logo
// ---------------------------------------------------------------------------

//go:embed assets/logo.png
var logoPNG []byte
