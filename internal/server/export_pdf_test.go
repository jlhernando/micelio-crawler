package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/types"
)

func TestExportPDF(t *testing.T) {
	api, _, m := newTestAPI(t)
	var html string
	old := printPDF
	printPDF = func(_ context.Context, doc string) ([]byte, error) {
		html = doc
		return []byte("%PDF-1.7 fake"), nil
	}
	t.Cleanup(func() { printPDF = old })

	stats := map[string]interface{}{
		"totalPages":       2,
		"statusCodes":      map[string]interface{}{"200": 1, "404": 1},
		"brokenLinks":      []interface{}{map[string]interface{}{"url": "https://ex.com/gone", "statusCode": 404}},
		"totalBrokenLinks": 37,
	}
	pages := []*types.PageData{{URL: "https://ex.com/", StatusCode: 200, PageRank: 0.8}}
	m.mu.Lock()
	m.cacheResultLocked("pdf", &CrawlResults{Stats: stats, Pages: pages, PagesReady: true}, len(pages))
	m.mu.Unlock()

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/pdf/export/pdf", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("content type %q", ct)
	}
	if !strings.HasPrefix(rec.Body.String(), "%PDF") {
		t.Errorf("body is not a PDF")
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), ".pdf") {
		t.Errorf("disposition %q", rec.Header().Get("Content-Disposition"))
	}
	// The capped list's real total reaches the report.
	if !strings.Contains(html, "Broken internal links") || !strings.Contains(html, ">37<") {
		t.Errorf("report misses the broken link total")
	}

	printPDF = func(context.Context, string) ([]byte, error) {
		return nil, errors.New(`exec: "google-chrome": executable file not found in $PATH`)
	}
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/pdf/export/pdf", nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "Chrome") {
		t.Errorf("chrome missing: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/nope/export/pdf", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown crawl: %d", rec.Code)
	}
}

func TestPDFFileName(t *testing.T) {
	got := pdfFileName("https://www.ex-ample.com/es/", "id1")
	if !strings.HasPrefix(got, "micelio-ex-ample.com-") || !strings.HasSuffix(got, ".pdf") {
		t.Errorf("got %q", got)
	}
	if got := pdfFileName("", "id1"); !strings.HasPrefix(got, "micelio-crawl-id1-") {
		t.Errorf("empty seed: %q", got)
	}
}

