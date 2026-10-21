package analysis

import (
	"context"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// strippedSite returns pages without InternalLinks (as reloaded from the crawl
// DB) plus a disk iterator that yields their edges.
//
//	/          -> /a, /b, /gone, /noindex
//	/a         -> /b, /gone
//	/b         -> /
//	/orphan    (no inlinks)
//	/gone      404
//	/noindex   200 but noindex
func strippedSite() ([]*types.PageData, func(fn func(string, []string)) error) {
	idx := types.IndexabilityData{Indexable: true}
	pages := []*types.PageData{
		{URL: "https://ex.com/", StatusCode: 200, Depth: 0, Indexability: idx, InternalLinkCount: 4, ExternalLinkCount: 2},
		{URL: "https://ex.com/a", StatusCode: 200, Depth: 1, Indexability: idx, InternalLinkCount: 2, ExternalLinkCount: 1},
		{URL: "https://ex.com/b", StatusCode: 200, Depth: 1, Indexability: idx, InternalLinkCount: 1},
		{URL: "https://ex.com/orphan", StatusCode: 200, Depth: 2, Indexability: idx},
		{URL: "https://ex.com/gone", StatusCode: 404, Depth: 1},
		{URL: "https://ex.com/noindex", StatusCode: 200, Depth: 1, Indexability: types.IndexabilityData{Indexable: false, Reason: "noindex"}},
	}
	edges := map[string][]string{
		"https://ex.com/":  {"https://ex.com/a", "https://ex.com/b", "https://ex.com/gone", "https://ex.com/noindex"},
		"https://ex.com/a": {"https://ex.com/b", "https://ex.com/gone"},
		"https://ex.com/b": {"https://ex.com/"},
	}
	iter := func(fn func(string, []string)) error {
		for _, src := range []string{"https://ex.com/", "https://ex.com/a", "https://ex.com/b"} {
			fn(src, edges[src])
		}
		return nil
	}
	return pages, iter
}

func TestGenerateReportOrphansFromDiskGraph(t *testing.T) {
	pages, iter := strippedSite()
	stats := GenerateReport(pages, 0, ReportConfig{SeedURL: "https://ex.com/", InternalLinksIter: iter})
	if stats.OrphanMethodology != "graph" {
		t.Errorf("OrphanMethodology = %q, want graph", stats.OrphanMethodology)
	}
	if len(stats.OrphanPages) != 1 || stats.OrphanPages[0] != "https://ex.com/orphan" {
		t.Errorf("OrphanPages = %v, want [/orphan]", stats.OrphanPages)
	}
}

func TestGenerateReportOrphansUnavailableWithoutLinkData(t *testing.T) {
	pages, _ := strippedSite()
	stats := GenerateReport(pages, 0, ReportConfig{SeedURL: "https://ex.com/"})
	if len(stats.OrphanPages) != 0 {
		t.Errorf("OrphanPages = %v, want none when link data is unavailable", stats.OrphanPages)
	}
	if stats.OrphanMethodology != "unavailable" {
		t.Errorf("OrphanMethodology = %q, want unavailable", stats.OrphanMethodology)
	}
}

func TestGenerateReportBrokenLinkFoundOnFromDiskGraph(t *testing.T) {
	pages, iter := strippedSite()
	stats := GenerateReport(pages, 0, ReportConfig{SeedURL: "https://ex.com/", InternalLinksIter: iter})
	if len(stats.BrokenLinks) != 1 {
		t.Fatalf("BrokenLinks = %d, want 1", len(stats.BrokenLinks))
	}
	got := stats.BrokenLinks[0].FoundOn
	if len(got) != 2 || got[0] != "https://ex.com/" || got[1] != "https://ex.com/a" {
		t.Errorf("FoundOn = %v, want [/ /a]", got)
	}
}

func TestFillBrokenLinkSourcesCap(t *testing.T) {
	pages := []*types.PageData{{URL: "https://ex.com/gone", StatusCode: 404}}
	for i := 0; i < 15; i++ {
		pages = append(pages, &types.PageData{
			URL: "https://ex.com/p" + string(rune('a'+i)), StatusCode: 200,
			InternalLinks: []string{"https://ex.com/gone"},
		})
	}
	stats := GenerateReport(pages, 0, ReportConfig{})
	if len(stats.BrokenLinks) != 1 || len(stats.BrokenLinks[0].FoundOn) != maxBrokenLinkSources {
		t.Errorf("FoundOn len = %d, want %d", len(stats.BrokenLinks[0].FoundOn), maxBrokenLinkSources)
	}
}

func TestGenerateReportLinksToNonIndexableFromDiskGraph(t *testing.T) {
	pages, iter := strippedSite()
	stats := GenerateReport(pages, 0, ReportConfig{SeedURL: "https://ex.com/", InternalLinksIter: iter})
	got := map[string]bool{}
	for _, lp := range stats.LinkAnalysis.LinksToNonIndexable {
		got[lp.From+" -> "+lp.To] = true
	}
	want := []string{
		"https://ex.com/ -> https://ex.com/gone",
		"https://ex.com/ -> https://ex.com/noindex",
		"https://ex.com/a -> https://ex.com/gone",
	}
	if len(got) != len(want) {
		t.Errorf("LinksToNonIndexable = %v, want %v", stats.LinkAnalysis.LinksToNonIndexable, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing link pair %q", w)
		}
	}
}

func TestGenerateReportWithGraphReturnsDiskGraph(t *testing.T) {
	pages, iter := strippedSite()
	stats, g := GenerateReportWithGraph(pages, 0, ReportConfig{SeedURL: "https://ex.com/", InternalLinksIter: iter})
	if g == nil || g.N != len(pages) || len(g.Edges) != 7 {
		t.Fatalf("graph = %+v, want %d nodes and 7 edges", g, len(pages))
	}
	if stats.PageRankScores["https://ex.com/b"] == stats.PageRankScores["https://ex.com/orphan"] {
		t.Error("PageRank is flat; want scores from the disk graph")
	}
}

func TestPostCrawlAnalysisKeepsLinkCounts(t *testing.T) {
	pages, iter := strippedSite()
	cfg := PostProcessConfig{SeedURL: "https://ex.com/", InternalLinksIter: iter, LinkIntelligence: true, LINoCentrality: true}
	stats, g := RunPostCrawlAnalysis(context.Background(), pages, cfg)
	if g == nil {
		t.Fatal("expected graph from link intelligence")
	}
	if pages[0].InternalLinkCount != 4 || pages[0].ExternalLinkCount != 2 {
		t.Errorf("home counts = %d/%d, want 4/2", pages[0].InternalLinkCount, pages[0].ExternalLinkCount)
	}
	if pages[1].InternalLinkCount != 2 || pages[1].ExternalLinkCount != 1 {
		t.Errorf("/a counts = %d/%d, want 2/1", pages[1].InternalLinkCount, pages[1].ExternalLinkCount)
	}
	if len(stats.OrphanPages) != 1 {
		t.Errorf("OrphanPages = %v, want 1", stats.OrphanPages)
	}
}

func TestRunLinkIntelligenceReusesGraph(t *testing.T) {
	pages, iter := strippedSite()
	_, g := RunCoreAnalysisWithGraph(context.Background(), pages, PostProcessConfig{SeedURL: "https://ex.com/", InternalLinksIter: iter})
	calls := 0
	countingIter := func(fn func(string, []string)) error {
		calls++
		return iter(fn)
	}
	stats := types.CrawlStats{}
	cfg := PostProcessConfig{SeedURL: "https://ex.com/", InternalLinksIter: countingIter, Graph: g, LinkIntelligence: true, LINoCentrality: true}
	if got := RunDeferredAnalysis(context.Background(), pages, cfg, &stats); got != g {
		t.Error("expected link intelligence to reuse the core graph")
	}
	if calls != 0 {
		t.Errorf("disk iterator read %d times, want 0", calls)
	}
}

func TestRunCoreAnalysisKeepsCountWhenSliceReleased(t *testing.T) {
	pages, iter := strippedSite()
	RunCoreAnalysis(context.Background(), pages, PostProcessConfig{SeedURL: "https://ex.com/", InternalLinksIter: iter})
	if pages[0].InternalLinkCount != 4 {
		t.Errorf("InternalLinkCount = %d, want 4", pages[0].InternalLinkCount)
	}
}
