package server

import (
	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// inlinkIndex answers "which pages link to this URL?". It reads the link
// graph (pages reloaded from a crawl DB, or stripped during the crawl, no
// longer carry InternalLinks). Sources are found on demand with a binary
// search per node, so the only extra memory is one in-degree per page; a
// target→sources map would cost ~16 bytes per edge (hundreds of MB at 700K
// pages). The legacy map is used only when no graph exists and pages still
// carry InternalLinks.
type inlinkIndex struct {
	graph    *analysis.AdjacencyGraph
	inDegree []int32
	legacy   map[string][]string
}

// newGraphInlinkIndex builds an inlink index from the link graph.
func newGraphInlinkIndex(g *analysis.AdjacencyGraph) *inlinkIndex {
	if g == nil {
		return nil
	}
	inDegree := make([]int32, g.N)
	for i := 0; i < g.N; i++ {
		for _, t := range g.Neighbors(i) {
			if int(t) != i && int(t) < g.N {
				inDegree[t]++
			}
		}
	}
	return &inlinkIndex{graph: g, inDegree: inDegree}
}

// newInlinkIndex prefers the graph and falls back to pages' InternalLinks.
func newInlinkIndex(g *analysis.AdjacencyGraph, pages []*types.PageData) *inlinkIndex {
	if g != nil {
		return newGraphInlinkIndex(g)
	}
	return buildInlinkIndex(pages)
}

// buildInlinkIndex creates a target URL → source URLs index from pages'
// InternalLinks (legacy path for pages that still carry their links).
func buildInlinkIndex(pages []*types.PageData) *inlinkIndex {
	idx := make(map[string][]string, len(pages))
	for _, p := range pages {
		seen := make(map[string]bool, len(p.InternalLinks))
		for _, link := range p.InternalLinks {
			if !seen[link] {
				seen[link] = true
				idx[link] = append(idx[link], p.URL)
			}
		}
	}
	return &inlinkIndex{legacy: idx}
}

// node resolves a URL (or its redirect target) to a graph node.
func (ix *inlinkIndex) node(u string) (int, bool) {
	if ix == nil || ix.graph == nil {
		return 0, false
	}
	i, ok := ix.graph.URLIndex[u]
	return i, ok && i < ix.graph.N
}

// Sources returns the URLs of pages linking to u.
func (ix *inlinkIndex) Sources(u string) []string {
	if ix == nil {
		return nil
	}
	if ix.graph == nil {
		return ix.legacy[u]
	}
	dst, ok := ix.node(u)
	if !ok || ix.inDegree[dst] == 0 {
		return nil
	}
	out := make([]string, 0, ix.inDegree[dst])
	for src := 0; src < ix.graph.N && len(out) < int(ix.inDegree[dst]); src++ {
		if src != dst && ix.graph.HasEdge(src, dst) {
			out = append(out, ix.graph.URLs[src])
		}
	}
	return out
}

// Count returns the number of distinct pages linking to u.
func (ix *inlinkIndex) Count(u string) int {
	if ix == nil {
		return 0
	}
	if ix.graph == nil {
		return len(ix.legacy[u])
	}
	if dst, ok := ix.node(u); ok {
		return int(ix.inDegree[dst])
	}
	return 0
}

// assignInlinks sets each page's Inlinks count from the index.
func assignInlinks(pages []*types.PageData, ix *inlinkIndex) {
	if ix == nil {
		return
	}
	if ix.graph == nil && len(ix.legacy) == 0 {
		return
	}
	for _, p := range pages {
		if n := ix.Count(p.URL); n > 0 || ix.graph != nil {
			p.Inlinks = n
		}
	}
}
