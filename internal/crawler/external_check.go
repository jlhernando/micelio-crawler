package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/micelio/micelio/internal/types"
)

const (
	// DefaultExternalCheckMaxURLs caps how many unique external URLs are
	// checked after a crawl. The most-referenced URLs are checked first.
	DefaultExternalCheckMaxURLs = 50000
	// DefaultExternalCheckConcurrency checks this many external URLs at once.
	// They are on many different sites and each host gets at most
	// defaultExternalPerHost, so this is independent of the crawl's
	// concurrency (which is about one site).
	DefaultExternalCheckConcurrency = 20
	// DefaultExternalCheckTimeout is the wait per external URL.
	DefaultExternalCheckTimeout = 6 * time.Second
	// DefaultExternalCheckMaxFoundOn caps the source pages kept per broken URL.
	DefaultExternalCheckMaxFoundOn = 10
	defaultExternalPerHost         = 2
	// bigSitePerHost is the per-host limit for large platforms (bigSites):
	// six HEAD requests at once are nothing to them, and a blog can link to
	// GitHub hundreds of times (452 of 1,773 URLs in one test, which alone
	// took 2+ minutes at 2 at a time).
	bigSitePerHost              = 6
	maxExternalCheckConcurrency = 50
)

// ExternalCheckOptions configures CheckExternalLinks.
type ExternalCheckOptions struct {
	// OnProgress is called after each URL is checked (may be called concurrently).
	OnProgress func(checked, total int)
	UserAgent  string
	// Timeout per request (default 10s).
	Timeout time.Duration
	// Concurrency is the global number of in-flight checks (default 5, max 50).
	Concurrency int
	// PerHostConcurrency limits in-flight checks per host (default 2) so a
	// crawl linking heavily to one site does not hammer it.
	PerHostConcurrency int
	// MaxURLs caps unique URLs checked (0 = DefaultExternalCheckMaxURLs, <0 = no cap).
	MaxURLs int
	// MaxFoundOn caps source pages per result (0 = DefaultExternalCheckMaxFoundOn).
	MaxFoundOn int
	// SkipSSRF disables private-address protection (tests only).
	SkipSSRF bool
}

// ExternalCheckSummary is the outcome of CheckExternalLinks.
type ExternalCheckSummary struct {
	// Broken holds URLs that returned >= 400 or failed (sorted by URL).
	Broken []types.ExternalLinkResult
	// Total is the number of unique checkable (http/https) external URLs.
	Total int
	// Checked is how many were actually requested.
	Checked int
	// Skipped is how many were left unchecked (cap or cancellation).
	Skipped int
	// Busiest lists the hosts with the most request time, e.g.
	// "github.com 212 URLs 95s" (top 3; for the log).
	Busiest []string
}

