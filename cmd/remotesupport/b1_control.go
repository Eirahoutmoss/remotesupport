//go:build windows

package main

// Build 1 session features that ride on the control data channel:
// secure one-shot clipboard (7), repair pack (13), adaptive quality (17),
// SAS verification (28) and the agent-side click ripple (47).

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/eirahoutmoss/remotesupport/client/capture"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/eirahoutmoss/remotesupport/shared/protocol"
)

const (
	ID_CLIP_SECURE    = 5101
	ID_REPAIR_DNS     = 5110
	ID_REPAIR_TEMP    = 5111
	ID_REPAIR_SPOOLER = 5112
	ID_REPAIR_EXPLORE = 5113
	ID_REPAIR_WINSOCK = 5114
	ID_REPAIR_SFC     = 5115
	ID_NOTE_ADD       = 5120
	ID_SAS_VERIFY     = 5121
	ID_NETLOG_OPEN    = 5122
	WM_APP_RIPPLE     = 0x8010
	WM_APP_NOTES      = 0x8011
)

var (
	sasVerified atomic.Bool  // SAS confirmed out-of-band for this session
	adaptLevel  atomic.Int32 // agent: 0 normal … 3 lowest (set by operator)
	opAdapt     atomic.Int32 // operator: level last requested
	clipSeqNum  = user32.NewProc("GetClipboardSequenceNumber")
	regClipFmt  = user32.NewProc("RegisterClipboardFormatW")
	setTimer    = user32.NewProc("SetTimer")
	killTimer   = user32.NewProc("KillTimer")
)

func currentPeer() *webrtcpeer.Peer {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.peer
}

func sendCtl(msg string) bool {
	if p := currentPeer(); p != nil {
		return p.SendControlText(msg) == nil
	}
	return false
}

// handleB1Control consumes Build 1 control messages. It returns true when the
// message was fully handled; HELLO is observed but passed through.
func handleB1Control(peer *webrtcpeer.Peer, raw string) bool {
	if handleB2Control(peer, raw) {
		return true
	}
	switch {
	case strings.HasPrefix(raw, "HELLO:"):
		bookSeen(strings.TrimSpace(strings.TrimPrefix(raw, "HELLO:")))
		return false
	case strings.HasPrefix(raw, "MACS:"):
		host, macs, _ := strings.Cut(strings.TrimPrefix(raw, "MACS:"), "|")
		bookSetMACs(host, strings.Split(macs, ","))
		return true
	case strings.HasPrefix(raw, "CLIPONCE:"):
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "CLIPONCE:"))
		if err != nil || len(data) > 48*1024 {
			return true
		}
		if copySecureClipboard(string(data), 30*time.Second) {
			setStatus("● Güvenli pano alındı — 30 sn sonra otomatik silinecek, pano geçmişine yazılmadı.")
		}
		return true
	case strings.HasPrefix(raw, "REPAIR:"):
		if state.mode == 1 {
			go runRepair(peer, strings.TrimPrefix(raw, "REPAIR:"))
		}
		return true
	case strings.HasPrefix(raw, "REGDEV_REQ:"):
		// Target side: technician asks to promote this session to permanent
		// (unattended) access. The confirm dialog blocks, so run it off-thread.
		if state.mode == 1 {
			go handleRegDevRequest(peer, strings.TrimPrefix(raw, "REGDEV_REQ:"))
		}
		return true
	case strings.HasPrefix(raw, "REGDEV_OK:"):
		// Operator side: target approved; save the device for direct reconnect.
		if state.mode == 2 {
			id, pass, _ := strings.Cut(strings.TrimPrefix(raw, "REGDEV_OK:"), "|")
			id = strings.TrimSpace(id)
			if id != "" {
				upsertTechDevice(id, pass)
				populateTechDevCombo()
				setStatus("● Cihaz kaydedildi: " + id + " — artık Destek Ver'de listeden seçip onay sormadan bağlanabilirsiniz.")
				auditLog("Kalıcı cihaz kaydedildi: " + id)
			}
		}
		return true
	case raw == "REGDEV_NO":
		if state.mode == 2 {
			setStatus("● Karşı taraf kalıcı erişimi reddetti.")
		}
		return true
	case strings.HasPrefix(raw, "REPAIR_RES:"):
		msg := strings.TrimPrefix(raw, "REPAIR_RES:")
		netlogf("Onarım sonucu: %s", strings.ReplaceAll(msg, "\r\n", " / "))
		go messageBox.Call(0, uintptr(unsafe.Pointer(utf16ptr(msg))), uintptr(unsafe.Pointer(utf16ptr("Onarım Paketi"))), 0x00000040)
		return true
	case strings.HasPrefix(raw, "ADAPT:"):
		var lv int
		if _, err := fmt.Sscanf(strings.TrimPrefix(raw, "ADAPT:"), "%d", &lv); err == nil && lv >= 0 && lv <= 3 {
			adaptLevel.Store(int32(lv))
			capture.Grayscale.Store(lv >= 3)
		}
		return true
	case raw == "SAS_OK":
		sasVerified.Store(true)
		netlogf("SAS karşı tarafça doğrulandı")
		setStatus("● Güvenlik kodu (SAS) destek veren tarafından doğrulandı.")
		return true
	case raw == "SAS_BAD":
		netlogf("SAS UYUŞMADI — oturum kapatıldı")
		setStatus("● Güvenlik kodu uyuşmadı; oturum güvenlik için kapatıldı.")
		if state.hwnd != 0 {
			postMessage.Call(state.hwnd, WM_APP_IDLE_CLOSE, 0, 0)
		}
		return true
	}
	return false
}

