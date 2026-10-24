package analysis

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// Large crawls must not store one stats entry per page: per-URL lists are
// capped with their real totals kept, and PageRank lives on pages plus a
// histogram instead of a URL map.
func TestStatsCappedForLargeCrawl(t *testing.T) {
	const n = 30000
	pages := syntheticCrawl(n, 60)
	cfg := PostProcessConfig{SeedURL: "https://ex.com/", LinkIntelligence: true, LINoCentrality: true}
	stats, _ := RunPostCrawlAnalysis(context.Background(), pages, cfg)

	if stats.PageRankScores != nil {
		t.Errorf("PageRankScores kept for %d pages; want nil above %d", n, types.PageRankScoresInlineMax)
	}
	if h := stats.PageRankHistogram; h == nil || h.Total != n {
		t.Fatalf("PageRankHistogram = %+v, want total %d", h, n)
	}
	withPR := 0
	for _, p := range pages {
		if p.PageRank > 0 {
			withPR++
		}
	}
	if withPR < n-n/20 { // never-linked pages normalise to 0
		t.Errorf("%d/%d pages carry PageRank", withPR, n)
	}

	// Every 20th page is never linked: n/20 orphans (minus none: seed is 0).
	if got := stats.OrphanCount(); got < n/20-1 {
		t.Errorf("OrphanCount = %d, want ~%d", got, n/20)
	}
	if len(stats.OrphanPages) != types.StatsListCap {
		t.Errorf("len(OrphanPages) = %d, want cap %d", len(stats.OrphanPages), types.StatsListCap)
	}
	li := stats.LinkIntelligenceStats
	if li == nil {
		t.Fatal("no link intelligence stats")
	}
	if len(li.DilutionWarnings) > types.StatsListCap || li.DilutionWarningsCount <= types.StatsListCap {
		t.Errorf("dilution: len=%d count=%d, want len<=cap<count", len(li.DilutionWarnings), li.DilutionWarningsCount)
	}
	if len(li.UnreachablePages) > types.StatsListCap || li.UnreachablePagesCount < n/20-1 {
		t.Errorf("unreachable: len=%d count=%d", len(li.UnreachablePages), li.UnreachablePagesCount)
	}
	// Kept dilution warnings are the worst (highest out-degree) ones.
	for i := 1; i < len(li.DilutionWarnings); i++ {
		if li.DilutionWarnings[i].OutDegree > li.DilutionWarnings[i-1].OutDegree {
			t.Fatalf("dilution warnings not sorted worst-first at %d", i)
		}
	}

	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 1<<20 {
		t.Errorf("stats JSON is %d bytes for %d pages; want < 1MB", len(data), n)
	}
}

func TestSmallCrawlKeepsPageRankMap(t *testing.T) {
	pages := syntheticCrawl(100, 5)
	stats := RunCoreAnalysis(context.Background(), pages, PostProcessConfig{SeedURL: "https://ex.com/"})
	if len(stats.PageRankScores) != 100 {
		t.Fatalf("PageRankScores has %d entries, want 100", len(stats.PageRankScores))
	}
	for _, p := range pages {
		if stats.PageRankScores[p.URL] != p.PageRank {
			t.Fatalf("%s: map %v != page %v", p.URL, stats.PageRankScores[p.URL], p.PageRank)
		}
	}
	if stats.PageRankHistogram == nil || stats.PageRankHistogram.Total != 100 {
		t.Errorf("histogram = %+v", stats.PageRankHistogram)
	}
}
