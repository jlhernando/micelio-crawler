package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// limitedHandler returns the auth-limit middleware around a handler that
// checks the bearer token, with a clock the test moves.
func limitedHandler(tok string) (http.Handler, *time.Time, *[]string) {
	now := time.Unix(1_000_000, 0)
	var logs []string
	var mu sync.Mutex
	l := newAuthLimiter()
	l.now = func() time.Time { return now }
	l.logf = func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() }
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bearerAuthorized(r, tok) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return withAuthLimit(tok, l, inner), &now, &logs
}

func bearerReq(h http.Handler, ip, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/crawls", nil)
	r.RemoteAddr = ip + ":40000"
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Wrong tokens are throttled per address with a growing wait, a blocked
// address gets 429 even with the right token (no oracle), and the block
// ends: the user is never locked out for good.
func TestAuthLimitThrottlesWrongTokens(t *testing.T) {
	const tok = "s3cret-token-0123456789"
	h, now, logs := limitedHandler(tok)
	const ip = "203.0.113.7"

	for i := 0; i < authFreeFails; i++ {
		if w := bearerReq(h, ip, "guess"); w.Code != http.StatusUnauthorized {
			t.Fatalf("free attempt %d: status %d, want 401", i+1, w.Code)
		}
	}
	// The next wrong token is still answered, then the address waits 1 s.
	if w := bearerReq(h, ip, "guess"); w.Code != http.StatusUnauthorized {
		t.Fatalf("attempt %d: status %d", authFreeFails+1, w.Code)
	}
	w := bearerReq(h, ip, tok)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("blocked address: status %d Retry-After %q, want 429 and 1", w.Code, w.Header().Get("Retry-After"))
	}
	// Another address is not affected.
	if w := bearerReq(h, "198.51.100.1", tok); w.Code != http.StatusOK {
		t.Fatalf("other address: status %d", w.Code)
	}
	// Each further wrong token doubles the wait.
	*now = now.Add(time.Second)
	bearerReq(h, ip, "guess")
	if w := bearerReq(h, ip, "guess"); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("second block: status %d Retry-After %q, want 429 and 2", w.Code, w.Header().Get("Retry-After"))
	}
	// Capped: never more than authMaxBackoff.
	for i := 0; i < 30; i++ {
		*now = now.Add(authMaxBackoff)
		bearerReq(h, ip, "guess")
	}
	w = bearerReq(h, ip, tok)
	if ra := w.Header().Get("Retry-After"); w.Code != http.StatusTooManyRequests || ra != fmt.Sprint(int(authMaxBackoff/time.Second)) {
		t.Fatalf("capped block: status %d Retry-After %q", w.Code, ra)
	}
	// After the block, the right token works and clears the count.
	*now = now.Add(authMaxBackoff)
	if w := bearerReq(h, ip, tok); w.Code != http.StatusOK {
		t.Fatalf("after the block: status %d, want 200", w.Code)
	}
	if w := bearerReq(h, ip, "guess"); w.Code != http.StatusUnauthorized {
		t.Fatalf("count not cleared: status %d", w.Code)
	}

	if len(*logs) == 0 || !strings.Contains((*logs)[0], ip) {
		t.Fatalf("throttling not logged: %v", *logs)
	}
	for _, l := range *logs {
		if strings.Contains(l, "guess") || strings.Contains(l, tok) {
			t.Fatalf("log line holds a token: %q", l)
		}
	}
}

// The browser login paths count too: ?token= visits and POST /login.
func TestAuthLimitLoginPaths(t *testing.T) {
	const tok = "s3cret-token-0123456789"
	h, _ := guardServer(ServerOptions{Host: "0.0.0.0", Port: 3100, AuthToken: tok}, true)
	send := func(r *http.Request) *httptest.ResponseRecorder {
		r.RemoteAddr = "203.0.113.9:5555"
		r.Host = "dash.example.com:3100"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < authFreeFails+1; i++ {
		if i%2 == 0 {
			send(httptest.NewRequest("GET", "/?token=wrong", nil))
		} else {
			r := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"token": {"wrong"}}.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			send(r)
		}
	}
	r := httptest.NewRequest("POST", "/login", strings.NewReader("token="+tok))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if w := send(r); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("login while blocked: status %d, want 429 with Retry-After", w.Code)
	}
	if w := send(httptest.NewRequest("GET", "/?token="+tok, nil)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("?token= while blocked: status %d, want 429", w.Code)
	}
	// A browser that already has the login cookie keeps working.
	r = httptest.NewRequest("GET", "/api/crawls", nil)
	r.AddCookie(&http.Cookie{Name: "micelio_session_3100", Value: sessionValue(tok)})
	if w := send(r); w.Code != http.StatusOK {
		t.Fatalf("cookie while blocked: status %d, want 200", w.Code)
	}
}

func TestClientKeyGroupsIPv6(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[2001:db8:1:2:aaaa::1]:443"
	a := clientKey(r)
	r.RemoteAddr = "[2001:db8:1:2:bbbb::9]:443"
	if b := clientKey(r); a != b || a != "2001:db8:1:2::/64" {
		t.Fatalf("clientKey = %q, %q; want one /64", a, b)
	}
}
