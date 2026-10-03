//go:build windows

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/eirahoutmoss/remotesupport/client/capture"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	signaling "github.com/eirahoutmoss/remotesupport/server/signaling"
	"github.com/eirahoutmoss/remotesupport/shared/protocol"
)

func handleRemoteFile(data []byte) {
	if len(data) < 1 {
		return
	}
	switch data[0] {
	case 0x01:
		var meta struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(data[1:], &meta) != nil || meta.Size < 0 || meta.Size > 4*1024*1024*1024 {
			return
		}
		name := filepath.Base(strings.TrimSpace(meta.Name))
		if name == "" || name == "." || name == ".." {
			return
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return
		}
		dir := filepath.Join(home, "Downloads")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return
		}
		if resumeReceive(dir, meta.ID, meta.Size) {
			return
		}
		// Technician side: a NEW file from the supported PC needs consent.
		// (A resume above continues a transfer the technician already
		// accepted; the sender only waits 4 s for the resume offset, so it
		// must not sit behind a dialog.)
		if state.mode != 1 && !askIncomingFile(name, meta.Size) {
			sendCtl("FILE_REJECT:" + meta.ID)
			netlogf("Gelen dosya reddedildi: %s (%d bayt)", name, meta.Size)
			setStatus("● Gelen dosya reddedildi: " + name)
			return
		}
		final := filepath.Join(dir, name)
		if _, err := os.Stat(final); err == nil {
			ext := filepath.Ext(name)
			stem := strings.TrimSuffix(name, ext)
			for i := 1; ; i++ {
				candidate := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
				if _, e := os.Stat(candidate); os.IsNotExist(e) {
					name, final = filepath.Base(candidate), candidate
					break
				}
			}
		}
		tmp := final + ".part"
		f, err := os.Create(tmp)
		if err != nil {
			return
		}
		fileRx.mu.Lock()
		if fileRx.file != nil {
			_ = fileRx.file.Close()
		}
		fileRx.file, fileRx.name, fileRx.total, fileRx.received, fileRx.tempPath = f, name, meta.Size, 0, tmp
		fileRx.mu.Unlock()
		startReceive(tmp, meta.ID)
		setStatus(fmt.Sprintf("● Dosya alınıyor: %s  0%%", name))
	case 0x02:
		fileRx.mu.Lock()
		defer fileRx.mu.Unlock()
		if fileRx.file == nil {
			return
		}
		n, err := fileRx.file.Write(data[1:])
		if err != nil {
			_ = fileRx.file.Close()
			fileRx.file = nil
			return
		}
		fileRx.received += int64(n)
		if fileRx.total > 0 {
			setStatus(fmt.Sprintf("● Dosya alınıyor: %s  %d%%", fileRx.name, fileRx.received*100/fileRx.total))
		}
	case 0x03:
		fileRx.mu.Lock()
		defer fileRx.mu.Unlock()
		if fileRx.file == nil {
			return
		}
		_ = fileRx.file.Close()
		_ = os.Remove(fileRx.tempPath + ".id")
		final := strings.TrimSuffix(fileRx.tempPath, ".part")
		if err := os.Rename(fileRx.tempPath, final); err != nil {
			setStatus("● Dosya alınamadı: " + err.Error())
		} else {
			setStatus("● Dosya alındı: " + final)
		}
		fileRx.file = nil
		fileRx.tempPath = ""
	case 0x04:
		fileRx.mu.Lock()
		if fileRx.file != nil {
			_ = fileRx.file.Close()
		}
		if fileRx.tempPath != "" {
			_ = os.Remove(fileRx.tempPath)
			_ = os.Remove(fileRx.tempPath + ".id")
		}
		// Reset fields, not the struct: assigning fileReceiveState{} while
		// holding fileRx.mu replaced the locked mutex with an unlocked one,
		// and the Unlock below then crashed ("unlock of unlocked mutex").
		fileRx.file, fileRx.name, fileRx.total, fileRx.received, fileRx.tempPath = nil, "", 0, 0, ""
		fileRx.mu.Unlock()
		setStatus("● Dosya aktarımı iptal edildi.")
	}
}

