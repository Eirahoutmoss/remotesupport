package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	server "github.com/eirahoutmoss/remotesupport/server/signaling"
)

const discoveryPort = 8090
const discoveryRequest = "REMOTESUPPORT_DISCOVER_V1"
const discoveryResponsePrefix = "REMOTESUPPORT_SIGNALING_V1 "

func main() {
	addr := os.Getenv("REMOTESUPPORT_SIGNALING_ADDR")
	if addr == "" {
		addr = "0.0.0.0:8091"
	}

	s := server.NewServer()

	mux := s.Handler()

	log.Printf("RemoteSupport signaling listening on http://%s", addr)
	log.Printf("WebSocket endpoint: ws://%s/v1/ws", addr)

	go serveDiscovery()

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func serveDiscovery() {
	pc, err := net.ListenPacket("udp4", ":8090")
	if err != nil {
		log.Printf("LAN discovery disabled: %v", err)
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
		if strings.TrimSpace(string(buf[:n])) != discoveryRequest {
			continue
		}
		ip := localIPv4ForPeer(peer)
		if ip == "" {
			continue
		}
		_, _ = pc.WriteTo([]byte(discoveryResponsePrefix+"ws://"+ip+":8091/v1/ws"), peer)
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
			if udpPeer.IP.Equal(ip) {
				return ip.String()
			}
			if ipnet.Contains(udpPeer.IP) {
				return ip.String()
			}
		}
	}
	return ""
}
