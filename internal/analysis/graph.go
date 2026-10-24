package analysis

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/micelio/micelio/internal/types"
)

// AdjacencyGraph is a compact directed graph in CSR (Compressed Sparse Row) format.
// Uses flat int32 arrays instead of [][]int to reduce memory (~50% less) and GC pressure.
// URLs and StatusCodes arrays allow graph algorithms to run without holding PageData in memory.
type AdjacencyGraph struct {
	URLIndex    map[string]int // URL -> node index
	Edges       []int32        // flat CSR edge targets
	Offsets     []int32        // CSR row offsets: node i's edges are Edges[Offsets[i]:Offsets[i+1]]
	URLs        []string       // node index -> URL
	StatusCodes []int16        // node index -> HTTP status code
	N           int

	// Lazily built, cached derived indexes (see InDegree / InNeighbors).
	// A graph must not be modified after either is first used.
	derived *graphDerived
}

// graphDerived holds indexes computed once per graph on first use.
type graphDerived struct {
	inDegOnce sync.Once
	inDeg     []int32
	revOnce   sync.Once
	rev       *ReverseCSR
}

// derivedMu guards creation of AdjacencyGraph.derived (graphs are built in
// several places, including struct literals in tests, so there is no single
// constructor to allocate it in).
var derivedMu sync.Mutex

func (g *AdjacencyGraph) derivedIndexes() *graphDerived {
	derivedMu.Lock()
	defer derivedMu.Unlock()
	if g.derived == nil {
		g.derived = &graphDerived{}
	}
	return g.derived
}

// InDegree returns, per node, the number of distinct pages linking to it.
// Self-links never count as inlinks: a page linking to itself does not make
// it less orphaned. (buildGraph already drops self-links; the check here
// keeps hand-built graphs consistent.) Computed once and cached; callers must
// not modify the returned slice.
func (g *AdjacencyGraph) InDegree() []int32 {
	if g == nil {
		return nil
	}
	d := g.derivedIndexes()
	d.inDegOnce.Do(func() {
		in := make([]int32, g.N)
		for src := 0; src < g.N; src++ {
			for _, t := range g.Edges[g.Offsets[src]:g.Offsets[src+1]] {
				if int(t) < g.N && int(t) != src {
					in[t]++
				}
			}
		}
		d.inDeg = in
	})
	return d.inDeg
}

// ReverseCSR is the transposed graph: node i's inlink sources are
// Sources[Offsets[i]:Offsets[i+1]], sorted ascending, self-links excluded.
type ReverseCSR struct {
	Offsets []int32
	Sources []int32
}

// In returns the sources linking to node i.
func (r *ReverseCSR) In(i int) []int32 {
	return r.Sources[r.Offsets[i]:r.Offsets[i+1]]
}

// BuildReverse builds the transposed graph without caching it. Use it for
// one-off passes (e.g. near-orphan detection) so the graph does not keep the
// extra 4 bytes per edge alive.
func (g *AdjacencyGraph) BuildReverse() *ReverseCSR {
	offsets := make([]int32, g.N+1)
	for src := 0; src < g.N; src++ {
		for _, t := range g.Edges[g.Offsets[src]:g.Offsets[src+1]] {
			if int(t) < g.N && int(t) != src {
				offsets[t+1]++
			}
		}
	}
	for i := 0; i < g.N; i++ {
		offsets[i+1] += offsets[i]
	}
	sources := make([]int32, offsets[g.N])
	fill := make([]int32, g.N)
	copy(fill, offsets[:g.N])
	// Sources are visited in ascending order, so each list ends up sorted.
	for src := 0; src < g.N; src++ {
		for _, t := range g.Edges[g.Offsets[src]:g.Offsets[src+1]] {
			if int(t) < g.N && int(t) != src {
				sources[fill[t]] = int32(src)
				fill[t]++
			}
		}
	}
	return &ReverseCSR{Offsets: offsets, Sources: sources}
}

// Reverse returns the cached transposed graph, building it on first use
// (O(E) time, 4 bytes per edge + 4 per node). Use it for repeated inlink
// lookups (per-click inlinks in the dashboard) instead of scanning every edge.
func (g *AdjacencyGraph) Reverse() *ReverseCSR {
	if g == nil {
		return nil
	}
	d := g.derivedIndexes()
	d.revOnce.Do(func() { d.rev = g.BuildReverse() })
	return d.rev
}

