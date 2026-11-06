package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// sessionCookieName names the browser login cookie. It is set by the login
// screen (POST /login, see withLogin) and sent back on every same-origin
// fetch and WebSocket, so the Svelte app needs no token handling. Cookies
// ignore ports, so the port is in the name: two dashboards on one host keep
// separate logins.
func sessionCookieName(port int) string {
	if port <= 0 {
		return "micelio_session"
	}
	return "micelio_session_" + strconv.Itoa(port)
}

// sessionValue is what the login cookie stores: a value derived from the
// token, so the cookie never carries the long-lived token itself.
func sessionValue(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("micelio-session"))
	return hex.EncodeToString(mac.Sum(nil))
}

// NormalizeAllowedHost reduces an --allowed-host value ("example.com",
// "example.com:8443", "https://example.com/") to lowercase
// "[scheme://]host[:port]", keeping the scheme and port it names. It
// returns "" for an empty value.
func NormalizeAllowedHost(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	scheme := ""
	if i := strings.Index(v, "://"); i >= 0 {
		scheme, v = v[:i], v[i+3:]
	}
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	name, port := hostName(v), ""
	if _, p, err := net.SplitHostPort(v); err == nil {
		port = p
	}
	if name == "" {
		return ""
	}
	out := name
	if port != "" {
		out = net.JoinHostPort(name, port)
	}
	if scheme == "http" || scheme == "https" {
		out = scheme + "://" + out
	}
	return out
}

// allowedEntry is a parsed NormalizeAllowedHost value.
type allowedEntry struct{ scheme, name, port string }

func parseAllowed(a string) allowedEntry {
	var e allowedEntry
	if i := strings.Index(a, "://"); i >= 0 {
		e.scheme, a = a[:i], a[i+3:]
	}
	e.name = hostName(a)
	if _, p, err := net.SplitHostPort(a); err == nil {
		e.port = p
	}
	return e
}

func defaultPort(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	}
	return ""
}

// originListed reports whether a browser origin is one of the
// --allowed-host entries. Browsers isolate origins by port but cookies do
// not, so the port must match too: the one the entry names, or the
// scheme's default port for an entry without one. Another service on the
// same host name (https://example.com:8443) is a different origin.
func originListed(u *url.URL, allowed []string) bool {
	name := hostName(u.Host)
	port := u.Port()
	if port == "" {
		port = defaultPort(u.Scheme)
	}
	for _, a := range allowed {
		e := parseAllowed(a)
		if e.name != name || (e.scheme != "" && e.scheme != u.Scheme) {
			continue
		}
		want := e.port
		if want == "" {
			want = defaultPort(u.Scheme)
		}
		if port == want {
			return true
		}
	}
	return false
}

// hostName strips the port, IPv6 brackets and a trailing dot from a Host
// header and lowercases it.
func hostName(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
}

// hostListed reports whether the name of host (a Host header, with or
// without port) is one of the --allowed-host names. Ports are ignored
// here: this is the DNS rebinding check, which is about the name.
func hostListed(host string, allowed []string) bool {
	name := hostName(host)
	if name == "" {
		return false
	}
	for _, a := range allowed {
		if parseAllowed(a).name == name {
			return true
		}
	}
	return false
}

// IsLoopbackHost reports whether host (a bind address or a Host header,
// with or without port) is the local machine: localhost, *.localhost or a
// loopback IP.
func IsLoopbackHost(host string) bool {
	lower := hostName(host)
	if lower == "" {
		return false
	}
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true
	}
	ip := net.ParseIP(lower)
	return ip != nil && ip.IsLoopback()
}

// originAllowed reports whether a browser Origin may call the API: the
// dashboard's own origin (same host and port as the request, as the
// X-Forwarded-Host a reverse proxy set, or one of the --allowed-host
// entries, port included). Other ports of this machine are other sites: a
// local app on another port must not drive the dashboard. The Vite dev
// server keeps the browser's Host (changeOrigin: false in
// dashboard/vite.config.ts), so it passes as the same origin. "null" and
// unparsable origins fail.
func originAllowed(r *http.Request, origin string, allowed []string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	if originListed(u, allowed) {
		return true
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		first := strings.TrimSpace(strings.Split(fwd, ",")[0])
		return strings.EqualFold(u.Host, first)
	}
	return false
}

// isSafeMethod reports methods that must not change state.
func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// guardError writes a JSON error with a hint on how to fix it.
func guardError(w http.ResponseWriter, status int, msg string) {
	body, _ := json.Marshal(map[string]string{"error": msg})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	w.Write(body)
}

const allowHostHint = "start micelio ui with --allowed-host %s (or MICELIO_ALLOWED_HOSTS=%s) to allow it"

