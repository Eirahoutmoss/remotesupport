//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

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
	// Size the buffer to the real text length: a fixed 128 cut long pasted
	// codes (davet/yanıt ~700 chars) short.
	n, _, _ := user32.NewProc("GetWindowTextLengthW").Call(hwnd)
	buf := make([]uint16, n+1)
	getWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func setStatus(text string) {
	if os.Getenv("NEXDESK_DEBUG") != "" {
		netlogf("[durum pid=%d] %s", os.Getpid(), text)
	}
	state.mu.Lock()
	state.statusText = text
	h := state.status
	state.mu.Unlock()
	if h != 0 {
		setText(h, text)
	}
	if state.hwnd != 0 {
		invalidateRect.Call(state.hwnd, 0, 0)
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

func moveControl(hwnd uintptr, x, y, w, h int, visible bool) {
	if hwnd == 0 {
		return
	}
	setWindowPos.Call(hwnd, 0, uintptr(x), uintptr(y), uintptr(maxInt(1, w)), uintptr(maxInt(1, h)), 0x0004|0x0010)
	if visible {
		show(hwnd)
	} else {
		hide(hwnd)
	}
}

func layoutUI() {
	if state.hwnd == 0 {
		return
	}
	var rc rect
	getClientRect.Call(state.hwnd, uintptr(unsafe.Pointer(&rc)))
	w := int(rc.Right - rc.Left)
	h := int(rc.Bottom - rc.Top)
	if w < 700 || h < 500 {
		return
	}

	state.mu.Lock()
	mode := state.mode
	state.mu.Unlock()
	viewer.mu.RLock()
	connected := viewer.active
	viewer.mu.RUnlock()
	layoutInetButtons(mode, connected)
	layoutManualButtons(mode, connected)

	// Header and footer are painted by the main window. Keep legacy text controls hidden.
	hide(state.header)
	hide(state.subtitle)
	hide(state.status)
	hide(state.elevate)
	hide(state.clipSend)
	hide(state.clipGet)
	hide(state.chatInput)
	hide(state.chatSend)
	hide(state.stayOpen)
	hide(state.actionsBtn)
	hide(state.shareLink)

	if connected && !fullscreen.Load() {
		// Toolbar action buttons sit on the painted toolbar surface (y≈104).
		moveControl(state.fullscreenButton, w-158, 104, 134, 40, true)
		moveControl(state.actionsBtn, w-306, 104, 140, 40, true)
		hide(state.fileSend)
		hide(state.clipGet)
		hide(state.clipSend)
		cg := computeChatGeom(w, h)
		if cg.visible {
			moveControl(state.chatInput, cg.inputX, cg.inputY, cg.inputW, cg.inputH, true)
			moveControl(state.chatSend, cg.sendX, cg.inputY, cg.sendW, cg.inputH, true)
		}
		// Prominent red disconnect in the header (ID_BACK stops the session).
		setText(state.back, "Bağlantıyı Kes")
		moveControl(state.back, w-190, 22, 166, 42, true)
		moveControl(state.stayOpen, w-370, 22, 150, 42, idleWarn.Load())
		moveControl(state.action, 0, 0, 1, 1, false)
		moveControl(state.copy, 0, 0, 1, 1, false)
		moveControl(state.codeLabel, 0, 0, 1, 1, false)
		moveControl(state.remoteEdit, 0, 0, 1, 1, false)
		return
	}
	if connected && fullscreen.Load() {
		hide(state.fileSend)
		hide(state.fullscreenButton)
		return
	}

	if mode == modeBook {
		layoutBook(w, h)
		return
	}
	if mode == 13 {
		moveControl(state.devOn, contentX+24, 236, 220, 240, true)
		moveControl(state.devId, contentX+24, 320, 520, 34, true)
		moveControl(state.devPass, contentX+24, 400, 520, 34, true)
		moveControl(state.devSave, contentX+24, 466, 300, 46, true)
		return
	}
	if mode == 10 {
		moveControl(state.settingsEndpoint, 520, 200, 560, 40, true)
		moveControl(state.settingsTurnURL, 284, 320, 664, 30, true)
		moveControl(state.settingsTurnUser, 284, 358, 250, 30, true)
		moveControl(state.settingsTurnPass, 622, 358, 326, 30, true)
		moveControl(state.settingsQuality, 520, 434, 180, 240, true)
		moveControl(state.settingsResolution, 760, 434, 180, 240, true)
		moveControl(state.settingsIdle, 520, 522, 180, 240, true)
		moveControl(state.settingsIdleAction, 760, 522, 260, 240, true)
		moveControl(state.settingsInternet, contentX+24, 552, 200, 240, true)
		moveControl(state.settingsSave, 520, 610, 180, 44, true)
		moveControl(state.settingsReset, 720, 610, 220, 44, true)
		return
	}
	if mode == 11 {
		moveControl(state.aboutTheme, contentX+30, 566, 240, 240, true)
		moveControl(state.aboutUpdate, contentX+310, 566, 280, 40, true)
		return
	}

	if mode == 0 {
		// Two primary action cards, side by side, tracking the window width.
		contentW := w - contentX - gutter
		cardGap := 24
		cardW := (contentW - cardGap) / 2
		cardY := 176
		cardH := 200
		moveControl(state.homeGet, contentX, cardY, cardW, cardH, true)
		moveControl(state.homeGive, contentX+cardW+cardGap, cardY, cardW, cardH, true)
		moveControl(state.elevate, w-gutter-260, 110, 260, 40, !isElevated())
		moveControl(state.back, 184, 500, 150, 48, false)
		moveControl(state.action, 350, 500, 250, 48, false)
		moveControl(state.copy, 620, 500, 170, 48, false)
		moveControl(state.codeLabel, 184, 350, 816, 110, false)
		moveControl(state.remoteEdit, 184, 350, 816, 58, false)
		return
	}

	if mode == 1 {
		if capturing.Load() {
			setText(state.back, "Bağlantıyı Kes")
			hide(state.codeLabel)
			hide(state.copy)
			hide(state.remoteEdit)
			hide(state.action)
			cg := computeChatGeom(w, h)
			if cg.visible {
				moveControl(state.chatInput, cg.inputX, cg.inputY, cg.inputW, cg.inputH, true)
				moveControl(state.chatSend, cg.sendX, cg.inputY, cg.sendW, cg.inputH, true)
			}
			moveControl(state.back, w-gutter-180, 118, 168, 40, true)
			moveControl(state.stayOpen, contentX, 214, 150, 40, idleWarn.Load())
			return
		}
		if deviceModeActive.Load() {
			// Device-access wait screen: only the painted banner is shown.
			hide(state.chatInput)
			hide(state.chatSend)
			hide(state.codeLabel)
			hide(state.copy)
			hide(state.shareLink)
			hide(state.remoteEdit)
			hide(state.action)
			moveControl(state.back, 0, 0, 1, 1, false)
			return
		}
		setText(state.back, "‹ Geri")
		hide(state.chatInput)
		hide(state.chatSend)
		moveControl(state.codeLabel, 214, 344, 560, 86, true)
		// Both stay inside the code card (y 322..450).
		moveControl(state.copy, 784, 334, 158, 52, true)
		moveControl(state.shareLink, 784, 394, 158, 44, true)
		moveControl(state.remoteEdit, 178, 335, 782, 58, false)
		// Right of the QR code (QR occupies contentX..+150, y 478..628).
		moveControl(state.back, contentX+166, 574, 140, 46, true)
		moveControl(state.action, contentX+322, 574, 260, 46, true)
		return
	}

	if mode == 2 {
		setText(state.back, "‹ Geri")
		moveControl(state.techDev, contentX, 272, 620, 240, true)
		moveControl(state.techDevManage, contentX+636, 272, 146, 40, true)
		moveControl(state.remoteEdit, contentX, 352, 620, 56, true)
		moveControl(state.action, contentX+636, 352, 146, 56, true) // Bağlan next to input
		moveControl(state.remotePass, contentX, 444, 620, 34, true)
		moveControl(state.copy, 0, 0, 1, 1, false)
		moveControl(state.codeLabel, 0, 0, 1, 1, false)
		moveControl(state.back, contentX, 506, 140, 46, true)
	}
}

// Text alignment flags for paintText (DrawTextW).
const (
	dtCenter     = 0x00000001
	dtRight      = 0x00000002
	dtVCenter    = 0x00000004
	dtSingleLine = 0x00000020
	dtWordBreak  = 0x00000010
	dtEndEllip   = 0x00008000

	txLeft   = 0x00000000
	txMid    = dtCenter | dtVCenter | dtSingleLine // centered both axes (icons, pills)
	txVLeft  = dtVCenter | dtSingleLine            // left, vertically centered
	txVRight = dtRight | dtVCenter | dtSingleLine  // right, vertically centered
	txEnd    = dtVCenter | dtSingleLine | dtEndEllip
	txWrap   = dtWordBreak
)

func paintText(hdc uintptr, text string, x, y, w, h int, font uintptr, color uint32, flags uint32) {
	if hdc == 0 || text == "" {
		return
	}
	setBkMode.Call(hdc, 1)
	setTextColor.Call(hdc, rgb(color))
	old, _, _ := selectObject.Call(hdc, font)
	r := rect{Left: int32(x), Top: int32(y), Right: int32(x + w), Bottom: int32(y + h)}
	drawText.Call(hdc, uintptr(unsafe.Pointer(utf16ptr(text))), ^uintptr(0), uintptr(unsafe.Pointer(&r)), uintptr(flags))
	selectObject.Call(hdc, old)
}

func paintRoundPanel(hdc uintptr, x, y, w, h int, color uint32, radius int) {
	brush, _, _ := createSolidBrush.Call(rgb(color))
	if brush == 0 {
		return
	}
	// Match pen to the fill so RoundRect's 1px outline doesn't leave a stray edge.
	pen, _, _ := createPen.Call(0, 1, rgb(color))
	oldBrush, _, _ := selectObject.Call(hdc, brush)
	oldPen, _, _ := selectObject.Call(hdc, pen)
	roundRect.Call(hdc, uintptr(x), uintptr(y), uintptr(x+w), uintptr(y+h), uintptr(radius), uintptr(radius))
	selectObject.Call(hdc, oldBrush)
	selectObject.Call(hdc, oldPen)
	deleteObject.Call(brush)
	if pen != 0 {
		deleteObject.Call(pen)
	}
}

// paintCard draws a filled rounded panel with a 1px border, the standard card
// look used throughout the concept (soft surface + hairline edge).
func paintCard(hdc uintptr, x, y, w, h int, fill, border uint32, radius int) {
	brush, _, _ := createSolidBrush.Call(rgb(fill))
	pen, _, _ := createPen.Call(0, 1, rgb(border))
	oldBrush, _, _ := selectObject.Call(hdc, brush)
	oldPen, _, _ := selectObject.Call(hdc, pen)
	roundRect.Call(hdc, uintptr(x), uintptr(y), uintptr(x+w), uintptr(y+h), uintptr(radius), uintptr(radius))
	selectObject.Call(hdc, oldBrush)
	selectObject.Call(hdc, oldPen)
	if brush != 0 {
		deleteObject.Call(brush)
	}
	if pen != 0 {
		deleteObject.Call(pen)
	}
}

// paintDot draws a small filled circle (status indicators, timeline bullets).
func paintDot(hdc uintptr, cx, cy, r int, color uint32) {
	brush, _, _ := createSolidBrush.Call(rgb(color))
	pen, _, _ := createPen.Call(0, 1, rgb(color))
	ob, _, _ := selectObject.Call(hdc, brush)
	op, _, _ := selectObject.Call(hdc, pen)
	ellipse.Call(hdc, uintptr(cx-r), uintptr(cy-r), uintptr(cx+r), uintptr(cy+r))
	selectObject.Call(hdc, ob)
	selectObject.Call(hdc, op)
	if brush != 0 {
		deleteObject.Call(brush)
	}
	if pen != 0 {
		deleteObject.Call(pen)
	}
}

// paintGlow fakes a soft glow behind an accent tile by stacking a few
// progressively larger, darker rounded rectangles (no alpha channel in GDI).
func paintGlow(hdc uintptr, x, y, w, h int, color uint32, radius int) {
	for i := 3; i >= 1; i-- {
		g := i * 3
		paintRoundPanel(hdc, x-g, y-g, w+2*g, h+2*g, uint32(mixHex(uiBgColor, color, 0.10*float64(4-i))), radius+g)
	}
}

// mixHex is mix() but returns a 0xRRGGBB value (for callers that need a literal).
func mixHex(a, b uint32, t float64) uint32 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	ar, ag, ab := (a>>16)&0xFF, (a>>8)&0xFF, a&0xFF
	br, bg, bb := (b>>16)&0xFF, (b>>8)&0xFF, b&0xFF
	r := uint32(float64(ar) + (float64(br)-float64(ar))*t)
	g := uint32(float64(ag) + (float64(bg)-float64(ag))*t)
	bl := uint32(float64(ab) + (float64(bb)-float64(ab))*t)
	return r<<16 | g<<8 | bl
}
