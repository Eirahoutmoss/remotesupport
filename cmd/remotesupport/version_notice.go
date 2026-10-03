//go:build windows

package main

import (
	"strconv"
	"strings"
	"sync"
	"unsafe"

	clientsignaling "github.com/eirahoutmoss/remotesupport/client/signaling"
)

var versionNoticeOnce sync.Once

// versionNotice compares this build's appVersion against the server's advisories
// (c.MinApp / c.LatestApp) and informs the user once per run: a gentle note when
// a newer version exists, a stronger one when this build is below the supported
// minimum. It never blocks the connection — the box is shown on its own thread,
// and the server enforces the minimum separately (by protocol version).
func versionNotice(c *clientsignaling.Client) {
	if c == nil {
		return
	}
	minApp, latestApp := strings.TrimSpace(c.MinApp), strings.TrimSpace(c.LatestApp)
	if minApp == "" && latestApp == "" {
		return
	}
	setUpdateStatusFromAdvisory(minApp, latestApp) // keep the Hakkında page fresh
	versionNoticeOnce.Do(func() {
		switch {
		case minApp != "" && semverLess(appVersion, minApp):
			target := latestApp
			if target == "" {
				target = minApp
			}
			netlogf("Sürüm uyarısı: %s < zorunlu %s", appVersion, minApp)
			showVersionBox("Bu sürüm ("+appVersion+") artık desteklenmiyor.\n\nLütfen "+target+
				" sürümüne güncelleyin; aksi hâlde bağlantılar reddedilebilir.", true)
		case latestApp != "" && semverLess(appVersion, latestApp):
			netlogf("Sürüm bildirimi: %s → yeni %s", appVersion, latestApp)
			showVersionBox("Yeni bir sürüm mevcut: "+latestApp+"  (sizde "+appVersion+").\n\nGüncellemeniz önerilir.", false)
		}
	})
}

func showVersionBox(msg string, mandatory bool) {
	title := "Güncelleme"
	var icon uintptr = 0x00000040 // MB_ICONINFORMATION
	if mandatory {
		title = "Güncelleme Gerekli"
		icon = 0x00000030 // MB_ICONWARNING
	}
	go messageBox.Call(0,
		uintptr(unsafe.Pointer(utf16ptr(msg))),
		uintptr(unsafe.Pointer(utf16ptr(title))),
		icon|0x00010000) // | MB_SETFOREGROUND
}

// semverLess reports whether dotted numeric version a is older than b
// ("1.2.0" < "1.10.0"). Missing or non-numeric parts count as 0.
func semverLess(a, b string) bool {
	pa, pb := parseVer(a), parseVer(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func parseVer(s string) []int {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "-+ "); i >= 0 { // drop any pre-release/build suffix
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}
