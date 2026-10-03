//go:build windows

package main

// Build 2: session video recording as MJPEG AVI (16) and input macros (1).

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eirahoutmoss/remotesupport/shared/protocol"
)

// ---- 16: video recording ----

var (
	recording       atomic.Bool
	remoteRecording atomic.Bool // agent: the operator is recording this session
	recMu           sync.Mutex
	recLatest       []byte
	recW, recH      int
	recStop         chan struct{}
)

type aviWriter struct {
	f          *os.File
	w, h       int
	frames     int
	maxSize    int
	moviStart  int64 // offset of the 'movi' fourcc
	index      []byte
	avihFrames int64
	strhLength int64
	moviSize   int64
}

func le32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
func le16(v uint16) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }

func newAVI(path string, w, h, fps int) (*aviWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	a := &aviWriter{f: f, w: w, h: h}
	var b []byte
	put := func(p ...[]byte) {
		for _, x := range p {
			b = append(b, x...)
		}
	}
	put([]byte("RIFF"), le32(0), []byte("AVI "))
	put([]byte("LIST"), le32(4+8+56+8+4+8+56+8+40), []byte("hdrl"))
	put([]byte("avih"), le32(56))
	put(le32(uint32(1000000/fps)), le32(0), le32(0), le32(0x10))
	a.avihFrames = int64(len(b))
	put(le32(0), le32(0), le32(1), le32(1<<20), le32(uint32(w)), le32(uint32(h)), le32(0), le32(0), le32(0), le32(0))
	put([]byte("LIST"), le32(4+8+56+8+40), []byte("strl"))
	put([]byte("strh"), le32(56), []byte("vids"), []byte("MJPG"), le32(0), le16(0), le16(0), le32(0), le32(1), le32(uint32(fps)), le32(0))
	a.strhLength = int64(len(b))
	put(le32(0), le32(1<<20), le32(0xFFFFFFFF), le32(0), le16(0), le16(0), le16(uint16(w)), le16(uint16(h)))
	put([]byte("strf"), le32(40), le32(40), le32(uint32(w)), le32(uint32(h)), le16(1), le16(24), []byte("MJPG"), le32(uint32(w*h*3)), le32(0), le32(0), le32(0), le32(0))
	put([]byte("LIST"), le32(0))
	a.moviSize = int64(len(b) - 4)
	a.moviStart = int64(len(b))
	put([]byte("movi"))
	if _, err := f.Write(b); err != nil {
		f.Close()
		return nil, err
	}
	return a, nil
}

func (a *aviWriter) add(jpeg []byte) error {
	pos, _ := a.f.Seek(0, 1)
	chunk := append(append([]byte("00dc"), le32(uint32(len(jpeg)))...), jpeg...)
	if len(jpeg)%2 == 1 {
		chunk = append(chunk, 0)
	}
	if _, err := a.f.Write(chunk); err != nil {
		return err
	}
	a.index = append(a.index, []byte("00dc")...)
	a.index = append(a.index, le32(0x10)...)
	a.index = append(a.index, le32(uint32(pos-a.moviStart))...)
	a.index = append(a.index, le32(uint32(len(jpeg)))...)
	a.frames++
	return nil
}

func (a *aviWriter) close() error {
	end, _ := a.f.Seek(0, 1)
	moviLen := end - a.moviStart
	a.f.Write(append(append([]byte("idx1"), le32(uint32(len(a.index)))...), a.index...))
	total, _ := a.f.Seek(0, 1)
	a.f.WriteAt(le32(uint32(total-8)), 4)
	a.f.WriteAt(le32(uint32(moviLen)), a.moviSize)
	a.f.WriteAt(le32(uint32(a.frames)), a.avihFrames)
	a.f.WriteAt(le32(uint32(a.frames)), a.strhLength)
	return a.f.Close()
}

// recordSnapshot encodes what the viewer currently shows. Frames arrive as
// tile deltas, so the composed viewer image is the only complete picture.
func recordSnapshot() ([]byte, int, int) {
	viewer.mu.RLock()
	w, h := viewer.width, viewer.height
	var px []byte
	if viewer.pixels != nil && w > 0 && h > 0 {
		px = append([]byte(nil), viewer.pixels...)
	}
	viewer.mu.RUnlock()
	if px == nil {
		return nil, 0, 0
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+3 < len(px) && i+3 < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = px[i+2], px[i+1], px[i], 255
	}
	var buf bytes.Buffer
	if jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}) != nil {
		return nil, 0, 0
	}
	return buf.Bytes(), w, h
}

func startRecording() {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Videos", "NexDesk")
	if err := os.MkdirAll(dir, 0755); err != nil {
		setStatus("● Kayıt klasörü oluşturulamadı: " + err.Error())
		return
	}
	host := regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(getRemoteHost(), "")
	path := filepath.Join(dir, fmt.Sprintf("NexDesk-%s-%s.avi", host, time.Now().Format("20060102-150405")))
	recMu.Lock()
	recStop = make(chan struct{})
	stop := recStop
	recMu.Unlock()
	recording.Store(true)
	sendCtl("REC:1")
	auditLog("Video kaydı başladı: " + path)
	netlogf("Video kaydı başladı: %s", path)
	setStatus("● Oturum kaydediliyor: " + path)
	go func() {
		defer logCrash("record")
		const fps = 10
		var a *aviWriter
		t := time.NewTicker(time.Second / fps)
		defer t.Stop()
		for {
			select {
			case <-stop:
				if a != nil {
					a.close()
					setStatus(fmt.Sprintf("● Video kaydedildi (%d sn): %s", a.frames/fps, path))
				}
				return
			case <-t.C:
			}
			j, w, h := recordSnapshot()
			if j == nil {
				continue
			}
			if a == nil {
				var err error
				if a, err = newAVI(path, w, h, fps); err != nil {
					setStatus("● Kayıt dosyası açılamadı: " + err.Error())
					recording.Store(false)
					return
				}
			}
			if a.add(j) != nil {
				a.close()
				recording.Store(false)
				setStatus("● Kayıt yazılamadı (disk dolu olabilir).")
				return
			}
		}
	}()
}

