//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Shared shell geometry. Child-control layout in layoutUI() and the viewer in
// resizeViewer() are anchored to these same numbers, so keep them in sync.
const (
	navW     = 164
	headerH  = 86
	footerH  = 52
	contentX = 184
	gutter   = 24
)

// ---- In-session text chat (over the control data channel) ----

type chatMsg struct {
	mine bool
	text string
}

var chatMu sync.Mutex
var chatLog []chatMsg

func addChat(mine bool, text string) {
	if os.Getenv("NEXDESK_DEBUG") != "" {
		netlogf("[sohbet pid=%d mine=%v] %s", os.Getpid(), mine, text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	chatMu.Lock()
	chatLog = append(chatLog, chatMsg{mine: mine, text: text})
	if len(chatLog) > 200 {
		chatLog = chatLog[len(chatLog)-200:]
	}
	chatMu.Unlock()
	if state.hwnd != 0 {
		invalidateRect.Call(state.hwnd, 0, 0)
	}
}

func chatSnapshot() []chatMsg {
	chatMu.Lock()
	defer chatMu.Unlock()
	return append([]chatMsg(nil), chatLog...)
}

func resetChat() {
	chatMu.Lock()
	chatLog = nil
	chatMu.Unlock()
}

func sendChat() {
	txt := strings.TrimSpace(getText(state.chatInput))
	if txt == "" {
		return
	}
	if len(txt) > 2000 {
		txt = txt[:2000]
	}
	state.mu.Lock()
	pr := state.peer
	state.mu.Unlock()
	if pr == nil {
		return
	}
	if err := pr.SendControlText("CHAT:" + txt); err != nil {
		setStatus("● Mesaj gönderilemedi: " + err.Error())
		return
	}
	addChat(true, txt)
	setText(state.chatInput, "")
}

type chatGeom struct {
	x, y, w, h                     int
	inputX, inputY, inputW, inputH int
	sendX, sendW                   int
	visible                        bool
}

// computeChatGeom returns the chat panel + input geometry for the active view:
// a right-hand column for the operator, a wide area below the banner for the
// agent, or invisible when no session is on screen.
func computeChatGeom(w, h int) chatGeom {
	viewer.mu.RLock()
	active := viewer.active
	viewer.mu.RUnlock()
	inputH := 40
	if active && !fullscreen.Load() {
		cw := 300
		cx := w - gutter - cw
		cy := 160 // below the toolbar strip (was 106, overlapped the buttons)
		ch := h - footerH - 16 - cy - (inputH + 10)
		if ch < 60 {
			ch = 60
		}
		g := chatGeom{x: cx, y: cy, w: cw, h: ch, visible: true, inputY: cy + ch + 8, inputH: inputH}
		g.sendW = 84
		g.inputX = cx
		g.inputW = cw - g.sendW - 8
		g.sendX = cx + g.inputW + 8
		return g
	}
	state.mu.Lock()
	mode := state.mode
	state.mu.Unlock()
	if mode == 1 && capturing.Load() {
		cx := contentX
		cw := w - contentX - gutter
		cy := 250
		ch := h - footerH - 16 - cy - (inputH + 10)
		if ch < 60 {
			ch = 60
		}
		g := chatGeom{x: cx, y: cy, w: cw, h: ch, visible: true, inputY: cy + ch + 8, inputH: inputH}
		g.sendW = 110
		g.inputX = cx
		g.inputW = cw - g.sendW - 8
		g.sendX = cx + g.inputW + 8
		return g
	}
	return chatGeom{visible: false}
}

func paintChatPanel(hdc uintptr, g chatGeom) {
	if !g.visible {
		return
	}
	paintCard(hdc, g.x, g.y, g.w, g.h, uiPanelColor, uiBorderColor, 12)
	paintText(hdc, "SOHBET", g.x+16, g.y+12, g.w-32, 16, uiLabelFont, uiMutedColor, txVLeft)
	fillBand(hdc, g.x+16, g.y+36, g.w-32, 1, rgb(uiBorderColor))
	msgs := chatSnapshot()
	lineH := 38
	top := g.y + 46
	avail := (g.y + g.h - 10 - top) / lineH
	if avail < 1 {
		avail = 1
	}
	if len(msgs) > avail {
		msgs = msgs[len(msgs)-avail:]
	}
	if len(msgs) == 0 {
		paintText(hdc, "Henüz mesaj yok. Karşı tarafla yazışabilirsiniz.", g.x+16, top, g.w-32, 20, uiBodyFont, uiFaintColor, txVLeft)
		return
	}
	y := top
	for _, m := range msgs {
		who := "Karşı taraf"
		var col uint32 = uiMutedColor
		if m.mine {
			who = "Siz"
			col = uiAccentHi
		}
		paintText(hdc, who, g.x+16, y, g.w-32, 14, uiSmallFont, col, txVLeft)
		paintText(hdc, m.text, g.x+16, y+15, g.w-32, 20, uiBodyFont, uiTextColor, txEnd)
		y += lineH
	}
}

// ---- Recent connections (persisted to the config dir) ----
type recentConn struct {
	Role string `json:"role"`
	Code string `json:"code"`
	At   int64  `json:"at"`
}

var recentMu sync.Mutex
var recentList []recentConn

func recentPath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	dir := filepath.Join(base, "RemoteSupport")
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "recent.json")
}