// onB1Connected runs once per successful (re)connection from watchPeer.
func onB1Connected(peer *webrtcpeer.Peer) {
	path, sas := getConnInfo()
	role := "operatör"
	if state.mode == 1 {
		role = "agent"
	}
	netlogf("BAĞLANDI rol=%s yol=%s sas=%s", role, path, sas)
	onB2Connected(peer)
	if state.mode == 1 {
		host, _ := os.Hostname()
		if macs := localMACs(); len(macs) > 0 {
			go sendCtlRetry(peer, "MACS:"+host+"|"+strings.Join(macs, ","))
		}
		return
	}
}

// ---- 7: secure clipboard ----

// copySecureClipboard puts text on the clipboard flagged so Windows keeps it
// out of clipboard history / cloud sync, then wipes it after ttl unless
// something else has been copied meanwhile.
func copySecureClipboard(s string, ttl time.Duration) bool {
	if !copyToClipboard(s) {
		return false
	}
	if r, _, _ := openClipboard.Call(state.hwnd); r != 0 {
		for _, name := range []string{"ExcludeClipboardContentFromMonitorProcessing", "CanIncludeInClipboardHistory", "CanUploadToCloudClipboard"} {
			f, _, _ := regClipFmt.Call(uintptr(unsafe.Pointer(utf16ptr(name))))
			if f == 0 {
				continue
			}
			h, _, _ := globalAlloc.Call(0x0002, 4)
			if h == 0 {
				continue
			}
			if p, _, _ := globalLock.Call(h); p != 0 {
				*(*uint32)(unsafe.Pointer(p)) = 0
				globalUnlock.Call(h)
			}
			setClipboardData.Call(f, h)
		}
		closeClipboard.Call()
	}
	seq, _, _ := clipSeqNum.Call()
	go func() {
		time.Sleep(ttl)
		if now, _, _ := clipSeqNum.Call(); now != seq {
			return // user copied something else; leave it alone
		}
		if r, _, _ := openClipboard.Call(state.hwnd); r != 0 {
			emptyClipboard.Call()
			closeClipboard.Call()
			setStatus("● Güvenli pano içeriği silindi.")
		}
	}()
	return true
}

func sendSecureClipboard() {
	text, ok := readClipboardText()
	if !ok || text == "" {
		setStatus("● Panoda metin yok.")
		return
	}
	if len(text) > 48*1024 {
		setStatus("● Pano metni 48 KB sınırını aşıyor.")
		return
	}
	if sendCtl("CLIPONCE:" + base64.StdEncoding.EncodeToString([]byte(text))) {
		setStatus("● Güvenli pano gönderildi (karşıda 30 sn sonra silinir).")
	}
}

// ---- 13: repair pack (agent executes a fixed whitelist only) ----

