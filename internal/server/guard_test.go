package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.1:3100", true},
		{"127.1.2.3", true},
		{"localhost", true},
		{"LOCALHOST:3100", true},
		{"app.localhost:5175", true},
		{"::1", true},
		{"[::1]:3100", true},
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"192.168.1.5", false},
		{"evil.com", false},
		{"localhost.evil.com:3100", false},
		{"127.0.0.1.nip.io", false},
	}
	for _, tc := range tests {
		if got := IsLoopbackHost(tc.host); got != tc.want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestCheckExposure(t *testing.T) {
	tests := []struct {
		name    string
		opts    ServerOptions
		wantErr bool
	}{
		{"loopback no token", ServerOptions{Host: "127.0.0.1"}, false},
		{"localhost no token", ServerOptions{Host: "localhost"}, false},
		{"ipv6 loopback no token", ServerOptions{Host: "::1"}, false},
		{"all interfaces no token", ServerOptions{Host: "0.0.0.0"}, true},
		{"lan ip no token", ServerOptions{Host: "192.168.1.5"}, true},
		{"all interfaces with token", ServerOptions{Host: "0.0.0.0", AuthToken: "s3cret-token-0123456789"}, false},
		{"all interfaces opt-out", ServerOptions{Host: "0.0.0.0", InsecureNoAuth: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkExposure(tc.opts)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkExposure(%+v) = %v, wantErr %v", tc.opts, err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrNoAuthToken) {
				t.Fatalf("want ErrNoAuthToken, got %v", err)
			}
		})
	}
}

// guardServer builds the real handler stack around a stub API that records
// whether it ran.
func guardServer(opts ServerOptions, withDashboard bool) (http.Handler, *bool) {
	called := new(bool)
	srv := &Server{
		api: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*called = true
			w.WriteHeader(http.StatusOK)
		}),
		clients: make(map[*wsClient]struct{}),
	}
	var dash fstest.MapFS
	if withDashboard {
		dash = fstest.MapFS{"index.html": {Data: []byte("<html>dashboard</html>")}}
	}
	if dash == nil {
		return srv.buildHandler(opts, nil), called
	}
	return srv.buildHandler(opts, dash), called
}

type guardCase struct {
	name       string
	method     string
	target     string
	host       string
	headers    map[string]string
	wantStatus int
	wantAPI    bool
}

func runGuardCases(t *testing.T, opts ServerOptions, dashboard bool, cases []guardCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, called := guardServer(opts, dashboard)
			var body *strings.Reader
			if tc.method == http.MethodPost {
				body = strings.NewReader(`{"url":"https://example.com"}`)
			} else {
				body = strings.NewReader("")
			}
			r := httptest.NewRequest(tc.method, tc.target, body)
			r.Host = tc.host
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", w.Code, tc.wantStatus, w.Body.String())
			}
			if *called != tc.wantAPI {
				t.Fatalf("API handler called = %v, want %v", *called, tc.wantAPI)
			}
		})
	}
}

