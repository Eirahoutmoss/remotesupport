//go:build windows

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/eirahoutmoss/remotesupport/client/capture"
	clientsignaling "github.com/eirahoutmoss/remotesupport/client/signaling"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	signaling "github.com/eirahoutmoss/remotesupport/server/signaling"
	"github.com/pion/webrtc/v4"
)

func createControl(parent uintptr, class, text string, style uint32, x, y, w, h, id int) uintptr {
	if class == "BUTTON" {
		style |= 0x0000000B // BS_OWNERDRAW
	}
	r, _, _ := createWindow.Call(
		0,
		uintptr(unsafe.Pointer(utf16ptr(class))),
		uintptr(unsafe.Pointer(utf16ptr(text))),
		uintptr(style|0x40000000|0x10000000|0x00010000), // + WS_TABSTOP
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, uintptr(id), moduleHandle(), 0,
	)
	switch class {
	case "BUTTON":
		setControlFont(r, uiButtonFont)
		installBtnHover(r)
	case "EDIT":
		setControlFont(r, uiEditFont)
	case "STATIC":
		if text == appTitle {
			setControlFont(r, uiHeaderFont)
		} else if text == "Güvenli uzaktan destek" {
			setControlFont(r, uiSubtitleFont)
		} else if len(text) == 6 {
			setControlFont(r, uiCodeFont)
		} else {
			setControlFont(r, uiBodyFont)
		}
	}
	return r
}

func moduleHandle() uintptr {
	r, _, _ := getModule.Call(0)
	return r
}

// appIcon loads the embedded NexDesk icon (RT_GROUP_ICON id 1 from
// rsrc_windows_amd64.syso) at the given size; 0 if the resource is missing.
func appIcon(size int) uintptr {
	const imageIcon, lrShared = 1, 0x8000
	r, _, _ := loadImage.Call(moduleHandle(), 1, imageIcon, uintptr(size), uintptr(size), lrShared)
	return r
}

func hide(hwnd uintptr) {
	if hwnd != 0 {
		showWindow.Call(hwnd, 0) // SW_HIDE
	}
}

func show(hwnd uintptr) {
	if hwnd != 0 {
		showWindow.Call(hwnd, 5) // SW_SHOW
	}
}

func clearChildren() {
	clearResolutionButtons()
	hideBookControls()
	for _, h := range []uintptr{
		state.homeGet, state.homeGive, state.codeLabel, state.remoteEdit, state.action, state.copy, state.back, state.fileSend, state.fullscreenButton, state.settingsEndpoint, state.settingsQuality, state.settingsResolution, state.settingsSave, state.settingsReset, state.settingsIdle, state.settingsIdleAction, state.settingsInternet, state.remotePass, state.techDev, state.techDevManage, state.devOn, state.devId, state.devPass, state.devSave, state.settingsTurnURL, state.settingsTurnUser, state.settingsTurnPass, state.aboutTheme, state.aboutUpdate, state.elevate, state.clipSend, state.clipGet, state.chatInput, state.chatSend, state.stayOpen, state.actionsBtn,
	} {
		hide(h)
	}
}

func home() {
	clearChildren()
	state.mode = 0
	setStatus("Bir işlem seç.")
	setSessionTitle("") // back to the plain app title
	layoutUI()
}

func clearResolutionButtons() {
	state.mu.Lock()
	buttons := append([]uintptr(nil), state.resolutionButtons...)
	state.resolutionButtons = nil
	state.mu.Unlock()
	for _, h := range buttons {
		if h != 0 {
			destroyWindow.Call(h)
		}
	}
}

func supportMode() {
	ensureFirewall()
	startEmbeddedSignaling()
	clearChildren()
	state.mode = 1
	hide(state.homeGet)
	hide(state.homeGive)

	setText(state.action, "Kod oluşturuluyor...")
	show(state.codeLabel)
	show(state.action)
	show(state.copy)
	show(state.back)
	layoutUI()

	setStatus("Sunucuya bağlanılıyor...")
	go startTarget()
}

