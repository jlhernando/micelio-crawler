package analysis

import (
	"math"
	"math/rand"
	"runtime"
	"strings"
)

// ── Click Depth ──────────────────────────────────────────

// ComputeClickDepth computes minimum click depth from the homepage to every
// reachable page via BFS. Returns a map of URL -> click depth.
// Uses graph.URLs for result keys so no PageData slice is needed.
func ComputeClickDepth(homepageURL string, graph *AdjacencyGraph) map[string]int {
	result := make(map[string]int)
	if graph == nil || graph.N == 0 {
		return result
	}

	homeIdx := findHomepageIndex(graph, homepageURL)
	if homeIdx < 0 {
		return result
	}

	visited := make([]bool, graph.N)
	visited[homeIdx] = true
	result[graph.URLs[homeIdx]] = 0

	queue := []int{homeIdx}
	depth := 0

	for len(queue) > 0 {
		depth++
		nextQueue := make([]int, 0)
		for _, idx := range queue {
			for _, nb := range graph.Neighbors(idx) {
				neighbor := int(nb)
				if !visited[neighbor] {
					visited[neighbor] = true
					result[graph.URLs[neighbor]] = depth
					nextQueue = append(nextQueue, neighbor)
				}
			}
		}
		queue = nextQueue
	}

	return result
}

func findHomepageIndex(graph *AdjacencyGraph, homepageURL string) int {
	if idx, ok := graph.URLIndex[homepageURL]; ok {
		return idx
	}
	if strings.HasSuffix(homepageURL, "/") {
		if idx, ok := graph.URLIndex[homepageURL[:len(homepageURL)-1]]; ok {
			return idx
		}
	} else {
		if idx, ok := graph.URLIndex[homepageURL+"/"]; ok {
			return idx
		}
	}
	return -1
}

// ── HITS Hub/Authority Scores ────────────────────────────

// HitsScores contains HITS hub and authority scores for a page.
type HitsScores struct {
	HubScore       float64 `json:"hubScore"`
	AuthorityScore float64 `json:"authorityScore"`
}

// ComputeHits computes HITS (Hyperlink-Induced Topic Search) scores.
// Authority = pages that many good hubs link to.
// Hub = pages that link to many good authorities.
// Uses graph.URLs for result keys so no PageData slice is needed.
func ComputeHits(maxIterations int, epsilon float64, graph *AdjacencyGraph) map[string]HitsScores {
	if maxIterations == 0 {
		maxIterations = 50
	}
	if epsilon == 0 {
		epsilon = 0.0001
	}
	if graph == nil || graph.N == 0 {
		return make(map[string]HitsScores)
	}
	n := graph.N

	// Derive inlinks from outlinks (CSR format)
	inCounts := make([]int32, n)
	for i := 0; i < n; i++ {
		for _, j := range graph.Neighbors(i) {
			inCounts[j]++
		}
	}
	inOffsets := make([]int32, n+1)
	for i := 0; i < n; i++ {
		inOffsets[i+1] = inOffsets[i] + inCounts[i]
	}
	inEdges := make([]int32, inOffsets[n])
	fill := make([]int32, n)
	for i := 0; i < n; i++ {
		for _, j := range graph.Neighbors(i) {
			pos := inOffsets[j] + fill[j]
			inEdges[pos] = int32(i)
			fill[j]++
		}
	}

	auth := make([]float64, n)
	hub := make([]float64, n)
	for i := range auth {
		auth[i] = 1
		hub[i] = 1
	}

	for iter := 0; iter < maxIterations; iter++ {
		newAuth := make([]float64, n)
		for i := 0; i < n; i++ {
			for _, j := range inEdges[inOffsets[i]:inOffsets[i+1]] {
				newAuth[i] += hub[j]
			}
		}

		newHub := make([]float64, n)
		for i := 0; i < n; i++ {
			for _, j := range graph.Neighbors(i) {
				newHub[i] += auth[j]
			}
		}

		var authNorm, hubNorm float64
		for i := 0; i < n; i++ {
			authNorm += newAuth[i] * newAuth[i]
			hubNorm += newHub[i] * newHub[i]
		}
		authNorm = math.Sqrt(authNorm)
		hubNorm = math.Sqrt(hubNorm)

		if authNorm > 0 {
			for i := range newAuth {
				newAuth[i] /= authNorm
			}
		}
		if hubNorm > 0 {
			for i := range newHub {
				newHub[i] /= hubNorm
			}
		}

		var delta float64
		for i := 0; i < n; i++ {
			delta += math.Abs(newAuth[i]-auth[i]) + math.Abs(newHub[i]-hub[i])
		}

		auth = newAuth
		hub = newHub
		if delta < epsilon {
			break
		}
	}

	var maxAuth, maxHub float64
	for i := 0; i < n; i++ {
		if auth[i] > maxAuth {
			maxAuth = auth[i]
		}
		if hub[i] > maxHub {
			maxHub = hub[i]
		}
	}

	result := make(map[string]HitsScores, n)
	for i := 0; i < n; i++ {
		as := 0.0
		hs := 0.0
		if maxAuth > 0 {
			as = math.Round((auth[i]/maxAuth)*1000) / 100
		}
		if maxHub > 0 {
			hs = math.Round((hub[i]/maxHub)*1000) / 100
		}
		result[graph.URLs[i]] = HitsScores{AuthorityScore: as, HubScore: hs}
	}
	return result
}

