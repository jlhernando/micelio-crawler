package crawler

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/micelio/micelio/internal/analysis"
)

const (
	// assetInterval spaces size requests to a host other than the crawled
	// site (CDN, image host): at most 10 per second per host, measured from
	// the moment the previous request was sent.
	assetInterval = 100 * time.Millisecond
	// assetMaxInterval is the slowest pace a 429/503 can push an asset host
	// to (each one doubles its interval, once per pause).
	assetMaxInterval = 2 * time.Second
	// assetPerHost caps size requests in flight on such a host; bigSites
	// (wikimedia.org, github.com...) get bigSitePerHost (6).
	assetPerHost = 4
)

// resourceSizer returns the page-weight sizer: a HEAD per resource with the
// crawl client (proxy, Tor, SSRF guard).
//
// Politeness depends on the host:
//   - the crawled site (seed domain, allowed domains, their subdomains such
//     as static.example.com, and any host the crawl already sent requests
//     to) keeps the crawl's per-host limiter (delay, robots.txt
//     Crawl-delay, Retry-After pauses) and at most
//     analysis.DefaultPageWeightPerHost requests at once, as before;
//   - every other host (a CDN, an image host such as upload.wikimedia.org)
//     is not the site being crawled, so the crawl's delay does not apply: it
//     gets its own bucket (one request per assetInterval, so at most 10/s)
//     and at most assetPerHost requests at once (bigSitePerHost on large
//     platforms).
//
// A 429 or 503 pauses the host's bucket (Retry-After, else backoff) and is
// reported as Throttled, so the scheduler slows the host to one request at
// a time and asks again. Cookies and custom headers are only sent to the
// crawl's internal scope (seed host and allowed domains), never to other
// hosts: not to the site's subdomains, not to third-party hosts the crawl
// limiter knows (an iframe or script host a JS render fetched), not to
// CDNs.
func (s *crawlSession) resourceSizer() analysis.ResourceSizer {
	opts := s.fetchOpts
	opts.NoRetry = true
	opts.Referrer = ""
	opts.IfNoneMatch = ""
	opts.IfModifiedSince = ""
	thirdParty := opts
	thirdParty.Cookies = ""
	thirdParty.CustomHeaders = nil

	internal := map[string]bool{}
	if s.queue != nil {
		internal[strings.ToLower(s.queue.SeedDomain())] = true
	}
	for _, d := range s.config.AllowedDomains {
		internal[strings.ToLower(d)] = true
	}
	return newResourceSizer(opts, thirdParty, internal, s.hostRL)
}

// newResourceSizer builds the sizer described on resourceSizer. siteOpts
// (with credentials) are used for the internal hosts only, assetOpts for
// every other host; their Pace is replaced. hostRL may be nil (no crawl
// limiter).
func newResourceSizer(siteOpts, assetOpts FetchOptions, internal map[string]bool, hostRL *hostRateLimiter) analysis.ResourceSizer {
	assetRL := assetHosts.limiter()
	// Registrable domains (eTLD+1) of the internal hosts: their subdomains
	// are the site too, for pacing.
	siteDomains := map[string]bool{}
	for h := range internal {
		if d := registrableDomain(h); d != "" {
			siteDomains[d] = true
		}
	}
	// isInternal: the crawl's own scope, the only hosts that get cookies
	// and custom headers.
	isInternal := func(host string) bool {
		return internal[host] || internal[(&url.URL{Host: host}).Hostname()]
	}
	// isSite reports whether host (lower-case host[:port]) is paced as the
	// crawled site. Checked per request, not cached: the crawl limiter
	// learns hosts as the crawl runs (redirect targets, list-mode hosts).
	isSite := func(host string) bool {
		if isInternal(host) {
			return true
		}
		if d := registrableDomain(host); d != "" && siteDomains[d] {
			return true
		}
		return hostRL != nil && hostRL.knows(host)
	}
	limiterFor := func(rawURL string) *hostRateLimiter {
		if isSite(extractDomain(rawURL)) {
			return hostRL
		}
		return assetRL
	}
	// Every hop of a redirect is paced by the limiter of its own host.
	pace := func(ctx context.Context, rawURL string) func() {
		if rl := limiterFor(rawURL); rl != nil {
			return rl.pace(ctx, rawURL)
		}
		return noopSent
	}
	siteOpts.Pace = pace
	assetOpts.Pace = pace
	// 429/503 are handled here and by the scheduler, never retried inline.
	siteOpts.NoRetry = true
	assetOpts.NoRetry = true

	size := func(ctx context.Context, u string) analysis.SizeResult {
		host := extractDomain(u)
		o := assetOpts
		if isInternal(host) {
			o = siteOpts
		}
		if !isSite(host) {
			release, ok := assetHosts.acquire(ctx, host)
			if !ok {
				return analysis.SizeResult{} // cancelled
			}
			defer release()
		}
		r := FetchHead(ctx, u, o)
		if r.StatusCode == http.StatusTooManyRequests || r.StatusCode == http.StatusServiceUnavailable {
			pause, ok := RetryAfterDuration(r.Headers["retry-after"], MaxServerRetryAfter)
			if !ok {
				pause = 0 // the scheduler backs off (5 s doubling)
			}
			p := pause
			if p <= 0 {
				p = retryBackoff(0)
			}
			if isSite(host) {
				if hostRL != nil {
					hostRL.Pause(u, p)
				}
			} else {
				assetHosts.slowDown(u, p)
			}
			return analysis.SizeResult{Throttled: true, RetryAfter: pause}
		}
		if !isSite(host) {
			assetHosts.answered(u)
		}
		if r.Error != "" || r.StatusCode >= 400 || r.ContentLength == nil {
			return analysis.SizeResult{}
		}
		return analysis.SizeResult{Size: *r.ContentLength, OK: true}
	}
	perHost := func(host string) int {
		if isSite(host) {
			return analysis.DefaultPageWeightPerHost
		}
		return hostLimit(host, assetPerHost) // bigSites: bigSitePerHost (6)
	}
	return analysis.ResourceSizer{Size: size, PerHost: perHost}
}

