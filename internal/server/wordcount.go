package server

import "github.com/micelio/micelio/internal/analysis"

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
	if b := analysis.BuildWordCountBuckets(results.Pages); b != nil {
		stats["wordCountBuckets"] = b
	}
}