// CheckExternalLinks checks the status of external URLs (map of URL -> pages
// linking to it) using HEAD with a GET fallback for servers that reject HEAD.
// Only broken results are returned to keep memory bounded on large crawls.
// Robots.txt is not consulted: a single HEAD per URL is a link check, not a crawl.
func CheckExternalLinks(ctx context.Context, links map[string][]string, opts ExternalCheckOptions) ExternalCheckSummary {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 5
	}
	if opts.Concurrency > maxExternalCheckConcurrency {
		opts.Concurrency = maxExternalCheckConcurrency
	}
	if opts.PerHostConcurrency <= 0 {
		opts.PerHostConcurrency = defaultExternalPerHost
	}
	if opts.Timeout <= 0 {
		opts.Timeout = externalTimeout
	}
	if opts.MaxURLs == 0 {
		opts.MaxURLs = DefaultExternalCheckMaxURLs
	}
	if opts.MaxFoundOn <= 0 {
		opts.MaxFoundOn = DefaultExternalCheckMaxFoundOn
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "Micelio/1.0"
	}

	type target struct {
		url   string
		host  string
		site  string // scheme://host: the unit for host-level failures
		pages []string
		refs  int
	}
	targets := make([]target, 0, len(links))
	for raw, pages := range links {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		host := strings.ToLower(u.Host)
		targets = append(targets, target{url: raw, host: host, site: u.Scheme + "://" + host, pages: pages, refs: len(pages)})
	}
	// Most-referenced first so the cap keeps the URLs that matter most.
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].refs != targets[j].refs {
			return targets[i].refs > targets[j].refs
		}
		return targets[i].url < targets[j].url
	})

	summary := ExternalCheckSummary{Total: len(targets)}
	if opts.MaxURLs > 0 && len(targets) > opts.MaxURLs {
		targets = targets[:opts.MaxURLs]
	}

	var client *http.Client
	if opts.SkipSSRF {
		client = &http.Client{Timeout: opts.Timeout, CheckRedirect: limitRedirects}
	} else {
		client = SafeClientFollowRedirects(opts.Timeout)
	}
	defer client.CloseIdleConnections()

	var (
		mu      sync.Mutex
		checked atomic.Int32
		total   = len(targets)
	)
	report := func(t target, status int, err error) {
		n := int(checked.Add(1))
		if status >= 400 || err != nil {
			res := types.ExternalLinkResult{URL: t.url, StatusCode: status, FoundOn: dedupeCap(t.pages, opts.MaxFoundOn)}
			if err != nil {
				res.Error = err.Error()
			}
			mu.Lock()
			summary.Broken = append(summary.Broken, res)
			mu.Unlock()
		}
		if opts.OnProgress != nil {
			opts.OnProgress(n, total)
		}
	}

	// One queue per host, served round-robin. A worker only starts when its
	// host has a free slot, so 300 links to one site no longer hold every
	// global slot while they wait their turn (which ran the whole check at
	// PerHostConcurrency). Hosts keep the order of their most-linked URL.
	queues := make(map[string][]target)
	var hosts []string
	for _, t := range targets {
		if _, ok := queues[t.host]; !ok {
			hosts = append(hosts, t.host)
		}
		queues[t.host] = append(queues[t.host], t)
	}
	type result struct {
		t   target
		err error
		dur time.Duration
	}
	type hostTime struct {
		n   int
		dur time.Duration
	}
	spent := make(map[string]*hostTime)
	var (
		inflight = make(map[string]int)
		dead     = make(map[string]error) // site-level failure: skip its other URLs
		done     = make(chan result, opts.Concurrency)
		active   int
		cursor   int
	)
	// next returns a host with queued URLs and a free slot, or "".
	next := func() string {
		for range hosts {
			if cursor >= len(hosts) {
				cursor = 0
				// Drop finished hosts on each lap.
				kept := hosts[:0]
				for _, h := range hosts {
					if len(queues[h]) > 0 {
						kept = append(kept, h)
					}
				}
				hosts = kept
				if len(hosts) == 0 {
					return ""
				}
			}
			h := hosts[cursor]
			cursor++
			if len(queues[h]) > 0 && inflight[h] < hostLimit(h, opts.PerHostConcurrency) {
				return h
			}
		}
		return ""
	}
	for ctx.Err() == nil {
		for active < opts.Concurrency {
			h := next()
			if h == "" {
				break
			}
			t := queues[h][0]
			queues[h] = queues[h][1:]
			if err, ok := dead[t.site]; ok {
				report(t, 0, err)
				continue
			}
			inflight[h]++
			active++
			go func(t target) {
				start := time.Now()
				status, err := checkExternalStatus(ctx, client, t.url, opts.UserAgent)
				if ctx.Err() == nil { // cancelled mid-flight: don't report as broken
					report(t, status, err)
				}
				done <- result{t, err, time.Since(start)}
			}(t)
		}
		if active == 0 {
			break
		}
		select {
		case r := <-done:
			active--
			inflight[r.t.host]--
			ht := spent[r.t.host]
			if ht == nil {
				ht = &hostTime{}
				spent[r.t.host] = ht
			}
			ht.n++
			ht.dur += r.dur
			if hostUnreachable(r.err, r.t.site) {
				dead[r.t.site] = r.err
			}
		case <-ctx.Done():
		}
	}
	for ; active > 0; active-- {
		<-done
	}

	busy := make([]string, 0, len(spent))
	for h := range spent {
		busy = append(busy, h)
	}
	sort.Slice(busy, func(i, j int) bool { return spent[busy[i]].dur > spent[busy[j]].dur })
	for _, h := range busy[:min(3, len(busy))] {
		summary.Busiest = append(summary.Busiest, fmt.Sprintf("%s %d URLs %s", h, spent[h].n, spent[h].dur.Round(time.Second)))
	}

	summary.Checked = int(checked.Load())
	summary.Skipped = summary.Total - summary.Checked
	sort.Slice(summary.Broken, func(i, j int) bool { return summary.Broken[i].URL < summary.Broken[j].URL })
	return summary
}

