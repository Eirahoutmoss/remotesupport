//go:build windows

package main

// Build 1: address book with tags (39), per-computer note history (40),
// Wake-on-LAN (20) and the connection/outage log (29).

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/eirahoutmoss/remotesupport/client/capture"
)

const (
	ID_BOOK_SEARCH = 5210
	ID_BOOK_WOL    = 5211
	ID_BOOK_NOTE   = 5212
	ID_BOOK_NOTES  = 5213
	ID_BOOK_EDIT   = 5214
	ID_BOOK_DEL    = 5215
	modeBook       = 12
)

type bookNote struct {
	At   int64  `json:"at"`
	Text string `json:"text"`
}

type bookEntry struct {
	Host     string     `json:"host"`
	Name     string     `json:"name,omitempty"`
	Tags     string     `json:"tags,omitempty"`
	Role     string     `json:"role,omitempty"`
	MACs     []string   `json:"macs,omitempty"`
	LastSeen int64      `json:"last_seen"`
	Sessions int        `json:"sessions"`
	Notes    []bookNote `json:"notes,omitempty"`
}

func (e bookEntry) display() string {
	if e.Name != "" {
		return e.Name + "  (" + e.Host + ")"
	}
	return e.Host
}

var (
	bookMu       sync.Mutex
	book         = map[string]*bookEntry{}
	bookLoaded   bool
	bookSel      string   // selected host
	bookRows     []string // hosts in painted order
	bookRowsTop  int
	bookRowH     = 58
	bookSearch   uintptr
	bookButtons  []uintptr
	bookSeenOnce sync.Map // host -> struct{} per session, so reconnects don't double count
)

func b1Dir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	dir := filepath.Join(base, "RemoteSupport")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func bookPath() string {
	if d := b1Dir(); d != "" {
		return filepath.Join(d, "addressbook.json")
	}
	return ""
}

func loadBook() {
	bookMu.Lock()
	defer bookMu.Unlock()
	if bookLoaded {
		return
	}
	bookLoaded = true
	if b, err := os.ReadFile(bookPath()); err == nil {
		var list []*bookEntry
		if json.Unmarshal(b, &list) == nil {
			for _, e := range list {
				if e != nil && e.Host != "" {
					book[strings.ToLower(e.Host)] = e
				}
			}
		}
	}
}

// saveBookLocked writes the book; caller holds bookMu.
func saveBookLocked() {
	list := make([]*bookEntry, 0, len(book))
	for _, e := range book {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].LastSeen > list[j].LastSeen })
	if b, err := json.MarshalIndent(list, "", "  "); err == nil {
		if p := bookPath(); p != "" {
			_ = os.WriteFile(p, b, 0600)
		}
	}
}

func bookGet(host string) (bookEntry, bool) {
	loadBook()
	bookMu.Lock()
	defer bookMu.Unlock()
	e, ok := book[strings.ToLower(host)]
	if !ok {
		return bookEntry{}, false
	}
	c := *e
	c.Notes = append([]bookNote(nil), e.Notes...)
	c.MACs = append([]string(nil), e.MACs...)
	return c, true
}

func bookUpdate(host string, fn func(e *bookEntry)) {
	host = strings.TrimSpace(host)
	if host == "" {
		return
	}
	loadBook()
	bookMu.Lock()
	k := strings.ToLower(host)
	e := book[k]
	if e == nil {
		e = &bookEntry{Host: host}
		book[k] = e
	}
	fn(e)
	saveBookLocked()
	bookMu.Unlock()
	if state.hwnd != 0 {
		invalidateRect.Call(state.hwnd, 0, 0)
	}
}

// bookSeen records the peer's host name when HELLO arrives.
func bookSeen(host string) {
	if host == "" {
		return
	}
	_, dup := bookSeenOnce.LoadOrStore(strings.ToLower(host), struct{}{})
	role := "Destek verildi"
	if state.mode == 1 {
		role = "Destek alındı"
	}
	bookUpdate(host, func(e *bookEntry) {
		e.LastSeen = time.Now().Unix()
		e.Role = role
		if !dup {
			e.Sessions++
		}
	})
	netlogf("Karşı bilgisayar: %s", host)
	if !dup && state.mode == 2 && state.hwnd != 0 {
		if e, ok := bookGet(host); ok && len(e.Notes) > 0 {
			postMessage.Call(state.hwnd, WM_APP_NOTES, 0, 0)
		}
	}
}

