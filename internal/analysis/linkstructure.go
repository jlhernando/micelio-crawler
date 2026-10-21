package analysis

import (
	"sort"

	"github.com/micelio/micelio/internal/types"
)

const (
	// connectivityTopN caps the MostConnected / MostIsolated lists.
	connectivityTopN = 20
	// noContentLinksListCap caps the PagesWithNoContentLinks URL list.
	// PagesWithNoContentLinksCount keeps the full count.
	noContentLinksListCap = 50
)

// fillLinkStructureStats fills the link-intelligence fields that depend on
// graph degree and on per-page link zones:
//
//   - LinkPositionDistribution: internal links per page region (content,
//     navigation, header, footer, sidebar, breadcrumb, comments, related,
//     other), summed from PageData.LinkZones.
//   - PagesWithNoContentLinks: 200 pages that have inlinks, none of which can
//     come from a content area. Anchors are gone by the time link
//     intelligence runs, so this uses each source page's LinkZones: a page is
//     listed only when every page linking to it has zone data and zero
//     content-area links. This never flags a page that does have a content
//     inlink; it can miss pages whose sources link from content to other
//     pages only.
//   - MostConnected / MostIsolated: 200 pages ranked by total degree
//     (inlinks + outlinks) in the link graph. MostIsolated excludes pages
//     with no inlinks (already reported as orphans).
//
// pages[i] must correspond to graph node i. inDegree is indexed by node.
func fillLinkStructureStats(liStats *types.LinkIntelligenceStats, pages []*types.PageData, graph *AdjacencyGraph, inDegree []int) {
	if liStats == nil || graph == nil {
		return
	}
	n := graph.N
	if len(pages) < n {
		n = len(pages)
	}

	// Link position distribution.
	if liStats.LinkPositionDistribution == nil {
		liStats.LinkPositionDistribution = make(map[string]int)
	}
	for i := 0; i < n; i++ {
		z := pages[i].LinkZones
		if z == nil {
			continue
		}
		addZone(liStats.LinkPositionDistribution, types.LinkPosContent, z.Content)
		addZone(liStats.LinkPositionDistribution, types.LinkPosNavigation, z.Navigation)
		addZone(liStats.LinkPositionDistribution, types.LinkPosHeader, z.Header)
		addZone(liStats.LinkPositionDistribution, types.LinkPosFooter, z.Footer)
		addZone(liStats.LinkPositionDistribution, types.LinkPosSidebar, z.Sidebar)
		addZone(liStats.LinkPositionDistribution, types.LinkPosBreadcrumb, z.Breadcrumb)
		addZone(liStats.LinkPositionDistribution, types.LinkPosComments, z.Comments)
		addZone(liStats.LinkPositionDistribution, types.LinkPosRelated, z.Related)
		addZone(liStats.LinkPositionDistribution, types.LinkPosOther, z.Other)
	}

	// Pages with no content-area inlinks. mayHaveContentInlink[j] is true
	// when at least one source of j has content links or unknown zones.
	mayHaveContentInlink := make([]bool, graph.N)
	for i := 0; i < graph.N; i++ {
		var z *types.LinkZoneCounts
		if i < len(pages) {
			z = pages[i].LinkZones
		}
		if z != nil && z.Content == 0 {
			continue
		}
		for _, j := range graph.Neighbors(i) {
			if int(j) < len(mayHaveContentInlink) && int(j) != i {
				mayHaveContentInlink[j] = true
			}
		}
	}
	liStats.PagesWithNoContentLinks = nil
	liStats.PagesWithNoContentLinksCount = 0
	for j := 0; j < n; j++ {
		if graph.StatusCodes[j] != 200 || j >= len(inDegree) || inDegree[j] == 0 || mayHaveContentInlink[j] {
			continue
		}
		liStats.PagesWithNoContentLinksCount++
		if len(liStats.PagesWithNoContentLinks) < noContentLinksListCap {
			liStats.PagesWithNoContentLinks = append(liStats.PagesWithNoContentLinks, graph.URLs[j])
		}
	}

	// Most connected / most isolated by total degree.
	type degreeEntry struct {
		url    string
		degree int
		in     int
	}
	entries := make([]degreeEntry, 0, graph.N)
	for i := 0; i < graph.N; i++ {
		if graph.StatusCodes[i] != 200 {
			continue
		}
		in := 0
		if i < len(inDegree) {
			in = inDegree[i]
		}
		entries = append(entries, degreeEntry{url: graph.URLs[i], degree: in + graph.OutDegree(i), in: in})
	}
	sort.Slice(entries, func(a, b int) bool {
		if entries[a].degree != entries[b].degree {
			return entries[a].degree > entries[b].degree
		}
		return entries[a].url < entries[b].url
	})
	liStats.MostConnected = nil
	for _, e := range entries {
		if len(liStats.MostConnected) >= connectivityTopN || e.degree == 0 {
			break
		}
		liStats.MostConnected = append(liStats.MostConnected, types.URLScoreEntry{URL: e.url, Score: float64(e.degree)})
	}
	liStats.MostIsolated = nil
	for k := len(entries) - 1; k >= 0 && len(liStats.MostIsolated) < connectivityTopN; k-- {
		if entries[k].in == 0 {
			continue
		}
		liStats.MostIsolated = append(liStats.MostIsolated, types.URLScoreEntry{URL: entries[k].url, Score: float64(entries[k].degree)})
	}
}

func addZone(dist map[string]int, pos types.LinkPosition, count int) {
	if count > 0 {
		dist[string(pos)] += count
	}
}