// InNeighbors returns the nodes linking to node i (sorted, no self-link),
// using the cached reverse index.
func (g *AdjacencyGraph) InNeighbors(i int) []int32 {
	if g == nil || i < 0 || i >= g.N {
		return nil
	}
	return g.Reverse().In(i)
}

// URL returns the URL for node i.
func (g *AdjacencyGraph) URL(i int) string { return g.URLs[i] }

// Neighbors returns the outlink indices for node i.
func (g *AdjacencyGraph) Neighbors(i int) []int32 {
	return g.Edges[g.Offsets[i]:g.Offsets[i+1]]
}

// OutDegree returns the number of outlinks for node i.
func (g *AdjacencyGraph) OutDegree(i int) int {
	return int(g.Offsets[i+1] - g.Offsets[i])
}

// HasEdge returns true if there is a directed edge from src to dst.
// Uses binary search on sorted per-node edge lists (O(log degree)).
func (g *AdjacencyGraph) HasEdge(src, dst int) bool {
	if src < 0 || src >= g.N || dst < 0 || dst >= g.N {
		return false
	}
	edges := g.Edges[g.Offsets[src]:g.Offsets[src+1]]
	target := int32(dst)
	i := sort.Search(len(edges), func(k int) bool { return edges[k] >= target })
	return i < len(edges) && edges[i] == target
}

// SubGraphNode holds a node in a subgraph result with expansion metadata.
type SubGraphNode struct {
	Index        int  // node index in the parent graph
	Expandable   bool // has outlinks not included in this subgraph
	TotalOutlinks int  // total outlink count in the full graph
}

// SubGraphResult is the result of a BFS subgraph extraction.
type SubGraphResult struct {
	Nodes []SubGraphNode // nodes in the subgraph
	Edges [][2]int       // [source, target] index pairs (indices into parent graph)
}

// SubGraph extracts a subgraph via BFS from rootIdx, traversing up to maxHops levels.
// Returns at most maxNodes nodes. Each node includes whether it has outlinks beyond the subgraph.
func (g *AdjacencyGraph) SubGraph(rootIdx int, maxHops int, maxNodes int) SubGraphResult {
	if rootIdx < 0 || rootIdx >= g.N {
		return SubGraphResult{}
	}
	if maxNodes <= 0 {
		maxNodes = 500
	}
	if maxHops < 0 {
		maxHops = 1
	}

	visited := make(map[int]struct{}, maxNodes)
	visited[rootIdx] = struct{}{}
	frontier := []int{rootIdx}

	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		var nextFrontier []int
		for _, idx := range frontier {
			neighbors := g.Neighbors(idx)
			for _, ni := range neighbors {
				nIdx := int(ni)
				if _, seen := visited[nIdx]; seen {
					continue
				}
				visited[nIdx] = struct{}{}
				nextFrontier = append(nextFrontier, nIdx)
				if len(visited) >= maxNodes {
					goto done
				}
			}
		}
		frontier = nextFrontier
	}
done:

	// Build node list with expansion metadata
	nodes := make([]SubGraphNode, 0, len(visited))
	for idx := range visited {
		outDeg := g.OutDegree(idx)
		expandable := false
		if outDeg > 0 {
			// Check if any outlink is NOT in the visited set
			for _, ni := range g.Neighbors(idx) {
				if _, ok := visited[int(ni)]; !ok {
					expandable = true
					break
				}
			}
		}
		nodes = append(nodes, SubGraphNode{Index: idx, Expandable: expandable, TotalOutlinks: outDeg})
	}

	// Collect edges (only between visited nodes)
	edges := make([][2]int, 0, len(visited)*4)
	for idx := range visited {
		for _, ni := range g.Neighbors(idx) {
			nIdx := int(ni)
			if _, ok := visited[nIdx]; ok {
				edges = append(edges, [2]int{idx, nIdx})
			}
		}
	}

	return SubGraphResult{Nodes: nodes, Edges: edges}
}

// BuildAdjacencyList builds a deduplicated adjacency graph from pages' internal links.
// Self-links are excluded. Handles trailing-slash variants and finalUrl redirects.
func BuildAdjacencyList(pages []*types.PageData) *AdjacencyGraph {
	return buildGraph(pages, nil)
}

// BuildAdjacencyListFromDisk builds the graph using a disk-backed edge iterator
// instead of reading InternalLinks from pages. This saves ~5GB for 1M-page crawls.
func BuildAdjacencyListFromDisk(pages []*types.PageData, iter func(fn func(source string, targets []string)) error) *AdjacencyGraph {
	return buildGraph(pages, iter)
}