func handleRemoteInput(peer *webrtcpeer.Peer, raw string) {
	if handleB1Control(peer, raw) {
		return
	}
	if strings.HasPrefix(raw, "CHAT:") {
		msg := strings.TrimPrefix(raw, "CHAT:")
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
		addChat(false, msg)
		return
	}
	if raw == "CURTAIN:1" || raw == "CURTAIN:0" {
		if state.hwnd != 0 {
			on := uintptr(0)
			if raw == "CURTAIN:1" {
				on = 1
			}
			postMessage.Call(state.hwnd, WM_APP_CURTAIN, on, 0)
		}
		if raw == "CURTAIN:1" {
			setStatus("● Gizlilik perdesi açıldı.")
		} else {
			setStatus("● Gizlilik perdesi kapatıldı.")
		}
		return
	}
	if raw == "LOCKINPUT:1" {
		blockInput.Call(1)
		setStatus("● Yerel giriş kilitlendi (destek).")
		return
	}
	if raw == "LOCKINPUT:0" {
		blockInput.Call(0)
		setStatus("● Yerel giriş kilidi açıldı.")
		return
	}
	if strings.HasPrefix(raw, "ANNOT:") {
		var a1, b1, c1, d1 float64
		if n, _ := fmt.Sscanf(strings.TrimPrefix(raw, "ANNOT:"), "%f,%f,%f,%f", &a1, &b1, &c1, &d1); n == 4 {
			annotMu.Lock()
			annotPending = append(annotPending, annotSegN{a1, b1, c1, d1})
			annotMu.Unlock()
			if state.hwnd != 0 {
				postMessage.Call(state.hwnd, WM_APP_ANNOT, 0, 0)
			}
		}
		return
	}
	if raw == "ANNOT_CLEAR" {
		if state.hwnd != 0 {
			postMessage.Call(state.hwnd, WM_APP_ANNOT, 1, 0)
		}
		return
	}
	if raw == "SYSINFO_REQ" {
		if peer != nil {
			_ = peer.SendControlText("SYSINFO:" + gatherSysInfo())
		}
		return
	}
	if strings.HasPrefix(raw, "SYSINFO:") {
		info := strings.TrimPrefix(raw, "SYSINFO:")
		go messageBox.Call(0, uintptr(unsafe.Pointer(utf16ptr(info))), uintptr(unsafe.Pointer(utf16ptr("Uzak Sistem Bilgisi"))), 0x00000040)
		return
	}
	if raw == "CLIP_REQ" {
		if sendClipboardToPeer(peer) {
			setStatus("● Pano karşı bilgisayara gönderildi.")
		}
		return
	}
	if strings.HasPrefix(raw, "CLIP:") {
		// The technician's clipboard is written only in answer to the
		// technician's own "Panoyu Al" — never pushed by the supported side,
		// which could otherwise plant a command for the technician to paste.
		if state.mode != 1 && !consumeClipRequest() {
			netlogf("İstenmeden gelen pano verisi reddedildi")
			return
		}
		payload := strings.TrimPrefix(raw, "CLIP:")
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil || len(data) > 48*1024 {
			return
		}
		if copyToClipboard(string(data)) {
			setStatus("● Uzak pano yerel panoya aktarıldı.")
		}
		return
	}
	if strings.HasPrefix(raw, "FILE_BEGIN:") || strings.HasPrefix(raw, "FILE_CHUNK:") || raw == "FILE_ABORT" || raw == "FILE_END" {
		return
	}
	if strings.HasPrefix(raw, "MONITORS:") {
		var monitors []capture.Monitor
		if err := json.Unmarshal([]byte(strings.TrimPrefix(raw, "MONITORS:")), &monitors); err == nil && len(monitors) > 0 {
			remoteMonitorsMu.Lock()
			remoteMonitors = append([]capture.Monitor(nil), monitors...)
			remoteMonitorsMu.Unlock()
			if int(activeMonitor.Load()) >= len(monitors) {
				activeMonitor.Store(0)
			}
			// Win32 child-window operations must run on the GUI thread.
			// WebRTC callbacks run on worker goroutines, so marshal the UI
			// update through PostMessage instead of creating/destroying
			// buttons directly from the callback goroutine.
			state.mu.Lock()
			hwnd := state.hwnd
			state.mu.Unlock()
			if hwnd != 0 {
				postMessage.Call(hwnd, WM_APP_MONITORS, uintptr(len(monitors)), 0)
			}
		}
		return
	}
	if strings.HasPrefix(raw, "RESOLUTION:") {
		if state.mode != 1 {
			return
		}
		var h int
		if _, err := fmt.Sscanf(strings.TrimPrefix(raw, "RESOLUTION:"), "%d", &h); err == nil {
			if h == 0 || h == 480 || h == 720 || h == 1080 {
				captureMaxHeight.Store(int32(h))
				setStatus(fmt.Sprintf("● Ekran aktarım çözünürlüğü: %s", map[int]string{0: "Otomatik", 480: "480p", 720: "720p", 1080: "1080p"}[h]))
			}
		}
		return
	}
	if strings.HasPrefix(raw, "MONITOR:") {
		if state.mode != 1 {
			return
		}
		var idx int
		if _, err := fmt.Sscanf(strings.TrimPrefix(raw, "MONITOR:"), "%d", &idx); err == nil && idx >= 0 {
			if monitors, err := capture.ListMonitors(); err == nil && idx < len(monitors) {
				mon := monitors[idx]
				activeMonitor.Store(int32(idx))
				activeTargetMonitorMu.Lock()
				activeTargetMonitor = mon
				activeTargetMonitorOK = true
				activeTargetMonitorMu.Unlock()
				setStatus(fmt.Sprintf("● Monitör %d/%d aktarılıyor", idx+1, len(monitors)))
			}
		}
		return
	}
	if strings.HasPrefix(raw, "HELLO:") {
		remoteHost := strings.TrimSpace(strings.TrimPrefix(raw, "HELLO:"))
		setRemoteHost(remoteHost)
		if state.hwnd != 0 {
			invalidateRect.Call(state.hwnd, 0, 0)
		}
		localHost, _ := os.Hostname()
		if remoteHost != "" && localHost != "" && strings.EqualFold(remoteHost, localHost) {
			sameHostPeer.Store(true)
			setStatus("● Loopback test modu: remote mouse/klavye etkin")
		}
		return
	}
	if !strings.HasPrefix(raw, "INPUT:") {
		return
	}
	var ev protocol.InputEvent
	if err := json.Unmarshal([]byte(strings.TrimPrefix(raw, "INPUT:")), &ev); err != nil {
		return
	}
	if err := ev.Validate(); err != nil {
		return
	}
	if ev.T == "move" || ev.T == "down" || ev.T == "up" || ev.T == "wheel" {
		injectRemoteMouse(ev)
		return
	}
	if ev.T == "keydown" || ev.T == "keyup" {
		injectRemoteKey(ev)
	}
}

