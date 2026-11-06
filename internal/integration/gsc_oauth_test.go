package integration

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func callback(h http.Handler, query string) int {
	r := httptest.NewRequest("GET", "/oauth2callback?"+query, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

// A page in the browser can reach the loopback callback, but without the
// state it can neither end the flow with an error nor feed it a code.
func TestOAuthCallbackChecksStateFirst(t *testing.T) {
	const state = "random-state-0123456789"
	done := make(chan oauthResult, 1)
	h := oauthCallbackHandler(state, done)

	for _, q := range []string{"error=access_denied", "error=access_denied&state=wrong", "code=evil", "code=evil&state=wrong"} {
		if code := callback(h, q); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, code)
		}
	}
	select {
	case res := <-done:
		t.Fatalf("a forged callback ended the flow: %+v", res)
	default:
	}

	if code := callback(h, "code=good&state="+state); code != http.StatusOK {
		t.Fatalf("valid callback: status %d", code)
	}
	if res := <-done; res.code != "good" || res.err != nil {
		t.Fatalf("result %+v", res)
	}
}

// Many valid-looking callbacks at once never block a handler: only the
// first one counts, the rest are refused.
func TestOAuthCallbackNeverBlocks(t *testing.T) {
	const state = "random-state-0123456789"
	done := make(chan oauthResult, 1)
	h := oauthCallbackHandler(state, done)

	var wg sync.WaitGroup
	codes := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := "code=c&state=" + state
			if i%2 == 0 {
				q = "error=x&state=" + state
			}
			codes <- callback(h, q)
		}(i)
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("callback handlers blocked")
	}
	close(codes)
	ok, conflict := 0, 0
	for c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		}
	}
	if ok != 1 || conflict != 19 {
		t.Fatalf("ok=%d conflict=%d, want 1 and 19", ok, conflict)
	}
	if len(done) != 1 {
		t.Fatalf("results delivered: %d, want 1", len(done))
	}
}
