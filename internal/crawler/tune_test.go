package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// linkSite serves pages that each link to 20 more, so the crawl never runs
// dry before the limit. handle runs before each response.
func linkSite(handle func(path string)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		handle(r.URL.Path)
		w.Header().Set("Content-Type", "text/html")
		html := "<html><body>"
		for i := 0; i < 20; i++ {
			html += fmt.Sprintf(`<a href="%s-%d">x</a>`, r.URL.Path, i)
		}
		fmt.Fprint(w, html+"</body></html>")
	}))
}

func TestTunerDelayAndLimit(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	srv := linkSite(func(path string) {
		if path == "/micelio-404-check" || len(path) > 22 && path[:22] == "/micelio-404-check-" {
			return
		}
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
	})
	defer srv.Close()

	var tuner *Tuner
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := CrawlWithTuner(context.Background(), types.CrawlConfig{
			SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 10, MaxPages: 1000,
			Concurrency: 4, DelayMs: 300, DelayExplicit: true, UserAgent: "Micelio/1.0", SkipSSRF: true,
			NoSoft404Probe: true,
		}, nil, nil, nil, nil, func(tu *Tuner) { tuner = tu; close(ready) })
		done <- err
	}()
	<-ready
	time.Sleep(1300 * time.Millisecond) // ~4 requests at 300 ms
	mu.Lock()
	slow := len(times)
	mu.Unlock()
	if got := tuner.Get(); got.DelayMs != 300 || got.Concurrency != 4 {
		t.Fatalf("initial tuning = %+v", got)
	}
	if _, err := tuner.Apply(Tuning{DelayMs: 50, MaxPages: slow + 25}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if slow > 6 {
		t.Errorf("%d requests in 1.3 s at 300 ms", slow)
	}
	if n := len(times); n < slow+20 || n > slow+25+4 {
		t.Errorf("crawl made %d requests, want about %d (new limit)", n, slow+25)
	}
	// After the change, gaps are near 50 ms, never below it.
	fast := times[slow+2:]
	total := fast[len(fast)-1].Sub(fast[0])
	avg := total / time.Duration(len(fast)-1)
	if avg > 150*time.Millisecond {
		t.Errorf("average gap after lowering the delay = %v", avg)
	}
	for i := 1; i < len(fast); i++ {
		if g := fast[i].Sub(fast[i-1]); g < 40*time.Millisecond {
			t.Errorf("gap %v below the new 50 ms delay", g)
		}
	}
}

func TestTunerConcurrency(t *testing.T) {
	var inFlight, peak atomic.Int32
	release := make(chan struct{})
	srv := linkSite(func(string) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
	})
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var tuner *Tuner
	ready := make(chan struct{})
	go CrawlWithTuner(ctx, types.CrawlConfig{
		SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 10, MaxPages: 200,
		Concurrency: 1, DelayMs: 0, UserAgent: "Micelio/1.0", SkipSSRF: true, NoSoft404Probe: true,
	}, nil, nil, nil, nil, func(tu *Tuner) { tuner = tu; close(ready) })
	<-ready

	waitFor := func(want int32) {
		deadline := time.Now().Add(3 * time.Second)
		for inFlight.Load() < want && time.Now().Before(deadline) {
			release <- struct{}{} // let one request finish so more links exist
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitFor(1)
	if p := peak.Load(); p > 1 {
		t.Fatalf("peak %d with concurrency 1", p)
	}
	tuner.Apply(Tuning{Concurrency: 5})
	waitFor(5)
	if p := peak.Load(); p < 4 || p > 5 {
		t.Errorf("peak in-flight after raising to 5 = %d", p)
	}
	if _, err := tuner.Apply(Tuning{Concurrency: 500}); err == nil {
		t.Error("concurrency 500 accepted")
	}
}

// Raising the limit far past the bloom filter's size must not make new URLs
// look already seen.
func TestQueueRaiseLimitKeepsDedupAccurate(t *testing.T) {
	q := NewCrawlQueue("https://example.com/", 10, 100, nil)
	for i := 0; i < 100; i++ {
		q.Enqueue(fmt.Sprintf("https://example.com/a%d", i), 1, nil)
	}
	if q.Enqueue("https://example.com/over", 1, nil) {
		t.Fatal("enqueued past the limit")
	}
	q.SetMaxPages(50_000)
	added := 0
	for i := 0; i < 40_000; i++ {
		if q.Enqueue(fmt.Sprintf("https://example.com/b%d", i), 1, nil) {
			added++
		}
	}
	if added < 39_990 {
		t.Errorf("added %d of 40000 new URLs after raising the limit", added)
	}
	if q.Enqueue("https://example.com/a5", 1, nil) {
		t.Error("URL from before the raise enqueued twice")
	}
	q.SetMaxPages(10) // lowering keeps the filter
	if q.Enqueue("https://example.com/c", 1, nil) {
		t.Error("enqueued after lowering the limit")
	}
}