// TestGuardLoopbackNoToken covers the default "micelio ui": a hostile web
// page must not reach the API through the user's browser.
func TestGuardLoopbackNoToken(t *testing.T) {
	opts := ServerOptions{Host: "127.0.0.1"}
	const local = "127.0.0.1:3100"
	crossSite := map[string]string{"Origin": "https://evil.com", "Sec-Fetch-Site": "cross-site", "Content-Type": "text/plain"}
	runGuardCases(t, opts, true, []guardCase{
		{"cli post without origin", "POST", "/api/crawl/start", local, nil, 200, true},
		{"cli get", "GET", "/api/crawls", local, nil, 200, true},
		{"dashboard same origin post", "POST", "/api/crawl/start", local,
			map[string]string{"Origin": "http://127.0.0.1:3100", "Sec-Fetch-Site": "same-origin"}, 200, true},
		{"localhost dashboard post", "POST", "/api/crawl/start", "localhost:3100",
			map[string]string{"Origin": "http://localhost:3100", "Sec-Fetch-Site": "same-origin"}, 200, true},
		{"vite dev proxy post", "POST", "/api/crawl/start", "localhost:3101",
			map[string]string{"Origin": "http://127.0.0.1:5175", "Sec-Fetch-Site": "same-origin"}, 200, true},
		{"hostile crawl start", "POST", "/api/crawl/start", local, crossSite, 403, false},
		{"hostile schedule create", "POST", "/api/schedules", local, crossSite, 403, false},
		{"hostile update install", "POST", "/api/update/install", local,
			map[string]string{"Origin": "https://evil.com"}, 403, false},
		{"hostile update rollback", "POST", "/api/update/rollback", local,
			map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors"}, 403, false},
		{"null origin", "POST", "/api/crawl/start", local, map[string]string{"Origin": "null"}, 403, false},
		{"lookalike origin", "POST", "/api/crawl/start", local,
			map[string]string{"Origin": "http://localhost.evil.com:3100"}, 403, false},
		{"cross-site image get", "GET", "/api/crawls", local,
			map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors"}, 403, false},
		{"cross-site navigation to dashboard", "GET", "/", local,
			map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}, 200, false},
		{"dns rebinding api", "GET", "/api/crawls", "attacker.example:3100", nil, 421, false},
		{"dns rebinding post", "POST", "/api/crawl/start", "attacker.example:3100",
			map[string]string{"Origin": "http://attacker.example:3100"}, 421, false},
		{"pprof on loopback", "GET", "/debug/pprof/cmdline", local, nil, 200, false},
		{"pprof dns rebinding", "GET", "/debug/pprof/cmdline", "attacker.example:3100", nil, 421, false},
		{"websocket hostile origin", "GET", "/ws", local,
			map[string]string{"Origin": "https://evil.com", "Connection": "Upgrade", "Upgrade": "websocket"}, 403, false},
	})
}

// TestGuardWithToken covers "micelio ui --host 0.0.0.0 --auth-token ...".
func TestGuardWithToken(t *testing.T) {
	const tok = "s3cret-token-0123456789"
	opts := ServerOptions{Host: "0.0.0.0", Port: 3100, AuthToken: tok}
	const host = "dash.example.com:3100"
	cookie := "micelio_session_3100=" + sessionValue(tok)
	runGuardCases(t, opts, false, []guardCase{
		{"no token", "GET", "/api/crawls", host, nil, 401, false},
		{"wrong bearer", "GET", "/api/crawls", host, map[string]string{"Authorization": "Bearer nope"}, 401, false},
		{"bearer", "GET", "/api/crawls", host, map[string]string{"Authorization": "Bearer " + tok}, 200, true},
		{"cookie same origin post", "POST", "/api/crawl/start", host,
			map[string]string{"Cookie": cookie, "Origin": "http://dash.example.com:3100"}, 200, true},
		{"cookie behind proxy", "POST", "/api/crawl/start", "127.0.0.1:3100",
			map[string]string{"Cookie": cookie, "Origin": "https://micelio.example.com", "X-Forwarded-Host": "micelio.example.com"}, 200, true},
		{"cookie hostile origin", "POST", "/api/crawl/start", host,
			map[string]string{"Cookie": cookie, "Origin": "https://evil.com"}, 403, false},
		{"pprof off by default off loopback", "GET", "/debug/pprof/cmdline", host,
			map[string]string{"Authorization": "Bearer " + tok}, 404, false},
		{"websocket without token", "GET", "/ws", host, nil, 401, false},
		{"raw token in cookie", "GET", "/api/crawls", host,
			map[string]string{"Cookie": "micelio_session_3100=" + tok}, 401, false},
		{"session cookie of another port", "GET", "/api/crawls", host,
			map[string]string{"Cookie": "micelio_session_3200=" + sessionValue(tok)}, 401, false},
		{"old cookie name", "GET", "/api/crawls", host,
			map[string]string{"Cookie": "micelio_token=" + tok}, 401, false},
		{"default nginx proxy rewrites Host", "POST", "/api/crawl/start", "127.0.0.1:3100",
			map[string]string{"Cookie": cookie, "Origin": "https://micelio.example.com"}, 403, false},
		{"bearer skips origin check", "POST", "/api/crawl/start", host,
			map[string]string{"Authorization": "Bearer " + tok, "Origin": "https://tool.example.org", "Sec-Fetch-Site": "cross-site"}, 200, true},
		{"wrong bearer keeps origin check", "POST", "/api/crawl/start", host,
			map[string]string{"Authorization": "Bearer nope", "Origin": "https://evil.com"}, 403, false},
	})

	// --allowed-host lets a proxy that rewrites Host (default nginx
	// proxy_pass) serve the dashboard from its public name.
	px := opts
	px.AllowedHosts = []string{"https://Micelio.Example.com/"}
	runGuardCases(t, px, false, []guardCase{
		{"allowed host origin behind proxy", "POST", "/api/crawl/start", "127.0.0.1:3100",
			map[string]string{"Cookie": cookie, "Origin": "https://micelio.example.com"}, 200, true},
		{"allowed host websocket origin", "GET", "/ws", "127.0.0.1:3100",
			map[string]string{"Cookie": cookie, "Origin": "https://micelio.example.com:8443"}, 400, false},
		{"other origin still refused", "POST", "/api/crawl/start", "127.0.0.1:3100",
			map[string]string{"Cookie": cookie, "Origin": "https://evil.com"}, 403, false},
	})

	pp := opts
	pp.Pprof = true
	runGuardCases(t, pp, false, []guardCase{
		{"pprof without token", "GET", "/debug/pprof/cmdline", host, nil, 401, false},
		{"pprof with token", "GET", "/debug/pprof/cmdline", host, map[string]string{"Authorization": "Bearer " + tok}, 200, false},
	})

	// A loopback dashboard with a token also protects pprof.
	lo := ServerOptions{Host: "127.0.0.1", AuthToken: tok}
	runGuardCases(t, lo, false, []guardCase{
		{"loopback pprof without token", "GET", "/debug/pprof/cmdline", "127.0.0.1:3100", nil, 401, false},
	})
}

func TestLoginCookie(t *testing.T) {
	const tok = "s3cret-token-0123456789"
	h, _ := guardServer(ServerOptions{Host: "0.0.0.0", Port: 3100, AuthToken: tok}, true)

	r := httptest.NewRequest("GET", "/results?token="+tok+"&tab=links", nil)
	r.Host = "dash.example.com:3100"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/results?tab=links" {
		t.Fatalf("Location = %q, want the URL without the token", loc)
	}
	res := w.Result()
	var c *http.Cookie
	for _, ck := range res.Cookies() {
		if ck.Name == "micelio_session_3100" {
			c = ck
		}
	}
	if c == nil || c.Value != sessionValue(tok) || c.Value == tok || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("bad login cookie: %+v", c)
	}
	if strings.Contains(w.Header().Get("Set-Cookie"), tok) {
		t.Fatalf("login cookie carries the raw token: %q", w.Header().Get("Set-Cookie"))
	}

	// The cookie the login set opens the API.
	r = httptest.NewRequest("GET", "/api/crawls", nil)
	r.Host = "dash.example.com:3100"
	r.AddCookie(c)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("API with login cookie: status %d", w.Code)
	}

	r = httptest.NewRequest("GET", "/?token=wrong", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Fatalf("wrong token: status %d, cookies %v", w.Code, w.Result().Cookies())
	}

	// The dashboard page itself loads without a token (it then needs one
	// for /api/).
	r = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard page: status %d", w.Code)
	}
}

