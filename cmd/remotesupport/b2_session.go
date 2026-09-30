//go:build windows

package main

// Build 2 session features: role guard for control messages, fraud shield (8),
// reverse screen (19), session handover (27), drag-and-drop files (34) and
// automatic transfer resume after reconnect (6).

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
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
)

const (
	ID_REVERSE        = 5130
	ID_HANDOVER       = 5131
	ID_RECORD         = 5132
	ID_MACRO_REC      = 5133
	ID_MACRO_STOP     = 5134
	ID_MACRO_FOLDER   = 5135
	ID_MACRO_BASE     = 5300 // + index of saved macro (max 50)
	WM_APP_REVERSE    = 0x8020
	WM_APP_HANDOVER   = 0x8021
	WM_APP_FRAUD_ASK  = 0x8022
	wmDropFiles       = 0x0233
	reverseMonitorTag = 0xFFFF
)

var (
	dragAcceptFiles = shell32.NewProc("DragAcceptFiles")
	dragQueryFile   = shell32.NewProc("DragQueryFileW")
	dragFinish      = shell32.NewProc("DragFinish")
	getForegroundWn = user32.NewProc("GetForegroundWindow")
	getWindowTextW  = user32.NewProc("GetWindowTextW")
)

// ---- role guard ----

// agentOnly messages act on the machine that is being supported; the operator
// must never obey them (a hostile "agent" could otherwise lock or drive the
// technician's PC). operatorOnly is the reverse.
var agentOnly = []string{"INPUT:", "CURTAIN:", "LOCKINPUT:", "ANNOT:", "ANNOT_CLEAR", "SYSINFO_REQ", "CLIP_REQ", "REPAIR:", "RESOLUTION:", "MONITOR:", "ADAPT:", "HANDOVER_REQ", "REVERSE:", "REC:"}
var operatorOnly = []string{"SYSINFO:", "MONITORS:", "REPAIR_RES:", "HANDOVER_CODE:", "HANDOVER_DONE", "HANDOVER_NO", "FRAUD_HOLD:", "MACS:", "SECURE_DESKTOP:"}

