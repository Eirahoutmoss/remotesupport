//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"
)

type brandCfg struct {
	Name     string `json:"name"`
	Tagline  string `json:"tagline"`
	Author   string `json:"author"`
	Accent   string `json:"accent"`
	LogoPath string `json:"logo_path"`
}

var (
	brandMu       sync.RWMutex
	brandNameV    = "NexDesk"
	brandTaglineV = "Uzaktan destek, daha kolay"
	brandAuthorV  = "Hasan Güler"
	brandAccentV  = uint32(uiAccentColor)
	brandLogoPath string
)

func brandName() string    { brandMu.RLock(); defer brandMu.RUnlock(); return brandNameV }
func brandTagline() string { brandMu.RLock(); defer brandMu.RUnlock(); return brandTaglineV }
func brandAuthor() string  { brandMu.RLock(); defer brandMu.RUnlock(); return brandAuthorV }
func brandAccent() uint32  { brandMu.RLock(); defer brandMu.RUnlock(); return brandAccentV }

func brandingPath() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "RemoteSupport", "branding.json")
}

// loadBranding lets an integrator white-label the app by dropping a branding.json
// next to the settings (name, tagline, author, accent hex, optional logo path).
func loadBranding() {
	data, err := os.ReadFile(brandingPath())
	if err != nil {
		return
	}
	var b brandCfg
	if json.Unmarshal(data, &b) != nil {
		return
	}
	brandMu.Lock()
	if strings.TrimSpace(b.Name) != "" {
		brandNameV = strings.TrimSpace(b.Name)
	}
	if strings.TrimSpace(b.Tagline) != "" {
		brandTaglineV = strings.TrimSpace(b.Tagline)
	}
	if strings.TrimSpace(b.Author) != "" {
		brandAuthorV = strings.TrimSpace(b.Author)
	}
	if c, ok := parseHexColor(b.Accent); ok {
		brandAccentV = c
	}
	brandLogoPath = strings.TrimSpace(b.LogoPath)
	brandMu.Unlock()
}

func parseHexColor(s string) (uint32, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if len(s) != 6 {
		return 0, false
	}
	var v uint32
	for _, r := range s {
		var d uint32
		switch {
		case r >= '0' && r <= '9':
			d = uint32(r - '0')
		case r >= 'a' && r <= 'f':
			d = uint32(r-'a') + 10
		case r >= 'A' && r <= 'F':
			d = uint32(r-'A') + 10
		default:
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

var (
	logoOnce  sync.Once
	logoPix   []byte
	logoW     int
	logoH     int
	logoPlate uint32 = 0x1D1C1D
)

func loadLogo() {
	logoOnce.Do(func() {
		var raw []byte
		brandMu.RLock()
		lp := brandLogoPath
		brandMu.RUnlock()
		if lp != "" {
			if d, err := os.ReadFile(lp); err == nil {
				raw = d
			}
		}
		if raw == nil {
			raw = logoPNG
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return
		}
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		if w <= 0 || h <= 0 {
			return
		}
		pix := make([]byte, w*h*4)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
				i := (y*w + x) * 4
				pix[i+0] = byte(bl >> 8)
				pix[i+1] = byte(g >> 8)
				pix[i+2] = byte(r >> 8)
				pix[i+3] = 0
			}
		}
		cr, cg, cb, _ := img.At(b.Min.X+2, b.Min.Y+2).RGBA()
		logoPlate = (uint32(cr>>8) << 16) | (uint32(cg>>8) << 8) | uint32(cb>>8)
		logoPix, logoW, logoH = pix, w, h
	})
}

// drawLogoPlate paints a rounded plate the colour of the logo's own background
// and blits the logo aspect-fit + centred inside it, so any letterbox area blends
// seamlessly regardless of the surface behind it.
func drawLogoPlate(hdc uintptr, x, y, w, h, radius int) {
	loadLogo()
	paintRoundPanel(hdc, x, y, w, h, logoPlate, radius)
	if len(logoPix) == 0 || logoW == 0 || logoH == 0 {
		return
	}
	pad := 6
	aw, ah := w-pad*2, h-pad*2
	if aw <= 0 || ah <= 0 {
		return
	}
	dw, dh := aw, ah
	if logoW*ah > logoH*aw {
		dh = logoH * aw / logoW
	} else {
		dw = logoW * ah / logoH
	}
	dx := x + pad + (aw-dw)/2
	dy := y + pad + (ah-dh)/2
	bi := bitmapInfo{BmiHeader: bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       int32(logoW),
		BiHeight:      -int32(logoH),
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: 0,
	}}
	setStretchMode.Call(hdc, 4) // HALFTONE: smooth downscale
	stretchDIBits.Call(hdc,
		uintptr(dx), uintptr(dy), uintptr(dw), uintptr(dh),
		0, 0, uintptr(logoW), uintptr(logoH),
		uintptr(unsafe.Pointer(&logoPix[0])), uintptr(unsafe.Pointer(&bi)), 0, 0x00CC0020)
}

