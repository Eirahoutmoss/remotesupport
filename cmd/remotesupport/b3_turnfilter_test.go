//go:build windows

package main

import (
	"net"
	"testing"
)

func TestKeepTURN(t *testing.T) {
	_, lan, _ := net.ParseCIDR("192.168.1.10/24")
	local := []*net.IPNet{lan}
	cases := map[string]bool{
		"turn:198.51.100.10:3478?transport=udp": true,
		"turn:10.0.0.39:3478":                    false,
		"turn:192.168.1.5:3478":                  true,
		"turns:turn.example.com:443":             true,
		"turn:100.64.1.1:3478":                   false,
	}
	for u, want := range cases {
		if got := keepTURN(u, local); got != want {
			t.Errorf("%s: got %v want %v", u, got, want)
		}
	}
}
