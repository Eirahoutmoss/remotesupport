//go:build windows

package main

// Built-in server settings, embedded at build time. assets/server-defaults.json
// is the empty template kept in git; assets/server-defaults.local.json (git-
// ignored, holds the real TURN password) wins when present. Anything the user enters in
// Ayarlar overrides it; empty fields fall back to these defaults.

import (
	"embed"
	"encoding/json"
	"strings"
	"sync"

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

// iceServers: Google STUN + the user's TURN, or the built-in TURN.
func iceServers() []webrtc.ICEServer {
	ice := []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302", "stun:stun1.l.google.com:19302"}}}
	cfg := currentSettings()
	urls, user, pass := splitURLs(cfg.TurnURL), cfg.TurnUser, cfg.TurnPass
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
	return se
}
