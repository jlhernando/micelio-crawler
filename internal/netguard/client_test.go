package netguard

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPClientRefusesPrivateAddress(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	_, err := HTTPClient(5 * time.Second).Get(srv.URL)
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BlockedError", err)
	}
	if hits.Load() != 0 {
		t.Fatal("loopback server was reached")
	}
}

// HTTPClient honours the environment proxy (some machines have no other
// way out), guarded by ProxyTransport.
func TestHTTPClientUsesGuardedEnvironmentProxy(t *testing.T) {
	pt, ok := HTTPClient(time.Second).Transport.(*ProxyTransport)
	if !ok || pt.proxy == nil {
		t.Fatal("transport ignores the environment proxy")
	}
}

func TestBlockedIP(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":                     false,
		"127.0.0.1":                   true,
		"10.1.2.3":                    true,
		"169.254.169.254":             true,
		"100.64.0.1":                  true,
		"0.0.0.0":                     true,
		"2606:4700::1111":             false,
		"::1":                         true,
		"fe80::1":                     true,
		"fc00::1":                     true,
		"::ffff:127.0.0.1":            true,
		"64:ff9b::7f00:1":             true,  // NAT64 of 127.0.0.1
		"64:ff9b::a9fe:a9fe":          true,  // NAT64 of 169.254.169.254
		"64:ff9b::808:808":            false, // NAT64 of 8.8.8.8
		"64:ff9b:1::808:808":          true,  // local-use NAT64
		"2002:7f00:1::":               true,  // 6to4 of 127.0.0.1
		"2002:c0a8:101::1":            true,  // 6to4 of 192.168.1.1
		"2002:808:808::1":             false, // 6to4 of 8.8.8.8
		"2001:0:4136:e378::80ff:fffe": true,  // Teredo, client 127.0.0.1
		"2001:0:4136:e378::f7f7:f7f7": false, // Teredo, client 8.8.8.8
		"fec0::1":                     true,
		"feff::1":                     true,
	}
	for s, want := range cases {
		if got := BlockedIP(net.ParseIP(s)); got != want {
			t.Errorf("BlockedIP(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestCheckRedirect(t *testing.T) {
	req := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	for _, raw := range []string{
		"http://127.0.0.1/", "http://localhost:8080/", "http://10.1.2.3/",
		"http://169.254.169.254/latest/meta-data", "http://[::1]/", "file:///etc/passwd",
	} {
		if err := CheckRedirect(req(raw), nil); err == nil {
			t.Errorf("redirect to %s allowed", raw)
		}
	}
	if err := CheckRedirect(req("https://example.com/hook"), nil); err != nil {
		t.Errorf("public redirect refused: %v", err)
	}
	via := make([]*http.Request, maxRedirects)
	if err := CheckRedirect(req("https://example.com/"), via); err == nil {
		t.Error("redirect limit not enforced")
	}
}