func giveMode() {
	clearChildren()
	state.mode = 2
	hide(state.homeGet)
	hide(state.homeGive)

	populateTechDevCombo()
	show(state.techDev)
	show(state.techDevManage)
	show(state.remoteEdit)
	show(state.remotePass)
	show(state.action)
	show(state.back)
	setText(state.action, "BAĞLAN")
	layoutUI()
	setStatus("Kodu girin — ya da kayıtlı bir cihaza bağlanmak için cihaz kimliği + parolasını girin.")
}

func startTarget() {
	userStopped.Store(false)
	everConnected.Store(false)
	reconnecting.Store(false)
	captureMaxHeight.Store(0)
	activeTargetMonitorMu.Lock()
	activeTargetMonitor = capture.Monitor{}
	activeTargetMonitorOK = false
	activeTargetMonitorMu.Unlock()
	activeMonitor.Store(0)
	remoteMonitorsMu.Lock()
	remoteMonitors = nil
	remoteMonitorsMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	state.cancel = cancel
	state.ctx = ctx
	state.mu.Unlock()

	setSessionTURN(nil)
	ep := signalingEndpoint()
	setTargetEndpoint(ep)
	c, err := clientsignaling.Dial(ctx, ep)
	if err != nil {
		netlogf("Signaling bağlantısı başarısız: sunucu=%s hata=%v", ep, err)
		setStatus("● Sunucuya bağlanılamadı (" + ep + "): " + err.Error())
		return
	}

	c.App = appVersion
	code, expires, err := c.Create(ctx)
	if err != nil {
		_ = c.Close(context.Background())
		netlogf("Kod oluşturulamadı: sunucu=%s hata=%v", ep, err)
		setStatus("● Kod oluşturulamadı: " + signalingErrorText(err, ep))
		return
	}
	netlogf("Kod oluşturuldu: sunucu=%s sunucu-sürümü=%d özellikler=%v", ep, c.ServerV, c.Features)
	versionNotice(c)

	state.mu.Lock()
	state.sig = c
	state.mu.Unlock()

	setCode(formatCode(code))
	setActiveCode(code)
	if isLocalEndpoint(ep) && currentSettings().InternetCode {
		// Kod bu makinedeki gömülü signaling'de ve kullanıcı internet kodunu
		// açmış: dış IP + port koda gömülür, modemde UPnP ile port açılır.
		go prepareInternetCode(code)
	} else if isLocalEndpoint(ep) {
		// Internet sunucusuna ulaşılamadı; kod yalnızca yerel ağda geçerli.
		// Modemde port açılmaz, 8091 güvenlik duvarında yerel alt ağa sınırlı.
		resetInternetCode()
		setInetNote("Yerel ağ kodu — yalnızca aynı ağdaki bilgisayarlar bağlanabilir (internet sunucusuna ulaşılamadı).")
	} else {
		// Kod internet signaling sunucusunda kayıtlı. Bu makinenin dış IP'sini
		// gösteren bir internet kodu, karşı tarafı kodun BULUNMADIĞI gömülü
		// sunucuya yönlendirir ("invalid_or_expired_code") — üretme.
		resetInternetCode()
		setInetNote("Kod sunucu üzerinden her ağdan geçerli — kodu, karekodu ya da linki iletmeniz yeterli.")
	}
	codeExpiryUnix.Store(time.Now().Unix() + int64(expires))
	auditLog("Kod oluşturuldu")
	setText(state.action, "Kod hazır")
	setStatus(fmt.Sprintf("● Hazır  •  Kod yaklaşık %d saniye geçerli", expires))

	go func() {
		defer logCrash("codeCountdown")
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			if capturing.Load() || state.mode != 1 {
				return
			}
			if state.hwnd != 0 {
				invalidateRect.Call(state.hwnd, 0, 0)
			}
			if time.Now().Unix() >= codeExpiryUnix.Load() {
				return
			}
		}
	}()
	go targetLoop(ctx, c)
}

