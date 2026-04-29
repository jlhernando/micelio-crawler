package analysis

import (
	"strconv"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// ── GenerateLinkSuggestions ──────────────────────────────

func TestGenerateLinkSuggestionsEmpty(t *testing.T) {
	suggestions := GenerateLinkSuggestions(nil, nil, nil, nil, nil, nil, nil, LinkSuggestionsOptions{})
	if len(suggestions) != 0 {
		t.Errorf("Expected 0 suggestions, got %d", len(suggestions))
	}
}

func TestGenerateLinkSuggestionsBasic(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/source", StatusCode: 200, WordCount: 500,
			Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://example.com/target", StatusCode: 200, WordCount: 100,
			Indexability: types.IndexabilityData{Indexable: true}},
	}
	graph := BuildAdjacencyList(pages)
	inDegree := make([]int, graph.N)
	for _, j := range graph.Edges {
		inDegree[j]++
	}
	wordCounts := []int32{500, 100}
	indexable := []bool{true, true}

	clickDepths := map[string]int{
		"https://example.com/source": 1,
		"https://example.com/target": 5,
	}
	pageRanks := map[string]float64{
		"https://example.com/source": 5.0,
		"https://example.com/target": 8.0,
	}

	suggestions := GenerateLinkSuggestions(
		graph, nil, clickDepths, pageRanks, inDegree, wordCounts, indexable,
		LinkSuggestionsOptions{MaxSuggestions: 10},
	)

	if len(suggestions) == 0 {
		t.Fatal("Expected at least 1 suggestion")
	}
	if suggestions[0].SourceURL != "https://example.com/source" {
		t.Errorf("Source = %q, want source page", suggestions[0].SourceURL)
	}
	if suggestions[0].TargetURL != "https://example.com/target" {
		t.Errorf("Target = %q, want target page", suggestions[0].TargetURL)
	}
	if suggestions[0].Score <= 0 {
		t.Errorf("Score = %f, want > 0", suggestions[0].Score)
	}
}

func TestGenerateLinkSuggestionsSkipsExistingLinks(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/source", StatusCode: 200, WordCount: 500,
			Indexability: types.IndexabilityData{Indexable: true},
			InternalLinks: []string{"https://example.com/target"}},
		{URL: "https://example.com/target", StatusCode: 200, WordCount: 100,
			Indexability: types.IndexabilityData{Indexable: true}},
	}
	graph := BuildAdjacencyList(pages)
	inDegree := make([]int, graph.N)
	for _, j := range graph.Edges {
		inDegree[j]++
	}
	wordCounts := []int32{500, 100}
	indexable := []bool{true, true}
	pageRanks := map[string]float64{"https://example.com/target": 8.0}

	suggestions := GenerateLinkSuggestions(
		graph, nil, nil, pageRanks, inDegree, wordCounts, indexable,
		LinkSuggestionsOptions{MaxSuggestions: 10},
	)

	for _, s := range suggestions {
		if s.SourceURL == "https://example.com/source" && s.TargetURL == "https://example.com/target" {
			t.Error("Should not suggest existing link")
		}
	}
}

func TestGenerateLinkSuggestionsSkipsNonIndexable(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/source", StatusCode: 200, WordCount: 500,
			Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://example.com/noindex", StatusCode: 200, WordCount: 100,
			Indexability: types.IndexabilityData{Indexable: false, Reason: "noindex"}},
	}
	graph := BuildAdjacencyList(pages)
	inDegree := make([]int, graph.N)
	wordCounts := []int32{500, 100}
	indexable := []bool{true, false}
	pageRanks := map[string]float64{"https://example.com/noindex": 8.0}

	suggestions := GenerateLinkSuggestions(
		graph, nil, nil, pageRanks, inDegree, wordCounts, indexable,
		LinkSuggestionsOptions{MaxSuggestions: 10},
	)

	for _, s := range suggestions {
		if s.TargetURL == "https://example.com/noindex" {
			t.Error("Should not suggest linking to non-indexable page")
		}
	}
}

