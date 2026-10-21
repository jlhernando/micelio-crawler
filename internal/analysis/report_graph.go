package analysis

import "github.com/micelio/micelio/internal/types"

// maxBrokenLinkSources caps the FoundOn list per broken URL.
const maxBrokenLinkSources = 10

// maxLinksToNonIndexable caps LinkAnalysis.LinksToNonIndexable.
const maxLinksToNonIndexable = 50

// buildReportGraph builds the link graph used by the reporter. Pages that
// still carry InternalLinks are read from memory; otherwise the disk iterator
// (when available) supplies the edges.
func buildReportGraph(pages []*types.PageData, iter func(fn func(source string, targets []string)) error) *AdjacencyGraph {
	if iter != nil && !anyInternalLinks(pages) {
		return BuildAdjacencyListFromDisk(pages, iter)
	}
	return BuildAdjacencyList(pages)
}

// graphInDegree returns the number of distinct pages linking to each node.
func graphInDegree(g *AdjacencyGraph) []int32 {
	if g == nil {
		return nil
	}
	inDegree := make([]int32, g.N)
	for _, j := range g.Edges {
		if int(j) < g.N {
			inDegree[j]++
		}
	}
	return inDegree
}

// graphHasLinkData reports whether the graph reflects the crawl's links. A
// graph with no edges while pages report internal links means the link data
// was not available (stripped, no disk iterator); orphan detection would then
// flag every page.
func graphHasLinkData(g *AdjacencyGraph, pages []*types.PageData) bool {
	if g != nil && len(g.Edges) > 0 {
		return true
	}
	for _, p := range pages {
		if p.InternalLinkCount > 0 {
			return false
		}
	}
	return true
}

// graphOrphanPages lists 200 pages with zero graph in-degree, excluding the
// seed (or depth-0 pages when no seed is known). Node i is pages[i].
func graphOrphanPages(g *AdjacencyGraph, inDegree []int32, pages []*types.PageData, seedURL string) []string {
	if g == nil {
		return nil
	}
	seedNorm := normalizeTrailingSlash(seedURL)
	var orphans []string
	for i := 0; i < g.N && i < len(pages); i++ {
		p := pages[i]
		if p.RobotsBlocked || p.StatusCode != 200 || inDegree[i] > 0 {
			continue
		}
		normalized := normalizeTrailingSlash(p.URL)
		isSeed := (seedNorm != "" && normalized == seedNorm) || (seedNorm == "" && p.Depth == 0)
		if !isSeed {
			orphans = append(orphans, p.URL)
		}
	}
	return orphans
}

// fillBrokenLinkSources fills FoundOn for each broken URL with the pages that
// link to it, read from graph in-edges.
func fillBrokenLinkSources(broken []types.BrokenLink, g *AdjacencyGraph) {
	if len(broken) == 0 || g == nil || len(g.Edges) == 0 {
		return
	}
	brokenIdx := make(map[int32]int, len(broken))
	for i, bl := range broken {
		if idx, ok := g.URLIndex[bl.URL]; ok {
			brokenIdx[int32(idx)] = i
		}
	}
	if len(brokenIdx) == 0 {
		return
	}
	for src := 0; src < g.N; src++ {
		for _, dst := range g.Neighbors(src) {
			if i, ok := brokenIdx[dst]; ok && len(broken[i].FoundOn) < maxBrokenLinkSources {
				broken[i].FoundOn = append(broken[i].FoundOn, g.URLs[src])
			}
		}
	}
}

// linksToNonIndexableFromGraph lists links from crawlable 200 pages to pages
// that are not indexable (noindex, canonicalised, non-2xx, ...). Capped at 50.
func linksToNonIndexableFromGraph(g *AdjacencyGraph, pages []*types.PageData) []types.LinkPair {
	if g == nil || len(g.Edges) == 0 {
		return nil
	}
	var out []types.LinkPair
	for src := 0; src < g.N && src < len(pages); src++ {
		p := pages[src]
		if p.RobotsBlocked || p.StatusCode != 200 {
			continue
		}
		for _, dst := range g.Neighbors(src) {
			if int(dst) >= len(pages) || pages[dst].Indexability.Indexable {
				continue
			}
			out = append(out, types.LinkPair{From: p.URL, To: g.URLs[dst]})
			if len(out) >= maxLinksToNonIndexable {
				return out
			}
		}
	}
	return out
}
