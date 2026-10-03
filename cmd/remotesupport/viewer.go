//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/eirahoutmoss/remotesupport/client/capture"
	"github.com/eirahoutmoss/remotesupport/client/screen"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/eirahoutmoss/remotesupport/shared/protocol"
)

func registerViewerClass() {
	class := utf16ptr(viewerClass)
	wc := wndclassex{
		CbSize:        uint32(unsafe.Sizeof(wndclassex{})),
		LpfnWndProc:   syscall.NewCallback(viewerWndProc),
		HInstance:     moduleHandle(),
		HCursor:       func() uintptr { r, _, _ := loadCursor.Call(0, 32512); return r }(),
		HbrBackground: 4, // BLACK_BRUSH
		LpszClassName: class,
		HIcon:         appIcon(32),
		HIconSm:       appIcon(16),
	}
	registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
}

func createViewer(parent uintptr) uintptr {
	class := utf16ptr(viewerClass)
	hwnd, _, _ := createWindow.Call(
		0,
		uintptr(unsafe.Pointer(class)),
		0,
		0x40000000|0x10000000|0x00800000, // WS_CHILD | WS_VISIBLE | WS_BORDER
		20, 120, 720, 400,
		parent, 0, moduleHandle(), 0,
	)
	viewer.hwnd = hwnd
	viewer.mu.Lock()
	viewer.fit = true
	viewer.zoom = 1
	viewer.mu.Unlock()
	return hwnd
}

func resizeViewer() {
	h := viewer.hwnd
	if h == 0 || state.hwnd == 0 {
		return
	}
	var rc rect
	getClientRect.Call(state.hwnd, uintptr(unsafe.Pointer(&rc)))
	w := int(rc.Right - rc.Left)
	ht := int(rc.Bottom - rc.Top)
	if w < 1 || ht < 1 {
		return
	}

	viewer.mu.RLock()
	active := viewer.active
	viewer.mu.RUnlock()
	if active && !fullscreen.Load() {
		cg := computeChatGeom(w, ht)
		vw := w - 190
		if cg.visible {
			vw = cg.x - 168 - 12
		}
		setWindowPos.Call(h, 0, 168, 145, uintptr(maxInt(1, vw)), uintptr(maxInt(1, ht-210)), 0x0004|0x0010)
	} else if active {
		setWindowPos.Call(h, 0, 0, 86, uintptr(maxInt(1, w)), uintptr(maxInt(1, ht-86)), 0x0004|0x0010)
	}
}

func toggleFullscreen() {
	if state.hwnd == 0 {
		return
	}
	if fullscreen.Load() {
		if fullscreenSavedValid {
			setWindowLongPtr.Call(state.hwnd, ^uintptr(15), fullscreenSavedStyle)
			w := int(fullscreenSavedRect.Right - fullscreenSavedRect.Left)
			h := int(fullscreenSavedRect.Bottom - fullscreenSavedRect.Top)
			setWindowPos.Call(state.hwnd, 0, uintptr(fullscreenSavedRect.Left), uintptr(fullscreenSavedRect.Top), uintptr(maxInt(1, w)), uintptr(maxInt(1, h)), 0x0004|0x0020|0x0040)
		}
		fullscreen.Store(false)
		show(state.fullscreenButton)
		showResolutionControls(true)
		showMonitorControls(true)
	} else {
		var rc rect
		getWindowRect.Call(state.hwnd, uintptr(unsafe.Pointer(&rc)))
		style, _, _ := getWindowLongPtr.Call(state.hwnd, ^uintptr(15))
		fullscreenSavedStyle = style
		fullscreenSavedRect = rc
		fullscreenSavedValid = true
		setWindowLongPtr.Call(state.hwnd, ^uintptr(15), 0x80000000|0x10000000)
		cx := systemMetric(0)
		cy := systemMetric(1)
		setWindowPos.Call(state.hwnd, 0, 0, 0, uintptr(maxInt(1, int(cx))), uintptr(maxInt(1, int(cy))), 0x0004|0x0020|0x0040)
		fullscreen.Store(true)
		if viewer.hwnd != 0 {
			setFocus.Call(viewer.hwnd) // keys go to the remote screen right away
		}
		hide(state.fullscreenButton)
		showResolutionControls(false)
		showMonitorControls(false)
	}
	layoutUI()
	resizeViewer()
	invalidateRect.Call(viewer.hwnd, 0, 0)
}

