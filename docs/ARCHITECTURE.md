# Remote Support — Architecture

Product context: `REMOTE_SUPPORT_PRODUCT_CONTEXT.md`.

## 0. Güncel güvenlik modeli (1.2.0) — ÖNCE BUNU OKUYUN

Bu dosyanın §1–§6'sı **MVP-1 karar kaydıdır** ve tarihsel amaçla duruyor. Gönderilen
sürüm bazı noktalarda o kayıttan ayrılır; bağlayıcı olan aşağıdaki özettir:

- **İçerik şifrelemesi = WebRTC DTLS.** Ekran, girdi, dosya ve sohbet pion/webrtc
  veri kanallarından geçer; bunlar DTLS 1.2 ile uçtan uca şifrelidir. TURN sunucusu
  yalnızca DTLS paketlerini aktarır, içeriği göremez.
- **Ortadaki adama karşı = SAS.** `client/webrtc.FingerprintSAS`, iki ucun DTLS
  sertifika parmak izlerinden ve bağlantı kodundan 6 haneli bir sayı türetir. İki
  tarafta aynıysa signaling'e kimse karışmamış demektir. Kullanıcılar bunu telefonda
  karşılaştırır (menüdeki "Güvenlik Kodunu Doğrula"). SAS bir kimlik mekanizması değil,
  araya girmeyi yakalayan bir kontroldür.
- **Signaling güveni = pinli wss + sürüm.** İstemci sunucuya `wss://…#pin=…` ile
  bağlanır ve yalnızca pini tutan sertifikayı kabul eder (`client/signaling`).
- **Ayrı bir uygulama-katmanı şifrelemesi YOKTUR.** Aşağıda §1–§3'te geçen `shared/e2e`
  katmanı (X25519/AES-GCM) hiçbir zaman veri kanalına bağlanmadı ve 1.2.0'da depodan
  kaldırıldı. Güvenlik, DTLS + SAS üzerine kuruludur. Daha güçlü kimlik (kod'a bağlı
  PAKE ya da uzun ömürlü cihaz anahtarları) hâlâ gelecekteki bir iş.

### 1.2.0'da paket değişiklikleri
- `shared/protocol` — artık yalnızca `InputEvent` + `Validate`. Eski oturum mesajları,
  `Encode/Decode` ve `Ctl*` kontrol tipleri gönderilen taşımada hiç kullanılmadığı için
  kaldırıldı.
- `shared/landisc` — LAN keşif protokolü (UDP 8090). Önce `cmd/signaling` ve
  `cmd/remotesupport` içinde iki kopyaydı; tek pakette birleştirildi.
- `shared/e2e` — kaldırıldı (yukarıya bakın).

---

# Aşağısı: MVP-1 karar kaydı (tarihsel)

Status: decision record for MVP-1.

## 1. Existing code (kept)

| Package | Role | Notes |
|---|---|---|
| `shared/protocol` | Session message types (hello, approval, screen info, frame, input, select monitor, bye, ping) + `Encode/Decode`, `InputEvent.Validate` | **1.2.0: slimmed to `InputEvent` + `Validate`.** The session message types, `Encode/Decode` and the `Ctl*` control types were never used by the shipped transport and were removed. |
| `shared/e2e` | App-layer encrypted channel: X25519 → HKDF-SHA256 → AES-256-GCM, one key per direction, counter nonces | **1.2.0: removed — never wired into the data channel.** Shipped content protection is WebRTC DTLS; see §0. |

Known limits (still accurate, now about the SAS/DTLS model):
- The connection code is only used as salt / SAS input. Whoever relays the handshake (signaling) knows the code, so the code **does not authenticate** the peer.
- The 6-digit SAS is a **UX check**: both users see the same number only if nobody intercepted the key exchange. It is not an identity mechanism. Real authentication = explicit user consent + SAS comparison now; device identity keys (long-term Ed25519) or a PAKE bound to the code later (v1.x).

## 2. Options

1. **Custom transport** — our own WebSocket rendezvous + relay forwarding ciphertext.
2. **WebRTC** — ICE/STUN/TURN for connectivity, DTLS for the channel, a small signaling service for SDP exchange, a standard TURN server as relay.