func systemMetric(index int) int32 {
	r1, _, _ := getSystemMetrics.Call(uintptr(index))
	return int32(r1)
}

func normalizedToVirtualScreen(x, y float64) (int32, int32, bool) {
	mon, ok := remoteTargetMonitor()
	if !ok || mon.Width <= 0 || mon.Height <= 0 {
		return 0, 0, false
	}
	// The streamed image is monitor 0. Map normalized viewer coordinates to
	// that monitor's virtual-desktop rectangle, then to SendInput's 0..65535
	// virtual-screen coordinate space.
	px := float64(mon.X) + x*float64(maxInt(1, mon.Width-1))
	py := float64(mon.Y) + y*float64(maxInt(1, mon.Height-1))
	left := systemMetric(76)
	top := systemMetric(77)
	width := systemMetric(78)
	height := systemMetric(79)
	if width <= 1 || height <= 1 {
		return 0, 0, false
	}
	vx := int32(math.Round((px - float64(left)) * 65535 / float64(width-1)))
	vy := int32(math.Round((py - float64(top)) * 65535 / float64(height-1)))
	return vx, vy, true
}

func absoluteMouseInput(ev protocol.InputEvent, extraFlags uint32, mouseData uint32) (winInput, bool) {
	dx, dy, ok := normalizedToVirtualScreen(ev.X, ev.Y)
	if !ok {
		return winInput{}, false
	}
	return winInput{Type: inputMouse, Mi: winMouseInput{
		Dx: dx, Dy: dy, MouseData: mouseData,
		DwFlags: mouseAbsolute | mouseVirtual | extraFlags,
	}}, true
}

