package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/types"
)

// With JS rendering, each page reaches the server once (the browser gets the
// crawled HTML) and documents the browser loads itself (an iframe here) keep
// the per-host delay like crawler requests. Before, every page was fetched
// twice, the second time by the browser with no delay.
func TestJSRenderingHitsEachPageOnceAndKeepsDelay(t *testing.T) {
	probe := browser.NewRenderer("")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	_, err := probe.RenderPage(ctx, "about:blank", "", nil)
	cancel()
	probe.Close()
	if err != nil && strings.Contains(err.Error(), "executable file not found") {
		t.Skip("Chrome not installed")
	}

	var mu sync.Mutex
	hits := map[string]int{}
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/favicon.ico" {
			// A subresource: the browser's own requests for scripts, styles
			// and icons are not paced, only documents.
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		hits[r.URL.Path]++
		times = append(times, time.Now())
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/frame" {
			fmt.Fprint(w, "<html><body>frame</body></html>")
			return
		}
		fmt.Fprint(w, `<html><body><a href="/a">a</a><a href="/b">b</a><iframe src="/frame"></iframe>
<script>document.body.insertAdjacentHTML('beforeend','<p>js</p>')</script></body></html>`)
	}))
	defer srv.Close()

	_, err = Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 1, MaxPages: 3,
		Concurrency: 3, DelayMs: 300, DelayExplicit: true, UserAgent: "Micelio/1.0",
		SkipSSRF: true, NoSoft404Probe: true, JSRendering: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range []string{"/", "/a", "/b"} {
		if hits[p] != 1 {
			t.Errorf("%s fetched %d times, want 1", p, hits[p])
		}
	}
	if hits["/frame"] == 0 {
		t.Fatal("iframe never loaded: the test does not cover browser requests")
	}
	for i := 1; i < len(times); i++ {
		if g := times[i].Sub(times[i-1]); g < 280*time.Millisecond {
			t.Errorf("request %d came %v after the previous one; delay is 300ms", i, g)
		}
	}
}
