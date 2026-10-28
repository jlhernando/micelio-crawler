package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// A failing URL (5xx) is fetched 1 + Retries times; a 404 is never retried;
// failures never stop the crawl.
func TestRetriesPerURL(t *testing.T) {
	for _, retries := range []int{0, 2} {
		t.Run(fmt.Sprintf("retries=%d", retries), func(t *testing.T) {
			var serverErr, notFound atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/broken":
					serverErr.Add(1)
					w.WriteHeader(http.StatusInternalServerError)
					return
				case "/missing":
					notFound.Add(1)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<html><body><a href="/broken">b</a><a href="/missing">m</a><a href="/ok">o</a></body></html>`)
			}))
			defer srv.Close()

			n := retries
			res, err := Crawl(context.Background(), types.CrawlConfig{
				SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 2, MaxPages: 20,
				Concurrency: 2, UserAgent: "Micelio/1.0", SkipSSRF: true, Retries: &n,
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := int(serverErr.Load()); got != retries+1 {
				t.Errorf("/broken fetched %d times, want %d", got, retries+1)
			}
			if got := notFound.Load(); got != 1 {
				t.Errorf("/missing fetched %d times, want 1 (404 is not retried)", got)
			}
			if res.StopReason != "" {
				t.Errorf("failures stopped the crawl: %q", res.StopReason)
			}
			if len(res.Pages) != 4 {
				t.Errorf("stored %d pages, want 4 (failed pages are kept)", len(res.Pages))
			}
		})
	}
}

func TestRetryableResult(t *testing.T) {
	for status, want := range map[int]bool{200: false, 403: false, 404: false, 408: true, 429: true, 500: true, 502: true, 503: true} {
		if got := isRetryableResult(types.FetchResult{StatusCode: status}); got != want {
			t.Errorf("%d: retryable = %v, want %v", status, got, want)
		}
	}
	if !isRetryableResult(types.FetchResult{Error: "timeout", Retryable: true}) {
		t.Error("retryable network error not retried")
	}
	if isRetryableResult(types.FetchResult{Error: "invalid URL"}) {
		t.Error("permanent network error retried")
	}
}

// A crawl that hits its time limit reports why.
func TestTimeLimitStopReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html")
		// Endless site: every page links to two new ones.
		fmt.Fprintf(w, `<html><body><a href="%s1">a</a><a href="%s2">b</a></body></html>`, r.URL.Path, r.URL.Path)
	}))
	defer srv.Close()
	res, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL + "/p", Mode: types.ModeSpider, MaxDepth: 50, MaxPages: 10000,
		Concurrency: 2, UserAgent: "Micelio/1.0", SkipSSRF: true, TimeoutSeconds: 1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.StopReason, "time limit reached") {
		t.Errorf("StopReason = %q", res.StopReason)
	}
}
