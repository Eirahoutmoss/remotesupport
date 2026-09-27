//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/eirahoutmoss/remotesupport/client/capture"
	"github.com/eirahoutmoss/remotesupport/client/screen"
	clientsignaling "github.com/eirahoutmoss/remotesupport/client/signaling"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/eirahoutmoss/remotesupport/shared/protocol"
	"github.com/pion/webrtc/v4"
)

const (
	appTitle            = "Remote Support"
	defaultSignalingURL = "ws://127.0.0.1:8091/v1/ws"
	windowClass         = "RemoteSupportWindow"
	viewerClass         = "RemoteSupportViewer"
	windowWidth         = 760
	windowHeight        = 560

	ID_GET_SUPPORT  = 1001
	ID_GIVE_SUPPORT = 1002
	ID_BACK         = 1003
	ID_COPY_CODE    = 1004
	ID_REMOTE_CODE  = 1005
	ID_CONNECT      = 1006
	ID_MONITOR_BASE = 2000
	ID_FILE_SEND    = 3001
	WM_APP_MONITORS = 0x8001
)

var (
	gdi32DLL         = syscall.NewLazyDLL("gdi32.dll")
	comdlg32         = syscall.NewLazyDLL("comdlg32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	registerClass    = user32.NewProc("RegisterClassExW")
	createWindow     = user32.NewProc("CreateWindowExW")
	defWindowProc    = user32.NewProc("DefWindowProcW")
	showWindow       = user32.NewProc("ShowWindow")
	setWindowPos     = user32.NewProc("SetWindowPos")
	updateWindow     = user32.NewProc("UpdateWindow")
	getMessage       = user32.NewProc("GetMessageW")
	translateMsg     = user32.NewProc("TranslateMessage")
	dispatchMessage  = user32.NewProc("DispatchMessageW")
	postQuit         = user32.NewProc("PostQuitMessage")
	sendMessage      = user32.NewProc("SendMessageW")
	postMessage      = user32.NewProc("PostMessageW")
	setWindowText    = user32.NewProc("SetWindowTextW")
	getWindowText    = user32.NewProc("GetWindowTextW")
	destroyWindow    = user32.NewProc("DestroyWindow")
	messageBox       = user32.NewProc("MessageBoxW")
	getModule        = kernel32.NewProc("GetModuleHandleW")
	loadCursor       = user32.NewProc("LoadCursorW")
	openClipboard    = user32.NewProc("OpenClipboard")
	emptyClipboard   = user32.NewProc("EmptyClipboard")
	setClipboardData = user32.NewProc("SetClipboardData")
	closeClipboard   = user32.NewProc("CloseClipboard")
	beginPaint       = user32.NewProc("BeginPaint")
	endPaint         = user32.NewProc("EndPaint")
	fillRect         = user32.NewProc("FillRect")
	getClientRect    = user32.NewProc("GetClientRect")
	getCursorPos     = user32.NewProc("GetCursorPos")
	screenToClient   = user32.NewProc("ScreenToClient")
	getSystemMetrics = user32.NewProc("GetSystemMetrics")
	sendInput        = user32.NewProc("SendInput")
	setFocus         = user32.NewProc("SetFocus")
	invalidateRect   = user32.NewProc("InvalidateRect")
	registerClassEx  = user32.NewProc("RegisterClassExW")
	stretchDIBits    = gdi32DLL.NewProc("StretchDIBits")
	setStretchMode   = gdi32DLL.NewProc("SetStretchBltMode")
	getStockObject   = gdi32DLL.NewProc("GetStockObject")
	patBlt           = gdi32DLL.NewProc("PatBlt")
	setCapture       = user32.NewProc("SetCapture")
	releaseCapture   = user32.NewProc("ReleaseCapture")
	globalAlloc      = kernel32.NewProc("GlobalAlloc")
	globalLock       = kernel32.NewProc("GlobalLock")
	globalUnlock     = kernel32.NewProc("GlobalUnlock")
	getOpenFileName  = comdlg32.NewProc("GetOpenFileNameW")
)

type point struct{ X, Y int32 }
type openFileName struct {
	StructSize    uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	Reserved      uintptr
	Reserved2     uint32
	ExHook        uintptr
}

type fileReceiveState struct {
	mu       sync.Mutex
	file     *os.File
	name     string
	total    int64
	received int64
	tempPath string
}

var fileRx fileReceiveState
var fileSendMu sync.Mutex

type rect struct{ Left, Top, Right, Bottom int32 }
type wndclassex struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}
type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}
type windowState struct {
	hwnd           uintptr
	mode           int
	status         uintptr
	codeLabel      uintptr
	codeEdit       uintptr
	remoteEdit     uintptr
	monitorButtons []uintptr
	action         uintptr
	copy           uintptr
	back           uintptr
	homeGet        uintptr
	homeGive       uintptr
	fileSend       uintptr
	header         uintptr
	subtitle       uintptr

	mu     sync.Mutex
	sig    *clientsignaling.Client
	peer   *webrtcpeer.Peer
	cancel context.CancelFunc
}

var state windowState

var viewer viewerState

var targetMonitorCache struct {
	sync.Once
	monitor capture.Monitor
	ok      bool
}

var sameHostPeer atomic.Bool
var activeMonitor atomic.Int32
var remoteMonitorsMu sync.RWMutex
var remoteMonitors []capture.Monitor

