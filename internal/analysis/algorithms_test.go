package analysis

import (
	"math"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// ── CosineSimilarity additional tests ────────────────────

func TestCosineSimilarityDifferentLengths(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{1, 2}
	if sim := CosineSimilarity(a, b); sim != 0 {
		t.Errorf("Different lengths: sim = %f, want 0", sim)
	}
}

func TestCosineSimilarityZeroVector(t *testing.T) {
	a := []float64{0, 0, 0}
	b := []float64{1, 2, 3}
	if sim := CosineSimilarity(a, b); sim != 0 {
		t.Errorf("Zero vector: sim = %f, want 0", sim)
	}
}

func TestCosineSimilarityAntiParallel(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{-1, 0, 0}
	if sim := CosineSimilarity(a, b); math.Abs(sim-(-1.0)) > 0.001 {
		t.Errorf("Anti-parallel: sim = %f, want -1.0", sim)
	}
}

func TestCosineSimilarityKnownValues(t *testing.T) {
	// 45-degree angle → cos(45°) ≈ 0.7071
	a := []float64{1, 0}
	b := []float64{1, 1}
	expected := 1.0 / math.Sqrt(2)
	if sim := CosineSimilarity(a, b); math.Abs(sim-expected) > 0.001 {
		t.Errorf("45-degree: sim = %f, want %f", sim, expected)
	}
}

// ── normalizeToScale ─────────────────────────────────────

func TestNormalizeToScale(t *testing.T) {
	urls := []string{
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
	}
	raw := []float64{10, 5, 0}
	result := normalizeToScale(raw, urls, 10)

	if result["https://example.com/a"] != 10 {
		t.Errorf("a = %f, want 10", result["https://example.com/a"])
	}
	if result["https://example.com/b"] != 5 {
		t.Errorf("b = %f, want 5", result["https://example.com/b"])
	}
	if result["https://example.com/c"] != 0 {
		t.Errorf("c = %f, want 0", result["https://example.com/c"])
	}
}

func TestNormalizeToScaleAllZero(t *testing.T) {
	urls := []string{"https://example.com/a"}
	raw := []float64{0}
	result := normalizeToScale(raw, urls, 10)
	if result["https://example.com/a"] != 0 {
		t.Errorf("All-zero: a = %f, want 0", result["https://example.com/a"])
	}
}

// ── sampleSourceNodes ────────────────────────────────────

func TestSampleSourceNodesNoSampling(t *testing.T) {
	nodes := sampleSourceNodes(10, 5000)
	if len(nodes) != 10 {
		t.Errorf("Expected 10 nodes, got %d", len(nodes))
	}
	for i, n := range nodes {
		if n != i {
			t.Errorf("Node %d = %d, want %d", i, n, i)
		}
	}
}

func TestSampleSourceNodesSampling(t *testing.T) {
	nodes := sampleSourceNodes(10000, 5000)
	if len(nodes) != 500 {
		t.Errorf("Expected 500 sampled nodes, got %d", len(nodes))
	}
	for _, n := range nodes {
		if n < 0 || n >= 10000 {
			t.Errorf("Invalid node index: %d", n)
		}
	}
}

// ── findHomepageIndex ────────────────────────────────────

func TestFindHomepageIndexExact(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/"},
		{URL: "https://example.com/about"},
	}
	graph := BuildAdjacencyList(pages)
	idx := findHomepageIndex(graph, "https://example.com/")
	if idx != 0 {
		t.Errorf("Exact match: idx = %d, want 0", idx)
	}
}

func TestFindHomepageIndexTrailingSlash(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com"},
		{URL: "https://example.com/about"},
	}
	graph := BuildAdjacencyList(pages)
	idx := findHomepageIndex(graph, "https://example.com/")
	if idx != 0 {
		t.Errorf("Trailing slash: idx = %d, want 0", idx)
	}
}

