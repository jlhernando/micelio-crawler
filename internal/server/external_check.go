package server

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/types"
)

// checkExternalLinks runs the post-crawl external link check for a dashboard
// crawl, emitting "external_check" phase progress so the Monitor shows activity.
// When the crawl was gracefully stopped its context is already cancelled, so
// the manager context is used instead (same rule as post-crawl analysis).
func (m *CrawlManager) checkExternalLinks(ctx context.Context, crawlID string, cfg types.CrawlConfig, links map[string][]string, stopping bool) *crawler.ExternalCheckSummary {
	checkCtx := ctx
	if stopping {
		checkCtx = m.ctx
	}
	start := time.Now()
	// Runs after the results are shown, so progress goes to the Results page
	// (analysis_progress) and to /stats for a page opened mid-check.
	emitPhase := func(loaded, total int) {
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
	summary := crawler.CheckExternalLinks(checkCtx, links, crawler.ExternalCheckOptions{
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
	log.Printf("[external] crawl %s: checked %d/%d external URLs in %s, %d broken, %d skipped",
		crawlID, summary.Checked, summary.Total, time.Since(start).Round(time.Millisecond), len(summary.Broken), summary.Skipped)
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
}
