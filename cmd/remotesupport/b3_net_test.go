//go:build windows

package main

import (
	"net"
	"testing"
)

func TestInternetCodeRoundTrip(t *testing.T) {
	ic, ok := encodeInternetCode(net.ParseIP("81.2.69.160"), 8093, "5914905011")
	if !ok || len(ic) != 23 {
		t.Fatalf("encode: %q %v", ic, ok)
	}
	ws, code, ok := decodeInternetCode(" " + ic + " ")
	if !ok || ws != "ws://81.2.69.160:8093/v1/ws" || code != "5914905011" {
		t.Fatalf("decode: %q %q %v", ws, code, ok)
	}
	bad := []byte(ic)
	if bad[0] == 'A' {
		bad[0] = 'B'
	} else {
		bad[0] = 'A'
	}
	if _, _, ok := decodeInternetCode(string(bad)); ok {
		t.Fatal("typo not detected")
	}
	if _, _, ok := decodeInternetCode("5914905011"); ok {
		t.Fatal("plain code parsed as internet code")
	}
}
