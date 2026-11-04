package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// credRecorder is a server that records which credential headers it saw.
type credRecorder struct {
	mu   sync.Mutex
	seen []string
	srv  *httptest.Server
}

func (c *credRecorder) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, h := range []string{"Authorization", "Cookie", "X-Api-Key"} {
		if r.Header.Get(h) != "" {
			c.seen = append(c.seen, r.URL.Path+" "+h)
		}
	}
}

func (c *credRecorder) got() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.seen...)
}

// newEvil starts a server on "localhost", a different host than the
// 127.0.0.1 test sites, so redirects to it change host.
func newEvil(t *testing.T) *credRecorder {
	t.Helper()
	c := &credRecorder{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.record(r)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>x</title></head><body>hello</body></html>"))
	}))
	t.Cleanup(c.srv.Close)
	c.srv.URL = strings.Replace(c.srv.URL, "127.0.0.1", "localhost", 1)
	return c
}

func credFetchOpts() FetchOptions {
	return FetchOptions{
		SkipSSRF:      true,
		UserAgent:     "Micelio/1.0",
		Cookies:       "session=s3cret",
		CustomHeaders: map[string]string{"Authorization": "Bearer tok", "X-Api-Key": "k"},
	}
}

func TestCredentialsAllowed(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"https://example.com/", "https://example.com/a", true},
		{"https://example.com/", "https://www.example.com/a", true},
		{"https://example.com:8443/", "https://EXAMPLE.com/a", true},
		{"https://example.com/", "https://evil.com/", false},
		{"https://example.com/", "https://notexample.com/", false},
		{"https://www.example.com/", "https://example.com/", true},
		{"https://www.staging.example.com/", "https://staging.example.com/a", true},
		{"https://staging.example.com/", "https://www.staging.example.com/a", true},
		{"https://www.example.com/", "https://cdn.www.example.com/", true},
		{"https://www.example.com/", "http://example.com/", false},
		{"https://example.com/", "https://wwwexample.com/", false},
		{"https://www.example.com/", "https://wwwexample.com/", false},
		{"https://example.com/", "https://example.com.evil.com/", false},
		{"https://www.example.com/", "https://example.com.evil.com/", false},
		{"https://www.example.com/", "https://www.evil.com/", false},
		{"https://www.example.com/", "https://api.example.com/", false},
		{"https://example.com/", "http://example.com/", false},
		{"http://example.com/", "https://example.com/", true},
	}
	for _, c := range cases {
		f, _ := url.Parse(c.from)
		to, _ := url.Parse(c.to)
		if got := credentialsAllowed(f, to); got != c.want {
			t.Errorf("credentialsAllowed(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
	if credentialsAllowed(nil, &url.URL{}) {
		t.Error("nil from must not allow credentials")
	}
}

func TestFetchStripsCredentialsOnCrossHostRedirect(t *testing.T) {
	evil := newEvil(t)
	site := &credRecorder{}
	site.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site.record(r)
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body>ok</body></html>"))
		default:
			http.Redirect(w, r, evil.srv.URL+"/landing", http.StatusFound)
		}
	}))
	defer site.srv.Close()

	for _, head := range []bool{false, true} {
		if head {
			FetchHead(context.Background(), site.srv.URL+"/away", credFetchOpts())
		} else {
			res := FetchPage(context.Background(), site.srv.URL+"/away", credFetchOpts())
			if res.StatusCode != 200 {
				t.Fatalf("status = %d (%s)", res.StatusCode, res.Error)
			}
		}
	}
	if got := evil.got(); len(got) != 0 {
		t.Fatalf("other host received credentials: %v", got)
	}
	// The site itself still gets them, also after a same-host redirect.
	FetchPage(context.Background(), site.srv.URL+"/same", credFetchOpts())
	got := strings.Join(site.got(), ",")
	for _, want := range []string{"/away Authorization", "/away Cookie", "/final X-Api-Key", "/final Cookie"} {
		if !strings.Contains(got, want) {
			t.Errorf("site missing %q, saw %s", want, got)
		}
	}
}

func TestSoft404ProbeKeepsCredentialsOnSite(t *testing.T) {
	evil := newEvil(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/micelio-404-check-") {
			http.Redirect(w, r, evil.srv.URL+"/not-found", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>Home</title></head><body>home</body></html>"))
	}))
	defer srv.Close()
	cfg := types.CrawlConfig{
		SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 0, MaxPages: 1, Concurrency: 1,
		SkipSSRF:      true,
		Cookies:       "session=s3cret",
		CustomHeaders: map[string]string{"Authorization": "Bearer tok", "X-Api-Key": "k"},
	}
	res, err := Crawl(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Soft404Probes) != 1 || res.Soft404Probes[0].StatusCode != 200 {
		t.Fatalf("probe = %+v", res.Soft404Probes)
	}
	if got := evil.got(); len(got) != 0 {
		t.Fatalf("other host received credentials: %v", got)
	}
}
