//go:build windows

package main

import (
	"os"
	"os/exec"
	"strings"
	"unsafe"
)

// launchNewSession opens another operator window as a separate process of the
// same exe. REMOTESUPPORT_NO_DEVICE keeps that instance from announcing this
// machine's device id, so two instances never evict each other on the server —
// it is purely a technician window. True in-window tabs are a v2 refactor; this
// gives the same workflow (one exe, many labelled connections) at near-zero risk.
func launchNewSession() {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		netlogf("Yeni oturum: exe yolu bulunamadı: %v", err)
		return
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "REMOTESUPPORT_NO_DEVICE=1")
	if err := cmd.Start(); err != nil {
		netlogf("Yeni oturum penceresi açılamadı: %v", err)
		setStatus("● Yeni oturum penceresi açılamadı.")
		return
	}
	_ = cmd.Process.Release()
	setStatus("● Yeni oturum penceresi açıldı (teknisyen modu — bu makineyi cihaz olarak duyurmaz).")
}

// setSessionTitle updates the title bar so multiple session windows are
// distinguishable in the taskbar. Empty code restores the plain app title.
func setSessionTitle(code string) {
	if state.hwnd == 0 {
		return
	}
	title := appTitle
	if c := strings.TrimSpace(code); c != "" {
		title = appTitle + " — " + c
	}
	setWindowText.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr(title))))
}
