//go:build windows

package main

import (
	"fmt"
	"strings"
	"time"
	"unsafe"
)

func paintShell(hdc uintptr) {
	var rc rect
	getClientRect.Call(state.hwnd, uintptr(unsafe.Pointer(&rc)))
	w := int(rc.Right - rc.Left)
	h := int(rc.Bottom - rc.Top)
	if w <= 0 || h <= 0 {
		return
	}

	state.mu.Lock()
	mode := state.mode
	status := state.statusText
	state.mu.Unlock()
	viewer.mu.RLock()
	connected := viewer.active
	viewer.mu.RUnlock()

	// ---- Canvas: subtle vertical navy gradient ----
	vGradient(hdc, 0, 0, w, h, uiBgColor, uiBgColor2, 22)

	paintTopBar(hdc, w, connected, status)
	paintSidebar(hdc, h)

	contentW := w - contentX - gutter

	switch {
	case connected:
		paintConnectedChrome(hdc, w, h)
	case mode == 0:
		paintHome(hdc, w, h, contentW)
	case mode == 1 && capturing.Load():
		paintAgentSharingBanner(hdc, w)
		if idleWarn.Load() {
			paintText(hdc, "Oturum boşta — sürdürmek için 'Açık Kal'", contentX+164, 214, w-contentX-gutter-164, 40, uiButtonFont, uiWarn, txVLeft)
		}
		paintChatPanel(hdc, computeChatGeom(w, h))
	case mode == 1:
		if deviceModeActive.Load() {
			paintPageHeader(hdc, "Cihaz Erişimi", "Teknisyen bağlantısı bekleniyor.")
			paintCard(hdc, contentX, 322, 782, 120, uiPanelColor, uiBorderColor, 16)
			paintText(hdc, "DURUM", contentX+24, 344, 300, 16, uiLabelFont, uiAccentColor, txVLeft)
			paintText(hdc, "● Etkin — teknisyen kimlik ve parola ile bağlanabilir", contentX+24, 366, 740, 24, uiSubtitleFont, uiSuccess, txVLeft)
			paintText(hdc, "Cihaz kimliği:  "+getActiveCode(), contentX+24, 400, 740, 20, uiBodyFont, uiMutedColor, txVLeft)
			break
		}
		paintPageHeader(hdc, "Destek Al", "Bu kodu destek verecek kişiye iletin.")
		paintText(hdc, "BAĞLANTI KODUNUZ", contentX, 300, 300, 18, uiLabelFont, uiMutedColor, txVLeft)
		paintCard(hdc, contentX, 322, 782, 128, uiEditColor, uiBorderColor, 16)
		if code := getActiveCode(); code != "" && code != "------" {
			drawQR(hdc, buildJoinLink(code), contentX, 478, 150)
			ic, note := internetCodeState()
			if ic != "" {
				paintText(hdc, "İNTERNET KODU — KOPYALAYIP GÖNDERİN, ELLE YAZMAYA GEREK YOK", contentX+166, 482, 600, 18, uiLabelFont, uiAccentColor, txVLeft)
				paintText(hdc, ic, contentX+166, 502, 330, 34, uiTitleFont, uiTextColor, txVLeft)
			} else {
				paintText(hdc, "KAREKODU TARATIN VEYA LİNKİ PAYLAŞIN", contentX+166, 486, 470, 18, uiLabelFont, uiAccentColor, txVLeft)
			}
			if note == "" {
				note = "Karşı taraf telefon kamerasıyla tarayabilir ya da linki uygulamaya yapıştırabilir."
			}
			paintText(hdc, note, contentX+166, 536, 620, 36, uiSmallFont, uiMutedColor, txWrap)
		} else {
			paintText(hdc, "Kod oluşturulduğunda burada görünür ve otomatik paylaşılır.", contentX, 470, 782, 20, uiBodyFont, uiFaintColor, txVLeft)
		}
		if exp := codeExpiryUnix.Load(); exp > 0 {
			rem := exp - time.Now().Unix()
			if rem < 0 {
				rem = 0
			}
			var ccol uint32 = uiCyanColor
			label := fmt.Sprintf("Kalan süre  %02d:%02d", rem/60, rem%60)
			if rem == 0 {
				label = "Kod süresi doldu — Geri dönüp tekrar başlatın"
				ccol = uiDanger
			}
			paintText(hdc, label, contentX, 300, 782, 18, uiLabelFont, ccol, txVRight)
		}
	case mode == 2:
		paintPageHeader(hdc, "Destek Ver", "Karşı bilgisayarın kodunu, internet kodunu ya da nexdesk:// bağlantısını girin.")
		paintText(hdc, "KAYITLI CİHAZ — seçince kimlik ve parola otomatik dolar", contentX, 252, 620, 16, uiLabelFont, uiMutedColor, txVLeft)
		paintText(hdc, "BAĞLANTI KODU", contentX, 326, 300, 18, uiLabelFont, uiMutedColor, txVLeft)
		paintText(hdc, "CİHAZ PAROLASI — yalnızca kayıtlı cihaza bağlanırken doldurun (boşsa tek seferlik kod)", contentX, 420, 782, 16, uiLabelFont, uiMutedColor, txVLeft)
		paintText(hdc, "Kod yalnızca karşı tarafın onayıyla bağlantı kurar.", contentX, 566, 782, 20, uiBodyFont, uiFaintColor, txVLeft)
	case mode == modeBook:
		paintBook(hdc, w, h)
	case mode == 10:
		paintSettings(hdc, w)
	case mode == 11:
		paintAbout(hdc, w)
	case mode == 13:
		paintDeviceAccess(hdc, w)
	}

	paintFooter(hdc, w, h, connected)
}

