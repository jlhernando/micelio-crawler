package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// Examples from Google's robots.txt spec:
// https://developers.google.com/search/docs/crawling-indexing/robots/robots_txt
func TestRobotsGooglePathMatching(t *testing.T) {
	cases := []struct {
		pattern string
		path    string
		match   bool
	}{
		// "/" and "/*"
		{"/", "/", true},
		{"/", "/any/page.html", true},
		{"/*", "/", true},
		{"/*", "/any/page.html", true},
		// "/$" matches only the root
		{"/$", "/", true},
		{"/$", "/page.htm", false},
		// "/fish"
		{"/fish", "/fish", true},
		{"/fish", "/fish.html", true},
		{"/fish", "/fish/salmon.html", true},
		{"/fish", "/fishheads", true},
		{"/fish", "/fishheads/yummy.html", true},
		{"/fish", "/fish.php?id=anything", true},
		{"/fish", "/Fish.asp", false},
		{"/fish", "/catfish", false},
		{"/fish", "/?id=fish", false},
		{"/fish", "/desert/fish", false},
		// "/fish*" is equivalent to "/fish"
		{"/fish*", "/fish", true},
		{"/fish*", "/fish.html", true},
		{"/fish*", "/fishheads/yummy.html", true},
		{"/fish*", "/fish.php?id=anything", true},
		{"/fish*", "/Fish.asp", false},
		{"/fish*", "/catfish", false},
		{"/fish*", "/?id=fish", false},
		{"/fish*", "/desert/fish", false},
		// "/fish/"
		{"/fish/", "/fish/", true},
		{"/fish/", "/fish/?id=anything", true},
		{"/fish/", "/fish/salmon.htm", true},
		{"/fish/", "/fish", false},
		{"/fish/", "/fish.html", false},
		{"/fish/", "/animals/fish/", false},
		{"/fish/", "/Fish/Salmon.asp", false},
		// "/*.php"
		{"/*.php", "/index.php", true},
		{"/*.php", "/filename.php", true},
		{"/*.php", "/folder/filename.php", true},
		{"/*.php", "/folder/filename.php?parameters", true},
		{"/*.php", "/folder/any.php.file.html", true},
		{"/*.php", "/filename.php/", true},
		{"/*.php", "/", false},
		{"/*.php", "/windows.PHP", false},
		// "/*.php$"
		{"/*.php$", "/filename.php", true},
		{"/*.php$", "/folder/filename.php", true},
		{"/*.php$", "/filename.php?parameters", false},
		{"/*.php$", "/filename.php/", false},
		{"/*.php$", "/filename.php5", false},
		{"/*.php$", "/windows.PHP", false},
		// "/fish*.php"
		{"/fish*.php", "/fish.php", true},
		{"/fish*.php", "/fishheads/catfish.php?parameters", true},
		{"/fish*.php", "/Fish.PHP", false},
	}
	for _, tc := range cases {
		robots := "User-agent: *\nDisallow: " + tc.pattern + "\n"
		blocked := !IsAllowedByRobots(robots, "https://example.com"+tc.path, "Micelio/1.0")
		if blocked != tc.match {
			t.Errorf("pattern %q path %q: matched=%v want %v", tc.pattern, tc.path, blocked, tc.match)
		}
	}
}

func TestRobotsGooglePrecedence(t *testing.T) {
	cases := []struct {
		name   string
		robots string
		path   string
		want   bool
	}{
		{"longer allow wins", "allow: /p\ndisallow: /", "/page", true},
		{"tie: allow wins", "allow: /folder\ndisallow: /folder", "/folder/page", true},
		{"longer wildcard disallow wins", "allow: /page\ndisallow: /*.htm", "/page.htm", false},
		{"equal length: allow wins", "allow: /page\ndisallow: /*.ph", "/page.php", true},
		{"root anchor allow", "allow: /$\ndisallow: /", "/", true},
		{"root anchor allow, other page", "allow: /$\ndisallow: /", "/page.htm", false},
		{"empty disallow allows all", "disallow:", "/anything", true},
		{"query is matched", "disallow: /*?sort=", "/list?sort=asc", false},
		{"query absent", "disallow: /*?sort=", "/list", true},
		{"robots.txt always allowed", "disallow: /", "/robots.txt", true},
	}
	for _, tc := range cases {
		robots := "user-agent: *\n" + tc.robots + "\n"
		got := IsAllowedByRobots(robots, "https://example.com"+tc.path, "Micelio/1.0")
		if got != tc.want {
			t.Errorf("%s: allowed(%q)=%v want %v", tc.name, tc.path, got, tc.want)
		}
	}
}