type viewerState struct {
	hwnd   uintptr
	active bool
	mu     sync.RWMutex
	pixels []byte
	width  int
	height int
	seq    uint64

	// Fit is the default remote-desktop view. When zoomed, panX/panY are
	// the image's top-left corner in viewer-client coordinates.
	fit         bool
	zoom        float64
	panX        float64
	panY        float64
	dragging    bool
	clearNeeded bool
	dragX       int
	dragY       int
	lastMoveAt  time.Time
}

type winMouseInput struct {
	Dx, Dy      int32
	MouseData   uint32
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type winKeyboardInput struct {
	Vk          uint16
	Scan        uint16
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type winInput struct {
	Type uint32
	Pad  uint32
	Mi   winMouseInput
}

type winKeyInput struct {
	Type uint32
	Pad  uint32
	Ki   winKeyboardInput
}

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove       = 0x0001
	mouseLeftDown   = 0x0002
	mouseLeftUp     = 0x0004
	mouseRightDown  = 0x0008
	mouseRightUp    = 0x0010
	mouseMiddleDown = 0x0020
	mouseMiddleUp   = 0x0040
	mouseWheel      = 0x0800
	mouseAbsolute   = 0x8000
	mouseVirtual    = 0x4000

	keyUp = 0x0002
)

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
	BmiColors [3]uint32
}

type paintStruct struct {
	hdc         uintptr
	erase       int32
	rcPaint     rect
	restore     int32
	incUpdate   int32
	rgbReserved [32]byte
}