func bookSetMACs(host string, macs []string) {
	var clean []string
	for _, m := range macs {
		if hw, err := net.ParseMAC(strings.TrimSpace(m)); err == nil && len(hw) == 6 {
			clean = append(clean, hw.String())
		}
	}
	if len(clean) == 0 {
		return
	}
	bookUpdate(host, func(e *bookEntry) { e.MACs = clean })
}

func notesText(e bookEntry, max int) string {
	var sb strings.Builder
	notes := e.Notes
	if max > 0 && len(notes) > max {
		notes = notes[len(notes)-max:]
	}
	for _, n := range notes {
		sb.WriteString(time.Unix(n.At, 0).Format("02.01.2006 15:04"))
		sb.WriteString("  —  ")
		sb.WriteString(n.Text)
		sb.WriteString("\r\n")
	}
	return sb.String()
}

func showHostNotes() {
	host := getRemoteHost()
	e, ok := bookGet(host)
	if !ok || len(e.Notes) == 0 {
		return
	}
	text := "Bu bilgisayar için önceki notlar (" + e.display() + "):\r\n\r\n" + notesText(e, 10)
	go messageBox.Call(0, uintptr(unsafe.Pointer(utf16ptr(text))), uintptr(unsafe.Pointer(utf16ptr("Not Geçmişi"))), 0x00000040)
}

func addNoteForHost(host string) {
	if host == "" {
		setStatus("● Karşı bilgisayar adı henüz bilinmiyor.")
		return
	}
	txt, ok := promptText("Not Ekle", host+" için not:", "")
	txt = strings.TrimSpace(txt)
	if !ok || txt == "" {
		return
	}
	if len(txt) > 1000 {
		txt = txt[:1000]
	}
	bookUpdate(host, func(e *bookEntry) {
		e.Notes = append(e.Notes, bookNote{At: time.Now().Unix(), Text: txt})
		if len(e.Notes) > 200 {
			e.Notes = e.Notes[len(e.Notes)-200:]
		}
	})
	setStatus("● Not kaydedildi: " + host)
}

// ---- 20: Wake-on-LAN ----

func sendWOL(macs []string) (int, error) {
	sent := 0
	var lastErr error
	targets := []string{"255.255.255.255:9"}
	for _, b := range discoveryBroadcasts() {
		targets = append(targets, net.JoinHostPort(b.String(), "9"))
	}
	for _, m := range macs {
		hw, err := net.ParseMAC(m)
		if err != nil || len(hw) != 6 {
			continue
		}
		pkt := make([]byte, 0, 102)
		pkt = append(pkt, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
		for i := 0; i < 16; i++ {
			pkt = append(pkt, hw...)
		}
		for _, t := range targets {
			addr, err := net.ResolveUDPAddr("udp4", t)
			if err != nil {
				continue
			}
			c, err := net.DialUDP("udp4", nil, addr)
			if err != nil {
				lastErr = err
				continue
			}
			if _, err := c.Write(pkt); err == nil {
				sent++
			} else {
				lastErr = err
			}
			c.Close()
		}
	}
	return sent, lastErr
}

// ---- 29: connection / outage log ----

var netlogMu sync.Mutex

func netlogPath() string {
	if d := b1Dir(); d != "" {
		return filepath.Join(d, "netlog.log")
	}
	return ""
}

func netlogf(format string, a ...any) {
	p := netlogPath()
	if p == "" {
		return
	}
	netlogMu.Lock()
	defer netlogMu.Unlock()
	if fi, err := os.Stat(p); err == nil && fi.Size() > 2<<20 {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
}

func openNetlog() {
	p := netlogPath()
	if p == "" {
		return
	}
	if _, err := os.Stat(p); err != nil {
		netlogf("Günlük oluşturuldu")
	}
	shellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16ptr("open"))), uintptr(unsafe.Pointer(utf16ptr(p))), 0, 0, 1)
}