func targetLoop(ctx context.Context, c *clientsignaling.Client) {
	defer logCrash("targetLoop")
	for {
		msg, err := c.Read(ctx)
		if err != nil {
			switch {
			case everConnected.Load():
				// Media flows peer-to-peer over WebRTC; the signaling socket is
				// only needed for setup and reconnect. A drop here is harmless,
				// and the peer-state watcher owns the real connection status, so
				// don't overwrite a live "BAĞLANDI" with a false "closed".
				netlogf("Sinyal kanalı kapandı (WebRTC aktif, bağlantı sürüyor): %v", err)
			case codeExpiryUnix.Load() > 0 && time.Now().Unix() >= codeExpiryUnix.Load():
				setStatus("● Kod süresi doldu — Geri dönüp yeni kod oluşturun.")
			default:
				setStatus("● Bağlantı kapandı.")
			}
			return
		}

		switch msg.Type {
		case "peer_joined":
			messageBeep.Call(0x00000040) // MB_ICONASTERISK
			if state.hwnd != 0 {
				flashWindow.Call(state.hwnd, 1)
			}
			auditLog("Bağlantı isteği alındı: " + describeRequester(msg.Name, msg.From))
			ok := askApproval(msg.Code, msg.Name, msg.From)
			if err := c.Approve(ctx, ok, ""); err != nil {
				setStatus("● Onay gönderilemedi: " + err.Error())
				return
			}
			if ok && c.HasFeature(signaling.FeatureTURNCred) {
				// v2 server: the per-session TURN credential arrives right
				// after our approval and before the operator's offer.
				rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
				im, ierr := c.Read(rctx)
				rcancel()
				if ierr != nil || im.Type != "ice" {
					netlogf("TURN kimliği beklenirken: tip=%q hata=%v", im.Type, ierr)
					setStatus("● Sunucudan TURN kimliği alınamadı.")
					return
				}
				setSessionTURN(im.ICE)
			}
			if !ok {
				auditLog("İstek reddedildi")
				setStatus("Bağlantı isteği reddedildi.")
				continue
			}

			peer, err := newPeer(ctx, c, true)
			if err != nil {
				setStatus("● WebRTC başlatılamadı: " + err.Error())
				return
			}
			state.mu.Lock()
			state.peer = peer
			state.mu.Unlock()

			peer.SetControlHandler(func(msg string) { defer logCrash("control"); handleRemoteInput(peer, msg) })
			peer.SetFileHandler(func(d []byte) { defer logCrash("file"); handleRemoteFile(d) })
			setStatus("● İstek onaylandı. Güvenli bağlantı kuruluyor...")
			if state.mode != 1 { // device mode: a session is starting, show it
				state.mu.Lock()
				state.mode = 1
				state.mu.Unlock()
			}
			startCapture(peer, ctx)
			go watchPeer(peer)
		case "signal":
			state.mu.Lock()
			p := state.peer
			state.mu.Unlock()
			sig := webrtcpeer.Signal{Kind: msg.Kind, Payload: msg.Payload}
			switch sig.Kind {
			case "offer":
				if p == nil || peerDead(p) {
					p = targetRebuild(ctx, c)
					if p == nil {
						continue
					}
				}
				answer, err := p.AcceptOffer(ctx, sig)
				if err != nil {
					setStatus("● Teklif işlenemedi: " + err.Error())
					return
				}
				if err := c.Signal(ctx, answer.Kind, answer.Payload); err != nil {
					setStatus("● Cevap gönderilemedi: " + err.Error())
					return
				}
			case "candidate":
				if p == nil {
					continue
				}
				if err := p.AddSignal(sig); err != nil {
					setStatus("● ICE adayı işlenemedi: " + err.Error())
				}
			}
		case "closed":
			setStatus("Bağlantı sonlandırıldı.")
			return
		}
	}
}

