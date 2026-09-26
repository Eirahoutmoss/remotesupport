package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEncodeDecodeJSON(t *testing.T) {
	in := ScreenInfo{Monitors: []Monitor{{X: -1920, Y: 0, W: 1920, H: 1080, Primary: true}}, Active: 0}
	p, err := Encode(MsgScreenInfo, in)
	if err != nil {
		t.Fatal(err)
	}
	typ, body, err := Decode(p)
	if err != nil || typ != MsgScreenInfo {
		t.Fatalf("typ=%d err=%v", typ, err)
	}
	var out ScreenInfo
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Monitors) != 1 || out.Monitors[0] != in.Monitors[0] {
		t.Fatalf("got %+v", out)
	}
}

func TestEncodeRawAndNil(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff}
	p, _ := Encode(MsgFrame, jpeg)
	typ, body, _ := Decode(p)
	if typ != MsgFrame || !bytes.Equal(body, jpeg) {
		t.Fatal("raw payload changed")
	}
	p, _ = Encode(MsgBye, nil)
	if typ, body, _ := Decode(p); typ != MsgBye || len(body) != 0 {
		t.Fatal("nil payload")
	}
}

func TestDecodeEmpty(t *testing.T) {
	if _, _, err := Decode(nil); err == nil {
		t.Fatal("empty message accepted")
	}
}

func TestMalformedJSONRejected(t *testing.T) {
	var e InputEvent
	for _, b := range []string{`{"t":`, `[]`, `{"x":"a"}`} {
		if json.Unmarshal([]byte(b), &e) == nil {
			t.Errorf("accepted %s", b)
		}
	}
}

func TestInputValidate(t *testing.T) {
	ok := []InputEvent{
		{T: "move", X: 0.5, Y: 1},
		{T: "down", B: 2},
		{T: "wheel", D: -120},
		{T: "keydown", K: 0x41},
	}
	for _, e := range ok {
		if err := e.Validate(); err != nil {
			t.Errorf("%+v: %v", e, err)
		}
	}
	bad := []InputEvent{
		{T: ""},
		{T: "exec"},
		{T: "move", X: 1.5},
		{T: "move", Y: -0.1},
		{T: "down", B: 3},
		{T: "wheel", D: 1 << 20},
		{T: "keydown", K: 0},
		{T: "keyup", K: 999},
	}
	for _, e := range bad {
		if e.Validate() == nil {
			t.Errorf("%+v accepted", e)
		}
	}
}
