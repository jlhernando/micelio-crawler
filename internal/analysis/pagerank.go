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

func normalizePageRankOptions(opts PageRankOptions) PageRankOptions {
	defaults := DefaultPageRankOptions()
	if opts.Damping == 0 {
		opts.Damping = defaults.Damping
	}
	if opts.MaxIterations == 0 {
		opts.MaxIterations = defaults.MaxIterations
	}
	if opts.Epsilon == 0 {
		opts.Epsilon = defaults.Epsilon
	}
	return opts
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
	if len(pages) == 0 {
		return make(map[string]float64)
	}

	return ComputePageRankFromGraph(BuildAdjacencyList(pages), opts)
}

// ComputePageRankFromGraph computes PageRank from an already-built adjacency
// graph. This is used when links have been stripped from PageData and supplied
// by a disk-backed iterator instead.
func ComputePageRankFromGraph(graph *AdjacencyGraph, opts PageRankOptions) map[string]float64 {
	if graph == nil || graph.N == 0 {
		return make(map[string]float64)
	}
	opts = normalizePageRankOptions(opts)
	n := graph.N

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
			if graph.OutDegree(i) == 0 {
				danglingSum += rank[i]
			}
		}
		danglingContrib := (opts.Damping * danglingSum) / float64(n)
		for i := range next {
			next[i] += danglingContrib
		}

		// Distribute rank via outlinks
		for i := 0; i < n; i++ {
			deg := graph.OutDegree(i)
			if deg == 0 {
				continue
			}
			share := (opts.Damping * rank[i]) / float64(deg)
			for _, j := range graph.Neighbors(i) {
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
	for i, url := range graph.URLs {
		score := 0.0
		if maxRank > 0 {
			score = (rank[i] / maxRank) * 10
		}
		result[url] = math.Round(score*100) / 100
	}
	return result
}
