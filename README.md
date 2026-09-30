<div align="center">

<img src="cmd/remotesupport/assets/logo.png" alt="NexDesk" width="200"/>

# NexDesk

### Uzaktan destek, daha kolay.

Windows için **açık, güvenli ve tamamen kullanıcı onaylı** uzak masaüstü destek uygulaması.
Ara sunucuya muhtaç değil, reklam yok, arka planda casusluk yok — sadece iki bilgisayar, bir kod ve uçtan uca şifreli bir bağlantı.

<br/>

![Platform](https://img.shields.io/badge/platform-Windows-0A66C2?style=for-the-badge&logo=windows&logoColor=white)
![Go](https://img.shields.io/badge/Go-1.24-00ADD8?style=for-the-badge&logo=go&logoColor=white)
![WebRTC](https://img.shields.io/badge/WebRTC-P2P-333333?style=for-the-badge&logo=webrtc&logoColor=white)
![Lisans](https://img.shields.io/badge/lisans-MIT-26D07C?style=for-the-badge)
![Bağımlılık](https://img.shields.io/badge/kurulum-tek%20.exe-2E90FF?style=for-the-badge)

</div>

---

## 🎯 NexDesk nedir?

Birine uzaktan yardım etmek çoğu zaman şöyle başlar: *"Şu programı indir, şu kodu gir, şu izni ver, bu pencereyi kapat..."*

NexDesk bunu kısaltır:

> **Sen kodu ver → o kodu girer → sen "izin ver" dersin → bağlanırsınız.**

Görüntü de, klavye-fare de, dosyalar da doğrudan iki bilgisayar arasında (peer-to-peer) akar. Ortada verilerinizi gören bir şirket yoktur. Bağlantı kurulduğunda karşınıza çıkan **doğrulama kodunu (SAS)** karşı tarafla karşılaştırırsanız, araya kimsenin giremeyeceğinden de emin olursunuz.

---

## ✨ Öne çıkan özellikler

| | |
|---|---|
| 🔒 **Uçtan uca şifreli** | Görüntü ve kontrol WebRTC (DTLS/SRTP) ile şifrelenir. |
| ✅ **Her zaman onaylı** | Kimse sizin "İzin Ver" demeden ekranınıza bağlanamaz. |
| 🧭 **Ara sunucusuz P2P** | STUN ile doğrudan bağlantı; gerektiğinde TURN relay desteği. |
| 🌐 **LAN ve internet** | Aynı ağda otomatik keşif, internette 10 haneli kod + signaling. |
| 📱 **Karekod & link** | Bağlantıyı karekodla taratın ya da `nexdesk://` linkiyle paylaşın. |
| 🔁 **Otomatik yeniden bağlanma** | Ağ dalgalanınca oturum düşmez, kendiliğinden geri gelir. |
| 🖥️ **Çoklu monitör** | Karşı taraftaki tüm ekranlar arasında geçiş. |
| 🖊️ **Ekran işaretleme** | Uzak ekrana kırmızı kalemle çizip yol gösterin (çizimler solar). |
| 📁 **Dosya & pano aktarımı** | İki yönlü dosya gönderimi ve pano paylaşımı. |
| 🛡️ **Gizlilik perdesi** | Karşı tarafın ekranını yerelde karartıp mahremiyeti koruyun. |
| 🔐 **Yerel giriş kilidi** | Destek sırasında karşı tarafın klavye/faresini geçici kilitleyin. |
| 🎨 **Beyaz etiket** | İsmi, sloganı, rengi ve logoyu kendi markanıza göre değiştirin. |
| 🪶 **Tek dosya** | Kurulum yok. `NexDesk.exe` çalıştır, hazır. Signaling gömülü. |

---

## 🚀 Hızlı başlangıç

### İndir & çalıştır
`dist/NexDesk.exe` dosyasını çalıştırmanız yeterli. Kurulum, .NET, ek paket **yok**.

### Kaynaktan derleme
Go 1.24+ kurulu olmalı:

```powershell
powershell -ExecutionPolicy Bypass -File .\build.ps1
```

Çıktı: `dist\NexDesk.exe` (konsol penceresi olmayan, tek parça GUI uygulaması).

---

## 🕹️ Nasıl kullanılır?

<table>
<tr>
<td width="50%" valign="top">

### 🆘 Destek ALAN taraf
1. **"Destek Alıyorum"** kartına tıkla.
2. Ekranda beliren **kodu** (veya **karekodu / linki**) karşı tarafa ilet.
3. Bağlantı isteği gelince **"İzin Ver"** de.
4. Artık yardım eden kişi ekranını görüyor.

</td>
<td width="50%" valign="top">

### 🧑‍🔧 Destek VEREN taraf
1. **"Destek Veriyorum"** kartına tıkla.
2. Karşı tarafın **kodunu** ya da paylaştığı **`nexdesk://` linkini** yapıştır.
3. **"Bağlan"** de, onay bekle.
4. İşlemler menüsünden dosya gönder, ekranı işaretle, çözünürlüğü ayarla...

</td>
</tr>
</table>

> 💡 **Güvenlik ipucu:** Bağlandıktan sonra iki tarafta da görünen **SAS doğrulama kodu** birebir aynı olmalı. Aynıysa, bağlantınıza kimse müdahale etmemiş demektir.

---

## 📱 Karekod & bağlantı linki

Destek alan tarafta kod oluştuğunda ekranda bir **karekod** ve **"Bağlantı Linki"** butonu belirir:

```
nexdesk://join?ws=ws://192.168.1.50:8091/v1/ws&code=1234567890
```

- 📷 Karşı taraf telefon kamerasıyla karekodu taratabilir,
- 🔗 ya da linki NexDesk'e yapıştırıp tek adımda bağlanabilir (adres + kod otomatik dolar).

Karekod motoru tamamen uygulamanın içindedir; hiçbir dış servise kod gönderilmez.

---

## 🔁 Otomatik yeniden bağlanma

Wi-Fi bir an koptu, ağ değişti, VPN takıldı mı? Sorun değil.
NexDesk oturumu **sessizce kapatmaz** — açık signaling kanalı üzerinden yeni bir bağlantı pazarlığı yaparak **artan bekleme süreleriyle 6 denemeye kadar** kendiliğinden geri döner. Sadece siz **"Bağlantıyı Kes"** derseniz kapanır.

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

## 🧩 Mimari

```
┌───────────────┐        1) kod / karekod / link         ┌───────────────┐
│  Destek ALAN  │  ───────────────────────────────────▶  │ Destek VEREN  │
│   (target)    │                                         │  (operator)   │
│               │        2) signaling (kısa süreli)       │               │
│  ekranı yayar │  ◀────── STUN/TURN ile eşleşme ──────▶  │ ekranı görür  │
│               │                                         │               │
│               │  3) P2P WebRTC (DTLS/SRTP, şifreli)     │               │
│  ctl │ file   │  ◀═════════════════════════════════▶    │  ekran │ ctl  │
│  screen ch.   │        görüntü · kontrol · dosya        │  file ch.     │
└───────────────┘                                         └───────────────┘
```

- **Signaling** yalnızca iki tarafı tanıştırır; oturum verisi taşımaz ve `NexDesk.exe` içine gömülüdür (harici DOS penceresi yok).
- Kod **tek kullanımlık** ve **kısa ömürlüdür**; internet için 10 hanelidir.
- Ekran yakalama GDI `BitBlt`, arayüz ham Win32 (owner-draw) ile çizilir — hiçbir ağır UI çatısı yoktur.

### 📂 Proje yapısı

```
RemoteSupport/
├─ cmd/
│  ├─ remotesupport/      # GUI istemci (ana uygulama)
│  │  ├─ main.go          #   arayüz + oturum akışı
│  │  ├─ qr.go            #   bağımsız QR kod üretici
│  │  └─ assets/logo.png  #   gömülü logo
│  ├─ signaling/          # tek başına signaling sunucusu (opsiyonel)
│  └─ capture-check/      # ekran yakalama testi
├─ client/                # capture · screen · signaling · webrtc
├─ server/signaling/      # rendezvous/signaling sunucu mantığı
├─ shared/                # protokol · uçtan uca yardımcılar
└─ build.ps1              # tek komutla derleme
```

---

## 🔐 Güvenlik felsefesi

NexDesk, "kötüye kullanılamayacak" bir destek aracı olacak şekilde tasarlandı:

- 🙅 **Sessiz/gizli bağlantı yok.** Her oturum karşı tarafın açık onayıyla başlar.
- 👁️ **Görünür durum.** Ekranı paylaşılan tarafta kalıcı "paylaşılıyorsunuz" uyarısı vardır.
- ✋ **Her an kesilebilir.** Tek tıkla bağlantı sonlanır.
- 🔑 **SAS doğrulaması.** Ortadaki-adam saldırılarına karşı iki tarafın karşılaştırdığı kod.
- ⏱️ **Boşta kalma koruması.** Uzun süre işlem olmazsa uyarır (sistemciler için "Açık Kal" seçeneğiyle).

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
