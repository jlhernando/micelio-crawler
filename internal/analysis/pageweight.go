package analysis

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/netguard"
	"github.com/micelio/micelio/internal/types"
)

// SizeResult is the answer to one resource size request.
type SizeResult struct {
	Size int64
	// OK is false when the size is unknown (error, 4xx/5xx, no
	// Content-Length). The resource then counts as 0 bytes, as before.
	OK bool
	// Throttled is set on 429/503: the host asked us to slow down. The URL
	// is asked again after RetryAfter (or a growing backoff when 0).
	Throttled  bool
	RetryAfter time.Duration
}

// ResourceSizer sizes page resources (typically with a HEAD request). The
// crawler provides one that reuses the crawl HTTP client (proxy, Tor, SSRF
// guard) and paces every host: the crawled site at the crawl's own delay,
// other hosts (CDNs, image hosts) at their own modest rate.
type ResourceSizer struct {
	Size func(ctx context.Context, url string) SizeResult
	// PerHost returns how many requests may run at once on host (lower-case
	// host[:port]). Nil means DefaultPageWeightPerHost for every host.
	PerHost func(host string) int
}

const (
	// DefaultPageWeightPerHost caps concurrent size requests to one host
	// when the sizer does not say otherwise.
	DefaultPageWeightPerHost = 2
	// Size requests in flight across all hosts. Each host keeps its own
	// limit, so this only matters when resources sit on many hosts; it is
	// independent of the crawl's concurrency (which is about one site).
	minPageWeightConcurrency = 16
	maxPageWeightConcurrency = 32
	// A host answering 429/503 is asked one at a time after a pause (its
	// Retry-After, else pageWeightBaseWait doubling, at most
	// pageWeightMaxWait); an answer after the pause restores its limit.
	//
	// A 429 can also belong to one URL, not the host: Wikimedia answers 429
	// for good to a thumbnail it fails to render, while serving the others.
	// So a URL is asked at most pageWeightMaxAttempts times, then left
	// unsized like any error. The host is given up (its remaining resources
	// left unsized and reported, never guessed) only when more than
	// pageWeightMaxStrikes pauses in a row each start with a different
	// URL refused, with no answer in between (requests already in flight
	// when a pause starts add no strike).
	pageWeightMaxStrikes  = 4
	pageWeightMaxAttempts = 2
	pageWeightMaxWait     = 60 * time.Second
)

// pageWeightBaseWait is the first pause after a 429/503 without Retry-After
// (a variable so tests can shorten it).
var pageWeightBaseWait = 5 * time.Second

// PageWeightOptions configures FetchResourceSizes and ComputePageWeight.
type PageWeightOptions struct {
	// Concurrency is the crawl's concurrency; the size check uses at least
	// minPageWeightConcurrency slots in total (per-host limits still apply).
	Concurrency int
	// Sizer sizes one resource. A zero Sizer uses a plain HEAD client
	// without proxy (tests and standalone callers only).
	Sizer ResourceSizer
	// OnProgress is called from one goroutine: once with 0 before the first
	// request, then after each resource is done.
	OnProgress func(done, total int)
}

// PageWeightHost is the request count and rate of one host.
type PageWeightHost struct {
	Host      string
	Requests  int
	Throttled int
	PerSecond float64
}

// PageWeightSummary is the outcome of FetchResourceSizes.
type PageWeightSummary struct {
	Total    int // unique resource URLs
	Sized    int // answered with a size
	Unsized  int // answered without a usable size (error, 4xx, no Content-Length)
	Requests int // requests sent, retries included
	// Throttles counts 429/503 answers. RateLimited resources were left
	// unsized because their host kept answering 429/503.
	Throttles int
	// Refused resources were answered 429/503 pageWeightMaxAttempts times
	// (counted in Unsized); the host kept serving others.
	Refused          int
	RateLimited      int
	RateLimitedHosts []string
	Skipped          int              // not asked (cancelled)
	Hosts            []PageWeightHost // busiest hosts first (top 5)
	Duration         time.Duration
}

// String is the one-line log summary.
func (s PageWeightSummary) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d unique resources in %s: %d sized, %d without size, %d requests",
		s.Total, s.Duration.Round(time.Millisecond), s.Sized, s.Unsized, s.Requests)
	if s.Throttles > 0 {
		fmt.Fprintf(&b, ", %d answered 429/503", s.Throttles)
	}
	if s.Refused > 0 {
		fmt.Fprintf(&b, " (%d URLs refused every time)", s.Refused)
	}
	if s.RateLimited > 0 {
		fmt.Fprintf(&b, ", %d left unsized (rate-limited: %s)", s.RateLimited, strings.Join(s.RateLimitedHosts, ", "))
	}
	if s.Skipped > 0 {
		fmt.Fprintf(&b, ", %d skipped (cancelled)", s.Skipped)
	}
	if len(s.Hosts) > 0 {
		parts := make([]string, len(s.Hosts))
		for i, h := range s.Hosts {
			parts[i] = fmt.Sprintf("%s %d req %.1f/s", h.Host, h.Requests, h.PerSecond)
		}
		fmt.Fprintf(&b, "; hosts: %s", strings.Join(parts, ", "))
	}
	return b.String()
}