func injectRemoteMouse(ev protocol.InputEvent) {
	lastActivityUnix.Store(time.Now().Unix())
	flags := uint32(mouseMove)
	data := uint32(0)
	switch ev.T {
	case "move":
		flags = mouseMove
	case "down":
		noteRemoteClick(ev)
		switch ev.B {
		case 0:
			flags = mouseLeftDown
		case 1:
			flags = mouseMiddleDown
		case 2:
			flags = mouseRightDown
		}
	case "up":
		switch ev.B {
		case 0:
			flags = mouseLeftUp
		case 1:
			flags = mouseMiddleUp
		case 2:
			flags = mouseRightUp
		}
	case "wheel":
		flags = mouseWheel
		data = uint32(int32(ev.D))
	}
	in, ok := absoluteMouseInput(ev, flags, data)
	if !ok {
		return
	}
	// Do not immediately restore the physical cursor. SendInput is queued,
	// and SetCursorPos here can race the injected event on monitor 2/3.
	_, _, _ = sendInput.Call(1, uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Sizeof(in)))
}

func injectRemoteKey(ev protocol.InputEvent) {
	lastActivityUnix.Store(time.Now().Unix())
	vk := uint16(ev.K)
	if vk == 0 {
		return
	}
	// Use physical scan codes for navigation/modifier keys. This is important
	// for combinations such as Ctrl+Left/Ctrl+Right, where Windows text
	// controls rely on the extended-key semantics of the arrow keys.
	scanResult, _, _ := mapVirtualKey.Call(uintptr(vk), 0)
	scan := uint16(scanResult)
	flags := uint32(keyScancode)
	if ev.T == "keyup" {
		flags |= keyUp
	}
	if isExtendedVirtualKey(vk) {
		flags |= keyExtended
	}
	in := winKeyInput{Type: inputKeyboard, Ki: winKeyboardInput{
		Vk: vk, Scan: scan, Flags: flags,
	}}
	_, _, _ = sendInput.Call(1, uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Sizeof(in)))
}

func isExtendedVirtualKey(vk uint16) bool {
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x2D, 0x2E, 0x6F:
		return true
	}
	return false
}

// ---- consent for data pushed to the technician ----

var clipRequestedAt atomic.Int64 // unix nano of our last CLIP_REQ

// consumeClipRequest accepts one CLIP reply within 20 s of our own request.
func consumeClipRequest() bool {
	t := clipRequestedAt.Swap(0)
	return t != 0 && time.Since(time.Unix(0, t)) < 20*time.Second
}

func askIncomingFile(name string, size int64) bool {
	return askYesNo(brandName()+" — Gelen dosya", fmt.Sprintf(
		"Destek verdiğiniz bilgisayar size bir dosya göndermek istiyor:\r\n\r\n%s  (%s)\r\n\r\n"+
			"Kabul ederseniz İndirilenler klasörüne kaydedilir. Beklemediğiniz ya da tanımadığınız dosyaları açmayın.\r\n\r\n"+
			"Dosya kabul edilsin mi?", signaling.CleanName(name), humanBytes(size)))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d bayt", n)
	}
}
