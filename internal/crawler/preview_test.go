package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestPreviewHomepageMatchesCrawlTransport(t *testing.T) {
	var pages, effects, wrongCredentials atomic.Int32
	var responseStatus atomic.Int32
	responseStatus.Store(200)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /private\n")
			return
		case "/favicon.ico":
			return
		case "/effect":
			effects.Add(1)
			if r.Header.Get("Cookie") != "" || r.Header.Get("X-Preview") != "" {
				wrongCredentials.Add(1)
			}
			return
		case "/":
			pages.Add(1)
			if r.URL.RawQuery != "" || r.Header.Get("User-Agent") != "PreviewBot" || r.Header.Get("Cookie") != "session=test" || r.Header.Get("X-Preview") != "yes" {
				wrongCredentials.Add(1)
			}
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(int(responseStatus.Load()))
			fmt.Fprint(w, `<html><title>Source heading</title><body><h1>Source</h1><noscript>HTML view</noscript><script>document.title='Rendered heading';fetch('/effect')</script></body></html>`)
		default:
			t.Errorf("preview requested seed path instead of homepage: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	for _, js := range []bool{false, true} {
		c := types.CrawlConfig{SeedURL: srv.URL + "/category?q=1", Mode: types.ModeSpider, JSRendering: js, RespectRobots: true, SkipSSRF: true,
			UserAgent: "PreviewBot", Cookies: "session=test", CustomHeaders: map[string]string{"X-Preview": "yes"}}
		frame, err := PreviewHomepage(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		want, mode := "Source heading", "html"
		if js {
			want, mode = "Rendered heading", "javascript"
		}
		if frame.Title != want || frame.Mode != mode || frame.StatusCode != 200 || frame.URL != srv.URL+"/" || len(frame.JPEG) == 0 {
			t.Fatalf("wrong preview: title=%q mode=%q status=%d url=%q", frame.Title, frame.Mode, frame.StatusCode, frame.URL)
		}
		if !js && effects.Load() != 0 {
			t.Fatal("HTML preview executed JS")
		}
	}
	responseStatus.Store(403)
	frame, err := PreviewHomepage(context.Background(), types.CrawlConfig{SeedURL: srv.URL, JSRendering: true, SkipSSRF: true, UserAgent: "PreviewBot", Cookies: "session=test", CustomHeaders: map[string]string{"X-Preview": "yes"}})
	if err != nil || frame.StatusCode != 403 || frame.Mode != "html" || frame.Title != "Source heading" {
		t.Fatalf("error response did not match crawler behavior: %v %+v", err, frame)
	}
	if pages.Load() != 3 || effects.Load() != 1 || wrongCredentials.Load() != 0 {
		t.Fatalf("wrong requests or credential leakage: pages=%d effects=%d credential errors=%d", pages.Load(), effects.Load(), wrongCredentials.Load())
	}
}

func TestPreviewHomepageRespectsRobotsAndPrivateNetwork(t *testing.T) {
	var pages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
			return
		}
		pages.Add(1)
	}))
	defer srv.Close()
	_, err := PreviewHomepage(context.Background(), types.CrawlConfig{SeedURL: srv.URL, RespectRobots: true, SkipSSRF: true})
	if err == nil || !strings.Contains(err.Error(), "robots.txt disallows") || pages.Load() != 0 {
		t.Fatalf("robots bypassed: %v hits=%d", err, pages.Load())
	}
	_, err = PreviewHomepage(context.Background(), types.CrawlConfig{SeedURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "private/loopback") || pages.Load() != 0 {
		t.Fatalf("SSRF bypassed: %v hits=%d", err, pages.Load())
	}
}