func loadRecent() {
	path := recentPath()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var list []recentConn
	if json.Unmarshal(b, &list) != nil {
		return
	}
	if len(list) > 8 {
		list = list[:8]
	}
	recentMu.Lock()
	recentList = list
	recentMu.Unlock()
}

func addRecent(role, code string) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if code == "" {
		return
	}
	rc := recentConn{Role: role, Code: code, At: time.Now().Unix()}
	recentMu.Lock()
	recentList = append([]recentConn{rc}, recentList...)
	if len(recentList) > 8 {
		recentList = recentList[:8]
	}
	snap := append([]recentConn(nil), recentList...)
	recentMu.Unlock()
	if path := recentPath(); path != "" {
		if b, err := json.MarshalIndent(snap, "", "  "); err == nil {
			_ = os.WriteFile(path, b, 0600)
		}
	}
	if state.hwnd != 0 {
		invalidateRect.Call(state.hwnd, 0, 0)
	}
}

func recentSnapshot() []recentConn {
	recentMu.Lock()
	defer recentMu.Unlock()
	return append([]recentConn(nil), recentList...)
}

func relativeTime(unix int64) string {
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Minute:
		return "az önce"
	case d < time.Hour:
		return fmt.Sprintf("%d dk önce", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d saat önce", int(d.Hours()))
	default:
		return fmt.Sprintf("%d gün önce", int(d.Hours())/24)
	}
}

var codeExpiryUnix atomic.Int64
var origEditProc uintptr
var origRemoteEditProc uintptr

// remoteEditProc subclasses the connection-code box so Enter starts the
// connection, the same way the chat input sends on Enter.
func remoteEditProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	if m == 0x0087 { // WM_GETDLGCODE: want Enter + chars
		return 0x0004 | 0x0080 // DLGC_WANTALLKEYS | DLGC_WANTCHARS
	}
	if m == 0x0102 && wParam == 0x0D { // WM_CHAR, Enter
		sendMessage.Call(state.hwnd, 0x0111, uintptr(ID_CONNECT), 0) // WM_COMMAND → Bağlan
		return 0
	}
	r, _, _ := callWindowProc.Call(origRemoteEditProc, hwnd, uintptr(m), wParam, lParam)
	return r
}

// chatEditProc subclasses the chat input so Enter sends the message.
func chatEditProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	if m == 0x0087 { // WM_GETDLGCODE: want Enter/chars
		return 0x0004 | 0x0080 // DLGC_WANTALLKEYS | DLGC_WANTCHARS
	}
	if m == 0x0102 && wParam == 0x0D { // WM_CHAR, Enter
		sendChat()
		return 0
	}
	r, _, _ := callWindowProc.Call(origEditProc, hwnd, uintptr(m), wParam, lParam)
	return r
}

func logCrash(where string) {
	if r := recover(); r != nil {
		base, err := os.UserConfigDir()
		if err == nil && base != "" {
			dir := filepath.Join(base, "RemoteSupport")
			_ = os.MkdirAll(dir, 0700)
			if f, e := os.OpenFile(filepath.Join(dir, "crash.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); e == nil {
				fmt.Fprintf(f, "%s PANIC in %s: %v\n%s\n\n", time.Now().Format(time.RFC3339), where, r, debug.Stack())
				f.Close()
			}
		}
		setStatus("● Bir hata yakalandı ve kaydedildi (crash.log).")
	}
}

func auditLog(event string) {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return
	}
	dir := filepath.Join(base, "RemoteSupport")
	_ = os.MkdirAll(dir, 0700)
	f, err := os.OpenFile(filepath.Join(dir, "audit.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\n", time.Now().Format("2006-01-02 15:04:05"), event)
}