// paintTopBar draws the product header: logo, wordmark, and either the feature
// chips (idle) or a live session pill (connected).
func paintTopBar(hdc uintptr, w int, connected bool, status string) {
	fillBand(hdc, 0, 0, w, headerH, rgb(uiBarColor))
	fillBand(hdc, 0, headerH-1, w, 1, rgb(uiBorderColor))

	// Embedded product logo on its own plate, then the wordmark + tagline.
	paintGlow(hdc, 20, 15, 56, 56, brandAccent(), 14)
	drawLogoPlate(hdc, 20, 15, 56, 56, 14)

	paintText(hdc, strings.ToUpper(brandName()), 88, 16, 430, 26, uiBrandFont, uiTextColor, txVLeft)
	paintText(hdc, brandTagline(), 89, 46, 470, 22, uiBodyFont, uiMutedColor, txVLeft)

	if connected {
		// Session identity + live indicator, right-aligned (leaves room for the
		// red disconnect button that layoutUI places at the far right).
		px := w - 470
		if px > 540 {
			paintCard(hdc, px, 22, 240, 42, uiPanelColor, uiBorderColor, 12)
			paintDot(hdc, px+22, 43, 5, uiSuccess)
			paintText(hdc, "Bağlı", px+38, 22, 60, 42, uiButtonFont, uiTextColor, txVLeft)
			_, sas := getConnInfo()
			info := "Doğrulama —"
			var col uint32 = uiCyanColor
			if sas != "" {
				if sasVerified.Load() {
					// Out-of-band SAS confirmed: show it green with a shield
					// tick so a verified session reads at a glance.
					info, col = "✓ SAS "+sas, uiSuccess
				} else {
					info = "SAS " + sas
				}
			}
			paintText(hdc, info, px+92, 22, 138, 42, uiSmallFont, col, txEnd)
		}
		if idleWarn.Load() {
			paintText(hdc, "Oturum boşta kaldı", w-690, 22, 300, 42, uiButtonFont, uiWarn, txVRight)
		}
		return
	}

	// Feature chips, right-anchored so they track the window width.
	chips := []struct{ icon, title, sub string }{
		{"✓", "Güvenli Bağlantı", "Uçtan uca şifreleme"},
		{"ϟ", "Yüksek Performans", "Düşük gecikme"},
		{"▣", "Çoklu Monitör", "Tüm ekranlara erişim"},
		{"⚙", "Kolay Kullanım", "Herkes için tasarlandı"},
	}
	chipW := 172
	groupW := chipW * len(chips)
	gx := w - gutter - groupW
	if gx < 560 {
		return // small window: keep the header uncluttered
	}
	for i, c := range chips {
		x := gx + i*chipW
		if i > 0 {
			fillBand(hdc, x-6, 24, 1, 38, rgb(uiBorderColor))
		}
		paintRoundPanel(hdc, x, 24, 36, 36, uiPanelHi, 10)
		paintText(hdc, c.icon, x, 24, 36, 36, uiIconFont, uiCyanColor, txMid)
		paintText(hdc, c.title, x+46, 23, chipW-58, 18, uiButtonFont, uiTextColor, txEnd)
		paintText(hdc, c.sub, x+46, 42, chipW-58, 16, uiSmallFont, uiMutedColor, txEnd)
	}
}