// With the UI server address known, the export screenshots the main
// Universe map, one map per ticket that needs it (here: errors) and a cover
// backdrop, all on loopback with the dashboard token.
func TestExportPDFUniverse(t *testing.T) {
	api, _, m := newTestAPI(t)
	oldPrint, oldCapture := printPDF, captureApp
	var html string
	printPDF = func(_ context.Context, doc string) ([]byte, error) { html = doc; return []byte("%PDF"), nil }
	var got []browser.AppTarget
	var auth any
	failMap := false
	captureApp = func(_ context.Context, targets []browser.AppTarget, headers map[string]any, _ float64) []browser.AppShot {
		got, auth = targets, headers["Authorization"]
		shots := make([]browser.AppShot, len(targets))
		for i := range shots {
			shots[i] = browser.AppShot{JPEG: []byte{0xff, 0xd8, byte(i)}, Meta: `{"pages":12,"clusters":3,"links":40,"top":[{"label":"/blog/","count":7,"color":"#4361ee"}]}`}
		}
		if failMap {
			shots[1].Err = errors.New("webgl failed") // a failed view is left out
		}
		return shots
	}
	setSelfURL("0.0.0.0", 3999, "tok")
	t.Cleanup(func() {
		printPDF, captureApp = oldPrint, oldCapture
		self.Lock()
		self.base, self.token = "", ""
		self.Unlock()
	})

	stats := map[string]interface{}{"totalPages": 300, "statusCodes": map[string]interface{}{"200": 270, "404": 30}}
	m.mu.Lock()
	m.cacheResultLocked("uv", &CrawlResults{Stats: stats, PagesReady: true}, 0)
	m.mu.Unlock()

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/uv/export/pdf", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if len(got) != 3 || auth != "Bearer tok" {
		t.Fatalf("captures %d, auth %v", len(got), auth)
	}
	for _, tg := range got {
		if !strings.HasPrefix(tg.URL, "http://127.0.0.1:3999/#/capture/universe/uv?") {
			t.Errorf("capture url %q", tg.URL)
		}
	}
	if !strings.Contains(got[1].URL, "highlight=2") {
		t.Errorf("no errors view: %q", got[1].URL)
	}
	for _, want := range []string{"The site as a crawler sees it", "Errors on the map", "/blog/", "backdrop", "Fix 30 broken internal links"} {
		if !strings.Contains(html, want) {
			t.Errorf("report misses %q", want)
		}
	}

	failMap = true
	api.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/crawl/uv/export/pdf", nil))
	if strings.Contains(html, "Errors on the map") || !strings.Contains(html, "Fix 30 broken internal links") {
		t.Error("a failed map should leave the ticket without it")
	}

	// ?universe=0 skips the screenshots.
	got = nil
	api.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/crawl/uv/export/pdf?universe=0", nil))
	if got != nil || strings.Contains(html, "Site universe") {
		t.Error("universe=0 still captured")
	}

	// The backlog CSV for JIRA.
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/uv/export/backlog.csv", nil))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(rec.Body.String(), "SEO-1,Fix 30 broken internal links,Bug,Highest,3,SEO") {
		t.Errorf("backlog csv %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.HasSuffix(strings.Trim(rec.Header().Get("Content-Disposition"), `"`), "-backlog.csv") {
		t.Errorf("disposition %q", rec.Header().Get("Content-Disposition"))
	}
}

// Exports fetch Core Web Vitals and probe the CDN once per crawl (cached),
// with the CrUX/PageSpeed keys from Settings, and both land in PDF and CSV.
func TestExportWebChecks(t *testing.T) {
	api, store, m := newTestAPI(t)
	oldPrint, oldWeb := printPDF, fetchWebChecks
	var html string
	printPDF = func(_ context.Context, doc string) ([]byte, error) { html = doc; return []byte("%PDF"), nil }
	calls := 0
	var gotKey string
	fetchWebChecks = func(_ context.Context, _ *http.Client, cruxKey, psiKey, seedURL string) (*types.CWVReport, *types.CDNReport) {
		calls++
		gotKey = cruxKey
		return &types.CWVReport{Source: "crux", Field: []types.CWVField{{FormFactor: "phone", Metrics: []types.CWVMetric{{ID: "lcp", P75: 5000, Poor: .4}}}}},
			&types.CDNReport{Provider: "Cloudflare", HTML: []types.CDNCache{{Status: "DYNAMIC"}}, Compression: "br", Protocol: "HTTP/2.0"}
	}
	t.Cleanup(func() {
		printPDF, fetchWebChecks = oldPrint, oldWeb
		webCache.Lock()
		webCache.m = map[string]webCheck{}
		webCache.Unlock()
	})
	if err := store.UpdateSettings(map[string]interface{}{"cruxKey": "ck-123"}); err != nil {
		t.Fatal(err)
	}
	job, err := store.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://ex.com/"})
	if err != nil {
		t.Fatal(err)
	}
	id := job.ID
	m.mu.Lock()
	m.cacheResultLocked(id, &CrawlResults{Stats: map[string]interface{}{"totalPages": 10, "statusCodes": map[string]interface{}{"200": 10}}, PagesReady: true}, 0)
	m.mu.Unlock()

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/"+id+"/export/pdf?universe=0", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pdf %d %s", rec.Code, rec.Body.String())
	}
	if gotKey != "ck-123" {
		t.Errorf("crux key %q", gotKey)
	}
	for _, want := range []string{"Fails Core Web Vitals", "CDN &amp; caching", "Bring LCP under 2.5 s on phones (now 5.0 s)"} {
		if !strings.Contains(html, want) {
			t.Errorf("PDF misses %q", want)
		}
	}
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/"+id+"/export/backlog.csv", nil))
	if !strings.Contains(rec.Body.String(), "Cache HTML at the Cloudflare edge") {
		t.Errorf("csv misses the CDN ticket: %s", rec.Body.String())
	}
	if calls != 1 {
		t.Errorf("web checks ran %d times, want 1 (cached)", calls)
	}
}
