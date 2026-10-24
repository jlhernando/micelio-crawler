package analysis

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// ResourceSizer returns the byte size of a resource (typically via a HEAD
// request). ok is false when the size is unknown. The crawler provides one
// that reuses the crawl HTTP client (proxy, Tor, SSRF guard) and the per-host
// rate limiter.
type ResourceSizer func(ctx context.Context, url string) (size int64, ok bool)

// pageWeightPerHost caps concurrent HEAD requests to a single host.
const pageWeightPerHost = 2

// ComputePageWeight HEAD-fetches unique resources to get sizes, then builds
// PageWeightData per page from resourceRefs and transferSizes.
// At most `concurrency` requests run in total and at most 2 per host.
// If sizer is nil, a plain HEAD client without proxy or SSRF guard is used
// (tests and standalone callers only).
func ComputePageWeight(ctx context.Context, pages []*types.PageData, resourceRefs map[string][]types.ResourceEntry, transferSizes map[string]int64, concurrency int, sizer ResourceSizer) {
	uniqueResources := make(map[string]struct{})
	for _, refs := range resourceRefs {
		for _, r := range refs {
			uniqueResources[r.URL] = struct{}{}
		}
	}
	if len(uniqueResources) == 0 {
		return
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	if sizer == nil {
		sizer = plainHeadSizer("")
	}

	fmt.Fprintf(os.Stderr, "  Fetching sizes for %d unique resources...\n", len(uniqueResources))

	sizeCache := make(map[string]int64)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	var hostMu sync.Mutex
	hostSems := make(map[string]chan struct{})
	hostSem := func(u string) chan struct{} {
		host := u
		if p, err := url.Parse(u); err == nil {
			host = strings.ToLower(p.Host)
		}
		hostMu.Lock()
		defer hostMu.Unlock()
		hs, ok := hostSems[host]
		if !ok {
			hs = make(chan struct{}, pageWeightPerHost)
			hostSems[host] = hs
		}
		return hs
	}
	var checked atomic.Int32
	total := len(uniqueResources)

launch:
	for resURL := range uniqueResources {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			break launch
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(u string) {
			defer func() { <-sem; wg.Done() }()
			hs := hostSem(u)
			select {
			case <-ctx.Done():
				return
			case hs <- struct{}{}:
			}
			if ctx.Err() != nil {
				<-hs
				return
			}
			size, ok := sizer(ctx, u)
			<-hs
			if ok {
				mu.Lock()
				sizeCache[u] = size
				mu.Unlock()
			}
			n := int(checked.Add(1))
			if n%20 == 0 {
				fmt.Fprintf(os.Stderr, "\r  Resources: %d/%d", n, total)
			}
		}(resURL)
	}
	wg.Wait()
	fmt.Fprintf(os.Stderr, "\r  Resources: %d/%d done\n", checked.Load(), total)

	assignPageWeight(pages, resourceRefs, transferSizes, sizeCache)
}

// plainHeadSizer is the fallback sizer: a HEAD request with a basic client.
func plainHeadSizer(userAgent string) ResourceSizer {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	return func(ctx context.Context, u string) (int64, bool) {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			return 0, false
		}
		if userAgent != "" {
			req.Header.Set("User-Agent", userAgent)
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, false
		}
		resp.Body.Close()
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if size, err := strconv.ParseInt(cl, 10, 64); err == nil {
				return size, true
			}
		}
		return 0, false
	}
}

// AssignPageWeightFromRefs builds PageWeightData per page without HEAD-fetching.
// Uses BodySize from each page as HTMLBytes and existing SizeBytes on resources.
func AssignPageWeightFromRefs(pages []*types.PageData, resourceRefs map[string][]types.ResourceEntry, transferSizes map[string]int64) {
	assignPageWeight(pages, resourceRefs, transferSizes, nil)
}

func assignPageWeight(pages []*types.PageData, resourceRefs map[string][]types.ResourceEntry, transferSizes map[string]int64, sizeCache map[string]int64) {
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
