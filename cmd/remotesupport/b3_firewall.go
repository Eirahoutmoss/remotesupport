//go:build windows

package main

// Windows Firewall: with the user's consent (UAC prompt) allow NexDesk to
// receive LAN discovery (UDP 8090), signaling (TCP 8091) and WebRTC traffic.

import (
	"os"
	"strings"
	"time"
	"unsafe"
)

// Rule names. "NexDesk" was the 1.1 rule set: every inbound port to the exe
// plus TCP 8091 from anywhere. 1.2 replaces it with a narrower set, so the
// version is part of the name and existing installs are upgraded once.
const (
	fwLegacyRule   = "NexDesk"
	fwRuleName     = "NexDesk (v2)"
	fwInternetRule = "NexDesk (internet)"
)

func firewallRuleNamed(name string) bool {
	return hiddenCmd("netsh", "advfirewall", "firewall", "show", "rule", "name="+name).Run() == nil
}

func firewallRuleExists() bool { return firewallRuleNamed(fwRuleName) }

// ensureFirewall runs on the GUI thread when the agent starts sharing.
//
//   - WebRTC media: UDP to NexDesk.exe from anywhere (ICE picks random ports).
//   - LAN discovery UDP 8090 and embedded signaling TCP 8091: local subnet only.
//   - TCP 8091 from anywhere only when the user turned the internet code on.
func ensureFirewall() {
	internet := currentSettings().InternetCode
	needBase := !firewallRuleExists()
	needInet := internet && !firewallRuleNamed(fwInternetRule)
	dropInet := !internet && firewallRuleNamed(fwInternetRule)
	legacy := firewallRuleNamed(fwLegacyRule)
	if !needBase && !needInet && !dropInet && !legacy {
		return
	}
	text := "Windows Güvenlik Duvarı, başka bilgisayarların size bağlanmasını engelleyebilir.\r\n\r\n" +
		"NexDesk için izin eklensin mi?\r\n  • NexDesk.exe UDP (görüntü aktarımı)\r\n  • UDP 8090 ve TCP 8091 — yalnızca yerel ağdan\r\n"
	if internet {
		text += "  • TCP 8091 — internetten (Ayarlar'da internet kodu açık)\r\n"
	}
	if legacy {
		text += "\r\nEski sürümün daha geniş izinleri kaldırılacak.\r\n"
	}
	text += "\r\nEvet derseniz Windows yönetici onayı (UAC) isteyecek."
	if !askYesNo(brandName()+" — Güvenlik Duvarı", text) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	rules := []string{
		`netsh advfirewall firewall delete rule name="` + fwLegacyRule + `"`,
		`netsh advfirewall firewall delete rule name="` + fwRuleName + `"`,
		`netsh advfirewall firewall delete rule name="` + fwInternetRule + `"`,
		`netsh advfirewall firewall add rule name="` + fwRuleName + `" dir=in action=allow program="` + exe + `" protocol=UDP enable=yes profile=any`,
		`netsh advfirewall firewall add rule name="` + fwRuleName + `" dir=in action=allow protocol=UDP localport=8090 remoteip=localsubnet enable=yes profile=any`,
		`netsh advfirewall firewall add rule name="` + fwRuleName + `" dir=in action=allow protocol=TCP localport=8091 remoteip=localsubnet enable=yes profile=any`,
	}
	if internet {
		rules = append(rules, `netsh advfirewall firewall add rule name="`+fwInternetRule+`" dir=in action=allow protocol=TCP localport=8091 enable=yes profile=any`)
	}
	args := `/c ` + strings.Join(rules, " & ")
	r, _, _ := shellExecuteW.Call(state.hwnd, uintptr(unsafe.Pointer(utf16ptr("runas"))), uintptr(unsafe.Pointer(utf16ptr("cmd.exe"))),
		uintptr(unsafe.Pointer(utf16ptr(args))), 0, 0) // SW_HIDE
	if r <= 32 {
		setStatus("● Güvenlik duvarı izni eklenmedi (yönetici onayı verilmedi).")
		return
	}
	go func() {
		for i := 0; i < 30; i++ {
			time.Sleep(time.Second)
			if firewallRuleExists() && (!internet || firewallRuleNamed(fwInternetRule)) {
				auditLog("Güvenlik duvarı izni eklendi")
				netlogf("Güvenlik duvarı izni eklendi (UDP %s, 8090/8091 yerel alt ağ, internet=%v)", exe, internet)
				setStatus("● Güvenlik duvarı izni eklendi — başka bilgisayarlar artık bağlanabilir.")
				return
			}
		}
		setStatus("● Güvenlik duvarı izni doğrulanamadı.")
	}()
}
