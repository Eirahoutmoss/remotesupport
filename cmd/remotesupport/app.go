//go:build windows

package main

import (
	"runtime"
	"syscall"
	"unsafe"

	"github.com/pion/webrtc/v4"
)

func addComboItems(hwnd uintptr, items ...string) {
	for _, item := range items {
		sendMessage.Call(hwnd, 0x0143, 0, uintptr(unsafe.Pointer(utf16ptr(item))))
	}
}

func main() {
	runtime.LockOSThread()
	loadSettings()
	loadBranding()
	loadRecent()
	initUITheme()
	loadLogo()

	class := utf16ptr(windowClass)
	wc := wndclassex{
		CbSize:        uint32(unsafe.Sizeof(wndclassex{})),
		LpfnWndProc:   syscall.NewCallback(wndProc),
		HInstance:     moduleHandle(),
		HCursor:       func() uintptr { r, _, _ := loadCursor.Call(0, 32512); return r }(),
		HbrBackground: 0, // parent paints the background in WM_PAINT
		LpszClassName: class,
		HIcon:         appIcon(32),
		HIconSm:       appIcon(16),
	}
	registerClass.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := createWindow.Call(
		0,
		uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(utf16ptr(brandName()))),
		0x00CF0000|0x02000000, // WS_CLIPCHILDREN
		200, 120, windowWidth, windowHeight,
		0, 0, moduleHandle(), 0,
	)
	if hwnd == 0 {
		return
	}
	state.hwnd = hwnd
	dragAcceptFiles.Call(hwnd, 1)
	// Let Explorer drop files even when NexDesk runs elevated (UIPI).
	for _, m := range []uintptr{wmDropFiles, 0x004A, 0x0049} {
		user32.NewProc("ChangeWindowMessageFilterEx").Call(hwnd, m, 1, 0)
	}
	registerViewerClass()
	createViewer(hwnd)
	hideViewer()

	state.header = createControl(hwnd, "STATIC", appTitle, 0x00000000, 40, 30, 650, 45, 0)
	state.subtitle = createControl(hwnd, "STATIC", "Güvenli uzaktan destek", 0x00000000, 40, 72, 650, 28, 0)
	state.status = createControl(hwnd, "STATIC", "Bir işlem seç.", 0, 40, 585, 900, 32, 0)

	getSupport := createControl(hwnd, "BUTTON", "DESTEK ALIYORUM", 0x00000001, 90, 205, 380, 100, ID_GET_SUPPORT)
	giveSupport := createControl(hwnd, "BUTTON", "DESTEK VERİYORUM", 0x00000001, 510, 205, 380, 100, ID_GIVE_SUPPORT)
	state.homeGet = getSupport
	state.homeGive = giveSupport
	state.action = createControl(hwnd, "BUTTON", "BAĞLAN", 0x00000001, 330, 365, 320, 58, ID_CONNECT)
	state.copy = createControl(hwnd, "BUTTON", "Kodu kopyala", 0x00000001, 665, 365, 180, 58, ID_COPY_CODE)
	state.shareLink = createControl(hwnd, "BUTTON", "Bağlantı Linki", 0x00000001, 0, 0, 10, 10, ID_SHARE_LINK)
	hide(state.shareLink)
	state.back = createControl(hwnd, "BUTTON", "← Geri", 0x00000001, 90, 365, 180, 58, ID_BACK)
	state.fileSend = createControl(hwnd, "BUTTON", "Dosya Gönder", 0x00000001, 760, 14, 100, 30, ID_FILE_SEND)
	state.fullscreenButton = createControl(hwnd, "BUTTON", "Tam Ekran", 0x00000001, 865, 14, 95, 30, ID_FULLSCREEN)
	state.clipSend = createControl(hwnd, "BUTTON", "Pano Gönder", 0x00000001, 620, 14, 130, 30, ID_CLIP_SEND)
	state.clipGet = createControl(hwnd, "BUTTON", "Pano Al", 0x00000001, 500, 14, 110, 30, ID_CLIP_GET)
	state.elevate = createControl(hwnd, "BUTTON", "Yönetici olarak yeniden başlat", 0x00000001, 700, 108, 260, 40, ID_ELEVATE)
	state.chatInput = createControl(hwnd, "EDIT", "", 0x00000080, 0, 0, 10, 10, 0)
	state.chatSend = createControl(hwnd, "BUTTON", "Gönder", 0x00000001, 0, 0, 10, 10, ID_CHAT_SEND)
	origEditProc, _, _ = setWindowLongPtr.Call(state.chatInput, ^uintptr(3), syscall.NewCallback(chatEditProc))
	state.stayOpen = createControl(hwnd, "BUTTON", "Açık Kal", 0x00000001, 0, 0, 10, 10, ID_STAY_OPEN)
	hide(state.stayOpen)
	state.actionsBtn = createControl(hwnd, "BUTTON", "İşlemler  ▾", 0x00000001, 0, 0, 10, 10, ID_ACTIONS)
	hide(state.actionsBtn)
	hide(state.fileSend)
	hide(state.fullscreenButton)
	hide(state.clipSend)
	hide(state.clipGet)
	hide(state.elevate)
	hide(state.chatInput)
	hide(state.chatSend)

	state.codeLabel = createControl(hwnd, "STATIC", newCode(), 0x00000001|0x00000200|0x00000080, 250, 190, 480, 90, 0)
	state.remoteEdit = createControl(hwnd, "EDIT", "", 0x00000001|0x00000080, 250, 190, 480, 58, ID_REMOTE_CODE) // ES_CENTER|ES_AUTOHSCROLL: internet codes contain letters

	state.settingsEndpoint = createControl(hwnd, "EDIT", "", 0x00000080, 520, 235, 520, 44, 0) // ES_AUTOHSCROLL (metin/URL)
	state.settingsTurnURL = createControl(hwnd, "EDIT", "", 0x00000080, 284, 306, 664, 30, 0)
	state.settingsTurnUser = createControl(hwnd, "EDIT", "", 0x00000080, 284, 344, 250, 30, 0)
	state.settingsTurnPass = createControl(hwnd, "EDIT", "", 0x00000080, 622, 344, 326, 30, 0)
	state.settingsQuality = createControl(hwnd, "COMBOBOX", "", 0x0003|0x0010|0x0200, 520, 335, 180, 240, 0)
	state.settingsResolution = createControl(hwnd, "COMBOBOX", "", 0x0003|0x0010|0x0200, 760, 335, 180, 240, 0)
	addComboItems(state.settingsQuality, "Yüksek", "Dengeli")
	addComboItems(state.settingsResolution, "Otomatik", "1920 × 1080", "1280 × 720", "854 × 480")
	state.settingsIdle = createControl(hwnd, "COMBOBOX", "", 0x0003|0x0010|0x0200, 520, 490, 180, 240, 0)
	state.settingsIdleAction = createControl(hwnd, "COMBOBOX", "", 0x0003|0x0010|0x0200, 760, 490, 260, 240, 0)
	addComboItems(state.settingsIdle, "Kapalı", "10 dk", "20 dk", "30 dk")
	addComboItems(state.settingsIdleAction, "Açık kal", "Bağlantıyı kes")
	state.settingsSave = createControl(hwnd, "BUTTON", "Ayarları Kaydet", 0x00000001, 520, 500, 180, 44, ID_SETTINGS_SAVE)
	state.settingsReset = createControl(hwnd, "BUTTON", "Varsayılanlara Dön", 0x00000001, 720, 500, 220, 44, ID_SETTINGS_RESET)

	hide(state.action)
	hide(state.copy)
	hide(state.back)
	hide(state.codeLabel)
	hide(state.remoteEdit)
	hide(state.settingsEndpoint)
	hide(state.settingsQuality)
	hide(state.settingsResolution)
	hide(state.settingsSave)
	hide(state.settingsReset)
	hide(state.settingsIdle)
	hide(state.settingsIdleAction)
	hide(state.settingsTurnURL)
	hide(state.settingsTurnUser)
	hide(state.settingsTurnPass)

	state.mode = 0
	state.statusText = "Bir işlem seç."
	layoutUI()

	showWindow.Call(hwnd, 1)
	updateWindow.Call(hwnd)

	var m msg
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		// Esc / F11 leave full screen wherever the keyboard focus is. Before,
		// only the viewer handled them, and after switching to full screen the
		// focus sat elsewhere, so IsDialogMessage swallowed Esc as IDCANCEL.
		if m.Message == 0x0100 && (m.WParam == 0x1B || m.WParam == 0x7A) && fullscreen.Load() {
			toggleFullscreen()
			continue
		}
		if d, _, _ := isDialogMessage.Call(state.hwnd, uintptr(unsafe.Pointer(&m))); d != 0 {
			continue
		}
		translateMsg.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// Keep the package dependency explicit in this Windows-only client. The peer
// package exposes Pion's state values through ConnectionState().
var _ = webrtc.PeerConnectionStateConnected
var _ = destroyWindow
