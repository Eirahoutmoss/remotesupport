//go:build windows

package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/eirahoutmoss/remotesupport/client/capture"
	"github.com/eirahoutmoss/remotesupport/client/screen"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	server "github.com/eirahoutmoss/remotesupport/server/signaling"
	"github.com/eirahoutmoss/remotesupport/shared/landisc"
	"golang.org/x/sys/windows"
)

func startCapture(peer *webrtcpeer.Peer, ctx context.Context) {
	go func() {
		defer logCrash("capture")
		defer clearPrivacy()
		if err := peer.WaitScreen(ctx); err != nil {
			if ctx.Err() == nil {
				setStatus("● Ekran kanalı bekleniyor: " + err.Error())
			}
			return
		}
		setStatus("● Ekran kanalı hazır. Ekran aktarılıyor...")
		// ~15 fps cap. Frames are produced only when the screen channel has
		// drained (ScreenIdle), and only changed 64x64 tiles are sent.
		ticker := time.NewTicker(66 * time.Millisecond)
		defer ticker.Stop()
		enc := &tileEncoder{}
		activeTiles.Store(enc)
		var seq uint64
		var cong congestion
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if fraudHold.Load() {
					continue // fraud shield: stop streaming until the user decides
				}
				busy := !peer.ScreenIdle()
				cong.tick(peer, busy)
				if busy {
					continue // link still busy with the previous frame
				}
				monitor := int(activeMonitor.Load())
				maxHeight := int(captureMaxHeight.Load())
				cfg := currentSettings()
				quality := cfg.Quality
				if quality == 0 {
					quality = 88
				}
				quality, maxHeight = adaptParams(quality, maxHeight)
				img, err := capture.CaptureMonitorImage(monitor, maxHeight)
				if err != nil {
					// Ctrl+Alt+Del / UAC / lock screen switch to the secure desktop,
					// where capture fails. Keep retrying instead of ending the stream.
					secureDesktopState(peer, true)
					enc.reset()
					continue
				}
				secureDesktopState(peer, false)
				payload, skip, err := enc.encode(img, monitor, quality)
				if skip || err != nil {
					continue
				}
				if len(payload) > screen.MaxPayloadSize {
					enc.reset()
					continue
				}
				seq++
				err = peer.SendScreenFrame(screen.Frame{
					Monitor: uint16(monitor),
					Seq:     seq,
					Width:   uint32(img.Bounds().Dx()),
					Height:  uint32(img.Bounds().Dy()),
					JPEG:    payload,
				})
				if err != nil {
					setStatus("● Ekran DataChannel gönderim hatası: " + err.Error())
					capturing.Store(false)
					return
				}
				// Bir kare fiilen bu makineden çıktı: agent artık görülüyor.
				if capturing.CompareAndSwap(false, true) && state.hwnd != 0 {
					invalidateRect.Call(state.hwnd, 0, 0)
				}
			}
		}
	}()
}

func startEmbeddedSignaling() {
	// Bind the port ourselves so we KNOW we own it. The old probe only checked
	// whether *something* answered on :8091, so a leftover/old RemoteSupport
	// instance holding the port made us use ITS (6-digit) signaling. Now we use
	// our own 10-digit server when we can bind, and warn clearly when we cannot.
	ln, err := net.Listen("tcp", "0.0.0.0:8091")
	if err != nil {
		log.Printf("Embedded signaling could not bind :8091: %v", err)
		setStatus("● 8091 portu meşgul: açık/eski bir NexDesk örneği olabilir. Onu kapatıp yeniden başlatın.")
		return
	}
	s := server.NewServer()
	embeddedSignalingReady.Store(true)
	go func() {
		if e := http.Serve(ln, s.Handler()); e != nil {
			log.Printf("Embedded signaling stopped: %v", e)
			embeddedSignalingReady.Store(false)
		}
	}()
	go landisc.Serve(log.Printf)
}

func signalingEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("REMOTESUPPORT_SIGNALING_URL")); v != "" {
		return v
	}
	cfg := currentSettings()
	if v := pickSignaling(cfg.SignalingURL); v != "" {
		return v
	}
	if v := builtinSignaling(); v != "" {
		return v
	}
	if embeddedSignalingReady.Load() {
		return "ws://127.0.0.1:8091/v1/ws"
	}
	if v := discoverSignalingEndpoint(1200 * time.Millisecond); v != "" {
		return v
	}
	return defaultSignalingURL
}

func discoverSignalingEndpoint(timeout time.Duration) string {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return ""
	}
	defer conn.Close()
	if raw, err := conn.SyscallConn(); err == nil {
		_ = raw.Control(func(fd uintptr) {
			_ = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_BROADCAST, 1)
		})
	}
	deadline := time.Now().Add(timeout)
	_ = conn.SetReadDeadline(deadline)
	msg := []byte(landisc.Request)
	for _, bcast := range discoveryBroadcasts() {
		_, _ = conn.WriteToUDP(msg, &net.UDPAddr{IP: bcast, Port: landisc.DiscoveryPort})
	}
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return ""
		}
		line := strings.TrimSpace(string(buf[:n]))
		if strings.HasPrefix(line, landisc.ResponsePrefix) {
			u := strings.TrimSpace(strings.TrimPrefix(line, landisc.ResponsePrefix))
			if parsed, err := url.Parse(u); err == nil && parsed.Scheme == "ws" && parsed.Host != "" {
				return u
			}
		}
	}
	return ""
}

func discoveryBroadcasts() []net.IP {
	result := []net.IP{net.IPv4bcast}
	ifaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			mask := ipnet.Mask
			if ip == nil || len(mask) != 4 {
				continue
			}
			b := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				b[i] = ip[i] | ^mask[i]
			}
			result = append(result, b)
		}
	}
	return result
}