func startOperator(code string) {
	defer logCrash("startOperator")
	setActiveCode(code)
	setSessionTURN(nil)
	userStopped.Store(false)
	everConnected.Store(false)
	reconnecting.Store(false)
	activeTargetMonitorMu.Lock()
	activeTargetMonitor = capture.Monitor{}
	activeTargetMonitorOK = false
	activeTargetMonitorMu.Unlock()
	activeMonitor.Store(0)
	remoteMonitorsMu.Lock()
	remoteMonitors = nil
	remoteMonitorsMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	state.mu.Lock()
	state.cancel = cancel
	state.ctx = ctx
	state.mu.Unlock()

	ep := signalingEndpoint()
	ov := getOpEndpoint()
	if ov != "" {
		ep = ov
	}
	var c *clientsignaling.Client
	var err error
	if dp := getOpDevicePass(); dp != "" {
		// Connect to a registered device by id + password.
		c, err = dialDevice(ctx, ep, strings.TrimSpace(code), dp)
		if err != nil {
			netlogf("Cihaza bağlanma başarısız: sunucu=%s hata=%v", ep, err)
			// On the device path the password is verified before the online
			// check, so invalid_or_expired_code here means the device is
			// registered but not currently online — a truthful message helps.
			if strings.Contains(err.Error(), "invalid_or_expired_code") {
				setStatus("● Cihaz çevrimdışı — karşı bilgisayarda NexDesk açık ve Cihaz Erişimi etkin olmalı. Aynı bilgisayardan test ediyorsanız bu beklenendir.")
			} else {
				setStatus("● " + joinErrorText(err, ep))
			}
			return
		}
		netlogf("Cihaza bağlanıldı: %s (sunucu=%s)", code, ep)
		upsertTechDevice(strings.TrimSpace(code), dp)
	} else {
		c, err = dialJoin(ctx, ep, strings.TrimSpace(code))
		if err != nil && ov == "" && strings.Contains(err.Error(), "invalid_or_expired_code") {
			// Düz kod girildi ve kod bu sunucuda yok: karşı taraf internet
			// sunucusuna ulaşamayıp yerel ağ signaling'ine düşmüş olabilir.
			if alt := discoverSignalingEndpoint(1200 * time.Millisecond); alt != "" && alt != ep {
				netlogf("Kod %s üzerinde yok, yerel ağ sunucusu deneniyor: %s", ep, alt)
				if c2, err2 := dialJoin(ctx, alt, strings.TrimSpace(code)); err2 == nil {
					c, err, ep = c2, nil, alt
				}
			}
		}
		if err != nil {
			netlogf("Katılma başarısız: sunucu=%s hata=%v", ep, err)
			setStatus("● " + joinErrorText(err, ep))
			return
		}
		netlogf("Koda katılındı: sunucu=%s", ep)
	}

	state.mu.Lock()
	state.sig = c
	state.mu.Unlock()
	versionNotice(c)
	auditLog("Bağlantı isteği gönderildi")
	setStatus("● Bağlantı isteği gönderildi. Karşı tarafın onayı bekleniyor...")

	for {
		msg, err := c.Read(ctx)
		if err != nil {
			setStatus("● Bağlantı kurulamadı: " + err.Error())
			return
		}

		switch msg.Type {
		case "approved":
			if msg.ICE != nil {
				setSessionTURN(msg.ICE)
			}
			peer, err := newPeer(ctx, c, true)
			if err != nil {
				setStatus("● WebRTC başlatılamadı: " + err.Error())
				return
			}
			state.mu.Lock()
			state.peer = peer
			state.mu.Unlock()

			peer.SetScreenHandler(setViewerFrame)
			peer.SetControlHandler(func(msg string) { defer logCrash("control"); handleRemoteInput(peer, msg) })
			peer.SetFileHandler(func(d []byte) { defer logCrash("file"); handleRemoteFile(d) })

			if err := peer.CreateControl(); err != nil {
				setStatus("● Kontrol kanalı açılamadı: " + err.Error())
				return
			}
			if err := peer.CreateFile(); err != nil {
				setStatus("● Dosya kanalı açılamadı: " + err.Error())
				return
			}
			if err := peer.CreateScreen(); err != nil {
				setStatus("● Ekran kanalı açılamadı: " + err.Error())
				return
			}

			offer, err := peer.Offer(ctx)
			if err != nil {
				setStatus("● WebRTC teklifi oluşturulamadı: " + err.Error())
				return
			}
			if err := c.Signal(ctx, offer.Kind, offer.Payload); err != nil {
				setStatus("● WebRTC teklifi gönderilemedi: " + err.Error())
				return
			}

			setStatus("● Onaylandı. Güvenli WebRTC bağlantısı kuruluyor...")
			go watchPeer(peer)

		case "signal":
			state.mu.Lock()
			p := state.peer
			state.mu.Unlock()
			if p == nil {
				continue
			}
			sig := webrtcpeer.Signal{Kind: msg.Kind, Payload: msg.Payload}
			if err := p.AddSignal(sig); err != nil {
				setStatus("● WebRTC sinyali işlenemedi: " + err.Error())
			}
		case "rejected":
			setStatus("Karşı taraf bağlantı isteğini reddetti.")
			return
		case "closed":
			setStatus("Bağlantı sonlandırıldı.")
			return
		}
	}
}

