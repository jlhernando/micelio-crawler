package crawler

import (
	"context"
	"fmt"
	"github.com/micelio/micelio/internal/types"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
)

func TestCoverageReasonsAndBoundedSamples(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 1, 10, &QueueOptions{EnforceInternal: true, IncludePatterns: []*regexp.Regexp{regexp.MustCompile("/keep")}, ExcludePatterns: []*regexp.Regexp{regexp.MustCompile("blocked")}})
	defer q.Close()
	q.Enqueue("https://example.com/keep", 0, nil)
	q.Enqueue("https://example.com/keep", 0, nil) // accepted duplicate is not an exclusion
	q.Enqueue("https://example.com/keep/deep", 2, nil)
	q.Enqueue("https://outside.example/keep", 0, nil)
	q.Enqueue("https://example.com/other", 0, nil)
	for i := 0; i < 8; i++ {
		q.Enqueue(fmt.Sprintf("https://example.com/keep/blocked%d", i), 0, nil)
	}
	q.Enqueue("https://example.com/keep/blocked0", 0, nil) // repeated attempts count
	c := q.CoverageSnapshot()
	counts := map[string]int{}
	for _, r := range c.Reasons {
		counts[r.Reason] = r.Count
		if len(r.Examples) > 5 {
			t.Fatal("unbounded samples")
		}
		if r.Reason == "exclude_filter" && r.Rule != "blocked" {
			t.Fatal("lost filter rule")
		}
	}
	if c.AcceptedURLs != 1 || counts["depth"] != 1 || counts["domain"] != 1 || counts["include_filter"] != 1 || counts["exclude_filter"] != 9 {
		t.Fatalf("unexpected coverage: %+v %+v", c, counts)
	}
	c.Reasons[0].Examples[0] = "mutated"
	if q.CoverageSnapshot().Reasons[0].Examples[0] == "mutated" {
		t.Fatal("snapshot aliases queue memory")
	}
}

func TestCoverageConcurrentAndResumeAdmission(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 1, 1, nil)
	defer q.Close()
	q.EnqueueEntry(types.QueueEntry{URL: "https://example.com/", Depth: 0})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.EnqueueEntry(types.QueueEntry{URL: "https://example.com/limited", Depth: 0})
			q.CoverageSnapshot()
		}()
	}
	wg.Wait()
	c := q.CoverageSnapshot()
	if len(c.Reasons) != 1 || c.Reasons[0].Reason != "page_limit" || c.Reasons[0].Count != 20 {
		t.Fatalf("bad concurrent count: %+v", c)
	}
}

func TestCoverageRobotsEvenWithoutStoredPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
			return
		}
		t.Error("blocked page was fetched")
	}))
	defer srv.Close()
	result, err := Crawl(context.Background(), types.CrawlConfig{SeedURL: srv.URL, Mode: types.ModeSpider, MaxDepth: 1, MaxPages: 5, Concurrency: 1, RespectRobots: true, ShowBlockedInternal: false, SkipSSRF: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pages) != 0 || result.Coverage == nil || !result.Coverage.Final {
		t.Fatalf("missing zero-page final coverage: %+v", result)
	}
	if len(result.Coverage.Reasons) != 1 || result.Coverage.Reasons[0].Reason != "robots" || result.Coverage.Reasons[0].Count != 1 {
		t.Fatalf("missing robots reason: %+v", result.Coverage)
	}
}

func TestCoverageSurvivesSitemapSetupFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://outside.example/page</loc></url></urlset>`)
	}))
	defer srv.Close()
	result, err := Crawl(context.Background(), types.CrawlConfig{Mode: types.ModeSitemap, SitemapURLs: []string{srv.URL + "/sitemap.xml"}, AllowedDomains: []string{"allowed.example"}, MaxDepth: 1, MaxPages: 5, Concurrency: 1, SkipSSRF: true}, nil)
	if err == nil {
		t.Fatal("expected out-of-scope setup failure")
	}
	if result == nil || result.Coverage == nil || len(result.Coverage.Reasons) != 1 || result.Coverage.Reasons[0].Reason != "domain" {
		t.Fatalf("lost setup exclusions: %+v (%v)", result, err)
	}
}
