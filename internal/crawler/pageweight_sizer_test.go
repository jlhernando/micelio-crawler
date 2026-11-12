package crawler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// sizeServer answers HEAD with a Content-Length and records when each
// request arrived and how many ran at once.
type sizeServer struct {
	*httptest.Server
	mu       sync.Mutex
	arrivals []time.Time
	inFlight int
	maxIn    int
	respond  func(n int, w http.ResponseWriter) bool // true: answered
}

func newSizeServer(t *testing.T, hold time.Duration) *sizeServer {
	s := &sizeServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.arrivals = append(s.arrivals, time.Now())
		n := len(s.arrivals)
		s.inFlight++
		s.maxIn = max(s.maxIn, s.inFlight)
		respond := s.respond
		s.mu.Unlock()
		time.Sleep(hold)
		s.mu.Lock()
		s.inFlight--
		s.mu.Unlock()
		if respond != nil && respond(n, w) {
			return
		}
		w.Header().Set("Content-Length", "1000")
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *sizeServer) minGap() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts := append([]time.Time(nil), s.arrivals...)
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	gap := time.Hour
	for i := 1; i < len(ts); i++ {
		gap = min(gap, ts[i].Sub(ts[i-1]))
	}
	return gap
}

func resourceRefs(base string, n int) map[string][]types.ResourceEntry {
	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < n; i++ {
		refs["https://site.test/"] = append(refs["https://site.test/"], types.ResourceEntry{URL: fmt.Sprintf("%s/img%d.png", base, i), Type: "image"})
	}
	return refs
}

func hostPort(rawURL string) string {
	u, _ := url.Parse(rawURL)
	return u.Host
}

// A host that is not the crawled site (a CDN) does not wait the crawl's
// delay: it gets its own pace of at most 10 requests/s and at most
// assetPerHost at once.
func TestResourceSizerAssetHostOwnPace(t *testing.T) {
	cdn := newSizeServer(t, 150*time.Millisecond)
	opts := FetchOptions{SkipSSRF: true, UserAgent: "test"}
	crawlRL := newHostRateLimiter(time.Second, nil) // crawl delay: 1 s
	crawlRL.bucket("site.test")
	sizer := newResourceSizer(opts, opts, map[string]bool{"site.test": true}, crawlRL)

	const n = 20
	start := time.Now()
	sizes, sum := analysis.FetchResourceSizes(context.Background(), resourceRefs(cdn.URL, n), analysis.PageWeightOptions{Concurrency: 8, Sizer: sizer})
	took := time.Since(start)

	if len(sizes) != n || sum.Sized != n {
		t.Fatalf("sized %d/%d: %+v", len(sizes), n, sum)
	}
	if cdn.maxIn > assetPerHost {
		t.Errorf("%d requests at once on the CDN, limit %d", cdn.maxIn, assetPerHost)
	}
	if cdn.maxIn < 2 {
		t.Errorf("CDN never got parallel requests (max %d)", cdn.maxIn)
	}
	// The bucket spaces sends by assetInterval; allow scheduler jitter.
	if g := cdn.minGap(); g < assetInterval*8/10 {
		t.Errorf("two CDN requests %s apart: faster than 10/s", g)
	}
	if took > 5*time.Second {
		t.Errorf("took %s: waited on the crawl delay", took)
	}
	if crawlRL.knows(hostPort(cdn.URL)) {
		t.Error("the CDN went through the crawl limiter")
	}
}

// The crawled site's own hosts keep the crawl limiter (delay) and at most
// DefaultPageWeightPerHost requests at once.
func TestResourceSizerSiteHostKeepsCrawlDelay(t *testing.T) {
	site := newSizeServer(t, 0)
	opts := FetchOptions{SkipSSRF: true, UserAgent: "test"}
	crawlRL := newHostRateLimiter(250*time.Millisecond, nil)
	sizer := newResourceSizer(opts, opts, map[string]bool{hostPort(site.URL): true}, crawlRL)

	sizes, _ := analysis.FetchResourceSizes(context.Background(), resourceRefs(site.URL, 5), analysis.PageWeightOptions{Sizer: sizer})
	if len(sizes) != 5 {
		t.Fatalf("sized %d/5", len(sizes))
	}
	if g := site.minGap(); g < 200*time.Millisecond {
		t.Errorf("site requests %s apart, crawl delay 250ms", g)
	}
	if site.maxIn > analysis.DefaultPageWeightPerHost {
		t.Errorf("%d requests at once on the site", site.maxIn)
	}
}