// Annotate records the size check outcome on page weight stats (nil-safe).
func (s PageWeightSummary) Annotate(st *types.PageWeightStats) {
	if st == nil {
		return
	}
	st.ResourcesTotal = s.Total
	st.ResourcesSized = s.Sized
	st.ResourcesUnsized = s.Total - s.Sized
	st.RateLimitedHosts = s.RateLimitedHosts
}

// BuildPageWeightStats aggregates the page weights on pages (200s only).
func BuildPageWeightStats(pages []*types.PageData) *types.PageWeightStats {
	return buildPageWeightStats(pages)
}

// ComputePageWeight sizes every unique resource once (FetchResourceSizes),
// then builds PageWeightData per page from resourceRefs and transferSizes.
func ComputePageWeight(ctx context.Context, pages []*types.PageData, resourceRefs map[string][]types.ResourceEntry, transferSizes map[string]int64, opts PageWeightOptions) PageWeightSummary {
	sizes, sum := FetchResourceSizes(ctx, resourceRefs, opts)
	if sum.Total > 0 {
		ApplyResourceSizes(pages, resourceRefs, transferSizes, sizes)
	}
	return sum
}

// FetchResourceSizes asks the size of each unique resource URL in refs
// once. Hosts are served round-robin from their own queue (most referenced
// resources first), each with its own in-flight limit, so a host with
// thousands of images never holds every slot and never gets more than its
// limit. Pacing between requests is the sizer's job. A 429/503 is "slow
// down", not a missing size: the URL is asked again after a pause.
func FetchResourceSizes(ctx context.Context, refs map[string][]types.ResourceEntry, opts PageWeightOptions) (map[string]int64, PageWeightSummary) {
	start := time.Now()
	// One target per unique URL (40 bytes; the URL string is the one in
	// refs, the host string is shared by the host's targets). Host queues
	// hold indexes into targets.
	type target struct {
		url  string
		host string
		refs int
	}
	counts := make(map[string]int)
	for _, rs := range refs {
		for _, r := range rs {
			counts[r.URL]++
		}
	}
	sum := PageWeightSummary{Total: len(counts)}
	if len(counts) == 0 {
		return nil, sum
	}
	sizer := opts.Sizer
	if sizer.Size == nil {
		sizer.Size = plainHeadSizer("")
	}
	perHost := sizer.PerHost
	if perHost == nil {
		perHost = func(string) int { return DefaultPageWeightPerHost }
	}
	concurrency := min(max(opts.Concurrency, minPageWeightConcurrency), maxPageWeightConcurrency)

	targets := make([]target, 0, len(counts))
	hostNames := make(map[string]string)
	for u, n := range counts {
		host := u
		if p, err := url.Parse(u); err == nil {
			host = strings.ToLower(p.Host)
		}
		if h, ok := hostNames[host]; ok {
			host = h
		} else {
			hostNames[host] = host
		}
		targets = append(targets, target{url: u, host: host, refs: n})
	}
	counts, hostNames = nil, nil
	// Most referenced first (site-wide CSS/JS, logos), so a cancelled or
	// rate-limited run still sizes what weighs on most pages.
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].refs != targets[j].refs {
			return targets[i].refs > targets[j].refs
		}
		return targets[i].url < targets[j].url
	})
	queues := make(map[string][]int32)
	var hosts []string
	for i, t := range targets {
		if _, ok := queues[t.host]; !ok {
			hosts = append(hosts, t.host)
		}
		queues[t.host] = append(queues[t.host], int32(i))
	}
	limits := make(map[string]int, len(hosts))
	for _, h := range hosts {
		limits[h] = max(1, perHost(h))
	}

	fmt.Fprintf(os.Stderr, "  Fetching sizes for %d unique resources on %d hosts...\n", sum.Total, len(hosts))

	type result struct {
		i   int32
		r   SizeResult
		end time.Time
	}
	type throttle struct {
		strikes   int // pauses started by a different URL's first refusal, in a row
		pauses    int // pauses since the last answer (backoff exponent)
		notBefore time.Time
	}
	type hostStat struct {
		requests, throttled int
		first, last         time.Time
	}
	var (
		sizes     = make(map[string]int64, len(targets))
		inflight  = make(map[string]int)
		throttled = make(map[string]*throttle)
		stats     = make(map[string]*hostStat)
		done      = make(chan result, concurrency)
		active    int
		cursor    int
		finished  int
		lastLog   time.Time
	)
	progress := func() {
		finished++
		if opts.OnProgress != nil {
			opts.OnProgress(finished, sum.Total)
		}
		if finished == sum.Total || time.Since(lastLog) > 2*time.Second {
			lastLog = time.Now()
			fmt.Fprintf(os.Stderr, "\r  Resources: %d/%d", finished, sum.Total)
		}
	}
	limitFor := func(h string) int {
		if th := throttled[h]; th != nil && th.pauses > 0 {
			return 1
		}
		return limits[h]
	}
	attempts := make(map[int32]int) // throttled URLs: times refused
	// next returns a host with queued URLs and a free slot, or "".
	next := func(now time.Time) string {
		for range hosts {
			if cursor >= len(hosts) {
				cursor = 0
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
			if len(queues[h]) == 0 || inflight[h] >= limitFor(h) {
				continue
			}
			if th := throttled[h]; th != nil && now.Before(th.notBefore) {
				continue
			}
			return h
		}
		return ""
	}
	// wakeAt is the end of the earliest pause of an idle throttled host
	// with queued URLs (zero if none).
	wakeAt := func() time.Time {
		var w time.Time
		for h, th := range throttled {
			if len(queues[h]) > 0 && inflight[h] == 0 && (w.IsZero() || th.notBefore.Before(w)) {
				w = th.notBefore
			}
		}
		return w
	}
	if opts.OnProgress != nil {
		opts.OnProgress(0, sum.Total)
	}
	for ctx.Err() == nil {
		for active < concurrency {
			now := time.Now()
			h := next(now)
			if h == "" {
				break
			}
			i := queues[h][0]
			queues[h] = queues[h][1:]
			inflight[h]++
			active++
			st := stats[h]
			if st == nil {
				st = &hostStat{first: now}
				stats[h] = st
			}
			st.requests++
			sum.Requests++
			go func(i int32, u string) {
				r := sizer.Size(ctx, u)
				done <- result{i: i, r: r, end: time.Now()}
			}(i, targets[i].url)
		}
		var timer <-chan time.Time
		if w := wakeAt(); !w.IsZero() {
			timer = time.After(max(time.Until(w), time.Millisecond))
		}
		if active == 0 && timer == nil {
			break
		}
		select {
		case res := <-done:
			active--
			t := targets[res.i]
			h := t.host
			inflight[h]--
			st := stats[h]
			st.last = res.end
			if ctx.Err() != nil {
				continue // cancelled mid-flight: counted as skipped below
			}
			if res.r.Throttled {
				sum.Throttles++
				st.throttled++
				th := throttled[h]
				if th == nil {
					th = &throttle{}
					throttled[h] = th
				}
				attempts[res.i]++
				// Requests in flight when the host started refusing come
				// back throttled too: one pause, and at most one strike,
				// for them all. Otherwise a big site with 6 requests in
				// flight would be given up by a single burst of 429s.
				if time.Now().After(th.notBefore) {
					if attempts[res.i] == 1 {
						th.strikes++
					}
					th.pauses++
					wait := res.r.RetryAfter
					if wait <= 0 {
						wait = pageWeightBaseWait << min(th.pauses-1, 4)
					}
					th.notBefore = time.Now().Add(min(wait, pageWeightMaxWait))
				}
				if attempts[res.i] >= pageWeightMaxAttempts && th.strikes <= pageWeightMaxStrikes {
					// This URL keeps being refused: no size, like an error.
					delete(attempts, res.i)
					sum.Unsized++
					sum.Refused++
					progress()
					continue
				}
				if th.strikes > pageWeightMaxStrikes {
					n := 1 + len(queues[h])
					sum.RateLimitedHosts = append(sum.RateLimitedHosts, h)
					sum.RateLimited += n
					queues[h] = nil
					for range n {
						progress()
					}
					continue
				}
				queues[h] = append([]int32{res.i}, queues[h]...)
				if !slices.Contains(hosts, h) {
					hosts = append(hosts, h)
				}
				continue
			}
			if th := throttled[h]; th != nil && res.end.After(th.notBefore) {
				// Answered after the last pause: the host accepts this pace.
				th.strikes, th.pauses = 0, 0
			}
			delete(attempts, res.i)
			if res.r.OK {
				sizes[t.url] = res.r.Size
				sum.Sized++
			} else {
				sum.Unsized++
			}
			progress()
		case <-timer:
		case <-ctx.Done():
		}
	}
	for ; active > 0; active-- {
		<-done
	}
	if finished > 0 {
		fmt.Fprintln(os.Stderr)
	}
	slices.Sort(sum.RateLimitedHosts)
	sum.RateLimitedHosts = slices.Compact(sum.RateLimitedHosts)
	sum.Skipped = sum.Total - sum.Sized - sum.Unsized - sum.RateLimited

	for h, st := range stats {
		ph := PageWeightHost{Host: h, Requests: st.requests, Throttled: st.throttled}
		if span := st.last.Sub(st.first).Seconds(); span > 0 {
			ph.PerSecond = float64(st.requests) / span
		}
		sum.Hosts = append(sum.Hosts, ph)
	}
	sort.Slice(sum.Hosts, func(i, j int) bool {
		if sum.Hosts[i].Requests != sum.Hosts[j].Requests {
			return sum.Hosts[i].Requests > sum.Hosts[j].Requests
		}
		return sum.Hosts[i].Host < sum.Hosts[j].Host
	})
	if len(sum.Hosts) > 5 {
		sum.Hosts = sum.Hosts[:5]
	}
	sum.Duration = time.Since(start)
	fmt.Fprintf(os.Stderr, "  [page-weight] %s\n", sum)
	return sizes, sum
}

