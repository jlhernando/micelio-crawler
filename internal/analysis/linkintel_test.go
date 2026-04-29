package analysis

import (
	"fmt"
	"math"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func makeTestPages() []*types.PageData {
	return []*types.PageData{
		{URL: "https://example.com/", InternalLinks: []string{
			"https://example.com/about",
			"https://example.com/blog",
			"https://example.com/contact",
		}, StatusCode: 200, Depth: 0, Indexability: types.IndexabilityData{Indexable: true}, WordCount: 500},
		{URL: "https://example.com/about", InternalLinks: []string{
			"https://example.com/",
			"https://example.com/blog",
		}, StatusCode: 200, Depth: 1, Indexability: types.IndexabilityData{Indexable: true}, WordCount: 300},
		{URL: "https://example.com/blog", InternalLinks: []string{
			"https://example.com/",
			"https://example.com/blog/post-1",
		}, StatusCode: 200, Depth: 1, Indexability: types.IndexabilityData{Indexable: true}, WordCount: 400},
		{URL: "https://example.com/blog/post-1", InternalLinks: []string{
			"https://example.com/blog",
		}, StatusCode: 200, Depth: 2, Indexability: types.IndexabilityData{Indexable: true}, WordCount: 1000},
		{URL: "https://example.com/contact", StatusCode: 200, Depth: 1, Indexability: types.IndexabilityData{Indexable: true}, WordCount: 100},
	}
}

func TestBuildAdjacencyList(t *testing.T) {
	pages := makeTestPages()
	graph := BuildAdjacencyList(pages)

	if graph.N != 5 {
		t.Fatalf("N = %d, want 5", graph.N)
	}
	if graph.OutDegree(0) != 3 {
		t.Errorf("Homepage outlinks = %d, want 3", graph.OutDegree(0))
	}
	if graph.OutDegree(4) != 0 {
		t.Errorf("Contact outlinks = %d, want 0", graph.OutDegree(4))
	}
	// Verify URLs populated
	if graph.URLs[0] != "https://example.com/" {
		t.Errorf("URLs[0] = %q, want homepage", graph.URLs[0])
	}
	// Verify StatusCodes populated
	if graph.StatusCodes[0] != 200 {
		t.Errorf("StatusCodes[0] = %d, want 200", graph.StatusCodes[0])
	}
}

func TestClickDepth(t *testing.T) {
	pages := makeTestPages()
	graph := BuildAdjacencyList(pages)
	depths := ComputeClickDepth("https://example.com/", graph)

	if depths["https://example.com/"] != 0 {
		t.Errorf("Homepage depth = %d, want 0", depths["https://example.com/"])
	}
	if depths["https://example.com/about"] != 1 {
		t.Errorf("About depth = %d, want 1", depths["https://example.com/about"])
	}
	if depths["https://example.com/blog"] != 1 {
		t.Errorf("Blog depth = %d, want 1", depths["https://example.com/blog"])
	}
	if depths["https://example.com/blog/post-1"] != 2 {
		t.Errorf("Post depth = %d, want 2", depths["https://example.com/blog/post-1"])
	}
	if depths["https://example.com/contact"] != 1 {
		t.Errorf("Contact depth = %d, want 1", depths["https://example.com/contact"])
	}
}

func TestClickDepthEmpty(t *testing.T) {
	depths := ComputeClickDepth("https://example.com/", nil)
	if len(depths) != 0 {
		t.Errorf("Expected empty map, got %d", len(depths))
	}
}

func TestHits(t *testing.T) {
	pages := makeTestPages()
	graph := BuildAdjacencyList(pages)
	scores := ComputeHits(50, 0.0001, graph)

	if len(scores) != 5 {
		t.Fatalf("Expected 5 entries, got %d", len(scores))
	}

	homeScores := scores["https://example.com/"]
	if homeScores.HubScore < 5 {
		t.Errorf("Homepage hub score = %f, expected > 5", homeScores.HubScore)
	}

	aboutScores := scores["https://example.com/about"]
	if aboutScores.AuthorityScore == 0 {
		t.Error("About authority score should be > 0")
	}
}

func TestLinkDilution(t *testing.T) {
	// 300 outlinks: equityPerLink = 1/log2(301) ≈ 0.121 → "excessive"
	targets := make([]*types.PageData, 300)
	links := make([]string, 300)
	for i := range links {
		url := fmt.Sprintf("https://example.com/page-%d", i)
		links[i] = url
		targets[i] = &types.PageData{URL: url, StatusCode: 200}
	}
	pages := append([]*types.PageData{
		{URL: "https://example.com/mega", InternalLinks: links, StatusCode: 200},
	}, targets...)
	graph := BuildAdjacencyList(pages)
	warnings := ComputeLinkDilution(graph)
	if len(warnings) != 1 {
		t.Fatalf("Expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Warning != "excessive" {
		t.Errorf("Warning = %q, want 'excessive'", warnings[0].Warning)
	}
	if warnings[0].EquityPerLink <= 0 {
		t.Error("EquityPerLink should be > 0")
	}
}

func TestBetweennessCentrality(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{"https://example.com/b"}},
		{URL: "https://example.com/b", InternalLinks: []string{"https://example.com/c"}},
		{URL: "https://example.com/c", InternalLinks: []string{"https://example.com/d"}},
		{URL: "https://example.com/d"},
	}
	graph := BuildAdjacencyList(pages)
	bc := ComputeBetweennessCentrality(5000, graph)

	if len(bc) != 4 {
		t.Fatalf("Expected 4 entries, got %d", len(bc))
	}

	if bc["https://example.com/b"] <= bc["https://example.com/a"] {
		t.Errorf("B (%f) should have higher BC than A (%f)", bc["https://example.com/b"], bc["https://example.com/a"])
	}
}

func TestClosenessCentrality(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/center", InternalLinks: []string{
			"https://example.com/a", "https://example.com/b", "https://example.com/c",
		}},
		{URL: "https://example.com/a"},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
	}
	graph := BuildAdjacencyList(pages)
	cc := ComputeClosenessCentrality(5000, graph)

	if cc["https://example.com/center"] != 10 {
		t.Errorf("Center closeness = %f, want 10", cc["https://example.com/center"])
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{1, 0, 0}
	if sim := CosineSimilarity(a, b); math.Abs(sim-1.0) > 0.001 {
		t.Errorf("Same vectors: sim = %f, want 1.0", sim)
	}

	c := []float64{0, 1, 0}
	if sim := CosineSimilarity(a, c); math.Abs(sim) > 0.001 {
		t.Errorf("Orthogonal: sim = %f, want 0.0", sim)
	}

	if sim := CosineSimilarity(nil, nil); sim != 0 {
		t.Errorf("Empty: sim = %f, want 0", sim)
	}
}

func TestNearOrphans(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/deep", StatusCode: 200},
		{URL: "https://example.com/linker", StatusCode: 200, InternalLinks: []string{"https://example.com/deep"}},
	}
	clickDepths := map[string]int{
		"https://example.com/deep":   1,
		"https://example.com/linker": 5,
	}
	graph := BuildAdjacencyList(pages)
	inDeg := make([]int, graph.N)
	for _, j := range graph.Edges {
		inDeg[j]++
	}

	orphans := DetectNearOrphans(clickDepths, graph, inDeg, 2, 4)
	if len(orphans) != 1 {
		t.Fatalf("Expected 1 near-orphan, got %d", len(orphans))
	}
	if orphans[0].URL != "https://example.com/deep" {
		t.Errorf("Near-orphan URL = %q, want 'https://example.com/deep'", orphans[0].URL)
	}
}
