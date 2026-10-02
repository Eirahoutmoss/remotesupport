//go:build windows

package main

// Serverless handshake: the operator sends an "invite" (its WebRTC offer with
// all ICE candidates, compressed) over WhatsApp; the agent pastes it, approves,
// and sends back a "reply" (the answer). No signaling server is involved.

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/pion/webrtc/v4"
)

const (
	ID_MANUAL_OP    = 5230
	ID_MANUAL_AGENT = 5231
	WM_APP_MANUAL   = 0x8030
	manualOfferTag  = "NXD1-DAVET:"
	manualAnswerTag = "NXD1-YANIT:"
)

var (
	manualOpBtn, manualAgentBtn uintptr
	manualMu                    sync.Mutex
	manualBlob                  string // blob waiting to be shown on the GUI thread
	manualOffer                 []byte // operator's own offer, for diagnostics
	manualPeer                  *webrtcpeer.Peer
)

func packBlob(tag string, payload []byte) string {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	w.Write(payload)
	w.Close()
	return tag + base64.RawURLEncoding.EncodeToString(buf.Bytes())
}

func unpackBlob(tag, s string) ([]byte, bool) {
	s = strings.Join(strings.Fields(s), "") // WhatsApp may wrap long lines
	i := strings.Index(s, tag)
	if i < 0 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s[i+len(tag):], ".,;"))
	if err != nil {
		return nil, false
	}
	b, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(raw)), 64<<10))
	if err != nil || len(b) == 0 {
		return nil, false
	}
	return b, true
}

// newManualPeer has no trickle signaling: candidates travel inside the SDP.
func newManualPeer() (*webrtcpeer.Peer, error) {
	return webrtcpeer.NewWithConfig(true, webrtc.Configuration{ICEServers: iceServers()}, nil)
}

func sessionCtx() context.Context {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.ctx == nil {
		state.ctx, state.cancel = context.WithCancel(context.Background())
	}
	return state.ctx
}

// ---- operator ----

func manualOperatorStart() {
	defer logCrash("manualOp")
	userStopped.Store(false)
	everConnected.Store(false)
	setActiveCode("")
	setStatus("● Davet kodu hazırlanıyor (ağ adresleri toplanıyor)…")
	seed := newSeed()
	peer, err := newManualPeerSeed(seed)
	if err != nil {
		setStatus("● Bağlantı hazırlanamadı: " + err.Error())
		return
	}
	peer.SetScreenHandler(setViewerFrame)
	peer.SetControlHandler(func(msg string) { defer logCrash("control"); handleRemoteInput(peer, msg) })
	peer.SetFileHandler(func(d []byte) { defer logCrash("file"); handleRemoteFile(d) })
	if peer.CreateControl() != nil || peer.CreateFile() != nil || peer.CreateScreen() != nil {
		setStatus("● Veri kanalları açılamadı.")
		return
	}
	offer, err := peer.Offer(sessionCtx())
	if err != nil {
		setStatus("● Davet oluşturulamadı: " + err.Error())
		return
	}
	state.mu.Lock()
	state.peer = peer
	state.mu.Unlock()
	manualMu.Lock()
	manualPeer = peer
	manualOffer = offer.Payload
	if c, err := compactPack(false, seed, offer.Payload); err == nil {
		manualBlob = c
	} else {
		manualBlob = packBlob(manualOfferTag, offer.Payload)
	}
	manualMu.Unlock()
	netlogf("Sunucusuz davet oluşturuldu (%d karakter)", len(manualBlob))
	postMessage.Call(state.hwnd, WM_APP_MANUAL, 1, 0)
}

