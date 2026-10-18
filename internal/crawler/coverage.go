package crawler

import (
	"sort"

	"github.com/micelio/micelio/internal/types"
)

func (q *CrawlQueue) RecordExclusion(reason, rule, rawURL string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.recordExclusion(reason, rule, rawURL)
}

// Caller holds q.mu. Keep bounded samples, even on very large sites.
func (q *CrawlQueue) recordExclusion(reason, rule, rawURL string) {
	if q.exclusions == nil {
		q.exclusions = make(map[string]*types.ExclusionReason)
	}
	key := reason + "\x00" + rule
	entry := q.exclusions[key]
	if entry == nil {
		entry = &types.ExclusionReason{Reason: reason, Rule: rule, Examples: []string{}}
		q.exclusions[key] = entry
	}
	entry.Count++
	if len(entry.Examples) < 5 {
		for _, example := range entry.Examples {
			if example == rawURL {
				return
			}
		}
		// Avoid storing arbitrarily large invalid URLs in the UI database.
		if len(rawURL) > 2048 {
			rawURL = rawURL[:2048] + "…"
		}
		entry.Examples = append(entry.Examples, rawURL)
	}
}

func (q *CrawlQueue) CoverageSnapshot() *types.CrawlCoverage {
	q.mu.Lock()
	defer q.mu.Unlock()
	snapshot := &types.CrawlCoverage{AcceptedURLs: q.visitedCount, PendingURLs: q.pending.Len() + q.spillCount, Reasons: []types.ExclusionReason{}}
	for _, entry := range q.exclusions {
		copy := *entry
		copy.Examples = append([]string{}, entry.Examples...)
		snapshot.Reasons = append(snapshot.Reasons, copy)
	}
	sort.Slice(snapshot.Reasons, func(i, j int) bool {
		a, b := snapshot.Reasons[i], snapshot.Reasons[j]
		if a.Reason == b.Reason {
			return a.Rule < b.Rule
		}
		return a.Reason < b.Reason
	})
	return snapshot
}

// Preserve decisions even when setup rejects every sitemap URL.
type coverageSetupError struct {
	error
	coverage *types.CrawlCoverage
}

func (e *coverageSetupError) Unwrap() error { return e.error }