func utf16ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func registerViewerClass() {
	class := utf16ptr(viewerClass)
	wc := wndclassex{
		CbSize:        uint32(unsafe.Sizeof(wndclassex{})),
		LpfnWndProc:   syscall.NewCallback(viewerWndProc),
		HInstance:     moduleHandle(),
		HCursor:       func() uintptr { r, _, _ := loadCursor.Call(0, 32512); return r }(),
		HbrBackground: 4, // BLACK_BRUSH
		LpszClassName: class,
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

	// When the remote viewer is active it owns almost the entire client area.
	// Before connection, keep the original compact layout.
	y := 120
	viewer.mu.RLock()
	active := viewer.active
	viewer.mu.RUnlock()
	if active {
		y = 58
	}
	bottom := ht - 20
	if bottom <= y {
		bottom = y + 1
	}
	setWindowPos.Call(h, 0, 20, uintptr(y), uintptr(maxInt(1, w-40)), uintptr(bottom-y), 0x0004|0x0010) // SWP_NOZORDER | SWP_NOACTIVATE
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func setViewerFrame(f screen.Frame) {
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
}

func hideViewer() {
	hide(state.fileSend)
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
	if err := ev.Validate(); err != nil {
		return
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = peer.SendControlText("INPUT:" + string(b))
}

func remoteTargetMonitor() (capture.Monitor, bool) {
	idx := int(activeMonitor.Load())
	remoteMonitorsMu.RLock()
	defer remoteMonitorsMu.RUnlock()
	if idx >= 0 && idx < len(remoteMonitors) {
		return remoteMonitors[idx], true
	}
	// Keep monitor 0 as a safe fallback until the target publishes its monitor list.
	if len(remoteMonitors) > 0 {
		return remoteMonitors[0], true
	}
	targetMonitorCache.Do(func() {
		monitors, err := capture.ListMonitors()
		if err == nil && len(monitors) > 0 {
			targetMonitorCache.monitor = monitors[0]
			targetMonitorCache.ok = true
		}
	})
	return targetMonitorCache.monitor, targetMonitorCache.ok
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

func handleRemoteFile(data []byte) {
	if len(data) < 1 {
		return
	}
	switch data[0] {
	case 0x01:
		var meta struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		}
		if json.Unmarshal(data[1:], &meta) != nil || meta.Size < 0 || meta.Size > 4*1024*1024*1024 {
			return
		}
		name := filepath.Base(strings.TrimSpace(meta.Name))
		if name == "" || name == "." || name == ".." {
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir := filepath.Join(home, "Downloads")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return
		}
		final := filepath.Join(dir, name)
		if _, err := os.Stat(final); err == nil {
			ext := filepath.Ext(name)
			stem := strings.TrimSuffix(name, ext)
			for i := 1; ; i++ {
				candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
				if _, e := os.Stat(candidate); os.IsNotExist(e) {
					name, final = filepath.Base(candidate), candidate
					break
				}
			}
		}
		tmp := final + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			return
		}
		fileRx.mu.Lock()
		if fileRx.file != nil {
			_ = fileRx.file.Close()
		}
		fileRx.file, fileRx.name, fileRx.total, fileRx.received, fileRx.tempPath = f, name, meta.Size, 0, tmp
		fileRx.mu.Unlock()
		setStatus(fmt.Sprintf("● Dosya alınıyor: %s  0%%", name))
	case 0x02:
		fileRx.mu.Lock()
		defer fileRx.mu.Unlock()
		if fileRx.file == nil {
			return
		}
		n, err := fileRx.file.Write(data[1:])
		if err != nil {
			_ = fileRx.file.Close()
			fileRx.file = nil
			return
		}
		fileRx.received += int64(n)
		if fileRx.total > 0 {
			setStatus(fmt.Sprintf("● Dosya alınıyor: %s  %d%%", fileRx.name, fileRx.received*100/fileRx.total))
		}
	case 0x03:
		fileRx.mu.Lock()
		defer fileRx.mu.Unlock()
		if fileRx.file == nil {
			return
		}
		_ = fileRx.file.Close()
		final := strings.TrimSuffix(fileRx.tempPath, ".part")
		if err := os.Rename(fileRx.tempPath, final); err != nil {
			setStatus("● Dosya alınamadı: " + err.Error())
		} else {
			setStatus("● Dosya alındı: " + final)
		}
		fileRx.file = nil
		fileRx.tempPath = ""
	case 0x04:
		fileRx.mu.Lock()
		if fileRx.file != nil {
			_ = fileRx.file.Close()
		}
		if fileRx.tempPath != "" {
			_ = os.Remove(fileRx.tempPath)
		}
		fileRx = fileReceiveState{}
		fileRx.mu.Unlock()
		setStatus("● Dosya aktarımı iptal edildi.")
	}
}

func handleRemoteInput(peer *webrtcpeer.Peer, raw string) {
	if raw == "CLIP_REQ" {
		if sendClipboardToPeer(peer) {
			setStatus("● Pano karşı bilgisayara gönderildi.")
		}
		return
	}
	if strings.HasPrefix(raw, "CLIP:") {
		payload := strings.TrimPrefix(raw, "CLIP:")
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil || len(data) > 48*1024 {
			return
		}
		if copyToClipboard(string(data)) {
			setStatus("● Uzak pano yerel panoya aktarıldı.")
		}
		return
	}
	if strings.HasPrefix(raw, "FILE_BEGIN:") || strings.HasPrefix(raw, "FILE_CHUNK:") || raw == "FILE_ABORT" || raw == "FILE_END" {
		return
	}
	if strings.HasPrefix(raw, "MONITORS:") {
		var monitors []capture.Monitor
		if err := json.Unmarshal([]byte(strings.TrimPrefix(raw, "MONITORS:")), &monitors); err == nil && len(monitors) > 0 {
			remoteMonitorsMu.Lock()
			remoteMonitors = append([]capture.Monitor(nil), monitors...)
			remoteMonitorsMu.Unlock()
			if int(activeMonitor.Load()) >= len(monitors) {
				activeMonitor.Store(0)
			}
			// Win32 child-window operations must run on the GUI thread.
			// WebRTC callbacks run on worker goroutines, so marshal the UI
			// update through PostMessage instead of creating/destroying
			// buttons directly from the callback goroutine.
			state.mu.Lock()
			hwnd := state.hwnd
			state.mu.Unlock()
			if hwnd != 0 {
				postMessage.Call(hwnd, WM_APP_MONITORS, uintptr(len(monitors)), 0)
			}
		}
		return
	}
	if strings.HasPrefix(raw, "MONITOR:") {
		if state.mode != 1 {
			return
		}
		var idx int
		if _, err := fmt.Sscanf(strings.TrimPrefix(raw, "MONITOR:"), "%d", &idx); err == nil && idx >= 0 {
			if monitors, err := capture.ListMonitors(); err == nil && idx < len(monitors) {
				activeMonitor.Store(int32(idx))
				setStatus(fmt.Sprintf("● Monitör %d/%d aktarılıyor", idx+1, len(monitors)))
			}
		}
		return
	}
	if strings.HasPrefix(raw, "HELLO:") {
		remoteHost := strings.TrimSpace(strings.TrimPrefix(raw, "HELLO:"))
		localHost, _ := os.Hostname()
		if remoteHost != "" && localHost != "" && strings.EqualFold(remoteHost, localHost) {
			sameHostPeer.Store(true)
			setStatus("● Loopback test modu: remote mouse/klavye etkin")
		}
		return
	}
	if !strings.HasPrefix(raw, "INPUT:") {
		return
	}
	var ev protocol.InputEvent
	if err := json.Unmarshal([]byte(strings.TrimPrefix(raw, "INPUT:")), &ev); err != nil {
		return
	}
	if err := ev.Validate(); err != nil {
		return
	}
	if ev.T == "move" || ev.T == "down" || ev.T == "up" || ev.T == "wheel" {
		injectRemoteMouse(ev)
		return
	}
	if ev.T == "keydown" || ev.T == "keyup" {
		injectRemoteKey(ev)
	}
}

func systemMetric(index int) int32 {
	r1, _, _ := getSystemMetrics.Call(uintptr(index))
	return int32(r1)
}

func normalizedToVirtualScreen(x, y float64) (int32, int32, bool) {
	mon, ok := remoteTargetMonitor()
	if !ok || mon.Width <= 0 || mon.Height <= 0 {
		return 0, 0, false
	}
	// The streamed image is monitor 0. Map normalized viewer coordinates to
	// that monitor's virtual-desktop rectangle, then to SendInput's 0..65535
	// virtual-screen coordinate space.
	px := float64(mon.X) + x*float64(maxInt(1, mon.Width-1))
	py := float64(mon.Y) + y*float64(maxInt(1, mon.Height-1))
	left := systemMetric(76)
	top := systemMetric(77)
	width := systemMetric(78)
	height := systemMetric(79)
	if width <= 1 || height <= 1 {
		return 0, 0, false
	}
	vx := int32(math.Round((px - float64(left)) * 65535 / float64(width-1)))
	vy := int32(math.Round((py - float64(top)) * 65535 / float64(height-1)))
	return vx, vy, true
}

func absoluteMouseInput(ev protocol.InputEvent, extraFlags uint32, mouseData uint32) (winInput, bool) {
	dx, dy, ok := normalizedToVirtualScreen(ev.X, ev.Y)
	if !ok {
		return winInput{}, false
	}
	return winInput{Type: inputMouse, Mi: winMouseInput{
		Dx: dx, Dy: dy, MouseData: mouseData,
		DwFlags: mouseAbsolute | mouseVirtual | extraFlags,
	}}, true
}

func injectRemoteMouse(ev protocol.InputEvent) {
	if sameHostPeer.Load() {
		return
	}
	// Mouse input must remain enabled. The previous same-host guard made the
	// viewer a read-only camera during local testing because every mouse event
	// was discarded before SendInput. A real two-machine session uses the same
	// path. Same-host testing may move the single Windows cursor because Windows
	// has only one physical pointer, but the remote input itself must not be
	// silently dropped.
	flags := uint32(mouseMove)
	data := uint32(0)
	switch ev.T {
	case "move":
		flags = mouseMove
	case "down":
		switch ev.B {
		case 0:
			flags = mouseLeftDown
		case 1:
			flags = mouseMiddleDown
		case 2:
			flags = mouseRightDown
		}
	case "up":
		switch ev.B {
		case 0:
			flags = mouseLeftUp
		case 1:
			flags = mouseMiddleUp
		case 2:
			flags = mouseRightUp
		}
	case "wheel":
		flags = mouseWheel
		data = uint32(int32(ev.D))
	}
	in, ok := absoluteMouseInput(ev, flags, data)
	if !ok {
		return
	}
	_, _, _ = sendInput.Call(1, uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Sizeof(in)))
}

func injectRemoteKey(ev protocol.InputEvent) {
	in := winKeyInput{Type: inputKeyboard, Ki: winKeyboardInput{Vk: uint16(ev.K)}}
	if ev.T == "keyup" {
		in.Ki.Flags = keyUp
	}
	_, _, _ = sendInput.Call(1, uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Sizeof(in)))
}

