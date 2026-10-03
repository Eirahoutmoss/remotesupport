//go:build windows

package main

// Internet connection without a server: the agent opens its embedded
// signaling port on the home router via UPnP, learns its public IP and packs
// IP + port + session code into a short "internet code" that can be sent over
// WhatsApp. The operator pastes the code and dials the agent directly.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	inetMu     sync.Mutex
	inetCode   string // formatted XXXXX-XXXXX-XXXXX-XXXXX, "" until ready
	inetWS     string // ws://public:port/v1/ws
	inetNote   string // status line shown under the code
	upnpCtl    string // control URL + service type of the mapping we made
	upnpSvc    string
	upnpExtPrt int
)

func internetCodeState() (code, note string) {
	inetMu.Lock()
	defer inetMu.Unlock()
	return inetCode, inetNote
}

func setInetNote(s string) {
	inetMu.Lock()
	inetNote = s
	inetMu.Unlock()
	if state.hwnd != 0 {
		invalidateRect.Call(state.hwnd, 0, 0)
	}
}

func checksum(b []byte) byte {
	var c byte = 0xA5
	for i, x := range b {
		c = c*31 + x + byte(i)
	}
	return c
}

// encodeInternetCode packs IPv4, port and the numeric session code.
func encodeInternetCode(ip net.IP, port int, code string) (string, bool) {
	ip4 := ip.To4()
	n, err := strconv.ParseUint(code, 10, 64)
	if ip4 == nil || err != nil || n >= 1<<40 || port <= 0 || port > 65535 {
		return "", false
	}
	b := make([]byte, 12)
	copy(b, ip4)
	binary.BigEndian.PutUint16(b[4:], uint16(port))
	for i := 0; i < 5; i++ {
		b[6+i] = byte(n >> (8 * (4 - i)))
	}
	b[11] = checksum(b[:11])
	var sb strings.Builder
	var acc uint32
	bits := 0
	for _, x := range b {
		acc = acc<<8 | uint32(x)
		bits += 8
		for bits >= 5 {
			bits -= 5
			sb.WriteByte(crockford[(acc>>bits)&31])
		}
	}
	if bits > 0 {
		sb.WriteByte(crockford[(acc<<(5-bits))&31])
	}
	s := sb.String() // 20 chars
	return s[0:5] + "-" + s[5:10] + "-" + s[10:15] + "-" + s[15:20], true
}

// decodeInternetCode reverses encodeInternetCode; tolerant of case, spaces,
// dashes and the usual O/0, I/L/1 confusions.
func decodeInternetCode(s string) (ws, code string, ok bool) {
	s = strings.ToUpper(s)
	s = strings.NewReplacer("-", "", " ", "", "O", "0", "I", "1", "L", "1").Replace(s)
	if len(s) != 20 {
		return "", "", false
	}
	var acc uint32
	bits := 0
	var b []byte
	for _, r := range s {
		v := strings.IndexRune(crockford, r)
		if v < 0 {
			return "", "", false
		}
		acc = acc<<5 | uint32(v)
		bits += 5
		if bits >= 8 {
			bits -= 8
			b = append(b, byte(acc>>bits))
		}
	}
	if len(b) < 12 || checksum(b[:11]) != b[11] {
		return "", "", false
	}
	ip := net.IPv4(b[0], b[1], b[2], b[3])
	port := int(binary.BigEndian.Uint16(b[4:6]))
	var n uint64
	for i := 0; i < 5; i++ {
		n = n<<8 | uint64(b[6+i])
	}
	return fmt.Sprintf("ws://%s/v1/ws", net.JoinHostPort(ip.String(), strconv.Itoa(port))), fmt.Sprintf("%010d", n), true
}

func privateOrCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return true
	}
	return ip4.IsPrivate() || ip4.IsLoopback() || (ip4[0] == 100 && ip4[1]&0xC0 == 64)
}