func newPeer(ctx context.Context, c *clientsignaling.Client, approved bool) (*webrtcpeer.Peer, error) {
	_ = ctx
	ice := iceServers()
	var urls []string
	for _, s := range ice {
		urls = append(urls, s.URLs...)
	}
	// Direct + relay: virtual adapters are filtered out in peerSettings() so the
	// host-candidate set stays small; ICE takes a direct path on the LAN and
	// falls back to the TURN relay when direct isn't reachable.
	netlogf("WebRTC başlıyor, ICE sunucuları: %s", strings.Join(urls, ", "))
	p, err := webrtcpeer.NewWithSettings(approved, webrtc.Configuration{ICEServers: ice}, peerSettings(), func(sctx context.Context, sig webrtcpeer.Signal) error {
		return c.Signal(sctx, sig.Kind, sig.Payload)
	})
	if err == nil {
		p.SetDiagLogger(netlogf)
	}
	return p, err
}

func showConnectedView() {
	hide(state.action)
	hide(state.back)
	hide(state.remoteEdit)
	showViewer()
	show(state.fileSend)
	layoutUI()
	resizeViewer()
	setSessionTitle(getActiveCode()) // label the window for multi-session use
}

func watchPeer(peer *webrtcpeer.Peer) {
	defer logCrash("watchPeer")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	announced := false
	for range ticker.C {
		st := peer.ConnectionState()
		switch st {
		case webrtc.PeerConnectionStateConnected:
			if announced {
				continue
			}
			announced = true
			everConnected.Store(true)
			reconnecting.Store(false)
			// Keep the signaling socket alive only now that media is up: during
			// negotiation there is plenty of signaling traffic, and a ping then
			// only adds write contention. NAT/proxy idle timeouts matter only
			// for the long quiet stretch after connect (auto-reconnect needs it).
			state.mu.Lock()
			kctx, ksig := state.ctx, state.sig
			state.mu.Unlock()
			if ksig != nil && kctx != nil {
				ksig.KeepAlive(kctx, signalingKeepAlive)
			}
			if host, err := os.Hostname(); err == nil {
				go sendCtlRetry(peer, "HELLO:"+host)
			}
			if state.mode == 1 {
				sendRemoteMonitorList(peer)
			}
			showConnectedView()
			setConnInfo(peer.PathKind(), "")
			if v, ok := peer.FingerprintSAS(getActiveCode()); ok {
				setConnInfo("", v)
			}
			startSessionStats(peer)
			onB1Connected(peer)
			recRole := "Destek verildi"
			if state.mode == 1 {
				recRole = "Destek alındı"
			}
			addRecent(recRole, getActiveCode())
			auditLog("Bağlandı: " + recRole)
			setStatus("● BAĞLANDI  •  Güvenli WebRTC bağlantısı aktif")
			_ = peer.SendPing()
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			if announced {
				onPeerDropped(state.mode)
			} else {
				setStatus("● Güvenli bağlantı kurulamadı (ağ engeli olabilir). Sorun sürerse Ayarlar'dan TURN sunucusu tanımlayın; ayrıntı: İşlemler ▾ → Bağlantı Günlüğü.")
				netlogf("WebRTC bağlantısı kurulamadan kapandı: %s", st.String())
			}
			return
		}
	}
}

