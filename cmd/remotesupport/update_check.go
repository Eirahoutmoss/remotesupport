//go:build windows

package main

import (
	"context"
	"strings"
	"sync"
	"time"

	clientsignaling "github.com/eirahoutmoss/remotesupport/client/signaling"
)

// Update status shown on the Hakkında page. It is refreshed from the server's
// version advisory on every normal connection and on demand via the
// "Güncellemeleri Kontrol Et" button. semverLess lives in version_notice.go.

var (
	updateStatusMu   sync.Mutex
	updateStatusText string
	updateChecking   bool
)

func setUpdateStatus(s string) {
	updateStatusMu.Lock()
	updateStatusText = s
	updateStatusMu.Unlock()
}

func getUpdateStatus() string {
	updateStatusMu.Lock()
	defer updateStatusMu.Unlock()
	return updateStatusText
}

// setUpdateStatusFromAdvisory turns the server's min/latest advisory into a
// human line. Called from versionNotice (normal connects) and the on-demand
// check, so the Hakkında page reflects the most recent information either way.
func setUpdateStatusFromAdvisory(minApp, latestApp string) {
	minApp, latestApp = strings.TrimSpace(minApp), strings.TrimSpace(latestApp)
	switch {
	case minApp != "" && semverLess(appVersion, minApp):
		t := latestApp
		if t == "" {
			t = minApp
		}
		setUpdateStatus("Güncelleme gerekli → " + t)
	case latestApp != "" && semverLess(appVersion, latestApp):
		setUpdateStatus("Yeni sürüm mevcut: " + latestApp)
	case latestApp != "":
		setUpdateStatus("Güncel (" + appVersion + ")")
	default:
		setUpdateStatus("Sunucu sürüm bilgisi vermedi.")
	}
}

// repaintAbout redraws the Hakkında page if it is the one on screen.
func repaintAbout() {
	if state.hwnd != 0 && state.mode == 11 {
		invalidateRect.Call(state.hwnd, 0, 1)
	}
}

// checkForUpdates does a short signaling handshake (dial → create → read the
// advisory → close) to fetch the server's latest-version info on demand. The
// throwaway code is dropped when the connection closes, so nothing lingers.
func checkForUpdates() {
	updateStatusMu.Lock()
	if updateChecking {
		updateStatusMu.Unlock()
		return
	}
	updateChecking = true
	updateStatusMu.Unlock()

	go func() {
		defer logCrash("updateCheck")
		defer func() {
			updateStatusMu.Lock()
			updateChecking = false
			updateStatusMu.Unlock()
		}()
		setUpdateStatus("Kontrol ediliyor…")
		repaintAbout()

		ep := signalingEndpoint()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		c, err := clientsignaling.Dial(ctx, ep)
		if err != nil {
			setUpdateStatus("Kontrol edilemedi — sunucuya ulaşılamadı.")
			repaintAbout()
			return
		}
		c.App = appVersion
		if _, _, err := c.Create(ctx); err != nil {
			_ = c.Close(context.Background())
			setUpdateStatus("Kontrol edilemedi.")
			repaintAbout()
			return
		}
		setUpdateStatusFromAdvisory(c.MinApp, c.LatestApp)
		_ = c.Close(context.Background())
		repaintAbout()
	}()
}
