package landisc

import "testing"

func TestResponseFor(t *testing.T) {
	if got := ResponseFor("10.0.0.5"); got != "REMOTESUPPORT_SIGNALING_V1 ws://10.0.0.5:8091/v1/ws" {
		t.Fatalf("got %q", got)
	}
}

func TestTrimSpace(t *testing.T) {
	if string(trimSpace([]byte("  REMOTESUPPORT_DISCOVER_V1\n"))) != Request {
		t.Fatal("trimSpace")
	}
}
