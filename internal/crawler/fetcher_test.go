package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFetchPage200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><head><title>Test</title></head><body>Hello</body></html>")
	}))
	defer srv.Close()

	result := FetchPage(context.Background(), srv.URL+"/page", FetchOptions{
		UserAgent: "TestBot/1.0",
		SkipSSRF:  true,
	})

	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
	if result.HTML == "" {
		t.Error("Expected non-empty HTML")
	}
	if result.Error != "" {
		t.Errorf("Unexpected error: %s", result.Error)
	}
	if result.ResponseTimeMs < 0 {
		t.Error("Expected non-negative response time")
	}
}

func TestFetchPageRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			w.Header().Set("Location", "/new")
			w.WriteHeader(301)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>New page</body></html>")
	}))
	defer srv.Close()

	result := FetchPage(context.Background(), srv.URL+"/old", FetchOptions{
		UserAgent: "TestBot/1.0",
		SkipSSRF:  true,
	})

	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
	if len(result.RedirectChain) != 1 {
		t.Errorf("RedirectChain length = %d, want 1", len(result.RedirectChain))
	}
	if result.RedirectChain[0].StatusCode != 301 {
		t.Errorf("Redirect status = %d, want 301", result.RedirectChain[0].StatusCode)
	}
}

func TestFetchPageSSRFBlock(t *testing.T) {
	result := FetchPage(context.Background(), "http://127.0.0.1:1234/secret", FetchOptions{
		UserAgent: "TestBot/1.0",
	})

	if result.Error == "" {
		t.Error("Expected SSRF error for localhost")
	}
}

func TestFetchHead200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("Expected HEAD, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Robots-Tag", "noindex")
		w.Header().Set("Content-Length", "5000")
	}))
	defer srv.Close()

	result := FetchHead(context.Background(), srv.URL+"/page", FetchOptions{
		UserAgent: "TestBot/1.0",
		SkipSSRF:  true,
	})

	if result.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", result.StatusCode)
	}
	if result.XRobotsTag == nil || *result.XRobotsTag != "noindex" {
		t.Errorf("XRobotsTag = %v, want noindex", result.XRobotsTag)
	}
	if result.ContentLength == nil || *result.ContentLength != 5000 {
		t.Errorf("ContentLength = %v, want 5000", result.ContentLength)
	}
}

func TestCheckExternalURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	status, err := CheckExternalURL(context.Background(), srv.URL+"/check", "TestBot/1.0", true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if status != 200 {
		t.Errorf("Status = %d, want 200", status)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		header   string
		fallback int
		wantMs   int
	}{
		{"", 2000, 2000},
		{"5", 1000, 5000},
		{"120", 1000, 60000}, // Capped at 60s
	}

	for _, tt := range tests {
		got := parseRetryAfter(tt.header, time.Duration(tt.fallback)*time.Millisecond)
		wantDur := time.Duration(tt.wantMs) * time.Millisecond
		if got != wantDur {
			t.Errorf("parseRetryAfter(%q, %d) = %v, want %v", tt.header, tt.fallback, got, wantDur)
		}
	}
}

func TestIsChromeUA(t *testing.T) {
	tests := []struct {
		ua   string
		want bool
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36", true},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/136.0.0.0 Safari/537.36", true},
		{"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", false},
		{"Micelio/1.0", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isChromeUA(tt.ua); got != tt.want {
			t.Errorf("isChromeUA(%q) = %v, want %v", tt.ua, got, tt.want)
		}
	}
}

func TestExtractChromeVersion(t *testing.T) {
	tests := []struct {
		ua   string
		want string
	}{
		{"Mozilla/5.0 Chrome/131.0.0.0 Safari/537.36", "131"},
		{"Mozilla/5.0 Chrome/136.0.6778.0 Safari/537.36", "136"},
		{"Mozilla/5.0 Chrome/99.0.4844.51", "99"},
		{"Micelio/1.0", defaultChromeVersion},
		{"Chrome/", defaultChromeVersion}, // no dot after version
		{"", defaultChromeVersion},
	}
	for _, tt := range tests {
		if got := extractChromeVersion(tt.ua); got != tt.want {
			t.Errorf("extractChromeVersion(%q) = %q, want %q", tt.ua, got, tt.want)
		}
	}
}