// paintSidebar draws the left navigation rail with the active-page indicator.
func paintSidebar(hdc uintptr, h int) {
	navTop := headerH
	navH := h - headerH - footerH
	fillBand(hdc, 0, navTop, navW, navH, rgb(uiNavColor))
	fillBand(hdc, navW-1, navTop, 1, navH, rgb(uiBorderColor))

	page := sidebarPage.Load()
	items := []struct {
		label, icon string
		page        int32
	}{
		{"Ana Sayfa", "⌂", 0},
		{"Bağlantılar", "↔", 1},
		{"Ayarlar", "⚙", 2},
		{"Cihaz Erişimi", "⌨", 4},
		{"Hakkında", "ⓘ", 3},
		{"Yeni Oturum", "＋", -1}, // action, not a page: opens another window
	}
	iy := navTop + 16
	for _, it := range items {
		active := page == it.page
		var col, icol uint32 = uiMutedColor, uiFaintColor
		if it.page == -1 { // the "Yeni Oturum" action stands out in accent
			col, icol = uiAccentColor, uiAccentColor
		}
		if active {
			paintRoundPanel(hdc, 10, iy, navW-20, 44, uiPanelHi, 10)
			fillBand(hdc, 12, iy+9, 3, 26, rgb(uiAccentColor))
			col = uiTextColor
			icol = uiAccentColor
		}
		paintText(hdc, it.icon, 24, iy, 28, 44, uiIconFont, icol, txMid)
		paintText(hdc, it.label, 58, iy, navW-66, 44, uiBodyFont, col, txVLeft)
		iy += 52
	}

	paintText(hdc, strings.ToUpper(brandName()), 22, h-footerH-44, navW-30, 16, uiLabelFont, uiFaintColor, txVLeft)
	paintText(hdc, "Sürüm "+appVersion, 22, h-footerH-26, navW-30, 16, uiSmallFont, uiFaintColor, txVLeft)
}

func paintPageHeader(hdc uintptr, title, subtitle string) {
	paintText(hdc, title, contentX, 104, 640, 34, uiHeaderFont, uiTextColor, txVLeft)
	paintText(hdc, subtitle, contentX+2, 146, 720, 22, uiBodyFont, uiMutedColor, txVLeft)
}

// paintHome draws the two primary action cards (backgrounds are the owner-drawn
// homeGet/homeGive buttons) and a "Son Bağlantılar" panel below them.
func paintHome(hdc uintptr, w, h, contentW int) {
	paintPageHeader(hdc, "Uzaktan Destek", "Bilgisayarınızı güvenli bir bağlantıyla yönetin veya destek alın.")

	// "Son Bağlantılar" panel.
	recentY := 400
	recentH := h - footerH - recentY - 16
	if recentH <= 90 {
		return
	}
	paintCard(hdc, contentX, recentY, contentW, recentH, uiPanelColor, uiBorderColor, 14)
	paintText(hdc, "SON BAĞLANTILAR", contentX+22, recentY+18, 300, 18, uiLabelFont, uiMutedColor, txVLeft)
	fillBand(hdc, contentX+22, recentY+44, contentW-44, 1, rgb(uiBorderColor))
	items := recentSnapshot()
	if len(items) == 0 {
		paintText(hdc, "Henüz bağlantı geçmişi yok.", contentX+22, recentY+58, contentW-44, 22, uiSubtitleFont, uiFaintColor, txVLeft)
		paintText(hdc, "Kurduğunuz oturumlar burada listelenecek.", contentX+22, recentY+84, contentW-44, 20, uiBodyFont, uiFaintColor, txVLeft)
		return
	}
	rowH := 40
	y := recentY + 54
	maxRows := (recentY + recentH - 12 - y) / rowH
	if maxRows < 1 {
		maxRows = 1
	}
	if len(items) > maxRows {
		items = items[:maxRows]
	}
	for _, it := range items {
		paintDot(hdc, contentX+30, y+rowH/2, 4, uiSuccess)
		paintText(hdc, it.Role, contentX+48, y, 220, rowH, uiSubtitleFont, uiTextColor, txVLeft)
		paintText(hdc, "Kod "+formatCode(it.Code), contentX+280, y, 220, rowH, uiBodyFont, uiMutedColor, txVLeft)
		paintText(hdc, relativeTime(it.At), contentX+48, y, contentW-70, rowH, uiSmallFont, uiFaintColor, txVRight)
		y += rowH
	}
}