// b1SessionEnded is called from stopSession.
func b1SessionEnded() {
	releaseInternetCode()
	b2SessionEnded()
	if sessionStartUnix.Load() != 0 {
		netlogf("OTURUM SONU süre=%s", sessionElapsed())
	}
	sasVerified.Store(false)
	opAdapt.Store(0)
	adaptLevel.Store(0)
	relayPath.Store(false)
	capture.Grayscale.Store(false)
	bookSeenOnce.Range(func(k, _ any) bool { bookSeenOnce.Delete(k); return true })
}

// ---- Bağlantılar page ----

func ensureBookControls() {
	if bookSearch != 0 {
		return
	}
	bookSearch = createControl(state.hwnd, "EDIT", "", 0x00000080, 0, 0, 10, 10, ID_BOOK_SEARCH)
	sendMessage.Call(bookSearch, 0x1501, 1, uintptr(unsafe.Pointer(utf16ptr("Ara: ad, etiket, bilgisayar…")))) // EM_SETCUEBANNER
	for _, b := range []struct {
		t  string
		id int
	}{{"Uyandır (WoL)", ID_BOOK_WOL}, {"Not Ekle", ID_BOOK_NOTE}, {"Notlar", ID_BOOK_NOTES}, {"Düzenle", ID_BOOK_EDIT}, {"Sil", ID_BOOK_DEL}} {
		bookButtons = append(bookButtons, createControl(state.hwnd, "BUTTON", b.t, 0x00000001, 0, 0, 10, 10, b.id))
	}
}

func hideBookControls() {
	hide(bookSearch)
	for _, b := range bookButtons {
		hide(b)
	}
}

func showBook() {
	clearChildren()
	ensureBookControls()
	loadBook()
	state.mu.Lock()
	state.mode = modeBook
	state.mu.Unlock()
	layoutUI()
	invalidateRect.Call(state.hwnd, 0, 1)
}

func layoutBook(w, h int) {
	moveControl(bookSearch, contentX, 180, 340, 36, true)
	x := w - gutter
	widths := []int{130, 96, 90, 96, 70}
	for i := len(bookButtons) - 1; i >= 0; i-- {
		x -= widths[i]
		moveControl(bookButtons[i], x, 180, widths[i], 36, true)
		x -= 8
	}
}

func bookFiltered() []bookEntry {
	loadBook()
	q := strings.ToLower(strings.TrimSpace(getText(bookSearch)))
	bookMu.Lock()
	var out []bookEntry
	for _, e := range book {
		hay := strings.ToLower(e.Host + " " + e.Name + " " + e.Tags)
		if q == "" || strings.Contains(hay, q) {
			c := *e
			out = append(out, c)
		}
	}
	bookMu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out
}

func paintBook(hdc uintptr, w, h int) {
	paintPageHeader(hdc, "Bağlantılar", "Adres defteri, etiketler, not geçmişi ve uzaktan uyandırma.")
	cw := w - contentX - gutter
	top := 230
	ch := h - footerH - 16 - top
	if ch < 80 {
		return
	}
	paintCard(hdc, contentX, top, cw, ch, uiPanelColor, uiBorderColor, 14)
	items := bookFiltered()
	bookRows = bookRows[:0]
	bookRowsTop = top + 10
	if len(items) == 0 {
		paintText(hdc, "Kayıt yok. Kurulan her oturumda karşı bilgisayar buraya otomatik eklenir.", contentX+22, top+20, cw-44, 22, uiBodyFont, uiFaintColor, txVLeft)
		return
	}
	y := bookRowsTop
	for _, e := range items {
		if y+bookRowH > top+ch-6 {
			break
		}
		bookRows = append(bookRows, e.Host)
		if strings.EqualFold(e.Host, bookSel) {
			paintRoundPanel(hdc, contentX+8, y, cw-16, bookRowH-4, uiPanelHi, 10)
			fillBand(hdc, contentX+10, y+12, 3, bookRowH-28, rgb(uiAccentColor))
		}
		var dot uint32 = uiFaintColor
		if len(e.MACs) > 0 {
			dot = uiCyanColor
		}
		paintDot(hdc, contentX+28, y+18, 4, dot)
		paintText(hdc, e.display(), contentX+44, y+4, cw/2, 24, uiSubtitleFont, uiTextColor, txEnd)
		sub := e.Role
		if e.Tags != "" {
			sub = "#" + strings.Join(strings.Fields(strings.ReplaceAll(e.Tags, ",", " ")), "  #") + "   •   " + sub
		}
		if len(e.Notes) > 0 {
			sub += "   •   Son not: " + e.Notes[len(e.Notes)-1].Text
		}
		paintText(hdc, sub, contentX+44, y+28, cw-300, 20, uiSmallFont, uiMutedColor, txEnd)
		right := fmt.Sprintf("%s   •   %d oturum", relativeTime(e.LastSeen), e.Sessions)
		paintText(hdc, right, contentX+cw-280, y+4, 260, 24, uiSmallFont, uiFaintColor, txVRight)
		if len(e.MACs) > 0 {
			paintText(hdc, "WoL: "+e.MACs[0], contentX+cw-280, y+28, 260, 20, uiSmallFont, uiFaintColor, txVRight)
		}
		y += bookRowH
	}
}