func showResolutionControls(visible bool) {
	state.mu.Lock()
	buttons := append([]uintptr(nil), state.resolutionButtons...)
	state.mu.Unlock()
	for _, h := range buttons {
		if visible {
			show(h)
		} else {
			hide(h)
		}
	}
}

func showMonitorControls(visible bool) {
	state.mu.Lock()
	buttons := append([]uintptr(nil), state.monitorButtons...)
	state.mu.Unlock()
	for _, h := range buttons {
		if visible {
			show(h)
		} else {
			hide(h)
		}
	}
}

func setRemoteResolution(maxHeight int) {
	if maxHeight < 0 {
		maxHeight = 0
	}
	state.mu.Lock()
	peer := state.peer
	state.mu.Unlock()
	if peer != nil {
		_ = peer.SendControlText(fmt.Sprintf("RESOLUTION:%d", maxHeight))
	}
	labels := map[int]string{0: "Otomatik", 1080: "1080p", 720: "720p", 480: "480p"}
	if label, ok := labels[maxHeight]; ok {
		setStatus("● Görüntü çözünürlüğü: " + label)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func saveViewerScreenshot() {
	viewer.mu.RLock()
	w, h := viewer.width, viewer.height
	px := viewer.pixels
	viewer.mu.RUnlock()
	if w <= 0 || h <= 0 || len(px) < w*h*4 {
		setStatus("● Ekran görüntüsü alınamadı: görüntü yok.")
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4+0] = px[i*4+2] // R (frame is BGRA)
		img.Pix[i*4+1] = px[i*4+1] // G
		img.Pix[i*4+2] = px[i*4+0] // B
		img.Pix[i*4+3] = 255
	}
	home, err := os.UserHomeDir()
	if err != nil {
		setStatus("● Ekran görüntüsü kaydedilemedi.")
		return
	}
	dir := filepath.Join(home, "Downloads")
	_ = os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, fmt.Sprintf("RemoteSupport-%s.jpg", time.Now().Format("20060102-150405")))
	f, err := os.Create(path)
	if err != nil {
		setStatus("● Ekran görüntüsü kaydedilemedi: " + err.Error())
		return
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		setStatus("● Ekran görüntüsü kodlanamadı.")
		return
	}
	setStatus("● Ekran görüntüsü kaydedildi: " + path)
}

func setViewerFrame(f screen.Frame) {
	if f.Monitor == reverseMonitorTag {
		return
	}
	if tm, ok := parseTileMeta(f.JPEG); ok {
		applyTileDelta(int(f.Width), int(f.Height), f.JPEG, tm)
		return
	}
	img, err := jpeg.Decode(bytes.NewReader(f.JPEG))
	if err != nil {
		return
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	pixels := make([]byte, width*height*4)

	// image/jpeg normally decodes into *image.YCbCr. Avoid img.At(), which
	// performs interface/color-model work for every pixel and became a major
	// CPU/GC cost at 3440x1440. Read the Y/Cb/Cr planes directly instead.
	if ycc, ok := img.(*image.YCbCr); ok && bounds.Min.X == 0 && bounds.Min.Y == 0 {
		ratio := ycc.SubsampleRatio
		for y := 0; y < height; y++ {
			yOff := y * ycc.YStride
			for x := 0; x < width; x++ {
				var cx, cy int
				switch ratio {
				case image.YCbCrSubsampleRatio444:
					cx, cy = x, y
				case image.YCbCrSubsampleRatio422:
					cx, cy = x/2, y
				case image.YCbCrSubsampleRatio440:
					cx, cy = x, y/2
				default: // 4:2:0, the common JPEG case
					cx, cy = x/2, y/2
				}
				cOff := cy*ycc.CStride + cx
				r, g, b := color.YCbCrToRGB(ycc.Y[yOff+x], ycc.Cb[cOff], ycc.Cr[cOff])
				i := (y*width + x) * 4
				pixels[i+0] = b
				pixels[i+1] = g
				pixels[i+2] = r
				pixels[i+3] = 0
			}
		}
	} else {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
				i := (y*width + x) * 4
				pixels[i+0] = byte(b >> 8)
				pixels[i+1] = byte(g >> 8)
				pixels[i+2] = byte(r >> 8)
			}
		}
	}

	viewer.mu.Lock()
	viewer.pixels = pixels
	viewer.width = width
	viewer.height = height
	viewer.seq = f.Seq
	h := viewer.hwnd
	viewer.mu.Unlock()
	framesRecv.Add(1)
	if h != 0 {
		invalidateRect.Call(h, 0, 0)
	}
}

