package netguard

import (
	"net"
	"strings"
	"sync"
	"testing"
)

// Fake-IP proxies (Clash, Surge...) resolve every name into 198.18.0.0/15.
// It stays blocked by default, with a hint in the error; MICELIO_ALLOW_FAKE_IP
// allows that range only.
func TestFakeIPRange(t *testing.T) {
	t.Cleanup(func() { SetAllowFakeIP(false) })
	SetAllowFakeIP(false)
	if !BlockedIP(net.ParseIP("198.18.0.42")) {
		t.Fatal("198.18.0.42 must be blocked by default")
	}
	msg := (&BlockedError{Addr: "198.18.0.42"}).Error()
	for _, want := range []string{"198.18.0.42", "198.18.0.0/15", "fake-IP", "Clash", AllowFakeIPEnv + "=1", "redir-host"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	if msg := (&BlockedError{Addr: "10.0.0.1"}).Error(); strings.Contains(msg, "fake-IP") {
		t.Errorf("hint on a non fake-IP address: %q", msg)
	}

	SetAllowFakeIP(true)
	for _, s := range []string{"198.18.0.42", "198.19.255.255", "::ffff:198.18.0.1"} {
		if BlockedIP(net.ParseIP(s)) {
			t.Errorf("BlockedIP(%s) = true with the fake-IP opt-in", s)
		}
	}
	for _, s := range []string{
		"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "192.0.2.1", "203.0.113.7", "240.0.0.1",
		"2001:db8::1", "fc00::1",
		"64:ff9b::198.18.0.1", "2002:c612:1::", "::ffff:0:198.18.0.1", "::198.18.0.1", // transition forms stay blocked
	} {
		if !BlockedIP(net.ParseIP(s)) {
			t.Errorf("BlockedIP(%s) = false with the fake-IP opt-in, want true", s)
		}
	}
	if BlockedIP(net.ParseIP("8.8.8.8")) {
		t.Error("8.8.8.8 blocked")
	}
}

func TestFakeIPEnv(t *testing.T) {
	t.Cleanup(func() { SetAllowFakeIP(false) })
	for v, want := range map[string]bool{"1": true, "true": true, "": false, "0": false, "nope": false} {
		t.Setenv(AllowFakeIPEnv, v)
		fakeIPOnce = sync.Once{}
		if got := AllowFakeIP(); got != want {
			t.Errorf("%s=%q: AllowFakeIP() = %v, want %v", AllowFakeIPEnv, v, got, want)
		}
	}
}
