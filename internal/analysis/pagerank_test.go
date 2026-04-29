package analysis

import (
	"math"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestPageRankEmpty(t *testing.T) {
	result := ComputePageRank(nil, DefaultPageRankOptions())
	if len(result) != 0 {
		t.Errorf("Expected empty map, got %d entries", len(result))
	}
}

func TestPageRankSinglePage(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/"},
	}
	result := ComputePageRank(pages, DefaultPageRankOptions())
	if len(result) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(result))
	}
	// Single page should get score 10 (max normalized)
	if result["https://example.com/"] != 10 {
		t.Errorf("Single page score = %f, want 10", result["https://example.com/"])
	}
}

func TestPageRankLinearChain(t *testing.T) {
	// A -> B -> C: A should have lowest rank, C highest (as sink/dangling)
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{"https://example.com/b"}},
		{URL: "https://example.com/b", InternalLinks: []string{"https://example.com/c"}},
		{URL: "https://example.com/c"},
	}
	result := ComputePageRank(pages, DefaultPageRankOptions())

	if len(result) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(result))
	}

	// C (sink) should have highest score (10.0)
	if result["https://example.com/c"] != 10 {
		t.Errorf("C score = %f, want 10", result["https://example.com/c"])
	}
	// A should have lowest
	if result["https://example.com/a"] >= result["https://example.com/b"] {
		t.Errorf("A (%f) should be < B (%f)", result["https://example.com/a"], result["https://example.com/b"])
	}
	if result["https://example.com/b"] >= result["https://example.com/c"] {
		t.Errorf("B (%f) should be < C (%f)", result["https://example.com/b"], result["https://example.com/c"])
	}
}

func TestPageRankCycle(t *testing.T) {
	// Complete cycle: A -> B -> C -> A: all should have equal rank
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{"https://example.com/b"}},
		{URL: "https://example.com/b", InternalLinks: []string{"https://example.com/c"}},
		{URL: "https://example.com/c", InternalLinks: []string{"https://example.com/a"}},
	}
	result := ComputePageRank(pages, DefaultPageRankOptions())

	// All scores should be approximately equal (10.0 after normalization)
	for url, score := range result {
		if math.Abs(score-10) > 0.01 {
			t.Errorf("%s score = %f, want ~10", url, score)
		}
	}
}

func TestPageRankStarTopology(t *testing.T) {
	// Hub links to all spokes. Hub should have lower rank than the most-linked page.
	pages := []*types.PageData{
		{URL: "https://example.com/hub", InternalLinks: []string{
			"https://example.com/a",
			"https://example.com/b",
			"https://example.com/c",
		}},
		{URL: "https://example.com/a"},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
	}
	result := ComputePageRank(pages, DefaultPageRankOptions())

	// All spokes should have equal scores (all dangling, all receive same from hub)
	scoreA := result["https://example.com/a"]
	scoreB := result["https://example.com/b"]
	scoreC := result["https://example.com/c"]
	if math.Abs(scoreA-scoreB) > 0.01 || math.Abs(scoreB-scoreC) > 0.01 {
		t.Errorf("Spokes should have equal scores: a=%f, b=%f, c=%f", scoreA, scoreB, scoreC)
	}
}

func TestPageRankSelfLinksIgnored(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{"https://example.com/a", "https://example.com/b"}},
		{URL: "https://example.com/b"},
	}
	result := ComputePageRank(pages, DefaultPageRankOptions())
	// B should score 10 (highest), self-links on A shouldn't inflate A's score
	if result["https://example.com/b"] != 10 {
		t.Errorf("B score = %f, want 10", result["https://example.com/b"])
	}
}
