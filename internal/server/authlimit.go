package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Wrong dashboard tokens are throttled per client address, so a dashboard
// reachable from the network cannot be brute-forced at request speed. The
// first authFreeFails wrong tokens cost nothing (typos); every further one
// blocks token attempts from that address for twice as long as the last,
// from 1 s up to authMaxBackoff. A right token clears the count. The block
// never lasts longer than authMaxBackoff, so the user is never locked out
// for good, and a browser that is already logged in (session cookie) is
// never blocked: the cookie holds a 256-bit HMAC, not something to guess.
const (
	authFreeFails  = 5
	authMaxBackoff = 5 * time.Minute
	authForget     = 30 * time.Minute // a quiet address starts again from zero
	authMaxClients = 10000            // beyond this, unknown addresses share one bucket
)

type authStrikes struct {
	fails int
	until time.Time // token attempts refused until then
	last  time.Time // last wrong token
}

type authLimiter struct {
	mu      sync.Mutex
	clients map[string]*authStrikes
	now     func() time.Time
	logf    func(format string, args ...any)
}

func newAuthLimiter() *authLimiter {
	return &authLimiter{clients: map[string]*authStrikes{}, now: time.Now, logf: log.Printf}
}

// clientKey is the address token attempts are counted for: the remote IP,
// or its /64 for IPv6 (one host usually owns a whole /64). Behind a reverse
// proxy every client shares the proxy's address, which then acts as one
// global limit.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if ip.To4() == nil {
		return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
	}
	return ip.String()
}

// blocked returns how long token attempts from key are still refused.
func (l *authLimiter) blocked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.clients[l.bucket(key)]
	if s == nil {
		return 0
	}
	return s.until.Sub(l.now())
}

// bucket returns key, or a shared overflow bucket once too many addresses
// are tracked (so spraying from many addresses still meets a limit).
func (l *authLimiter) bucket(key string) string {
	if _, ok := l.clients[key]; ok || len(l.clients) < authMaxClients {
		return key
	}
	return "overflow"
}

// fail records a wrong token from key.
func (l *authLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.clients) >= authMaxClients {
		l.prune(now)
	}
	key = l.bucket(key)
	s := l.clients[key]
	if s == nil || now.Sub(s.last) > authForget {
		s = &authStrikes{}
		l.clients[key] = s
	}
	s.fails++
	s.last = now
	if s.fails <= authFreeFails {
		return
	}
	wait := authMaxBackoff
	if n := s.fails - authFreeFails - 1; n < 20 {
		wait = min(time.Second<<n, authMaxBackoff)
	}
	s.until = now.Add(wait)
	l.logf("auth: %d wrong tokens from %s; refusing its token attempts for %s", s.fails, key, wait)
}

// succeed clears the count of key after a right token.
func (l *authLimiter) succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.clients, key)
}

// prune forgets addresses that are neither blocked nor recent.
func (l *authLimiter) prune(now time.Time) {
	for k, s := range l.clients {
		if now.After(s.until) && now.Sub(s.last) > authForget {
			delete(l.clients, k)
		}
	}
}

// presentedToken returns the dashboard token a request offers, if any: an
// Authorization: Bearer header, the ?token= of a browser login visit, or
// the token field of a POST /login form. A session cookie is not a token
// attempt.
func presentedToken(r *http.Request) (string, bool) {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer "), true
	}
	if isLoginPost(r) {
		r.Body = http.MaxBytesReader(nil, r.Body, 4<<10)
		if err := r.ParseForm(); err != nil {
			return "", true // unreadable login form: counts as a wrong token
		}
		return r.PostForm.Get("token"), true
	}
	if r.Method == http.MethodGet && r.URL.Path != "/ws" && !strings.HasPrefix(r.URL.Path, "/api/") {
		if q := r.URL.Query(); q.Has("token") {
			return q.Get("token"), true
		}
	}
	return "", false
}

// isLoginPost reports whether r is the dashboard's login form.
func isLoginPost(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == "/login"
}

// withAuthLimit counts the token attempts of each client address and
// answers 429 with Retry-After while an address is blocked. It runs before
// every other check, so a blocked address learns nothing about its guess.
func withAuthLimit(token string, l *authLimiter, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := presentedToken(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		key := clientKey(r)
		if wait := l.blocked(key); wait > 0 {
			secs := int((wait + time.Second - 1) / time.Second)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			w.Header().Set("Cache-Control", "no-store")
			guardError(w, http.StatusTooManyRequests,
				fmt.Sprintf("too many wrong tokens from your address; try again in %d s", secs))
			return
		}
		if tokenEqual(got, token) {
			l.succeed(key)
		} else {
			l.fail(key)
		}
		next.ServeHTTP(w, r)
	})
}
