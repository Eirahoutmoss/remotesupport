//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

func gatherSysInfo() string {
	host, _ := os.Hostname()
	gb := func(b uint64) float64 { return float64(b) / (1 << 30) }
	var mse memoryStatusEx
	mse.Length = uint32(unsafe.Sizeof(mse))
	globalMemStatus.Call(uintptr(unsafe.Pointer(&mse)))
	var freeAvail, total, totalFree uint64
	getDiskFreeSpace.Call(uintptr(unsafe.Pointer(utf16ptr("C:\\"))), uintptr(unsafe.Pointer(&freeAvail)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	return fmt.Sprintf("Bilgisayar: %s\r\nSistem: %s / %s\r\nİşlemci çekirdeği: %d\r\nBellek: %.1f / %.1f GB boş\r\nDisk (C:): %.1f / %.1f GB boş",
		host, runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), gb(mse.AvailPhys), gb(mse.TotalPhys), gb(totalFree), gb(total))
}

func showActionsMenu() {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu)
	mi := func(m, flags, id uintptr, label string) {
		var p uintptr
		if label != "" {
			p = uintptr(unsafe.Pointer(utf16ptr(label)))
		}
		appendMenuW.Call(m, flags, id, p)
	}
	mi(menu, 0x0000, ID_FILE_SEND, "Dosya Gönder")
	mi(menu, 0x0000, ID_FILE_CANCEL, "Dosya Aktarımını İptal Et")
	mi(menu, 0x0000, ID_CLIP_SEND, "Panoyu Gönder")
	mi(menu, 0x0000, ID_CLIP_GET, "Panoyu Al")
	mi(menu, 0x0000, ID_SHOT, "Ekran Görüntüsü Kaydet (F12)")
	mi(menu, 0x0000, ID_SYSINFO, "Uzak Sistem Bilgisi")
	annotLabel := "İşaretleme Modu — Aç"
	if annotateMode.Load() {
		annotLabel = "İşaretleme Modu — Kapat"
	}
	mi(menu, 0x0000, ID_ANNOT, annotLabel)
	mi(menu, 0x0800, 0, "") // MF_SEPARATOR
	sub, _, _ := createPopupMenu.Call()
	if sub != 0 {
		mi(sub, 0x0000, ID_KEY_WIN, "Windows tuşu")
		mi(sub, 0x0000, ID_KEY_ALTTAB, "Alt + Tab")
		mi(sub, 0x0000, ID_KEY_ESC, "Esc")
		mi(menu, 0x0010, sub, "Özel Tuşlar") // MF_POPUP
	}
	mi(menu, 0x0800, 0, "") // MF_SEPARATOR
	appendB1Menu(menu, mi)
	mi(menu, 0x0800, 0, "")
	curtainLabel := "Gizlilik Perdesi — Aç"
	if remoteCurtainOn.Load() {
		curtainLabel = "Gizlilik Perdesi — Kapat"
	}
	mi(menu, 0x0000, ID_CURTAIN, curtainLabel)
	lockLabel := "Yerel Girişi Kilitle"
	if remoteLockOn.Load() {
		lockLabel = "Yerel Giriş Kilidini Aç"
	}
	mi(menu, 0x0000, ID_LOCKINPUT, lockLabel)
	var pt point
	getCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	cmd, _, _ := trackPopupMenu.Call(menu, 0x0100, uintptr(pt.X), uintptr(pt.Y), 0, state.hwnd, 0) // TPM_RETURNCMD
	if cmd != 0 {
		onCommand(int(cmd))
	}
}

