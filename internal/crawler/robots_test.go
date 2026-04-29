package crawler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAllowedByRobots(t *testing.T) {
	robotsTxt := `User-agent: *
Disallow: /admin/
Disallow: /private
Allow: /admin/public

User-agent: Googlebot
Disallow: /no-google/
`
	tests := []struct {
		url   string
		agent string
		want  bool
	}{
		{"https://example.com/", "*", true},
		{"https://example.com/admin/", "*", false},
		{"https://example.com/admin/secret", "*", false},
		{"https://example.com/admin/public", "*", true}, // longer Allow wins
		{"https://example.com/private", "*", false},
		{"https://example.com/public", "*", true},
		{"https://example.com/no-google/page", "Googlebot", false},
		{"https://example.com/admin/", "Googlebot", false}, // falls back to *
		{"https://example.com/", "Googlebot", true},
	}

	for _, tc := range tests {
		got := IsAllowedByRobots(robotsTxt, tc.url, tc.agent)
		if got != tc.want {
			t.Errorf("IsAllowedByRobots(%q, %q) = %v, want %v", tc.url, tc.agent, got, tc.want)
		}
	}
}

func TestIsAllowedEmptyRobots(t *testing.T) {
	if !IsAllowedByRobots("", "https://example.com/admin", "*") {
		t.Fatal("empty robots.txt should allow all")
	}
}

func TestMatchDirective(t *testing.T) {
	tests := []struct {
		line      string
		directive string
		want      string
	}{
		{"User-agent: Googlebot", "User-agent", "Googlebot"},
		{"Disallow: /admin/", "Disallow", "/admin/"},
		{"Allow: /public", "Allow", "/public"},
		{"Sitemap: https://example.com/sitemap.xml", "Sitemap", "https://example.com/sitemap.xml"},
		{"Crawl-delay: 5", "Crawl-delay", "5"},
		{"user-agent: bot", "User-agent", "bot"}, // case insensitive
		{"Not a directive", "User-agent", ""},
		{"", "User-agent", ""},
	}
	for _, tc := range tests {
		got := MatchDirective(tc.line, tc.directive)
		if got != tc.want {
			t.Errorf("MatchDirective(%q, %q) = %q, want %q", tc.line, tc.directive, got, tc.want)
		}
	}
}

func TestRobotsCheckerInit(t *testing.T) {
	robotsBody := `User-agent: *
Disallow: /blocked/
Crawl-delay: 2

Sitemap: https://example.com/sitemap.xml
Sitemap: https://example.com/sitemap-news.xml
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(robotsBody))
	}))
	defer srv.Close()

	rc := &RobotsChecker{SkipSSRF: true}
	if err := rc.Init(t.Context(), srv.URL, "TestBot"); err != nil {
		t.Fatal(err)
	}

	if !rc.IsAllowed(srv.URL+"/page", "TestBot") {
		t.Fatal("expected /page to be allowed")
	}
	if rc.IsAllowed(srv.URL+"/blocked/page", "TestBot") {
		t.Fatal("expected /blocked/page to be disallowed")
	}
	if rc.IsUnavailable() {
		t.Fatal("expected available")
	}

	sitemaps := rc.SitemapURLs()
	if len(sitemaps) != 2 {
		t.Fatalf("expected 2 sitemaps, got %d", len(sitemaps))
	}

	delay := rc.CrawlDelay("TestBot")
	if delay != 2 {
		t.Fatalf("expected crawl delay 2, got %d", delay)
	}
}

func TestRobotsChecker5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	rc := &RobotsChecker{SkipSSRF: true}
	if err := rc.Init(t.Context(), srv.URL, "TestBot"); err != nil {
		t.Fatal(err)
	}

	if !rc.IsUnavailable() {
		t.Fatal("expected unavailable for 5xx")
	}
	if rc.IsAllowed(srv.URL+"/page", "TestBot") {
		t.Fatal("expected disallowed when 5xx")
	}
}

func TestRobotsChecker404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	rc := &RobotsChecker{SkipSSRF: true}
	if err := rc.Init(t.Context(), srv.URL, "TestBot"); err != nil {
		t.Fatal(err)
	}

	if rc.IsUnavailable() {
		t.Fatal("404 should not be unavailable")
	}
	if !rc.IsAllowed(srv.URL+"/anything", "TestBot") {
		t.Fatal("expected all allowed when 404 (no robots.txt)")
	}
}

func TestRobotsCheckerCrawlDelaySpecific(t *testing.T) {
	robotsBody := `User-agent: SlowBot
Crawl-delay: 10

User-agent: *
Crawl-delay: 1
`
	rc := &RobotsChecker{body: robotsBody, fetched: true}
	rc.parseExtras()

	if delay := rc.CrawlDelay("SlowBot"); delay != 10 {
		t.Fatalf("expected 10 for SlowBot, got %d", delay)
	}
	if delay := rc.CrawlDelay("OtherBot"); delay != 1 {
		t.Fatalf("expected 1 for wildcard, got %d", delay)
	}
	if delay := rc.CrawlDelay("UnknownBot"); delay != 1 {
		t.Fatalf("expected 1 for unknown, got %d", delay)
	}
}