func stopRecording() {
	if !recording.Swap(false) {
		return
	}
	recMu.Lock()
	if recStop != nil {
		close(recStop)
		recStop = nil
	}
	recMu.Unlock()
	sendCtl("REC:0")
	auditLog("Video kaydı durduruldu")
	netlogf("Video kaydı durduruldu")
}

// ---- 1: macros ----

type macroStep struct {
	Dt int                 `json:"dt"` // ms since previous step
	Ev protocol.InputEvent `json:"ev"`
}

type macroFile struct {
	Name    string      `json:"name"`
	Created string      `json:"created"`
	Steps   []macroStep `json:"steps"`
}

var (
	macroRecording atomic.Bool
	macroPlaying   atomic.Bool
	macroAbort     atomic.Bool
	macroMu        sync.Mutex
	macroSteps     []macroStep
	macroLast      time.Time
)

func macroDir() string {
	d := filepath.Join(b1Dir(), "macros")
	_ = os.MkdirAll(d, 0700)
	return d
}

func macroList() []string {
	ents, _ := os.ReadDir(macroDir())
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Strings(out)
	if len(out) > 50 {
		out = out[:50]
	}
	return out
}

// macroRecord is called from sendRemoteInput for every event sent live.
func macroRecord(ev protocol.InputEvent) {
	if !macroRecording.Load() || macroPlaying.Load() {
		return
	}
	macroMu.Lock()
	defer macroMu.Unlock()
	now := time.Now()
	dt := int(now.Sub(macroLast) / time.Millisecond)
	if len(macroSteps) == 0 {
		dt = 0
	}
	// Collapse bursts of mouse moves to keep files small.
	if ev.T == "move" && len(macroSteps) > 0 && macroSteps[len(macroSteps)-1].Ev.T == "move" && dt < 30 {
		macroSteps[len(macroSteps)-1].Ev = ev
		return
	}
	macroLast = now
	macroSteps = append(macroSteps, macroStep{Dt: dt, Ev: ev})
}

func macroStart() {
	macroMu.Lock()
	macroSteps = nil
	macroLast = time.Now()
	macroMu.Unlock()
	macroRecording.Store(true)
	setStatus("● Makro kaydediliyor — uzak ekranda işlemleri yapın, bitince İşlemler > Makrolar > Kaydı Durdur.")
}

func macroStopAndSave() {
	macroRecording.Store(false)
	macroMu.Lock()
	steps := append([]macroStep(nil), macroSteps...)
	macroMu.Unlock()
	if len(steps) == 0 {
		setStatus("● Makroda kayıtlı işlem yok.")
		return
	}
	name, ok := promptText("Makroyu Kaydet", fmt.Sprintf("Makro adı (%d adım):", len(steps)), "Makro "+time.Now().Format("02.01 15.04"))
	name = strings.TrimSpace(regexp.MustCompile(`[\\/:*?"<>|]`).ReplaceAllString(name, ""))
	if !ok || name == "" {
		setStatus("● Makro kaydedilmedi.")
		return
	}
	b, _ := json.MarshalIndent(macroFile{Name: name, Created: time.Now().Format(time.RFC3339), Steps: steps}, "", " ")
	if err := os.WriteFile(filepath.Join(macroDir(), name+".json"), b, 0600); err != nil {
		setStatus("● Makro yazılamadı: " + err.Error())
		return
	}
	setStatus("● Makro kaydedildi: " + name)
}

func macroPlay(name string) {
	defer logCrash("macroPlay")
	if !macroPlaying.CompareAndSwap(false, true) {
		return
	}
	defer macroPlaying.Store(false)
	b, err := os.ReadFile(filepath.Join(macroDir(), name+".json"))
	var mf macroFile
	if err != nil || json.Unmarshal(b, &mf) != nil {
		setStatus("● Makro okunamadı: " + name)
		return
	}
	macroAbort.Store(false)
	netlogf("Makro oynatılıyor: %s (%d adım)", name, len(mf.Steps))
	for i, s := range mf.Steps {
		d := time.Duration(min(s.Dt, 5000)) * time.Millisecond
		time.Sleep(d)
		if macroAbort.Load() {
			setStatus("● Makro durduruldu.")
			return
		}
		p := currentPeer()
		if p == nil {
			return
		}
		sendRemoteInput(p, s.Ev)
		if i%20 == 0 {
			setStatus(fmt.Sprintf("● Makro oynatılıyor: %s  %d/%d", name, i+1, len(mf.Steps)))
		}
	}
	setStatus("● Makro tamamlandı: " + name)
}

func macroStopAll() {
	macroAbort.Store(true)
	macroRecording.Store(false)
}