func askApproval(code, name, from string) bool {
	title := utf16ptr(brandName())
	who := signaling.CleanName(name)
	if who == "" {
		who = "bilinmiyor (karşı tarafın programı eski sürüm)"
	}
	where := signaling.CleanName(from)
	if where == "" {
		where = "bilinmiyor"
	}
	text := utf16ptr(fmt.Sprintf(
		"Bir bilgisayar size destek vermek için bağlanmak istiyor.\r\n\r\n"+
			"Kendini tanıttığı ad:\t%s\r\n"+
			"Bağlandığı adres:\t%s\r\n"+
			"Destek kodu:\t%s\r\n\r\n"+
			"Bu kişiyi tanımıyorsanız ya da destek istemediyseniz HAYIR'a basın. "+
			"Ad, karşı tarafın kendi beyanıdır; şüphede kalırsanız onu telefonla arayıp doğrulayın.\r\n\r\n"+
			"Bağlantıya izin verilsin mi?",
		who, where, formatCode(code),
	))
	r, _, _ := messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x00000004|0x00000020)
	return r == 6 // IDYES
}

func formatCode(code string) string {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	switch len(code) {
	case 6:
		return code[:3] + " " + code[3:]
	case 10:
		return code[:3] + " " + code[3:6] + " " + code[6:]
	default:
		return code
	}
}

func copyCode() {
	if ic, _ := internetCodeState(); ic != "" {
		if copyToClipboard(ic) {
			setStatus("İnternet kodu panoya kopyalandı: " + ic + "  — WhatsApp vb. ile gönderin.")
		}
		return
	}
	code := strings.ReplaceAll(getText(state.codeLabel), " ", "")
	if code == "" {
		return
	}
	if copyToClipboard(code) {
		setStatus("Kod panoya kopyalandı: " + formatCode(code))
	} else {
		setStatus("Kod: " + formatCode(code) + "  • Karşı bilgisayarda bu kodu girin.")
	}
}

func copyToClipboard(s string) bool {
	// Another process (clipboard history, a clipboard manager) may hold the
	// clipboard for a moment; retry instead of failing silently.
	opened := false
	for i := 0; i < 20 && !opened; i++ {
		if r, _, _ := openClipboard.Call(state.hwnd); r != 0 {
			opened = true
		} else {
			time.Sleep(25 * time.Millisecond)
		}
	}
	if !opened {
		setStatus("● Pano şu an başka bir uygulama tarafından kullanılıyor, tekrar deneyin.")
		return false
	}
	defer closeClipboard.Call()
	emptyClipboard.Call()

	data := syscall.StringToUTF16(s)
	size := uintptr(len(data) * 2)
	h, _, _ := globalAlloc.Call(0x0002, size) // GMEM_MOVEABLE
	if h == 0 {
		return false
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		return false
	}
	copyBytes(unsafe.Pointer(p), unsafe.Pointer(&data[0]), int(size))
	globalUnlock.Call(h)
	if r, _, _ := setClipboardData.Call(13, h); r == 0 { // CF_UNICODETEXT
		return false
	}
	return true
}

func copyBytes(dst, src unsafe.Pointer, n int) {
	d := unsafe.Slice((*byte)(dst), n)
	s := unsafe.Slice((*byte)(src), n)
	copy(d, s)
}

func readClipboardText() (string, bool) {
	if state.hwnd == 0 {
		return "", false
	}
	if r, _, _ := openClipboard.Call(state.hwnd); r == 0 {
		return "", false
	}
	defer closeClipboard.Call()
	h, _, _ := user32.NewProc("GetClipboardData").Call(13) // CF_UNICODETEXT
	if h == 0 {
		return "", false
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		return "", false
	}
	defer globalUnlock.Call(h)
	// CF_UNICODETEXT is UTF-16 and NUL terminated. Bound the read so a
	// malformed clipboard cannot make us scan unbounded memory.
	const maxClipboardRunes = 32768
	buf := unsafe.Slice((*uint16)(unsafe.Pointer(p)), maxClipboardRunes)
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(buf[:n]), true
}