// paintConnectedChrome draws the remote-desktop workspace frame: a toolbar
// surface behind the screen/quality controls and a stats strip under the viewer.
func paintConnectedChrome(hdc uintptr, w, h int) {
	// Toolbar surface behind the monitor/resolution/action buttons.
	paintCard(hdc, contentX, 98, w-contentX-gutter, 58, uiPanelColor, uiBorderColor, 12)
	hostLbl := "UZAK MASAÜSTÜ"
	if h := getRemoteHost(); h != "" {
		hostLbl = "UZAK MASAÜSTÜ — " + h
	}
	paintText(hdc, hostLbl, contentX, 60, 600, 18, uiLabelFont, uiFaintColor, txVLeft)

	// Stats strip along the bottom of the viewer area (viewer sits at y=145,
	// height h-210 → its bottom is h-65; the footer starts at h-52).
	stripY := h - footerH - 13
	res := map[int32]string{0: "Otomatik", 1080: "1080p", 720: "720p", 480: "480p"}[captureMaxHeight.Load()]
	if res == "" {
		res = "Otomatik"
	}
	path, sas := getConnInfo()
	pathLabel := map[string]string{"direct": "Doğrudan bağlantı", "relay": "TURN üzerinden"}[path]
	if pathLabel == "" {
		pathLabel = "Yol belirleniyor…"
	}
	fps := fpsValue.Load()
	qCol := uint32(uiSuccess)
	qLabel := "İyi"
	if fps < 8 {
		qCol = uiDanger
		qLabel = "Zayıf"
	} else if fps < 18 {
		qCol = uiWarn
		qLabel = "Orta"
	}
	idleS := time.Now().Unix() - lastActivityUnix.Load()
	if idleS < 0 {
		idleS = 0
	}
	parts := []string{
		"Bağlantı: " + qLabel,
		"Süre: " + sessionElapsed(),
		fmt.Sprintf("Boşta: %02d:%02d", idleS/60, idleS%60),
		fmt.Sprintf("%d FPS", fpsValue.Load()),
		"Çözünürlük: " + res,
		"Kodek: JPEG karo (yalnızca değişen bölgeler)",
		pathLabel,
	}
	if sas != "" {
		parts = append(parts, "SAS: "+sas)
	}
	if a := adaptLabel(); a != "" {
		parts = append(parts, a)
	}
	stats := strings.Join(parts, "     •     ")
	paintDot(hdc, contentX+12, stripY+6, 4, qCol)
	paintText(hdc, stats, contentX+24, stripY, w-contentX-gutter-24, 12, uiSmallFont, uiFaintColor, txVLeft)
	paintChatPanel(hdc, computeChatGeom(w, h))
}

func paintAgentSharingBanner(hdc uintptr, w int) {
	bw := w - contentX - gutter
	// Persistent, high-visibility "you are being viewed" banner (agent side).
	paintCard(hdc, contentX, 104, bw, 96, uiPanelHi, uiDanger, 16)
	paintDot(hdc, contentX+30, 152, 7, uiDanger)
	paintText(hdc, "Ekranınız şu anda paylaşılıyor", contentX+52, 118, bw-74, 26, uiTitleFont, uiTextColor, txVLeft)
	if remoteRecording.Load() {
		paintText(hdc, "● KAYIT ALINIYOR", contentX+bw-220, 118, 200, 26, uiButtonFont, uiDanger, txVRight)
	}
	paintText(hdc, "Uzaktaki kişi ekranınızı görüyor ve fare/klavye kullanabilir. Bağlantıyı istediğiniz an kesebilirsiniz.", contentX+52, 150, bw-74, 22, uiBodyFont, uiMutedColor, txVLeft)
	_, sas := getConnInfo()
	if sas != "" {
		paintText(hdc, "Güvenlik doğrulama kodu (SAS): "+sas+"  —  karşı taraftakiyle birebir aynı olmalı."+map[bool]string{true: "  ✓ Doğrulandı"}[sasVerified.Load()], contentX, 214, bw, 20, uiBodyFont, uiCyanColor, txVLeft)
	}
}

