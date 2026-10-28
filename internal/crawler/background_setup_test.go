package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// A slow sitemap must not delay the first page, and the crawl must wait for
// its URLs instead of ending when the seed runs out of links.
func TestSitemapDiscoveryRunsAlongsideCrawl(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]time.Time{}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path] = time.Now()
		mu.Unlock()
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", srv.URL)
		case "/sitemap.xml":
			time.Sleep(1500 * time.Millisecond)
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%[1]s/orphan-a</loc></url><url><loc>%[1]s/orphan-b</loc></url></urlset>`, srv.URL)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><head><title>p</title></head><body>no links</body></html>")
		}
	}))
	defer srv.Close()

	start := time.Now()
	res, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 5, MaxPages: 100,
		Concurrency: 2, UserAgent: "Micelio/1.0", SkipSSRF: true, RespectRobots: true,
		DiscoverSitemaps: true, NoSoft404Probe: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	seedAt := hits["/"]
	mu.Unlock()
	if d := seedAt.Sub(start); d > time.Second {
		t.Errorf("seed fetched %s after start: waited for the sitemap", d)
	}
	got := map[string]bool{}
	for _, p := range res.Pages {
		got[p.URL] = true
	}
	if !got[srv.URL+"/orphan-a"] || !got[srv.URL+"/orphan-b"] {
		t.Errorf("sitemap URLs not crawled; pages: %v", got)
	}
	if len(res.DiscoveredSitemapURLs) != 2 {
		t.Errorf("DiscoveredSitemapURLs = %v", res.DiscoveredSitemapURLs)
	}
}

// The soft-404 probe runs next to the crawl and its result is still kept.
func TestSoft404ProbeRunsAlongsideCrawl(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/" {
			time.Sleep(time.Second) // slow 404 page
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>home</body></html>")
	}))
	defer srv.Close()
	res, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 5, MaxPages: 10,
		Concurrency: 2, UserAgent: "Micelio/1.0", SkipSSRF: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Soft404Probes) != 1 || res.Soft404Probes[0].StatusCode != 404 {
		t.Fatalf("probes = %+v", res.Soft404Probes)
	}
}
