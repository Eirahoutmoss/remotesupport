//go:build windows

package main

import (
	"crypto/rand"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	wsOverlappedWindow = 0x00000000
	wsVisible          = 0x10000000
	wsChild            = 0x40000000
	wsBorder           = 0x00800000
	wsTabStop          = 0x00010000
	wsGroup            = 0x00020000
	wsVScroll          = 0x00200000
	wsExClientEdge     = 0x00000200

	swShow = 5

	wmCreate  = 0x0001
	wmDestroy = 0x0002
	wmClose   = 0x0010
	wmCommand = 0x0111
	wmSetFont = 0x0030

	bnClicked = 0

	cfUnicodeText = 13
	gmemMoveable  = 0x0002

	idCopy       = 1001
	idAccept     = 1002
	idDisconnect = 1003
	idStatus     = 1004

	bsPushButton = 0x00000000
	bsDefPushButton = 0x00000001

	esCenter    = 0x0001
	esReadOnly  = 0x0800
	esAutoHScroll = 0x0080

	mbOk        = 0x00000000
	mbIconInfo  = 0x00000040
	mbIconWarn  = 0x00000030

	colorWindow = 5
	colorBtnFace = 15

	errorClassExists = 1410
)

type point struct {
	X int32
	Y int32
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type rect struct {
	Left, Top, Right, Bottom int32
}

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	registerClassExW = user32.NewProc("RegisterClassExW")
	createWindowExW  = user32.NewProc("CreateWindowExW")
	defWindowProcW   = user32.NewProc("DefWindowProcW")
	showWindow       = user32.NewProc("ShowWindow")
	updateWindow     = user32.NewProc("UpdateWindow")
	getMessageW      = user32.NewProc("GetMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessageW = user32.NewProc("DispatchMessageW")
	postQuitMessage  = user32.NewProc("PostQuitMessage")
	loadCursorW      = user32.NewProc("LoadCursorW")
	messageBoxW      = user32.NewProc("MessageBoxW")
	sendMessageW     = user32.NewProc("SendMessageW")
	setWindowTextW   = user32.NewProc("SetWindowTextW")
	enableWindow     = user32.NewProc("EnableWindow")
	openClipboard    = user32.NewProc("OpenClipboard")
	closeClipboard   = user32.NewProc("CloseClipboard")
	emptyClipboard   = user32.NewProc("EmptyClipboard")
	setClipboardData = user32.NewProc("SetClipboardData")
	globalAlloc      = kernel32.NewProc("GlobalAlloc")
	globalLock       = kernel32.NewProc("GlobalLock")
	globalUnlock     = kernel32.NewProc("GlobalUnlock")
)

var (
	hInstance uintptr
	hwndStatus uintptr
	hwndAccept uintptr
	hwndDisconnect uintptr
	connectionCode string
	connected bool
)

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func newCode() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		n := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
		n %= 1000000
		return fmt.Sprintf("%06d", n)
	}
	return "482731"
}

func makeControl(class, text string, style uint32, x, y, w, h int32, id uintptr) uintptr {
	r, _, _ := createWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(u16(class))),
		uintptr(unsafe.Pointer(u16(text))),
		uintptr(style|wsChild|wsVisible),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		hInstance,
		id,
		hInstance,
		0,
	)
	return r
}

func setStatus(text string) {
	if hwndStatus != 0 {
		setWindowTextW.Call(hwndStatus, uintptr(unsafe.Pointer(u16(text))))
	}
}