// A host the crawl fetched pages from (not the seed domain, e.g. a list-mode
// host) is treated as the site too.
func TestResourceSizerKnownCrawlHostIsSite(t *testing.T) {
	other := newSizeServer(t, 0)
	opts := FetchOptions{SkipSSRF: true, UserAgent: "test"}
	crawlRL := newHostRateLimiter(250*time.Millisecond, nil)
	crawlRL.bucket(hostPort(other.URL)) // the crawl sent pages there
	sizer := newResourceSizer(opts, opts, map[string]bool{"site.test": true}, crawlRL)
	if got := sizer.PerHost(hostPort(other.URL)); got != analysis.DefaultPageWeightPerHost {
		t.Errorf("per-host limit %d for a crawled host", got)
	}
	analysis.FetchResourceSizes(context.Background(), resourceRefs(other.URL, 3), analysis.PageWeightOptions{Sizer: sizer})
	if g := other.minGap(); g < 200*time.Millisecond {
		t.Errorf("crawled host requests %s apart, crawl delay 250ms", g)
	}
}

// A 429 with Retry-After stops requests to that host for the pause, then
// the resource is asked again and gets its size.
func TestResourceSizerHonours429RetryAfter(t *testing.T) {
	cdn := newSizeServer(t, 0)
	var refusedAt time.Time
	cdn.respond = func(n int, w http.ResponseWriter) bool {
		if n == 1 {
			refusedAt = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	}
	opts := FetchOptions{SkipSSRF: true, UserAgent: "test"}
	sizer := newResourceSizer(opts, opts, map[string]bool{}, nil)
	sizes, sum := analysis.FetchResourceSizes(context.Background(), resourceRefs(cdn.URL, 6), analysis.PageWeightOptions{Sizer: sizer})
	if len(sizes) != 6 || sum.Throttles != 1 || sum.Requests != 7 {
		t.Fatalf("sized %d/6, summary %+v", len(sizes), sum)
	}
	cdn.mu.Lock()
	defer cdn.mu.Unlock()
	for _, at := range cdn.arrivals[1:] {
		// One request may have left before the 429 came back (the bucket
		// lets one out every 100 ms); none after it until the pause ends.
		if d := at.Sub(refusedAt); d > 150*time.Millisecond && d < 900*time.Millisecond {
			t.Errorf("request %s after the 429, Retry-After 1s", d)
		}
	}
}

// After a 429 an asset host is asked more slowly for the rest of the run:
// its gap doubles (100 ms -> 200 ms), once per pause even when several
// answers in flight come back 429.
func TestResourceSizer429SlowsAssetHost(t *testing.T) {
	cdn := newSizeServer(t, 0)
	var refusedAt time.Time
	cdn.respond = func(n int, w http.ResponseWriter) bool {
		if n == 3 {
			refusedAt = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	}
	opts := FetchOptions{SkipSSRF: true, UserAgent: "test"}
	sizer := newResourceSizer(opts, opts, map[string]bool{}, nil)
	sizes, sum := analysis.FetchResourceSizes(context.Background(), resourceRefs(cdn.URL, 8), analysis.PageWeightOptions{Sizer: sizer})
	if len(sizes) != 8 || sum.Throttles != 1 || sum.RateLimited != 0 {
		t.Fatalf("sized %d/8, summary %+v", len(sizes), sum)
	}
	if got := assetHosts.limiter().Interval(cdn.URL); got != 2*assetInterval {
		t.Errorf("interval after one 429 = %s, want %s", got, 2*assetInterval)
	}
	cdn.mu.Lock()
	defer cdn.mu.Unlock()
	var after []time.Time
	for _, at := range cdn.arrivals {
		if at.Sub(refusedAt) > 900*time.Millisecond {
			after = append(after, at)
		}
	}
	if len(after) < 3 {
		t.Fatalf("only %d requests after the pause", len(after))
	}
	for i := 1; i < len(after); i++ {
		if g := after[i].Sub(after[i-1]); g < 2*assetInterval*8/10 {
			t.Errorf("requests %s apart after a 429, want >= %s", g, 2*assetInterval)
		}
	}
}

// One URL that answers 429 every time (Wikimedia does that for a thumbnail
// it cannot render) is left unsized; the host keeps being sized and its
// pace comes back once it answers again. On a 10K Wikipedia crawl one such
// thumbnail left 9,089 resources of its host unsized.
func TestResourceSizerOneRefusedURL(t *testing.T) {
	old := assetRecoverAfter
	assetRecoverAfter = 5
	defer func() { assetRecoverAfter = old }()
	cdn := newSizeServer(t, 0)
	var mu sync.Mutex
	cdn.Server.Config.Handler = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/img1.png" { // asked early: answers follow
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}(cdn.Server.Config.Handler)
	opts := FetchOptions{SkipSSRF: true, UserAgent: "test"}
	sizer := newResourceSizer(opts, opts, map[string]bool{}, nil)
	sizes, sum := analysis.FetchResourceSizes(context.Background(), resourceRefs(cdn.URL, 20), analysis.PageWeightOptions{Sizer: sizer})
	if len(sizes) != 19 || sum.RateLimited != 0 || sum.Refused != 1 {
		t.Fatalf("sized %d/19, summary %+v", len(sizes), sum)
	}
	if got := assetHosts.limiter().Interval(cdn.URL); got != assetInterval {
		t.Errorf("interval %s after the host answered again, want %s", got, assetInterval)
	}
}

// namedHostServer answers HEAD for any host name: its client dials the test
// server whatever the URL's host, so tests can use real-looking names
// (static.example.com) and still reach it. It records the requests per Host.
type namedHostServer struct {
	*sizeServer
	port   string
	client *http.Client
	hmu    sync.Mutex
	seen   map[string][]http.Header // Host header -> request headers
}

func newNamedHostServer(t *testing.T) *namedHostServer {
	s := &namedHostServer{sizeServer: newSizeServer(t, 0), seen: map[string][]http.Header{}}
	inner := s.Server.Config.Handler
	s.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hmu.Lock()
		s.seen[r.Host] = append(s.seen[r.Host], r.Header.Clone())
		s.hmu.Unlock()
		inner.ServeHTTP(w, r)
	})
	addr := s.Listener.Addr().String()
	_, s.port, _ = net.SplitHostPort(addr)
	s.client = &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return s
}

