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
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var seq uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if fraudHold.Load() {
					continue // fraud shield: stop streaming until the user decides
				}
				monitor := int(activeMonitor.Load())
				maxHeight := int(captureMaxHeight.Load())
				cfg := currentSettings()
				quality := cfg.Quality
				if quality == 0 {
					quality = 88
				}
				quality, maxHeight = adaptParams(quality, maxHeight)
				frame, err := capture.CaptureMonitorScaled(monitor, quality, maxHeight)
				if err != nil {
					// Ctrl+Alt+Del / UAC / lock screen switch to the secure desktop,
					// where capture fails. Keep retrying instead of ending the stream.
					secureDesktopState(peer, true)
					continue
				}
				secureDesktopState(peer, false)
				if len(frame.JPEG) > screen.MaxPayloadSize {
					fallbackQuality := 76
					if quality < 80 {
						fallbackQuality = quality
					}
					frame, err = capture.CaptureMonitorScaled(monitor, fallbackQuality, maxHeight)
					if err != nil {
						continue
					}
				}
				seq++
				err = peer.SendScreenFrame(screen.Frame{
					Monitor: uint16(monitor),
					Seq:     seq,
					Width:   uint32(frame.Width),
					Height:  uint32(frame.Height),
					JPEG:    frame.JPEG,
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
	go serveEmbeddedDiscovery()
}

func serveEmbeddedDiscovery() {
	pc, err := net.ListenPacket("udp4", ":8090")
	if err != nil {
		log.Printf("Embedded LAN discovery unavailable: %v", err)
		return
	}
	defer pc.Close()
	buf := make([]byte, 256)
	for {
		_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, peer, err := pc.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			continue
		}
		if strings.TrimSpace(string(buf[:n])) != "REMOTESUPPORT_DISCOVER_V1" {
			continue
		}
		ip := localIPv4ForPeer(peer)
		if ip == "" {
			continue
		}
		_, _ = pc.WriteTo([]byte("REMOTESUPPORT_SIGNALING_V1 ws://"+ip+":8091/v1/ws"), peer)
	}
}

func localIPv4ForPeer(peer net.Addr) string {
	udpPeer, ok := peer.(*net.UDPAddr)
	if !ok {
		return ""
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
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
			if ip == nil {
				continue
			}
			if ip.Equal(udpPeer.IP) || ipnet.Contains(udpPeer.IP) {
				return ip.String()
			}
		}
	}
	return ""
}

func signalingEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("REMOTESUPPORT_SIGNALING_URL")); v != "" {
		return v
	}
	cfg := currentSettings()
	if v := strings.TrimSpace(cfg.SignalingURL); v != "" {
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
	msg := []byte("REMOTESUPPORT_DISCOVER_V1")
	for _, bcast := range discoveryBroadcasts() {
		_, _ = conn.WriteToUDP(msg, &net.UDPAddr{IP: bcast, Port: 8090})
	}
	buf := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return ""
		}
		line := strings.TrimSpace(string(buf[:n]))
		if strings.HasPrefix(line, "REMOTESUPPORT_SIGNALING_V1 ") {
			u := strings.TrimSpace(strings.TrimPrefix(line, "REMOTESUPPORT_SIGNALING_V1 "))
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
