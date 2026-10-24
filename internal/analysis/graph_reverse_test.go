package analysis

import (
	"slices"
	"sync"
	"testing"
)

// naiveInlinks scans every edge, the way per-click handlers used to.
func naiveInlinks(g *AdjacencyGraph, dst int) []int32 {
	var out []int32
	for src := 0; src < g.N; src++ {
		if src != dst && g.HasEdge(src, dst) {
			out = append(out, int32(src))
		}
	}
	return out
}

func TestReverseIndexMatchesNaiveScan(t *testing.T) {
	g := BuildAdjacencyList(syntheticCrawl(3000, 15))
	if len(g.Edges) == 0 {
		t.Fatal("empty graph")
	}
	in := g.InDegree()
	for dst := 0; dst < g.N; dst++ {
		want := naiveInlinks(g, dst)
		got := g.InNeighbors(dst)
		if !slices.Equal(got, want) {
			t.Fatalf("node %d: InNeighbors=%v want %v", dst, got, want)
		}
		if int(in[dst]) != len(want) {
			t.Fatalf("node %d: InDegree=%d want %d", dst, in[dst], len(want))
		}
	}
}

// Self-links never count as inlinks, even in hand-built graphs.
func TestInDegreeIgnoresSelfLinks(t *testing.T) {
	g := &AdjacencyGraph{
		N:       3,
		URLs:    []string{"a", "b", "c"},
		Offsets: []int32{0, 2, 3, 4},
		Edges:   []int32{0, 1, 1, 0}, // a->a (self), a->b, b->b (self), c->a
	}
	if got := g.InDegree(); !slices.Equal(got, []int32{1, 1, 0}) {
		t.Errorf("InDegree = %v, want [1 1 0]", got)
	}
	if got := g.InNeighbors(1); !slices.Equal(got, []int32{0}) {
		t.Errorf("InNeighbors(b) = %v, want [0]", got)
	}
}

func TestReverseIndexConcurrentFirstUse(t *testing.T) {
	g := BuildAdjacencyList(syntheticCrawl(2000, 10))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = g.InNeighbors(i)
			_ = g.InDegree()
		}(i)
	}
	wg.Wait()
	if g.Reverse() != g.Reverse() {
		t.Error("reverse index not cached")
	}
}

// BenchmarkInlinksPerClick compares one node's inlinks via a full edge scan
// (old per-click universe path) with the cached reverse index.
func BenchmarkInlinksPerClick(b *testing.B) {
	g := BuildAdjacencyList(syntheticCrawl(200000, 40))
	b.Run("scan", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pi := int32(i % g.N)
			n := 0
			for src := 0; src < g.N; src++ {
				for _, t := range g.Edges[g.Offsets[src]:g.Offsets[src+1]] {
					if t == pi {
						n++
						break
					}
				}
			}
			_ = n
		}
	})
	g.Reverse()
	b.Run("reverse", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = len(g.InNeighbors(i % g.N))
		}
	})
}
