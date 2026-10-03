package signaling

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestV2HandshakeIdentityAndTURN(t *testing.T) {
	s := NewServer()
	defer s.Close()
	s.SetTURN([]string{"turn:203.0.113.5:3478"}, "sır", time.Hour)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	target := dialServer(t, ts)
	defer target.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, target, Message{Type: "create", V: ProtocolVersion})
	created := readMessage(t, target)
	if created.V != ProtocolVersion || len(created.Features) != 1 || created.Features[0] != FeatureTURNCred {
		t.Fatalf("created lacks version/features: %+v", created)
	}

	operator := dialServer(t, ts)
	defer operator.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, operator, Message{Type: "join", Code: created.Code, V: ProtocolVersion, Name: "TEKNİK-PC (hasan)\r\nSahte satır"})
	if got := readMessage(t, operator); got.Type != "joined" || got.V != ProtocolVersion {
		t.Fatalf("joined: %+v", got)
	}
	pj := readMessage(t, target)
	if pj.Type != "peer_joined" || pj.Code != created.Code || pj.From == "" {
		t.Fatalf("peer_joined: %+v", pj)
	}
	if strings.ContainsAny(pj.Name, "\r\n") || !strings.HasPrefix(pj.Name, "TEKNİK-PC (hasan)") {
		t.Fatalf("name not sanitized: %q", pj.Name)
	}

	writeMessage(t, target, Message{Type: "approve", Approved: true})
	ice := readMessage(t, target)
	if ice.Type != "ice" || ice.ICE == nil {
		t.Fatalf("target expected ice, got %+v", ice)
	}
	ap := readMessage(t, operator)
	if ap.Type != "approved" || ap.ICE == nil || ap.ICE.Username != ice.ICE.Username {
		t.Fatalf("approved: %+v", ap)
	}
	m := hmac.New(sha1.New, []byte("sır"))
	m.Write([]byte(ap.ICE.Username))
	if base64.StdEncoding.EncodeToString(m.Sum(nil)) != ap.ICE.Credential {
		t.Fatal("credential is not HMAC-SHA1(secret, username)")
	}
	if ap.ICE.ExpiresAt <= time.Now().Unix() {
		t.Fatal("credential already expired")
	}
}

func TestOldClientGetsNoTURNOrICEMessage(t *testing.T) {
	s := NewServer()
	defer s.Close()
	s.SetTURN([]string{"turn:203.0.113.5:3478"}, "sır", time.Hour)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	target := dialServer(t, ts)
	defer target.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, target, Message{Type: "create"})
	created := readMessage(t, target)
	operator := dialServer(t, ts)
	defer operator.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, operator, Message{Type: "join", Code: created.Code})
	readMessage(t, operator)
	readMessage(t, target)
	writeMessage(t, target, Message{Type: "approve", Approved: true})
	if ap := readMessage(t, operator); ap.Type != "approved" || ap.ICE != nil {
		t.Fatalf("old operator got %+v", ap)
	}
}

func TestMinClientVersion(t *testing.T) {
	s := NewServer()
	defer s.Close()
	s.SetMinClientVersion(2)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	c := dialServer(t, ts)
	defer c.Close(websocket.StatusNormalClosure, "")
	writeMessage(t, c, Message{Type: "create", V: 1})
	if got := readMessage(t, c); got.Type != "error" || got.Reason != "client_outdated" {
		t.Fatalf("got %+v", got)
	}
}

func TestCreateRateLimit(t *testing.T) {
	s := NewServer()
	defer s.Close()
	// Exercise the limiter directly: httptest connections come from loopback,
	// which is exempt so the embedded LAN server never limits itself.
	for i := 0; i < defaultCreates; i++ {
		if s.createRateLimited("198.51.100.7") {
			t.Fatalf("limited too early at %d", i)
		}
	}
	if !s.createRateLimited("198.51.100.7") {
		t.Fatal("expected limit")
	}
	if s.createRateLimited("127.0.0.1") {
		t.Fatal("loopback must be exempt")
	}
}

func TestCleanName(t *testing.T) {
	if got := CleanName("a‮b\x00c"); got != "a b c" {
		t.Fatalf("got %q", got)
	}
	if got := CleanName(strings.Repeat("ş", 100)); len([]rune(got)) != maxNameRunes {
		t.Fatalf("len %d", len([]rune(got)))
	}
}