// TestGuardAllowedHost covers a tokenless loopback dashboard behind a local
// reverse proxy or tunnel that keeps the public Host (Caddy, cloudflared).
func TestGuardAllowedHost(t *testing.T) {
	plain := ServerOptions{Host: "127.0.0.1"}
	h, _ := guardServer(plain, false)
	r := httptest.NewRequest("GET", "/api/crawls", nil)
	r.Host = "micelio.example.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMisdirectedRequest || !strings.Contains(w.Body.String(), "--allowed-host micelio.example.com") ||
		!strings.Contains(w.Body.String(), "MICELIO_ALLOWED_HOSTS") {
		t.Fatalf("status %d body %q: want 421 with the --allowed-host hint", w.Code, w.Body.String())
	}

	opts := ServerOptions{Host: "127.0.0.1", AllowedHosts: []string{"micelio.example.com:443", " ", "Tunnel.Example.NET"}}
	runGuardCases(t, opts, false, []guardCase{
		{"allowed host get", "GET", "/api/crawls", "micelio.example.com", nil, 200, true},
		{"allowed host other case", "GET", "/api/crawls", "TUNNEL.example.net:8080", nil, 200, true},
		{"allowed host same origin post", "POST", "/api/crawl/start", "micelio.example.com",
			map[string]string{"Origin": "https://micelio.example.com", "Sec-Fetch-Site": "same-origin"}, 200, true},
		{"allowed host hostile origin", "POST", "/api/crawl/start", "micelio.example.com",
			map[string]string{"Origin": "https://evil.com"}, 403, false},
		{"unlisted host still refused", "GET", "/api/crawls", "attacker.example", nil, 421, false},
		{"suffix of allowed host refused", "GET", "/api/crawls", "evil.micelio.example.com", nil, 421, false},
	})

	// The 403 explains how to allow a proxy's public origin.
	h, _ = guardServer(ServerOptions{Host: "127.0.0.1"}, false)
	r = httptest.NewRequest("POST", "/api/crawl/start", strings.NewReader("{}"))
	r.Host = "127.0.0.1:3100"
	r.Header.Set("Origin", "https://micelio.example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "X-Forwarded-Host") ||
		!strings.Contains(w.Body.String(), "--allowed-host micelio.example.com") {
		t.Fatalf("status %d body %q: want 403 with the proxy hint", w.Code, w.Body.String())
	}
}

func TestNormalizeAllowedHost(t *testing.T) {
	tests := map[string]string{
		"example.com":               "example.com",
		" Example.COM ":             "example.com",
		"example.com:443":           "example.com",
		"https://example.com/":      "example.com",
		"http://example.com:8080/x": "example.com",
		"example.com.":              "example.com",
		"[::1]:3100":                "::1",
		"":                          "",
	}
	for in, want := range tests {
		if got := NormalizeAllowedHost(in); got != want {
			t.Errorf("NormalizeAllowedHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionCookieName(t *testing.T) {
	if got := sessionCookieName(3100); got != "micelio_session_3100" {
		t.Fatalf("got %q", got)
	}
	if got := sessionCookieName(0); got != "micelio_session" {
		t.Fatalf("got %q", got)
	}
	if sessionValue("a") == sessionValue("b") || len(sessionValue("a")) != 64 {
		t.Fatal("sessionValue must depend on the token and be hex SHA-256")
	}
}