func onCommand(id int) {
	// Monitor buttons use ID_MONITOR_BASE+index. Bound the range tightly so
	// unrelated IDs in [2000,4000) (e.g. ID_FILE_SEND=3001) are NOT swallowed
	// here — that bug made "Dosya Gönder" silently call selectMonitor instead.
	if id >= ID_MONITOR_BASE && id < ID_MONITOR_BASE+64 {
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
		if path, ok := chooseFile(); ok {
			go sendFileToPeer(path)
		}
	case ID_RES_ORIGINAL:
		setRemoteResolution(0)
	case ID_RES_1080P:
		setRemoteResolution(1080)
	case ID_RES_720P:
		setRemoteResolution(720)
	case ID_RES_480P:
		setRemoteResolution(480)
	case ID_FULLSCREEN:
		toggleFullscreen()
	case ID_ELEVATE:
		restartAsAdmin()
	case ID_CLIP_SEND:
		state.mu.Lock()
		pr := state.peer
		state.mu.Unlock()
		if pr != nil {
			go func() {
				if sendClipboardToPeer(pr) {
					setStatus("● Pano karşı bilgisayara gönderildi.")
				}
			}()
		}
	case ID_CLIP_GET:
		state.mu.Lock()
		pr2 := state.peer
		state.mu.Unlock()
		if pr2 != nil {
			requestRemoteClipboard(pr2)
			setStatus("● Uzak pano istendi.")
		}
	case ID_CHAT_SEND:
		sendChat()
	case ID_STAY_OPEN:
		lastActivityUnix.Store(time.Now().Unix())
		idleWarn.Store(false)
		layoutUI()
		invalidateRect.Call(state.hwnd, 0, 0)
		setStatus("● Oturum açık tutuluyor.")
	case ID_ACTIONS:
		showActionsMenu()
	case ID_FILE_CANCEL:
		fileCancel.Store(true)
		setStatus("● Dosya aktarımı iptal ediliyor…")
	case ID_SHOT:
		go saveViewerScreenshot()
	case ID_SYSINFO:
		state.mu.Lock()
		ps := state.peer
		state.mu.Unlock()
		if ps != nil {
			ps.SendControlText("SYSINFO_REQ")
		}
	case ID_ANNOT:
		on := !annotateMode.Load()
		annotateMode.Store(on)
		if on {
			setStatus("● İşaretleme modu açık: ekranda sürükleyerek işaretleyin.")
		} else {
			state.mu.Lock()
			pa := state.peer
			state.mu.Unlock()
			if pa != nil {
				pa.SendControlText("ANNOT_CLEAR")
			}
			setStatus("● İşaretleme modu kapalı.")
		}
	case ID_KEY_WIN:
		sendRemoteKey("keydown", 0x5B)
		sendRemoteKey("keyup", 0x5B)
	case ID_KEY_ALTTAB:
		sendRemoteKey("keydown", 0x12)
		sendRemoteKey("keydown", 0x09)
		sendRemoteKey("keyup", 0x09)
		sendRemoteKey("keyup", 0x12)
	case ID_KEY_ESC:
		sendRemoteKey("keydown", 0x1B)
		sendRemoteKey("keyup", 0x1B)
	case ID_CURTAIN:
		on := !remoteCurtainOn.Load()
		remoteCurtainOn.Store(on)
		state.mu.Lock()
		pc := state.peer
		state.mu.Unlock()
		if pc != nil {
			if on {
				pc.SendControlText("CURTAIN:1")
			} else {
				pc.SendControlText("CURTAIN:0")
			}
		}
	case ID_LOCKINPUT:
		on := !remoteLockOn.Load()
		remoteLockOn.Store(on)
		state.mu.Lock()
		pl := state.peer
		state.mu.Unlock()
		if pl != nil {
			if on {
				pl.SendControlText("LOCKINPUT:1")
			} else {
				pl.SendControlText("LOCKINPUT:0")
			}
		}
	case ID_SETTINGS:
		sidebarPage.Store(2)
		showSettings()
		return
	case ID_ABOUT:
		sidebarPage.Store(3)
		showAbout()
		return
	case ID_SETTINGS_SAVE:
		saveSettingsFromUI()
		return
	case ID_SETTINGS_RESET:
		resetSettingsUI()
		return
	case ID_DEVICE_SAVE:
		saveDeviceAccess()
		return
	case ID_TECH_DEV:
		onTechDevSelect()
	case ID_TECH_DEV_MANAGE:
		manageTechDevice()
	case ID_ABOUT_THEME:
		onThemeSelect()
	case ID_ABOUT_UPDATE:
		checkForUpdates()
		return
	case ID_CONNECT:
		if pass := getText(state.remotePass); pass != "" {
			// Device access: the box holds a device id, not a one-time code.
			id := strings.TrimSpace(getText(state.remoteEdit))
			if id == "" {
				setStatus("Cihaz kimliğini girin.")
				return
			}
			setOpEndpoint("")
			setOpDevicePass(pass)
			setText(state.action, "BAĞLANIYOR...")
			setStatus("● Cihaza bağlanılıyor…")
			go startOperator(id)
			return
		}
		code, ws := parseJoinInput(getText(state.remoteEdit))
		if code == "" {
			setStatus("Bağlantı kodunu, internet kodunu ya da nexdesk:// bağlantısını girin.")
			return
		}
		setOpEndpoint(ws)
		setOpDevicePass("")
		setText(state.action, "BAĞLANIYOR...")
		setStatus("● Bağlantı kodu doğrulanıyor...")
		go startOperator(code)
	case ID_SHARE_LINK:
		code := getActiveCode()
		if code == "" {
			setStatus("Önce bir bağlantı kodu oluşturulmalı.")
			return
		}
		if copyToClipboard(buildJoinLink(code)) {
			setStatus("● Bağlantı linki panoya kopyalandı — karşı tarafa iletin.")
		}
	default:
		onB1Command(id)
	}
}