func paintSettings(hdc uintptr, w int) {
	paintPageHeader(hdc, "Ayarlar", "Bağlantı, görüntü ve çalışma tercihleri.")
	cw := w - contentX - gutter
	if cw > 1000 {
		cw = 1000
	}
	// A — signaling
	paintCard(hdc, contentX, 168, cw, 84, uiPanelColor, uiBorderColor, 14)
	paintText(hdc, "BAĞLANTI", contentX+24, 182, 300, 16, uiLabelFont, uiAccentColor, txVLeft)
	paintText(hdc, "İnternet Signaling — boşsa gömülü kurum sunucusu, o da yoksa LAN", contentX+24, 210, 300, 18, uiBodyFont, uiMutedColor, txVLeft)
	// B — TURN
	paintCard(hdc, contentX, 262, cw, 138, uiPanelColor, uiBorderColor, 14)
	paintText(hdc, "TURN SUNUCUSU — İSTEĞE BAĞLI", contentX+24, 276, 400, 16, uiLabelFont, uiAccentColor, txVLeft)
	paintText(hdc, "STUN yetmezse (symmetric NAT) relay olarak kullanılır.", contentX+24, 296, 520, 16, uiSmallFont, uiMutedColor, txVLeft)
	paintText(hdc, "Adres", contentX+24, 328, 70, 16, uiSmallFont, uiMutedColor, txVLeft)
	paintText(hdc, "Kullanıcı", contentX+24, 366, 70, 16, uiSmallFont, uiMutedColor, txVLeft)
	paintText(hdc, "Parola", 556, 366, 60, 16, uiSmallFont, uiMutedColor, txVLeft)
	// C — görüntü
	paintCard(hdc, contentX, 402, cw, 74, uiPanelColor, uiBorderColor, 14)
	paintText(hdc, "GÖRÜNTÜ", contentX+24, 416, 200, 16, uiLabelFont, uiAccentColor, txVLeft)
	paintText(hdc, "Kalite", 520, 416, 120, 16, uiSmallFont, uiMutedColor, txVLeft)
	paintText(hdc, "Maksimum yükseklik", 760, 416, 180, 16, uiSmallFont, uiMutedColor, txVLeft)
	// D — oturum güvenliği
	paintCard(hdc, contentX, 486, cw, 106, uiPanelColor, uiBorderColor, 14)
	paintText(hdc, "OTURUM GÜVENLİĞİ", contentX+24, 500, 260, 16, uiLabelFont, uiAccentColor, txVLeft)
	paintText(hdc, "İnternet kodu — sunucusuz, modemde port açar", contentX+24, 530, 460, 16, uiSmallFont, uiMutedColor, txVLeft)
	paintText(hdc, "Boşta kalma uyarısı", 520, 500, 180, 16, uiSmallFont, uiMutedColor, txVLeft)
	paintText(hdc, "Süre dolunca (varsayılan: açık kal)", 760, 500, 320, 16, uiSmallFont, uiMutedColor, txVLeft)
	paintText(hdc, "Ayarlar uygulama yeniden başlatıldığında korunur.", contentX, 662, cw, 18, uiBodyFont, uiFaintColor, txVLeft)
}

func paintAbout(hdc uintptr, w int) {
	paintPageHeader(hdc, "Hakkında", brandName()+" hakkında bilgi.")
	cw := w - contentX - gutter
	paintCard(hdc, contentX, 190, cw, 316, uiPanelColor, uiBorderColor, 16)
	paintGlow(hdc, contentX+30, 214, 96, 96, brandAccent(), 20)
	drawLogoPlate(hdc, contentX+30, 214, 96, 96, 18)
	paintText(hdc, brandName(), contentX+150, 224, 460, 34, uiHeaderFont, uiTextColor, txVLeft)
	paintText(hdc, brandTagline(), contentX+150, 264, 520, 24, uiBodyFont, uiMutedColor, txVLeft)
	fillBand(hdc, contentX+30, 330, cw-60, 1, rgb(uiBorderColor))
	paintText(hdc, "PROGRAM YAZARI", contentX+30, 346, 240, 18, uiLabelFont, brandAccent(), txVLeft)
	paintText(hdc, brandAuthor(), contentX+30, 370, 320, 30, uiSubtitleFont, uiTextColor, txVLeft)
	paintText(hdc, "Sürüm "+appVersion+"     •     © 2026", contentX+30, 420, 420, 22, uiBodyFont, uiMutedColor, txVLeft)
	paintText(hdc, "Uçtan uca şifreleme  •  Çoklu monitör  •  Ekran/dosya aktarımı  •  İşaretleme  •  Otomatik yeniden bağlanma", contentX+30, 452, cw-60, 22, uiBodyFont, uiMutedColor, txVLeft)
	paintText(hdc, "GÜVENLİK", contentX+30, 480, 200, 16, uiLabelFont, uiCyanColor, txVLeft)
	paintText(hdc, "DTLS 1.2 şifreleme  •  SAS parmak izi doğrulaması  •  Pinli sunucu bağlantısı  •  Oturum başına TURN kimliği", contentX+150, 480, cw-180, 18, uiSmallFont, uiMutedColor, txVLeft)
	// Theme selector + update check (controls positioned in ui_layout mode 11).
	paintText(hdc, "GÖRÜNÜM — TEMA", contentX+30, 540, 260, 16, uiLabelFont, brandAccent(), txVLeft)
	paintText(hdc, "GÜNCELLEME", contentX+310, 540, 280, 16, uiLabelFont, brandAccent(), txVLeft)
	if st := getUpdateStatus(); st != "" {
		col := uiMutedColor
		switch {
		case strings.HasPrefix(st, "Yeni sürüm"), strings.HasPrefix(st, "Güncelleme gerekli"):
			col = uiWarn
		case strings.HasPrefix(st, "Güncel"):
			col = uiSuccess
		}
		paintText(hdc, st, contentX+310, 614, 430, 18, uiBodyFont, col, txVLeft)
	}
}

