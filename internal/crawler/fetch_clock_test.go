package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Time spent waiting for the rate limiter is not response time: a busy crawl
// must not make a fast site look slow (the adaptive limiter reads it).
func TestResponseTimeLeavesOutPacingWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		if r.URL.Path == "/slow" {
			time.Sleep(200 * time.Millisecond)
		}
		fmt.Fprint(w, `<html><head><title>x</title></head></html>`)
	}))
	defer srv.Close()
	pace := func(ctx context.Context, _ string) func() {
		time.Sleep(300 * time.Millisecond) // the host's turn comes late
		return func() {}
	}
	opts := FetchOptions{SkipSSRF: true, Pace: pace, NoRetry: true}

	// Two paced hops (redirect + target): 600 ms of waiting, ~0 ms of server.
	if r := FetchPage(context.Background(), srv.URL+"/old", opts); r.StatusCode != 200 || r.ResponseTimeMs >= 150 {
		t.Errorf("/old: status %d, response time %d ms, want 200 and < 150 ms", r.StatusCode, r.ResponseTimeMs)
	}
	// A slow server still shows as slow.
	if r := FetchPage(context.Background(), srv.URL+"/slow", opts); r.ResponseTimeMs < 200 || r.ResponseTimeMs >= 400 {
		t.Errorf("/slow: response time %d ms, want 200-400", r.ResponseTimeMs)
	}
	if r := FetchHead(context.Background(), srv.URL+"/old", opts); r.ResponseTimeMs >= 150 {
		t.Errorf("HEAD /old: response time %d ms, want < 150", r.ResponseTimeMs)
	}
}
