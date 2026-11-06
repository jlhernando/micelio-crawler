package mcpserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// A completed crawl whose analysis still runs is not done: its results are
// not cached yet, so crawl_summary would fail.
func TestCrawlWaitNotDoneWhileAnalysing(t *testing.T) {
	var calls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/crawl/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		job := map[string]any{"id": "c1", "status": "completed", "pageCount": 5}
		if calls.Add(1) <= 3 {
			job["analyzing"] = true
			job["statusCodes"] = map[string]int{"2xx": 5}
		}
		writeJSON(w, job)
	})
	mux.HandleFunc("GET /api/crawl/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		if calls.Load() <= 3 {
			http.Error(w, `{"error":"results not available"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]any{"stats": map[string]any{}, "pageCount": 5})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cs := connect(t, srv.URL, Options{AllowCrawl: true, PollInterval: 50 * time.Millisecond}, nil)

	var st map[string]any
	callOK(t, cs, "crawl_status", map[string]any{"id": "c1"}, &st)
	if st["done"] != false || !strings.Contains(st["phase"].(string), "analys") {
		t.Errorf("analysing crawl: done=%v phase=%v, want not done and an analysing phase", st["done"], st["phase"])
	}
	msg := callErr(t, cs, "crawl_summary", map[string]any{"id": "c1"})
	if !strings.Contains(msg, "crawl_wait") || strings.Contains(msg, "not found") {
		t.Errorf("summary while analysing: %q, want a hint to call crawl_wait", msg)
	}

	callOK(t, cs, "crawl_wait", map[string]any{"id": "c1", "timeoutSec": 5}, &st)
	if st["done"] != true || st["phase"] != "done" {
		t.Errorf("after analysis: done=%v phase=%v", st["done"], st["phase"])
	}
	callOK(t, cs, "crawl_summary", map[string]any{"id": "c1"}, nil)
}

// The 4 KB summary limit holds even with a huge seed URL and bulky stats.
func TestFitSummaryAlwaysFits(t *testing.T) {
	out := crawlSummaryOut{ID: "c1", SeedURL: "https://ex.com/" + strings.Repeat("a", 20000), Untrusted: summaryNote,
		StatusCodes: map[string]int{}, Indexability: summaryIndexability{Reasons: map[string]int{}}}
	for i := range 200 {
		out.StatusCodes["2"+strings.Repeat("0", i%5)+string(rune('a'+i%26))+strings.Repeat("x", i)] = i
		out.Indexability.Reasons[strings.Repeat("r", i)] = i
	}
	for range 10 {
		out.Issues = append(out.Issues, summaryIssue{ID: strings.Repeat("i", 300), Label: strings.Repeat("l", 300), Samples: []string{"a", "b", "c"}})
	}
	fitSummary(&out)
	b, _ := json.Marshal(out)
	if len(b) > maxSummaryBytes {
		t.Fatalf("summary is %d bytes, limit %d", len(b), maxSummaryBytes)
	}
	if !strings.HasPrefix(out.SeedURL, "https://ex.com/") {
		t.Fatalf("seed URL lost its start: %.40q", out.SeedURL)
	}
}

// Sample URLs share one short time budget: a dashboard that keeps loading
// pages must not hold crawl_summary for 20 s per issue.
func TestCrawlSummarySamplesHaveOneBudget(t *testing.T) {
	var pageCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/crawl/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"id": "c1", "status": "completed", "pageCount": 50})
	})
	mux.HandleFunc("GET /api/crawl/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"pageCount": 50, "stats": map[string]any{
			"totalPages": 50, "pagesWithoutTitle": 9, "pagesWithoutDescription": 8, "pagesWithoutH1": 7,
			"multipleH1Count": 6, "titleTooLongCount": 5, "titleTooShortCount": 4, "duplicateTitleCount": 3,
		}})
	})
	mux.HandleFunc("GET /api/crawl/{id}/pages", func(w http.ResponseWriter, r *http.Request) {
		pageCalls.Add(1)
		writeJSON(w, map[string]any{"loading": true, "pages": []any{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cs := connect(t, srv.URL, Options{SampleBudget: time.Second}, nil)

	start := time.Now()
	var got crawlSummaryOut
	callOK(t, cs, "crawl_summary", map[string]any{"id": "c1"}, &got)
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("crawl_summary took %s with a 1 s sample budget", d)
	}
	if len(got.Issues) < 7 {
		t.Errorf("issues = %d, want the counts even without samples", len(got.Issues))
	}
}

// The dashboard can go away after the first call: the next call starts it
// again instead of failing until the MCP server restarts.
func TestBackendRestartsDashboardAfterItStops(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	var mu sync.Mutex
	var srv *http.Server
	start := func() {
		mu.Lock()
		defer mu.Unlock()
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, map[string]any{"crawls": []any{}, "total": 0})
		})}
		go srv.Serve(l)
	}
	var readyCalls atomic.Int32
	b := NewHTTPBackend("http://"+addr, "")
	b.Ready = func(ctx context.Context) error {
		readyCalls.Add(1)
		start()
		return nil
	}
	ctx := context.Background()
	if err := b.Do(ctx, "GET", "/api/crawls", nil, nil, nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	mu.Lock()
	srv.Close()
	mu.Unlock()
	if err := b.Do(ctx, "GET", "/api/crawls", nil, nil, nil); err != nil {
		t.Fatalf("call after the dashboard stopped: %v", err)
	}
	if n := readyCalls.Load(); n != 2 {
		t.Errorf("Ready ran %d times, want 2 (start, then restart)", n)
	}
	mu.Lock()
	srv.Close()
	mu.Unlock()
}

// url_links in waits for the crawl's pages like url_explain does, and an
// uncrawled URL is an error, not 0 inlinks.
func TestURLLinksInWaitsForPagesAndChecksURL(t *testing.T) {
	var pageCalls atomic.Int32
	var loaded atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/crawl/{id}/page", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("url") != "https://ex.com/a" {
			w.WriteHeader(http.StatusNotFound)
			writeJSON(w, map[string]any{"error": "page not found"})
			return
		}
		if pageCalls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSON(w, map[string]any{"error": "loading", "loading": true})
			return
		}
		loaded.Store(true)
		writeJSON(w, map[string]any{"page": map[string]any{"url": "https://ex.com/a", "statusCode": 200}, "inlinkCount": 2})
	})
	mux.HandleFunc("GET /api/crawl/{id}/page-inlinks", func(w http.ResponseWriter, r *http.Request) {
		// Like the real endpoint: no "loading" signal, just no sources yet.
		if !loaded.Load() {
			writeJSON(w, map[string]any{"sources": []string{}})
			return
		}
		writeJSON(w, map[string]any{"sources": []string{"https://ex.com/", "https://ex.com/b"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cs := connect(t, srv.URL, Options{}, nil)

	var got urlLinksOut
	callOK(t, cs, "url_links", map[string]any{"id": "c1", "url": "https://ex.com/a"}, &got)
	if got.Total != 2 {
		t.Errorf("inlinks while loading = %d, want 2 once the pages are loaded", got.Total)
	}
	msg := callErr(t, cs, "url_links", map[string]any{"id": "c1", "url": "https://ex.com/never"})
	if !strings.Contains(msg, "was not crawled") {
		t.Errorf("uncrawled URL: %q, want a not-crawled error", msg)
	}
}

// url_explain compares the canonical the way the crawler does: scheme, www
// and a trailing slash do not make it point elsewhere.
func TestURLExplainCanonicalMatchesCrawler(t *testing.T) {
	canon := "https://www.ex.com/a/"
	p := &types.PageData{URL: "http://ex.com/a", FinalURL: "http://ex.com/a", StatusCode: 200,
		Canonical: &canon, Indexability: types.IndexabilityData{Indexable: true}}
	out := explain(p, 1, 0)
	if out.CanonicalIsSelf == nil || !*out.CanonicalIsSelf {
		t.Errorf("canonicalIsSelf = %v, want true", out.CanonicalIsSelf)
	}
	for _, r := range out.Reasons {
		if strings.Contains(r, "canonical tag points to") {
			t.Errorf("reason %q contradicts the indexable verdict", r)
		}
	}
	other := "https://ex.com/b"
	p.Canonical = &other
	p.Indexability = types.IndexabilityData{Reason: "canonicalized"}
	if out := explain(p, 1, 0); out.CanonicalIsSelf == nil || *out.CanonicalIsSelf {
		t.Errorf("canonical to /b: canonicalIsSelf = %v, want false", out.CanonicalIsSelf)
	}
}

// A redirect the crawler did not follow has no target data: targetIndexable
// must not repeat the redirect's own verdict as the target's.
func TestURLExplainUnfollowedRedirect(t *testing.T) {
	p := &types.PageData{URL: "https://ex.com/old", StatusCode: 301,
		Indexability: types.IndexabilityData{Reason: "HTTP 301"}}
	out := explain(p, 0, 0)
	if out.TargetIndexable != nil {
		t.Errorf("targetIndexable = %v for a redirect whose target was not fetched", *out.TargetIndexable)
	}
	if out.Indexable {
		t.Error("an unfollowed redirect is not indexable")
	}

	// A followed redirect: the page holds the target's data.
	p = &types.PageData{URL: "https://ex.com/old", FinalURL: "https://ex.com/new", StatusCode: 200,
		RedirectChain: []types.RedirectHop{{URL: "https://ex.com/old", StatusCode: 301}},
		Indexability:  types.IndexabilityData{Indexable: true}}
	out = explain(p, 0, 0)
	if out.TargetIndexable == nil || !*out.TargetIndexable {
		t.Errorf("followed redirect to an indexable page: targetIndexable = %v", out.TargetIndexable)
	}
}