var repairNames = map[string]string{
	"dns":      "DNS önbelleğini temizle",
	"temp":     "Geçici dosyaları temizle",
	"spooler":  "Yazıcı kuyruğunu sıfırla",
	"explorer": "Gezgin'i yeniden başlat",
	"winsock":  "Ağ yığınını sıfırla (winsock)",
	"sfc":      "Sistem dosyası denetimi (sfc)",
}

func hiddenCmd(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}

func runRepair(peer *webrtcpeer.Peer, name string) {
	defer logCrash("repair")
	title, ok := repairNames[name]
	if !ok {
		return
	}
	setStatus("● Destek veren: " + title + " çalıştırılıyor…")
	auditLog("Onarım: " + title)
	res := "Başarılı"
	needAdmin := false
	run := func(c *exec.Cmd) bool {
		if err := c.Run(); err != nil {
			res = "Hata: " + err.Error()
			return false
		}
		return true
	}
	switch name {
	case "dns":
		run(hiddenCmd("ipconfig", "/flushdns"))
	case "temp":
		freed, n := cleanTemp()
		res = fmt.Sprintf("Başarılı — %d dosya, %.1f MB temizlendi", n, float64(freed)/(1<<20))
	case "spooler":
		needAdmin = true
		if run(hiddenCmd("net", "stop", "spooler", "/y")) {
			dir := filepath.Join(os.Getenv("SystemRoot"), "System32", "spool", "PRINTERS")
			if ents, err := os.ReadDir(dir); err == nil {
				for _, e := range ents {
					_ = os.Remove(filepath.Join(dir, e.Name()))
				}
			}
			run(hiddenCmd("net", "start", "spooler"))
		}
	case "explorer":
		_ = hiddenCmd("taskkill", "/f", "/im", "explorer.exe").Run()
		time.Sleep(800 * time.Millisecond)
		if err := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "explorer.exe")).Start(); err != nil {
			res = "Hata: " + err.Error()
		}
	case "winsock":
		needAdmin = true
		if run(hiddenCmd("netsh", "winsock", "reset")) {
			res = "Başarılı — değişiklik yeniden başlatmadan sonra etkin olur"
		}
	case "sfc":
		needAdmin = true
		setStatus("● Sistem dosyası denetimi sürüyor (birkaç dakika sürebilir)…")
		if run(hiddenCmd("sfc", "/scannow")) {
			res = "Tamamlandı — ayrıntı: %SystemRoot%\\Logs\\CBS\\CBS.log"
		}
	}
	if needAdmin && !isElevated() && strings.HasPrefix(res, "Hata") {
		res += "\r\n(Bu işlem yönetici yetkisi ister: karşı tarafta NexDesk'i yönetici olarak başlatın.)"
	}
	setStatus("● " + title + ": " + res)
	if peer != nil {
		_ = peer.SendControlText("REPAIR_RES:" + title + "\r\n" + res)
	}
}

func cleanTemp() (freed int64, count int) {
	dir := os.TempDir()
	cutoff := time.Now().Add(-24 * time.Hour)
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil && info.ModTime().Before(cutoff) {
			if os.Remove(p) == nil {
				freed += info.Size()
				count++
			}
		}
		return nil
	})
	return
}

func requestRepair(name string) {
	title := repairNames[name]
	if name == "winsock" || name == "explorer" {
		r, _, _ := messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr(title+" karşı bilgisayarda çalıştırılsın mı?"))), uintptr(unsafe.Pointer(utf16ptr("Onarım Paketi"))), 0x00000004|0x00000030)
		if r != 6 {
			return
		}
	}
	if sendCtl("REPAIR:" + name) {
		netlogf("Onarım istendi: %s", title)
		setStatus("● İstendi: " + title)
	}
}

// ---- 17: adaptive quality (operator measures, agent applies) ----

