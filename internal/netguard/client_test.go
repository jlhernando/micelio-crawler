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

// Special-purpose ranges that are not globally reachable (IANA registries,
// RFC 6890), and their IPv6 transition forms. Mirrors src/net-guard.test.ts.
func TestBlockedIPSpecialPurpose(t *testing.T) {
	blocked := []string{
		"0.1.2.3",
		"192.0.0.1", "192.0.0.255", // IETF protocol assignments
		"192.0.2.1", "198.51.100.7", "203.0.113.7", // documentation
		"192.88.99.1",                  // deprecated 6to4 relay anycast
		"198.18.0.1", "198.19.255.255", // benchmarking 198.18.0.0/15
		"240.0.0.1", "254.1.2.3", "255.255.255.255", // reserved 240/4, broadcast
		"2001:db8::1", "2001:db8:ffff::1", // documentation 2001:db8::/32
		"3fff::1", "3fff:fff::1", // documentation 3fff::/20
		"2001:2::1", "2001:10::1", "2001:20::1", "2001:1ff::1", // 2001::/23
		"100::1", "100::ffff:ffff:ffff:ffff", // discard 100::/64
		"5f00::1", "4000::1", "1::1", "e000::1", // outside 2000::/3
		"fc00::1", "fdff::1", "fe80::1", "fec0::1", "ff0e::1",
		// Transition forms decoded to a special IPv4 address.
		"::ffff:198.18.0.1", "::ffff:192.0.2.1", "::ffff:240.0.0.1",
		"::ffff:0:127.0.0.1", "::ffff:0:10.0.0.1", "::ffff:0:198.18.0.1", "::ffff:0:0:0", // IPv4-translatable
		"::127.0.0.1", "::10.0.0.1", "::198.18.0.1", "::", "::1", // IPv4-compatible
		"64:ff9b::198.18.0.1", "64:ff9b::192.0.2.1", "64:ff9b::240.0.0.1",
		"64:ff9b:1::8.8.8.8",
		"2002:c612:1::", "2002:c000:201::", "2002:f000:1::", // 6to4
		"2001:0:4136:e378::39ed:fffe", // Teredo, client 198.18.0.1
	}
	for _, s := range blocked {
		if !BlockedIP(net.ParseIP(s)) {
			t.Errorf("BlockedIP(%s) = false, want true", s)
		}
	}
	allowed := []string{
		"8.8.8.8", "1.1.1.1", "93.184.215.14", "198.17.255.255", "198.20.0.1", "192.0.3.1", "223.255.255.255",
		"2606:4700::1111", "2a00:1450:4001::1", "3fff:1000::1", "2001:db9::1", "2001:200::1", "2001:4860:4860::8888",
		"::ffff:8.8.8.8", "::ffff:0:808:808", "::8.8.8.8", "64:ff9b::8.8.8.8", "2002:808:808::1",
	}
	for _, s := range allowed {
		if BlockedIP(net.ParseIP(s)) {
			t.Errorf("BlockedIP(%s) = true, want false", s)
		}
	}
	if !BlockedIP(nil) || !BlockedIP(net.IP{1, 2, 3}) {
		t.Error("nil or malformed IP must be blocked")
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
