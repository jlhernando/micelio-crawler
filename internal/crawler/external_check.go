package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	maxExternalCheckConcurrency    = 50
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
		pages []string
		refs  int
	}
	targets := make([]target, 0, len(links))
	for raw, pages := range links {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		targets = append(targets, target{url: raw, host: strings.ToLower(u.Host), pages: pages, refs: len(pages)})
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
		mu       sync.Mutex
		wg       sync.WaitGroup
		hostMu   sync.Mutex
		hostSems = make(map[string]chan struct{})
		sem      = make(chan struct{}, opts.Concurrency)
		checked  atomic.Int32
		total    = len(targets)
	)
	hostSem := func(host string) chan struct{} {
		hostMu.Lock()
		defer hostMu.Unlock()
		s, ok := hostSems[host]
		if !ok {
			s = make(chan struct{}, opts.PerHostConcurrency)
			hostSems[host] = s
		}
		return s
	}

dispatch:
	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			break dispatch
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(t target) {
			defer func() { <-sem; wg.Done() }()
			hs := hostSem(t.host)
			select {
			case <-ctx.Done():
				return
			case hs <- struct{}{}:
			}
			status, err := checkExternalStatus(ctx, client, t.url, opts.UserAgent)
			<-hs
			if ctx.Err() != nil {
				return // cancelled mid-flight: don't report as broken
			}
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
		}(t)
	}
	wg.Wait()

	summary.Checked = int(checked.Load())
	summary.Skipped = summary.Total - summary.Checked
	sort.Slice(summary.Broken, func(i, j int) bool { return summary.Broken[i].URL < summary.Broken[j].URL })
	return summary
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