func chooseFile() (string, bool) {
	buf := make([]uint16, 32768)
	filter := utf16ptr("Tüm Dosyalar (*.*)\x00*.*\x00\x00")
	ofn := openFileName{StructSize: uint32(unsafe.Sizeof(openFileName{})), Owner: state.hwnd, Filter: filter, File: &buf[0], MaxFile: uint32(len(buf)), Flags: 0x00001000 | 0x00000800}
	r, _, _ := getOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

func sendFileToPeer() {
	fileSendMu.Lock()
	defer fileSendMu.Unlock()

	state.mu.Lock()
	peer := state.peer
	state.mu.Unlock()
	if peer == nil || peer.ConnectionState() != webrtc.PeerConnectionStateConnected {
		setStatus("● Dosya gönderilemiyor: bağlantı hazır değil.")
		return
	}
	path, ok := chooseFile()
	if !ok {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		setStatus("● Dosya açılamadı: " + err.Error())
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		setStatus("● Dosya bilgisi okunamadı: " + err.Error())
		return
	}
	if info.Size() > 4*1024*1024*1024 {
		setStatus("● Dosya 4 GB sınırını aşıyor.")
		return
	}
	setStatus("● Dosya kanalı hazırlanıyor...")
	deadline := time.Now().Add(10 * time.Second)
	for !peer.FileReady() {
		if time.Now().After(deadline) {
			setStatus("● Dosya kanalı hazır değil.")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	meta, _ := json.Marshal(struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}{filepath.Base(path), info.Size()})
	if err := peer.SendFileData(append([]byte{0x01}, meta...)); err != nil {
		setStatus("● Dosya başlatılamadı: " + err.Error())
		return
	}
	buf := make([]byte, 48*1024)
	var sent int64
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			packet := make([]byte, n+1)
			packet[0] = 0x02
			copy(packet[1:], buf[:n])
			if err := peer.SendFileData(packet); err != nil {
				_ = peer.SendFileData([]byte{0x04})
				setStatus("● Dosya gönderimi kesildi: " + err.Error())
				return
			}
			sent += int64(n)
			pct := int64(100)
			if info.Size() > 0 {
				pct = sent * 100 / info.Size()
			}
			setStatus(fmt.Sprintf("● Gönderiliyor: %s  %d%%", filepath.Base(path), pct))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = peer.SendFileData([]byte{0x04})
			setStatus("● Dosya okuma hatası: " + readErr.Error())
			return
		}
	}
	if err := peer.SendFileData([]byte{0x03}); err != nil {
		setStatus("● Dosya tamamlanamadı: " + err.Error())
		return
	}
	setStatus("● Dosya gönderildi: " + filepath.Base(path))
}

func sendRemoteMonitorList(peer *webrtcpeer.Peer) {
	monitors, err := capture.ListMonitors()
	if err != nil || len(monitors) == 0 {
		return
	}
	b, err := json.Marshal(monitors)
	if err != nil {
		return
	}
	_ = peer.SendControlText("MONITORS:" + string(b))
}

