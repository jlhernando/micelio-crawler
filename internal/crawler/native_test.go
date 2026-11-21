package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

func TestNativeCrawlRobotsURLAndPacing(t *testing.T) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" {
		t.Skip("native Chrome requires a desktop")
	}
	var mu sync.Mutex
	hits := map[string]int{}
	var documents []time.Time
	var blockMode atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		if r.URL.Path != "/robots.txt" && r.URL.Path != "/favicon.ico" {
			documents = append(documents, time.Now())
		}
		mu.Unlock()
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /blocked\n")
			return
		}
		if blockMode.Load() {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "blocked")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>Native</title><link rel="canonical" href="%s"></head><body><h1>Native</h1><a href="/next">next</a><a href="/blocked">blocked</a><script>history.replaceState(null,'','?range=20')</script></body></html>`, r.URL.Path)
	}))
	defer srv.Close()
	result, err := Crawl(context.Background(), types.CrawlConfig{NativeChrome: true, SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxPages: 5, RespectRobots: true, SkipSSRF: true, Concurrency: 5, DelayMs: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.PageCount != 2 || len(result.Pages) != 2 {
		t.Fatalf("unexpected native page count %d/%d", result.PageCount, len(result.Pages))
	}
	for _, page := range result.Pages {
		if page.StatusCode != 200 || !page.Indexability.Indexable || page.FinalURL == page.BrowserURL || !page.JSRendered {
			t.Fatalf("native metadata or canonical misclassified: %+v", page)
		}
	}
	mu.Lock()
	if hits["/blocked"] != 0 || hits["/.well-known/llms.txt"] != 0 {
		t.Fatalf("robots/probe bypass: %+v", hits)
	}
	if hits["/"] != 1 || hits["/next"] != 1 {
		t.Fatalf("pages fetched more than once: %+v", hits)
	}
	for i := 1; i < len(documents); i++ {
		if documents[i].Sub(documents[i-1]) < 900*time.Millisecond {
			t.Fatalf("native pacing bypassed: %v", documents)
		}
	}
	mu.Unlock()
	blockMode.Store(true)
	blocked, err := Crawl(context.Background(), types.CrawlConfig{NativeChrome: true, SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxPages: 5, RespectRobots: true, SkipSSRF: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.PageCount != 1 || blocked.StopReason == "" || blocked.Pages[0].StatusCode != 403 {
		t.Fatalf("native blocking did not stop with a useful result: %+v", blocked)
	}
}
