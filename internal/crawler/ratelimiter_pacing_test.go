package crawler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// slowNewConnections makes every new connection the crawl client opens take
// d to set up (crawl clients clone http.DefaultTransport), like TCP+TLS to a
// real server. Requests on a reused keep-alive connection stay fast.
func slowNewConnections(t *testing.T, d time.Duration) {
	t.Helper()
	orig := http.DefaultTransport
	tr := orig.(*http.Transport).Clone()
	var dialer net.Dialer
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		sleepCtx(ctx, d)
		return dialer.DialContext(ctx, network, addr)
	}
	http.DefaultTransport = tr
	t.Cleanup(func() { http.DefaultTransport = orig })
}

// assertSpacing fails if two consecutive server-side arrivals are closer
// than interval (minus 20 ms for scheduling noise between the client writing
// a request and the handler running).
func assertSpacing(t *testing.T, times []time.Time, paths []string, interval time.Duration) {
	t.Helper()
	for i := 1; i < len(times); i++ {
		if g := times[i].Sub(times[i-1]); g < interval-20*time.Millisecond {
			t.Fatalf("request %d (%s) came %v after the previous one (%s); minimum is %v",
				i, paths[i], g.Round(100*time.Microsecond), paths[i-1], interval)
		}
	}
}

// A caller that sends its request late (here 80 ms after Wait returned) must
// push the next caller back: the gap counts from the send, not from a slot
// booked in advance.
func TestTokenBucketSpacesFromActualSend(t *testing.T) {
	const interval = 100 * time.Millisecond
	tb := newTokenBucket(interval)
	ctx := context.Background()
	tb.Wait(ctx)() // previous request, long ago enough

	time.Sleep(interval)
	sentA := tb.Wait(ctx)
	bDone := make(chan time.Time)
	go func() {
		tb.Wait(ctx)()
		bDone <- time.Now()
	}()
	time.Sleep(80 * time.Millisecond) // A is slow to put its request on the wire
	aSent := time.Now()
	sentA()
	if g := (<-bDone).Sub(aSent); g < interval {
		t.Fatalf("second request released %v after the first was sent, want >= %v", g, interval)
	}
}

// When a Retry-After pause ends, waiting callers must leave one interval
// apart, not all at once.
func TestTokenBucketPauseEndKeepsSpacing(t *testing.T) {
	const interval = 60 * time.Millisecond
	tb := newTokenBucket(interval)
	ctx := context.Background()
	tb.PauseFor(100 * time.Millisecond)
	var mu sync.Mutex
	var released []time.Time
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sent := tb.Wait(ctx)
			mu.Lock()
			released = append(released, time.Now())
			mu.Unlock()
			sent()
		}()
	}
	wg.Wait()
	sort.Slice(released, func(i, j int) bool { return released[i].Before(released[j]) })
	for i := 1; i < len(released); i++ {
		if g := released[i].Sub(released[i-1]); g < interval {
			t.Fatalf("waiter %d released %v after the previous one, want >= %v", i, g, interval)
		}
	}
}

// A caller that never reports its request as sent must not block the host
// forever.
func TestTokenBucketSendHoldIsCapped(t *testing.T) {
	old := maxSendHold
	maxSendHold = 50 * time.Millisecond
	t.Cleanup(func() { maxSendHold = old })
	tb := newTokenBucket(10 * time.Millisecond)
	_ = tb.Wait(context.Background()) // sent never called
	done := make(chan struct{})
	go func() {
		tb.Wait(context.Background())()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second Wait still blocked after the send hold cap")
	}
}

// Cancelling a caller that holds or waits for the turn must not block others.
func TestTokenBucketCancelReleasesTurn(t *testing.T) {
	tb := newTokenBucket(time.Second)
	tb.Wait(context.Background())() // next slot is 1 s away
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	tb.Wait(ctx)()
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("cancelled Wait took %v", d)
	}
	// The cancelled caller sent nothing, so the next one still waits for the
	// slot one interval after the first request, and no longer.
	start = time.Now()
	tb.Wait(context.Background())()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Wait after a cancelled caller took %v, want < 1s", d)
	}
}

// Every request the crawl sends to the site counts against Crawl-delay:
// sitemap downloads that run alongside the workers, the soft-404 probe and
// redirect hops followed inside one fetch.
func TestCrawlDelayCoversBackgroundRequestsAndRedirects(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	var paths []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprintf(w, "User-agent: *\nCrawl-delay: 0.2\nSitemap: %s/sitemap.xml\n", srv.URL)
			return
		}
		mu.Lock()
		times = append(times, time.Now())
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>%s/sitemap-1.xml</loc></sitemap></sitemapindex>`, srv.URL)
		case "/sitemap-1.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%[1]s/s1</loc></url><url><loc>%[1]s/s2</loc></url></urlset>`, srv.URL)
		case "/old":
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><a href="/old">old</a><a href="/a">a</a></body></html>`)
		}
	}))
	defer srv.Close()

	_, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 2, MaxPages: 6,
		Concurrency: 8, UserAgent: "Micelio/1.0", SkipSSRF: true, RespectRobots: true,
		DiscoverSitemaps: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	seen := map[string]bool{}
	for _, p := range paths {
		seen[p] = true
	}
	for _, want := range []string{"/sitemap.xml", "/sitemap-1.xml", "/old", "/new"} {
		if !seen[want] {
			t.Fatalf("%s never requested (got %v); the test no longer covers it", want, paths)
		}
	}
	assertSpacing(t, times, paths, 200*time.Millisecond)
}
