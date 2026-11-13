package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// Page weight resource sizes are asked after the results are shown: the
// results do not wait for them, progress is visible, and once they finish
// the pages and stats carry the same weights a synchronous run gives.
func TestPageWeightRunsAfterResults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		li      bool // link intelligence: sizes land before or after it
		instant bool // sizes answer at once (published with the deferred stats)
	}{{"alone", false, false}, {"after link intelligence", true, false}, {"before link intelligence ends", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			m := newTestManager(t, s)
			var mu sync.Mutex
			var phases []string
			done := make(chan struct{})
			m.SetProgressCallback(func(e ProgressEvent) {
				if e.Type != "analysis_progress" {
					return
				}
				p, _ := e.Data["phase"].(string)
				mu.Lock()
				phases = append(phases, p)
				mu.Unlock()
				if p == "page_weight_done" {
					close(done)
				}
			})

			refs := func() map[string][]types.ResourceEntry {
				return map[string][]types.ResourceEntry{
					"https://ex.com/":       {{URL: "https://cdn.test/a.css", Type: "stylesheet"}, {URL: "https://cdn.test/logo.png", Type: "image"}},
					"https://ex.com/cat/":   {{URL: "https://cdn.test/a.css", Type: "stylesheet"}, {URL: "https://cdn.test/big.jpg", Type: "image"}},
					"https://ex.com/cat/p1": {{URL: "https://cdn.test/a.css", Type: "stylesheet"}, {URL: "https://cdn.test/gone.png", Type: "image"}},
				}
			}
			sizes := map[string]int64{"https://cdn.test/a.css": 1000, "https://cdn.test/logo.png": 2000, "https://cdn.test/big.jpg": 4_000_000}
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			sizer := analysis.ResourceSizer{Size: func(ctx context.Context, u string) analysis.SizeResult {
				select {
				case <-release:
				case <-ctx.Done():
				}
				if n, ok := sizes[u]; ok {
					return analysis.SizeResult{Size: n, OK: true}
				}
				return analysis.SizeResult{}
			}}

			pages := linkedPages()
			want := linkedPages() // expected: the synchronous computation
			for i := range pages {
				pages[i].BodySize = 500
				want[i].BodySize = 500
			}
			analysis.ComputePageWeight(context.Background(), want, refs(), nil, analysis.PageWeightOptions{Sizer: analysis.ResourceSizer{Size: func(ctx context.Context, u string) analysis.SizeResult {
				if n, ok := sizes[u]; ok {
					return analysis.SizeResult{Size: n, OK: true}
				}
				return analysis.SizeResult{}
			}}})
			wantStats := analysis.BuildPageWeightStats(want)

			cfg := types.CrawlConfig{SeedURL: "https://ex.com/", PageWeight: true, LinkIntelligence: tc.li}
			postCfg := analysis.PostProcessConfigFromCrawl(cfg)
			postCfg.InternalLinksIter = diskIter(pages)
			if tc.instant {
				unblock()
			}
			start := time.Now()
			m.analyzeAndCache(analysisRun{
				ctx: context.Background(), crawlID: "pw", config: cfg, pages: pages, postCfg: postCfg,
				pageWeight: &pageWeightJob{refs: analysis.CompactResourceRefs(refs()), sizer: sizer, concurrency: 2},
			})
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("results took %s: waited for page weight", d)
			}
			res := m.GetQuickStats("pw")
			if res == nil {
				t.Fatal("results missing")
			}
			if !tc.instant {
				res.mu.RLock()
				pwBefore := res.Stats["pageWeightStats"]
				res.mu.RUnlock()
				if pwBefore != nil {
					t.Fatalf("page weight stats before sizes: %v", pwBefore)
				}
				deadline := time.Now().Add(5 * time.Second)
				for res.pageWeightTotal.Load() != 4 && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				if got := res.pageWeightTotal.Load(); got != 4 {
					t.Fatalf("page weight progress total %d, want 4 unique resources", got)
				}
			}
			unblock()

			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("page weight did not finish")
			}
			if res.pageWeightTotal.Load() != 0 {
				t.Error("page weight progress not cleared")
			}
			res.mu.RLock()
			defer res.mu.RUnlock()
			st, _ := res.Stats["pageWeightStats"].(map[string]interface{})
			if st == nil {
				t.Fatalf("no pageWeightStats after the check: %v", res.Stats["pageWeightStats"])
			}
			if got := int64(st["avgTotalBytes"].(float64)); got != wantStats.AvgTotalBytes {
				t.Errorf("avgTotalBytes %d, want %d", got, wantStats.AvgTotalBytes)
			}
			if st["resourcesTotal"] != float64(4) || st["resourcesSized"] != float64(3) || st["resourcesUnsized"] != float64(1) {
				t.Errorf("resource counts %v %v %v", st["resourcesTotal"], st["resourcesSized"], st["resourcesUnsized"])
			}
			byURL := map[string]*types.PageData{}
			for _, p := range want {
				byURL[p.URL] = p
			}
			for _, p := range res.Pages {
				w := byURL[p.URL].PageWeight
				if (w == nil) != (p.PageWeight == nil) {
					t.Errorf("%s: weight %+v, want %+v", p.URL, p.PageWeight, w)
					continue
				}
				if w == nil {
					continue
				}
				if p.PageWeight.TotalBytes != w.TotalBytes || p.PageWeight.HTMLBytes != w.HTMLBytes {
					t.Errorf("%s: total %d html %d, want %d %d", p.URL, p.PageWeight.TotalBytes, p.PageWeight.HTMLBytes, w.TotalBytes, w.HTMLBytes)
				}
				if p.PageWeight.Resources != nil {
					t.Errorf("%s: per-resource detail kept in the cache", p.URL)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			saw := false
			for _, p := range phases {
				if p == "page_weight" {
					saw = true
				}
			}
			if !saw {
				t.Errorf("no page_weight progress event in %v", phases)
			}
		})
	}
}

