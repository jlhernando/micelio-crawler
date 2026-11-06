package netguard

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// trustedClient is pinnedClient with the default proxy dialer (nil), as
// production clients use for a proxy the local user configured.
func trustedClient(o *origin, proxy *url.URL, dns *rebindingDNS) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialContext = Dialer(5 * time.Second).DialContext
	if o != nil && o.TLS != nil {
		base.TLSClientConfig = o.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	}
	return &http.Client{Timeout: 5 * time.Second,
		Transport: NewProxyTransport(base, http.ProxyURL(proxy), nil, dns.lookup)}
}

// A proxy the local user configured on 127.0.0.1 (a forward proxy, Tor)
// is reached by default: before, the guarded dialer refused the proxy's
// own address and every request through a local proxy failed.
func TestTrustedLocalProxyIsReached(t *testing.T) {
	for _, socks := range []bool{false, true} {
		o := startOrigin(t, true)
		var opts []func(*fakeProxy)
		if socks {
			opts = append(opts, socksProxy)
		}
		p := startFakeProxy(t, o.Listener.Addr().String(), opts...)
		c := trustedClient(o, p.url(""), &rebindingDNS{})
		if err := get(t, c, "https://example.com/"); err != nil {
			t.Fatalf("socks=%v: request through local proxy: %v", socks, err)
		}
		if p.conns.Load() == 0 {
			t.Fatalf("socks=%v: local proxy never reached", socks)
		}
	}
}

// Targets reached through a trusted local proxy keep the full check: a
// private target (literal, localhost, or a name with a private address)
// never reaches the proxy.
func TestTrustedLocalProxyStillBlocksPrivateTargets(t *testing.T) {
	p := startFakeProxy(t, "127.0.0.1:9")
	c := trustedClient(nil, p.url(""), &rebindingDNS{})
	for _, raw := range []string{
		"http://127.0.0.1/", "https://10.0.0.1/", "http://169.254.169.254/latest/meta-data",
		"http://localhost/", "https://internal.example/",
	} {
		if err := get(t, c, raw); err == nil {
			t.Errorf("%s: allowed through the trusted proxy", raw)
		}
	}
	if n := p.conns.Load(); n != 0 {
		t.Fatalf("proxy reached %d times for private targets", n)
	}
}

// The guarded Dialer still refuses a proxy on a private address: callers
// that cannot vouch for the proxy (not configured by the local user) pass
// it as dialProxy.
func TestUntrustedPrivateProxyRefused(t *testing.T) {
	p := startFakeProxy(t, "127.0.0.1:9")
	dial, err := DialThrough(p.url(""), Dialer(5*time.Second).DialContext, (&rebindingDNS{}).lookup)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dial(context.Background(), "tcp", "example.com:443")
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BlockedError for the proxy address", err)
	}
	if p.conns.Load() != 0 {
		t.Fatal("private proxy reached through the guarded dialer")
	}
}

// Chrome's guard proxy chains to a local upstream with the default dialer.
func TestStartProxyViaReachesLocalUpstream(t *testing.T) {
	dns := &rebindingDNS{}
	old := lookupIPAddr
	lookupIPAddr = dns.lookup
	defer func() { lookupIPAddr = old }()

	o := startOrigin(t, true)
	up := startFakeProxy(t, o.Listener.Addr().String())
	g, err := StartProxyVia(up.url(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	gu, _ := url.Parse("http://" + g.Addr())
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(gu),
		TLSClientConfig: o.Client().Transport.(*http.Transport).TLSClientConfig.Clone()}}
	if err := get(t, c, "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	if lines, _, _ := up.seen(); len(lines) != 1 || lines[0] != "CONNECT "+publicIP+":443 HTTP/1.1" {
		t.Fatalf("upstream saw %q", lines)
	}
	if err := get(t, c, "http://127.0.0.1:1/"); err == nil {
		t.Fatal("loopback target allowed through the guard proxy")
	}
}
