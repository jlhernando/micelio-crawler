package server

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/types"
)

// checkExternalLinks runs the post-crawl external link check for a dashboard
// crawl, emitting "external_check" phase progress so the Monitor shows activity.
// ctx is the crawl's deferred-analysis context (startDeferred): deleting or
// restarting the crawl cancels the check, and no progress is reported after.
func (m *CrawlManager) checkExternalLinks(ctx context.Context, crawlID string, cfg types.CrawlConfig, links map[string][]string) *crawler.ExternalCheckSummary {
	start := time.Now()
	// Runs after the results are shown, so progress goes to the Results page
	// (analysis_progress) and to /stats for a page opened mid-check.
	emitPhase := func(loaded, total int) {
		if ctx.Err() != nil {
			return // a restarted run with the same ID owns the progress now
		}
		m.mu.RLock()
		res := m.results[crawlID]
		m.mu.RUnlock()
		if res != nil {
			res.externalChecked.Store(int64(loaded))
			res.externalTotal.Store(int64(total))
		}
		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: "analysis_progress",
			Data: map[string]interface{}{
				"crawlId": crawlID,
				"phase":   "external_check",
				"loaded":  loaded,
				"total":   total,
			},
		})
	}
	emitPhase(0, len(links))

	var (
		emitMu   sync.Mutex
		lastEmit time.Time
	)
	summary := crawler.CheckExternalLinks(ctx, links, crawler.ExternalCheckOptions{
		UserAgent:   cfg.UserAgent,
		Concurrency: crawler.DefaultExternalCheckConcurrency,
		Timeout:     crawler.DefaultExternalCheckTimeout,
		SkipSSRF:    cfg.SkipSSRF,
		OnProgress: func(n, total int) {
			// Throttle WebSocket events for large link sets.
			emitMu.Lock()
			defer emitMu.Unlock()
			if n == total || time.Since(lastEmit) > 500*time.Millisecond {
				lastEmit = time.Now()
				emitPhase(n, total)
			}
		},
	})
	log.Printf("[external] crawl %s: checked %d/%d external URLs in %s, %d broken, %d skipped; busiest: %s; rate-limited: %d (%s)",
		crawlID, summary.Checked, summary.Total, time.Since(start).Round(time.Millisecond), len(summary.Broken), summary.Skipped, strings.Join(summary.Busiest, ", "),
		summary.RateLimited, strings.Join(summary.RateLimitedHosts, ", "))
	return &summary
}

// applyExternalCheckSummary records how many external URLs were checked so the
// dashboard can tell "no broken links" apart from "not checked".
func applyExternalCheckSummary(stats *types.CrawlStats, summary *crawler.ExternalCheckSummary) {
	if stats == nil || summary == nil {
		return
	}
	stats.ExternalLinksChecked = summary.Checked
	stats.ExternalLinksUnchecked = summary.Skipped
	stats.ExternalLinksRateLimited = summary.RateLimited
	stats.ExternalRateLimitedHosts = summary.RateLimitedHosts
}
