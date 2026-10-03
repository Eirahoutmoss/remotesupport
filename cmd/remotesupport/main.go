//go:build windows

package main

import (
	"syscall"
)

const (
	appTitle            = "NexDesk"
	defaultSignalingURL = ""
	windowClass         = "RemoteSupportWindow"
	viewerClass         = "RemoteSupportViewer"
	windowWidth         = 1180
	windowHeight        = 760

	ID_GET_SUPPORT        = 1001
	ID_GIVE_SUPPORT       = 1002
	ID_BACK               = 1003
	ID_COPY_CODE          = 1004
	ID_REMOTE_CODE        = 1005
	ID_CONNECT            = 1006
	ID_SETTINGS           = 1007
	ID_ABOUT              = 1008
	ID_SETTINGS_SAVE      = 1009
	ID_SETTINGS_RESET     = 1010
	ID_MONITOR_BASE       = 2000
	ID_FILE_SEND          = 3001
	ID_RES_ORIGINAL       = 4000
	ID_RES_1080P          = 4001
	ID_RES_720P           = 4002
	ID_RES_480P           = 4003
	ID_FULLSCREEN         = 5001
	ID_ELEVATE            = 5002
	ID_CLIP_SEND          = 5003
	ID_CLIP_GET           = 5004
	ID_CHAT_SEND          = 5005
	ID_STAY_OPEN          = 5006
	ID_ACTIONS            = 5007
	ID_SHOT               = 5008
	ID_KEY_WIN            = 5009
	ID_KEY_ALTTAB         = 5010
	ID_KEY_ESC            = 5011
	ID_CURTAIN            = 5012
	ID_LOCKINPUT          = 5013
	ID_SYSINFO            = 5014
	ID_ANNOT              = 5015
	ID_SHARE_LINK         = 5016
	ID_FILE_CANCEL        = 5017
	ID_DEVICE_SAVE        = 5018
	ID_TECH_DEV           = 5019
	ID_SAVE_REMOTE_DEVICE = 5020
	ID_ABOUT_THEME        = 5021
	ID_TECH_DEV_MANAGE    = 5022
	ID_ABOUT_UPDATE       = 5023
	WM_APP_MONITORS       = 0x8001
	WM_APP_IDLE           = 0x8002
	WM_APP_IDLE_CLOSE     = 0x8003
	WM_APP_CURTAIN        = 0x8004
	WM_APP_ANNOT          = 0x8005
)