func sendClipboardToPeer(peer *webrtcpeer.Peer) bool {
	text, ok := readClipboardText()
	if !ok {
		return false
	}
	data := []byte(text)
	if len(data) > 48*1024 {
		setStatus("● Pano metni 48 KB sınırını aşıyor.")
		return false
	}
	payload := base64.StdEncoding.EncodeToString(data)
	return peer.SendControlText("CLIP:"+payload) == nil
}

func requestRemoteClipboard(peer *webrtcpeer.Peer) bool {
	clipRequestedAt.Store(time.Now().UnixNano())
	return peer.SendControlText("CLIP_REQ") == nil
}

func normalizeCode(s string) string {
	s = strings.NewReplacer(" ", "", "-", "", ".", "", "\u00a0", "").Replace(strings.TrimSpace(s))
	if len(s) < 6 || len(s) > 14 {
		return ""
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return s
}

func clearMonitorButtons() {
	state.mu.Lock()
	buttons := append([]uintptr(nil), state.monitorButtons...)
	state.monitorButtons = nil
	state.mu.Unlock()
	for _, h := range buttons {
		if h != 0 {
			destroyWindow.Call(h)
		}
	}
}

func updateMonitorButtons(count int) {
	if count < 0 {
		count = 0
	}
	clearMonitorButtons()
	state.mu.Lock()
	oldRes := append([]uintptr(nil), state.resolutionButtons...)
	state.resolutionButtons = nil
	state.mu.Unlock()
	for _, h := range oldRes {
		if h != 0 {
			destroyWindow.Call(h)
		}
	}

	buttons := make([]uintptr, 0, count)
	const buttonW = 112
	const buttonH = 30
	const gap = 8
	for i := 0; i < count; i++ {
		x := 178 + i*(buttonW+gap)
		h := createControl(state.hwnd, "BUTTON", fmt.Sprintf("Monitör %d", i+1), 0x00000001, x, 102, buttonW, buttonH, ID_MONITOR_BASE+i)
		buttons = append(buttons, h)
	}
	state.mu.Lock()
	state.monitorButtons = buttons
	state.mu.Unlock()

	res := []struct {
		name string
		id   int
	}{
		{"Otomatik", ID_RES_ORIGINAL}, {"1080p", ID_RES_1080P}, {"720p", ID_RES_720P}, {"480p", ID_RES_480P},
	}
	resButtons := make([]uintptr, 0, len(res))
	startX := 178
	if count > 0 {
		startX += count * (buttonW + gap)
	}
	for i, r := range res {
		x := startX + i*82
		resButtons = append(resButtons, createControl(state.hwnd, "BUTTON", r.name, 0x00000001, x, 102, 76, 30, r.id))
	}
	state.mu.Lock()
	state.resolutionButtons = resButtons
	state.mu.Unlock()
	showResolutionControls(!fullscreen.Load())
	resizeViewer()
}

func selectMonitor(index int) {
	remoteMonitorsMu.RLock()
	count := len(remoteMonitors)
	remoteMonitorsMu.RUnlock()
	if index < 0 || index >= count {
		return
	}
	activeMonitor.Store(int32(index))
	state.mu.Lock()
	peer := state.peer
	state.mu.Unlock()
	if peer != nil {
		_ = peer.SendControlText(fmt.Sprintf("MONITOR:%d", index))
	}
	viewer.mu.Lock()
	viewer.pixels = nil
	viewer.width = 0
	viewer.height = 0
	viewer.fit = true
	viewer.zoom = 1
	viewer.panX = 0
	viewer.panY = 0
	viewer.clearNeeded = true
	viewer.mu.Unlock()
	if viewer.hwnd != 0 {
		invalidateRect.Call(viewer.hwnd, 0, 0)
	}
	setStatus(fmt.Sprintf("● Monitör %d/%d isteniyor...", index+1, count))
}

// dialJoin connects to a signaling server and joins the session code.
func dialJoin(ctx context.Context, ep, code string) (*clientsignaling.Client, error) {
	c, err := clientsignaling.Dial(ctx, ep)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	c.App, c.Name = appVersion, operatorName()
	if err := c.Join(ctx, code); err != nil {
		_ = c.Close(context.Background())
		return nil, err
	}
	return c, nil
}

// joinErrorText turns signaling join errors into a readable Turkish status.
func joinErrorText(err error, ep string) string {
	e := err.Error()
	switch {
	case strings.Contains(e, "pin ile eşleşmiyor"):
		return "Sunucu kimliği doğrulanamadı (" + ep + "): sertifika beklenen pin ile eşleşmiyor. Araya giren biri olabilir ya da sunucu sertifikası değişti — bağlanmayın, yöneticinize haber verin."
	case strings.HasPrefix(e, "dial:"):
		return "Sunucuya bağlanılamadı (" + ep + "): " + strings.TrimSpace(strings.TrimPrefix(e, "dial:"))
	case strings.Contains(e, "client_outdated"):
		return "Programınız bu sunucu için eski. NexDesk'in güncel sürümünü kurun."
	case strings.Contains(e, "invalid_or_expired_code"):
		return "Kod bu sunucuda bulunamadı ya da süresi doldu (" + ep + "). Kod 5 dk geçerli ve tek kullanımlıktır; karşı tarafta yeni kod oluşturup tekrar deneyin."
	case strings.Contains(e, "rate_limited"):
		return "Çok fazla hatalı deneme — 1 dakika bekleyip tekrar deneyin."
	case strings.Contains(e, "invalid_code"):
		return "Kod biçimi geçersiz."
	case strings.Contains(e, "device_auth_failed"):
		return "Cihaz kimliği ya da parolası hatalı."
	case strings.Contains(e, "unknown_device"):
		return "Böyle bir kayıtlı cihaz yok (çevrimdışı olabilir)."
	default:
		return "Kod kabul edilmedi (" + ep + "): " + e
	}
}

// signalingErrorText is joinErrorText for the target's create path.
func signalingErrorText(err error, ep string) string {
	e := err.Error()
	switch {
	case strings.Contains(e, "pin ile eşleşmiyor"), strings.Contains(e, "client_outdated"):
		return joinErrorText(err, ep)
	case strings.Contains(e, "rate_limited"):
		return "Kısa sürede çok fazla kod istendi — 1 dakika bekleyip tekrar deneyin."
	case strings.Contains(e, "server_capacity"):
		return "Sunucu şu an dolu — biraz sonra tekrar deneyin."
	default:
		return "(" + ep + ") " + e
	}
}

// operatorName is what the target sees in the approval dialog: computer name
// and Windows user, e.g. "BIM-PC12 (hasan)".
func operatorName() string {
	host, _ := os.Hostname()
	user := strings.TrimSpace(os.Getenv("USERNAME"))
	switch {
	case host != "" && user != "":
		return host + " (" + user + ")"
	case host != "":
		return host
	default:
		return user
	}
}

func describeRequester(name, from string) string {
	n := signaling.CleanName(name)
	if n == "" {
		n = "?"
	}
	return n + " @ " + signaling.CleanName(from)
}

// signalingKeepAlive is the WebSocket ping interval; well under the 60–300 s
// idle timeouts typical of NAT gateways and proxies.
const signalingKeepAlive = 25 * time.Second

// ---- device access (technician → registered device) ----

var (
	opDevicePassMu sync.Mutex
	opDevicePass   string
)

func setOpDevicePass(v string) { opDevicePassMu.Lock(); opDevicePass = v; opDevicePassMu.Unlock() }
func getOpDevicePass() string {
	opDevicePassMu.Lock()
	defer opDevicePassMu.Unlock()
	return opDevicePass
}

func dialDevice(ctx context.Context, ep, id, pass string) (*clientsignaling.Client, error) {
	c, err := clientsignaling.Dial(ctx, ep)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	c.App, c.Name = appVersion, operatorName()
	if err := c.JoinDevice(ctx, id, pass); err != nil {
		_ = c.Close(context.Background())
		return nil, err
	}
	return c, nil
}
