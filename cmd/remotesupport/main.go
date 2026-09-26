//go:build windows

package main

import (
	"crypto/rand"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_CHILD            = 0x40000000
	WS_VISIBLE           = 0x10000000
	WS_BORDER             = 0x00800000
	WS_TABSTOP            = 0x00010000
	ES_CENTER             = 0x0001
	ES_READONLY           = 0x0800
	BS_PUSHBUTTON         = 0
	BS_DEFPUSHBUTTON      = 1
	SS_LEFT               = 0
	WM_CREATE             = 1
	WM_SIZE               = 5
	WM_COMMAND            = 0x0111
	WM_CLOSE              = 0x0010
	WM_DESTROY            = 2
	WM_SETFONT            = 0x0030
	CF_UNICODETEXT        = 13
	GMEM_MOVEABLE         = 2
	SW_HIDE               = 0
	SW_SHOW               = 5

	ID_GET_SUPPORT        = 1001
	ID_GIVE_SUPPORT       = 1002
	ID_COPY_CODE          = 1003
	ID_CONNECT            = 1005
	ID_BACK               = 1006
)

type msg struct {
	Hwnd uintptr
	Message uint32
	WParam uintptr
	LParam uintptr
	Time uint32
	Pt struct{ X, Y int32 }
}

type wndClassEx struct {
	Size uint32
	Style uint32
	WndProc uintptr
	ClsExtra int32
	WndExtra int32
	Instance uintptr
	Icon uintptr
	Cursor uintptr
	Background uintptr
	MenuName *uint16
	ClassName *uint16
	IconSm uintptr
}

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32 = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	registerClassExW = user32.NewProc("RegisterClassExW")
	createWindowExW = user32.NewProc("CreateWindowExW")
	defWindowProcW = user32.NewProc("DefWindowProcW")
	showWindow = user32.NewProc("ShowWindow")
	updateWindow = user32.NewProc("UpdateWindow")
	getMessageW = user32.NewProc("GetMessageW")
	translateMessage = user32.NewProc("TranslateMessage")
	dispatchMessageW = user32.NewProc("DispatchMessageW")
	postQuitMessage = user32.NewProc("PostQuitMessage")
	loadCursorW = user32.NewProc("LoadCursorW")
	sendMessageW = user32.NewProc("SendMessageW")
	setWindowTextW = user32.NewProc("SetWindowTextW")
	setWindowPos = user32.NewProc("SetWindowPos")
	enableWindow = user32.NewProc("EnableWindow")
	openClipboard = user32.NewProc("OpenClipboard")
	closeClipboard = user32.NewProc("CloseClipboard")
	emptyClipboard = user32.NewProc("EmptyClipboard")
	setClipboardData = user32.NewProc("SetClipboardData")
	getStockObject = gdi32.NewProc("GetStockObject")
	getModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	globalAlloc = kernel32.NewProc("GlobalAlloc")
	globalLock = kernel32.NewProc("GlobalLock")
	globalUnlock = kernel32.NewProc("GlobalUnlock")
	messageBoxW = user32.NewProc("MessageBoxW")
)

var (
	hInstance uintptr
	connectionCode string
	mode int

	header, subtitle uintptr
	getSupportBtn, giveSupportBtn uintptr
	codeLabel, codeEdit, copyBtn uintptr
	remoteLabel, remoteEdit, connectBtn uintptr
	backBtn, status uintptr
)

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func newCode() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "482731"
	}
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
	return fmt.Sprintf("%06d", n)
}

func control(parent uintptr, class, text string, style uint32, x, y, w, h int32, id uintptr) uintptr {
	r, _, _ := createWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(u16(class))),
		uintptr(unsafe.Pointer(u16(text))),
		uintptr(style|WS_CHILD|WS_VISIBLE),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, id, hInstance, 0,
	)
	return r
}

func setFont(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	font, _, _ := getStockObject.Call(17)
	if font != 0 {
		sendMessageW.Call(hwnd, WM_SETFONT, font, 1)
	}
}