// prepareInternetCode runs on the agent after the session code exists.
func prepareInternetCode(code string) {
	defer logCrash("internetCode")
	resetInternetCode()
	setInetNote("İnternet kodu hazırlanıyor (modemde port açılıyor)…")
	lan := primaryLANIP()
	extIP, port, err := upnpMap(lan, 8091)
	how := "UPnP ile modemde port açıldı"
	if err != nil {
		netlogf("UPnP başarısız: %v", err)
		port = 8091
		how = "Modemde UPnP yok — 8091 TCP portunu bu bilgisayara (" + lan + ") yönlendirmeniz gerekir"
	}
	pub := publicIP()
	if extIP == nil || privateOrCGNAT(extIP) {
		if pub == nil {
			setInetNote("İnternet kodu oluşturulamadı: dış IP öğrenilemedi. Yalnızca yerel ağ kodu geçerli.")
			return
		}
		if extIP != nil && privateOrCGNAT(extIP) {
			how = "İnternet sağlayıcınız paylaşımlı IP (CGNAT) kullanıyor — dışarıdan bağlantı büyük olasılıkla ulaşamaz"
		}
		extIP = pub
	}
	ic, ok := encodeInternetCode(extIP, port, code)
	if !ok {
		setInetNote("İnternet kodu oluşturulamadı.")
		return
	}
	inetMu.Lock()
	inetCode = ic
	inetWS = fmt.Sprintf("ws://%s/v1/ws", net.JoinHostPort(extIP.String(), strconv.Itoa(port)))
	inetMu.Unlock()
	linkCacheMu.Lock()
	linkCacheCode = "" // rebuild the QR/link with the public endpoint
	linkCacheMu.Unlock()
	netlogf("İnternet kodu hazır: %s (%s:%d) — %s", ic, extIP, port, how)
	setInetNote(how)
	if state.hwnd != 0 {
		postMessage.Call(state.hwnd, WM_APP_IDLE, 0, 0) // re-layout on the GUI thread to show the buttons
	}
}

func resetInternetCode() {
	inetMu.Lock()
	inetCode, inetWS, inetNote = "", "", ""
	inetMu.Unlock()
}

// releaseInternetCode removes our router mapping (called when the agent stops).
func releaseInternetCode() {
	inetMu.Lock()
	ctl, svc, port := upnpCtl, upnpSvc, upnpExtPrt
	upnpCtl, upnpSvc, upnpExtPrt = "", "", 0
	inetMu.Unlock()
	resetInternetCode()
	if state.hwnd != 0 {
		postMessage.Call(state.hwnd, WM_APP_IDLE, 0, 0)
	}
	if ctl != "" {
		go soapCall(ctl, svc, "DeletePortMapping", fmt.Sprintf("<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort><NewProtocol>TCP</NewProtocol>", port))
	}
}

func publicIP() net.IP {
	c := &http.Client{Timeout: 4 * time.Second}
	for _, u := range []string{"https://api.ipify.org", "https://ifconfig.me/ip", "https://icanhazip.com"} {
		resp, err := c.Get(u)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if ip := net.ParseIP(strings.TrimSpace(string(b))); ip != nil && ip.To4() != nil {
			return ip
		}
	}
	return nil
}

// ---- minimal UPnP IGD client ----

func upnpDiscover(timeout time.Duration) []string {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil
	}
	defer conn.Close()
	dst := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	for _, st := range []string{"urn:schemas-upnp-org:device:InternetGatewayDevice:1", "urn:schemas-upnp-org:device:InternetGatewayDevice:2", "urn:schemas-upnp-org:service:WANIPConnection:1"} {
		msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nST: " + st + "\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\n\r\n"
		conn.WriteTo([]byte(msg), dst)
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	seen := map[string]bool{}
	var locs []string
	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		for _, line := range strings.Split(string(buf[:n]), "\r\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "location") {
				v = strings.TrimSpace(v)
				if !seen[v] {
					seen[v] = true
					locs = append(locs, v)
				}
			}
		}
	}
	return locs
}

type igdDesc struct {
	URLBase  string       `xml:"URLBase"`
	Services []igdService `xml:"device>deviceList>device>deviceList>device>serviceList>service"`
}