func TestGenerateLinkSuggestionsWithEmbeddings(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/source", StatusCode: 200, WordCount: 500,
			Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://example.com/related", StatusCode: 200, WordCount: 100,
			Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://example.com/unrelated", StatusCode: 200, WordCount: 100,
			Indexability: types.IndexabilityData{Indexable: true}},
	}
	graph := BuildAdjacencyList(pages)
	inDegree := make([]int, graph.N)
	wordCounts := []int32{500, 100, 100}
	indexable := []bool{true, true, true}
	pageRanks := map[string]float64{
		"https://example.com/related":   5.0,
		"https://example.com/unrelated": 5.0,
	}

	embeddings := map[string][]float64{
		"https://example.com/source":    {1, 0, 0},
		"https://example.com/related":   {0.6, 0.8, 0},
		"https://example.com/unrelated": {0, 0.1, 1},
	}

	suggestions := GenerateLinkSuggestions(
		graph, embeddings, nil, pageRanks, inDegree, wordCounts, indexable,
		LinkSuggestionsOptions{
			MaxSuggestions:        10,
			MinSemanticSimilarity: 0.3,
			MaxSemanticSimilarity: 0.85,
			TargetMaxInDegree:     3,
			SourceMinWordCount:    200,
		},
	)

	foundRelated := false
	foundUnrelated := false
	for _, s := range suggestions {
		if s.TargetURL == "https://example.com/related" {
			foundRelated = true
			if s.Signals.SemanticSimilarity == nil {
				t.Error("Expected semantic similarity signal")
			}
		}
		if s.TargetURL == "https://example.com/unrelated" {
			foundUnrelated = true
		}
	}
	if !foundRelated {
		t.Error("Expected suggestion for related page")
	}
	if foundUnrelated {
		t.Error("Should not suggest unrelated page (similarity too low)")
	}
}

// ── buildSuggestionReason ────────────────────────────────

func TestBuildSuggestionReason(t *testing.T) {
	tests := []struct {
		name           string
		similarity     *float64
		inDegree       int
		clickDepth     *int
		depthReduction *int
		pageRank       float64
		wantContains   []string
	}{
		{
			name:         "no signals",
			inDegree:     5,
			wantContains: []string{"potential linking opportunity"},
		},
		{
			name:         "high similarity",
			similarity:   floatPtr(0.65),
			inDegree:     5,
			wantContains: []string{"topically related", "65%"},
		},
		{
			name:         "single inlink",
			inDegree:     1,
			wantContains: []string{"only 1 inlink"},
		},
		{
			name:         "zero inlinks",
			inDegree:     0,
			wantContains: []string{"only 0 inlinks"},
		},
		{
			name:         "few inlinks",
			inDegree:     2,
			wantContains: []string{"only 2 inlinks"},
		},
		{
			name:           "depth reduction",
			inDegree:       5,
			depthReduction: intPtr(3),
			wantContains:   []string{"reduce click depth by 3"},
		},
		{
			name:         "deep target",
			inDegree:     5,
			clickDepth:   intPtr(5),
			wantContains: []string{"5 clicks deep"},
		},
		{
			name:         "high pagerank",
			inDegree:     5,
			pageRank:     7.5,
			wantContains: []string{"high-value target", "PR 7.5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason := buildSuggestionReason(tt.similarity, tt.inDegree, tt.clickDepth, tt.depthReduction, tt.pageRank)
			for _, want := range tt.wantContains {
				if !strings.Contains(reason, want) {
					t.Errorf("reason = %q, want to contain %q", reason, want)
				}
			}
		})
	}
}

// ── AnalyzeSemanticLinkDistance ───────────────────────────

func TestAnalyzeSemanticLinkDistanceBasic(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{
			"https://example.com/b", "https://example.com/c",
		}},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
	}
	graph := BuildAdjacencyList(pages)

	embeddings := map[string][]float64{
		"https://example.com/a": {1, 0, 0},
		"https://example.com/b": {0.99, 0.01, 0},
		"https://example.com/c": {0, 0, 1},
	}

	analysis := AnalyzeSemanticLinkDistance(embeddings, graph, 0.15, 0.6)

	if analysis.TotalLinks != 2 {
		t.Errorf("TotalLinks = %d, want 2", analysis.TotalLinks)
	}
	if len(analysis.StrongLinks) == 0 {
		t.Error("Expected at least 1 strong link (a->b)")
	}
}

func TestAnalyzeSemanticLinkDistanceNoEmbeddings(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{"https://example.com/b"}},
		{URL: "https://example.com/b"},
	}
	graph := BuildAdjacencyList(pages)

	analysis := AnalyzeSemanticLinkDistance(map[string][]float64{}, graph, 0, 0)
	if analysis.TotalLinks != 0 {
		t.Errorf("Expected 0 links with no embeddings, got %d", analysis.TotalLinks)
	}
}

