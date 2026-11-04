package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/types"
)

// Deferred analysis used to clear link slices, swap pages for stubs and
// write scores on the cached pages without the result lock, while the page
// endpoint read them (and encoded its shallow copy after unlocking). Run
// with -race.
func TestDeferredAnalysisDoesNotRaceHandlers(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := linkedPages()
	for _, p := range pages {
		p.Title = &types.TextLength{Text: "Cat page"}
		p.Topicality = &types.TopicalityData{}
		p.Anchors = []types.AnchorData{{Href: "https://ex.com/cat/", Text: "cat page", IsInternal: true}}
	}
	res := &CrawlResults{Pages: pages, PagesReady: true, AnalysisPending: true}
	m.mu.Lock()
	m.cacheResultLocked("race", res, len(pages))
	m.mu.Unlock()

	ctx, done := m.startDeferred("race")
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer done()
		cfg := analysis.PostProcessConfig{SeedURL: "https://ex.com/", LinkIntelligence: true}
		m.runDeferredAnalysis(ctx, "race", res, cfg, types.CrawlStats{}, "", nil)
	}()
	for running := true; running; {
		select {
		case <-finished:
			running = false
		default:
		}
		if code, _ := getPage(t, api, "race", "https://ex.com/cat/"); code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
	}

	code, got := getPage(t, api, "race", "https://ex.com/cat/")
	if code != http.StatusOK || got.Page == nil || got.Page.LinkIntelligence == nil {
		t.Fatalf("after analysis: status %d, page %+v", code, got.Page)
	}
	if len(got.Outlinks) == 0 {
		t.Error("outlinks lost after analysis")
	}
	res.mu.RLock()
	defer res.mu.RUnlock()
	if res.AnalysisPending {
		t.Error("analysis still pending")
	}
}

// The external link check ran on the manager context, so deleting the crawl
// left it running and it then published stats and events for the deleted
// (or restarted) crawl.
func TestDeleteCancelsExternalCheck(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	m.SkipSSRF = true
	var hit sync.Once
	reached := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Do(func() { close(reached) })
		<-r.Context().Done()
	}))
	defer slow.Close()

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://ex.com/"})
	var completed atomic.Bool
	m.SetProgressCallback(func(e ProgressEvent) {
		if e.CrawlID == job.ID && e.Type == "analysis_complete" {
			completed.Store(true)
		}
	})
	res := &CrawlResults{Pages: linkedPages(), PagesReady: true, AnalysisPending: true}
	m.mu.Lock()
	m.cacheResultLocked(job.ID, res, len(res.Pages))
	m.mu.Unlock()

	cfg := types.CrawlConfig{SkipSSRF: true}
	links := map[string][]string{slow.URL + "/a": {"https://ex.com/"}}
	check := func(ctx context.Context) *crawler.ExternalCheckSummary {
		return m.checkExternalLinks(ctx, job.ID, cfg, links)
	}
	ctx, done := m.startDeferred(job.ID)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer done()
		m.runDeferredAnalysis(ctx, job.ID, res, analysis.PostProcessConfig{}, types.CrawlStats{}, "", check)
	}()

	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("external check never started")
	}
	if ok, err := m.DeleteCrawl(job.ID); !ok || err != nil {
		t.Fatalf("delete: %v %v", ok, err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("external check still running after the crawl was deleted")
	}
	if completed.Load() {
		t.Error("analysis_complete emitted for a deleted crawl")
	}
}