func stopSession() {
	b1SessionEnded()
	userStopped.Store(true)
	auditLog("Oturum kapatıldı")
	clearPrivacy()
	setRemoteHost("")
	annotateMode.Store(false)
	clearAnnot()
	resetConnInfo()
	resetChat()
	hideViewer()
	clearMonitorButtons()
	state.mu.Lock()
	cancel := state.cancel
	sig := state.sig
	peer := state.peer
	state.cancel = nil
	state.sig = nil
	state.peer = nil
	state.ctx = nil
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

type measureItemStruct struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemWidth  uint32
	ItemHeight uint32
	ItemData   uintptr
}

type drawItemStruct struct {
	CtlType    uint32
	CtlID      uint32
	ItemID     uint32
	ItemAction uint32
	ItemState  uint32
	HwndItem   uintptr
	HDC        uintptr
	RcItem     rect
	ItemData   uintptr
}

func buttonLabel(hwnd uintptr) string {
	buf := make([]uint16, 128)
	n, _, _ := getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func drawComboItem(dis *drawItemStruct) {
	if dis == nil || dis.HDC == 0 {
		return
	}
	hdc := dis.HDC
	r := dis.RcItem
	selected := dis.ItemState&0x0001 != 0 // ODS_SELECTED
	bg := uint32(uiEditColor)
	if selected {
		bg = uiAccentColor
	}
	brush, _, _ := createSolidBrush.Call(rgb(bg))
	if brush != 0 {
		fillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), brush)
		deleteObject.Call(brush)
	}
	if int32(dis.ItemID) >= 0 {
		var tb [256]uint16
		sendMessage.Call(dis.HwndItem, 0x0148, uintptr(dis.ItemID), uintptr(unsafe.Pointer(&tb[0]))) // CB_GETLBTEXT
		txt := syscall.UTF16ToString(tb[:])
		setBkMode.Call(hdc, 1)
		setTextColor.Call(hdc, rgb(uiTextColor))
		old, _, _ := selectObject.Call(hdc, uiBodyFont)
		rr := rect{Left: r.Left + 10, Top: r.Top, Right: r.Right - 6, Bottom: r.Bottom}
		drawText.Call(hdc, uintptr(unsafe.Pointer(utf16ptr(txt))), ^uintptr(0), uintptr(unsafe.Pointer(&rr)), uintptr(dtVCenter|dtSingleLine|dtEndEllip))
		selectObject.Call(hdc, old)
	}
}