func adaptiveWatch(peer *webrtcpeer.Peer) {
	defer logCrash("adaptive")
	opAdapt.Store(0)
	low, good := 0, 0
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		if sessionStartUnix.Load() == 0 || peerDead(peer) || currentPeer() != peer {
			return
		}
		if time.Since(time.Unix(0, sessionStartUnix.Load())) < 5*time.Second {
			continue
		}
		fps := fpsValue.Load()
		lv := opAdapt.Load()
		switch {
		case fps <= 4:
			low++
			good = 0
		case fps >= 9:
			good++
			low = 0
		default:
			low, good = 0, 0
		}
		if low >= 3 && lv < 3 {
			lv++
			low = 0
		} else if good >= 8 && lv > 0 {
			lv--
			good = 0
		} else {
			continue
		}
		opAdapt.Store(lv)
		_ = peer.SendControlText(fmt.Sprintf("ADAPT:%d", lv))
		netlogf("Uyarlamalı kalite seviyesi %d (fps=%d)", lv, fps)
	}
}

// adaptParams caps quality / height for the agent's current adapt level.
func adaptParams(quality, maxHeight int) (int, int) {
	capH := func(h, c int) int {
		if h == 0 || h > c {
			return c
		}
		return h
	}
	lv := adaptLevel.Load()
	if relayPath.Load() && lv < 1 {
		lv = 1 // relayed via TURN: start at 720p / lower quality
	}
	switch lv {
	case 1:
		return min(quality, 70), capH(maxHeight, 720)
	case 2:
		return min(quality, 55), capH(maxHeight, 480)
	case 3:
		return min(quality, 45), capH(maxHeight, 480)
	}
	return quality, maxHeight
}

func adaptLabel() string {
	return map[int32]string{1: "Uyarlama: Orta", 2: "Uyarlama: Düşük", 3: "Uyarlama: Gri ton"}[opAdapt.Load()]
}

// ---- 28: SAS verification ----

func verifySAS() {
	_, sas := getConnInfo()
	if sas == "" {
		setStatus("● Güvenlik kodu henüz hazır değil.")
		return
	}
	text := "Karşı tarafa ekranındaki güvenlik kodunu (SAS) sorun.\r\n\r\nSizdeki kod:   " + sas + "\r\n\r\nKodlar birebir aynı mı?\r\n\r\n(Hayır derseniz bağlantı araya giren biri olabileceği için hemen kesilir.)"
	r, _, _ := messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr(text))), uintptr(unsafe.Pointer(utf16ptr("Uçtan Uca Doğrulama"))), 0x00000004|0x00000020)
	if r == 6 {
		sasVerified.Store(true)
		sendCtl("SAS_OK")
		netlogf("SAS doğrulandı: %s", sas)
		auditLog("SAS doğrulandı")
		setStatus("● Güvenlik kodu doğrulandı — bağlantı uçtan uca güvenli.")
	} else {
		sendCtl("SAS_BAD")
		netlogf("SAS UYUŞMADI: %s — oturum kesildi", sas)
		auditLog("SAS uyuşmadı, oturum kesildi")
		stopSession()
		home()
		setStatus("● Güvenlik kodu uyuşmadı — bağlantı kesildi.")
	}
}

// ---- 47: click ripple on the agent screen ----

var (
	rippleHwnd  uintptr
	rippleStep  int
	rippleClass = "NexDeskRipple"
	rippleSize  = 56
)

func rippleProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	switch m {
	case 0x000F: // WM_PAINT
		var ps paintStruct
		hdc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if hdc != 0 {
			black, _, _ := getStockObject.Call(4)
			r := rect{0, 0, int32(rippleSize), int32(rippleSize)}
			fillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), black)
			pen, _, _ := createPen.Call(0, 4, rgb(uiAccentHi))
			hollow, _, _ := getStockObject.Call(5) // NULL_BRUSH
			op, _, _ := selectObject.Call(hdc, pen)
			ob, _, _ := selectObject.Call(hdc, hollow)
			inset := 4 + rippleStep*2
			ellipse.Call(hdc, uintptr(inset), uintptr(inset), uintptr(rippleSize-inset), uintptr(rippleSize-inset))
			selectObject.Call(hdc, op)
			selectObject.Call(hdc, ob)
			deleteObject.Call(pen)
			endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	case 0x0113: // WM_TIMER
		rippleStep++
		if rippleStep > 8 {
			killTimer.Call(hwnd, 1)
			hide(hwnd)
			return 0
		}
		setLayeredWinAttr.Call(hwnd, 0, uintptr(230-rippleStep*25), 0x1|0x2) // LWA_COLORKEY|LWA_ALPHA
		invalidateRect.Call(hwnd, 0, 1)
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