func hasAnyPrefix(s string, ps []string) bool {
	for _, p := range ps {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// handleB2Control returns true when raw was consumed (handled or rejected).
func handleB2Control(peer *webrtcpeer.Peer, raw string) bool {
	agent := state.mode == 1
	if !agent && hasAnyPrefix(raw, agentOnly) && !strings.HasPrefix(raw, "MONITORS:") {
		return true
	}
	if agent && hasAnyPrefix(raw, operatorOnly) {
		return true
	}
	if agent && strings.HasPrefix(raw, "INPUT:") && fraudHold.Load() {
		return true
	}
	switch {
	case strings.HasPrefix(raw, "FILE_RESUME:"):
		var id string
		var off int64
		if parts := strings.SplitN(strings.TrimPrefix(raw, "FILE_RESUME:"), ":", 2); len(parts) == 2 {
			id = parts[0]
			fmt.Sscanf(parts[1], "%d", &off)
			deliverResume(id, off)
		}
		return true
	case strings.HasPrefix(raw, "SECURE_DESKTOP:"):
		if raw == "SECURE_DESKTOP:1" {
			netlogf("Karşı tarafta güvenli masaüstü açık (Ctrl+Alt+Del/UAC/kilit)")
			setStatus("● Karşı tarafta Ctrl+Alt+Del / UAC / kilit ekranı açık — kullanıcı kapatınca görüntü otomatik devam eder.")
		} else {
			setStatus("● Karşı taraf güvenli ekrandan döndü, görüntü devam ediyor.")
		}
		return true
	case strings.HasPrefix(raw, "FRAUD_HOLD:"):
		if raw == "FRAUD_HOLD:1" {
			netlogf("Karşı tarafta hassas sayfa: görüntü/kontrol durduruldu")
			setStatus("● Karşı tarafta banka/ödeme sayfası açıldı — görüntü ve kontrol kullanıcı onayına kadar durduruldu.")
		} else {
			setStatus("● Karşı taraf devam etmeyi onayladı.")
		}
		return true
	case strings.HasPrefix(raw, "REVERSE:"):
		on := raw == "REVERSE:1"
		if on {
			peer.SetScreenHandler(reverseFrame)
		}
		if state.hwnd != 0 {
			postMessage.Call(state.hwnd, WM_APP_REVERSE, map[bool]uintptr{true: 1}[on], 0)
		}
		return true
	case strings.HasPrefix(raw, "REC:"):
		remoteRecording.Store(raw == "REC:1")
		if raw == "REC:1" {
			setStatus("● Destek veren bu oturumu video olarak kaydediyor.")
		} else {
			setStatus("● Oturum kaydı durduruldu.")
		}
		if state.hwnd != 0 {
			invalidateRect.Call(state.hwnd, 0, 0)
		}
		return true
	case raw == "HANDOVER_REQ":
		go agentHandover(peer)
		return true
	case strings.HasPrefix(raw, "HANDOVER_CODE:"):
		code := strings.TrimPrefix(raw, "HANDOVER_CODE:")
		link := buildJoinLink(code)
		copyToClipboard(link)
		netlogf("Devir kodu alındı: %s", code)
		msg := "Devir kodu:  " + formatCode(code) + "\r\n\r\nBağlantı linki panoya kopyalandı; yeni teknisyene iletin.\r\nO bağlanıp karşı taraf onaylayınca sizin oturumunuz kapanır."
		go messageBox.Call(0, uintptr(unsafe.Pointer(utf16ptr(msg))), uintptr(unsafe.Pointer(utf16ptr("Oturum Devri"))), 0x00000040)
		return true
	case raw == "HANDOVER_NO":
		setStatus("● Karşı taraf oturum devrini reddetti.")
		return true
	case raw == "HANDOVER_DONE":
		userStopped.Store(true)
		if state.hwnd != 0 {
			postMessage.Call(state.hwnd, WM_APP_HANDOVER, 0, 0)
		}
		return true
	}
	return false
}

func onB2Connected(peer *webrtcpeer.Peer) {
	if state.mode == 1 {
		finishHandover(peer)
		go fraudWatch(peer)
		peer.SetScreenHandler(reverseFrame)
	}
	if p := takePendingSend(); p != "" {
		go func() {
			time.Sleep(2 * time.Second)
			netlogf("Yarım kalan dosya aktarımı sürdürülüyor: %s", p)
			sendFileToPeer(p)
		}()
	}
}

func b2SessionEnded() {
	stopReverseCapture()
	stopRecording()
	macroStopAll()
	fraudHold.Store(false)
	remoteRecording.Store(false)
	fraudAllowed = sync.Map{}
	if state.hwnd != 0 {
		postMessage.Call(state.hwnd, WM_APP_REVERSE, 0, 0)
	}
	pendingMu.Lock()
	pendingSend = ""
	pendingMu.Unlock()
}

// ---- 6: transfer resume plumbing ----

var (
	resumeMu    sync.Mutex
	resumeWait  = map[string]chan int64{}
	pendingMu   sync.Mutex
	pendingSend string // file whose transfer was cut by a dropped link
)

func awaitResume(id string) chan int64 {
	ch := make(chan int64, 1)
	resumeMu.Lock()
	resumeWait[id] = ch
	resumeMu.Unlock()
	return ch
}

func deliverResume(id string, off int64) {
	resumeMu.Lock()
	ch := resumeWait[id]
	delete(resumeWait, id)
	resumeMu.Unlock()
	if ch != nil {
		ch <- off
	}
}

func setPendingSend(p string) { pendingMu.Lock(); pendingSend = p; pendingMu.Unlock() }
func takePendingSend() string {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	p := pendingSend
	pendingSend = ""
	return p
}

// ---- 8: fraud shield (agent) ----

var (
	fraudHold    atomic.Bool
	fraudAllowed sync.Map // lower-cased window titles the user chose to continue on
	fraudTitle   string
	fraudWords   = []string{"internet şubesi", "internet bankacılığı", "mobil şube", "bankacılık", "ziraat", "garanti bbva", "akbank", "yapı kredi", "yapıkredi", "iş bankası", "işbank", "halkbank", "vakıfbank", "vakifbank", "qnb", "finansbank", "denizbank", "enpara", "papara", "kuveyt türk", "türkiye finans", "odeabank", "şekerbank", "albaraka", "fibabanka", "hsbc", "e-devlet", "turkiye.gov.tr", "paypal", "binance", "btcturk", "paribu", "kredi kartı", "havale", "eft işlemi", "online banking"}
)

func foregroundTitle() string {
	h, _, _ := getForegroundWn.Call()
	if h == 0 || h == state.hwnd {
		return ""
	}
	buf := make([]uint16, 512)
	n, _, _ := getWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func fraudWatch(peer *webrtcpeer.Peer) {
	defer logCrash("fraudWatch")
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		if peerDead(peer) || currentPeer() != peer || state.mode != 1 {
			return
		}
		if fraudHold.Load() {
			continue
		}
		title := strings.ToLower(foregroundTitle())
		if title == "" {
			continue
		}
		if _, ok := fraudAllowed.Load(title); ok {
			continue
		}
		for _, w := range fraudWords {
			if strings.Contains(title, w) {
				fraudHold.Store(true)
				fraudTitle = title
				blockInput.Call(0)
				_ = peer.SendControlText("FRAUD_HOLD:1")
				auditLog("Dolandırıcılık kalkanı: " + title)
				netlogf("Dolandırıcılık kalkanı tetiklendi: %s", title)
				if state.hwnd != 0 {
					postMessage.Call(state.hwnd, WM_APP_FRAUD_ASK, 0, 0)
				}
				break
			}
		}
	}
}

func fraudAsk() {
	text := "DİKKAT: Uzaktan bağlantı açıkken bir banka / ödeme sayfası açıldı.\r\n\r\n" +
		"Ekran paylaşımı ve uzaktan kontrol şu an DURDURULDU.\r\n\r\n" +
		"Sizi arayıp kendini banka, polis, savcı veya bir kurum yetkilisi olarak tanıtan biri bağlanmak istediyse bu bir DOLANDIRICILIK olabilir.\r\n\r\n" +
		"Bağlantı KESİLSİN mi?\r\n\r\n" +
		"Evet = bağlantıyı hemen kes (önerilen)\r\nHayır = karşı tarafı tanıyorum, devam et"
	r, _, _ := messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr(text))), uintptr(unsafe.Pointer(utf16ptr(brandName()+" — Güvenlik Uyarısı"))), 0x00000004|0x00000030|0x00040000) // YESNO|WARNING|TOPMOST; Yes (disconnect) is the default
	if r == 6 {
		auditLog("Dolandırıcılık kalkanı: kullanıcı bağlantıyı kesti")
		netlogf("Kalkan: kullanıcı bağlantıyı kesti")
		stopSession()
		home()
		setStatus("● Bağlantı güvenlik için kesildi.")
		return
	}
	fraudAllowed.Store(fraudTitle, struct{}{})
	fraudHold.Store(false)
	sendCtl("FRAUD_HOLD:0")
	auditLog("Dolandırıcılık kalkanı: kullanıcı devam etti")
	netlogf("Kalkan: kullanıcı devam etmeyi seçti")
}