func showControl(hwnd uintptr, visible bool) {
	if hwnd == 0 {
		return
	}
	if visible {
		showWindow.Call(hwnd, SW_SHOW)
	} else {
		showWindow.Call(hwnd, SW_HIDE)
	}
}

func move(hwnd uintptr, x, y, w, h int32) {
	if hwnd != 0 {
		setWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0x0004)
	}
}

func setStatus(s string) {
	setWindowTextW.Call(status, uintptr(unsafe.Pointer(u16(s))))
}

func hideAll() {
	for _, h := range []uintptr{
		getSupportBtn, giveSupportBtn,
		codeLabel, codeEdit, copyBtn,
		remoteLabel, remoteEdit, connectBtn,
		backBtn, status,
	} {
		showControl(h, false)
	}
}

func home() {
	hideAll()
	showControl(getSupportBtn, true)
	showControl(giveSupportBtn, true)
	showControl(status, true)

	move(getSupportBtn, 80, 190, 250, 80)
	move(giveSupportBtn, 370, 190, 250, 80)
	move(status, 80, 330, 540, 28)
	setStatus("Bir işlem seç.")
}

func supportMode() {
	hideAll()
	for _, h := range []uintptr{codeLabel, codeEdit, copyBtn, backBtn, status} {
		showControl(h, true)
	}

	move(codeLabel, 70, 120, 560, 28)
	move(codeEdit, 70, 160, 560, 72)
	move(copyBtn, 70, 250, 180, 38)
	move(backBtn, 70, 430, 150, 38)
	move(status, 70, 385, 560, 28)
	setStatus("Bu kodu destek verecek kişiye gönder.")
}

func giveMode() {
	hideAll()
	for _, h := range []uintptr{remoteLabel, remoteEdit, connectBtn, backBtn, status} {
		showControl(h, true)
	}

	move(remoteLabel, 70, 120, 560, 28)
	move(remoteEdit, 70, 160, 560, 58)
	move(connectBtn, 70, 235, 180, 38)
	move(backBtn, 70, 430, 150, 38)
	move(status, 70, 285, 560, 28)
	setStatus("Karşı tarafın verdiği 6 haneli kodu gir.")
}

func copyCode(hwnd uintptr) {
	if r, _, _ := openClipboard.Call(hwnd); r == 0 {
		setStatus("Panoya erişilemedi.")
		return
	}
	defer closeClipboard.Call()
	emptyClipboard.Call()

	raw, _ := syscall.UTF16FromString(connectionCode)
	mem, _, _ := globalAlloc.Call(GMEM_MOVEABLE, uintptr(len(raw)*2))
	if mem == 0 {
		setStatus("Kod kopyalanamadı.")
		return
	}
	ptr, _, _ := globalLock.Call(mem)
	if ptr == 0 {
		setStatus("Kod kopyalanamadı.")
		return
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(raw))
	copy(dst, raw)
	globalUnlock.Call(mem)
	setClipboardData.Call(CF_UNICODETEXT, mem)
	setStatus("Kod kopyalandı. Karşı tarafa gönderebilirsin.")
}

