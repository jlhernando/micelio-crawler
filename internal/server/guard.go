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

// sessionCookieName names the browser login cookie. It is set by opening
// the dashboard once with ?token=<token> and sent back on every same-origin
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
// "example.com:443", "https://example.com/") to a lowercase host name.
// It returns "" for an empty value.
func NormalizeAllowedHost(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, "://"); i >= 0 {
		v = v[i+3:]
	}
	if i := strings.IndexAny(v, "/?#"); i >= 0 {
		v = v[:i]
	}
	return hostName(v)
}

// hostName strips the port, IPv6 brackets and a trailing dot from a Host
// header and lowercases it.
func hostName(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
}

// hostListed reports whether host (with or without port) is one of the
// --allowed-host names.
func hostListed(host string, allowed []string) bool {
	name := hostName(host)
	if name == "" {
		return false
	}
	for _, a := range allowed {
		if a == name {
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

// originAllowed reports whether a browser Origin may call the API: a page
// served from this machine (any port, which covers the Vite dev server), or
// the dashboard's own origin (same host as the request, as the
// X-Forwarded-Host a reverse proxy set, or one of the --allowed-host names).
// "null" and unparsable origins fail.
func originAllowed(r *http.Request, origin string, allowed []string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	if IsLoopbackHost(u.Host) || hostListed(u.Host, allowed) {
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
//   - Cross-site requests: an Origin that is neither local nor the
//     dashboard's own is refused, and so is Sec-Fetch-Site: cross-site,
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
				name = hostName(u.Host)
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

// withLogin turns a dashboard visit with ?token=<token> into the login
// cookie and redirects to the same page without the token, so it does not
// stay in the address bar or history. A wrong token sends a browser back
// to the page with ?login=failed (no token), where the login screen says
// so; other clients get a 401.
func withLogin(token, cookie string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method == http.MethodGet && q.Has("token") && r.URL.Path != "/ws" &&
			!strings.HasPrefix(r.URL.Path, "/api/") {
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
			http.SetCookie(w, &http.Cookie{
				Name:     cookie,
				Value:    sessionValue(token),
				Path:     "/",
				HttpOnly: true,
				Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
				SameSite: http.SameSiteStrictMode,
			})
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
