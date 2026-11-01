package crawler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/netguard"
)

// Every address that reaches this computer or its networks is blocked,
// both as a literal in a URL and after DNS resolution.
func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"10.0.0.1", "172.16.5.4", "192.168.1.1", "127.0.0.1", "127.8.8.8",
		"169.254.169.254", "100.64.0.1", "100.127.255.254", // CGNAT / Tailscale
		"0.0.0.0", "0.1.2.3", "224.0.0.1", "255.255.255.255",
		"::", "::1", "fd00::1", "fe80::1", "ff02::1", "::ffff:192.168.1.1", "::ffff:127.0.0.1",
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.1", "2001:4860:4860::8888"}
	for _, s := range blocked {
		if !blockedIP(net.ParseIP(s)) {
			t.Errorf("blockedIP(%s) = false, want true", s)
		}
	}
	for _, s := range allowed {
		if blockedIP(net.ParseIP(s)) {
			t.Errorf("blockedIP(%s) = true, want false", s)
		}
	}
}

func TestIsPrivateURLLiterals(t *testing.T) {
	for _, u := range []string{
		"http://100.64.0.1/", "http://[fd00::1]/", "http://0.0.0.0:8080/", "http://0.1.2.3/",
		"http://[::ffff:127.0.0.1]/", "http://foo.localhost/", "http://LOCALHOST/", "http://localhost./",
	} {
		if !IsPrivateURL(u) {
			t.Errorf("IsPrivateURL(%s) = false, want true", u)
		}
	}
	for _, u := range []string{"https://example.com/", "http://8.8.8.8/", "https://localhost.example.com/", "https://fdic.gov/", "https://fcbarcelona.com/", "https://fe80.example/"} {
		if IsPrivateURL(u) {
			t.Errorf("IsPrivateURL(%s) = true, want false", u)
		}
	}
}

// localTarget is a server on 127.0.0.1 reached through the host name
// "localhost": the literal URL check cannot see it, only the dial-time one.
func localTarget(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "<urlset></urlset>")
	}))
	t.Cleanup(srv.Close)
	return strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), &hits
}

// Stealth mode uses its own HTTP stack: it must still refuse private
// addresses when it connects.
func TestStealthClientBlocksPrivateAddresses(t *testing.T) {
	target, hits := localTarget(t)
	client, err := StealthClient(5*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := client.Get(target + "/"); err == nil {
		resp.Body.Close()
		t.Error("stealth client reached a loopback address")
	}
	if hits.Load() != 0 {
		t.Errorf("loopback server got %d requests", hits.Load())
	}
}

// A sitemap fetched without a client (the fallback) is protected too.
func TestSitemapFallbackClientBlocksPrivateAddresses(t *testing.T) {
	target, hits := localTarget(t)
	err := fetchSitemap(context.Background(), target+"/sitemap.xml", nil, "test", nil, 0, func([]string) {})
	if err == nil {
		t.Error("sitemap fallback client reached a loopback address")
	}
	if hits.Load() != 0 {
		t.Errorf("loopback server got %d requests", hits.Load())
	}
}

// SafeDialer checks the address it really connects to.
func TestSafeDialerBlocksResolvedPrivateAddress(t *testing.T) {
	target, _ := localTarget(t)
	d := SafeDialer(5 * time.Second)
	host := strings.TrimPrefix(target, "http://")
	if c, err := d.DialContext(context.Background(), "tcp", host); err == nil {
		c.Close()
		t.Errorf("SafeDialer connected to %s", host)
	}
}

// A refused private connection is final: retrying only wastes time.
var netguardBlocked = netguard.BlockedError{Addr: "::1"}

func TestBlockedConnectionsAreNotRetried(t *testing.T) {
	ctx := context.Background()
	for _, err := range []error{
		&ssrfBlockedError{msg: "DNS rebinding blocked: x resolved to private IP 10.0.0.1"},
		fmt.Errorf("wrapped: %w", &netguardBlocked),
		fmt.Errorf("error fetching response: dial tcp [::1]:80: blocked connection to private address ::1"),
	} {
		if isRetryableFetchErr(ctx, err) {
			t.Errorf("isRetryableFetchErr(%v) = true", err)
		}
	}
}