func startCapture(peer *webrtcpeer.Peer, ctx context.Context) {
	go func() {
		if err := peer.WaitScreen(ctx); err != nil {
			if ctx.Err() == nil {
				setStatus("● Ekran kanalı bekleniyor: " + err.Error())
			}
			return
		}
		setStatus("● Ekran kanalı hazır. Ekran aktarılıyor...")
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var seq uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				monitor := int(activeMonitor.Load())
				frame, err := capture.CaptureMonitor(monitor, 60)
				if err != nil {
					setStatus("● Ekran yakalanamadı: " + err.Error())
					return
				}
				if len(frame.JPEG) > screen.MaxPayloadSize {
					frame, err = capture.CaptureMonitor(monitor, 45)
					if err != nil {
						setStatus("● Ekran sıkıştırılamadı: " + err.Error())
						return
					}
				}
				seq++
				err = peer.SendScreenFrame(screen.Frame{
					Monitor: uint16(monitor),
					Seq:     seq,
					Width:   uint32(frame.Width),
					Height:  uint32(frame.Height),
					JPEG:    frame.JPEG,
				})
				if err != nil {
					setStatus("● Ekran DataChannel gönderim hatası: " + err.Error())
					return
				}
			}
		}
	}()
}

func signalingEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("REMOTESUPPORT_SIGNALING_URL")); v != "" {
		return v
	}
	return defaultSignalingURL
}

func newCode() string {
	// The server is authoritative for the actual connection code.
	// This value is only used before Create() returns.
	return "------"
}

func setText(hwnd uintptr, text string) {
	if hwnd != 0 {
		sendMessage.Call(hwnd, 0x000C, 0, uintptr(unsafe.Pointer(utf16ptr(text))))
	}
}