type igdService struct {
	Type    string `xml:"serviceType"`
	Control string `xml:"controlURL"`
}

func upnpControl(loc string) (ctl, svc string, err error) {
	c := &http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get(loc)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	var d igdDesc
	if err := xml.Unmarshal(body, &d); err != nil {
		return "", "", err
	}
	base, _ := url.Parse(loc)
	if d.URLBase != "" {
		if b, e := url.Parse(d.URLBase); e == nil {
			base = b
		}
	}
	for _, s := range d.Services {
		if strings.Contains(s.Type, "WANIPConnection") || strings.Contains(s.Type, "WANPPPConnection") {
			u, e := base.Parse(s.Control)
			if e != nil {
				continue
			}
			return u.String(), s.Type, nil
		}
	}
	return "", "", fmt.Errorf("modemde WAN bağlantı servisi bulunamadı")
}

func soapCall(ctl, svc, action, args string) (string, error) {
	body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>` +
		`<u:` + action + ` xmlns:u="` + svc + `">` + args + `</u:` + action + `></s:Body></s:Envelope>`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", ctl, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+svc+`#`+action+`"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 200 {
		return string(b), fmt.Errorf("%s: HTTP %d", action, resp.StatusCode)
	}
	return string(b), nil
}

func xmlField(s, tag string) string {
	i := strings.Index(s, "<"+tag+">")
	if i < 0 {
		return ""
	}
	s = s[i+len(tag)+2:]
	if j := strings.Index(s, "</"+tag+">"); j >= 0 {
		return strings.TrimSpace(s[:j])
	}
	return ""
}

// upnpMap opens external TCP port -> lanIP:internal and returns the router's
// external IP and the external port actually used.
func upnpMap(lanIP string, internal int) (net.IP, int, error) {
	if lanIP == "" {
		return nil, 0, fmt.Errorf("yerel IP bulunamadı")
	}
	locs := upnpDiscover(2500 * time.Millisecond)
	if len(locs) == 0 {
		return nil, 0, fmt.Errorf("UPnP modem bulunamadı")
	}
	var lastErr error
	for _, loc := range locs {
		ctl, svc, err := upnpControl(loc)
		if err != nil {
			lastErr = err
			continue
		}
		for ext := internal; ext < internal+10; ext++ {
			args := fmt.Sprintf("<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort><NewProtocol>TCP</NewProtocol><NewInternalPort>%d</NewInternalPort><NewInternalClient>%s</NewInternalClient><NewEnabled>1</NewEnabled><NewPortMappingDescription>NexDesk</NewPortMappingDescription><NewLeaseDuration>7200</NewLeaseDuration>", ext, internal, lanIP)
			if _, err := soapCall(ctl, svc, "AddPortMapping", args); err != nil {
				lastErr = err
				continue
			}
			inetMu.Lock()
			upnpCtl, upnpSvc, upnpExtPrt = ctl, svc, ext
			inetMu.Unlock()
			var ip net.IP
			if r, err := soapCall(ctl, svc, "GetExternalIPAddress", ""); err == nil {
				ip = net.ParseIP(xmlField(r, "NewExternalIPAddress"))
			}
			return ip, ext, nil
		}
	}
	return nil, 0, lastErr
}

var (
	createCompatDC  = gdi32DLL.NewProc("CreateCompatibleDC")
	createCompatBmp = gdi32DLL.NewProc("CreateCompatibleBitmap")
	deleteDC        = gdi32DLL.NewProc("DeleteDC")
	bitBlt          = gdi32DLL.NewProc("BitBlt")
)

