package alerts

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// A public webhook that redirects to an internal service must not make
// alert delivery reach that service.
func TestAlertWebhookDoesNotFollowRedirectToPrivate(t *testing.T) {
	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHits.Add(1)
	}))
	defer internal.Close()

	summary := &types.AlertSummary{Alerts: []types.AlertResult{{ID: "x", Message: "m"}}}

	// The delivery client itself refuses loopback, even with no redirect.
	sendWebhook(internal.URL, summary, "https://example.com/")
	if n := internalHits.Load(); n != 0 {
		t.Fatalf("alert webhook reached loopback directly (%d hits)", n)
	}

	// A redirect from an allowed hop to a private address is refused too.
	// The first hop is loopback here, so allow it by host while keeping the
	// redirect check of the real client.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	old := newSafeClient
	newSafeClient = func(timeout time.Duration) *http.Client {
		c := old(timeout)
		c.Transport = http.DefaultTransport
		return c
	}
	defer func() { newSafeClient = old }()
	sendWebhook(redirector.URL, summary, "https://example.com/")
	if n := internalHits.Load(); n != 0 {
		t.Fatalf("alert webhook followed a redirect to loopback (%d hits)", n)
	}
}