// ---- 19: reverse screen ----

var (
	reverseOn     atomic.Bool // operator: sharing own screen
	reverseCancel context.CancelFunc
	reverseMu     sync.Mutex
	revHwnd       uintptr
	revPixMu      sync.Mutex
	revPix        []byte
	revW, revH    int
	revRegOnce    sync.Once
)

func toggleReverse() {
	p := currentPeer()
	if p == nil {
		return
	}
	if reverseOn.Load() {
		stopReverseCapture()
		_ = p.SendControlText("REVERSE:0")
		setStatus("● Ekranınızın paylaşımı durduruldu.")
		return
	}
	_ = p.SendControlText("REVERSE:1")
	ctx, cancel := context.WithCancel(context.Background())
	reverseMu.Lock()
	reverseCancel = cancel
	reverseMu.Unlock()
	reverseOn.Store(true)
	netlogf("Karşılıklı ekran başladı")
	setStatus("● Ekranınız karşı tarafa gösteriliyor (yalnızca izleme).")
	go func() {
		defer logCrash("reverse")
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		var seq uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			primary := 0
			if mons, err := capture.ListMonitors(); err == nil {
				for i, m := range mons {
					if m.Primary {
						primary = i
					}
				}
			}
			f, err := capture.CaptureMonitorScaled(primary, 65, 720)
			if err != nil || len(f.JPEG) > screen.MaxPayloadSize {
				continue
			}
			seq++
			if p.SendScreenFrame(screen.Frame{Monitor: reverseMonitorTag, Seq: seq, Width: uint32(f.Width), Height: uint32(f.Height), JPEG: f.JPEG}) != nil {
				return
			}
		}
	}()
}

func stopReverseCapture() {
	reverseMu.Lock()
	if reverseCancel != nil {
		reverseCancel()
		reverseCancel = nil
	}
	reverseMu.Unlock()
	reverseOn.Store(false)
}

