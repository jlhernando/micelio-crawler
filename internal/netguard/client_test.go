package netguard

import (
	"context"
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

// With a proxy in use, the dialer only sees the proxy, so the target is
// checked before the request is handed to it: literal private hosts and
// names resolving to a private address never reach the proxy.
func TestGuardProxyChecksTarget(t *testing.T) {
	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyHits.Add(1) }))
	defer proxy.Close()
	pu, _ := url.Parse(proxy.URL)

	old := lookupIPAddr
	defer func() { lookupIPAddr = old }()
	lookupIPAddr = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "internal.example":
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.5")}}, nil
		case "public.example":
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}

	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: GuardProxy(http.ProxyURL(pu))}}
	for _, raw := range []string{
		"http://127.0.0.1/", "http://169.254.169.254/latest/meta-data", "http://localhost/",
		"http://[::1]/", "http://internal.example/", "http://missing.example/",
	} {
		if _, err := c.Get(raw); err == nil {
			t.Errorf("%s: request allowed through the proxy", raw)
		}
	}
	if proxyHits.Load() != 0 {
		t.Fatalf("proxy reached %d times for blocked targets", proxyHits.Load())
	}
	resp, err := c.Get("http://public.example/")
	if err != nil {
		t.Fatalf("public target refused: %v", err)
	}
	resp.Body.Close()
	if proxyHits.Load() != 1 {
		t.Fatal("public target did not go through the proxy")
	}
}

// HTTPClient honours the environment proxy (some machines have no other
// way out), guarded by GuardProxy.
func TestHTTPClientUsesGuardedEnvironmentProxy(t *testing.T) {
	if HTTPClient(time.Second).Transport.(*http.Transport).Proxy == nil {
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