func copyCode(hwnd uintptr) {
	if openClipboard.Call(hwnd) == 0 {
		messageBoxW.Call(hwnd, uintptr(unsafe.Pointer(u16("Panoya erişilemedi."))), uintptr(unsafe.Pointer(u16("Remote Support"))), mbIconWarn|mbOk)
		return
	}
	defer closeClipboard.Call()

	emptyClipboard.Call()

	raw, _ := syscall.UTF16FromString(connectionCode)
	size := uintptr(len(raw) * 2)
	mem, _, _ := globalAlloc.Call(gmemMoveable, size)
	if mem == 0 {
		return
	}
	ptr, _, _ := globalLock.Call(mem)
	if ptr == 0 {
		return
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(raw))
	copy(dst, raw)
	globalUnlock.Call(mem)

	if setClipboardData.Call(cfUnicodeText, mem) == 0 {
		return
	}
	setStatus("● Kod panoya kopyalandı")
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch uint32(message) {
	case wmCreate:
		makeControl("STATIC", "REMOTE SUPPORT", wsVisible, 28, 22, 430, 28, 0)
		makeControl("STATIC", "Bağlantı kodunuz", wsVisible, 28, 68, 430, 24, 0)

		code := makeControl("EDIT", connectionCode, wsBorder|esCenter|esReadOnly|esAutoHScroll, 28, 98, 430, 52, 0)
		_ = code
		copyBtn := makeControl("BUTTON", "Kodu Kopyala", wsTabStop|bsPushButton, 28, 166, 136, 34, idCopy)
		_ = copyBtn

		makeControl("STATIC", "Bağlantı isteği geldiğinde açıkça onaylamadan", wsVisible, 28, 218, 430, 22, 0)
		makeControl("STATIC", "uzak bağlantı başlatılmaz.", wsVisible, 28, 240, 430, 22, 0)

		hwndAccept = makeControl("BUTTON", "Bağlantıyı Kabul Et", wsTabStop|bsDefPushButton, 28, 284, 190, 38, idAccept)
		hwndDisconnect = makeControl("BUTTON", "Bağlantıyı Kes", wsTabStop|bsPushButton, 228, 284, 160, 38, idDisconnect)
		enableWindow.Call(hwndDisconnect, 0)

		hwndStatus = makeControl("STATIC", "● Hazır", wsVisible, 28, 342, 430, 26, idStatus)

	case wmCommand:
		id := uintptr(uint32(wParam) & 0xffff)
		notify := uint32(wParam >> 16)
		if notify == bnClicked {
			switch id {
			case idCopy:
				copyCode(hwnd)
			case idAccept:
				connected = true
				enableWindow.Call(hwndAccept, 0)
				enableWindow.Call(hwndDisconnect, 1)
				setStatus("● Bağlantı kabul edildi")
			case idDisconnect:
				connected = false
				enableWindow.Call(hwndAccept, 1)
				enableWindow.Call(hwndDisconnect, 0)
				setStatus("● Bağlantı kesildi")
			}
		}
	case wmClose, wmDestroy:
		if connected {
			connected = false
		}
		postQuitMessage.Call(0)
		return 0
	}
	r, _, _ := defWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}

func main() {
	connectionCode = newCode()
	hInstance, _, _ = kernel32.NewProc("GetModuleHandleW").Call(0)

	className := u16("RemoteSupportMainWindow")
	wc := wndClassEx{
		Size:       uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:    syscall.NewCallback(wndProc),
		Instance:   hInstance,
		Background: colorWindow + 1,
		ClassName:  className,
	}
	cursor, _, _ := loadCursorW.Call(0, 32512)
	wc.Cursor = cursor

	if r, _, e := registerClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 && e != syscall.Errno(errorClassExists) {
		messageBoxW.Call(0, uintptr(unsafe.Pointer(u16("Pencere sınıfı oluşturulamadı."))), uintptr(unsafe.Pointer(u16("Remote Support"))), mbIconWarn|mbOk)
		return
	}

	hwnd, _, _ := createWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(u16("Remote Support"))),
		wsOverlappedWindow|wsVisible,
		0x80000000, 0x80000000, 500, 430,
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		return
	}
	showWindow.Call(hwnd, swShow)
	updateWindow.Call(hwnd)

	var m msg
	for {
		r, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	_ = sendMessageW
}
