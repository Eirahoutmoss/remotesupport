//go:build windows

package main

import (
	"crypto/rand"
	"strconv"
	"strings"
	"time"
	"unsafe"

	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
)

// Promote an active, already-approved session to permanent (unattended) access.
//
// Flow: the technician (operator, mode 2) clicks "Bu Cihazı Kaydet" and sends
// REGDEV_REQ over the DTLS-encrypted control channel. The target (mode 1) shows
// a one-time confirm dialog; on approval it turns on device access with a device
// id + password (reusing any it already has, otherwise generating a random
// password) and replies REGDEV_OK:<id>|<pass>. The operator saves that to its
// technician device list and can reconnect directly from then on. On refusal the
// target replies REGDEV_NO. No privileged/service code is involved — this is the
// normal consent-based device listener, armed with the target's explicit OK.

// requestSaveRemoteDevice is called on the operator side (ID_SAVE_REMOTE_DEVICE).
func requestSaveRemoteDevice() {
	if state.mode != 2 {
		return
	}
	if currentPeer() == nil {
		setStatus("● Önce bir cihaza bağlanın.")
		return
	}
	if !sendCtl("REGDEV_REQ:" + operatorName()) {
		setStatus("● İstek gönderilemedi — kontrol kanalı hazır değil.")
		return
	}
	setStatus("● Karşı taraftan kalıcı erişim izni isteniyor — onayı bekleyin...")
}

// handleRegDevRequest runs on the target side when a REGDEV_REQ arrives. It
// blocks on a confirm dialog, so callers launch it in its own goroutine.
func handleRegDevRequest(peer *webrtcpeer.Peer, name string) {
	defer logCrash("regdev")
	if peer == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Teknisyen"
	}
	prompt := "Teknisyen \"" + name + "\" bu bilgisayara KALICI ERİŞİM istiyor.\n\n" +
		"İzin verirseniz bir cihaz kimliği ve parola oluşturulacak; bu teknisyen bundan " +
		"sonra her seferinde onay sormadan bu bilgisayara bağlanabilecek.\n\n" +
		"İstediğiniz zaman Cihaz Erişimi sayfasından kapatabilirsiniz.\n\nİzin veriyor musunuz?"
	// MB_YESNO | MB_ICONWARNING | MB_SETFOREGROUND | MB_SYSTEMMODAL
	r, _, _ := messageBox.Call(0,
		uintptr(unsafe.Pointer(utf16ptr(prompt))),
		uintptr(unsafe.Pointer(utf16ptr("Kalıcı Erişim İzni"))),
		0x00000004|0x00000030|0x00010000|0x00001000)
	if r != 6 { // IDYES == 6
		_ = peer.SendControlText("REGDEV_NO")
		setStatus("● Kalıcı erişim isteği reddedildi.")
		auditLog("Kalıcı erişim isteği reddedildi: " + name)
		return
	}
	id, pass := armDeviceForTechnician()
	if id == "" || pass == "" {
		_ = peer.SendControlText("REGDEV_NO")
		setStatus("● Kalıcı erişim açılamadı.")
		return
	}
	_ = peer.SendControlText("REGDEV_OK:" + id + "|" + pass)
	setStatus("● Kalıcı erişim verildi: " + name)
	auditLog("Kalıcı erişim verildi: " + name + " → cihaz " + id)
	info := "Kalıcı erişim açıldı.\n\nCihaz kimliği: " + id + "\nParola: " + pass +
		"\n\nBu teknisyen bundan sonra onay sormadan bağlanabilir.\n" +
		"Kapatmak için: Cihaz Erişimi sayfası → kapalı → Kaydet."
	// MB_ICONINFORMATION | MB_SETFOREGROUND
	go messageBox.Call(0,
		uintptr(unsafe.Pointer(utf16ptr(info))),
		uintptr(unsafe.Pointer(utf16ptr("Kalıcı Erişim"))),
		0x00000040|0x00010000)
}

// armDeviceForTechnician turns on device access and returns the id + password a
// technician should use. If device access is already active with a valid
// password it is reused untouched; otherwise a password is generated (or an
// existing short/empty one replaced), settings are saved, and the listener is
// started. Returns empty strings if it could not be armed.
func armDeviceForTechnician() (id, pass string) {
	cfg := currentSettings()
	if deviceModeActive.Load() && cfg.DeviceAccessOn && len(strings.TrimSpace(cfg.DevicePass)) >= 4 {
		return effectiveDeviceID(), cfg.DevicePass
	}
	id = effectiveDeviceID()
	if id == "" {
		return "", ""
	}
	pass = strings.TrimSpace(cfg.DevicePass)
	if len(pass) < 4 {
		pass = randomDevicePass()
	}
	cfg.DeviceAccessOn = true
	cfg.DeviceID = id
	cfg.DevicePass = pass
	settingsMu.Lock()
	appCfg = cfg
	settingsMu.Unlock()
	saveSettings()
	if !deviceModeActive.Load() {
		startDeviceMode()
	}
	return id, pass
}

// randomDevicePass makes an 8-character password from an unambiguous alphabet
// (no 0/O/1/l/I). crypto/rand backed; a time-based fallback is used only if the
// RNG fails, which never happens in practice on Windows.
func randomDevicePass() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "nd" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