func TestRobotsGroupSelection(t *testing.T) {
	robots := `# comment
user-agent: googlebot-news
disallow: /news-only

user-agent: *
disallow: /all

user-agent: googlebot
disallow: /web-only

User-agent: Micelio
Disallow: /m1
user-agent: micelio
disallow: /m2   # inline comment
crawl-delay: 1.5
`
	cases := []struct {
		ua, path string
		want     bool
	}{
		{"Googlebot-News", "/news-only", false},
		{"Googlebot-News", "/all", true}, // own group only
		{"Googlebot", "/web-only", false},
		{"Googlebot", "/all", true},
		{"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", "/web-only", false},
		{"OtherBot/2.0", "/all", false},
		{"OtherBot/2.0", "/web-only", true},
		// Product token before "/" selects the group, case-insensitive; groups merge.
		{"Micelio/1.0", "/m1", false},
		{"Micelio/1.0", "/m2", false},
		{"Micelio/1.0", "/all", true},
	}
	for _, tc := range cases {
		got := IsAllowedByRobots(robots, "https://example.com"+tc.path, tc.ua)
		if got != tc.want {
			t.Errorf("ua %q path %q: allowed=%v want %v", tc.ua, tc.path, got, tc.want)
		}
	}

	rc := &RobotsChecker{body: robots, fetched: true}
	rc.parseExtras()
	if d := rc.CrawlDelayDuration("Micelio/1.0"); d != 1500*time.Millisecond {
		t.Errorf("crawl-delay for Micelio = %v, want 1.5s", d)
	}
	if d := rc.CrawlDelay("Micelio/1.0"); d != 2 {
		t.Errorf("CrawlDelay rounded = %d, want 2", d)
	}
	if d := rc.CrawlDelayDuration("OtherBot"); d != 0 {
		t.Errorf("crawl-delay for OtherBot = %v, want 0", d)
	}
}

func TestRobotsPercentEncoding(t *testing.T) {
	robots := "user-agent: *\ndisallow: /ü/\ndisallow: /a%3cd\n"
	if IsAllowedByRobots(robots, "https://example.com/%C3%BC/page", "x") {
		t.Error("non-ASCII pattern should match percent-encoded path")
	}
	if IsAllowedByRobots(robots, "https://example.com/a%3Cd", "x") {
		t.Error("percent escapes should compare case-insensitively")
	}
}

