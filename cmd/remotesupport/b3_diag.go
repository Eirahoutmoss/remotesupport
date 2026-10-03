//go:build windows

package main

// Connection diagnostics for the serverless (invite code) path: live ICE
// progress in the status bar, a timeline in netlog.log and, if no connection
// forms, a plain-language report (also copied to the clipboard).

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unsafe"

	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/pion/webrtc/v4"
)

type diagCand struct{ typ, addr string }

func sdpCandidates(payload []byte) []diagCand {
	var sd webrtc.SessionDescription
	if json.Unmarshal(payload, &sd) != nil {
		return nil
	}
	var out []diagCand
	for _, line := range strings.Split(sd.SDP, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "a=candidate:") {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(line, "a=candidate:"))
		if len(f) >= 8 && f[1] == "1" {
			out = append(out, diagCand{f[7], f[4] + ":" + f[5]})
		}
	}
	return out
}

func candSummary(cs []diagCand) string {
	if len(cs) == 0 {
		return "    (hiç adres yok)\r\n"
	}
	var sb strings.Builder
	names := map[string]string{"host": "yerel ", "srflx": "dış   ", "relay": "TURN  ", "prflx": "eşten "}
	for _, c := range cs {
		n := names[c.typ]
		if n == "" {
			n = c.typ
		}
		sb.WriteString("    " + n + " " + c.addr + "\r\n")
	}
	return sb.String()
}

func publicIPs(cs []diagCand) map[string]bool {
	m := map[string]bool{}
	for _, c := range cs {
		if c.typ == "srflx" {
			m[strings.Split(c.addr, ":")[0]] = true
		}
	}
	return m
}

func hasType(cs []diagCand, t string) bool {
	for _, c := range cs {
		if c.typ == t {
			return true
		}
	}
	return false
}

// diagnose guesses the most likely cause from both sides' candidates.
func diagnose(local, remote []diagCand) string {
	var tips []string
	if !hasType(local, "srflx") && !hasType(local, "relay") {
		tips = append(tips, "• Bu bilgisayar dış adresini öğrenemedi (STUN'a ulaşılamadı). Ağ, dışarıya UDP trafiğini engelliyor olabilir; Ayarlar'a bir TURN sunucusu girmeniz gerekir.")
	}
	if !hasType(remote, "srflx") && !hasType(remote, "relay") {
		tips = append(tips, "• Karşı bilgisayar dış adresini öğrenemedi (STUN'a ulaşılamadı). Onun ağı dışarıya UDP'yi engelliyor olabilir.")
	}
	if hasType(local, "relay") && !hasType(remote, "relay") {
		return "• Bu bilgisayar TURN sunucusundan aktarma adresi aldı, karşı bilgisayar ALAMADI.\r\n\r\n  Olası sebepler:\r\n  1) Sunucunun portları dışarıya yönlendirilmemiş (TCP 8091, UDP/TCP 3478, UDP 49160-49200 → sunucu).\r\n  2) Karşı bilgisayarda gömülü sunucu ayarlarını içermeyen ESKİ NexDesk sürümü çalışıyor.\r\n\r\n  Ayrıca karşı tarafın bu bilgisayarın aktarma adresine (UDP 49160-49200) ulaşamaması da yönlendirmenin eksik olduğunu gösterir."
	}
	if hasType(local, "relay") && hasType(remote, "relay") {
		return "• İki taraf da TURN aktarma adresi aldı ama bağlantı kurulamadı. Sunucudaki UDP 49160-49200 aralığının dışarıya doğru yönlendirildiğini ve sunucu güvenlik duvarının bu portlara izin verdiğini kontrol edin."
	}
	lp, rp := publicIPs(local), publicIPs(remote)
	same := false
	for ip := range lp {
		if rp[ip] {
			same = true
		}
	}
	if same {
		tips = append(tips, "• İki bilgisayar aynı internet çıkışını kullanıyor (aynı ağ). Bağlantı yerel adresler üzerinden kurulmalı: karşı tarafta Windows Güvenlik Duvarı UDP'yi engelliyor olabilir — 'Destek Al' ekranında güvenlik duvarı iznini verin. Ayrıca iki bilgisayarın yerel adreslerinin listede göründüğünü kontrol edin.")
	} else if len(lp) > 0 && len(rp) > 0 {
		tips = append(tips, "• İki taraf da dış adresini öğrendi ama birbirine ulaşamadı. Ağlardan en az biri katı NAT (symmetric NAT / kurumsal güvenlik duvarı) kullanıyor olabilir; bu durumda doğrudan bağlantı mümkün değildir, Ayarlar'a bir TURN sunucusu girilmelidir.")
	}
	if len(tips) == 0 {
		tips = append(tips, "• Belirgin bir sebep bulunamadı. Kodların tamamının kopyalandığından ve iki tarafta da aynı NexDesk sürümünün çalıştığından emin olun.")
	}
	return strings.Join(tips, "\r\n\r\n")
}