func (s *namedHostServer) url(host string) string { return "http://" + host + ":" + s.port }

func (s *namedHostServer) headers(host string) []http.Header {
	s.hmu.Lock()
	defer s.hmu.Unlock()
	return s.seen[host+":"+s.port]
}

// credentialOpts are the crawl's options (cookies, custom headers) and the
// same without them, as resourceSizer builds them.
func credentialOpts(client *http.Client) (site, asset FetchOptions) {
	site = FetchOptions{SkipSSRF: true, UserAgent: "test", Client: client,
		Cookies: "sid=secret", CustomHeaders: map[string]string{"Authorization": "Bearer secret", "X-Custom": "1"}}
	asset = site
	asset.Cookies = ""
	asset.CustomHeaders = nil
	return site, asset
}

// A third-party host the crawl limiter has a bucket for (with JS rendering,
// an iframe or script host such as www.google.com or youtube) is paced by
// the crawl limiter but never gets the crawl's cookies or custom headers;
// the seed host does.
func TestResourceSizerKnownThirdPartyGetsNoCredentials(t *testing.T) {
	srv := newNamedHostServer(t)
	site, asset := credentialOpts(srv.client)
	crawlRL := newHostRateLimiter(10*time.Millisecond, nil)
	crawlRL.bucket("www.google.com:" + srv.port) // the JS render fetched it
	sizer := newResourceSizer(site, asset, map[string]bool{"www.example.com": true}, crawlRL)

	refs := map[string][]types.ResourceEntry{"https://www.example.com/": {
		{URL: srv.url("www.google.com") + "/maps.js"},
		{URL: srv.url("www.example.com") + "/app.js"},
	}}
	sizes, _ := analysis.FetchResourceSizes(context.Background(), refs, analysis.PageWeightOptions{Sizer: sizer})
	if len(sizes) != 2 {
		t.Fatalf("sized %d/2", len(sizes))
	}
	third := srv.headers("www.google.com")
	if len(third) != 1 {
		t.Fatalf("third-party host got %d requests, want 1", len(third))
	}
	for _, h := range third {
		if h.Get("Cookie") != "" || h.Get("Authorization") != "" || h.Get("X-Custom") != "" {
			t.Errorf("third-party host got credentials: %v", h)
		}
	}
	got := srv.headers("www.example.com")
	if len(got) != 1 || got[0].Get("Cookie") != "sid=secret" || got[0].Get("Authorization") != "Bearer secret" {
		t.Errorf("seed host headers %v, want the crawl's cookie and headers", got)
	}
	if n := sizer.PerHost("www.google.com:" + srv.port); n != analysis.DefaultPageWeightPerHost {
		t.Errorf("known host per-host limit %d, want the crawl's %d", n, analysis.DefaultPageWeightPerHost)
	}
}

