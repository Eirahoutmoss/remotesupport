# Remote Support — Architecture (MVP-1)

Status: decision record for MVP-1. Product context: `REMOTE_SUPPORT_PRODUCT_CONTEXT.md`.

## 1. Existing code (kept)

| Package | Role | Notes |
|---|---|---|
| `shared/protocol` | Session message types (hello, approval, screen info, frame, input, select monitor, bye, ping) + `Encode/Decode`, `InputEvent.Validate` | Transport-agnostic. The `Ctl*` control types describe a custom rendezvous and become unused if §3 is adopted. |
| `shared/e2e` | App-layer encrypted channel: X25519 → HKDF-SHA256 → AES-256-GCM, one key per direction, counter nonces | Replay, reorder and tampering fail authentication, and after a failure the channel can't be used again. Has unit tests. |

Known limits of `shared/e2e`:
- The connection code is only used as HKDF salt. Whoever relays the handshake (signaling, relay) knows the code, so the code **does not authenticate** the peer.
- The 6-digit SAS is a **UX check**: both users see the same number only if nobody intercepted the key exchange. It is not an identity mechanism. Real authentication = explicit user consent + SAS comparison now; device identity keys (long-term Ed25519) or a PAKE bound to the code later (v1.x).

## 2. Options

1. **Custom transport** — our own WebSocket rendezvous + relay forwarding `e2e` ciphertext.
2. **WebRTC** — ICE/STUN/TURN for connectivity, DTLS for the channel, a small signaling service for SDP exchange, a standard TURN server as relay.

| Criterion | 1. Custom WS + relay | 2. WebRTC + TURN |
|---|---|---|
| Windows portable EXE | Pure Go, trivial | Pure Go with `pion/webrtc`, no CGO, single EXE |
| NAT traversal | None, always relayed | ICE: direct when possible, TURN otherwise |
| Outbound-only / proxy networks | WSS over 443 works | TURN over TLS/TCP 443 works; HTTP-proxy-only networks need TURN-TLS through CONNECT (to be tested) |
| Latency | Always via server, TCP head-of-line blocking | Direct path usually; UDP; unordered data channels possible |
| Screen streaming | JPEG/H.264 frames over TCP | JPEG over data channel now; H.264 media track later with congestion control |
| Security | Only our `e2e` layer, which we must audit alone | DTLS 1.2 (standard, audited) + our `e2e` layer inside |
| Relay sees content | No (e2e) | No (DTLS, + e2e) |
| Multi-session later | One WS per session | One `PeerConnection` per session |
| File transfer later | Must build flow control ourselves | Dedicated reliable data channel with built-in SCTP flow control |
| Clipboard later | Multiplex on one stream | Dedicated data channel |
| License | Own code + ISC websocket | pion: MIT; coturn: BSD-3 (or `pion/turn`: MIT) |
| Maintenance | We own relay, NAT, congestion, keepalive | We own signaling only; relay = off-the-shelf TURN |

## 3. Decision: WebRTC (pion) + standard TURN + existing `e2e` inside the data channel

Reasons:
- Direct paths where the network allows, and a standard, audited relay (TURN) where it doesn't. No custom relay protocol to write, secure and maintain.
- TURN only forwards DTLS packets, so it can't read session content. Our `e2e` layer runs **inside** the data channel, so even a compromised signaling service that swaps DTLS fingerprints still can't read content, and the SAS check stays valid.
- Pure Go and no CGO, so `GOOS=windows GOARCH=amd64` gives a single portable `RemoteSupport.exe`.
- File transfer, clipboard and chat map to separate data channels; multi-session maps to N peer connections.

What we don't do: no port scanning and no firewall, proxy or policy changes. If neither direct nor TURN works, diagnostics say so. We don't try to get around the network policy.

### Components

```
client (RemoteSupport.exe, one binary, two roles)
  agent     : consent dialog, capture, input, status + Disconnect
  operator  : code entry, viewer, session list (limit MAX_CONCURRENT_SESSIONS, default 4)
  webrtc    : PeerConnection, data channels "ctl" (reliable) / "screen" (unordered)
  e2e       : shared/e2e over the "ctl" and "screen" channels
server (self-hosted)
  signaling : short-lived code → SDP/ICE exchange only (no media)
  TURN      : coturn or pion/turn, TLS on 443, time-limited credentials
```

### Session limit

`MAX_CONCURRENT_SESSIONS` (default 4) is a config value (`remotesupport.json` next to the EXE, flag, or env), **not a license lock**. The operator keeps a `map[sessionID]*Session`; nothing assumes a single session. MVP-1 is tested with 1 session.

## 4. Windows screen capture

Behind a `Capturer` interface (`Monitors()`, `Capture(rect) → image`):

| API | Pros | Cons | MVP-1 |
|---|---|---|---|
| GDI `BitBlt` | Trivial via `syscall`, no CGO, works everywhere | CPU-heavy, no change detection | **Yes** (baseline) |
| DXGI Desktop Duplication | GPU, dirty rects, low CPU | COM/D3D11 via syscall, fails on some RDP/secure desktops | Benchmark next |
| Windows.Graphics.Capture | Modern, per-monitor/window, Win10 1903+; shows a yellow capture border (a visible consent signal) | WinRT activation + D3D11 interop from Go without CGO is the most complex; bindings are immature | Evaluate after DXGI |

Benchmarks per §23 (CPU, FPS, first-frame latency) decide which one replaces GDI.

## 5. UAC

The manifest uses `asInvoker`. The agent runs unelevated and offers a **"Restart as administrator"** button that relaunches through `ShellExecute("runas")`, which is the standard UAC prompt. We never bypass UAC. An unelevated agent can't control elevated windows (UIPI), and the UI says so.

## 6. MVP-1 slice and order

1. `shared/protocol`, `shared/e2e` + tests (done)
2. Signaling + TURN config; loopback test between two in-process peers
3. Agent: consent → e2e → GDI capture (single monitor) → JPEG over "screen"
4. Input: validated `InputEvent` → `SendInput`
5. Operator viewer + Disconnect on both sides
6. Windows x64 build, manual test on Win10/Win11

Next (MVP-2): multi-monitor, file transfer, clipboard, chat, diagnostics.