// registrableDomain returns host's registrable domain (eTLD+1, e.g.
// example.co.uk for static.example.co.uk), or "" for IPs and hosts without
// one. host may carry a port.
func registrableDomain(host string) string {
	h := strings.ToLower((&url.URL{Host: host}).Hostname())
	if h == "" || net.ParseIP(h) != nil {
		return ""
	}
	d, err := publicsuffix.EffectiveTLDPlusOne(h)
	if err != nil {
		return ""
	}
	return d
}

// assetHosts holds the limits of hosts other than the crawled site. They are
// shared by every sizer in the process, so two size checks running at the
// same time (a dashboard crawl started while the previous crawl's check is
// still going) still send one host at most one request per assetInterval
// and at most hostLimit(host, assetPerHost) at once.
var assetHosts = &assetHostLimits{}

type assetHostLimits struct {
	once  sync.Once
	rl    *hostRateLimiter
	mu    sync.Mutex
	slots map[string]chan struct{}
	// slowUntil: a host slowed down after a 429/503 is not slowed again
	// for the answers already in flight (one step per pause).
	slowUntil map[string]time.Time
	// answers since the last slow-down, per slowed host (speed-up step).
	answers map[string]int
}

// assetRecoverAfter answers in a row (no 429/503) halve a slowed host's gap
// again, down to assetInterval. A 429 can be about one URL (a thumbnail
// that fails to render) rather than the pace; a host that does mind the
// pace answers 429 again and is slowed again.
var assetRecoverAfter = 100 // a variable so tests can shorten it

// answered records an answer that was not a 429/503.
func (a *assetHostLimits) answered(rawURL string) {
	rl := a.limiter()
	host := extractDomain(rawURL)
	a.mu.Lock()
	if a.answers == nil {
		a.answers = make(map[string]int)
	}
	_, slowed := a.slowUntil[host]
	speedUp := false
	if slowed {
		a.answers[host]++
		if a.answers[host] >= assetRecoverAfter {
			a.answers[host] = 0
			speedUp = true
		}
	}
	a.mu.Unlock()
	if speedUp {
		if cur := rl.Interval(rawURL); cur > assetInterval {
			rl.SetInterval(rawURL, max(cur/2, assetInterval))
		}
	}
}

// slowDown pauses rawURL's host for pause and doubles the gap between its
// requests (100 ms -> 200 ms -> ... at most assetMaxInterval), until
// assetRecoverAfter answers in a row halve it again (answered).
func (a *assetHostLimits) slowDown(rawURL string, pause time.Duration) {
	rl := a.limiter()
	host := extractDomain(rawURL)
	now := time.Now()
	a.mu.Lock()
	if a.slowUntil == nil {
		a.slowUntil = make(map[string]time.Time)
	}
	step := now.After(a.slowUntil[host])
	if step {
		a.slowUntil[host] = now.Add(max(pause, time.Second))
	}
	if a.answers != nil {
		a.answers[host] = 0
	}
	a.mu.Unlock()
	rl.Pause(rawURL, pause)
	if step {
		rl.SetInterval(rawURL, min(max(2*rl.Interval(rawURL), 2*assetInterval), assetMaxInterval))
	}
}

func (a *assetHostLimits) limiter() *hostRateLimiter {
	a.once.Do(func() { a.rl = newHostRateLimiter(assetInterval, nil) })
	return a.rl
}

// acquire takes one of host's in-flight slots; ok is false when ctx ended
// first.
func (a *assetHostLimits) acquire(ctx context.Context, host string) (release func(), ok bool) {
	a.mu.Lock()
	if a.slots == nil {
		a.slots = make(map[string]chan struct{})
	}
	slot, found := a.slots[host]
	if !found {
		slot = make(chan struct{}, hostLimit(host, assetPerHost))
		a.slots[host] = slot
	}
	a.mu.Unlock()
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, true
	case <-ctx.Done():
		return nil, false
	}
}