func getText(hwnd uintptr) string {
	if hwnd == 0 {
		return ""
	}
	buf := make([]uint16, 128)
	getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func setStatus(text string) {
	state.mu.Lock()
	h := state.status
	state.mu.Unlock()
	if h != 0 {
		setText(h, text)
	}
}

func setCode(text string) {
	state.mu.Lock()
	h := state.codeLabel
	state.mu.Unlock()
	if h != 0 {
		setText(h, text)
	}
}

func setModeVisible(mode int) {
	state.mu.Lock()
	state.mode = mode
	homeA, homeB := state.action, state.copy
	state.mu.Unlock()
	_ = homeA
	_ = homeB
}

func createControl(parent uintptr, class, text string, style uint32, x, y, w, h, id int) uintptr {
	r, _, _ := createWindow.Call(
		0,
		uintptr(unsafe.Pointer(utf16ptr(class))),
		uintptr(unsafe.Pointer(utf16ptr(text))),
		uintptr(style|0x40000000|0x10000000),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, uintptr(id), moduleHandle(), 0,
	)
	return r
}

func moduleHandle() uintptr {
	r, _, _ := getModule.Call(0)
	return r
}

func hide(hwnd uintptr) {
	if hwnd != 0 {
		showWindow.Call(hwnd, 0) // SW_HIDE
	}
}

func show(hwnd uintptr) {
	if hwnd != 0 {
		showWindow.Call(hwnd, 5) // SW_SHOW
	}
}

func clearChildren() {
	for _, h := range []uintptr{
		state.homeGet, state.homeGive, state.codeLabel, state.remoteEdit, state.action, state.copy, state.back, state.fileSend,
	} {
		hide(h)
	}
}

func home() {
	clearChildren()
	state.mode = 0
	show(state.header)
	show(state.subtitle)
	show(state.status)
	setStatus("Bir işlem seç.")
	show(state.homeGet)
	show(state.homeGive)
}

func supportMode() {
	clearChildren()
	state.mode = 1
	hide(state.homeGet)
	hide(state.homeGive)

	show(state.header)
	show(state.subtitle)
	show(state.status)
	setText(state.action, "Kod oluşturuluyor...")
	show(state.codeLabel)
	show(state.action)
	show(state.copy)
	show(state.back)

	setStatus("Sunucuya bağlanılıyor...")
	go startTarget()
}

func giveMode() {
	clearChildren()
	state.mode = 2
	hide(state.homeGet)
	hide(state.homeGive)

	show(state.header)
	show(state.subtitle)
	show(state.status)
	show(state.remoteEdit)
	show(state.action)
	show(state.back)
	setText(state.action, "BAĞLAN")
	setStatus("Karşı bilgisayarın 6 haneli kodunu girin.")
}

func startTarget() {
	activeMonitor.Store(0)
	remoteMonitorsMu.Lock()
	remoteMonitors = nil
	remoteMonitorsMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	state.cancel = cancel
	state.mu.Unlock()

	c, err := clientsignaling.Dial(ctx, signalingEndpoint())
	if err != nil {
		setStatus("● Sunucuya bağlanılamadı: " + err.Error())
		return
	}

	code, expires, err := c.Create(ctx)
	if err != nil {
		_ = c.Close(context.Background())
		setStatus("● Kod oluşturulamadı: " + err.Error())
		return
	}

	state.mu.Lock()
	state.sig = c
	state.mu.Unlock()

	setCode(formatCode(code))
	setText(state.action, "Kod hazır")
	setStatus(fmt.Sprintf("● Hazır  •  Kod yaklaşık %d saniye geçerli", expires))

	go targetLoop(ctx, c)
}

func targetLoop(ctx context.Context, c *clientsignaling.Client) {
	for {
		msg, err := c.Read(ctx)
		if err != nil {
			setStatus("● Bağlantı kapandı.")
			return
		}

		switch msg.Type {
		case "peer_joined":
			ok := askApproval(msg.Code)
			if err := c.Approve(ctx, ok, ""); err != nil {
				setStatus("● Onay gönderilemedi: " + err.Error())
				return
			}
			if !ok {
				setStatus("Bağlantı isteği reddedildi.")
				continue
			}

			peer, err := newPeer(ctx, c, true)
			if err != nil {
				setStatus("● WebRTC başlatılamadı: " + err.Error())
				return
			}
			state.mu.Lock()
			state.peer = peer
			state.mu.Unlock()

			peer.SetControlHandler(func(msg string) { handleRemoteInput(peer, msg) })
			peer.SetFileHandler(handleRemoteFile)
			setStatus("● İstek onaylandı. Güvenli bağlantı kuruluyor...")
			startCapture(peer, ctx)
			go watchPeer(peer)
		case "signal":
			state.mu.Lock()
			p := state.peer
			state.mu.Unlock()
			if p == nil {
				continue
			}
			sig := webrtcpeer.Signal{Kind: msg.Kind, Payload: msg.Payload}
			switch sig.Kind {
			case "offer":
				answer, err := p.AcceptOffer(ctx, sig)
				if err != nil {
					setStatus("● Teklif işlenemedi: " + err.Error())
					return
				}
				if err := c.Signal(ctx, answer.Kind, answer.Payload); err != nil {
					setStatus("● Cevap gönderilemedi: " + err.Error())
					return
				}
			case "candidate":
				if err := p.AddSignal(sig); err != nil {
					setStatus("● ICE adayı işlenemedi: " + err.Error())
				}
			}
		case "closed":
			setStatus("Bağlantı sonlandırıldı.")
			return
		}
	}
}

func startOperator(code string) {
	activeMonitor.Store(0)
	remoteMonitorsMu.Lock()
	remoteMonitors = nil
	remoteMonitorsMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	state.cancel = cancel
	state.mu.Unlock()

	c, err := clientsignaling.Dial(ctx, signalingEndpoint())
	if err != nil {
		setStatus("● Sunucuya bağlanılamadı: " + err.Error())
		return
	}

	if err := c.Join(ctx, strings.TrimSpace(code)); err != nil {
		_ = c.Close(context.Background())
		setStatus("● Kod kabul edilmedi: " + err.Error())
		return
	}

	state.mu.Lock()
	state.sig = c
	state.mu.Unlock()
	setStatus("● Bağlantı isteği gönderildi. Karşı tarafın onayı bekleniyor...")

	for {
		msg, err := c.Read(ctx)
		if err != nil {
			setStatus("● Bağlantı kurulamadı: " + err.Error())
			return
		}

		switch msg.Type {
		case "approved":
			peer, err := newPeer(ctx, c, true)
			if err != nil {
				setStatus("● WebRTC başlatılamadı: " + err.Error())
				return
			}
			state.mu.Lock()
			state.peer = peer
			state.mu.Unlock()

			peer.SetScreenHandler(setViewerFrame)
			peer.SetControlHandler(func(msg string) { handleRemoteInput(peer, msg) })
			peer.SetFileHandler(handleRemoteFile)

			if err := peer.CreateControl(); err != nil {
				setStatus("● Kontrol kanalı açılamadı: " + err.Error())
				return
			}
			if err := peer.CreateFile(); err != nil {
				setStatus("● Dosya kanalı açılamadı: " + err.Error())
				return
			}
			if err := peer.CreateScreen(); err != nil {
				setStatus("● Ekran kanalı açılamadı: " + err.Error())
				return
			}

			offer, err := peer.Offer(ctx)
			if err != nil {
				setStatus("● WebRTC teklifi oluşturulamadı: " + err.Error())
				return
			}
			if err := c.Signal(ctx, offer.Kind, offer.Payload); err != nil {
				setStatus("● WebRTC teklifi gönderilemedi: " + err.Error())
				return
			}

			setStatus("● Onaylandı. Güvenli WebRTC bağlantısı kuruluyor...")
			go watchPeer(peer)

		case "signal":
			state.mu.Lock()
			p := state.peer
			state.mu.Unlock()
			if p == nil {
				continue
			}
			sig := webrtcpeer.Signal{Kind: msg.Kind, Payload: msg.Payload}
			if err := p.AddSignal(sig); err != nil {
				setStatus("● WebRTC sinyali işlenemedi: " + err.Error())
			}
		case "rejected":
			setStatus("Karşı taraf bağlantı isteğini reddetti.")
			return
		case "closed":
			setStatus("Bağlantı sonlandırıldı.")
			return
		}
	}
}

func newPeer(ctx context.Context, c *clientsignaling.Client, approved bool) (*webrtcpeer.Peer, error) {
	_ = ctx
	return webrtcpeer.New(approved, func(sctx context.Context, sig webrtcpeer.Signal) error {
		return c.Signal(sctx, sig.Kind, sig.Payload)
	})
}

func showConnectedView() {
	hide(state.action)
	hide(state.back)
	hide(state.remoteEdit)
	hide(state.header)
	hide(state.subtitle)
	hide(state.status)
	showViewer()
	show(state.fileSend)
	resizeViewer()
}

func watchPeer(peer *webrtcpeer.Peer) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		st := peer.ConnectionState()
		switch st {
		case webrtc.PeerConnectionStateConnected:
			if host, err := os.Hostname(); err == nil {
				_ = peer.SendControlText("HELLO:" + host)
			}
			if state.mode == 1 {
				sendRemoteMonitorList(peer)
			}
			showConnectedView()
			setStatus("● BAĞLANDI  •  Güvenli WebRTC bağlantısı aktif")
			_ = peer.SendPing()
			return
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			setStatus("● WebRTC bağlantısı kapandı: " + st.String())
			return
		}
	}
}