| Criterion | 1. Custom WS + relay | 2. WebRTC + TURN |
|---|---|---|
| Windows portable EXE | Pure Go, trivial | Pure Go with `pion/webrtc`, no CGO, single EXE |
| NAT traversal | None, always relayed | ICE: direct when possible, TURN otherwise |
| Outbound-only / proxy networks | WSS over 443 works | TURN over TLS/TCP 443 works; HTTP-proxy-only networks need TURN-TLS through CONNECT (to be tested) |
| Latency | Always via server, TCP head-of-line blocking | Direct path usually; UDP; unordered data channels possible |
| Screen streaming | JPEG/H.264 frames over TCP | JPEG over data channel now; H.264 media track later with congestion control |
| Security | A custom app-layer cipher we must audit alone | DTLS 1.2 (standard, audited); SAS verifies the DTLS fingerprints out-of-band |
| Relay sees content | No | No (DTLS) |
| Multi-session later | One WS per session | One `PeerConnection` per session |
| File transfer later | Must build flow control ourselves | Dedicated reliable data channel with built-in SCTP flow control |
| Clipboard later | Multiplex on one stream | Dedicated data channel |
| License | Own code + ISC websocket | pion: MIT; coturn: BSD-3 (or `pion/turn`: MIT) |
| Maintenance | We own relay, NAT, congestion, keepalive | We own signaling only; relay = off-the-shelf TURN |

## 3. Decision: WebRTC (pion) + standard TURN

Reasons:
- Direct paths where the network allows, and a standard, audited relay (TURN) where it doesn't. No custom relay protocol to write, secure and maintain.
- TURN only forwards DTLS packets, so it can't read session content. A signaling service that tried to swap DTLS fingerprints to intercept the channel changes the SAS, so the out-of-band SAS comparison detects it. (This is why the SAS check matters; there is no separate app-layer cipher — see §0.)
- Pure Go and no CGO, so `GOOS=windows GOARCH=amd64` gives a single portable `NexDesk.exe`.
- File transfer, clipboard and chat map to separate data channels; multi-session maps to N peer connections.

What we don't do: no port scanning and no firewall/proxy/policy circumvention. If neither direct nor TURN works, diagnostics say so. We don't try to get around the network policy.

### Components

```
client (NexDesk.exe, one binary, two roles)
  agent     : consent dialog, capture, input, status + Disconnect
  operator  : code entry, viewer, session list
  webrtc    : PeerConnection, data channels "ctl"/"file" (reliable) / "screen" (unordered)
  security  : DTLS 1.2 on every channel; FingerprintSAS compares DTLS fingerprints out-of-band
server (self-hosted)
  signaling : short-lived code → SDP/ICE exchange only (no media); wss + cert pin; per-session TURN credentials
  TURN      : coturn, time-limited credentials (use-auth-secret)
```

## 4. Windows screen capture

Behind a `Capturer` interface (`Monitors()`, `Capture(rect) → image`):

| API | Pros | Cons | Status |
|---|---|---|---|
| GDI `BitBlt` | Trivial via `syscall`, no CGO, works everywhere | CPU-heavy, no change detection in the API | **Shipped** (per-monitor DPI-aware; change detection done above it, 64×64 tiles) |
| DXGI Desktop Duplication | GPU, dirty rects, low CPU | COM/D3D11 via syscall, fails on some RDP/secure desktops | Possible future |
| Windows.Graphics.Capture | Modern, per-monitor/window; shows a yellow capture border | WinRT activation + D3D11 interop from Go without CGO is complex | Possible future |

## 5. UAC

The manifest uses `asInvoker`. The agent runs unelevated and offers a **"Restart as administrator"** button that relaunches through `ShellExecute("runas")`, the standard UAC prompt. We never bypass UAC. An unelevated agent can't control elevated windows (UIPI), and the UI says so. (A SYSTEM service for secure-desktop/UAC control was considered and deferred.)

## 6. Build order (historical)

1. ~~`shared/protocol`, `shared/e2e` + tests~~ → shipped as `shared/protocol` (InputEvent) only; discovery in `shared/landisc`
2. Signaling + TURN; loopback test between two in-process peers
3. Agent: consent → GDI capture → JPEG over "screen"
4. Input: validated `InputEvent` → `SendInput`
5. Operator viewer + Disconnect on both sides
6. Windows x64 build, manual test on Win10/Win11
