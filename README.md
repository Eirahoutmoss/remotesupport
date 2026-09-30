<div align="center">

<img src="cmd/remotesupport/assets/logo.png" alt="NexDesk" width="200"/>

# NexDesk

### Uzaktan destek, daha kolay.

Windows için **açık, güvenli ve tamamen kullanıcı onaylı** uzak masaüstü destek uygulaması.
Ara sunucuya muhtaç değil, reklam yok, arka planda casusluk yok — sadece iki bilgisayar, bir kod ve uçtan uca şifreli bir bağlantı.

<br/>

![Platform](https://img.shields.io/badge/platform-Windows-0A66C2?style=for-the-badge&logo=windows&logoColor=white)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![WebRTC](https://img.shields.io/badge/WebRTC-P2P-333333?style=for-the-badge&logo=webrtc&logoColor=white)
![Lisans](https://img.shields.io/badge/lisans-MIT-26D07C?style=for-the-badge)
![Bağımlılık](https://img.shields.io/badge/kurulum-tek%20.exe-2E90FF?style=for-the-badge)

</div>

---

## 🎯 NexDesk nedir?

Birine uzaktan yardım etmek çoğu zaman şöyle başlar: *"Şu programı indir, şu hesabı aç, şu izni ver, bu pencereyi kapat..."*

NexDesk bunu kısaltır:

> **Sen kodu ver → o kodu yapıştırır → sen "izin ver" dersin → bağlanırsınız.**

Görüntü de, klavye-fare de, dosyalar da doğrudan iki bilgisayar arasında (peer-to-peer) akar. Ortada verilerinizi gören bir şirket yoktur — **kendi sunucunuzu kurmanıza da gerek yoktur.** Bağlantı kurulduğunda iki tarafta görünen **doğrulama kodunu (SAS)** karşılaştırırsanız araya kimsenin giremeyeceğinden de emin olursunuz.

---

## ✨ Öne çıkan özellikler

| | |
|---|---|
| 🔒 **Uçtan uca şifreli** | Görüntü, kontrol ve dosyalar WebRTC (DTLS) ile şifrelenir. |
| ✅ **Her zaman onaylı** | Kimse sizin "İzin Ver" demeden ekranınıza bağlanamaz. |
| 🌍 **Sunucusuz internet bağlantısı** | İngiltere'deki birine bile: internet kodu (UPnP) ya da 97 karakterlik davet kodu. |
| 🧭 **Doğrudan P2P** | STUN ile doğrudan bağlantı; çok katı ağlarda isteğe bağlı TURN. |
| 🛡️ **Dolandırıcılık kalkanı** | Oturumda banka/ödeme sayfası açılırsa görüntü ve kontrol anında durur. |
| 🔑 **SAS doğrulaması** | Tek tıkla karşılaştırma; uyuşmazsa oturum hemen kesilir. |
| 🔁 **Otomatik yeniden bağlanma** | Ağ dalgalanınca oturum geri gelir; yarım kalan dosya **kaldığı yerden** sürer. |
| 🖥️ **Çoklu monitör** | Karşı taraftaki tüm ekranlar arasında geçiş. |
| 🖱️ **Uzak imleç & tıklama efekti** | Teknisyen imleci görür; kullanıcı teknisyenin nereye tıkladığını görür. |
| 📶 **Uyarlamalı kalite** | Hat zayıflarsa 720p → 480p → gri tona iner, düzelince geri çıkar. |
| 🧰 **Onarım paketi** | DNS, geçici dosyalar, yazıcı kuyruğu, Gezgin, winsock, sfc — tek tık. |
| 🎬 **Oturum video kaydı** | MJPEG AVI olarak kaydedilir; karşı tarafa "KAYIT ALINIYOR" gösterilir. |
| ⏺️ **Makrolar** | Yaptığınız işlemleri kaydedin, sonra tek tıkla tekrar oynatın. |
| 🔄 **Karşılıklı ekran** | Kendi ekranınızı karşı tarafa gösterin (yalnızca izleme). |
| 🤝 **Oturum devri** | Bağlantıyı koparmadan oturumu başka bir teknisyene aktarın. |
| 📇 **Adres defteri** | Etiketler, bilgisayar başına not geçmişi, Wake-on-LAN ile uyandırma. |
| 📁 **Dosya & pano** | Sürükle-bırak dosya, pano paylaşımı, 30 sn'de silinen güvenli pano. |
| 🖊️ **Ekran işaretleme** | Uzak ekrana çizip yol gösterin (çizimler solar). |
| 🕶️ **Gizlilik perdesi & giriş kilidi** | Karşı ekranı karartın, yerel klavye/fareyi geçici kilitleyin. |
| 🎨 **Beyaz etiket** | İsim, slogan, renk ve logoyu kendi markanıza göre değiştirin. |
| 🪶 **Tek dosya** | Kurulum yok. `NexDesk.exe` çalıştır, hazır. Signaling gömülü. |

---

## 🚀 Hızlı başlangıç

### İndir & çalıştır
`dist/NexDesk.exe` dosyasını çalıştırmanız yeterli. Kurulum, .NET, ek paket **yok**.

### Kaynaktan derleme
Go 1.26+ kurulu olmalı:

```powershell
powershell -ExecutionPolicy Bypass -File .\build.ps1
```

Çıktı: `dist\NexDesk.exe` (konsol penceresi olmayan, tek parça GUI uygulaması, gömülü uygulama ikonu).
`assets\nexdesk.ico` değişirse ikon kaynağı (`rsrc_windows_amd64.syso`) derleme sırasında otomatik yenilenir.

---

## 🕹️ Nasıl kullanılır?

<table>
<tr>
<td width="50%" valign="top">

### 🆘 Destek ALAN taraf
1. **"Destek Al"** kartına tıkla.
2. Ekrandaki **kodu** karşı tarafa ilet (**Kopyala** düğmesi var).
3. Bağlantı isteği gelince **"İzin Ver"** de.
4. Ekranın paylaşıldığı sürece üstte kırmızı uyarı görünür; istediğin an **"Bağlantıyı Kes"**.

</td>
<td width="50%" valign="top">

### 🧑‍🔧 Destek VEREN taraf
1. **"Destek Ver"** kartına tıkla.
2. Karşı tarafın **kodunu / internet kodunu / linkini** yapıştır.
3. **"Bağlan"** de, onay bekle.
4. **İşlemler ▾** menüsünden dosya, pano, onarım, kayıt, makro, SAS doğrulama...

</td>
</tr>
</table>

> 💡 **Güvenlik ipucu:** Bağlandıktan sonra **İşlemler ▾ → Güvenlik Kodunu Doğrula (SAS)** ile iki taraftaki 6 haneli kodu karşılaştırın. Aynıysa ✓ işareti çıkar; farklıysa oturum güvenlik için hemen kesilir.

---

## 🌍 Bağlantı yolları — sunucu kurmadan

Hangi yol kullanılırsa kullanılsın, kod NexDesk'te **aynı kutuya yapıştırılır**; program türünü kendisi anlar.

| Yol | Ne zaman? | Kod | Nasıl çalışır? |
|---|---|---|---|
| 🏠 **Yerel ağ kodu** | Aynı ofis/ev ağı | `591 490 5011` | Gömülü signaling + LAN keşfi. |
| 🌐 **İnternet kodu** | Farklı şehir/ülke, ev modemi | `REQKE-2GZKC-0FZX1-S32WG` | Modemde port **UPnP ile otomatik** açılır, dış IP koda gömülür. |
| ✉️ **Davet kodu** | UPnP yok, kurumsal ağ, CGNAT | `DAVET-…` / `YANIT-…` (97 karakter) | Tamamen sunucusuz iki adımlı el sıkışma. |

### 🌐 İnternet kodu
- "Destek Al" sayfasında kod oluşunca NexDesk modemde portu açar ve dış IP'yi öğrenir.
- 20 karakterlik kod **IP + port + oturum kodunu** taşır; içinde doğrulama hanesi olduğu için yanlış yazılan kod reddedilir, O/0 ve I/L/1 karışıklığı kendiliğinden düzeltilir.
- Oturum bitince modemde açılan port kapatılır.
- **Çalışmadığı durumlar** ekranda açıklanır: modemde UPnP kapalıysa 8091/TCP elle yönlendirilmelidir; operatör paylaşımlı IP (CGNAT) kullanıyorsa bu yol ulaşamaz → **davet kodunu** kullanın.

### ✉️ Davet kodu (sunucusuz el sıkışma)
1. **Destek veren:** "Destek Ver" → **Sunucusuz Bağlan (davet kodu)**. Kod ekranda seçilebilir kutuda çıkar ve panoya kopyalanır → karşıya gönderin.
2. **Destek alan:** "Destek Al" → **Davet Kodunu Yapıştır** → onay verir → ekranda bir **YANIT** kodu çıkar → geri gönderir.
3. **Destek veren** yanıtı yapıştırır → doğrudan bağlantı kurulur.

Kod yalnızca yeniden üretilemeyen bilgileri taşır: DTLS parmak izi (tam 32 bayt — güvenlik kısaltılmadı), ICE kimliğinin türetildiği kısa bir tohum ve en fazla 5 adres.

> ℹ️ Dış IP'yi öğrenmek için ücretsiz servisler (ipify vb.) ve Google STUN kullanılır; **veri bu servislerden geçmez**. İki taraf da çok katı ağdaysa (symmetric NAT) doğrudan bağlantı kurulamaz; **Ayarlar → TURN** alanını kullanın.

---

## 🧰 Oturum içi araçlar (İşlemler ▾)

| Araç | Açıklama |
|---|---|
| **Dosya Gönder / sürükle-bırak** | Dosyaları pencereye bırakın. Bağlantı koparsa aktarım yeniden bağlanınca **kaldığı yerden** sürer. |
| **Panoyu Gönder / Al** | Metin panosu iki yönlü. |
| **Panoyu Güvenli Gönder** | Karşıda pano geçmişine yazılmaz, 30 sn sonra silinir (şifre vb. için). |
| **Onarım Paketi** | DNS önbelleği, geçici dosyalar, yazıcı kuyruğu, Gezgin, winsock, sfc. Bazıları karşı tarafta yönetici yetkisi ister. |
| **Uzak Sistem Bilgisi** | Bilgisayar, bellek, disk özeti. |
| **Ekran Görüntüsü (F12)** | İndirilenler klasörüne kaydeder. |
| **İşaretleme Modu** | Uzak ekrana çizim. |
| **Oturumu Video Kaydet** | `Videolar\NexDesk\*.avi` — karşı tarafa kayıt uyarısı gösterilir. |
| **Makrolar** | Kaydet → adlandır → listeden tek tıkla oynat. |
| **Ekranımı Karşı Tarafa Göster** | Sizin ekranınız karşıda ayrı pencerede, yalnızca izleme. |
| **Oturumu Devret** | Karşı taraf onaylarsa yeni kod üretilir; yeni teknisyen bağlanınca sizin oturumunuz kapanır. |
| **Bu Bilgisayara Not Ekle** | Not geçmişi, aynı bilgisayara bir sonraki bağlantıda otomatik gösterilir. |
| **Bağlantı Günlüğü** | Kopmalar, yeniden bağlanmalar, kalite değişimleri, SAS olayları. |
| **Gizlilik Perdesi / Giriş Kilidi** | Karşı ekranı karart / yerel girişi kilitle. |

---

## 📇 Bağlantılar sayfası (adres defteri)

Sol menüdeki **Bağlantılar** sayfası, oturum kurduğunuz her bilgisayarı otomatik kaydeder:

- 🔎 Ad, etiket veya bilgisayar adına göre arama
- 🏷️ Görünen ad ve etiketler (müşteri, şube, konum…)
- 📝 Bilgisayar başına tarihli not geçmişi
- ⏰ **Uyandır (WoL):** MAC adresi, o bilgisayar bir kez destek alan taraf olduğunda öğrenilir; aynı ağda kapalı makineyi açar.

---

## 🔐 Güvenlik felsefesi

NexDesk, "kötüye kullanılamayacak" bir destek aracı olacak şekilde tasarlandı:

- 🙅 **Sessiz/gizli bağlantı yok.** Her oturum karşı tarafın açık onayıyla başlar; gizli çalışma, kendiliğinden başlama ya da kalıcılık yoktur.
- 👁️ **Görünür durum.** Ekranı paylaşılan tarafta kalıcı "paylaşılıyorsunuz" uyarısı, kayıt sırasında "KAYIT ALINIYOR" işareti vardır.
- ✋ **Her an kesilebilir.** Tek tıkla bağlantı sonlanır.
- 🔑 **SAS doğrulaması.** Ortadaki-adam saldırılarına karşı; uyuşmazlıkta oturum otomatik kesilir.
- 🛡️ **Dolandırıcılık kalkanı.** Oturum sırasında banka, e-Devlet, kripto borsası veya ödeme sayfası öne gelirse görüntü ve uzaktan kontrol durur; kullanıcıya *"sizi arayan kişi kendini banka/polis olarak tanıttıysa bu dolandırıcılık olabilir"* uyarısıyla bağlantıyı kesme seçeneği sunulur (varsayılan: **kes**).
- 🧱 **Rol koruması.** Kontrol mesajları yalnızca doğru tarafta kabul edilir; destek alan taraf, teknisyenin bilgisayarını yönetemez ya da kilitleyemez.
- 🔒 **Güvenli masaüstüne saygı.** Ctrl+Alt+Del, UAC ve kilit ekranı sırasında paylaşım bekler, teknisyene bildirilir; kullanıcı kapatınca kendiliğinden devam eder.
- ⏱️ **Boşta kalma koruması.** Uzun süre işlem olmazsa uyarır (isteğe bağlı otomatik kesme).
- 📜 **Denetim kaydı.** Bağlantı, onay, onarım ve kayıt olayları yerel günlüğe yazılır.

---

## 🎨 Beyaz etiket (kurumsal marka)

NexDesk'i kendi firmanızın uygulamasıymış gibi dağıtabilirsiniz. Ayar klasörüne
(`%APPDATA%\RemoteSupport\`) bir `branding.json` bırakmanız yeterli:

```json
{
  "name": "Firma Destek",
  "tagline": "Bir tık uzağınızdayız",
  "author": "Firma A.Ş.",
  "accent": "2E90FF",
  "logo_path": "C:\\firma\\logo.png"
}
```

| Alan | Anlamı |
|------|--------|
| `name` | Uygulama adı (başlık, üst bar, hakkında). |
| `tagline` | Slogan. |
| `author` | Alt bilgide görünen "Program Yazarı". |
| `accent` | Vurgu rengi (6 haneli HEX, `#` opsiyonel). |
| `logo_path` | Özel PNG logo yolu (boşsa gömülü NexDesk logosu). |

Dosya yoksa varsayılan **NexDesk** kimliği kullanılır.

---

## 🗂️ Yerel veriler

Hepsi `%APPDATA%\RemoteSupport\` altında, yalnızca bu bilgisayarda tutulur:

| Dosya | İçerik |
|---|---|
| `settings.json` | Ayarlar (signaling, TURN, kalite, boşta kalma). |
| `addressbook.json` | Adres defteri, etiketler, notlar, MAC adresleri. |
| `recent.json` | Son bağlantılar. |
| `netlog.log` | Bağlantı / kesinti günlüğü (2 MB'da döner). |
| `audit.log` | Denetim kaydı. |
| `macros\*.json` | Kayıtlı makrolar. |
| `crash.log` | Yakalanan hatalar. |

Video kayıtları `Videolar\NexDesk\`, alınan dosyalar ve ekran görüntüleri `İndirilenler` klasörüne gider.

---

## 🧩 Mimari

```
┌───────────────┐   1) kod: yerel / internet / davet     ┌───────────────┐
│  Destek ALAN  │  ───────────────────────────────────▶  │ Destek VEREN  │
│   (target)    │                                         │  (operator)   │
│               │   2) tanışma: gömülü signaling (LAN,    │               │
│  ekranı yayar │      UPnP) ya da davet/yanıt kodu       │ ekranı görür  │
│               │                                         │               │
│               │   3) P2P WebRTC (DTLS, şifreli)         │               │
│  ctl │ file   │  ◀═════════════════════════════════▶    │  ekran │ ctl  │
│  screen ch.   │      görüntü · kontrol · dosya          │  file ch.     │
└───────────────┘                                         └───────────────┘
```

- **Signaling** yalnızca iki tarafı tanıştırır; oturum verisi taşımaz ve `NexDesk.exe` içine gömülüdür. Davet kodu yolunda hiç kullanılmaz.
- Kodlar **tek kullanımlık** ve **kısa ömürlüdür**.
- Ekran yakalama GDI `BitBlt` (imleç dahil), arayüz ham Win32 (owner-draw, çift tamponlu çizim) — ağır UI çatısı yok.

### 📂 Proje yapısı

```
RemoteSupport/
├─ cmd/
│  ├─ remotesupport/            # GUI istemci (ana uygulama)
│  │  ├─ app.go                 #   giriş noktası, pencere kurulumu
│  │  ├─ main.go                #   sabitler, Win32 bağları
│  │  ├─ session.go             #   oturum akışı (agent / operator)
│  │  ├─ network.go             #   ekran yayını, gömülü signaling, LAN keşfi
│  │  ├─ viewer.go              #   uzak ekran görüntüleyici
│  │  ├─ remote_input.go        #   kontrol mesajları, girdi enjeksiyonu, dosya alma
│  │  ├─ commands.go            #   menüler, komutlar, pencere yordamı
│  │  ├─ paint.go · ui_*.go     #   arayüz çizimi ve yerleşim
│  │  ├─ b1_*.go                #   onarım, SAS, pano, adres defteri, WoL, günlük
│  │  ├─ b2_*.go                #   kalkan, devir, karşılıklı ekran, kayıt, makro
│  │  ├─ b3_net.go              #   internet kodu + UPnP
│  │  ├─ b3_manual.go · b3_compact.go  # sunucusuz davet kodu
│  │  ├─ qr.go                  #   bağımsız QR kod üretici
│  │  └─ assets/                #   logo.png, nexdesk.ico
│  ├─ signaling/                # tek başına signaling sunucusu (opsiyonel)
│  └─ capture-check/            # ekran yakalama testi
├─ client/                      # capture · screen · signaling · webrtc
├─ server/signaling/            # rendezvous/signaling sunucu mantığı
├─ shared/                      # protokol · uçtan uca yardımcılar
└─ build.ps1                    # tek komutla derleme (ikon kaynağı dahil)
```

---

## 🤝 Katkı

Fikir, hata bildirimi ve PR'lara açığız! Bir hata mı buldunuz, bir özellik mi hayal ettiniz — issue açmaktan çekinmeyin.

---

## 📜 Lisans

MIT Lisansı altında dağıtılır. Dilediğiniz gibi kullanın, geliştirin, paylaşın.

---

<div align="center">

**Program Yazarı: Hasan Güler** · © 2026

*Uzaktan destek, daha kolay.* 💙

</div>
