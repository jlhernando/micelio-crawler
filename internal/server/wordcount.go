package server

import (
	"sync"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// buildWordCountBuckets is swapped in tests to count computations.
var buildWordCountBuckets = analysis.BuildWordCountBuckets

// wordBucketCache keeps the backfilled histogram of one crawl, so a
// legacy crawl is scanned once per page set instead of on every /stats
// request. The mutex also makes concurrent first requests share one scan.
type wordBucketCache struct {
	mu  sync.Mutex
	gen string
	ok  bool
	b   *types.WordCountBuckets
}

// backfillWordCountBuckets adds the Content tab word-count histogram to an
// API stats payload for crawls analysed before the reporter produced it.
//
// stats must be the per-request copy returned by capStatsForAPI (never the
// cached results.Stats map), and the caller must hold results.mu (read lock
// is enough). It is a no-op until the cached pages are loaded.
func backfillWordCountBuckets(stats map[string]interface{}, results *CrawlResults) {
	if stats == nil || results == nil {
		return
	}
	if _, ok := stats["wordCountBuckets"]; ok {
		return
	}
	if !results.PagesReady || len(results.Pages) == 0 {
		return
	}
	if b := results.cachedWordCountBuckets(); b != nil {
		stats["wordCountBuckets"] = b
	}
}

// cachedWordCountBuckets computes the histogram once per page set (see
// pagesGen) and reuses it until the pages change. Caller holds r.mu (read).
func (r *CrawlResults) cachedWordCountBuckets() *types.WordCountBuckets {
	gen := r.pagesGen()
	c := &r.wordBuckets
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ok || c.gen != gen {
		c.b, c.gen, c.ok = buildWordCountBuckets(r.Pages), gen, true
	}
	return c.b
}