// plainHeadSizer is the fallback sizer: a HEAD request with a basic client.
func plainHeadSizer(userAgent string) func(context.Context, string) SizeResult {
	// Resource URLs come from crawled pages: refuse private addresses at
	// connect time, redirects included.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Direct connections only: through an environment proxy the dialer
	// would check the proxy's address, not the resource's.
	transport.Proxy = nil
	transport.DialContext = netguard.Dialer(10 * time.Second).DialContext
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	return func(ctx context.Context, u string) SizeResult {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			return SizeResult{}
		}
		if userAgent != "" {
			req.Header.Set("User-Agent", userAgent)
		}
		resp, err := client.Do(req)
		if err != nil {
			return SizeResult{}
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			var wait time.Duration
			if secs, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && secs > 0 {
				wait = time.Duration(secs) * time.Second
			}
			return SizeResult{Throttled: true, RetryAfter: wait}
		}
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if size, err := strconv.ParseInt(cl, 10, 64); err == nil {
				return SizeResult{Size: size, OK: true}
			}
		}
		return SizeResult{}
	}
}

// AssignPageWeightFromRefs builds PageWeightData per page without HEAD-fetching.
// Uses BodySize from each page as HTMLBytes and existing SizeBytes on resources.
func AssignPageWeightFromRefs(pages []*types.PageData, resourceRefs map[string][]types.ResourceEntry, transferSizes map[string]int64) {
	ApplyResourceSizes(pages, resourceRefs, transferSizes, nil)
}

