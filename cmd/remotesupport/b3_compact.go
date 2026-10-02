//go:build windows

package main

// Compact serverless codes (~90 chars instead of ~700). Only what cannot be
// re-derived travels: a 6-byte seed (both ICE credentials are derived from
// it), the 32-byte DTLS fingerprint, the DTLS role and a few IPv4 candidates.
// The receiver rebuilds a data-channel-only SDP from these.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/pion/webrtc/v4"
)

const (
	compactOfferTag  = "DAVET-"
	compactAnswerTag = "YANIT-"
	compactVersion   = 1
)

var setupNames = []string{"actpass", "active", "passive"}
var candTypes = []string{"host", "srflx", "relay"}
var candPrio = []uint32{2130706431, 1694498815, 16777215}

func newSeed() []byte {
	s := make([]byte, 6)
	_, _ = rand.Read(s)
	return s
}

// iceCreds derives ufrag (8 chars) and pwd (24 chars) from the seed.
func iceCreds(seed []byte) (string, string) {
	h := sha256.Sum256(append([]byte("nexdesk-ice/v1|"), seed...))
	enc := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h[:]))
	return enc[:8], enc[8:32]
}

func newManualPeerSeed(seed []byte) (*webrtcpeer.Peer, error) {
	se := peerSettings()
	u, p := iceCreds(seed)
	se.SetICECredentials(u, p)
	// The codes travel by hand (WhatsApp etc.), so the side that answers may
	// wait minutes for the other to paste. Pion's defaults give up after 30 s
	// in 'checking' and stop pinging a pair after 7 requests; keep trying.
	se.SetICETimeouts(8*time.Second, 10*time.Minute, 2*time.Second)
	se.SetICEMaxBindingRequests(3000)
	return webrtcpeer.NewWithSettings(true, webrtc.Configuration{ICEServers: iceServers()}, se, nil)
}

type compactCand struct {
	typ  byte
	ip   net.IP
	port uint16
}

func compactPack(answer bool, seed []byte, payload []byte) (string, error) {
	var sd webrtc.SessionDescription
	if err := json.Unmarshal(payload, &sd); err != nil {
		return "", err
	}
	var fp []byte
	setup := byte(0)
	var cands []compactCand
	seen := map[string]bool{}
	for _, line := range strings.Split(sd.SDP, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "a=fingerprint:sha-256 "):
			fp, _ = hex.DecodeString(strings.ReplaceAll(strings.TrimPrefix(line, "a=fingerprint:sha-256 "), ":", ""))
		case strings.HasPrefix(line, "a=setup:"):
			for i, n := range setupNames {
				if strings.TrimPrefix(line, "a=setup:") == n {
					setup = byte(i)
				}
			}
		case strings.HasPrefix(line, "a=candidate:"):
			f := strings.Fields(strings.TrimPrefix(line, "a=candidate:"))
			if len(f) < 8 || f[1] != "1" || !strings.EqualFold(f[2], "udp") {
				continue
			}
			ip := net.ParseIP(f[4]).To4()
			var port uint16
			if _, err := fmt.Sscanf(f[5], "%d", &port); err != nil || ip == nil || ip.IsLinkLocalUnicast() {
				continue
			}
			t := -1
			for i, n := range candTypes {
				if f[7] == n {
					t = i
				}
			}
			key := fmt.Sprintf("%s:%d", ip, port)
			if t < 0 || seen[key] {
				continue
			}
			seen[key] = true
			cands = append(cands, compactCand{byte(t), ip, port})
		}
	}
	if len(fp) != 32 {
		return "", errors.New("compact: no sha-256 fingerprint")
	}
	// Public (srflx/relay) candidates first: they matter across the internet.
	var ordered []compactCand
	for _, want := range []byte{1, 2, 0} {
		for _, c := range cands {
			if c.typ == want && len(ordered) < 5 {
				ordered = append(ordered, c)
			}
		}
	}
	if len(ordered) == 0 {
		return "", errors.New("compact: no usable candidates")
	}
	head := byte(compactVersion<<4) | setup<<1
	if answer {
		head |= 1
	}
	b := []byte{head}
	b = append(b, seed...)
	b = append(b, fp...)
	b = append(b, byte(len(ordered)))
	for _, c := range ordered {
		b = append(b, c.typ)
		b = append(b, c.ip...)
		b = binary.BigEndian.AppendUint16(b, c.port)
	}
	tag := compactOfferTag
	if answer {
		tag = compactAnswerTag
	}
	return tag + base64.RawURLEncoding.EncodeToString(b), nil
}

// compactUnpack finds a DAVET-/YANIT- code in s and rebuilds the JSON
// SessionDescription payload that Peer.AcceptOffer / AddSignal expect.
func compactUnpack(wantAnswer bool, s string) ([]byte, bool) {
	s = strings.Join(strings.Fields(s), "")
	tag := compactOfferTag
	if wantAnswer {
		tag = compactAnswerTag
	}
	i := strings.Index(s, tag)
	if i < 0 {
		return nil, false
	}
	body := s[i+len(tag):]
	if j := strings.IndexFunc(body, func(r rune) bool {
		return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}); j >= 0 {
		body = body[:j]
	}
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(b) < 1+6+32+1 || b[0]>>4 != compactVersion || (b[0]&1 == 1) != wantAnswer {
		return nil, false
	}
	setup := int(b[0]>>1) & 3
	if setup >= len(setupNames) {
		return nil, false
	}
	seed, fp := b[1:7], b[7:39]
	n := int(b[39])
	rest := b[40:]
	if n == 0 || len(rest) != n*7 {
		return nil, false
	}
	u, p := iceCreds(seed)
	fps := make([]string, 32)
	for k, x := range fp {
		fps[k] = fmt.Sprintf("%02X", x)
	}
	var sb strings.Builder
	w := func(l string) { sb.WriteString(l); sb.WriteString("\r\n") }
	w("v=0")
	w(fmt.Sprintf("o=- %d 2 IN IP4 127.0.0.1", binary.BigEndian.Uint32(seed[:4])))
	w("s=-")
	w("t=0 0")
	w("a=fingerprint:sha-256 " + strings.Join(fps, ":"))
	w("a=group:BUNDLE 0")
	w("m=application 9 UDP/DTLS/SCTP webrtc-datachannel")
	w("c=IN IP4 0.0.0.0")
	w("a=setup:" + setupNames[setup])
	w("a=mid:0")
	w("a=sendrecv")
	w("a=sctp-port:5000")
	w("a=ice-ufrag:" + u)
	w("a=ice-pwd:" + p)
	for k := 0; k < n; k++ {
		c := rest[k*7:]
		t := int(c[0])
		if t >= len(candTypes) {
			return nil, false
		}
		ip := net.IPv4(c[1], c[2], c[3], c[4])
		port := binary.BigEndian.Uint16(c[5:7])
		line := fmt.Sprintf("a=candidate:%d 1 udp %d %s %d typ %s", k+1, candPrio[t]-uint32(k), ip, port, candTypes[t])
		if t != 0 {
			line += " raddr 0.0.0.0 rport 0"
		}
		w(line)
	}
	w("a=end-of-candidates")
	typ := webrtc.SDPTypeOffer
	if wantAnswer {
		typ = webrtc.SDPTypeAnswer
	}
	out, err := json.Marshal(webrtc.SessionDescription{Type: typ, SDP: sb.String()})
	return out, err == nil
}