// graphNodes builds the per-node arrays shared by every graph built from
// pages: node i is pages[i]; FinalURL aliases resolve redirects.
func graphNodes(pages []*types.PageData) (urls []string, statusCodes []int16, urlIndex map[string]int) {
	n := len(pages)
	urls = make([]string, n)
	statusCodes = make([]int16, n)
	urlIndex = make(map[string]int, n*2)
	for i, p := range pages {
		urls[i] = p.URL
		statusCodes[i] = int16(p.StatusCode)
		urlIndex[p.URL] = i
		if p.FinalURL != "" && p.FinalURL != p.URL {
			if _, exists := urlIndex[p.FinalURL]; !exists {
				urlIndex[p.FinalURL] = i
			}
		}
	}
	return urls, statusCodes, urlIndex
}

// NewAdjacencyGraphFromCSR rebuilds a graph from stored CSR arrays (see
// storage.CrawlStore.SaveLinkGraph). Node i must be pages[i]. Returns nil
// when the arrays do not fit the pages.
func NewAdjacencyGraphFromCSR(pages []*types.PageData, offsets, edges []int32) *AdjacencyGraph {
	n := len(pages)
	if len(offsets) != n+1 || offsets[0] != 0 || int(offsets[n]) != len(edges) {
		return nil
	}
	for i := 0; i < n; i++ {
		if offsets[i+1] < offsets[i] {
			return nil
		}
	}
	for _, t := range edges {
		if t < 0 || int(t) >= n {
			return nil
		}
	}
	urls, statusCodes, urlIndex := graphNodes(pages)
	return &AdjacencyGraph{URLIndex: urlIndex, Edges: edges, Offsets: offsets, URLs: urls, StatusCodes: statusCodes, N: n}
}

func buildGraph(pages []*types.PageData, diskIter func(fn func(source string, targets []string)) error) *AdjacencyGraph {
	n := len(pages)
	urls, statusCodes, urlIndex := graphNodes(pages)

	resolveLink := func(link string) (int, bool) {
		if idx, ok := urlIndex[link]; ok {
			return idx, true
		}
		if strings.HasSuffix(link, "/") && len(link) > 1 {
			if idx, ok := urlIndex[link[:len(link)-1]]; ok {
				return idx, true
			}
		} else {
			if idx, ok := urlIndex[link+"/"]; ok {
				return idx, true
			}
		}
		return 0, false
	}

	// Collect edges per source node. Uses []int32 slices instead of maps
	// for lower memory overhead (sort+compact dedup after collection).
	edgeLists := make([][]int32, n)

	if diskIter != nil {
		// Read edges from disk iterator (memory-efficient: no string retention)
		if err := diskIter(func(source string, targets []string) {
			srcIdx, ok := resolveLink(source)
			if !ok {
				return
			}
			for _, target := range targets {
				dstIdx, ok := resolveLink(target)
				if ok && dstIdx != srcIdx {
					edgeLists[srcIdx] = append(edgeLists[srcIdx], int32(dstIdx))
				}
			}
		}); err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] reading internal links from disk: %v\n", err)
		}
	} else {
		// Read edges from pages' InternalLinks (legacy path)
		for i, p := range pages {
			for _, link := range p.InternalLinks {
				j, ok := resolveLink(link)
				if ok && j != i {
					edgeLists[i] = append(edgeLists[i], int32(j))
				}
			}
		}
	}

	// Build CSR: sort + compact dedup each node's edges, then flatten.
	offsets := make([]int32, n+1)
	var allEdges []int32
	for i, edges := range edgeLists {
		if len(edges) == 0 {
			offsets[i+1] = offsets[i]
			continue
		}
		sort.Slice(edges, func(a, b int) bool { return edges[a] < edges[b] })
		// Compact dedup
		j := 0
		for k := 1; k < len(edges); k++ {
			if edges[k] != edges[j] {
				j++
				edges[j] = edges[k]
			}
		}
		allEdges = append(allEdges, edges[:j+1]...)
		offsets[i+1] = int32(len(allEdges))
		edgeLists[i] = nil // free as we go
	}

	return &AdjacencyGraph{URLIndex: urlIndex, Edges: allEdges, Offsets: offsets, URLs: urls, StatusCodes: statusCodes, N: n}
}