// ── DetectNearOrphans edge cases ─────────────────────────

func TestDetectNearOrphansHighInDegree(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/popular", StatusCode: 200},
		{URL: "https://example.com/a", StatusCode: 200, InternalLinks: []string{"https://example.com/popular"}},
		{URL: "https://example.com/b", StatusCode: 200, InternalLinks: []string{"https://example.com/popular"}},
		{URL: "https://example.com/c", StatusCode: 200, InternalLinks: []string{"https://example.com/popular"}},
	}
	graph := BuildAdjacencyList(pages)
	inDeg := make([]int, graph.N)
	for _, j := range graph.Edges {
		inDeg[j]++
	}
	clickDepths := map[string]int{
		"https://example.com/popular": 1,
		"https://example.com/a":       5,
		"https://example.com/b":       5,
		"https://example.com/c":       5,
	}

	orphans := DetectNearOrphans(clickDepths, graph, inDeg, 2, 4)
	for _, o := range orphans {
		if o.URL == "https://example.com/popular" {
			t.Error("Popular page with 3 inlinks should not be a near-orphan")
		}
	}
}

func TestDetectNearOrphansNon200(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/gone", StatusCode: 404},
		{URL: "https://example.com/linker", StatusCode: 200, InternalLinks: []string{"https://example.com/gone"}},
	}
	graph := BuildAdjacencyList(pages)
	inDeg := make([]int, graph.N)
	for _, j := range graph.Edges {
		inDeg[j]++
	}
	clickDepths := map[string]int{
		"https://example.com/linker": 5,
	}

	orphans := DetectNearOrphans(clickDepths, graph, inDeg, 2, 4)
	if len(orphans) != 0 {
		t.Errorf("Expected 0 near-orphans (404 excluded), got %d", len(orphans))
	}
}

// ── ComputeLinkDilution edge cases ───────────────────────

func TestLinkDilutionHighWarning(t *testing.T) {
	targets := make([]*types.PageData, 150)
	links := make([]string, 150)
	for i := range links {
		url := "https://example.com/page-" + strconv.Itoa(i)
		links[i] = url
		targets[i] = &types.PageData{URL: url, StatusCode: 200}
	}
	pages := append([]*types.PageData{
		{URL: "https://example.com/hub", InternalLinks: links, StatusCode: 200},
	}, targets...)
	graph := BuildAdjacencyList(pages)
	warnings := ComputeLinkDilution(graph)
	if len(warnings) != 1 {
		t.Fatalf("Expected 1 warning, got %d", len(warnings))
	}
	if warnings[0].Warning != "high" {
		t.Errorf("Warning = %q, want 'high'", warnings[0].Warning)
	}
}

func TestLinkDilutionNoWarning(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/normal", StatusCode: 200, InternalLinks: []string{"https://example.com/a"}},
		{URL: "https://example.com/a", StatusCode: 200},
	}
	graph := BuildAdjacencyList(pages)
	warnings := ComputeLinkDilution(graph)
	if len(warnings) != 0 {
		t.Errorf("Expected 0 warnings, got %d", len(warnings))
	}
}

// ── DefaultLinkSuggestionsOptions ────────────────────────

func TestDefaultLinkSuggestionsOptions(t *testing.T) {
	opts := DefaultLinkSuggestionsOptions()
	if opts.MaxSuggestions != 50 {
		t.Errorf("MaxSuggestions = %d, want 50", opts.MaxSuggestions)
	}
	if opts.MinSemanticSimilarity != 0.3 {
		t.Errorf("MinSemanticSimilarity = %f, want 0.3", opts.MinSemanticSimilarity)
	}
	if opts.MaxSemanticSimilarity != 0.85 {
		t.Errorf("MaxSemanticSimilarity = %f, want 0.85", opts.MaxSemanticSimilarity)
	}
	if opts.TargetMaxInDegree != 3 {
		t.Errorf("TargetMaxInDegree = %d, want 3", opts.TargetMaxInDegree)
	}
	if opts.SourceMinWordCount != 200 {
		t.Errorf("SourceMinWordCount = %d, want 200", opts.SourceMinWordCount)
	}
}

// ── Helpers ──────────────────────────────────────────────

func floatPtr(f float64) *float64 { return &f }
func intPtr(i int) *int           { return &i }
