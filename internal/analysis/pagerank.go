// Package analysis implements SEO analysis algorithms.
package analysis

import (
	"math"

	"github.com/micelio/micelio/internal/types"
)

// PageRankOptions configures the PageRank algorithm.
type PageRankOptions struct {
	Damping       float64 // Damping factor (default 0.85)
	MaxIterations int     // Maximum iterations (default 100)
	Epsilon       float64 // Convergence threshold (default 0.0001)
}

// DefaultPageRankOptions returns sensible defaults.
func DefaultPageRankOptions() PageRankOptions {
	return PageRankOptions{
		Damping:       0.85,
		MaxIterations: 100,
		Epsilon:       0.0001,
	}
}

// ComputePageRank computes internal PageRank scores for crawled pages.
//
// Uses the standard iterative PageRank algorithm:
//
//	PR(i) = (1-d)/N + d * SUM(PR(j) / L(j)) for all j linking to i
//
// where d = damping factor, N = total pages, L(j) = outlink count of page j.
// Dangling nodes (pages with no outlinks) distribute rank evenly.
// Scores are normalized to a 0–10 scale.
func ComputePageRank(pages []*types.PageData, opts PageRankOptions) map[string]float64 {
	n := len(pages)
	if n == 0 {
		return make(map[string]float64)
	}

	if opts.Damping == 0 {
		opts = DefaultPageRankOptions()
	}

	// Build URL → index mapping
	urlIndex := make(map[string]int, n)
	for i, p := range pages {
		urlIndex[p.URL] = i
	}

	// Build adjacency in CSR format: flat edges + offsets
	offsets := make([]int32, n+1)
	var edges []int32
	for i, p := range pages {
		seen := make(map[int32]struct{})
		for _, link := range p.InternalLinks {
			j, ok := urlIndex[link]
			if ok && j != i {
				seen[int32(j)] = struct{}{}
			}
		}
		for j := range seen {
			edges = append(edges, j)
		}
		offsets[i+1] = int32(len(edges))
	}

	// Iterative PageRank
	rank := make([]float64, n)
	initial := 1.0 / float64(n)
	for i := range rank {
		rank[i] = initial
	}

	base := (1 - opts.Damping) / float64(n)

	for iter := 0; iter < opts.MaxIterations; iter++ {
		next := make([]float64, n)
		for i := range next {
			next[i] = base
		}

		// Dangling node rank (pages with 0 outlinks)
		var danglingSum float64
		for i := 0; i < n; i++ {
			if offsets[i+1] == offsets[i] {
				danglingSum += rank[i]
			}
		}
		danglingContrib := (opts.Damping * danglingSum) / float64(n)
		for i := range next {
			next[i] += danglingContrib
		}

		// Distribute rank via outlinks
		for i := 0; i < n; i++ {
			deg := offsets[i+1] - offsets[i]
			if deg == 0 {
				continue
			}
			share := (opts.Damping * rank[i]) / float64(deg)
			for _, j := range edges[offsets[i]:offsets[i+1]] {
				next[j] += share
			}
		}

		// Check convergence
		var delta float64
		for i := 0; i < n; i++ {
			delta += math.Abs(next[i] - rank[i])
		}
		rank = next
		if delta < opts.Epsilon {
			break
		}
	}

	// Normalize to 0–10 scale
	var maxRank float64
	for _, r := range rank {
		if r > maxRank {
			maxRank = r
		}
	}

	result := make(map[string]float64, n)
	for i, p := range pages {
		score := 0.0
		if maxRank > 0 {
			score = (rank[i] / maxRank) * 10
		}
		result[p.URL] = math.Round(score*100) / 100
	}
	return result
}