// ── Betweenness Centrality ───────────────────────────────

// ComputeBetweennessCentrality computes betweenness centrality using Brandes' algorithm.
// For large graphs (>maxNodes), uses random sampling of 500 source nodes.
// Uses graph.URLs for result keys so no PageData slice is needed.
func ComputeBetweennessCentrality(maxNodes int, graph *AdjacencyGraph) map[string]float64 {
	if maxNodes == 0 {
		maxNodes = 5000
	}
	if graph == nil || graph.N == 0 {
		return make(map[string]float64)
	}
	n := graph.N

	sourceNodes := sampleSourceNodes(n, maxNodes)
	bc := make([]float64, n)

	for si, s := range sourceNodes {
		stack := make([]int, 0, n)
		pred := make([][]int, n)
		sigma := make([]float64, n)
		sigma[s] = 1

		dist := make([]int, n)
		for i := range dist {
			dist[i] = -1
		}
		dist[s] = 0

		queue := []int{s}
		qi := 0

		for qi < len(queue) {
			v := queue[qi]
			qi++
			stack = append(stack, v)
			for _, ww := range graph.Neighbors(v) {
				w := int(ww)
				if dist[w] < 0 {
					dist[w] = dist[v] + 1
					queue = append(queue, w)
				}
				if dist[w] == dist[v]+1 {
					sigma[w] += sigma[v]
					pred[w] = append(pred[w], v)
				}
			}
		}

		delta := make([]float64, n)
		for i := len(stack) - 1; i >= 0; i-- {
			w := stack[i]
			for _, v := range pred[w] {
				delta[v] += (sigma[v] / sigma[w]) * (1 + delta[w])
			}
			if w != s {
				bc[w] += delta[w]
			}
		}

		// Periodic GC to prevent memory accumulation from intermediate allocations
		if si > 0 && si%500 == 0 {
			runtime.GC()
		}
	}

	// Scale up if sampled
	if len(sourceNodes) < n {
		scale := float64(n) / float64(len(sourceNodes))
		for i := range bc {
			bc[i] *= scale
		}
	}

	return normalizeToScale(bc, graph.URLs, 10)
}

// ── Closeness Centrality ─────────────────────────────────

// ComputeClosenessCentrality computes closeness centrality: how quickly a page
// can reach all other pages.
// Uses graph.URLs for result keys so no PageData slice is needed.
func ComputeClosenessCentrality(maxNodes int, graph *AdjacencyGraph) map[string]float64 {
	if maxNodes == 0 {
		maxNodes = 5000
	}
	if graph == nil || graph.N == 0 {
		return make(map[string]float64)
	}
	n := graph.N

	sourceNodes := sampleSourceNodes(n, maxNodes)
	rawCloseness := make([]float64, n)

	for si, s := range sourceNodes {
		dist := make([]int, n)
		for i := range dist {
			dist[i] = -1
		}
		dist[s] = 0
		queue := []int{s}
		qi := 0
		totalDist := 0
		reachable := 0

		for qi < len(queue) {
			v := queue[qi]
			qi++
			for _, ww := range graph.Neighbors(v) {
				w := int(ww)
				if dist[w] < 0 {
					dist[w] = dist[v] + 1
					totalDist += dist[w]
					reachable++
					queue = append(queue, w)
				}
			}
		}

		if reachable > 0 && totalDist > 0 {
			rawCloseness[s] = float64(reachable) / float64(totalDist)
		}

		// Periodic GC to prevent memory accumulation
		if si > 0 && si%500 == 0 {
			runtime.GC()
		}
	}

	return normalizeToScale(rawCloseness, graph.URLs, 10)
}

// ── Cosine Similarity ────────────────────────────────────

// CosineSimilarity computes cosine similarity between two vectors.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom > 0 {
		return dot / denom
	}
	return 0
}

// ── Shared helpers ───────────────────────────────────────

// sampleSourceNodes returns source node indices, sampling if n > maxNodes.
func sampleSourceNodes(n, maxNodes int) []int {
	sampleSize := n
	useSampling := n > maxNodes
	if useSampling {
		sampleSize = 500
		if sampleSize > n {
			sampleSize = n
		}
	}

	sourceNodes := make([]int, sampleSize)
	if useSampling {
		indices := make([]int, n)
		for i := range indices {
			indices[i] = i
		}
		for i := n - 1; i > 0 && i >= n-sampleSize; i-- {
			j := rand.Intn(i + 1)
			indices[i], indices[j] = indices[j], indices[i]
		}
		copy(sourceNodes, indices[n-sampleSize:])
	} else {
		for i := range sourceNodes {
			sourceNodes[i] = i
		}
	}
	return sourceNodes
}

// normalizeToScale normalizes raw scores to a 0-scale range and maps to URLs.
func normalizeToScale(raw []float64, urls []string, scale float64) map[string]float64 {
	var maxVal float64
	for _, v := range raw {
		if v > maxVal {
			maxVal = v
		}
	}

	result := make(map[string]float64, len(urls))
	for i, u := range urls {
		score := 0.0
		if maxVal > 0 {
			score = math.Round((raw[i]/maxVal)*scale*100) / 100
		}
		result[u] = score
	}
	return result
}