// bigSites are platforms built for heavy traffic; subdomains included.
var bigSites = []string{
	"github.com", "githubusercontent.com", "gitlab.com",
	"youtube.com", "youtu.be", "wikipedia.org", "wikimedia.org",
	"twitter.com", "x.com", "google.com", "apple.com", "microsoft.com",
	"amazon.com", "cloudflare.com", "npmjs.com", "pypi.org",
}

// hostLimit is the number of checks allowed at once on host.
func hostLimit(host string, def int) int {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	for _, d := range bigSites {
		if host == d || strings.HasSuffix(host, "."+d) {
			return max(def, bigSitePerHost)
		}
	}
	return def
}

// hostUnreachable reports errors that every URL on site would hit (the name
// does not resolve, nothing listens, or it resolves to a private address),
// so the rest are not requested. Only errors from site itself count, not
// from a site it redirected to. Timeouts are not included: they can be one
// slow page.
func hostUnreachable(err error, site string) bool {
	if err == nil {
		return false
	}
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		return false
	}
	u, perr := url.Parse(uerr.URL)
	if perr != nil || !strings.EqualFold(u.Scheme+"://"+u.Host, site) {
		return false
	}
	var dnsErr *net.DNSError
	var blocked *ssrfBlockedError
	return (errors.As(err, &dnsErr) && dnsErr.IsNotFound) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.As(err, &blocked)
}

func limitRedirects(_ *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	return nil
}

// checkExternalStatus issues HEAD, falling back to GET when the server does
// not support HEAD (405/501) or rejects it outright (some CDNs answer 403/404
// to HEAD only). Returns the final status after redirects.
func checkExternalStatus(ctx context.Context, client *http.Client, rawURL, userAgent string) (int, error) {
	status, err := doExternalRequest(ctx, client, http.MethodHead, rawURL, userAgent)
	// Network errors (DNS, timeout, TLS) would fail for GET too: don't double the wait.
	if err != nil || (status != http.StatusMethodNotAllowed && status != http.StatusNotImplemented &&
		status != http.StatusForbidden && status != http.StatusNotFound) {
		return status, err
	}
	getStatus, getErr := doExternalRequest(ctx, client, http.MethodGet, rawURL, userAgent)
	if getErr != nil {
		return status, nil // keep the HEAD answer when GET fails outright
	}
	return getStatus, nil
}

func doExternalRequest(ctx context.Context, client *http.Client, method, rawURL, userAgent string) (int, error) {
	req, err := newRequest(ctx, method, rawURL)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	// Drain a little so the connection can be reused, never the full body.
	io.CopyN(io.Discard, resp.Body, 4096)
	resp.Body.Close()
	return resp.StatusCode, nil
}

func dedupeCap(pages []string, max int) []string {
	out := make([]string, 0, min(len(pages), max))
	seen := make(map[string]struct{}, min(len(pages), max))
	for _, p := range pages {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
		if len(out) >= max {
			break
		}
	}
	return out
}
