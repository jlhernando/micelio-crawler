package analysis

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// Graph: home -> a, b (content links); nav -> c (navigation only);
// a -> b (content); c has no outlinks. d is an orphan.
func linkStructurePages() []*types.PageData {
	return []*types.PageData{
		{URL: "https://ex.com/", StatusCode: 200, InternalLinks: []string{"https://ex.com/a", "https://ex.com/b", "https://ex.com/nav"},
			LinkZones: &types.LinkZoneCounts{Content: 2, Navigation: 1}},
		{URL: "https://ex.com/a", StatusCode: 200, InternalLinks: []string{"https://ex.com/b"},
			LinkZones: &types.LinkZoneCounts{Content: 1, Footer: 2}},
		{URL: "https://ex.com/b", StatusCode: 200, LinkZones: &types.LinkZoneCounts{}},
		{URL: "https://ex.com/nav", StatusCode: 200, InternalLinks: []string{"https://ex.com/c"},
			LinkZones: &types.LinkZoneCounts{Navigation: 1, Breadcrumb: 1}},
		{URL: "https://ex.com/c", StatusCode: 200, LinkZones: &types.LinkZoneCounts{}},
		{URL: "https://ex.com/d", StatusCode: 200},
	}
}

func degreesOf(g *AdjacencyGraph) []int {
	in := make([]int, g.N)
	for _, j := range g.Edges {
		in[j]++
	}
	return in
}

func TestFillLinkStructureStats(t *testing.T) {
	pages := linkStructurePages()
	g := BuildAdjacencyList(pages)
	li := &types.LinkIntelligenceStats{LinkPositionDistribution: map[string]int{}}
	fillLinkStructureStats(li, pages, g, degreesOf(g))

	want := map[string]int{"content": 3, "navigation": 2, "footer": 2, "breadcrumb": 1}
	if len(li.LinkPositionDistribution) != len(want) {
		t.Fatalf("distribution = %v, want %v", li.LinkPositionDistribution, want)
	}
	for k, v := range want {
		if li.LinkPositionDistribution[k] != v {
			t.Errorf("distribution[%s] = %d, want %d", k, li.LinkPositionDistribution[k], v)
		}
	}

	// c is linked only from /nav, which has no content-area links.
	// /nav itself is linked from home, which has content links, so it may
	// have a content inlink and is not flagged. d is an orphan (no inlinks).
	if li.PagesWithNoContentLinksCount != 1 || len(li.PagesWithNoContentLinks) != 1 || li.PagesWithNoContentLinks[0] != "https://ex.com/c" {
		t.Errorf("pagesWithNoContentLinks = %v (count %d)", li.PagesWithNoContentLinks, li.PagesWithNoContentLinksCount)
	}

	if len(li.MostConnected) == 0 || li.MostConnected[0].URL != "https://ex.com/" || li.MostConnected[0].Score != 3 {
		t.Errorf("mostConnected = %+v", li.MostConnected)
	}
	for _, e := range li.MostConnected {
		if e.URL == "https://ex.com/d" {
			t.Errorf("orphan with degree 0 should not be in mostConnected")
		}
	}
	if len(li.MostIsolated) == 0 || li.MostIsolated[0].Score != 1 {
		t.Errorf("mostIsolated = %+v", li.MostIsolated)
	}
	for _, e := range li.MostIsolated {
		if e.URL == "https://ex.com/d" || e.URL == "https://ex.com/" {
			t.Errorf("mostIsolated must skip pages without inlinks, got %s", e.URL)
		}
	}
}

func TestFillLinkStructureStats_UnknownZonesAreConservative(t *testing.T) {
	pages := linkStructurePages()
	pages[3].LinkZones = nil // zones unknown for /nav: cannot prove c lacks content inlinks
	g := BuildAdjacencyList(pages)
	li := &types.LinkIntelligenceStats{}
	fillLinkStructureStats(li, pages, g, degreesOf(g))
	if li.PagesWithNoContentLinksCount != 0 {
		t.Errorf("expected no flagged pages when source zones are unknown, got %v", li.PagesWithNoContentLinks)
	}
	if li.LinkPositionDistribution["navigation"] != 1 {
		t.Errorf("distribution = %v", li.LinkPositionDistribution)
	}
}

func TestRunDeferredAnalysis_FillsLinkStructureStats(t *testing.T) {
	pages := linkStructurePages()
	stats := &types.CrawlStats{}
	RunDeferredAnalysis(t.Context(), pages, PostProcessConfig{LinkIntelligence: true, SeedURL: "https://ex.com/", LINoCentrality: true}, stats)
	li := stats.LinkIntelligenceStats
	if li == nil {
		t.Fatal("expected link intelligence stats")
	}
	if li.LinkPositionDistribution["content"] != 3 {
		t.Errorf("distribution = %v", li.LinkPositionDistribution)
	}
	if li.PagesWithNoContentLinksCount != 1 {
		t.Errorf("pagesWithNoContentLinksCount = %d", li.PagesWithNoContentLinksCount)
	}
	if len(li.MostConnected) == 0 || len(li.MostIsolated) == 0 {
		t.Errorf("mostConnected=%v mostIsolated=%v", li.MostConnected, li.MostIsolated)
	}
}