func askApproval(code string) bool {
	title := utf16ptr(appTitle)
	text := utf16ptr(fmt.Sprintf(
		"Bir bilgisayar bu destek koduyla bağlanmak istiyor.\r\n\r\nKod: %s\r\n\r\nBağlantıya izin verilsin mi?",
		formatCode(code),
	))
	r, _, _ := messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x00000004|0x00000020)
	return r == 6 // IDYES
}

func formatCode(code string) string {
	code = strings.TrimSpace(code)
	if len(code) == 6 {
		return code[:3] + " " + code[3:]
	}
	return code
}

func copyCode() {
	code := strings.ReplaceAll(getText(state.codeLabel), " ", "")
	if code == "" {
		return
	}
	if copyToClipboard(code) {
		setStatus("Kod panoya kopyalandı: " + formatCode(code))
	} else {
		setStatus("Kod: " + formatCode(code) + "  • Karşı bilgisayarda bu kodu girin.")
	}
}

func copyToClipboard(s string) bool {
	if r, _, _ := openClipboard.Call(state.hwnd); r == 0 {
		return false
	}
	defer closeClipboard.Call()
	emptyClipboard.Call()

	data := syscall.StringToUTF16(s)
	size := uintptr(len(data) * 2)
	h, _, _ := globalAlloc.Call(0x0002, size) // GMEM_MOVEABLE
	if h == 0 {
		return false
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		return false
	}
	copyBytes(unsafe.Pointer(p), unsafe.Pointer(&data[0]), int(size))
	globalUnlock.Call(h)
	if r, _, _ := setClipboardData.Call(13, h); r == 0 { // CF_UNICODETEXT
		return false
	}
	return true
}

func copyBytes(dst, src unsafe.Pointer, n int) {
	d := unsafe.Slice((*byte)(dst), n)
	s := unsafe.Slice((*byte)(src), n)
	copy(d, s)
}