// ---------------------------------------------------------------------------
// Join link + QR code
// ---------------------------------------------------------------------------

func primaryLANIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	if ua, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return ua.IP.String()
	}
	return ""
}

// shareWSURL returns the ws:// endpoint an operator should dial to reach THIS
// target: the configured internet signaling if set, else this machine's LAN
// address on the embedded signaling port.
func shareWSURL() string {
	cfg := currentSettings()
	if l := splitURLs(cfg.SignalingURL); len(l) > 0 {
		return l[0]
	}
	if builtinSignaling() != "" {
		return splitURLs(builtinServer().SignalingURL)[0] // public address for the link/QR
	}
	inetMu.Lock()
	pub := inetWS
	inetMu.Unlock()
	if pub != "" {
		return pub
	}
	if ip := primaryLANIP(); ip != "" {
		return "ws://" + ip + ":8091/v1/ws"
	}
	return ""
}

var linkCacheMu sync.Mutex
var linkCacheCode, linkCacheVal string

func buildJoinLink(code string) string {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	linkCacheMu.Lock()
	defer linkCacheMu.Unlock()
	if code == linkCacheCode && linkCacheVal != "" {
		return linkCacheVal
	}
	v := url.Values{}
	if ws := shareWSURL(); ws != "" {
		v.Set("ws", ws)
	}
	v.Set("code", code)
	linkCacheCode = code
	linkCacheVal = "nexdesk://join?" + v.Encode()
	return linkCacheVal
}

// parseJoinInput accepts either a raw numeric code or a nexdesk://join link and
// returns the code plus an optional ws endpoint override.
func parseJoinInput(s string) (code, ws string) {
	s = strings.TrimSpace(s)
	if ws, code, ok := decodeInternetCode(s); ok {
		return code, ws
	}
	if strings.HasPrefix(strings.ToLower(s), "nexdesk://") {
		if u, err := url.Parse(s); err == nil {
			q := u.Query()
			return normalizeCode(q.Get("code")), strings.TrimSpace(q.Get("ws"))
		}
	}
	return normalizeCode(s), ""
}

var opEndpointMu sync.Mutex
var opEndpointOverride string

func setOpEndpoint(v string) { opEndpointMu.Lock(); opEndpointOverride = v; opEndpointMu.Unlock() }
func getOpEndpoint() string {
	opEndpointMu.Lock()
	defer opEndpointMu.Unlock()
	return opEndpointOverride
}

var (
	qrCacheMu  sync.Mutex
	qrCacheKey string
	qrCacheMat [][]bool
)

func qrMatrix(text string) [][]bool {
	qrCacheMu.Lock()
	defer qrCacheMu.Unlock()
	if text == qrCacheKey && qrCacheMat != nil {
		return qrCacheMat
	}
	m, _ := qrEncode(text, 0)
	qrCacheKey = text
	qrCacheMat = m
	return m
}

// drawQR paints a cached QR of text into a size×size box at (x,y). A 1px-per-
// module DIB is scaled up with a nearest-neighbour StretchBlt for crisp squares.
func drawQR(hdc uintptr, text string, x, y, size int) {
	mat := qrMatrix(text)
	n := len(mat)
	if n == 0 {
		return
	}
	quiet := 3
	dim := n + quiet*2
	paintRoundPanel(hdc, x, y, size, size, 0xFFFFFF, 8)
	pix := make([]byte, dim*dim*4)
	for i := range pix {
		pix[i] = 0xFF
	}
	for r := 0; r < n; r++ {
		for c := 0; c < n; c++ {
			if mat[r][c] {
				i := ((r+quiet)*dim + (c + quiet)) * 4
				pix[i+0], pix[i+1], pix[i+2] = 0, 0, 0
			}
		}
	}
	inner := size - 12
	bi := bitmapInfo{BmiHeader: bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       int32(dim),
		BiHeight:      -int32(dim),
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: 0,
	}}
	setStretchMode.Call(hdc, 3) // COLORONCOLOR: crisp modules
	stretchDIBits.Call(hdc,
		uintptr(x+6), uintptr(y+6), uintptr(inner), uintptr(inner),
		0, 0, uintptr(dim), uintptr(dim),
		uintptr(unsafe.Pointer(&pix[0])), uintptr(unsafe.Pointer(&bi)), 0, 0x00CC0020)
}

// ---------------------------------------------------------------------------
// Auto-reconnect
// ---------------------------------------------------------------------------
