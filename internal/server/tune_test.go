package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/types"
)

// Stop & Analyze must show results before the external link check ends; the
// broken links are added to the stats afterwards.
func TestExternalCheckRunsAfterResults(t *testing.T) {
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(800 * time.Millisecond)
		if strings.HasSuffix(r.URL.Path, "/gone") {
			http.NotFound(w, r)
		}
	}))
	defer ext.Close()

	s := newTestStore(t)
	m := newTestManager(t, s)
	var mu sync.Mutex
	var events []ProgressEvent
	done := make(chan struct{})
	m.SetProgressCallback(func(e ProgressEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
		if e.Type == "analysis_complete" {
			close(done)
		}
	})

	pages := []*types.PageData{{URL: "https://site.test/", StatusCode: 200}}
	cfg := types.CrawlConfig{SeedURL: "https://site.test/", CheckExternal: true, SkipSSRF: true}
	start := time.Now()
	m.analyzeAndCache(analysisRun{
		ctx: context.Background(), crawlID: "c1", config: cfg, pages: pages,
		postCfg: analysis.PostProcessConfigFromCrawl(cfg),
		externalLinks: map[string][]string{
			ext.URL + "/ok":   {"https://site.test/"},
			ext.URL + "/gone": {"https://site.test/"},
		},
	})
	if d := time.Since(start); d > 700*time.Millisecond {
		t.Fatalf("results took %s: waited for the external check", d)
	}
	res := m.GetQuickStats("c1")
	if res == nil {
		t.Fatal("results missing")
	}
	res.mu.RLock()
	pending := res.AnalysisPending
	res.mu.RUnlock()
	if !pending {
		t.Fatal("results not marked pending")
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("external check did not finish")
	}
	res.mu.RLock()
	broken, _ := res.Stats["brokenExternalLinks"].([]interface{})
	checked := res.Stats["externalLinksChecked"]
	res.mu.RUnlock()
	if len(broken) != 1 || !strings.Contains(broken[0].(map[string]interface{})["url"].(string), "/gone") {
		t.Fatalf("broken external links = %v", broken)
	}
	if checked != float64(2) {
		t.Fatalf("externalLinksChecked = %v", checked)
	}
	if res.externalTotal.Load() != 0 {
		t.Error("external progress not cleared")
	}
	mu.Lock()
	defer mu.Unlock()
	var sawProgress, sawDone bool
	for _, e := range events {
		if e.Type == "analysis_progress" {
			switch e.Data["phase"] {
			case "external_check":
				sawProgress = true
			case "external_done":
				sawDone = true
			}
		}
	}
	if !sawProgress || !sawDone {
		t.Errorf("progress=%v done=%v", sawProgress, sawDone)
	}
}

func TestTuneCrawlNeedsRunningCrawl(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	if _, err := m.TuneCrawl("nope", crawler.Tuning{DelayMs: 100}); err == nil {
		t.Fatal("tuned a crawl that is not running")
	}
	m.mu.Lock()
	m.active = &ActiveCrawl{ID: "c1", Config: map[string]interface{}{}}
	m.mu.Unlock()
	if _, err := m.TuneCrawl("c1", crawler.Tuning{DelayMs: 100}); err == nil || !strings.Contains(err.Error(), "starting") {
		t.Fatalf("err = %v, want still starting", err)
	}
	m.mu.Lock()
	m.active = nil
	m.mu.Unlock()
}