// The site's own subdomains (static.example.com for a www.example.com
// crawl) keep the crawl delay and per-host limit, not the 10/s asset pace,
// even when the crawl never fetched a page there; they get no credentials.
func TestResourceSizerSiteSubdomainKeepsCrawlDelay(t *testing.T) {
	srv := newNamedHostServer(t)
	site, asset := credentialOpts(srv.client)
	crawlRL := newHostRateLimiter(250*time.Millisecond, nil)
	sizer := newResourceSizer(site, asset, map[string]bool{"www.example.com": true}, crawlRL)

	static := "static.example.com:" + srv.port
	if n := sizer.PerHost(static); n != analysis.DefaultPageWeightPerHost {
		t.Errorf("subdomain per-host limit %d, want %d", n, analysis.DefaultPageWeightPerHost)
	}
	if n := sizer.PerHost("cdn.other.com"); n != assetPerHost {
		t.Errorf("other site's per-host limit %d, want %d", n, assetPerHost)
	}
	refs := map[string][]types.ResourceEntry{}
	for i := 0; i < 4; i++ {
		refs["https://www.example.com/"] = append(refs["https://www.example.com/"], types.ResourceEntry{URL: fmt.Sprintf("%s/img%d.png", srv.url("static.example.com"), i)})
	}
	sizes, _ := analysis.FetchResourceSizes(context.Background(), refs, analysis.PageWeightOptions{Sizer: sizer})
	if len(sizes) != 4 {
		t.Fatalf("sized %d/4", len(sizes))
	}
	if g := srv.minGap(); g < 200*time.Millisecond {
		t.Errorf("subdomain requests %s apart, crawl delay 250ms", g)
	}
	if !crawlRL.knows(static) {
		t.Error("the subdomain did not go through the crawl limiter")
	}
	for _, h := range srv.headers("static.example.com") {
		if h.Get("Cookie") != "" || h.Get("Authorization") != "" {
			t.Errorf("subdomain got credentials: %v", h)
		}
	}
}

func TestRegistrableDomain(t *testing.T) {
	for in, want := range map[string]string{
		"static.example.com":       "example.com",
		"Images.Example.co.uk:443": "example.co.uk",
		"example.com":              "example.com",
		"user.github.io":           "user.github.io",
		"127.0.0.1:8080":           "",
		"[::1]:80":                 "",
		"localhost":                "",
	} {
		if got := registrableDomain(in); got != want {
			t.Errorf("registrableDomain(%q) = %q, want %q", in, got, want)
		}
	}
}
