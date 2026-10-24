package server

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestBackfillWordCountBuckets(t *testing.T) {
	results := &CrawlResults{
		Stats:      map[string]interface{}{"totalPages": 2},
		Pages:      []*types.PageData{{WordCount: 50}, {WordCount: 2500}},
		PagesReady: true,
	}
	stats := capStatsForAPI(results.Stats)
	backfillWordCountBuckets(stats, results)
	b, ok := stats["wordCountBuckets"].(*types.WordCountBuckets)
	if !ok {
		t.Fatalf("wordCountBuckets missing: %#v", stats["wordCountBuckets"])
	}
	if b.Counts[0] != 1 || b.Counts[5] != 1 || b.Pages != 2 {
		t.Errorf("unexpected buckets %+v", b)
	}
	if _, leaked := results.Stats["wordCountBuckets"]; leaked {
		t.Error("backfill must not mutate the cached stats map")
	}

	// Existing reporter output wins.
	stats = map[string]interface{}{"wordCountBuckets": "keep"}
	backfillWordCountBuckets(stats, results)
	if stats["wordCountBuckets"] != "keep" {
		t.Error("existing value overwritten")
	}

	// Pages not loaded yet: no-op.
	results.PagesReady = false
	stats = map[string]interface{}{}
	backfillWordCountBuckets(stats, results)
	if _, ok := stats["wordCountBuckets"]; ok {
		t.Error("expected no buckets before pages are ready")
	}
}