func TestRobotsFollowsRedirects(t *testing.T) {
	var target *httptest.Server
	target = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /*.pdf$\nCrawl-delay: 3\nSitemap: https://example.com/s.xml\n")
			return
		}
		http.NotFound(w, r)
	}))
	defer target.Close()
	// Two hops: origin -> /hop -> target (like http->https, apex->www).
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			http.Redirect(w, r, "/hop", http.StatusMovedPermanently)
		case "/hop":
			http.Redirect(w, r, target.URL+"/robots.txt", http.StatusFound)
		}
	}))
	defer origin.Close()

	rc := &RobotsChecker{SkipSSRF: true}
	if err := rc.Init(t.Context(), origin.URL, "Micelio/1.0"); err != nil {
		t.Fatal(err)
	}
	if rc.StatusCode() != 200 {
		t.Fatalf("status=%d want 200 after redirects", rc.StatusCode())
	}
	if rc.IsAllowed(origin.URL+"/doc.pdf", "Micelio/1.0") {
		t.Error("/doc.pdf should be disallowed by redirected robots.txt")
	}
	if !rc.IsAllowed(origin.URL+"/doc.pdf?x=1", "Micelio/1.0") {
		t.Error("/doc.pdf?x=1 should be allowed ($ anchor)")
	}
	if rc.CrawlDelay("Micelio/1.0") != 3 {
		t.Errorf("crawl-delay lost across redirect: %d", rc.CrawlDelay("Micelio/1.0"))
	}
	if len(rc.SitemapURLs()) != 1 {
		t.Errorf("sitemaps lost across redirect: %v", rc.SitemapURLs())
	}
}

func TestRobotsTooManyRedirectsAllowsAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/robots.txt?n="+r.URL.RawQuery+"x", http.StatusFound)
	}))
	defer srv.Close()
	rc := &RobotsChecker{SkipSSRF: true}
	if err := rc.Init(t.Context(), srv.URL, "Micelio/1.0"); err != nil {
		t.Fatal(err)
	}
	if rc.IsUnavailable() || !rc.IsAllowed(srv.URL+"/x", "Micelio/1.0") {
		t.Error("redirect loop should be treated like 404 (allow all)")
	}
}

func TestRobots429IsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	rc := &RobotsChecker{SkipSSRF: true}
	if err := rc.Init(t.Context(), srv.URL, "Micelio/1.0"); err != nil {
		t.Fatal(err)
	}
	if !rc.IsUnavailable() || rc.IsAllowed(srv.URL+"/x", "Micelio/1.0") {
		t.Error("429 on robots.txt should disallow all (Google treats it as 5xx)")
	}
}

func TestRobotsUsesProvidedClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: *\nDisallow: /x\n")
	}))
	defer srv.Close()
	var used atomic.Bool
	base := UnsafeClient(5*time.Second, nil)
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			used.Store(true)
			return base.Transport.RoundTrip(r)
		}),
		CheckRedirect: base.CheckRedirect,
	}
	rc := &RobotsChecker{SkipSSRF: true, Client: client}
	if err := rc.Init(t.Context(), srv.URL, "Micelio/1.0"); err != nil {
		t.Fatal(err)
	}
	if !used.Load() {
		t.Fatal("robots.txt must be fetched through the crawl client (proxy/Tor aware)")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestCrawlRespectsRobotsWildcards runs a real crawl against a local server
// and checks that wildcard/anchor rules block fetches.
func TestCrawlRespectsRobotsWildcards(t *testing.T) {
	var mu sync.Mutex
	fetched := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /*?sort=\nDisallow: /*.php$\nAllow: /private/ok\nDisallow: /private\n")
			return
		}
		mu.Lock()
		fetched[r.URL.RequestURI()] = true
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<html><body>
<a href="/list?sort=asc">a</a><a href="/list">b</a>
<a href="/index.php">c</a><a href="/index.php?x=1">d</a>
<a href="/private/secret">e</a><a href="/private/ok">f</a>
</body></html>`)
			return
		}
		fmt.Fprint(w, `<html><body>ok</body></html>`)
	}))
	defer srv.Close()

	_, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 2, MaxPages: 50,
		Concurrency: 3, UserAgent: "Micelio/1.0", SkipSSRF: true, RespectRobots: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, u := range []string{"/list?sort=asc", "/index.php", "/private/secret"} {
		if fetched[u] {
			t.Errorf("%s fetched despite robots.txt", u)
		}
	}
	for _, u := range []string{"/list", "/index.php?x=1", "/private/ok"} {
		if !fetched[u] {
			t.Errorf("%s should have been fetched", u)
		}
	}
}

// TestCrawlRobotsPerAllowedDomain checks that robots.txt is fetched and applied
// for every host the crawl visits, not only the seed host.
func TestCrawlRobotsPerAllowedDomain(t *testing.T) {
	var otherHits sync.Map
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /blocked\n")
			return
		}
		otherHits.Store(r.URL.Path, true)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>other</body></html>`)
	}))
	defer other.Close()
	seed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><a href="%s/blocked">x</a><a href="%s/open">y</a></body></html>`, other.URL, other.URL)
	}))
	defer seed.Close()

	otherHost := strings.TrimPrefix(other.URL, "http://")
	otherHostname := otherHost[:strings.LastIndex(otherHost, ":")]
	_, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: seed.URL + "/", Mode: types.ModeSpider, MaxDepth: 2, MaxPages: 20,
		Concurrency: 2, UserAgent: "Micelio/1.0", SkipSSRF: true, RespectRobots: true,
		AllowedDomains: []string{otherHostname},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, hit := otherHits.Load("/blocked"); hit {
		t.Error("/blocked on allowed domain fetched despite its robots.txt")
	}
}

// TestCrawlRetryAfterPausesHost checks that a 429 with Retry-After pauses all
// workers for the host and that URLs are retried at most 3 times.
func TestCrawlRetryAfterPausesHost(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	var flakyHits atomic.Int32
	var deadHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		switch r.URL.Path {
		case "/flaky":
			if flakyHits.Add(1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
		case "/dead":
			deadHits.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<html><body><a href="/flaky">f</a><a href="/dead">d</a><a href="/a">a</a><a href="/b">b</a></body></html>`)
			return
		}
		fmt.Fprint(w, `<html><body>ok</body></html>`)
	}))
	defer srv.Close()

	res, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 2, MaxPages: 20,
		Concurrency: 4, UserAgent: "Micelio/1.0", SkipSSRF: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := flakyHits.Load(); got != 2 {
		t.Errorf("/flaky fetched %d times, want 2 (429 then success)", got)
	}
	if got, want := int(deadHits.Load()), types.DefaultRetries+1; got != want {
		t.Errorf("/dead fetched %d times, want %d (attempt cap)", got, want)
	}
	var flakyStatus int
	for _, p := range res.Pages {
		if strings.HasSuffix(p.URL, "/flaky") {
			flakyStatus = p.StatusCode
		}
	}
	if flakyStatus != 200 {
		t.Errorf("/flaky final status %d, want 200", flakyStatus)
	}
	// After the 429 (Retry-After: 1), no request may reach the host for ~1s.
	mu.Lock()
	defer mu.Unlock()
	maxGap := time.Duration(0)
	for i := 1; i < len(times); i++ {
		if g := times[i].Sub(times[i-1]); g > maxGap {
			maxGap = g
		}
	}
	if maxGap < 900*time.Millisecond {
		t.Errorf("expected a >=1s pause after Retry-After, largest gap was %v", maxGap)
	}
}

// TestCrawlDelayNeverUndercut checks the effective spacing with adaptive rate
// on, as the server sees it. New connections take 120 ms to set up (like
// TCP+TLS to a real server) and the server closes every other connection, so
// some requests go out late while the next one reuses a warm connection. The
// old limiter booked slots in advance and sent that next request on its
// original slot, 80 ms after the late one.
func TestCrawlDelayNeverUndercut(t *testing.T) {
	slowNewConnections(t, 120*time.Millisecond)
	var mu sync.Mutex
	var times []time.Time
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nCrawl-delay: 0.2\n")
			return
		}
		mu.Lock()
		times = append(times, time.Now())
		paths = append(paths, r.URL.Path)
		closeConn := len(times)%2 == 1
		mu.Unlock()
		if closeConn {
			w.Header().Set("Connection", "close")
		}
		w.Header().Set("Content-Type", "text/html")
		html := "<html><body>"
		for i := 0; i < 12; i++ {
			html += fmt.Sprintf(`<a href="/p%d">x</a>`, i)
		}
		fmt.Fprint(w, html+"</body></html>")
	}))
	defer srv.Close()

	_, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 2, MaxPages: 10,
		Concurrency: 8, UserAgent: "Micelio/1.0", SkipSSRF: true, RespectRobots: true,
		AdaptiveRate: true, DelayMs: 50, DelayExplicit: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(times) < 8 {
		t.Fatalf("only %d requests reached the server", len(times))
	}
	assertSpacing(t, times, paths, 200*time.Millisecond)
}