// reverseFrame runs on the agent for frames the operator shares.
func reverseFrame(f screen.Frame) {
	if state.mode != 1 {
		return
	}
	img, err := jpeg.Decode(bytes.NewReader(f.JPEG))
	if err != nil {
		return
	}
	b := img.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, b.Min, draw.Src)
	px := rgba.Pix
	for i := 0; i+3 < len(px); i += 4 {
		px[i], px[i+2] = px[i+2], px[i] // RGBA -> BGRA for StretchDIBits
	}
	revPixMu.Lock()
	revPix, revW, revH = px, b.Dx(), b.Dy()
	revPixMu.Unlock()
	if revHwnd != 0 {
		invalidateRect.Call(revHwnd, 0, 0)
	}
}

func reverseProc(hwnd uintptr, m uint32, wParam, lParam uintptr) uintptr {
	switch m {
	case 0x0010: // WM_CLOSE: just hide; operator controls the stream
		hide(hwnd)
		return 0
	case 0x0014:
		return 1
	case 0x000F:
		var ps paintStruct
		hdc, _, _ := beginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if hdc == 0 {
			return 0
		}
		var rc rect
		getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		cw, ch := int(rc.Right), int(rc.Bottom)
		black, _, _ := getStockObject.Call(4)
		fillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), black)
		revPixMu.Lock()
		if len(revPix) > 0 {
			l, t, w, h := fitRect(revW, revH, cw, ch)
			bi := bitmapInfo{BmiHeader: bitmapInfoHeader{BiSize: uint32(unsafe.Sizeof(bitmapInfoHeader{})), BiWidth: int32(revW), BiHeight: -int32(revH), BiPlanes: 1, BiBitCount: 32}}
			setStretchMode.Call(hdc, 4) // HALFTONE
			stretchDIBits.Call(hdc, uintptr(l), uintptr(t), uintptr(w), uintptr(h), 0, 0, uintptr(revW), uintptr(revH), uintptr(unsafe.Pointer(&revPix[0])), uintptr(unsafe.Pointer(&bi)), 0, 0x00CC0020)
		} else {
			paintText(hdc, "Destek verenin ekranı bekleniyor…", 0, 0, cw, ch, uiBodyFont, 0xFFFFFF, txMid)
		}
		revPixMu.Unlock()
		endPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

func showReverseWindow(on bool) {
	if !on {
		hide(revHwnd)
		revPixMu.Lock()
		revPix = nil
		revPixMu.Unlock()
		return
	}
	cls := utf16ptr("NexDeskReverse")
	revRegOnce.Do(func() {
		wc := wndclassex{CbSize: uint32(unsafe.Sizeof(wndclassex{})), LpfnWndProc: syscall.NewCallback(reverseProc), HInstance: moduleHandle(), HCursor: func() uintptr { r, _, _ := loadCursor.Call(0, 32512); return r }(), LpszClassName: cls, HIcon: appIcon(32), HIconSm: appIcon(16)}
		registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	})
	if revHwnd == 0 {
		revHwnd, _, _ = createWindow.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16ptr("Destek Verenin Ekranı — yalnızca izleme"))), 0x00CF0000, 120, 80, 1000, 620, 0, 0, moduleHandle(), 0)
		setWinDisplayAff.Call(revHwnd, 0x11) // don't bounce it back into our own capture
	}
	showWindow.Call(revHwnd, 5)
	user32.NewProc("SetForegroundWindow").Call(revHwnd)
}

// ---- 27: session handover ----

var (
	handoverMu     sync.Mutex
	handoverOld    *webrtcpeer.Peer
	handoverOldSig *clientsignaling.Client
	handoverOldCan context.CancelFunc
	handoverNewSig *clientsignaling.Client
	handoverNewCtx context.Context
	handoverNewCan context.CancelFunc
)

func requestHandover() {
	r, _, _ := messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr("Bu oturum başka bir teknisyene devredilsin mi?\r\n\r\nKarşı taraf onaylarsa size bir devir kodu gelir; yeni teknisyen o kodla bağlanınca sizin oturumunuz kapanır."))), uintptr(unsafe.Pointer(utf16ptr("Oturum Devri"))), 0x00000004|0x00000020)
	if r == 6 && sendCtl("HANDOVER_REQ") {
		setStatus("● Devir isteği gönderildi, karşı tarafın onayı bekleniyor…")
	}
}