var (
	gdi32DLL            = syscall.NewLazyDLL("gdi32.dll")
	comdlg32            = syscall.NewLazyDLL("comdlg32.dll")
	user32              = syscall.NewLazyDLL("user32.dll")
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	shell32             = syscall.NewLazyDLL("shell32.dll")
	shellExecuteW       = shell32.NewProc("ShellExecuteW")
	registerClass       = user32.NewProc("RegisterClassExW")
	createWindow        = user32.NewProc("CreateWindowExW")
	defWindowProc       = user32.NewProc("DefWindowProcW")
	showWindow          = user32.NewProc("ShowWindow")
	setWindowPos        = user32.NewProc("SetWindowPos")
	getWindowRect       = user32.NewProc("GetWindowRect")
	getWindowLongPtr    = user32.NewProc("GetWindowLongPtrW")
	setWindowLongPtr    = user32.NewProc("SetWindowLongPtrW")
	updateWindow        = user32.NewProc("UpdateWindow")
	getMessage          = user32.NewProc("GetMessageW")
	translateMsg        = user32.NewProc("TranslateMessage")
	dispatchMessage     = user32.NewProc("DispatchMessageW")
	postQuit            = user32.NewProc("PostQuitMessage")
	sendMessage         = user32.NewProc("SendMessageW")
	postMessage         = user32.NewProc("PostMessageW")
	setWindowText       = user32.NewProc("SetWindowTextW")
	getWindowText       = user32.NewProc("GetWindowTextW")
	destroyWindow       = user32.NewProc("DestroyWindow")
	messageBox          = user32.NewProc("MessageBoxW")
	getModule           = kernel32.NewProc("GetModuleHandleW")
	globalMemStatus     = kernel32.NewProc("GlobalMemoryStatusEx")
	getDiskFreeSpace    = kernel32.NewProc("GetDiskFreeSpaceExW")
	loadCursor          = user32.NewProc("LoadCursorW")
	loadImage           = user32.NewProc("LoadImageW")
	openClipboard       = user32.NewProc("OpenClipboard")
	emptyClipboard      = user32.NewProc("EmptyClipboard")
	setClipboardData    = user32.NewProc("SetClipboardData")
	closeClipboard      = user32.NewProc("CloseClipboard")
	beginPaint          = user32.NewProc("BeginPaint")
	endPaint            = user32.NewProc("EndPaint")
	fillRect            = user32.NewProc("FillRect")
	getClientRect       = user32.NewProc("GetClientRect")
	getCursorPos        = user32.NewProc("GetCursorPos")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	appendMenuW         = user32.NewProc("AppendMenuW")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	destroyMenu         = user32.NewProc("DestroyMenu")
	setWinDisplayAff    = user32.NewProc("SetWindowDisplayAffinity")
	blockInput          = user32.NewProc("BlockInput")
	setLayeredWinAttr   = user32.NewProc("SetLayeredWindowAttributes")
	screenToClient      = user32.NewProc("ScreenToClient")
	getSystemMetrics    = user32.NewProc("GetSystemMetrics")
	sendInput           = user32.NewProc("SendInput")
	setFocus            = user32.NewProc("SetFocus")
	invalidateRect      = user32.NewProc("InvalidateRect")
	registerClassEx     = user32.NewProc("RegisterClassExW")
	stretchDIBits       = gdi32DLL.NewProc("StretchDIBits")
	setStretchMode      = gdi32DLL.NewProc("SetStretchBltMode")
	getStockObject      = gdi32DLL.NewProc("GetStockObject")
	createPen           = gdi32DLL.NewProc("CreatePen")
	moveToEx            = gdi32DLL.NewProc("MoveToEx")
	lineTo              = gdi32DLL.NewProc("LineTo")
	selectObject        = gdi32DLL.NewProc("SelectObject")
	deleteObject        = gdi32DLL.NewProc("DeleteObject")
	roundRect           = gdi32DLL.NewProc("RoundRect")
	drawText            = user32.NewProc("DrawTextW")
	mapVirtualKey       = user32.NewProc("MapVirtualKeyW")
	createFont          = gdi32DLL.NewProc("CreateFontW")
	createSolidBrush    = gdi32DLL.NewProc("CreateSolidBrush")
	setTextColor        = gdi32DLL.NewProc("SetTextColor")
	setBkColor          = gdi32DLL.NewProc("SetBkColor")
	setBkMode           = gdi32DLL.NewProc("SetBkMode")
	patBlt              = gdi32DLL.NewProc("PatBlt")
	ellipse             = gdi32DLL.NewProc("Ellipse")
	setCapture          = user32.NewProc("SetCapture")
	releaseCapture      = user32.NewProc("ReleaseCapture")
	globalAlloc         = kernel32.NewProc("GlobalAlloc")
	globalLock          = kernel32.NewProc("GlobalLock")
	globalUnlock        = kernel32.NewProc("GlobalUnlock")
	getOpenFileName     = comdlg32.NewProc("GetOpenFileNameW")
	isDialogMessage     = user32.NewProc("IsDialogMessageW")
	commDlgError        = comdlg32.NewProc("CommDlgExtendedError")
	messageBeep         = user32.NewProc("MessageBeep")
	flashWindow         = user32.NewProc("FlashWindow")
	callWindowProc      = user32.NewProc("CallWindowProcW")
	trackMouseEventProc = user32.NewProc("TrackMouseEvent")
)