func TestFindHomepageIndexTrailingSlashReverse(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/"},
		{URL: "https://example.com/about"},
	}
	graph := BuildAdjacencyList(pages)
	idx := findHomepageIndex(graph, "https://example.com")
	if idx != 0 {
		t.Errorf("Reverse trailing slash: idx = %d, want 0", idx)
	}
}

func TestFindHomepageIndexNoMatch(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://other.com/a", Depth: 1},
		{URL: "https://other.com/b", Depth: 2},
	}
	graph := BuildAdjacencyList(pages)
	idx := findHomepageIndex(graph, "https://nomatch.com/")
	if idx != -1 {
		t.Errorf("No match: idx = %d, want -1", idx)
	}
}

// ── ComputeClickDepth edge cases ─────────────────────────

func TestComputeClickDepthNoHomepage(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", Depth: 1},
		{URL: "https://example.com/b", Depth: 2},
	}
	graph := BuildAdjacencyList(pages)
	depths := ComputeClickDepth("https://nomatch.com/", graph)
	if len(depths) != 0 {
		t.Errorf("No homepage: expected empty map, got %d entries", len(depths))
	}
}

func TestComputeClickDepthDisconnected(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/", InternalLinks: []string{"https://example.com/a"}, Depth: 0},
		{URL: "https://example.com/a", Depth: 1},
		{URL: "https://example.com/isolated", Depth: 1},
	}
	graph := BuildAdjacencyList(pages)
	depths := ComputeClickDepth("https://example.com/", graph)
	if _, ok := depths["https://example.com/isolated"]; ok {
		t.Error("Isolated page should not be reachable")
	}
	if depths["https://example.com/a"] != 1 {
		t.Errorf("Page a depth = %d, want 1", depths["https://example.com/a"])
	}
}

// ── ComputeHits edge cases ───────────────────────────────

func TestComputeHitsEmpty(t *testing.T) {
	scores := ComputeHits(0, 0, nil)
	if len(scores) != 0 {
		t.Errorf("Empty pages: expected 0 scores, got %d", len(scores))
	}
}

func TestComputeHitsSingleNode(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/"},
	}
	graph := BuildAdjacencyList(pages)
	scores := ComputeHits(50, 0.0001, graph)
	if len(scores) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(scores))
	}
}

func TestComputeHitsStarGraph(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/hub", InternalLinks: []string{
			"https://example.com/a",
			"https://example.com/b",
			"https://example.com/c",
			"https://example.com/d",
		}},
		{URL: "https://example.com/a"},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
		{URL: "https://example.com/d"},
	}
	graph := BuildAdjacencyList(pages)
	scores := ComputeHits(50, 0.0001, graph)

	hubScore := scores["https://example.com/hub"]
	if hubScore.HubScore != 10 {
		t.Errorf("Hub score = %f, want 10", hubScore.HubScore)
	}

	aAuth := scores["https://example.com/a"].AuthorityScore
	bAuth := scores["https://example.com/b"].AuthorityScore
	if math.Abs(aAuth-bAuth) > 0.01 {
		t.Errorf("Targets should have equal authority: a=%f, b=%f", aAuth, bAuth)
	}
}

// ── ComputeBetweennessCentrality edge cases ──────────────

func TestBetweennessCentralityEmpty(t *testing.T) {
	bc := ComputeBetweennessCentrality(0, nil)
	if len(bc) != 0 {
		t.Errorf("Expected empty map, got %d entries", len(bc))
	}
}

func TestBetweennessCentralitySingleNode(t *testing.T) {
	pages := []*types.PageData{{URL: "https://example.com/"}}
	graph := BuildAdjacencyList(pages)
	bc := ComputeBetweennessCentrality(5000, graph)
	if bc["https://example.com/"] != 0 {
		t.Errorf("Single node BC = %f, want 0", bc["https://example.com/"])
	}
}

// ── ComputeClosenessCentrality edge cases ────────────────

func TestClosenessCentralityEmpty(t *testing.T) {
	cc := ComputeClosenessCentrality(0, nil)
	if len(cc) != 0 {
		t.Errorf("Expected empty map, got %d entries", len(cc))
	}
}