func bookClick(x, y int) bool {
	state.mu.Lock()
	mode := state.mode
	state.mu.Unlock()
	if x >= 0 && x < navW && y >= 148 && y < 200 {
		sidebarPage.Store(1)
		showBook()
		return true
	}
	if mode != modeBook || x < contentX || y < bookRowsTop {
		return false
	}
	i := (y - bookRowsTop) / bookRowH
	if i >= 0 && i < len(bookRows) {
		bookSel = bookRows[i]
		invalidateRect.Call(state.hwnd, 0, 0)
		return true
	}
	return false
}

func onBookCommand(id int) bool {
	switch id {
	case ID_BOOK_SEARCH:
		invalidateRect.Call(state.hwnd, 0, 0)
		return true
	case ID_BOOK_WOL, ID_BOOK_NOTE, ID_BOOK_NOTES, ID_BOOK_EDIT, ID_BOOK_DEL:
	default:
		return false
	}
	e, ok := bookGet(bookSel)
	if !ok {
		setStatus("● Önce listeden bir bilgisayar seçin.")
		return true
	}
	switch id {
	case ID_BOOK_WOL:
		if len(e.MACs) == 0 {
			setStatus("● Bu bilgisayarın MAC adresi bilinmiyor (bir kez destek alan taraf olarak bağlanmalı).")
			return true
		}
		n, err := sendWOL(e.MACs)
		if n == 0 && err != nil {
			setStatus("● Uyandırma paketi gönderilemedi: " + err.Error())
		} else {
			netlogf("WoL gönderildi: %s %v", e.Host, e.MACs)
			setStatus(fmt.Sprintf("● Uyandırma paketi gönderildi (%d) — %s. Açılması 1-2 dk sürebilir.", n, e.display()))
		}
	case ID_BOOK_NOTE:
		addNoteForHost(e.Host)
	case ID_BOOK_NOTES:
		text := notesText(e, 0)
		if text == "" {
			text = "Henüz not yok."
		}
		go messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr(text))), uintptr(unsafe.Pointer(utf16ptr("Notlar — "+e.display()))), 0x00000040)
	case ID_BOOK_EDIT:
		name, ok := promptText("Düzenle", "Görünen ad ("+e.Host+"):", e.Name)
		if !ok {
			return true
		}
		tags, ok := promptText("Düzenle", "Etiketler (virgülle: müşteri, şube, konum):", e.Tags)
		if !ok {
			tags = e.Tags
		}
		bookUpdate(e.Host, func(x *bookEntry) { x.Name = strings.TrimSpace(name); x.Tags = strings.TrimSpace(tags) })
	case ID_BOOK_DEL:
		r, _, _ := messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr(e.display()+" adres defterinden silinsin mi? (notlar da silinir)"))), uintptr(unsafe.Pointer(utf16ptr("Sil"))), 0x00000004|0x00000030)
		if r == 6 {
			bookMu.Lock()
			delete(book, strings.ToLower(e.Host))
			saveBookLocked()
			bookMu.Unlock()
			bookSel = ""
			invalidateRect.Call(state.hwnd, 0, 0)
		}
	}
	return true
}

// ---- modal single-line text prompt ----