// showRipple must run on the GUI thread (posted via WM_APP_RIPPLE).
func showRipple(x, y int32) {
	if rippleHwnd == 0 {
		cls := utf16ptr(rippleClass)
		wc := wndclassex{CbSize: uint32(unsafe.Sizeof(wndclassex{})), LpfnWndProc: syscall.NewCallback(rippleProc), HInstance: moduleHandle(), LpszClassName: cls}
		registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		// WS_EX_LAYERED|TOPMOST|TRANSPARENT|TOOLWINDOW|NOACTIVATE, WS_POPUP
		rippleHwnd, _, _ = createWindow.Call(0x00080000|0x00000008|0x00000020|0x00000080|0x08000000, uintptr(unsafe.Pointer(cls)), 0, 0x80000000, 0, 0, uintptr(rippleSize), uintptr(rippleSize), 0, 0, moduleHandle(), 0)
		if rippleHwnd == 0 {
			return
		}
		setWinDisplayAff.Call(rippleHwnd, 0x11) // WDA_EXCLUDEFROMCAPTURE: operator sees the real cursor instead
	}
	rippleStep = 0
	setLayeredWinAttr.Call(rippleHwnd, 0, 230, 0x1|0x2)
	half := int32(rippleSize / 2)
	setWindowPos.Call(rippleHwnd, ^uintptr(0), uintptr(x-half), uintptr(y-half), uintptr(rippleSize), uintptr(rippleSize), 0x0010|0x0040) // HWND_TOPMOST, NOACTIVATE|SHOWWINDOW
	invalidateRect.Call(rippleHwnd, 0, 1)
	killTimer.Call(rippleHwnd, 1)
	setTimer.Call(rippleHwnd, 1, 45, 0)
}

// noteRemoteClick is called from the input injector for mouse-down events.
func noteRemoteClick(ev protocol.InputEvent) {
	mon, ok := remoteTargetMonitor()
	if !ok || state.hwnd == 0 {
		return
	}
	px := int32(float64(mon.X) + ev.X*float64(maxInt(1, mon.Width-1)))
	py := int32(float64(mon.Y) + ev.Y*float64(maxInt(1, mon.Height-1)))
	postMessage.Call(state.hwnd, WM_APP_RIPPLE, uintptr(uint32(px)), uintptr(uint32(py)))
}

// b1WndProc handles Build 1 window messages; ok=false lets wndProc continue.
func b1WndProc(hwnd uintptr, m uint32, wParam, lParam uintptr) (uintptr, bool) {
	if r, ok := b2WndProc(hwnd, m, wParam, lParam); ok {
		return r, true
	}
	switch m {
	case WM_APP_RIPPLE:
		showRipple(int32(uint32(wParam)), int32(uint32(lParam)))
		return 0, true
	case WM_APP_NOTES:
		showHostNotes()
		return 0, true
	case 0x0202: // WM_LBUTTONUP
		if clickCopiesInternetCode(int(int16(lParam&0xffff)), int(int16((lParam>>16)&0xffff))) {
			return 0, true
		}
		if bookClick(int(int16(lParam&0xffff)), int(int16((lParam>>16)&0xffff))) {
			return 0, true
		}
	}
	return 0, false
}

// ---- menu wiring ----

func appendB1Menu(menu uintptr, mi func(m, flags, id uintptr, label string)) {
	mi(menu, 0x0000, ID_CLIP_SECURE, "Panoyu Güvenli Gönder (tek seferlik)")
	sub, _, _ := createPopupMenu.Call()
	if sub != 0 {
		for _, it := range []struct {
			id  int
			key string
		}{{ID_REPAIR_DNS, "dns"}, {ID_REPAIR_TEMP, "temp"}, {ID_REPAIR_SPOOLER, "spooler"}, {ID_REPAIR_EXPLORE, "explorer"}, {ID_REPAIR_WINSOCK, "winsock"}, {ID_REPAIR_SFC, "sfc"}} {
			mi(sub, 0x0000, uintptr(it.id), repairNames[it.key])
		}
		mi(menu, 0x0010, sub, "Onarım Paketi")
	}
	mi(menu, 0x0800, 0, "")
	sasLabel := "Güvenlik Kodunu Doğrula (SAS)"
	if sasVerified.Load() {
		sasLabel = "Güvenlik Kodu Doğrulandı ✓"
	}
	mi(menu, 0x0000, ID_SAS_VERIFY, sasLabel)
	mi(menu, 0x0000, ID_NOTE_ADD, "Bu Bilgisayara Not Ekle…")
	mi(menu, 0x0000, ID_NETLOG_OPEN, "Bağlantı Günlüğünü Aç")
	appendB2Menu(menu, mi)
}

