package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/extract"
	"github.com/micelio/micelio/internal/types"
)

// url_explain must never contradict the crawler. For every combination of
// status, robots directives and canonical form, the page gets the
// crawler's own verdict, and explain's answer must agree with it:
//   - verdict "indexable" only when the crawler says indexable, and then
//     no reason may say the page is not indexed;
//   - canonicalIsSelf false only when the crawler says "canonicalized"
//     (for a 2xx page without noindex), absent when there is no canonical.
func TestURLExplainNeverContradictsCrawler(t *testing.T) {
	str := func(s string) *string { return &s }
	pageURL := "https://ex.com/a/"
	statuses := []int{200, 204, 301, 302, 404, 410, 500, 0}
	robots := []*string{nil, str(""), str("index,follow"), str("noindex"), str("NOINDEX, nofollow")}
	canonicals := []*string{nil, str(""), str(pageURL), str("https://ex.com/a"), str("http://ex.com/a/"),
		str("https://www.ex.com/a"), str("https://ex.com/b"), str("https://other.com/a/"), str("https://ex.com/a/?p=1")}
	finals := []string{"", pageURL, "https://ex.com/new"}
	contradict := []string{`says "noindex"`, "canonical tag points", "error pages are not indexed", "redirects ("}
	n := 0
	for _, code := range statuses {
		for _, meta := range robots {
			for _, xr := range robots {
				for _, canon := range canonicals {
					for _, final := range finals {
						n++
						p := &types.PageData{URL: pageURL, FinalURL: final, StatusCode: code,
							MetaRobots: meta, XRobotsTag: xr, Canonical: canon}
						verdictURL := final
						if verdictURL == "" {
							verdictURL = pageURL
						}
						p.Indexability = extract.DetermineIndexability(code, meta, xr, canon, verdictURL)
						out := explain(p, 1, 1)
						where := func() string {
							c := "<nil>"
							if canon != nil {
								c = *canon
							}
							return "status " + itoa(code) + " canonical " + c + " final " + final
						}
						if out.Verdict == "indexable" {
							if !p.Indexability.Indexable {
								t.Fatalf("%s: verdict indexable, crawler says %q", where(), p.Indexability.Reason)
							}
							for _, r := range out.Reasons {
								for _, c := range contradict {
									if strings.Contains(r, c) {
										t.Fatalf("%s: indexable but reason %q", where(), r)
									}
								}
							}
						}
						if canon == nil || *canon == "" {
							if out.CanonicalIsSelf != nil {
								t.Fatalf("%s: no canonical, canonicalIsSelf = %v", where(), *out.CanonicalIsSelf)
							}
						} else if code >= 200 && code < 300 && !hasNoindex(meta) && !hasNoindex(xr) {
							canonicalized := p.Indexability.Reason == "canonicalized"
							if out.CanonicalIsSelf == nil || *out.CanonicalIsSelf == canonicalized {
								t.Fatalf("%s: canonicalIsSelf = %v, crawler reason %q", where(), out.CanonicalIsSelf, p.Indexability.Reason)
							}
						}
					}
				}
			}
		}
	}
	if n < 1000 {
		t.Fatalf("only %d combinations checked", n)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

// A dashboard that is up but slow must not be "restarted": only a refused
// connection means nothing listens.
func TestBackendDoesNotRestartSlowDashboard(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
		writeJSON(w, map[string]any{})
	}))
	defer slow.Close()
	var readyCalls atomic.Int32
	b := NewHTTPBackend(slow.URL, "")
	b.Client = &http.Client{Timeout: 200 * time.Millisecond}
	b.Ready = func(ctx context.Context) error {
		readyCalls.Add(1)
		return nil
	}
	err := b.Do(context.Background(), "GET", "/api/crawls", nil, nil, nil)
	if err == nil {
		t.Fatal("want the timeout error")
	}
	if n := readyCalls.Load(); n != 1 {
		t.Errorf("Ready ran %d times on a slow dashboard, want 1 (no restart)", n)
	}
	var ue interface{ Timeout() bool }
	if !errors.As(err, &ue) || !ue.Timeout() {
		t.Errorf("error = %v, want the real timeout error", err)
	}
}