func agentHandover(peer *webrtcpeer.Peer) {
	defer logCrash("handover")
	r, _, _ := messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr("Destek veren kişi bu oturumu başka bir teknisyene devretmek istiyor.\r\n\r\nİzin veriyor musunuz? (Yeni teknisyen bağlandığında yine sizden onay istenecek.)"))), uintptr(unsafe.Pointer(utf16ptr(brandName()+" — Oturum Devri"))), 0x00000004|0x00000020|0x00040000)
	if r != 6 {
		_ = peer.SendControlText("HANDOVER_NO")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c, err := clientsignaling.Dial(ctx, signalingEndpoint())
	if err != nil {
		cancel()
		setStatus("● Devir için sunucuya bağlanılamadı: " + err.Error())
		_ = peer.SendControlText("HANDOVER_NO")
		return
	}
	code, _, err := c.Create(ctx)
	if err != nil {
		cancel()
		_ = c.Close(context.Background())
		_ = peer.SendControlText("HANDOVER_NO")
		return
	}
	state.mu.Lock()
	handoverMu.Lock()
	handoverOld, handoverOldSig, handoverOldCan = state.peer, state.sig, state.cancel
	handoverNewSig, handoverNewCtx, handoverNewCan = c, ctx, cancel
	handoverMu.Unlock()
	state.mu.Unlock()
	auditLog("Oturum devri başlatıldı")
	netlogf("Oturum devri: yeni kod %s", code)
	_ = peer.SendControlText("HANDOVER_CODE:" + code)
	setStatus("● Oturum devri bekleniyor — yeni teknisyen bağlanınca onayınız istenecek.")
	targetLoop(ctx, c)
}

// finishHandover retires the previous operator once the new one is connected.
func finishHandover(newPeer *webrtcpeer.Peer) {
	handoverMu.Lock()
	old, oldSig, oldCan := handoverOld, handoverOldSig, handoverOldCan
	nsig, nctx, ncan := handoverNewSig, handoverNewCtx, handoverNewCan
	if old == nil || old == newPeer {
		handoverMu.Unlock()
		return
	}
	handoverOld, handoverOldSig, handoverOldCan = nil, nil, nil
	handoverNewSig, handoverNewCtx, handoverNewCan = nil, nil, nil
	handoverMu.Unlock()
	state.mu.Lock()
	state.sig, state.ctx, state.cancel = nsig, nctx, ncan
	state.mu.Unlock()
	_ = old.SendControlText("HANDOVER_DONE")
	time.Sleep(300 * time.Millisecond)
	_ = old.Close()
	if oldSig != nil {
		_ = oldSig.Close(context.Background())
	}
	if oldCan != nil {
		oldCan()
	}
	auditLog("Oturum yeni teknisyene devredildi")
	netlogf("Oturum devri tamamlandı")
	go func() {
		time.Sleep(time.Second)
		setStatus("● Oturum yeni teknisyene devredildi.")
	}()
}

// ---- 34: drag & drop ----

