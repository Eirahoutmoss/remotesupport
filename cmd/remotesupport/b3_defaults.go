//go:build windows

package main

// Built-in server settings, embedded at build time. assets/server-defaults.json
// is the empty template kept in git; assets/server-defaults.local.json (git-
// ignored, holds the real TURN password) wins when present. Anything the user enters in
// Ayarlar overrides it; empty fields fall back to these defaults.

import (
	"embed"
	"encoding/json"
	"net"
	"strings"
	"sync"

	signaling "github.com/eirahoutmoss/remotesupport/server/signaling"
	"github.com/pion/webrtc/v4"
)

//go:embed assets/server-defaults*.json
var serverDefaultsFS embed.FS

type serverDefaults struct {
	SignalingURL string `json:"signaling_url"`
	TurnURL      string `json:"turn_url"`
	TurnUser     string `json:"turn_user"`
	TurnPass     string `json:"turn_pass"`
}

var (
	defOnce sync.Once
	defVal  serverDefaults
)

func builtinServer() serverDefaults {
	defOnce.Do(func() {
		for _, name := range []string{"assets/server-defaults.local.json", "assets/server-defaults.json"} {
			if b, err := serverDefaultsFS.ReadFile(name); err == nil && json.Unmarshal(b, &defVal) == nil {
				return
			}
		}
	})
	return defVal
}

// sessionTURN holds the short-lived TURN credential the signaling server
// issued for the current session (protocol v2 "turncred"); nil otherwise.
var (
	sessionTURNMu sync.Mutex
	sessionTURN   *signaling.ICECreds
)

func setSessionTURN(c *signaling.ICECreds) {
	sessionTURNMu.Lock()
	sessionTURN = c
	sessionTURNMu.Unlock()
}

func getSessionTURN() *signaling.ICECreds {
	sessionTURNMu.Lock()
	defer sessionTURNMu.Unlock()
	return sessionTURN
}

// iceServers: Google STUN + TURN, chosen in this order: the user's own TURN
// from Ayarlar, the per-session credential from the signaling server, and
// finally the static built-in TURN (only for servers that predate "turncred").
func iceServers() []webrtc.ICEServer {
	ice := []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302", "stun:stun1.l.google.com:19302"}}}
	cfg := currentSettings()
	urls, user, pass := splitURLs(cfg.TurnURL), cfg.TurnUser, cfg.TurnPass
	if len(urls) == 0 {
		if st := getSessionTURN(); st != nil && len(st.URLs) > 0 {
			urls, user, pass = reachableTURN(st.URLs), st.Username, st.Credential
		}
	}
	if len(urls) == 0 {
		d := builtinServer()
		urls, user, pass = splitURLs(d.TurnURL), d.TurnUser, d.TurnPass
	}
	if len(urls) > 0 {
		srv := webrtc.ICEServer{URLs: urls}
		if strings.TrimSpace(user) != "" {
			srv.Username, srv.Credential, srv.CredentialType = user, pass, webrtc.ICECredentialTypePassword
		}
		ice = append(ice, srv)
	}
	return ice
}

// builtinSignaling returns a reachable built-in signaling URL, or "" so the
// caller can fall back to the embedded LAN signaling / discovery.
func builtinSignaling() string {
	d := builtinServer()
	if len(splitURLs(d.SignalingURL)) == 0 {
		return ""
	}
	return reachableSignaling(d.SignalingURL)
}

// peerSettings: a larger SCTP receive window. Data-channel throughput is
// roughly window/RTT; pion's 1 MiB default caps a ~150 ms relayed path at a
// few MB/s, which is what made the screen sluggish over TURN.
func peerSettings() webrtc.SettingEngine {
	var se webrtc.SettingEngine
	se.SetSCTPMaxReceiveBufferSize(8 << 20)
	// Drop virtual/VPN adapters from ICE. Machines often have many (WSL,
	// VirtualBox, Hyper-V, VPNs) whose host candidates can't reach the peer but
	// bloat the candidate list and, together with TURN, stop ICE from ever
	// completing a check. Keeping only real NICs lets the direct (host) path
	// work again while TURN relay stays as the fallback.
	se.SetInterfaceFilter(keepInterface)
	return se
}

var ifaceLogged sync.Map

func keepInterface(name string) bool {
	n := strings.ToLower(name)
	bad := []string{"vethernet", "virtualbox", "vmware", "hyper-v", "loopback",
		"bluetooth", "tap-win", "tailscale", "zerotier", "docker", "wsl", "vpn",
		"radmin", "hamachi", "npcap", "pseudo", "teredo", "isatap", "vbox",
		"nordlynx", "wintun", "openvpn", "wireguard"}
	for _, b := range bad {
		if strings.Contains(n, b) {
			if _, dup := ifaceLogged.LoadOrStore("-"+name, true); !dup {
				netlogf("ICE: sanal arayüz atlandı: %s", name)
			}
			return false
		}
	}
	if _, dup := ifaceLogged.LoadOrStore(name, true); !dup {
		netlogf("ICE: arayüz kullanılıyor: %s", name)
	}
	return true
}

// reachableTURN drops TURN URLs that point at a private address outside every
// local subnet (e.g. a cloud server's internal IP handed out for in-house
// technicians). Such an address cannot answer, and pion waits ~8 s for it
// before ICE gathering completes — on both ends.
func reachableTURN(urls []string) []string {
	var local []*net.IPNet
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				local = append(local, n)
			}
		}
	}
	var out []string
	for _, u := range urls {
		if keepTURN(u, local) {
			out = append(out, u)
		} else {
			netlogf("TURN adresi atlandı (bu ağdan ulaşılamayan iç adres): %s", u)
		}
	}
	if len(out) == 0 {
		return urls // never leave the session without TURN because of the filter
	}
	return out
}

func keepTURN(u string, local []*net.IPNet) bool {
	rest := u
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		rest = rest[i+1:] // drop "turn:" / "turns:"
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[:i]
	}
	host := rest
	if h, _, err := net.SplitHostPort(rest); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !(ip.IsPrivate() || (ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xC0 == 64)) {
		return true // public address or hostname
	}
	for _, n := range local {
		if n.Contains(ip) {
			return true // we are on that network
		}
	}
	return false
}