// Stop & Analyze and cancel end the background size check: its requests
// stop, the sizes known by then are published with a note, and the run
// ends. Delete publishes nothing.
func TestPageWeightStopCancelDelete(t *testing.T) {
	for _, action := range []string{"stop", "cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			s := newTestStore(t)
			m := newTestManager(t, s)
			job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://ex.com/"})
			id := job.ID
			pwDone := make(chan struct{})
			var once sync.Once
			m.SetProgressCallback(func(e ProgressEvent) {
				if e.CrawlID == id && e.Type == "analysis_progress" && e.Data["phase"] == "page_weight_done" {
					once.Do(func() { close(pwDone) })
				}
			})
			refs := map[string][]types.ResourceEntry{}
			for i := 0; i < 40; i++ {
				refs["https://ex.com/"] = append(refs["https://ex.com/"], types.ResourceEntry{URL: fmt.Sprintf("https://cdn.test/%d.png", i), Type: "image"})
			}
			var mu sync.Mutex
			answered, blocked := 0, 0
			sizer := analysis.ResourceSizer{
				PerHost: func(string) int { return 4 },
				Size: func(ctx context.Context, u string) analysis.SizeResult {
					mu.Lock()
					if answered < 8 {
						answered++
						mu.Unlock()
						return analysis.SizeResult{Size: 100, OK: true}
					}
					blocked++
					mu.Unlock()
					<-ctx.Done() // hangs until stop, cancel or delete
					return analysis.SizeResult{}
				},
			}
			pages := linkedPages()
			cfg := types.CrawlConfig{SeedURL: "https://ex.com/", PageWeight: true}
			m.analyzeAndCache(analysisRun{
				ctx: context.Background(), crawlID: id, job: job, config: cfg, pages: pages,
				postCfg:    analysis.PostProcessConfigFromCrawl(cfg),
				pageWeight: &pageWeightJob{refs: analysis.CompactResourceRefs(refs), sizer: sizer, concurrency: 2},
			})
			res := m.GetQuickStats(id)
			if res == nil {
				t.Fatal("results missing")
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				mu.Lock()
				b := blocked
				mu.Unlock()
				if b > 0 || time.Now().After(deadline) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}

			var err error
			switch action {
			case "stop":
				err = m.StopCrawl(id)
			case "cancel":
				err = m.CancelCrawl(id)
			case "delete":
				_, err = m.DeleteCrawl(id)
			}
			if err != nil {
				t.Fatalf("%s: %v", action, err)
			}
			waitBackground(t, m, 5*time.Second)

			res.mu.RLock()
			defer res.mu.RUnlock()
			st, _ := res.Stats["pageWeightStats"].(map[string]interface{})
			note, _ := res.Stats["analysisInterrupted"].(string)
			if action == "delete" {
				if st != nil {
					t.Errorf("deleted crawl got page weight stats: %v", st)
				}
				return
			}
			select {
			case <-pwDone:
			default:
				t.Error("no page_weight_done event")
			}
			if st == nil {
				t.Fatalf("no partial page weight stats after %s", action)
			}
			if st["resourcesSized"] != float64(8) || st["resourcesTotal"] != float64(40) {
				t.Errorf("sized %v of %v, want 8 of 40", st["resourcesSized"], st["resourcesTotal"])
			}
			want := map[string]string{"stop": "page weight stopped by user", "cancel": "page weight cancelled by user"}[action]
			if !strings.Contains(note, want) {
				t.Errorf("analysisInterrupted = %q, want %q", note, want)
			}
			if res.pageWeightTotal.Load() != 0 {
				t.Error("page weight progress not cleared")
			}
		})
	}
}