func handleDrop(hdrop uintptr) {
	defer dragFinish.Call(hdrop)
	n, _, _ := dragQueryFile.Call(hdrop, 0xFFFFFFFF, 0, 0)
	var files []string
	for i := uintptr(0); i < n; i++ {
		buf := make([]uint16, 1024)
		l, _, _ := dragQueryFile.Call(hdrop, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if l > 0 {
			files = append(files, syscall.UTF16ToString(buf[:l]))
		}
	}
	if currentPeer() == nil {
		setStatus("● Dosya bırakmak için önce bir oturum kurun.")
		return
	}
	go func() {
		for _, f := range files {
			sendFileToPeer(f)
		}
	}()
}

func b2WndProc(hwnd uintptr, m uint32, wParam, lParam uintptr) (uintptr, bool) {
	switch m {
	case wmDropFiles:
		handleDrop(wParam)
		return 0, true
	case WM_APP_REVERSE:
		showReverseWindow(wParam != 0)
		return 0, true
	case WM_APP_HANDOVER:
		stopSession()
		home()
		setStatus("● Oturum yeni teknisyene devredildi; bağlantınız kapatıldı.")
		return 0, true
	case WM_APP_MANUAL:
		if wParam == 1 {
			manualOperatorShow()
		} else {
			manualAgentShow()
		}
		return 0, true
	case WM_APP_FRAUD_ASK:
		fraudAsk()
		return 0, true
	}
	return 0, false
}

// ---- menu ----

func appendB2Menu(menu uintptr, mi func(m, flags, id uintptr, label string)) {
	mi(menu, 0x0800, 0, "")
	mi(menu, 0x0000, ID_REVERSE, map[bool]string{false: "Ekranımı Karşı Tarafa Göster", true: "Ekranımı Göstermeyi Durdur"}[reverseOn.Load()])
	mi(menu, 0x0000, ID_RECORD, map[bool]string{false: "Oturumu Video Kaydet", true: "Video Kaydını Durdur"}[recording.Load()])
	msub, _, _ := createPopupMenu.Call()
	if msub != 0 {
		if macroRecording.Load() {
			mi(msub, 0x0000, ID_MACRO_REC, "Kaydı Durdur ve Kaydet…")
		} else {
			mi(msub, 0x0000, ID_MACRO_REC, "Kaydı Başlat")
		}
		if macroPlaying.Load() {
			mi(msub, 0x0000, ID_MACRO_STOP, "Oynatmayı Durdur")
		}
		names := macroList()
		if len(names) > 0 {
			mi(msub, 0x0800, 0, "")
			for i, n := range names {
				mi(msub, 0x0000, uintptr(ID_MACRO_BASE+i), "▶  "+n)
			}
		}
		mi(msub, 0x0800, 0, "")
		mi(msub, 0x0000, ID_MACRO_FOLDER, "Makro Klasörünü Aç")
		mi(menu, 0x0010, msub, "Makrolar")
	}
	mi(menu, 0x0000, ID_HANDOVER, "Oturumu Başka Teknisyene Devret…")
}

func onB2Command(id int) bool {
	if id >= ID_MACRO_BASE && id < ID_MACRO_BASE+50 {
		names := macroList()
		if i := id - ID_MACRO_BASE; i < len(names) {
			go macroPlay(names[i])
		}
		return true
	}
	switch id {
	case ID_REVERSE:
		toggleReverse()
	case ID_HANDOVER:
		requestHandover()
	case ID_RECORD:
		if recording.Load() {
			stopRecording()
		} else {
			startRecording()
		}
	case ID_MACRO_REC:
		if macroRecording.Load() {
			macroStopAndSave()
		} else {
			macroStart()
		}
	case ID_MACRO_STOP:
		macroStopAll()
	case ID_MACRO_FOLDER:
		shellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16ptr("open"))), uintptr(unsafe.Pointer(utf16ptr(macroDir()))), 0, 0, 1)
	default:
		return false
	}
	return true
}

// transferID identifies a file version so an interrupted transfer can resume.
func transferID(name string, info os.FileInfo) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", name, info.Size(), info.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:8])
}

// resumeReceive reopens a matching .part file (identified by its .part.id
// sidecar) and tells the sender where to continue. Returns true if resumed.
func resumeReceive(dir, id string, total int64) bool {
	if id == "" {
		return false
	}
	ids, _ := filepath.Glob(filepath.Join(dir, "*.part.id"))
	for _, sc := range ids {
		b, err := os.ReadFile(sc)
		if err != nil || strings.TrimSpace(string(b)) != id {
			continue
		}
		tmp := strings.TrimSuffix(sc, ".id")
		st, err := os.Stat(tmp)
		if err != nil || st.Size() > total {
			continue
		}
		f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			continue
		}
		fileRx.mu.Lock()
		if fileRx.file != nil && fileRx.file.Name() != tmp {
			_ = fileRx.file.Close()
		}
		name := filepath.Base(strings.TrimSuffix(tmp, ".part"))
		fileRx.file, fileRx.name, fileRx.total, fileRx.received, fileRx.tempPath = f, name, total, st.Size(), tmp
		fileRx.mu.Unlock()
		sendCtl(fmt.Sprintf("FILE_RESUME:%s:%d", id, st.Size()))
		setStatus(fmt.Sprintf("● Dosya kaldığı yerden alınıyor: %s", name))
		return true
	}
	return false
}

func startReceive(tmp, id string) {
	if id == "" {
		return
	}
	_ = os.WriteFile(tmp+".id", []byte(id), 0644)
	sendCtl("FILE_RESUME:" + id + ":0")
}

var secureDesk atomic.Bool

// secureDesktopState reports transitions to/from the secure desktop once.
func secureDesktopState(peer *webrtcpeer.Peer, on bool) {
	if secureDesk.Swap(on) == on {
		return
	}
	if on {
		setStatus("● Güvenli ekran açık (Ctrl+Alt+Del / UAC / kilit) — kapatınca paylaşım sürer.")
		_ = peer.SendControlText("SECURE_DESKTOP:1")
	} else {
		setStatus("● Ekran paylaşımı devam ediyor.")
		_ = peer.SendControlText("SECURE_DESKTOP:0")
	}
}