// paintBuffered renders the shell into an off-screen bitmap and copies it in
// one BitBlt, so the once-a-second repaints (countdown, live stats) no longer
// flash the background gradient on screen.
func paintBuffered(hwnd, hdc uintptr) {
	var rc rect
	getClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	w, h := uintptr(rc.Right-rc.Left), uintptr(rc.Bottom-rc.Top)
	mem, _, _ := createCompatDC.Call(hdc)
	bmp, _, _ := createCompatBmp.Call(hdc, w, h)
	if mem == 0 || bmp == 0 || w == 0 || h == 0 {
		if bmp != 0 {
			deleteObject.Call(bmp)
		}
		if mem != 0 {
			deleteDC.Call(mem)
		}
		paintShell(hdc)
		return
	}
	old, _, _ := selectObject.Call(mem, bmp)
	paintShell(mem)
	bitBlt.Call(hdc, 0, 0, w, h, mem, 0, 0, 0x00CC0020) // SRCCOPY
	selectObject.Call(mem, old)
	deleteObject.Call(bmp)
	deleteDC.Call(mem)
}

// clickCopiesInternetCode lets the agent click the internet code to copy it.
func clickCopiesInternetCode(x, y int) bool {
	if state.mode != 1 || capturing.Load() {
		return false
	}
	if ic, _ := internetCodeState(); ic == "" {
		return false
	}
	if x >= contentX+166 && x <= contentX+766 && y >= 482 && y <= 540 {
		copyCode()
		return true
	}
	return false
}

const (
	ID_INET_COPY = 5220
	ID_INET_WA   = 5221
)

var inetCopyBtn, inetWABtn uintptr

// layoutInetButtons shows Copy / WhatsApp next to the internet code while the
// agent is waiting for a connection, and hides them everywhere else.
func layoutInetButtons(mode int, connected bool) {
	ic, _ := internetCodeState()
	visible := mode == 1 && !connected && !capturing.Load() && ic != ""
	if visible && inetCopyBtn == 0 {
		inetCopyBtn = createControl(state.hwnd, "BUTTON", "Kopyala", 0x00000001, 0, 0, 10, 10, ID_INET_COPY)
		inetWABtn = createControl(state.hwnd, "BUTTON", "WhatsApp ile Gönder", 0x00000001, 0, 0, 10, 10, ID_INET_WA)
	}
	moveControl(inetCopyBtn, contentX+500, 500, 110, 36, visible)
	moveControl(inetWABtn, contentX+618, 500, 180, 36, visible)
}

func shareInternetCodeWhatsApp() {
	ic, _ := internetCodeState()
	if ic == "" {
		return
	}
	msg := "NexDesk destek kodu: " + ic + "\nNexDesk'te \"Destek Ver\" bölümüne bu kodu yapıştırın."
	link := "https://wa.me/?text=" + url.QueryEscape(msg)
	shellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16ptr("open"))), uintptr(unsafe.Pointer(utf16ptr(link))), 0, 0, 1)
	setStatus("● WhatsApp açılıyor — kişiyi seçip mesajı gönderin.")
}

func onInetCommand(id int) bool {
	switch id {
	case ID_INET_COPY:
		copyCode()
	case ID_INET_WA:
		shareInternetCodeWhatsApp()
	default:
		return false
	}
	return true
}

// splitURLs accepts several addresses separated by commas, semicolons or
// spaces, e.g. the public and the in-house address of the same server.
func splitURLs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\t' })
}

// pickSignaling returns the first configured signaling URL that accepts a TCP
// connection, so technicians inside the company network (where the public
// address may not loop back) fall through to the in-house address.
func pickSignaling(s string) string {
	list := splitURLs(s)
	if len(list) <= 1 {
		return strings.Join(list, "")
	}
	if v := reachableSignaling(s); v != "" {
		return v
	}
	return list[0]
}

// reachableSignaling returns the first URL in s accepting TCP, or "".
func reachableSignaling(s string) string {
	list := splitURLs(s)
	for _, u := range list {
		p, err := url.Parse(u)
		if err != nil || p.Host == "" {
			continue
		}
		host := p.Host
		if p.Port() == "" {
			host = net.JoinHostPort(p.Hostname(), map[string]string{"wss": "443", "https": "443"}[p.Scheme]+map[bool]string{true: "80"}[p.Scheme == "ws" || p.Scheme == "http"])
		}
		if c, err := net.DialTimeout("tcp", host, 1500*time.Millisecond); err == nil {
			c.Close()
			return u
		}
	}
	return ""
}