func showViewer() {
	state.mu.Lock()
	buttons := append([]uintptr(nil), state.monitorButtons...)
	state.mu.Unlock()
	for _, h := range buttons {
		show(h)
	}
	viewer.mu.Lock()
	viewer.active = true
	viewer.fit = true
	viewer.zoom = 1
	viewer.panX = 0
	viewer.panY = 0
	viewer.clearNeeded = true
	h := viewer.hwnd
	viewer.mu.Unlock()
	if h != 0 {
		show(h)
	}
	showMonitorControls(!fullscreen.Load())
	showResolutionControls(!fullscreen.Load())
	show(state.fullscreenButton)
	resizeViewer()
}

func hideViewer() {
	if fullscreen.Load() {
		toggleFullscreen()
	}
	hide(state.fileSend)
	hide(state.fullscreenButton)
	showResolutionControls(false)
	showMonitorControls(false)
	state.mu.Lock()
	buttons := append([]uintptr(nil), state.monitorButtons...)
	state.mu.Unlock()
	for _, h := range buttons {
		hide(h)
	}
	viewer.mu.Lock()
	viewer.active = false
	h := viewer.hwnd
	viewer.mu.Unlock()
	if h != 0 {
		hide(h)
	}
}

func fitRect(srcW, srcH, dstW, dstH int) (left, top, width, height int) {
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return 0, 0, dstW, dstH
	}
	// Compare ratios without floating point.
	if srcW*dstH >= srcH*dstW {
		width = dstW
		height = maxInt(1, srcH*dstW/srcW)
	} else {
		height = dstH
		width = maxInt(1, srcW*dstH/srcH)
	}
	left = (dstW - width) / 2
	top = (dstH - height) / 2
	return
}

func viewerWndProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	switch m {
	case 0x0014: // WM_ERASEBKGND
		// Suppress background erase. WM_ERASEBKGND between frames is another
		// source of visible flashing; WM_PAINT draws the changed frame itself.
		return 1
	case 0x000F: // WM_PAINT
		var ps paintStruct
		hdc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		viewer.mu.Lock()
		pixels := viewer.pixels // immutable after publication; setViewerFrame replaces the slice.
		width, height := viewer.width, viewer.height
		fit, zoom, panX, panY := viewer.fit, viewer.zoom, viewer.panX, viewer.panY
		clearNeeded := viewer.clearNeeded
		viewer.clearNeeded = false
		viewer.mu.Unlock()
		if hdc != 0 {
			var rc rect
			getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
			dw := int(rc.Right - rc.Left)
			dh := int(rc.Bottom - rc.Top)
			// Clear only when the viewer geometry changes. Frame arrivals invalidate
			// the same window, but must not clear the existing image first.
			// That preserves the no-flicker behavior while keeping letterbox areas black.
			if clearNeeded {
				blackBrush, _, _ := getStockObject.Call(4) // BLACK_BRUSH
				if blackBrush != 0 {
					fillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), blackBrush)
				}
			}
			if len(pixels) == 0 {
				setBkMode.Call(hdc, 1)
				setTextColor.Call(hdc, rgb(uiMutedColor))
				oldf, _, _ := selectObject.Call(hdc, uiBodyFont)
				wt := utf16ptr("Bağlanıldı — ekran görüntüsü bekleniyor…")
				drawText.Call(hdc, uintptr(unsafe.Pointer(wt)), ^uintptr(0), uintptr(unsafe.Pointer(&rc)), uintptr(dtCenter|dtVCenter|dtSingleLine))
				selectObject.Call(hdc, oldf)
			}
			if len(pixels) > 0 && width > 0 && height > 0 && dw > 0 && dh > 0 {
				var left, top, drawW, drawH int
				if fit {
					left, top, drawW, drawH = fitRect(width, height, dw, dh)
				} else {
					drawW = maxInt(1, int(math.Round(float64(width)*zoom)))
					drawH = maxInt(1, int(math.Round(float64(height)*zoom)))
					left = int(math.Round(panX))
					top = int(math.Round(panY))
				}
				bi := bitmapInfo{BmiHeader: bitmapInfoHeader{
					BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
					BiWidth:       int32(width),
					BiHeight:      -int32(height),
					BiPlanes:      1,
					BiBitCount:    32,
					BiCompression: 0,
				}}
				setStretchMode.Call(hdc, 3) // COLORONCOLOR: materially faster for live remote desktop
				stretchDIBits.Call(hdc,
					uintptr(left), uintptr(top), uintptr(drawW), uintptr(drawH),
					0, 0, uintptr(width), uintptr(height),
					uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&bi)), 0, 0x00CC0020)
			}
			endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	case 0x0005: // WM_SIZE
		viewer.mu.Lock()
		viewer.clearNeeded = true
		viewer.mu.Unlock()
		invalidateRect.Call(hwnd, 0, 0)
		return 0
	case 0x0007: // WM_SETFOCUS
		return 0
	case 0x0087: // WM_GETDLGCODE
		return 0x0004 | 0x0080 | 0x0001 | 0x0002 | 0x0004
	case 0x0201: // WM_LBUTTONDOWN
		setFocus.Call(hwnd)
		setCapture.Call(hwnd)
		mx := int(int16(uint16(lParam & 0xffff)))
		my := int(int16(uint16(lParam >> 16)))
		if annotateMode.Load() {
			if ax, ay, ok := normalizedViewerPoint(mx, my); ok {
				annotLastX, annotLastY, annotDrawing = ax, ay, true
			}
			return 0
		}
		sendRemoteMouse("down", mx, my, 0, 0)
		return 0
	case 0x0202: // WM_LBUTTONUP
		releaseCapture.Call()
		mx := int(int16(uint16(lParam & 0xffff)))
		my := int(int16(uint16(lParam >> 16)))
		if annotateMode.Load() {
			annotDrawing = false
			return 0
		}
		sendRemoteMouse("up", mx, my, 0, 0)
		return 0
	case 0x0204: // WM_RBUTTONDOWN
		setFocus.Call(hwnd)
		setCapture.Call(hwnd)
		mx := int(int16(uint16(lParam & 0xffff)))
		my := int(int16(uint16(lParam >> 16)))
		sendRemoteMouse("down", mx, my, 2, 0)
		return 0
	case 0x0205: // WM_RBUTTONUP
		releaseCapture.Call()
		mx := int(int16(uint16(lParam & 0xffff)))
		my := int(int16(uint16(lParam >> 16)))
		sendRemoteMouse("up", mx, my, 2, 0)
		return 0
	case 0x0207: // WM_MBUTTONDOWN
		setFocus.Call(hwnd)
		setCapture.Call(hwnd)
		mx := int(int16(uint16(lParam & 0xffff)))
		my := int(int16(uint16(lParam >> 16)))
		sendRemoteMouse("down", mx, my, 1, 0)
		return 0
	case 0x0208: // WM_MBUTTONUP
		releaseCapture.Call()
		mx := int(int16(uint16(lParam & 0xffff)))
		my := int(int16(uint16(lParam >> 16)))
		sendRemoteMouse("up", mx, my, 1, 0)
		return 0
	case 0x0100: // WM_KEYDOWN
		if wParam == 0x7A { // VK_F11
			toggleFullscreen()
			return 0
		}
		if wParam == 0x1B && fullscreen.Load() { // VK_ESCAPE
			toggleFullscreen()
			return 0
		}
		if wParam == 0x7B { // VK_F12: local screenshot, do not forward
			go saveViewerScreenshot()
			return 0
		}
		if wParam != 0 {
			sendRemoteKey("keydown", uint16(wParam))
		}
		return 0
	case 0x0101: // WM_KEYUP
		if wParam != 0 {
			sendRemoteKey("keyup", uint16(wParam))
		}
		return 0
	case 0x0104: // WM_SYSKEYDOWN (Alt, F-keys, etc.)
		if wParam != 0 {
			sendRemoteKey("keydown", uint16(wParam))
		}
		return 0
	case 0x0105: // WM_SYSKEYUP
		if wParam != 0 {
			sendRemoteKey("keyup", uint16(wParam))
		}
		return 0
	case 0x020A: // WM_MOUSEWHEEL
		delta := int16(uint16(wParam >> 16))
		if delta == 0 {
			return 0
		}
		var cursor point
		getCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
		screenToClient.Call(hwnd, uintptr(unsafe.Pointer(&cursor)))
		mx, my := int(cursor.X), int(cursor.Y)

		// The viewer is a remote desktop, not a camera. Every wheel event
		// goes to the remote computer. It must never zoom the local viewer.
		sendRemoteMouse("wheel", mx, my, 0, int(delta))
		return 0
	case 0x0200: // WM_MOUSEMOVE
		mx := int(int16(uint16(lParam & 0xffff)))
		my := int(int16(uint16(lParam >> 16)))
		if annotateMode.Load() {
			if annotDrawing {
				if ax, ay, ok := normalizedViewerPoint(mx, my); ok {
					sendAnnot(annotLastX, annotLastY, ax, ay)
					annotLastX, annotLastY = ax, ay
				}
			}
			return 0
		}

		// When panning the viewer, the mouse event belongs to the local viewer.
		// Do not forward it to the remote machine, otherwise the remote cursor
		// can jump while the user is dragging the image.
		viewer.mu.Lock()
		if viewer.dragging {
			if !viewer.fit {
				viewer.panX += float64(mx - viewer.dragX)
				viewer.panY += float64(my - viewer.dragY)
			}
			viewer.dragX, viewer.dragY = mx, my
			viewer.mu.Unlock()
			invalidateRect.Call(hwnd, 0, 0)
			return 0
		}
		viewer.mu.Unlock()

		// Only the viewer image receives remote mouse motion. Letterbox areas
		// remain local and cannot cause a remote cursor jump.
		sendRemoteMouse("move", mx, my, 0, 0)
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