// manualOperatorShow runs on the GUI thread: hand out the invite, take the reply.
func manualOperatorShow() {
	manualMu.Lock()
	blob, peer := manualBlob, manualPeer
	manualMu.Unlock()
	if blob == "" || peer == nil {
		return
	}
	var lastAnswer []byte
	copyToClipboard(blob)
	showCodeDialog("Sunucusuz Bağlantı — 1/2", "DAVET kodu (panoya da kopyalandı). Karşı tarafa gönderin; o 'Destek Al' > 'Davet Kodunu Yapıştır' ile girip size bir YANIT kodu gönderecek.", blob)
	for {
		txt, ok := promptText("Sunucusuz Bağlantı — 2/2", "Karşı tarafın gönderdiği YANIT kodunu yapıştırın:", "")
		if !ok {
			setStatus("● Sunucusuz bağlantı iptal edildi.")
			_ = peer.Close()
			return
		}
		payload, valid := compactUnpack(true, txt)
		if !valid {
			payload, valid = unpackBlob(manualAnswerTag, txt)
		}
		if !valid {
			if strings.Contains(txt, manualOfferTag) || strings.Contains(txt, compactOfferTag) {
				showInfo("Bu sizin kendi davet kodunuz. Karşı tarafın gönderdiği YANIT kodunu yapıştırın.")
			} else {
				showInfo("Yanıt kodu okunamadı. Mesajın tamamını kopyaladığınızdan emin olun.")
			}
			continue
		}
		lastAnswer = payload
		if err := peer.AddSignal(webrtcpeer.Signal{Kind: "answer", Payload: payload}); err != nil {
			showInfo("Yanıt uygulanamadı: " + err.Error())
			_ = peer.Close()
			return
		}
		break
	}
	setStatus("● Yanıt alındı — doğrudan bağlantı kuruluyor…")
	go watchPeer(peer)
	manualMu.Lock()
	own := manualOffer
	manualMu.Unlock()
	go manualDiag(peer, own, lastAnswer, true)
}

// ---- agent ----

func manualAgentPaste() {
	txt, ok := promptText("Davet Kodunu Yapıştır", "Destek verenin gönderdiği DAVET kodunu yapıştırın:", "")
	if !ok {
		return
	}
	payload, valid := compactUnpack(false, txt)
	if !valid {
		payload, valid = unpackBlob(manualOfferTag, txt)
	}
	if !valid {
		showInfo("Davet kodu okunamadı. Mesajın tamamını kopyaladığınızdan emin olun.")
		return
	}
	if !askYesNo(brandName(), "Bir kişi bu davetle bilgisayarınıza bağlanmak istiyor.\r\n\r\nYalnızca TANIDIĞINIZ ve sizin çağırdığınız birine izin verin.\r\n\r\nBağlantıya izin verilsin mi?") {
		auditLog("Sunucusuz davet reddedildi")
		return
	}
	auditLog("Sunucusuz davet onaylandı")
	go manualAgentAnswer(payload)
}

func manualAgentAnswer(offer []byte) {
	defer logCrash("manualAgent")
	userStopped.Store(false)
	everConnected.Store(false)
	setStatus("● Yanıt kodu hazırlanıyor…")
	seed := newSeed()
	peer, err := newManualPeerSeed(seed)
	if err != nil {
		setStatus("● Bağlantı hazırlanamadı: " + err.Error())
		return
	}
	peer.SetControlHandler(func(msg string) { defer logCrash("control"); handleRemoteInput(peer, msg) })
	peer.SetFileHandler(func(d []byte) { defer logCrash("file"); handleRemoteFile(d) })
	ctx := sessionCtx()
	answer, err := peer.AcceptOffer(ctx, webrtcpeer.Signal{Kind: "offer", Payload: offer})
	if err != nil {
		setStatus("● Davet işlenemedi: " + err.Error())
		_ = peer.Close()
		return
	}
	state.mu.Lock()
	state.peer = peer
	state.mu.Unlock()
	startCapture(peer, ctx)
	go watchPeer(peer)
	go manualDiag(peer, answer.Payload, offer, false)
	manualMu.Lock()
	if c, err := compactPack(true, seed, answer.Payload); err == nil {
		manualBlob = c
	} else {
		manualBlob = packBlob(manualAnswerTag, answer.Payload)
	}
	manualMu.Unlock()
	postMessage.Call(state.hwnd, WM_APP_MANUAL, 2, 0)
}

func manualAgentShow() {
	manualMu.Lock()
	blob := manualBlob
	manualMu.Unlock()
	if blob == "" {
		return
	}
	copyToClipboard(blob)
	setStatus("● Yanıt kodu panoya kopyalandı — destek verene gönderin.")
	showCodeDialog("Yanıt Kodu", "YANIT kodu (panoya da kopyalandı). Destek veren kişiye gönderin; o yapıştırınca bağlantı kurulur.", blob)
}

// ---- helpers / wiring ----