func paintFooter(hdc uintptr, w, h int, connected bool) {
	footerY := h - footerH
	fillBand(hdc, 0, footerY, w, footerH, rgb(uiBarColor))
	fillBand(hdc, 0, footerY, w, 1, rgb(uiBorderColor))
	var dotCol uint32 = uiFaintColor
	if connected {
		dotCol = uiSuccess
	}
	paintDot(hdc, 30, footerY+footerH/2, 5, dotCol)
	label := "Bağlantı Durumu: " + map[bool]string{true: "Aktif", false: "Hazır"}[connected]
	paintText(hdc, label, 44, footerY, 260, footerH, uiBodyFont, uiTextColor, txVLeft)
	paintText(hdc, "Program Yazarı: "+brandAuthor()+"  •  © 2026", w-360, footerY, 336, footerH, uiBodyFont, uiMutedColor, txVRight)
	// Live status line (file transfer %, reconnects, errors …). It used to be
	// kept only in memory, so progress was never visible.
	state.mu.Lock()
	status := state.statusText
	state.mu.Unlock()
	if mid := w - 360 - 320; mid > 120 {
		paintText(hdc, status, 320, footerY, mid-16, footerH, uiBodyFont, uiCyanColor, txEnd)
	}
}

func paintDeviceAccess(hdc uintptr, w int) {
	paintPageHeader(hdc, "Cihaz Erişimi", "Teknisyenlerin bu bilgisayara kimlik ve parola ile bağlanmasına izin verin.")
	cw := w - contentX - gutter
	if cw > 820 {
		cw = 820
	}
	paintCard(hdc, contentX, 190, cw, 340, uiPanelColor, uiBorderColor, 16)
	paintText(hdc, "DURUM", contentX+24, 212, 300, 16, uiLabelFont, uiAccentColor, txVLeft)
	paintText(hdc, "Açıkken bu bilgisayar sunucuya kayıtlı kalır; teknisyen kimlik + parola ile bağlanır. Bağlantı yine onay ve kayıt ile görünür olur.", contentX+300, 240, cw-320, 40, uiSmallFont, uiMutedColor, txWrap)
	paintText(hdc, "CİHAZ KİMLİĞİ (teknisyen bunu girer)", contentX+24, 298, 400, 16, uiLabelFont, uiMutedColor, txVLeft)
	paintText(hdc, "CİHAZ PAROLASI (en az 4 karakter)", contentX+24, 378, 400, 16, uiLabelFont, uiMutedColor, txVLeft)
	st := "● Kapalı"
	var sc uint32 = uiFaintColor
	if deviceModeActive.Load() {
		st = "● Etkin — teknisyen bağlantısı bekleniyor"
		sc = uiSuccess
	}
	paintText(hdc, st, contentX+24, 540, cw, 20, uiBodyFont, sc, txVLeft)
	paintText(hdc, "Parola sunucuda yalnızca şifreli özet (salt + SHA-256) olarak tutulur.", contentX+24, 566, cw, 18, uiSmallFont, uiFaintColor, txVLeft)
}
