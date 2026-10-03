//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/eirahoutmoss/remotesupport/client/capture"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/pion/webrtc/v4"
)

func chooseFile() (string, bool) {
	buf := make([]uint16, 32768)
	// Build a valid double-NUL-terminated filter (utf16ptr stops at the first NUL).
	fa, _ := syscall.UTF16FromString("Tüm Dosyalar (*.*)")
	fb, _ := syscall.UTF16FromString("*.*")
	filterBuf := append(append(fa, fb...), 0)
	ofn := openFileName{
		StructSize: uint32(unsafe.Sizeof(openFileName{})),
		Owner:      state.hwnd,
		Filter:     &filterBuf[0],
		File:       &buf[0],
		MaxFile:    uint32(len(buf)),
		Flags:      0x00080000 | 0x00001000 | 0x00000800, // OFN_EXPLORER | FILEMUSTEXIST | PATHMUSTEXIST
	}
	r, _, _ := getOpenFileName.Call(uintptr(unsafe.Pointer(&ofn)))
	runtime.KeepAlive(filterBuf)
	runtime.KeepAlive(buf)
	if r == 0 {
		code, _, _ := commDlgError.Call()
		if code != 0 {
			msg := utf16ptr(fmt.Sprintf("Dosya seçici açılamadı (hata kodu %d).", code))
			title := utf16ptr(brandName())
			messageBox.Call(state.hwnd, uintptr(unsafe.Pointer(msg)), uintptr(unsafe.Pointer(title)), 0x00000010)
		}
		return "", false
	}
	return syscall.UTF16ToString(buf), true
}

func fileNotify(msg string) {
	setStatus("● " + msg)
}

func sendFileToPeer(path string) {
	fileSendMu.Lock()
	defer fileSendMu.Unlock()

	state.mu.Lock()
	peer := state.peer
	state.mu.Unlock()
	if peer == nil || peer.ConnectionState() != webrtc.PeerConnectionStateConnected {
		fileNotify("Dosya gönderilemiyor: bağlantı hazır değil.")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		setStatus("● Dosya açılamadı: " + err.Error())
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		setStatus("● Dosya bilgisi okunamadı: " + err.Error())
		return
	}
	if info.Size() > 4*1024*1024*1024 {
		setStatus("● Dosya 4 GB sınırını aşıyor.")
		return
	}
	setStatus("● Dosya kanalı hazırlanıyor...")
	deadline := time.Now().Add(10 * time.Second)
	for !peer.FileReady() {
		if time.Now().After(deadline) {
			fileNotify("Dosya kanalı hazır değil (10 sn içinde açılmadı).")
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	id := transferID(filepath.Base(path), info)
	meta, _ := json.Marshal(struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		ID   string `json:"id"`
	}{filepath.Base(path), info.Size(), id})
	resumeCh := awaitResume(id)
	if err := peer.SendFileData(append([]byte{0x01}, meta...)); err != nil {
		fileNotify("Dosya başlatılamadı: " + err.Error())
		return
	}
	var sent int64
	select {
	case off := <-resumeCh:
		if off > 0 && off < info.Size() {
			if _, err := f.Seek(off, 0); err == nil {
				sent = off
				netlogf("Dosya kaldığı yerden sürüyor: %s @%d", filepath.Base(path), off)
			}
		}
	case <-time.After(4 * time.Second):
	}
	fileCancel.Store(false)
	buf := make([]byte, 48*1024)
	for {
		if fileCancel.Swap(false) {
			_ = peer.SendFileData([]byte{0x04}) // abort → receiver drops the .part
			netlogf("Dosya gönderimi iptal edildi: %s", filepath.Base(path))
			fileNotify("Dosya gönderimi iptal edildi.")
			return
		}
		if _, rejected := fileRejected.LoadAndDelete(id); rejected {
			netlogf("Karşı taraf dosyayı reddetti: %s", filepath.Base(path))
			fileNotify("Karşı taraf dosyayı kabul etmedi: " + filepath.Base(path))
			return
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			packet := make([]byte, n+1)
			packet[0] = 0x02
			copy(packet[1:], buf[:n])
			for peer.FileBufferedAmount() > 8*1024*1024 {
				time.Sleep(5 * time.Millisecond)
			}
			if err := peer.SendFileData(packet); err != nil {
				// Keep the receiver's .part: the transfer resumes after reconnect.
				setPendingSend(path)
				netlogf("Dosya aktarımı kesildi: %s @%d", filepath.Base(path), sent)
				fileNotify("Dosya gönderimi kesildi — bağlantı dönünce kaldığı yerden sürecek.")
				return
			}
			sent += int64(n)
			pct := int64(100)
			if info.Size() > 0 {
				pct = sent * 100 / info.Size()
			}
			setStatus(fmt.Sprintf("● Gönderiliyor: %s  %d%%", filepath.Base(path), pct))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = peer.SendFileData([]byte{0x04})
			setStatus("● Dosya okuma hatası: " + readErr.Error())
			return
		}
	}
	if err := peer.SendFileData([]byte{0x03}); err != nil {
		setStatus("● Dosya tamamlanamadı: " + err.Error())
		return
	}
	fileNotify("Dosya gönderildi: " + filepath.Base(path))
}

func sendRemoteMonitorList(peer *webrtcpeer.Peer) {
	monitors, err := capture.ListMonitors()
	if err != nil || len(monitors) == 0 {
		return
	}
	b, err := json.Marshal(monitors)
	if err != nil {
		return
	}
	go sendCtlRetry(peer, "MONITORS:"+string(b))
}

// fileCancel asks an in-progress sendFileToPeer to abort at the next chunk.
var fileCancel atomic.Bool