func TestPlatformFromUA(t *testing.T) {
	tests := []struct {
		ua   string
		want string
	}{
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/131.0.0.0", "Windows"},
		{"Mozilla/5.0 (X11; Linux x86_64) Chrome/131.0.0.0", "Linux"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/131.0.0.0", "macOS"},
		{"Mozilla/5.0 (Linux; Android 14) Chrome/131.0.0.0 Mobile", "Android"},
		{"Mozilla/5.0 (X11; CrOS x86_64) Chrome/131.0.0.0", "Chrome OS"},
	}
	for _, tt := range tests {
		if got := platformFromUA(tt.ua); got != tt.want {
			t.Errorf("platformFromUA(%q) = %q, want %q", tt.ua, got, tt.want)
		}
	}
}

func TestSecFetchSite(t *testing.T) {
	parse := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	tests := []struct {
		referrer string
		reqURL   string
		want     string
	}{
		{"", "https://example.com/page", "none"},
		{"https://example.com/", "https://example.com/page", "same-origin"},
		{"https://www.example.com/", "https://api.example.com/data", "same-site"},
		{"https://other.com/", "https://example.com/page", "cross-site"},
		{"https://example.com/", "http://example.com/page", "same-site"}, // same eTLD+1, different scheme
	}
	for _, tt := range tests {
		if got := secFetchSite(tt.referrer, parse(tt.reqURL)); got != tt.want {
			t.Errorf("secFetchSite(%q, %q) = %q, want %q", tt.referrer, tt.reqURL, got, tt.want)
		}
	}
}

func TestChromeSecChUa(t *testing.T) {
	// Different versions should produce different Not-A-Brand tokens
	results := map[string]bool{}
	for _, ver := range []string{"131", "132", "133"} {
		h := chromeSecChUa(ver)
		results[h] = true
		if !strings.Contains(h, fmt.Sprintf(`"Chromium";v="%s"`, ver)) {
			t.Errorf("chromeSecChUa(%q) missing Chromium version: %s", ver, h)
		}
	}
	if len(results) < 2 {
		t.Error("expected brand rotation across versions")
	}
}

func TestJitter(t *testing.T) {
	d := 100 * time.Millisecond
	for i := 0; i < 100; i++ {
		j := jitter(d)
		if j < d || j > d+d/2 {
			t.Fatalf("jitter(%v) = %v, want [%v, %v]", d, j, d, d+d/2)
		}
	}
}

func TestIsTorProxy(t *testing.T) {
	tests := []struct {
		proxy string
		want  bool
	}{
		{"tor", true},
		{"socks5://127.0.0.1:9050", true},
		{"socks5://localhost:9050", true},
		{"http://proxy:8080", false},
		{"socks5://proxy:1080", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsTorProxy(tt.proxy); got != tt.want {
			t.Errorf("IsTorProxy(%q) = %v, want %v", tt.proxy, got, tt.want)
		}
	}
}

func TestTorManagerProxyURL(t *testing.T) {
	tm := NewTorManager("", "", 0)
	u := tm.ProxyURL()
	if u.Scheme != "socks5" || u.Host != "127.0.0.1:9050" {
		t.Errorf("ProxyURL() = %v, want socks5://127.0.0.1:9050", u)
	}
}

func TestTorManagerRequestCounting(t *testing.T) {
	tm := NewTorManager("127.0.0.1:99999", "", 10) // unreachable control port
	for i := 0; i < 25; i++ {
		tm.RecordRequest()
	}
	if tm.requestCount.Load() != 25 {
		t.Errorf("requestCount = %d, want 25", tm.requestCount.Load())
	}
}
