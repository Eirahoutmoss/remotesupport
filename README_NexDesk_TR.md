# NexDesk

**NexDesk**, Windows üzerinde uzaktan destek ve uzak masaüstü bağlantısı sağlamak için geliştirilen, hafif ve taşınabilir bir uygulamadır.

> **Program Yazarı:** Hasan Güler

> **NexDesk**, projenin kullanıcıya görünen ürün adıdır. Kaynak kodundaki `remotesupport` paket ve klasör adları mevcut geliştirme yapısı korunurken daha sonra `nexdesk` adına taşınabilir.

## İçindekiler

- [Özellikler](#özellikler)
- [Nasıl Çalışır](#nasıl-çalışır)
- [Mimari](#mimari)
- [Gereksinimler](#gereksinimler)
- [Kurulum](#kurulum)
- [PowerShell ile Derleme](#powershell-ile-derleme)
- [Kullanım](#kullanım)
- [Yerel Ağ Kullanımı](#yerel-ağ-kullanımı)
- [İnternet Üzerinden Kullanım](#internet-üzerinden-kullanım)
- [Ayarlar](#ayarlar)
- [Güvenlik](#güvenlik)
- [Geliştirici Kurulumu](#geliştirici-kurulumu)
- [Testler](#testler)
- [Proje Yapısı](#proje-yapısı)
- [Sorun Giderme](#sorun-giderme)
- [Proje Durumu](#proje-durumu)
- [Lisans](#lisans)
- [Yazar](#yazar)

## Özellikler

- Windows 10/11 ve Windows x64 desteği
- Taşınabilir EXE, temel kullanımda kurulum gerektirmez
- Destek alma / destek verme
- Kısa bağlantı kodu ile eşleştirme
- Bağlantı öncesi kullanıcı onayı
- WebRTC tabanlı bağlantı
- STUN/TURN mimarisi
- Uzak ekran aktarımı
- Çoklu monitör ve monitör seçimi
- Tam ekran viewer
- Çözünürlük ve görüntü kalitesi seçenekleri
- Mouse ve klavye kontrolü
- Ctrl + yön tuşları gibi klavye kombinasyonları
- Clipboard desteği
- Dosya aktarımı
- Bağlantı durumu takibi
- LAN signaling
- Ayarlar ve Hakkında ekranları
- Koyu temalı masaüstü arayüzü
- CGO gerektirmeyen Windows derlemesi

## Nasıl Çalışır

Temel akış:

```text
Destek veren
     │
     ▼
RemoteSupport
     │
     ▼
Bağlantı kodu
     │
     ▼
Destek alan
     │
     ▼
Kodu gir
     │
     ▼
Bağlantı isteği
     │
     ▼
Kullanıcı onayı
     │
     ▼
WebRTC
     │
     ▼
Uzak masaüstü
```

Signaling, istemcilerin eşleştirilmesi ve WebRTC bağlantısının kurulması için gereken SDP/ICE bilgilerinin aktarılmasını sağlar. Ekran, mouse, klavye, clipboard ve dosya gibi oturum verileri WebRTC üzerinden taşınır.

## Mimari

```text
                       Signaling
                     WSS / WebSocket
                           │
              ┌────────────┴────────────┐
              │                         │
       RemoteSupport              RemoteSupport
        Destek Veren                Destek Alan
              │                         │
              └────────── WebRTC ───────┘
                           │
                  ┌────────┴────────┐
                  │                 │
              Doğrudan          TURN Relay
              bağlantı          gerektiğinde
```

### Signaling

Signaling katmanı:

- Oturum oluşturur
- Bağlantı kodunu yönetir
- İstemcileri eşleştirir
- Bağlantı isteğini ve onayı iletir
- SDP / ICE bilgilerini aktarır
- Keepalive ve oturum kapatma işlemlerini yönetir

Normal çalışma sırasında uzak ekran görüntüsünün signaling sunucusundan geçirilmesi hedeflenmez.

### WebRTC

WebRTC gerçek uzak oturum için kullanılır. Doğrudan bağlantı mümkün olduğunda istemciler birbirine bağlanır, mümkün olmadığında TURN relay kullanılabilir.

## Gereksinimler

### Kullanıcı

- Windows 10 veya Windows 11
- Windows x64
- Ağ bağlantısı

Derlenmiş `NexDesk.exe` için Go kurulumu gerekmez.

### Geliştirici

- Windows 10/11
- Go 1.26.4 veya `go.mod` tarafından belirtilen uyumlu sürüm
- PowerShell
- Git

## Kurulum

Kaynak kodunu alın:

```powershell
git clone https://github.com/Eirahoutmoss/remotesupport.git
cd remotesupport
```

Bağımlılıkları indirin:

```powershell
go mod download
```

Derlenmiş EXE'yi aldıysanız ayrıca Go kurulmasına gerek yoktur. `NexDesk.exe` doğrudan çalıştırılabilir.

## PowerShell ile Derleme

### Release derlemesi

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass

$env:CGO_ENABLED="0"
$env:GOOS="windows"
$env:GOARCH="amd64"

go build -trimpath -ldflags="-s -w" -o NexDesk.exe .\cmd\remotesupport
```

Başarılı derleme sonunda:

```text
NexDesk.exe
```

oluşur.

Çalıştırmak için:

```powershell
.\NexDesk.exe
```

### Debug derlemesi

```powershell
$env:CGO_ENABLED="0"
$env:GOOS="windows"
$env:GOARCH="amd64"

go build -o NexDesk.exe .\cmd\remotesupport
```

### Build script

Projede `build.ps1` bulunuyorsa:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
.\build.ps1
```

## Kullanım

### Destek veren taraf

1. `NexDesk.exe` çalıştırılır.
2. **Destek Ver** seçilir.
3. Bağlantı kodu oluşturulur.
4. Kod karşı kullanıcıya iletilir.
5. Gelen bağlantı isteği kabul edilir.
6. WebRTC bağlantısı kurulur.
7. Uzak masaüstü görüntülenir.

### Destek alan taraf

1. `NexDesk.exe` çalıştırılır.
2. **Destek Al** seçilir.
3. Karşı tarafın bağlantı kodu girilir.
4. **Bağlan** seçilir.
5. Karşı tarafın onayı beklenir.
6. Oturum başlar.

## Yerel Ağ Kullanımı

Aynı LAN üzerindeki bilgisayarlarda ayrı bir internet sunucusu gerekmeyebilir.

```text
PC A
 │
 │ LAN
 ▼
Signaling
 │
 ▼
PC B
```

Mevcut LAN signaling yapısında kullanılan varsayılan portlar:

```text
TCP 8091
UDP 8090
```

WebSocket endpoint:

```text
ws://127.0.0.1:8091/v1/ws
```

## İnternet Üzerinden Kullanım

Farklı internet bağlantılarındaki istemcilerin buluşabilmesi için ortak erişilebilir bir signaling endpoint gerekir.

```text
RemoteSupport
      │
      │ WSS :443
      ▼
Public Signaling Server
      │
      │ SDP / ICE
      ▼
RemoteSupport
      │
      └──── WebRTC ────► diğer istemci
                         │
                    gerekirse TURN
```

Üretim ortamında hedeflenen yapı:

- WSS signaling
- STUN
- Doğrudan WebRTC bağlantısı
- Gerekirse TURN relay
- Kısıtlı ağlarda TCP/TLS 443 fallback

## Ayarlar

Ayarlar ekranı uygulamanın bağlantı ve görüntü davranışını yönetmek için kullanılır.

Örnek ayarlar:

- Signaling adresi
- Görüntü kalitesi
- Çözünürlük
- Tam ekran davranışı
- Bağlantı seçenekleri
- Görüntü aktarım tercihleri

## Güvenlik

Temel bağlantı akışı:

```text
Bağlantı kodu
     ↓
Eşleştirme
     ↓
Açık kullanıcı onayı
     ↓
WebRTC / DTLS
     ↓
Uzak oturum
```

Projede uygulama seviyesinde şifreli kanal için:

```text
X25519
   ↓
HKDF-SHA256
   ↓
AES-256-GCM
```

kullanılan yapı bulunmaktadır.

Altı haneli bağlantı kodu kalıcı parola veya cihaz kimliği değildir. Oturum eşleştirmesi ve kullanıcı doğrulaması için kullanılan kısa süreli bir mekanizmadır.

## Geliştirici Kurulumu

```powershell
git clone https://github.com/Eirahoutmoss/remotesupport.git
cd remotesupport
go mod download
```

Kod biçimlendirme:

```powershell
gofmt -w .\cmd\remotesupport\*.go
```

Derleme:

```powershell
$env:CGO_ENABLED="0"
$env:GOOS="windows"
$env:GOARCH="amd64"

go build -o NexDesk.exe .\cmd\remotesupport
```

## Signaling Sunucusu

Standalone signaling bileşeni projede mevcutsa:

```powershell
$env:REMOTESUPPORT_SIGNALING_ADDR="127.0.0.1:8091"
go run .\cmd\signaling
```

Endpoint:

```text
ws://127.0.0.1:8091/v1/ws
```

İnternet üretim ortamında WSS kullanılmalıdır.

## Testler

Tüm testler:

```powershell
go test ./...
```

Ayrıntılı çıktı:

```powershell
go test -v ./...
```

## Proje Yapısı

```text
remotesupport/
│
├── cmd/
│   ├── remotesupport/
│   ├── signaling/
│   └── capture-check/
│
├── capture/
├── signaling/
├── protocol/
├── e2e/
├── docs/
├── build.ps1
├── go.mod
├── go.sum
└── README.md
```

Dosya yapısı geliştirme sürecinde değişebilir.

## Sorun Giderme

### Go sürümü

```powershell
go version
Get-Content .\go.mod
```

### PowerShell script engeli

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
```

### 8091 portunu kontrol etme

```powershell
Get-NetTCPConnection -LocalPort 8091 -ErrorAction SilentlyContinue
```

### Uzak ekran görünmüyorsa

1. Bağlantının kurulduğunu doğrulayın.
2. Hedef monitörü kontrol edin.
3. Çözünürlüğü düşürerek tekrar deneyin.
4. Windows ekran yakalama izinlerini kontrol edin.
5. Aynı bilgisayarda yapılan testlerde loopback davranışını ayrıca değerlendirin.

### Klavye çalışmıyorsa

Özellikle şu kombinasyonları test edin:

- Ctrl
- Shift
- Alt
- Windows tuşu
- yön tuşları
- Ctrl + yön
- Home / End
- PageUp / PageDown

Hedef uygulama yönetici yetkisiyle çalışıyorsa RemoteSupport'un da gerekli yetkilerle çalıştırılması gerekebilir.

## Proje Durumu

### Mevcut

- Windows uzak destek istemcisi
- WebRTC bağlantısı
- Signaling
- Uygulama katmanı şifreleme
- Ekran yakalama ve aktarımı
- Çoklu monitör
- Monitör seçimi
- Mouse kontrolü
- Klavye kontrolü
- Clipboard
- Dosya aktarımı
- LAN signaling
- Çözünürlük seçenekleri
- Görüntü kalitesi seçenekleri
- Tam ekran viewer
- Ayarlar
- Hakkında
- Taşınabilir Windows EXE

### Geliştirme aşamasında

- Genel internet signaling altyapısı
- Üretim WSS endpoint'i
- STUN/TURN dağıtımı
- Bağlantı tanılama
- Viewer performans optimizasyonları
- Arayüz iyileştirmeleri
- Otomatik güncelleme

## Tasarım İlkeleri

- **Basit:** Kullanıcı ağ uzmanı olmadan bağlantı kurabilmeli.
- **Taşınabilir:** Temel istemci tek Windows EXE olarak çalışabilmeli.
- **Kullanıcı onayı:** Hedef kullanıcı onayı olmadan uzak oturum başlatılmamalı.
- **Doğrudan bağlantı:** Mümkün olduğunda WebRTC P2P tercih edilmeli.
- **Minimum altyapı:** Signaling yalnızca gerekli koordinasyonu sağlamalı.
- **Relay gerektiğinde:** TURN yalnızca doğrudan bağlantı mümkün olmadığında kullanılmalı.
- **Ağ politikalarına saygı:** Uygulama firewall, proxy veya kurum politikalarını gizlice aşmaya çalışmamalı.

## Lisans

Proje lisans bilgisi henüz kesinleştirilmemiştir.

Lisans dosyası eklenene kadar kaynak kod:

**All Rights Reserved**

olarak değerlendirilmelidir.

## Yazar

**Hasan Güler**

NexDesk Program Yazarı ve Geliştiricisi.

## Katkıda Bulunma

```powershell
git checkout -b feature/yeni-ozellik

go test ./...

git add .
git commit -m "Yeni özellik eklendi"
git push origin feature/yeni-ozellik
```

Ardından GitHub üzerinden Pull Request oluşturulabilir.

## Temel Teknolojiler

- Go
- WebRTC
- Pion WebRTC
- WebSocket
- Windows API
- Windows GDI
- STUN
- TURN
- X25519
- HKDF-SHA256
- AES-256-GCM

Bağımlılıkların tam listesi ve sürümleri `go.mod` içerisinde tutulur.