// ApplyResourceSizes builds PageWeightData for each page in resourceRefs
// from the sizes FetchResourceSizes returned (a resource without a size
// counts as 0 bytes). Pages missing from pages are skipped. Running it again
// on another copy of the same pages gives the same result.
func ApplyResourceSizes(pages []*types.PageData, resourceRefs map[string][]types.ResourceEntry, transferSizes map[string]int64, sizeCache map[string]int64) {
	pageMap := make(map[string]*types.PageData, len(pages))
	for _, p := range pages {
		pageMap[p.URL] = p
	}
	for pageURL, refs := range resourceRefs {
		page, ok := pageMap[pageURL]
		if !ok {
			continue
		}
		htmlBytes := int64(0)
		if transferSizes != nil {
			htmlBytes = transferSizes[pageURL]
		}
		if htmlBytes == 0 {
			htmlBytes = page.BodySize
		}
		byType := map[string]types.TypeWeightSummary{
			"html": {Count: 1, Bytes: htmlBytes},
		}
		totalBytes := htmlBytes
		for i := range refs {
			if sizeCache != nil {
				if cached, ok := sizeCache[refs[i].URL]; ok {
					v := cached
					refs[i].SizeBytes = &v
				}
			}
			size := int64(0)
			if refs[i].SizeBytes != nil {
				size = *refs[i].SizeBytes
			}
			totalBytes += size
			t := string(refs[i].Type)
			entry := byType[t]
			entry.Count++
			entry.Bytes += size
			byType[t] = entry
		}
		page.PageWeight = &types.PageWeightData{
			TotalBytes: totalBytes,
			HTMLBytes:  htmlBytes,
			ByType:     byType,
			Resources:  refs,
		}
	}
}
