// Package landisc is the LAN discovery wire shared by the standalone signaling
// server (cmd/signaling) and the signaling embedded in the app
// (cmd/remotesupport). A peer that is looking for a signaling host broadcasts
// Request on UDP DiscoveryPort; whoever is running signaling answers with
// ResponsePrefix followed by its own ws:// URL as seen from that peer.
//
// Keeping both the responder loop and LocalIPv4ForPeer here removes the copy
// that previously lived in each command.
package landisc

import (
	"fmt"
	"net"
	"time"
)

const (
	DiscoveryPort  = 8090
	SignalingPort  = 8091
	Request        = "REMOTESUPPORT_DISCOVER_V1"
	ResponsePrefix = "REMOTESUPPORT_SIGNALING_V1 "
)

// ResponseFor builds the discovery reply advertising ip's signaling endpoint.
func ResponseFor(ip string) string {
	return fmt.Sprintf("%sws://%s:%d/v1/ws", ResponsePrefix, ip, SignalingPort)
}

// Serve listens on UDP DiscoveryPort and answers each discovery request with
// this host's signaling ws:// URL as seen from the requesting peer. It blocks,
// so run it in a goroutine. logf (may be nil) reports why it could not start.
func Serve(logf func(string, ...any)) {
	pc, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", DiscoveryPort))
	if err != nil {
		if logf != nil {
			logf("LAN keşfi başlatılamadı: %v", err)
		}
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
		if string(trimSpace(buf[:n])) != Request {
			continue
		}
		ip := LocalIPv4ForPeer(peer)
		if ip == "" {
			continue
		}
		_, _ = pc.WriteTo([]byte(ResponseFor(ip)), peer)
	}
}

// trimSpace trims ASCII whitespace without importing strings.
func trimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && b[i] <= ' ' {
		i++
	}
	for j > i && b[j-1] <= ' ' {
		j--
	}
	return b[i:j]
}

// LocalIPv4ForPeer returns this host's IPv4 address on the interface that can
// reach peer: an address equal to the peer's, or one whose subnet contains it.
func LocalIPv4ForPeer(peer net.Addr) string {
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