func readEdit(hwnd uintptr) string {
	buf := make([]uint16, 64)
	r, _, _ := sendMessageW.Call(hwnd, 0x000D, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:r])
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch uint32(message) {
	case WM_CREATE:
		header = control(hwnd, "STATIC", "Remote Support", SS_LEFT, 40, 28, 600, 32, 0)
		subtitle = control(hwnd, "STATIC", "Güvenli uzaktan destek", SS_LEFT, 40, 62, 600, 24, 0)

		getSupportBtn = control(hwnd, "BUTTON", "DESTEK ALIYORUM", WS_TABSTOP|BS_DEFPUSHBUTTON, 80, 190, 250, 80, ID_GET_SUPPORT)
		giveSupportBtn = control(hwnd, "BUTTON", "DESTEK VERİYORUM", WS_TABSTOP|BS_PUSHBUTTON, 370, 190, 250, 80, ID_GIVE_SUPPORT)

		codeLabel = control(hwnd, "STATIC", "Bu kodu destek verecek kişiye gönder:", SS_LEFT, 70, 120, 560, 28, 0)
		codeEdit = control(hwnd, "EDIT", connectionCode[:3]+"  "+connectionCode[3:], WS_BORDER|ES_CENTER|ES_READONLY, 70, 160, 560, 72, 0)
		copyBtn = control(hwnd, "BUTTON", "Kodu Kopyala", WS_TABSTOP|BS_PUSHBUTTON, 70, 250, 180, 38, ID_COPY_CODE)

		remoteLabel = control(hwnd, "STATIC", "Karşı tarafın bağlantı kodunu gir:", SS_LEFT, 70, 120, 560, 28, 0)
		remoteEdit = control(hwnd, "EDIT", "", WS_BORDER|ES_CENTER, 70, 160, 560, 58, 0)
		connectBtn = control(hwnd, "BUTTON", "Bağlan", WS_TABSTOP|BS_DEFPUSHBUTTON, 70, 235, 180, 38, ID_CONNECT)

		backBtn = control(hwnd, "BUTTON", "← Geri", WS_TABSTOP|BS_PUSHBUTTON, 70, 430, 150, 38, ID_BACK)
		status = control(hwnd, "STATIC", "", SS_LEFT, 70, 285, 560, 28, 0)

		for _, h := range []uintptr{
			header, subtitle, getSupportBtn, giveSupportBtn,
			codeLabel, codeEdit, copyBtn, remoteLabel, remoteEdit,
			connectBtn, backBtn, status,
		} {
			setFont(h)
		}
		home()
		return 0

	case WM_SIZE:
		width := int32(uint16(lParam))
		height := int32(uint16(lParam >> 16))
		if width < 680 {
			width = 680
		}
		if height < 500 {
			height = 500
		}
		margin := int32(40)
		move(header, margin, 28, width-80, 32)
		move(subtitle, margin, 62, width-80, 24)
		return 0

	case WM_COMMAND:
		if uint32(wParam>>16) == 0 {
			switch uintptr(uint32(wParam)) {
			case ID_GET_SUPPORT:
				mode = 1
				supportMode()
			case ID_GIVE_SUPPORT:
				mode = 2
				giveMode()
			case ID_COPY_CODE:
				copyCode(hwnd)
			case ID_CONNECT:
				if len(readEdit(remoteEdit)) != 6 {
					setStatus("6 haneli bağlantı kodu gir.")
				} else {
					setStatus("● Bağlantı isteği gönderiliyor...")
				}
			case ID_BACK:
				mode = 0
				home()
			}
		}
	case WM_CLOSE:
		postQuitMessage.Call(0)
		return 0
	case WM_DESTROY:
		postQuitMessage.Call(0)
		return 0
	}

	r, _, _ := defWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}

func main() {
	connectionCode = newCode()
	hInstance, _, _ = getModuleHandleW.Call(0)

	className := u16("RemoteSupportUI12")
	cursor, _, _ := loadCursorW.Call(0, 32512)

	wc := wndClassEx{
		Size: uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc: syscall.NewCallback(wndProc),
		Instance: hInstance,
		Cursor: cursor,
		Background: 16,
		ClassName: className,
	}

	if r, _, _ := registerClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		messageBoxW.Call(0, uintptr(unsafe.Pointer(u16("Pencere sınıfı oluşturulamadı."))), uintptr(unsafe.Pointer(u16("Remote Support"))), 0x10)
		return
	}

	hwnd, _, _ := createWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(u16("Remote Support"))),
		WS_OVERLAPPEDWINDOW,
		0x80000000, 0x80000000, 720, 560,
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		messageBoxW.Call(0, uintptr(unsafe.Pointer(u16("Ana pencere oluşturulamadı."))), uintptr(unsafe.Pointer(u16("Remote Support"))), 0x10)
		return
	}

	showWindow.Call(hwnd, SW_SHOW)
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
}
