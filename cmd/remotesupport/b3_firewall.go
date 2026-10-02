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

const fwRuleName = "NexDesk"

func firewallRuleExists() bool {
	return hiddenCmd("netsh", "advfirewall", "firewall", "show", "rule", "name="+fwRuleName).Run() == nil
}

// ensureFirewall runs on the GUI thread when the agent starts sharing.
func ensureFirewall() {
	if firewallRuleExists() {
		return
	}
	if !askYesNo(brandName()+" — Güvenlik Duvarı", "Windows Güvenlik Duvarı, başka bilgisayarların size bağlanmasını engelleyebilir.\r\n\r\n"+
		"NexDesk için izin eklensin mi?\r\n  • UDP 8090 (yerel ağda bulunma)\r\n  • TCP 8091 (bağlantı kurma)\r\n  • NexDesk.exe (görüntü aktarımı)\r\n\r\n"+
		"Evet derseniz Windows yönetici onayı (UAC) isteyecek.") {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	rules := []string{
		`netsh advfirewall firewall delete rule name="` + fwRuleName + `"`,
		`netsh advfirewall firewall add rule name="` + fwRuleName + `" dir=in action=allow program="` + exe + `" enable=yes profile=any`,
		`netsh advfirewall firewall add rule name="` + fwRuleName + `" dir=in action=allow protocol=UDP localport=8090 enable=yes profile=any`,
		`netsh advfirewall firewall add rule name="` + fwRuleName + `" dir=in action=allow protocol=TCP localport=8091 enable=yes profile=any`,
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
			if firewallRuleExists() {
				auditLog("Güvenlik duvarı izni eklendi")
				netlogf("Güvenlik duvarı izni eklendi (UDP 8090, TCP 8091, %s)", exe)
				setStatus("● Güvenlik duvarı izni eklendi — başka bilgisayarlar artık bağlanabilir.")
				return
			}
		}
		setStatus("● Güvenlik duvarı izni doğrulanamadı.")
	}()
}