// manualDiag follows one serverless attempt. operator=true on the side that
// pasted the reply (it reports after a timeout); the agent only reports
// progress, since it legitimately waits for the other side to paste.
func manualDiag(peer *webrtcpeer.Peer, localPayload, remotePayload []byte, operator bool) {
	defer logCrash("manualDiag")
	local, remote := sdpCandidates(localPayload), sdpCandidates(remotePayload)
	role := map[bool]string{true: "operatör", false: "agent"}[operator]
	netlogf("[tanı %s] kendi adresleri: %v", role, local)
	netlogf("[tanı %s] karşı adresler: %v", role, remote)
	start := time.Now()
	var timeline []string
	last := webrtc.ICEConnectionState(0)
	limit := 45 * time.Second
	if !operator {
		limit = 10 * time.Minute
	}
	for time.Since(start) < limit {
		st := peer.ICEState()
		if st != last {
			e := fmt.Sprintf("%4.1f sn  %s", time.Since(start).Seconds(), st)
			timeline = append(timeline, e)
			netlogf("[tanı %s] ICE %s", role, e)
			last = st
		}
		switch st {
		case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
			netlogf("[tanı %s] bağlandı: %s (%.1f sn)", role, peer.SelectedPair(), time.Since(start).Seconds())
			return
		case webrtc.ICEConnectionStateClosed:
			return
		case webrtc.ICEConnectionStateFailed:
			if !operator {
				setStatus("● Bağlantı kurulamadı (ICE başarısız). Ayrıntı: Bağlantı Günlüğü.")
				return
			}
			limit = 0
		}
		if sec := int(time.Since(start).Seconds()); sec%3 == 0 {
			if operator {
				setStatus(fmt.Sprintf("● Doğrudan bağlantı deneniyor… (%d sn, durum: %s)", sec, st))
			} else if st == webrtc.ICEConnectionStateChecking || st == webrtc.ICEConnectionStateNew {
				setStatus(fmt.Sprintf("● Yanıt kodunun karşıda girilmesi bekleniyor… (%d sn)", sec))
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !operator || currentPeer() != peer {
		return
	}
	report := "Bağlantı kurulamadı.\r\n\r\nBU BİLGİSAYARIN ADRESLERİ:\r\n" + candSummary(local) +
		"\r\nKARŞI BİLGİSAYARIN ADRESLERİ:\r\n" + candSummary(remote) +
		"\r\nBAĞLANTI ADIMLARI:\r\n    " + strings.Join(timeline, "\r\n    ") +
		"\r\n\r\nOLASI SEBEP:\r\n" + diagnose(local, remote) +
		"\r\n\r\n(Bu rapor panoya kopyalandı.)"
	netlogf("[tanı] RAPOR\n%s", strings.ReplaceAll(report, "\r\n", "\n"))
	copyToClipboard(report)
	setStatus("● Bağlantı kurulamadı — tanı raporu panoya kopyalandı.")
	messageBox.Call(0, uintptr(unsafe.Pointer(utf16ptr(report))), uintptr(unsafe.Pointer(utf16ptr(brandName()+" — Bağlantı Tanısı"))), 0x00000030)
}
