package analysis

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestHasEdge(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{"https://example.com/b", "https://example.com/c"}},
		{URL: "https://example.com/b", InternalLinks: []string{"https://example.com/c"}},
		{URL: "https://example.com/c"},
	}
	graph := BuildAdjacencyList(pages)

	tests := []struct {
		name string
		src  int
		dst  int
		want bool
	}{
		{"a→b exists", 0, 1, true},
		{"a→c exists", 0, 2, true},
		{"b→c exists", 1, 2, true},
		{"b→a not exists", 1, 0, false},
		{"c→a not exists", 2, 0, false},
		{"a→a self-link", 0, 0, false},
		{"out of bounds negative", -1, 0, false},
		{"out of bounds high", 0, 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graph.HasEdge(tt.src, tt.dst)
			if got != tt.want {
				t.Errorf("HasEdge(%d, %d) = %v, want %v", tt.src, tt.dst, got, tt.want)
			}
		})
	}
}

func TestNeighbors(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{
			"https://example.com/b", "https://example.com/c",
		}},
		{URL: "https://example.com/b"},
		{URL: "https://example.com/c"},
	}
	graph := BuildAdjacencyList(pages)

	neighbors := graph.Neighbors(0)
	if len(neighbors) != 2 {
		t.Fatalf("Expected 2 neighbors, got %d", len(neighbors))
	}

	// Neighbors should contain indices for b(1) and c(2)
	has1, has2 := false, false
	for _, n := range neighbors {
		if n == 1 {
			has1 = true
		}
		if n == 2 {
			has2 = true
		}
	}
	if !has1 || !has2 {
		t.Errorf("Expected neighbors [1, 2], got %v", neighbors)
	}

	// Node with no outlinks
	neighbors = graph.Neighbors(1)
	if len(neighbors) != 0 {
		t.Errorf("Expected 0 neighbors for b, got %d", len(neighbors))
	}
}

func TestOutDegreeGraph(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{
			"https://example.com/b", "https://example.com/c", "https://example.com/d",
		}},
		{URL: "https://example.com/b", InternalLinks: []string{"https://example.com/a"}},
		{URL: "https://example.com/c"},
		{URL: "https://example.com/d"},
	}
	graph := BuildAdjacencyList(pages)

	tests := []struct {
		name string
		idx  int
		want int
	}{
		{"a has 3 outlinks", 0, 3},
		{"b has 1 outlink", 1, 1},
		{"c has 0 outlinks", 2, 0},
		{"d has 0 outlinks", 3, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := graph.OutDegree(tt.idx)
			if got != tt.want {
				t.Errorf("OutDegree(%d) = %d, want %d", tt.idx, got, tt.want)
			}
		})
	}
}

func TestBuildAdjacencyListDedupLinks(t *testing.T) {
	// Duplicate links should be deduplicated
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{
			"https://example.com/b",
			"https://example.com/b", // duplicate
			"https://example.com/b", // duplicate
		}},
		{URL: "https://example.com/b"},
	}
	graph := BuildAdjacencyList(pages)
	if graph.OutDegree(0) != 1 {
		t.Errorf("Expected 1 unique edge, got %d", graph.OutDegree(0))
	}
}

func TestBuildAdjacencyListSelfLink(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{"https://example.com/a"}},
	}
	graph := BuildAdjacencyList(pages)
	if graph.OutDegree(0) != 0 {
		t.Errorf("Self-links should be excluded, got OutDegree %d", graph.OutDegree(0))
	}
}

func TestBuildAdjacencyListFromDisk(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{"https://example.com/b", "https://example.com/c"}},
		{URL: "https://example.com/b", InternalLinks: []string{"https://example.com/c"}},
		{URL: "https://example.com/c"},
	}
	// Build in-memory graph for reference
	memGraph := BuildAdjacencyList(copyPages(pages))

	// Build disk-backed graph: iterator yields edges from pages, then nil out InternalLinks
	iter := func(fn func(source string, targets []string)) error {
		for _, p := range pages {
			if len(p.InternalLinks) > 0 {
				fn(p.URL, p.InternalLinks)
			}
		}
		return nil
	}
	stripped := make([]*types.PageData, len(pages))
	for i, p := range pages {
		stripped[i] = &types.PageData{URL: p.URL, StatusCode: p.StatusCode}
	}
	diskGraph := BuildAdjacencyListFromDisk(stripped, iter)

	// Both graphs should be identical
	if memGraph.N != diskGraph.N {
		t.Fatalf("N mismatch: mem=%d disk=%d", memGraph.N, diskGraph.N)
	}
	for i := 0; i < memGraph.N; i++ {
		if memGraph.OutDegree(i) != diskGraph.OutDegree(i) {
			t.Errorf("OutDegree(%d) mismatch: mem=%d disk=%d", i, memGraph.OutDegree(i), diskGraph.OutDegree(i))
		}
		memNeigh := memGraph.Neighbors(i)
		diskNeigh := diskGraph.Neighbors(i)
		if len(memNeigh) != len(diskNeigh) {
			t.Errorf("Neighbors(%d) length mismatch: mem=%d disk=%d", i, len(memNeigh), len(diskNeigh))
			continue
		}
		for j := range memNeigh {
			if memNeigh[j] != diskNeigh[j] {
				t.Errorf("Neighbors(%d)[%d] mismatch: mem=%d disk=%d", i, j, memNeigh[j], diskNeigh[j])
			}
		}
	}
}

func copyPages(pages []*types.PageData) []*types.PageData {
	out := make([]*types.PageData, len(pages))
	for i, p := range pages {
		cp := *p
		cp.InternalLinks = append([]string(nil), p.InternalLinks...)
		out[i] = &cp
	}
	return out
}

func TestBuildAdjacencyListTrailingSlashResolution(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", InternalLinks: []string{"https://example.com/b/"}},
		{URL: "https://example.com/b"},
	}
	graph := BuildAdjacencyList(pages)
	// Link "b/" should resolve to "b"
	if graph.OutDegree(0) != 1 {
		t.Errorf("Expected trailing-slash resolution, OutDegree = %d", graph.OutDegree(0))
	}
	if !graph.HasEdge(0, 1) {
		t.Error("Expected edge from a to b via trailing-slash resolution")
	}
}
