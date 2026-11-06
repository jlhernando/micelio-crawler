package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The login screen posts the token: it never travels in a URL. A visit
// with ?token= still logs in but tells the browser not to pass the URL on,
// and /ws no longer takes the token from the URL.
func TestLoginPostAndTokenNotInURL(t *testing.T) {
	const tok = "s3cret-token-0123456789"
	h, _ := guardServer(ServerOptions{Host: "0.0.0.0", Port: 3100, AuthToken: tok}, true)
	post := func(token string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"token": {token}}.Encode()))
		r.Host = "dash.example.com:3100"
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := post(tok, map[string]string{"Origin": "http://dash.example.com:3100", "Sec-Fetch-Site": "same-origin"})
	var c *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "micelio_session_3100" {
			c = ck
		}
	}
	if w.Code != http.StatusNoContent || c == nil || c.Value != sessionValue(tok) || !c.HttpOnly {
		t.Fatalf("POST /login: status %d, cookie %+v", w.Code, c)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("POST /login Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	if w := post("wrong", nil); w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Fatalf("POST /login wrong token: status %d, cookies %v", w.Code, w.Result().Cookies())
	}
	// Another site cannot log the browser in (or probe tokens through it).
	if w := post(tok, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); w.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST /login: status %d, want 403", w.Code)
	}

	r := httptest.NewRequest("GET", "/?token="+tok, nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("?token= login: status %d, Referrer-Policy %q, Cache-Control %q", w.Code,
			w.Header().Get("Referrer-Policy"), w.Header().Get("Cache-Control"))
	}

	for _, target := range []string{"/ws?token=" + tok, "/api/crawls?token=" + tok} {
		r = httptest.NewRequest("GET", target, nil)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401 (token in URL not accepted)", strings.SplitN(target, "?", 2)[0], w.Code)
		}
	}
}
