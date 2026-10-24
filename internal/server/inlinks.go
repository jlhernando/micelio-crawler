package server

import (
	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// inlinkIndex answers "which pages link to this URL?". It reads the link
// graph (pages reloaded from a crawl DB, or stripped during the crawl, no
// longer carry InternalLinks). Counts come from the graph's cached
// in-degree; sources from its reverse CSR, built on the first Sources call
// and shared with the Universe view (4 bytes per edge, vs ~16 for a
// target→sources map). Self-links never count as inlinks. The legacy map is
// used only when no graph exists and pages still carry InternalLinks.
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
	return &inlinkIndex{graph: g, inDegree: g.InDegree()}
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
	in := ix.graph.InNeighbors(dst)
	out := make([]string, len(in))
	for k, src := range in {
		out[k] = ix.graph.URLs[src]
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