func sendRemoteInput(peer *webrtcpeer.Peer, ev protocol.InputEvent) {
	if peer == nil {
		return
	}
	lastActivityUnix.Store(time.Now().Unix())
	if err := ev.Validate(); err != nil {
		return
	}
	macroRecord(ev)
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = peer.SendControlText("INPUT:" + string(b))
}

func remoteTargetMonitor() (capture.Monitor, bool) {
	activeTargetMonitorMu.RLock()
	mon, ok := activeTargetMonitor, activeTargetMonitorOK
	activeTargetMonitorMu.RUnlock()
	if ok {
		return mon, true
	}
	idx := int(activeMonitor.Load())
	monitors, err := capture.ListMonitors()
	if err == nil && idx >= 0 && idx < len(monitors) {
		return monitors[idx], true
	}
	if len(monitors) > 0 {
		return monitors[0], true
	}
	return capture.Monitor{}, false
}

func normalizedViewerPoint(mx, my int) (float64, float64, bool) {
	viewer.mu.RLock()
	width, height := viewer.width, viewer.height
	fit, zoom, panX, panY := viewer.fit, viewer.zoom, viewer.panX, viewer.panY
	viewer.mu.RUnlock()
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}

	var left, top, drawW, drawH int
	var rc rect
	getClientRect.Call(viewer.hwnd, uintptr(unsafe.Pointer(&rc)))
	dw, dh := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
	if fit {
		left, top, drawW, drawH = fitRect(width, height, dw, dh)
	} else {
		drawW = maxInt(1, int(math.Round(float64(width)*zoom)))
		drawH = maxInt(1, int(math.Round(float64(height)*zoom)))
		left, top = int(math.Round(panX)), int(math.Round(panY))
	}
	if drawW <= 0 || drawH <= 0 || mx < left || my < top || mx >= left+drawW || my >= top+drawH {
		return 0, 0, false
	}
	x := float64(mx-left) / float64(drawW)
	y := float64(my-top) / float64(drawH)
	if x < 0 {
		x = 0
	}
	if x > 1 {
		x = 1
	}
	if y < 0 {
		y = 0
	}
	if y > 1 {
		y = 1
	}
	return x, y, true
}

func sendRemoteKey(keyType string, key uint16) {
	state.mu.Lock()
	peer := state.peer
	state.mu.Unlock()
	if peer == nil || key == 0 {
		return
	}
	sendRemoteInput(peer, protocol.InputEvent{T: keyType, K: int(key)})
}

func sendRemoteMouse(moveType string, mx, my int, button int, delta int) {
	state.mu.Lock()
	peer := state.peer
	state.mu.Unlock()
	if peer == nil {
		return
	}
	x, y, ok := normalizedViewerPoint(mx, my)
	if !ok {
		return
	}
	if moveType == "move" {
		viewer.mu.Lock()
		now := time.Now()
		if !viewer.lastMoveAt.IsZero() && now.Sub(viewer.lastMoveAt) < 16*time.Millisecond {
			viewer.mu.Unlock()
			return
		}
		viewer.lastMoveAt = now
		viewer.mu.Unlock()
	}
	sendRemoteInput(peer, protocol.InputEvent{T: moveType, X: x, Y: y, B: button, D: delta})
}
