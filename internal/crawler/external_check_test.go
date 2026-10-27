package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestCheckExternalLinks_ReportsBrokenOnly(t *testing.T) {
	var gotUA atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA.Store(r.UserAgent())
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(200)
		case "/gone":
			w.WriteHeader(404)
		case "/head-not-allowed":
			if r.Method == http.MethodHead {
				w.WriteHeader(405)
				return
			}
			w.WriteHeader(200)
		case "/redirect-to-gone":
			http.Redirect(w, r, "/gone", http.StatusMovedPermanently)
		case "/server-error":
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()

	links := map[string][]string{
		srv.URL + "/ok":               {"https://site.test/a"},
		srv.URL + "/gone":             {"https://site.test/a", "https://site.test/a", "https://site.test/b"},
		srv.URL + "/head-not-allowed": {"https://site.test/c"},
		srv.URL + "/redirect-to-gone": {"https://site.test/d"},
		srv.URL + "/server-error":     {"https://site.test/e"},
		"mailto:someone@example.com":  {"https://site.test/f"},
	}

	sum := CheckExternalLinks(context.Background(), links, ExternalCheckOptions{
		UserAgent: "TestBot/2.0", Concurrency: 3, SkipSSRF: true,
	})

	if sum.Total != 5 || sum.Checked != 5 || sum.Skipped != 0 {
		t.Fatalf("Total/Checked/Skipped = %d/%d/%d, want 5/5/0", sum.Total, sum.Checked, sum.Skipped)
	}
	got := map[string]int{}
	for _, b := range sum.Broken {
		got[strings.TrimPrefix(b.URL, srv.URL)] = b.StatusCode
	}
	want := map[string]int{"/gone": 404, "/redirect-to-gone": 404, "/server-error": 503}
	if len(got) != len(want) {
		t.Fatalf("broken = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("broken[%s] = %d, want %d", k, got[k], v)
		}
	}
	for _, b := range sum.Broken {
		if strings.HasSuffix(b.URL, "/gone") && len(b.FoundOn) != 2 {
			t.Errorf("FoundOn not deduped: %v", b.FoundOn)
		}
	}
	if ua, _ := gotUA.Load().(string); ua != "TestBot/2.0" {
		t.Errorf("User-Agent = %q, want TestBot/2.0", ua)
	}
}

func TestCheckExternalLinks_CapAndConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	deadURL := srv.URL
	srv.Close() // closed server -> connection refused

	links := map[string][]string{
		deadURL + "/most-linked": {"p1", "p2", "p3"},
		deadURL + "/less-linked": {"p1"},
	}
	sum := CheckExternalLinks(context.Background(), links, ExternalCheckOptions{MaxURLs: 1, SkipSSRF: true})
	if sum.Total != 2 || sum.Checked != 1 || sum.Skipped != 1 {
		t.Fatalf("Total/Checked/Skipped = %d/%d/%d, want 2/1/1", sum.Total, sum.Checked, sum.Skipped)
	}
	if len(sum.Broken) != 1 || !strings.HasSuffix(sum.Broken[0].URL, "/most-linked") {
		t.Fatalf("expected most-linked URL to be checked first, got %+v", sum.Broken)
	}
	if sum.Broken[0].StatusCode != 0 || sum.Broken[0].Error == "" {
		t.Errorf("expected connection error result, got %+v", sum.Broken[0])
	}
}

func TestCheckExternalLinks_Cancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sum := CheckExternalLinks(ctx, map[string][]string{"https://example.com/x": {"p"}}, ExternalCheckOptions{SkipSSRF: true})
	if sum.Checked != 0 || len(sum.Broken) != 0 || sum.Skipped != 1 {
		t.Fatalf("cancelled check should do nothing, got %+v", sum)
	}
}

func TestCheckExternalLinks_BlocksPrivateWithoutSkipSSRF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	sum := CheckExternalLinks(context.Background(), map[string][]string{srv.URL + "/": {"p"}}, ExternalCheckOptions{})
	if len(sum.Broken) != 1 || sum.Broken[0].Error == "" {
		t.Fatalf("loopback URL should be blocked by SSRF protection, got %+v", sum.Broken)
	}
}

func TestCheckExternalStatus_RetriesGETAfterHEAD404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(404) // CDN that only rejects HEAD
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	status, err := checkExternalStatus(context.Background(), srv.Client(), srv.URL+"/asset", "test-agent")
	if err != nil || status != 200 {
		t.Errorf("status = %d, err = %v; want 200 from the GET retry", status, err)
	}
}

// Many links to one slow site must not hold every worker while they wait
// for that site's two slots: other sites are checked meanwhile.
func TestCheckExternalLinks_SlowHostDoesNotBlockOthers(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
	}))
	defer slow.Close()
	start := time.Now()
	var lastFast atomic.Int64
	links := map[string][]string{}
	for i := 0; i < 20; i++ { // most linked: dispatched first
		links[fmt.Sprintf("%s/s%d", slow.URL, i)] = []string{"a", "b", "c"}
	}
	for i := 0; i < 8; i++ {
		fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lastFast.Store(int64(time.Since(start)))
		}))
		defer fast.Close()
		links[fast.URL+"/f"] = []string{"a"}
	}
	sum := CheckExternalLinks(context.Background(), links, ExternalCheckOptions{Concurrency: 20, SkipSSRF: true})
	if sum.Checked != 28 || len(sum.Broken) != 0 {
		t.Fatalf("checked %d, broken %d", sum.Checked, len(sum.Broken))
	}
	// The slow site alone needs 20/2 × 150 ms = 1.5 s.
	if d := time.Duration(lastFast.Load()); d > 600*time.Millisecond {
		t.Errorf("other sites finished after %s: they waited behind the slow one", d)
	}
}

// When a site refuses connections, its other URLs are reported with the
// same error without a request each.
func TestCheckExternalLinks_DeadSiteShortCircuits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	dead := srv.URL
	srv.Close()
	links := map[string][]string{}
	for i := 0; i < 30; i++ {
		links[fmt.Sprintf("%s/p%d", dead, i)] = []string{"a"}
	}
	sum := CheckExternalLinks(context.Background(), links, ExternalCheckOptions{SkipSSRF: true})
	if sum.Checked != 30 || len(sum.Broken) != 30 {
		t.Fatalf("checked %d, broken %d, want 30/30", sum.Checked, len(sum.Broken))
	}
	for _, b := range sum.Broken {
		if !strings.Contains(b.Error, "refused") {
			t.Fatalf("error = %q", b.Error)
		}
	}
	if hostUnreachable(&url.Error{Op: "Head", URL: "https://other.test/x", Err: syscall.ECONNREFUSED}, "https://site.test") {
		t.Error("a redirect target's failure marked the linking site dead")
	}
}

func TestHostLimit(t *testing.T) {
	for host, want := range map[string]int{
		"github.com": 6, "gist.github.com": 6, "en.wikipedia.org": 6, "x.com:443": 6,
		"notgithub.com": 2, "example.com": 2, "github.com.evil.test": 2,
	} {
		if got := hostLimit(host, 2); got != want {
			t.Errorf("hostLimit(%q) = %d, want %d", host, got, want)
		}
	}
}