// onB1Command returns true when id belonged to Build 1.
func onB1Command(id int) bool {
	switch id {
	case ID_CLIP_SECURE:
		go sendSecureClipboard()
	case ID_REPAIR_DNS:
		requestRepair("dns")
	case ID_REPAIR_TEMP:
		requestRepair("temp")
	case ID_REPAIR_SPOOLER:
		requestRepair("spooler")
	case ID_REPAIR_EXPLORE:
		requestRepair("explorer")
	case ID_REPAIR_WINSOCK:
		requestRepair("winsock")
	case ID_REPAIR_SFC:
		requestRepair("sfc")
	case ID_SAS_VERIFY:
		verifySAS()
	case ID_NOTE_ADD:
		addNoteForHost(getRemoteHost())
	case ID_NETLOG_OPEN:
		openNetlog()
	default:
		return onInetCommand(id) || onManualCommand(id) || onB2Command(id) || onBookCommand(id)
	}
	return true
}

// localMACs lists hardware addresses of up, non-loopback interfaces (for WoL).
func localMACs() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || len(i.HardwareAddr) != 6 {
			continue
		}
		name := strings.ToLower(i.Name)
		if strings.Contains(name, "virtual") || strings.Contains(name, "vethernet") || strings.Contains(name, "bluetooth") {
			continue
		}
		out = append(out, i.HardwareAddr.String())
	}
	return out
}

// relayPath is true while the session runs through a TURN relay.
var relayPath atomic.Bool

// sendCtlRetry sends msg once the control data channel is open. Over a TURN
// relay the channel opens noticeably after the peer reports "connected", and
// messages sent before that (HELLO, monitor list) were silently lost — which
// left the operator without monitor/resolution buttons.
func sendCtlRetry(peer *webrtcpeer.Peer, msg string) {
	for i := 0; i < 100; i++ {
		if peer.SendControlText(msg) == nil {
			return
		}
		if peerDead(peer) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// congestion drives adaptive quality on the agent. The old operator-side
// rule read "few frames per second" as a slow link, but with tile deltas an
// unchanged screen legitimately sends nothing — so it degraded quality on
// idle screens. Here we measure what actually matters: how often the capture
// tick finds the screen channel still busy with the previous frame.
type congestion struct {
	ticks, busy int
	calm        int
	since       time.Time
}

func (c *congestion) tick(peer *webrtcpeer.Peer, busy bool) {
	if c.since.IsZero() {
		c.since = time.Now()
	}
	c.ticks++
	if busy {
		c.busy++
	}
	if time.Since(c.since) < 2*time.Second {
		return
	}
	ratio := float64(c.busy) / float64(max(1, c.ticks))
	c.ticks, c.busy, c.since = 0, 0, time.Now()
	lv := adaptLevel.Load()
	switch {
	case ratio > 0.6 && lv < 2: // max level 2: keep colour; grey helped little
		lv++
		c.calm = 0
	case ratio < 0.15:
		c.calm++
		if c.calm >= 4 && lv > 0 { // ~8 s of headroom before stepping back up
			lv--
			c.calm = 0
		} else {
			return
		}
	default:
		c.calm = 0
		return
	}
	adaptLevel.Store(lv)
	capture.Grayscale.Store(false)
	netlogf("Uyarlamalı kalite seviyesi %d (meşgul oranı %.0f%%)", lv, ratio*100)
	go sendCtlRetry(peer, fmt.Sprintf("ADAPT_INFO:%d", lv))
}
