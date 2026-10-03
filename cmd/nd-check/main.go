// nd-check: NexDesk bağlantı tanısı. Signaling (wss + pin), oturum başına TURN
// kimliği ve TURN üzerinden gerçek bir WebRTC bağlantısını sırayla dener.
package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	clientsignaling "github.com/eirahoutmoss/remotesupport/client/signaling"
	server "github.com/eirahoutmoss/remotesupport/server/signaling"
	"github.com/pion/webrtc/v4"
)

func main() {
	ws := flag.String("ws", "wss://SUNUCU_IP:8443/v1/ws#pin=PIN", "signaling adresi")
	flag.Parse()
	fmt.Println("NexDesk bağlantı tanısı —", time.Now().Format("2006-01-02 15:04:05"))
	creds := signalingStep(*ws)
	if creds == nil {
		fmt.Println("\nSONUÇ: TURN kimliği alınamadı, TURN testleri atlandı.")
	} else {
		fmt.Printf("\n== 3) Her TURN adresi tek tek (yalnızca relay aday toplama)\n")
		for _, u := range creds.URLs {
			gatherRelay(u, creds.Username, creds.Credential)
		}
		fmt.Printf("\n== 4) İki uç arasında YALNIZCA TURN üzerinden bağlantı\n")
		connectPair("relay", []webrtc.ICEServer{{URLs: creds.URLs, Username: creds.Username, Credential: creds.Credential, CredentialType: webrtc.ICECredentialTypePassword}}, webrtc.ICETransportPolicyRelay)
	}
	fmt.Printf("\n== 5) İki uç arasında TURN'süz bağlantı (yerel adaylar + STUN)\n")
	connectPair("doğrudan", []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}, webrtc.ICETransportPolicyAll)
	fmt.Println("\nBİTTİ")
}

func signalingStep(ws string) *server.ICECreds {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fmt.Println("\n== 1) Signaling:", ws)
	tg, err := clientsignaling.Dial(ctx, ws)
	if err != nil {
		fmt.Println("  HATA bağlanılamadı:", err)
		return nil
	}
	defer tg.Close(context.Background())
	tg.App = "nd-check"
	code, _, err := tg.Create(ctx)
	if err != nil {
		fmt.Println("  HATA kod alınamadı:", err)
		return nil
	}
	fmt.Printf("  OK kod alındı, sunucu sürümü=%d özellikler=%v\n", tg.ServerV, tg.Features)
	op, err := clientsignaling.Dial(ctx, ws)
	if err != nil {
		fmt.Println("  HATA ikinci bağlantı:", err)
		return nil
	}
	defer op.Close(context.Background())
	op.App, op.Name = "nd-check", "nd-check"
	if err := op.Join(ctx, code); err != nil {
		fmt.Println("  HATA katılma:", err)
		return nil
	}
	pj, err := tg.Read(ctx)
	if err != nil {
		fmt.Println("  HATA peer_joined:", err)
		return nil
	}
	fmt.Printf("  OK katılındı, sunucunun gördüğü adres=%s\n", pj.From)
	if err := tg.Approve(ctx, true, ""); err != nil {
		fmt.Println("  HATA onay:", err)
		return nil
	}
	fmt.Println("\n== 2) TURN kimliği")
	var creds *server.ICECreds
	if tg.HasFeature(server.FeatureTURNCred) {
		im, err := tg.Read(ctx)
		if err != nil || im.ICE == nil {
			fmt.Println("  HATA hedef ice mesajı:", im.Type, err)
		} else {
			creds = im.ICE
		}
	}
	ap, err := op.Read(ctx)
	if err != nil || ap.Type != "approved" {
		fmt.Println("  HATA approved:", ap.Type, err)
		return nil
	}
	if ap.ICE != nil {
		creds = ap.ICE
	}
	if creds == nil {
		fmt.Println("  HATA sunucu TURN kimliği vermedi")
		return nil
	}
	fmt.Printf("  OK kullanıcı=%s geçerlilik=%s\n", creds.Username, time.Unix(creds.ExpiresAt, 0).Format("15:04"))
	for _, u := range creds.URLs {
		fmt.Println("     adres:", u)
	}
	return creds
}

func gatherRelay(u, user, pass string) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers:         []webrtc.ICEServer{{URLs: []string{u}, Username: user, Credential: pass, CredentialType: webrtc.ICECredentialTypePassword}},
		ICETransportPolicy: webrtc.ICETransportPolicyRelay,
	})
	if err != nil {
		fmt.Printf("  %-45s HATA %v\n", u, err)
		return
	}
	defer pc.Close()
	_, _ = pc.CreateDataChannel("t", nil)
	relay := 0
	var addrs []string
	done := make(chan struct{})
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			close(done)
			return
		}
		if c.Typ == webrtc.ICECandidateTypeRelay {
			relay++
			addrs = append(addrs, fmt.Sprintf("%s:%d", c.Address, c.Port))
		}
	})
	offer, _ := pc.CreateOffer(nil)
	start := time.Now()
	_ = pc.SetLocalDescription(offer)
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		fmt.Printf("  %-45s ZAMAN AŞIMI (15 sn)\n", u)
		return
	}
	if relay == 0 {
		fmt.Printf("  %-45s BAŞARISIZ: relay adayı yok (%s) — kimlik reddedildi ya da port kapalı\n", u, time.Since(start).Round(time.Millisecond))
		return
	}
	fmt.Printf("  %-45s OK relay=%s (%s)\n", u, strings.Join(addrs, ","), time.Since(start).Round(time.Millisecond))
}

func connectPair(label string, ice []webrtc.ICEServer, policy webrtc.ICETransportPolicy) {
	cfg := webrtc.Configuration{ICEServers: ice, ICETransportPolicy: policy}
	a, err1 := webrtc.NewPeerConnection(cfg)
	b, err2 := webrtc.NewPeerConnection(cfg)
	if err1 != nil || err2 != nil {
		fmt.Println("  HATA:", err1, err2)
		return
	}
	defer a.Close()
	defer b.Close()
	got := make(chan string, 1)
	b.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnMessage(func(m webrtc.DataChannelMessage) { got <- string(m.Data) })
	})
	dc, _ := a.CreateDataChannel("t", nil)
	dc.OnOpen(func() { _ = dc.SendText("merhaba") })
	start := time.Now()
	offer, _ := a.CreateOffer(nil)
	ga := webrtc.GatheringCompletePromise(a)
	_ = a.SetLocalDescription(offer)
	<-ga
	_ = b.SetRemoteDescription(*a.LocalDescription())
	ans, _ := b.CreateAnswer(nil)
	gb := webrtc.GatheringCompletePromise(b)
	_ = b.SetLocalDescription(ans)
	<-gb
	_ = a.SetRemoteDescription(*b.LocalDescription())
	select {
	case m := <-got:
		pair := "?"
		if sp, err := a.SCTP().Transport().ICETransport().GetSelectedCandidatePair(); err == nil && sp != nil {
			pair = fmt.Sprintf("%s %s:%d <-> %s %s:%d", sp.Local.Typ, sp.Local.Address, sp.Local.Port, sp.Remote.Typ, sp.Remote.Address, sp.Remote.Port)
		}
		fmt.Printf("  OK (%s) %q alındı, %s — yol: %s\n", label, m, time.Since(start).Round(time.Millisecond), pair)
	case <-time.After(20 * time.Second):
		fmt.Printf("  BAŞARISIZ (%s): 20 sn içinde bağlantı kurulamadı, durum=%s\n", label, a.ICEConnectionState())
	}
}