func drawOwnerButton(dis *drawItemStruct) {
	if dis == nil || dis.HDC == 0 {
		return
	}
	hdc := dis.HDC
	r := dis.RcItem
	id := int(dis.CtlID)
	x, y := int(r.Left), int(r.Top)
	bw, bh := int(r.Right-r.Left), int(r.Bottom-r.Top)
	pressed := dis.ItemState&0x0001 != 0 // ODS_SELECTED
	hot := dis.HwndItem == hoveredBtn    // mouse hover, tracked in btnProc
	viewer.mu.RLock()
	connected := viewer.active
	viewer.mu.RUnlock()

	// ---- Primary action cards (Destek Al / Destek Ver) ----
	if id == ID_GET_SUPPORT || id == ID_GIVE_SUPPORT {
		var fill, border uint32 = uiPanelColor, uiBorderColor
		if pressed || hot {
			fill = uiPanelHi
			border = uiAccentColor
		}
		paintCard(hdc, x, y, bw, bh, fill, border, 16)

		icon := "▣"
		title := "DESTEK VER"
		desc := "Karşı bilgisayara güvenli şekilde bağlanın."
		cta := "Bağlan  →"
		if id == ID_GET_SUPPORT {
			icon = "♙"
			title = "DESTEK AL"
			desc = "Bilgisayarınıza uzaktan bağlanılmasına izin verin."
			cta = "Kod Oluştur  →"
		}
		// Accent icon medallion with glow.
		paintGlow(hdc, x+24, y+24, 56, 56, uiAccentColor, 16)
		paintRoundPanel(hdc, x+24, y+24, 56, 56, uiAccentColor, 16)
		paintText(hdc, icon, x+24, y+24, 56, 56, uiHeaderFont, uiTextColor, txMid)

		paintText(hdc, title, x+96, y+28, bw-116, 26, uiTitleFont, uiTextColor, txVLeft)
		paintText(hdc, desc, x+96, y+58, bw-116, 44, uiBodyFont, uiMutedColor, txWrap)

		// CTA chip pinned to the bottom-left of the card.
		var chipCol uint32 = uiAccentColor
		if pressed {
			chipCol = uiAccentHi
		}
		paintText(hdc, cta, x+26, y+bh-40, bw-52, 22, uiButtonFont, chipCol, txVLeft)
		return
	}

	// ---- Regular buttons: pill styling by role ----
	text := buttonLabel(dis.HwndItem)
	font := uiButtonFont
	var fg uint32 = uiTextColor
	radius := 12

	var fill, border uint32
	switch {
	case id == ID_CONNECT:
		// Primary filled accent pill (faux gradient: accent body + lighter cap).
		fill, border = uiAccentColor, uiAccentColor
		if pressed {
			fill, border = uiAccentDim, uiAccentDim
		}
		fg = 0xFFFFFF
		font = uiSubtitleFont
		if hot && !pressed {
			paintGlow(hdc, x, y, bw, bh, uiAccentHi, radius)
		}
		paintCard(hdc, x, y, bw, bh, fill, border, radius)
		if !pressed && bh > 10 {
			paintRoundPanel(hdc, x+2, y+2, bw-4, bh/2, mixHex(uiAccentColor, uiAccentHi, 0.5), radius)
		}
		paintText(hdc, text, x+10, y, bw-20, bh, font, fg, txMid|txEnd)
		return
	case id == ID_BACK && (connected || capturing.Load()):
		// Destructive: disconnect.
		fill, border, fg = uiDanger, uiDanger, 0xFFFFFF
		if pressed {
			fill, border = mixHex(uiDanger, 0x000000, 0.15), uiDanger
		}
		font = uiButtonFont
	case id == ID_COPY_CODE:
		fill, border = uiPanelHi, uiBorderColor
		fg = uiAccentColor
		if pressed {
			fill = uiPanelColor
		}
	case id == ID_STAY_OPEN:
		fill, border = uiSuccess, uiSuccess
		fg = 0xFFFFFF
		if pressed {
			fill, border = mixHex(uiSuccess, 0x000000, 0.15), uiSuccess
		}
	case id == ID_CHAT_SEND:
		fill, border = uiAccentColor, uiAccentColor
		fg = 0xFFFFFF
		if pressed {
			fill, border = uiAccentDim, uiAccentDim
		}
	case id == ID_ELEVATE:
		fill, border = uiPanelColor, uiAccentColor
		fg = uiAccentHi
		if pressed {
			fill = uiPanelHi
		}
	default:
		// Back (idle), file send, fullscreen, monitor & resolution pills.
		fill, border = uiPanelHi, uiBorderColor
		fg = uiTextColor
		if hot && !pressed {
			border = uiAccentColor
		}
		if pressed {
			fill, border = uiAccentColor, uiAccentColor
			fg = 0xFFFFFF
		}
	}

	if hot && !pressed {
		// Hover glow: a soft halo tinted by the button's own accent so every
		// actionable pill lights up under the cursor.
		glow := border
		if glow == uiBorderColor {
			glow = uiAccentColor
		}
		paintGlow(hdc, x, y, bw, bh, glow, radius)
	}
	paintCard(hdc, x, y, bw, bh, fill, border, radius)
	paintText(hdc, text, x+10, y, bw-20, bh, font, fg, txMid|txEnd)
}

func wndProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	if r, ok := b1WndProc(hwnd, m, wParam, lParam); ok {
		return r
	}
	switch m {
	case 0x0014: // WM_ERASEBKGND
		return 1
	case 0x0002: // WM_DESTROY
		stopSession()
		postQuit.Call(0)
		return 0
	case 0x0138: // WM_CTLCOLORSTATIC
		hdc := wParam
		// The only prominent static is the connection-code label; give it the
		// input-field surface so it reads as the code box painted behind it.
		setTextColor.Call(hdc, rgb(uiTextColor))
		setBkColor.Call(hdc, rgb(uiEditColor))
		setBkMode.Call(hdc, 1) // TRANSPARENT
		return uiEditBrush
	case 0x0133: // WM_CTLCOLOREDIT
		hdc := wParam
		setTextColor.Call(hdc, rgb(uiTextColor))
		setBkColor.Call(hdc, rgb(uiEditColor)) // fill the field dark (missing before -> stayed white)
		setBkMode.Call(hdc, 2)                 // OPAQUE
		return uiEditBrush
	case 0x0135: // WM_CTLCOLORBTN
		hdc := wParam
		setTextColor.Call(hdc, rgb(uiTextColor))
		setBkMode.Call(hdc, 1)
		return uiPanelBrush
	case 0x0134: // WM_CTLCOLORLISTBOX (combo dropdown)
		hdc := wParam
		setTextColor.Call(hdc, rgb(uiTextColor))
		setBkColor.Call(hdc, rgb(uiEditColor))
		setBkMode.Call(hdc, 2)
		return uiEditBrush
	case 0x002C: // WM_MEASUREITEM
		mis := (*measureItemStruct)(unsafe.Pointer(lParam))
		if mis != nil {
			mis.ItemHeight = 26
		}
		return 1
	case 0x002B: // WM_DRAWITEM
		dis := (*drawItemStruct)(unsafe.Pointer(lParam))
		if dis != nil && dis.CtlType == 4 { // ODT_BUTTON
			drawOwnerButton(dis)
			return 1
		}
		if dis != nil && dis.CtlType == 3 { // ODT_COMBOBOX
			drawComboItem(dis)
			return 1
		}
		return 0
	case 0x0202: // WM_LBUTTONUP
		x := int(int16(lParam & 0xffff))
		y := int(int16((lParam >> 16) & 0xffff))
		// Left navigation rows: painted at y = 102 + 52*i, height 44 (see paintSidebar).
		if x >= 0 && x < navW && y >= 96 {
			switch {
			case y >= 96 && y < 148:
				sidebarPage.Store(0)
				home()
				return 0
			case y >= 148 && y < 200:
				sidebarPage.Store(1)
				return 0
			case y >= 200 && y < 252:
				sidebarPage.Store(2)
				showSettings()
				return 0
			case y >= 252 && y < 304:
				sidebarPage.Store(4)
				showDeviceAccess()
				return 0
			case y >= 304 && y < 356:
				sidebarPage.Store(3)
				showAbout()
				return 0
			case y >= 356 && y < 408:
				// "Yeni Oturum" — an action, not a page: open another window.
				launchNewSession()
				return 0
			}
		}
	case 0x0111: // WM_COMMAND
		onCommand(int(wParam & 0xffff))
		return 0
	case WM_APP_MONITORS:
		count := int(wParam)
		updateMonitorButtons(count)
		setStatus(fmt.Sprintf("● Uzak bilgisayarda %d monitör bulundu", count))
		return 0
	case WM_APP_IDLE:
		layoutUI()
		invalidateRect.Call(hwnd, 0, 0)
		return 0
	case WM_APP_CURTAIN:
		if wParam != 0 {
			showCurtain()
		} else {
			hideCurtain()
		}
		return 0
	case WM_APP_ANNOT:
		if wParam != 0 {
			clearAnnot()
		} else {
			drainAnnot()
		}
		return 0
	case WM_APP_IDLE_CLOSE:
		stopSession()
		home()
		setStatus("● Oturum boşta kalma nedeniyle kapatıldı.")
		return 0
	case 0x0005: // WM_SIZE
		layoutUI()
		resizeViewer()
		invalidateRect.Call(hwnd, 0, 1) // repaint whole shell; erase is suppressed so stale paint lingers otherwise
		return 0
	case 0x000F: // WM_PAINT
		var ps paintStruct
		hdc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if hdc != 0 {
			paintBuffered(hwnd, hdc)
			endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

// ---- button hover tracking ----
//
// Owner-draw BUTTONs do not reliably get ODS_HOTLIGHT, so each button is
// subclassed to watch the mouse itself: entering sets hoveredBtn and repaints,
// leaving (WM_MOUSELEAVE, armed via TrackMouseEvent) clears it. drawOwnerButton
// reads hoveredBtn to draw the hover glow. All runs on the GUI thread, so a
// plain variable is safe.
var (
	hoveredBtn  uintptr
	origBtnProc uintptr
	btnProcCB   = syscall.NewCallback(btnProc)
)

type trackMouseEvent struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   uintptr
	DwHoverTime uint32
}

func btnProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	switch m {
	case 0x0200: // WM_MOUSEMOVE
		if hoveredBtn != hwnd {
			prev := hoveredBtn
			hoveredBtn = hwnd
			if prev != 0 {
				invalidateRect.Call(prev, 0, 1)
			}
			invalidateRect.Call(hwnd, 0, 1)
			tme := trackMouseEvent{CbSize: uint32(unsafe.Sizeof(trackMouseEvent{})), DwFlags: 0x02, HwndTrack: hwnd} // TME_LEAVE
			trackMouseEventProc.Call(uintptr(unsafe.Pointer(&tme)))
		}
	case 0x02A3: // WM_MOUSELEAVE
		if hoveredBtn == hwnd {
			hoveredBtn = 0
			invalidateRect.Call(hwnd, 0, 1)
		}
	}
	r, _, _ := callWindowProc.Call(origBtnProc, hwnd, uintptr(m), wParam, lParam)
	return r
}

// installBtnHover subclasses an owner-draw button for hover tracking.
func installBtnHover(h uintptr) {
	prev, _, _ := setWindowLongPtr.Call(h, ^uintptr(3), btnProcCB) // GWLP_WNDPROC
	if origBtnProc == 0 {
		origBtnProc = prev
	}
}