func readClipboardText() (string, bool) {
	if state.hwnd == 0 {
		return "", false
	}
	if r, _, _ := openClipboard.Call(state.hwnd); r == 0 {
		return "", false
	}
	defer closeClipboard.Call()
	h, _, _ := user32.NewProc("GetClipboardData").Call(13) // CF_UNICODETEXT
	if h == 0 {
		return "", false
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer globalUnlock.Call(h)
	// CF_UNICODETEXT is UTF-16 and NUL terminated. Bound the read so a
	// malformed clipboard cannot make us scan unbounded memory.
	const maxClipboardRunes = 32768
	buf := unsafe.Slice((*uint16)(unsafe.Pointer(p)), maxClipboardRunes)
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(buf[:n]), true
}

func sendClipboardToPeer(peer *webrtcpeer.Peer) bool {
	text, ok := readClipboardText()
	if !ok {
		return false
	}
	data := []byte(text)
	if len(data) > 48*1024 {
		setStatus("● Pano metni 48 KB sınırını aşıyor.")
		return false
	}
	payload := base64.StdEncoding.EncodeToString(data)
	return peer.SendControlText("CLIP:"+payload) == nil
}

func requestRemoteClipboard(peer *webrtcpeer.Peer) bool {
	return peer.SendControlText("CLIP_REQ") == nil
}

func normalizeCode(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, " ", "")
	if len(s) != 6 {
		return ""
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return s
}

func clearMonitorButtons() {
	state.mu.Lock()
	buttons := append([]uintptr(nil), state.monitorButtons...)
	state.monitorButtons = nil
	state.mu.Unlock()
	for _, h := range buttons {
		if h != 0 {
			destroyWindow.Call(h)
		}
	}
}

func updateMonitorButtons(count int) {
	if count < 0 {
		count = 0
	}
	clearMonitorButtons()
	if count == 0 {
		resizeViewer()
		return
	}

	buttons := make([]uintptr, 0, count)
	const buttonW = 112
	const buttonH = 30
	const gap = 8
	for i := 0; i < count; i++ {
		x := 20 + i*(buttonW+gap)
		text := fmt.Sprintf("Monitör %d", i+1)
		h := createControl(state.hwnd, "BUTTON", text, 0x00000001, x, 20, buttonW, buttonH, ID_MONITOR_BASE+i)
		buttons = append(buttons, h)
	}
	state.mu.Lock()
	state.monitorButtons = buttons
	fileBtn := state.fileSend
	state.mu.Unlock()
	if fileBtn != 0 {
		show(fileBtn)
		setWindowPos.Call(fileBtn, 0, 620, 20, 120, 30, 0)
	}
	resizeViewer()
}

func selectMonitor(index int) {
	remoteMonitorsMu.RLock()
	count := len(remoteMonitors)
	remoteMonitorsMu.RUnlock()
	if index < 0 || index >= count {
		return
	}
	activeMonitor.Store(int32(index))
	state.mu.Lock()
	peer := state.peer
	state.mu.Unlock()
	if peer != nil {
		_ = peer.SendControlText(fmt.Sprintf("MONITOR:%d", index))
	}
	viewer.mu.Lock()
	viewer.pixels = nil
	viewer.width = 0
	viewer.height = 0
	viewer.fit = true
	viewer.zoom = 1
	viewer.panX = 0
	viewer.panY = 0
	viewer.clearNeeded = true
	viewer.mu.Unlock()
	if viewer.hwnd != 0 {
		invalidateRect.Call(viewer.hwnd, 0, 0)
	}
	setStatus(fmt.Sprintf("● Monitör %d/%d isteniyor...", index+1, count))
}

func onCommand(id int) {
	if id >= ID_MONITOR_BASE {
		selectMonitor(id - ID_MONITOR_BASE)
		return
	}
	switch id {
	case ID_GET_SUPPORT:
		supportMode()
	case ID_GIVE_SUPPORT:
		giveMode()
	case ID_BACK:
		stopSession()
		home()
	case ID_COPY_CODE:
		copyCode()
	case ID_FILE_SEND:
		go sendFileToPeer()
	case ID_CONNECT:
		code := normalizeCode(getText(state.remoteEdit))
		if code == "" {
			setStatus("6 haneli sayısal bağlantı kodu gerekli.")
			return
		}
		setText(state.action, "BAĞLANIYOR...")
		setStatus("● Bağlantı kodu doğrulanıyor...")
		go startOperator(code)
	}
}

func stopSession() {
	hideViewer()
	clearMonitorButtons()
	state.mu.Lock()
	cancel := state.cancel
	sig := state.sig
	peer := state.peer
	state.cancel = nil
	state.sig = nil
	state.peer = nil
	state.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if peer != nil {
		_ = peer.Close()
	}
	if sig != nil {
		_ = sig.Close(context.Background())
	}
}

func wndProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	switch m {
	case 0x0002: // WM_DESTROY
		stopSession()
		postQuit.Call(0)
		return 0
	case 0x0111: // WM_COMMAND
		onCommand(int(wParam & 0xffff))
		return 0
	case WM_APP_MONITORS:
		count := int(wParam)
		updateMonitorButtons(count)
		setStatus(fmt.Sprintf("● Uzak bilgisayarda %d monitör bulundu", count))
		return 0
	case 0x0005: // WM_SIZE
		resizeViewer()
		return 0
	case 0x000F: // WM_PAINT
		r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
		return r
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

func main() {
	runtime.LockOSThread()

	class := utf16ptr(windowClass)
	wc := wndclassex{
		CbSize:        uint32(unsafe.Sizeof(wndclassex{})),
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     moduleHandle(),
		HCursor:       func() uintptr { r, _, _ := loadCursor.Call(0, 32512); return r }(),
		HbrBackground: 6, // COLOR_WINDOW + 1
		LpszClassName: class,
	}
	registerClass.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := createWindow.Call(
		0,
		uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(utf16ptr(appTitle))),
		0x00CF0000|0x02000000, // WS_CLIPCHILDREN
		200, 120, windowWidth, windowHeight,
		0, 0, moduleHandle(), 0,
	)
	if hwnd == 0 {
		return
	}
	state.hwnd = hwnd
	registerViewerClass()
	createViewer(hwnd)
	hideViewer()

	state.header = createControl(hwnd, "STATIC", "Remote Support", 0x00000000, 40, 30, 650, 45, 0)
	state.subtitle = createControl(hwnd, "STATIC", "Güvenli uzaktan destek", 0x00000000, 40, 72, 650, 28, 0)
	state.status = createControl(hwnd, "STATIC", "Bir işlem seç.", 0, 40, 465, 650, 35, 0)

	getSupport := createControl(hwnd, "BUTTON", "DESTEK ALIYORUM", 0x00000001, 80, 185, 280, 70, ID_GET_SUPPORT)
	giveSupport := createControl(hwnd, "BUTTON", "DESTEK VERİYORUM", 0x00000001, 400, 185, 280, 70, ID_GIVE_SUPPORT)
	state.homeGet = getSupport
	state.homeGive = giveSupport
	state.action = createControl(hwnd, "BUTTON", "BAĞLAN", 0x00000001, 250, 300, 260, 52, ID_CONNECT)
	state.copy = createControl(hwnd, "BUTTON", "Kodu kopyala", 0x00000001, 520, 300, 160, 52, ID_COPY_CODE)
	state.back = createControl(hwnd, "BUTTON", "← Geri", 0x00000001, 40, 300, 120, 52, ID_BACK)
	state.fileSend = createControl(hwnd, "BUTTON", "Dosya Gönder", 0x00000001, 620, 20, 110, 30, ID_FILE_SEND)
	hide(state.fileSend)

	state.codeLabel = createControl(hwnd, "STATIC", newCode(), 0x00000001|0x00000200|0x00000080, 180, 185, 400, 60, 0)
	state.remoteEdit = createControl(hwnd, "EDIT", "", 0x00000001|0x00002000, 170, 185, 420, 50, ID_REMOTE_CODE)

	hide(state.action)
	hide(state.copy)
	hide(state.back)
	hide(state.codeLabel)
	hide(state.remoteEdit)

	show(state.header)
	show(state.subtitle)
	show(state.status)
	show(state.homeGet)
	show(state.homeGive)

	showWindow.Call(hwnd, 1)
	updateWindow.Call(hwnd)

	var m msg
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		translateMsg.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// Keep the package dependency explicit in this Windows-only client. The peer
// package exposes Pion's state values through ConnectionState().
var _ = webrtc.PeerConnectionStateConnected
var _ = destroyWindow