func askYesNo(title, text string) bool {
	r, _, _ := messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr(text))), uintptr(unsafe.Pointer(utf16ptr(title))), 0x00000004|0x00000020)
	return r == 6
}

func showInfo(text string) {
	messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr(text))), uintptr(unsafe.Pointer(utf16ptr(brandName()))), 0x00000040)
}

func openWhatsApp(msg string) {
	link := "https://wa.me/?text=" + url.QueryEscape(msg)
	shellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16ptr("open"))), uintptr(unsafe.Pointer(utf16ptr(link))), 0, 0, 1)
}

func layoutManualButtons(mode int, connected bool) {
	if manualOpBtn == 0 && (mode == 1 || mode == 2) {
		manualOpBtn = createControl(state.hwnd, "BUTTON", "Sunucusuz Bağlan (davet kodu)", 0x00000001, 0, 0, 10, 10, ID_MANUAL_OP)
		manualAgentBtn = createControl(state.hwnd, "BUTTON", "Davet Kodunu Yapıştır", 0x00000001, 0, 0, 10, 10, ID_MANUAL_AGENT)
	}
	moveControl(manualOpBtn, contentX+156, 470, 290, 46, mode == 2 && !connected)
	moveControl(manualAgentBtn, contentX+598, 574, 210, 46, mode == 1 && !connected && !capturing.Load())
}

func onManualCommand(id int) bool {
	switch id {
	case ID_MANUAL_OP:
		go manualOperatorStart()
	case ID_MANUAL_AGENT:
		manualAgentPaste()
	default:
		return false
	}
	return true
}

// showCodeDialog shows a long code in a selectable, read-only box with a
// Kopyala button (modal, GUI thread). Reuses the prompt window class/proc.
func showCodeDialog(title, label, code string) {
	cls := utf16ptr("NexDeskPrompt")
	promptReg.Do(func() {
		wc := wndclassex{CbSize: uint32(unsafe.Sizeof(wndclassex{})), LpfnWndProc: syscall.NewCallback(promptProc), HInstance: moduleHandle(), HbrBackground: 16, LpszClassName: cls, HIcon: appIcon(32), HIconSm: appIcon(16)}
		registerClassEx.Call(uintptr(unsafe.Pointer(&wc)))
	})
	var wr rect
	getWindowRect.Call(state.hwnd, uintptr(unsafe.Pointer(&wr)))
	const pw, ph = 620, 360
	x := int(wr.Left) + (int(wr.Right-wr.Left)-pw)/2
	y := int(wr.Top) + (int(wr.Bottom-wr.Top)-ph)/2
	promptHwnd, _, _ = createWindow.Call(0x00000001, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16ptr(title))),
		0x80000000|0x00C00000|0x00080000, uintptr(x), uintptr(y), pw, ph, state.hwnd, 0, moduleHandle(), 0)
	if promptHwnd == 0 {
		return
	}
	child := func(class, text string, style uint32, x, y, w, h, id int) uintptr {
		r, _, _ := createWindow.Call(0, uintptr(unsafe.Pointer(utf16ptr(class))), uintptr(unsafe.Pointer(utf16ptr(text))),
			uintptr(0x40000000|0x10000000|style), uintptr(x), uintptr(y), uintptr(w), uintptr(h), promptHwnd, uintptr(id), moduleHandle(), 0)
		setControlFont(r, uiBodyFont)
		return r
	}
	child("STATIC", label, 0, 16, 12, pw-48, 44, 0)
	// WS_BORDER|WS_VSCROLL|ES_MULTILINE|ES_AUTOVSCROLL|ES_READONLY
	promptEdit = child("EDIT", code, 0x00800000|0x00200000|0x0004|0x0040|0x0800, 16, 60, pw-48, 196, 100)
	child("BUTTON", "Kopyala", 0x00010000, pw-252, 270, 110, 36, 3)
	child("BUTTON", "Tamam", 0x00010000|0x00000001, pw-134, 270, 100, 36, 1)
	showWindow.Call(promptHwnd, 5)
	sendMessage.Call(promptEdit, 0x00B1, 0, ^uintptr(0)) // select all so Ctrl+C works too
	setFocus.Call(promptEdit)
	promptDone = false
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
	user32.NewProc("SetForegroundWindow").Call(state.hwnd)
}