var (
	promptHwnd   uintptr
	promptEdit   uintptr
	promptResult string
	promptOK     bool
	promptDone   bool
	promptReg    sync.Once
	enableWindow = user32.NewProc("EnableWindow")
)

func promptProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	switch m {
	case 0x0111: // WM_COMMAND
		switch wParam & 0xffff {
		case 1: // IDOK
			promptResult, promptOK, promptDone = getText(promptEdit), true, true
			destroyWindow.Call(hwnd)
			return 0
		case 3: // Kopyala (code dialog)
			if copyToClipboard(getText(promptEdit)) {
				setWindowText.Call(lParam, uintptr(unsafe.Pointer(utf16ptr("Kopyalandı ✓"))))
			}
			return 0
		case 2: // IDCANCEL
			promptDone = true
			destroyWindow.Call(hwnd)
			return 0
		}
	case 0x0010: // WM_CLOSE
		promptDone = true
		destroyWindow.Call(hwnd)
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

// promptText shows a small modal input box on the GUI thread.
func promptText(title, label, initial string) (string, bool) {
	cls := utf16ptr("NexDeskPrompt")
	promptReg.Do(func() {
		wc := wndclassex{CbSize: uint32(unsafe.Sizeof(wndclassex{})), LpfnWndProc: syscall.NewCallback(promptProc), HInstance: moduleHandle(), HbrBackground: 16, LpszClassName: cls, HIcon: appIcon(32), HIconSm: appIcon(16)} // COLOR_BTNFACE+1
		registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	})
	var wr rect
	getWindowRect.Call(state.hwnd, uintptr(unsafe.Pointer(&wr)))
	const pw, ph = 480, 190
	x := int(wr.Left) + (int(wr.Right-wr.Left)-pw)/2
	y := int(wr.Top) + (int(wr.Bottom-wr.Top)-ph)/2
	promptHwnd, _, _ = createWindow.Call(0x00000001, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16ptr(title))),
		0x80000000|0x00C00000|0x00080000, uintptr(x), uintptr(y), pw, ph, state.hwnd, 0, moduleHandle(), 0)
	if promptHwnd == 0 {
		return "", false
	}
	child := func(class, text string, style uint32, x, y, w, h, id int) uintptr {
		r, _, _ := createWindow.Call(0, uintptr(unsafe.Pointer(utf16ptr(class))), uintptr(unsafe.Pointer(utf16ptr(text))),
			uintptr(0x40000000|0x10000000|style), uintptr(x), uintptr(y), uintptr(w), uintptr(h), promptHwnd, uintptr(id), moduleHandle(), 0)
		setControlFont(r, uiBodyFont)
		return r
	}
	child("STATIC", label, 0, 16, 14, pw-48, 22, 0)
	promptEdit = child("EDIT", initial, 0x00800000|0x00010000|0x00000080, 16, 42, pw-48, 30, 100) // WS_BORDER|WS_TABSTOP|ES_AUTOHSCROLL
	child("BUTTON", "Tamam", 0x00010000|0x00000001, pw-236, 92, 100, 34, 1)                       // BS_DEFPUSHBUTTON
	child("BUTTON", "İptal", 0x00010000, pw-128, 92, 100, 34, 2)
	showWindow.Call(promptHwnd, 5)                       // SW_SHOW — without this the box stayed invisible while the main window was disabled (looked frozen)
	sendMessage.Call(promptEdit, 0x00B1, 0, ^uintptr(0)) // EM_SETSEL all
	setFocus.Call(promptEdit)
	promptResult, promptOK, promptDone = "", false, false
	enableWindow.Call(state.hwnd, 0)
	var m msg
	for !promptDone {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			postQuit.Call(m.WParam)
			break
		}
		if d, _, _ := isDialogMessage.Call(promptHwnd, uintptr(unsafe.Pointer(&m))); d != 0 {
			continue
		}
		translateMsg.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	enableWindow.Call(state.hwnd, 1)
	setWindowPos.Call(state.hwnd, 0, 0, 0, 0, 0, 0x0001|0x0002|0x0004) // keep z-order, re-activate below
	user32.NewProc("SetForegroundWindow").Call(state.hwnd)
	return promptResult, promptOK
}
