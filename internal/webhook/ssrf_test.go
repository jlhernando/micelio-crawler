package webhook

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/netguard"
)

// With HTTP(S)_PROXY set, webhook delivery uses the proxy, but only after
// the target is checked, and the proxy only gets the checked IP (the
// dialer only sees the proxy's address).
func TestWebhookClientGuardsEnvironmentProxy(t *testing.T) {
	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")

	c := crawler.SafeClientFollowRedirects(5 * time.Second)
	if _, ok := c.Transport.(*netguard.ProxyTransport); !ok {
		t.Fatalf("webhook transport %T does not guard the environment proxy", c.Transport)
	}
	if Send(Options{URL: "http://internal.example.invalid/hook"}, Payload{Event: "x"}) {
		t.Fatal("webhook to an unresolvable host delivered through the proxy")
	}
	if proxyHits.Load() != 0 {
		t.Fatal("webhook request went to the environment proxy")
	}
}

// Every redirect hop is checked, not only the first URL.
func TestWebhookClientRefusesRedirectToPrivate(t *testing.T) {
	c := crawler.SafeClientFollowRedirects(5 * time.Second)
	for _, target := range []string{"http://127.0.0.1:9/", "http://169.254.169.254/latest/meta-data", "http://localhost/"} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		if err := c.CheckRedirect(req, []*http.Request{{}}); err == nil {
			t.Errorf("redirect to %s allowed", target)
		}
	}
}