// withGuard blocks requests a hostile web page can make through the user's
// browser:
//   - DNS rebinding: when the dashboard listens on loopback without a token,
//     the Host header must name this machine or an --allowed-host (an
//     attacker domain that resolves to 127.0.0.1 is refused).
//   - Cross-origin requests: an Origin that is not the dashboard's own
//     (scheme, host and port; see originAllowed) is refused, also from
//     another port of the same host, which browsers call same-site and
//     send the login cookie from. Sec-Fetch-Site: cross-site is refused,
//     except for plain navigations (a link to the dashboard).
//
// Requests without Origin and Sec-Fetch-Site (CLI, MCP, curl) pass, and so
// do requests with a valid Authorization: Bearer token: a web page cannot
// attach that header without knowing the token.
func withGuard(checkHost bool, token string, allowed []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if checkHost && !IsLoopbackHost(r.Host) && !hostListed(r.Host, allowed) {
			name := hostName(r.Host)
			guardError(w, http.StatusMisdirectedRequest, "Host header "+strconv.Quote(name)+
				" is not this computer; if a local reverse proxy or tunnel forwards it, "+
				fmt.Sprintf(allowHostHint, name, name))
			return
		}
		if token != "" && bearerAuthorized(r, token) {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(r, origin, allowed) {
			name := origin
			if u, err := url.Parse(origin); err == nil && u.Host != "" {
				name = NormalizeAllowedHost(u.Host)
				if u.Port() != "" {
					name = u.Scheme + "://" + name // that exact origin
				}
			}
			guardError(w, http.StatusForbidden, "cross-origin request from "+strconv.Quote(origin)+
				" refused; if this is the dashboard behind a reverse proxy, make the proxy send the "+
				"public Host or X-Forwarded-Host header, or "+fmt.Sprintf(allowHostHint, name, name))
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			navigation := isSafeMethod(r.Method) && r.Header.Get("Sec-Fetch-Mode") == "navigate"
			if !navigation {
				guardError(w, http.StatusForbidden, "cross-site request refused")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// tokenEqual compares a presented token with the configured one in
// constant time.
func tokenEqual(got, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// bearerAuthorized reports whether r carries the token in an
// Authorization: Bearer header (CLI, MCP).
func bearerAuthorized(r *http.Request, token string) bool {
	auth := r.Header.Get("Authorization")
	return strings.HasPrefix(auth, "Bearer ") && tokenEqual(strings.TrimPrefix(auth, "Bearer "), token)
}

// authorized reports whether r carries the dashboard token: an
// Authorization: Bearer header (CLI, MCP) or the login cookie (browser),
// which holds sessionValue(token).
func authorized(r *http.Request, token, cookie string) bool {
	if bearerAuthorized(r, token) {
		return true
	}
	if c, err := r.Cookie(cookie); err == nil && token != "" && tokenEqual(c.Value, sessionValue(token)) {
		return true
	}
	return false
}

// withLogin logs a browser in: it sets the login cookie, which holds
// sessionValue(token), never the token itself.
//
//   - POST /login with a form field token (the dashboard's login screen)
//     answers 204 with the cookie, or 401 for a wrong token. The token
//     travels in the request body, so it never lands in a URL, the browser
//     history or a proxy's access log.
//   - A visit with ?token=<token> (a link the user typed or bookmarked)
//     still works: it sets the cookie and redirects to the same page
//     without the token, with Referrer-Policy: no-referrer and no caching,
//     so the URL with the token is not passed on. A wrong token sends a
//     browser back to the page with ?login=failed (no token), where the
//     login screen says so; other clients get a 401.
//
// /api/ and /ws never take the token from the URL: only the Authorization
// header or the cookie.
func withLogin(token, cookie string, next http.Handler) http.Handler {
	setCookie := func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     cookie,
			Value:    sessionValue(token),
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
			SameSite: http.SameSiteStrictMode,
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isLoginPost(r) {
			w.Header().Set("Cache-Control", "no-store")
			r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
			if !tokenEqual(r.PostFormValue("token"), token) {
				guardError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			setCookie(w, r)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		q := r.URL.Query()
		if r.Method == http.MethodGet && q.Has("token") && r.URL.Path != "/ws" &&
			!strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Cache-Control", "no-store")
			if !tokenEqual(q.Get("token"), token) {
				if !isPageNavigation(r) {
					http.Error(w, "invalid token", http.StatusUnauthorized)
					return
				}
				q.Del("token")
				q.Set("login", "failed")
				dest := *r.URL
				dest.RawQuery = q.Encode()
				http.Redirect(w, r, dest.RequestURI(), http.StatusSeeOther)
				return
			}
			q.Del("login")
			setCookie(w, r)
			q.Del("token")
			dest := *r.URL
			dest.RawQuery = q.Encode()
			http.Redirect(w, r, dest.RequestURI(), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isPageNavigation reports whether a request is a browser loading a page
// (address bar, link, form), not a script or a command-line client.
func isPageNavigation(r *http.Request) bool {
	if mode := r.Header.Get("Sec-Fetch-Mode"); mode != "" {
		return mode == "navigate"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
