//go:build windows

package main

import "strings"

// showDeviceAccess opens the "Cihaz Erişimi" page (mode 12).
func showDeviceAccess() {
	clearChildren()
	state.mu.Lock()
	state.mode = 13
	state.mu.Unlock()
	populateDeviceControls()
	layoutUI()
	invalidateRect.Call(state.hwnd, 0, 1)
}

func populateDeviceControls() {
	cfg := currentSettings()
	if state.devOn != 0 {
		idx := 0
		if cfg.DeviceAccessOn {
			idx = 1
		}
		sendMessage.Call(state.devOn, 0x014E, uintptr(idx), 0) // CB_SETCURSEL
	}
	if state.devId != 0 {
		id := strings.TrimSpace(cfg.DeviceID)
		if id == "" {
			id = effectiveDeviceID()
		}
		setText(state.devId, id)
	}
	if state.devPass != 0 {
		setText(state.devPass, cfg.DevicePass)
	}
}

func saveDeviceAccess() {
	on := false
	if state.devOn != 0 {
		r, _, _ := sendMessage.Call(state.devOn, 0x0147, 0, 0) // CB_GETCURSEL
		on = r == 1
	}
	id := strings.TrimSpace(getText(state.devId))
	pass := getText(state.devPass)
	if on {
		if id == "" {
			setStatus("Cihaz kimliği boş olamaz.")
			return
		}
		if len(pass) < 4 {
			setStatus("Parola en az 4 karakter olmalı.")
			return
		}
	}
	cfg := currentSettings()
	cfg.DeviceAccessOn = on
	cfg.DeviceID = id
	cfg.DevicePass = pass
	settingsMu.Lock()
	appCfg = cfg
	settingsMu.Unlock()
	saveSettings()
	auditLog("Cihaz erişimi ayarı kaydedildi: " + map[bool]string{true: "açık", false: "kapalı"}[on])

	switch {
	case on && !deviceModeActive.Load():
		startDeviceMode()
		setStatus("● Cihaz erişimi etkinleştirildi — teknisyen bağlantısı bekleniyor.")
	case on:
		setStatus("● Kaydedildi. Kimlik/parola değişikliğinin tam uygulanması için programı yeniden başlatın.")
	default:
		stopDeviceMode()
		setStatus("● Cihaz erişimi kapatıldı.")
	}
	invalidateRect.Call(state.hwnd, 0, 1)
}
