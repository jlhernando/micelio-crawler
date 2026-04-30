package analysis

import (
	"fmt"
	"os"
	"sort"
	"strings"

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

func buildGraph(pages []*types.PageData, diskIter func(fn func(source string, targets []string)) error) *AdjacencyGraph {
	n := len(pages)
	urls := make([]string, n)
	statusCodes := make([]int16, n)
	urlIndex := make(map[string]int, n*2)
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
