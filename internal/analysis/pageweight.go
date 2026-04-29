package analysis

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// ComputePageWeight HEAD-fetches unique resources to get sizes, then builds
// PageWeightData per page from resourceRefs and transferSizes.
func ComputePageWeight(pages []*types.PageData, resourceRefs map[string][]types.ResourceEntry, transferSizes map[string]int64, concurrency int, userAgent string) {
	uniqueResources := make(map[string]struct{})
	for _, refs := range resourceRefs {
		for _, r := range refs {
			uniqueResources[r.URL] = struct{}{}
		}
	}
	if len(uniqueResources) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "  Fetching sizes for %d unique resources...\n", len(uniqueResources))

	sizeCache := make(map[string]int64)
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	var checked atomic.Int32
	total := len(uniqueResources)

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	for resURL := range uniqueResources {
		wg.Add(1)
		sem <- struct{}{}
		go func(u string) {
			defer func() { <-sem; wg.Done() }()
			req, err := http.NewRequest("HEAD", u, nil)
			if err != nil {
				return
			}
			req.Header.Set("User-Agent", userAgent)
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
			if cl := resp.Header.Get("Content-Length"); cl != "" {
				if size, err := strconv.ParseInt(cl, 10, 64); err == nil {
					mu.Lock()
					sizeCache[u] = size
					mu.Unlock()
				}
			}
			n := int(checked.Add(1))
			if n%20 == 0 {
				fmt.Fprintf(os.Stderr, "\r  Resources: %d/%d", n, total)
			}
		}(resURL)
	}
	wg.Wait()
	fmt.Fprintf(os.Stderr, "\r  Resources: %d/%d done\n", total, total)

	assignPageWeight(pages, resourceRefs, transferSizes, sizeCache)
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
