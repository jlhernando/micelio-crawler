package analysis

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// ── Near-Orphan Detection ────────────────────────────────
//
// Pages with low in-degree linked only from deep pages are practically invisible
// to both users and Googlebot. Crawl budget allocation favors well-linked pages.
// Source: Google crawl budget guidance, DOJ (Trawler/Alexandria index tiering).
// Confidence: STRONG_PROXY (thresholds are configurable heuristics, not Google's values).

// NearOrphanInfo describes a near-orphan page.
type NearOrphanInfo struct {
	WorstSourceDepth *int   `json:"worstSourceDepth"`
	URL              string `json:"url"`
	InDegree         int    `json:"inDegree"`
}

// DetectNearOrphans finds pages with low in-degree where linking sources
// are themselves deep. These pages are technically linked but practically invisible.
// Uses graph.URLs and graph.StatusCodes so no PageData slice is needed.
func DetectNearOrphans(
	clickDepths map[string]int,
	graph *AdjacencyGraph,
	inDegree []int,
	maxInDegree int,
	minSourceDepth int,
) []NearOrphanInfo {
	if maxInDegree == 0 {
		maxInDegree = 2
	}
	if minSourceDepth == 0 {
		minSourceDepth = 4
	}

	// Build reverse adjacency: target → source indices
	reverseAdj := make([][]int32, graph.N)
	for i := 0; i < graph.N; i++ {
		for _, j := range graph.Neighbors(i) {
			if int(j) >= len(reverseAdj) {
				continue
			}
			reverseAdj[j] = append(reverseAdj[j], int32(i))
		}
	}

	var nearOrphans []NearOrphanInfo

	for i := 0; i < graph.N; i++ {
		if graph.StatusCodes[i] != 200 {
			continue
		}
		deg := inDegree[i]
		if deg == 0 || deg > maxInDegree {
			continue
		}

		worstDepth := 0
		for _, srcIdx := range reverseAdj[i] {
			if int(srcIdx) >= graph.N {
				continue
			}
			srcURL := graph.URLs[srcIdx]
			d, ok := clickDepths[srcURL]
			if !ok {
				d = 999
			}
			if d > worstDepth {
				worstDepth = d
			}
		}

		if worstDepth >= minSourceDepth {
			var wd *int
			if worstDepth != 999 {
				wd = &worstDepth
			}
			nearOrphans = append(nearOrphans, NearOrphanInfo{
				URL:              graph.URLs[i],
				InDegree:         deg,
				WorstSourceDepth: wd,
			})
		}
	}

	sort.Slice(nearOrphans, func(i, j int) bool {
		di := 999
		dj := 999
		if nearOrphans[i].WorstSourceDepth != nil {
			di = *nearOrphans[i].WorstSourceDepth
		}
		if nearOrphans[j].WorstSourceDepth != nil {
			dj = *nearOrphans[j].WorstSourceDepth
		}
		return di > dj
	})

	return nearOrphans
}

// ── Link Equity Dilution ─────────────────────────────────
//
// Google's ranking formula uses log transformation on signals before scoring
// (DOJ: Nayak PXR0357 — "linear combination of log of individual raw signals").
// Link equity per outlink decays logarithmically, not via hard cutoffs.
// Source: Reasonable Surfer patent (US7716225), DOJ (sigmoid/log transform).
// Confidence: CONFIRMED (log transform), STRONG_PROXY (specific decay formula).

// LinkDilutionInfo describes a page with diluted outgoing link equity.
type LinkDilutionInfo struct {
	URL            string  `json:"url"`
	Warning        string  `json:"warning"`
	OutDegree      int     `json:"outDegree"`
	EquityPerLink  float64 `json:"equityPerLink"`  // Estimated equity fraction per outlink (0-1)
}

// ComputeLinkDilution finds pages with diluted outgoing link equity.
// Uses logarithmic decay instead of hard cutoffs, matching Google's
// log-transformed signal processing (DOJ: Nayak PXR0357).
func ComputeLinkDilution(graph *AdjacencyGraph) []LinkDilutionInfo {
	var warnings []LinkDilutionInfo
	for i := 0; i < graph.N; i++ {
		if graph.StatusCodes[i] != 200 {
			continue
		}
		outDegree := graph.OutDegree(i)
		if outDegree < 20 {
			continue // Below concern threshold
		}

		// Logarithmic equity decay: each outlink gets 1/log2(n+1) of equity.
		// At 50 links: 0.176, at 100: 0.150, at 200: 0.131, at 500: 0.111
		equityPerLink := 1.0 / math.Log2(float64(outDegree)+1)

		var warning string
		switch {
		case equityPerLink < 0.13: // ~200+ links
			warning = "excessive"
		case equityPerLink < 0.15: // ~100-200 links
			warning = "high"
		case equityPerLink < 0.18: // ~50-100 links
			warning = "moderate"
		default:
			continue
		}

		warnings = append(warnings, LinkDilutionInfo{
			URL:           graph.URLs[i],
			OutDegree:     outDegree,
			Warning:       warning,
			EquityPerLink: math.Round(equityPerLink*1000) / 1000,
		})
	}
	sort.Slice(warnings, func(i, j int) bool {
		return warnings[i].OutDegree > warnings[j].OutDegree
	})
	return warnings
}

// ── Link Suggestions ─────────────────────────────────────

// LinkSuggestionsOptions configures link suggestion generation.
type LinkSuggestionsOptions struct {
	MaxSuggestions        int
	MinSemanticSimilarity float64
	MaxSemanticSimilarity float64
	TargetMaxInDegree     int
	SourceMinWordCount    int
}

// DefaultLinkSuggestionsOptions returns defaults.
func DefaultLinkSuggestionsOptions() LinkSuggestionsOptions {
	return LinkSuggestionsOptions{
		MaxSuggestions:        50,
		MinSemanticSimilarity: 0.3,
		MaxSemanticSimilarity: 0.85,
		TargetMaxInDegree:     3,
		SourceMinWordCount:    200,
	}
}

// GenerateLinkSuggestions generates internal linking suggestions based on
// semantic similarity, target in-degree, PageRank, and click depth.
// Uses graph metadata (URLs, StatusCodes) plus wordCounts/indexable arrays
// so no PageData slice is needed during computation.
func GenerateLinkSuggestions(
	graph *AdjacencyGraph,
	embeddings map[string][]float64,
	clickDepths map[string]int,
	pageRanks map[string]float64,
	inDegree []int,
	wordCounts []int32,
	indexable []bool,
	opts LinkSuggestionsOptions,
) []types.LinkSuggestion {
	if opts.MaxSuggestions == 0 {
		opts = DefaultLinkSuggestionsOptions()
	}
	if graph == nil || graph.N == 0 {
		return nil
	}

	// Target candidates: low in-degree, indexable, 200 OK
	type candidate struct {
		url string
		idx int
	}
	var targetCandidates []candidate
	for i := 0; i < graph.N; i++ {
		if graph.StatusCodes[i] != 200 || !indexable[i] {
			continue
		}
		if inDegree[i] <= opts.TargetMaxInDegree {
			targetCandidates = append(targetCandidates, candidate{graph.URLs[i], i})
		}
	}

	// Source candidates: content pages with enough text
	var sourceCandidates []candidate
	for i := 0; i < graph.N; i++ {
		if graph.StatusCodes[i] == 200 && int(wordCounts[i]) >= opts.SourceMinWordCount {
			sourceCandidates = append(sourceCandidates, candidate{graph.URLs[i], i})
		}
	}

	// Cap candidates for performance
	if len(sourceCandidates) > 500 {
		sort.Slice(sourceCandidates, func(i, j int) bool {
			return pageRanks[sourceCandidates[i].url] > pageRanks[sourceCandidates[j].url]
		})
		sourceCandidates = sourceCandidates[:500]
	}
	if len(targetCandidates) > 200 {
		sort.Slice(targetCandidates, func(i, j int) bool {
			return inDegree[targetCandidates[i].idx] < inDegree[targetCandidates[j].idx]
		})
		targetCandidates = targetCandidates[:200]
	}

	var suggestions []types.LinkSuggestion

	for _, target := range targetCandidates {
		for _, source := range sourceCandidates {
			if source.url == target.url {
				continue
			}
			// Check existing link via graph (O(log degree) binary search)
			if graph.HasEdge(source.idx, target.idx) {
				continue
			}

			// Semantic similarity
			var semanticSim *float64
			if embeddings != nil {
				srcVec := embeddings[source.url]
				tgtVec := embeddings[target.url]
				if srcVec != nil && tgtVec != nil {
					sim := CosineSimilarity(srcVec, tgtVec)
					if sim < opts.MinSemanticSimilarity || sim > opts.MaxSemanticSimilarity {
						continue
					}
					semanticSim = &sim
				}
			}

			targetInDeg := inDegree[target.idx]
			targetPR := pageRanks[target.url]
			targetCD, targetCDOK := clickDepths[target.url]
			sourceCD, sourceCDOK := clickDepths[source.url]

			var depthReduction *int
			if targetCDOK && sourceCDOK {
				dr := targetCD - sourceCD - 1
				if dr < 0 {
					dr = 0
				}
				depthReduction = &dr
			}

			// Score components (each 0-25, total max 100)
			var score float64

			if semanticSim != nil {
				idealRange := *semanticSim >= 0.4 && *semanticSim <= 0.75
				if idealRange {
					score += *semanticSim * 33
				} else {
					score += *semanticSim * 20
				}
			}

			inDegScore := math.Max(0, 25-float64(targetInDeg)*5)
			score += inDegScore

			score += math.Min(25, targetPR*2.5)

			if depthReduction != nil && *depthReduction > 0 {
				score += math.Min(25, float64(*depthReduction)*8)
			}

			if score >= 20 {
				var targetCDPtr *int
				if targetCDOK {
					targetCDPtr = &targetCD
				}
				suggestions = append(suggestions, types.LinkSuggestion{
					SourceURL: source.url,
					TargetURL: target.url,
					Score:     math.Round(score*10) / 10,
					Reason:    buildSuggestionReason(semanticSim, targetInDeg, targetCDPtr, depthReduction, targetPR),
					Signals: types.LinkSuggestionSignals{
						SemanticSimilarity: semanticSim,
						TargetInDegree:     targetInDeg,
						TargetClickDepth:   targetCDPtr,
						DepthReduction:     depthReduction,
						TargetPageRank:     targetPR,
					},
				})
			}
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})

	if len(suggestions) > opts.MaxSuggestions {
		suggestions = suggestions[:opts.MaxSuggestions]
	}
	return suggestions
}

func buildSuggestionReason(similarity *float64, inDegree int, clickDepth *int, depthReduction *int, pageRank float64) string {
	var parts []string
	if similarity != nil && *similarity >= 0.4 {
		parts = append(parts, fmt.Sprintf("topically related (%d%% similarity)", int(math.Round(*similarity*100))))
	}
	if inDegree <= 1 {
		if inDegree == 1 {
			parts = append(parts, "target has only 1 inlink")
		} else {
			parts = append(parts, fmt.Sprintf("target has only %d inlinks", inDegree))
		}
	} else if inDegree <= 3 {
		parts = append(parts, fmt.Sprintf("target has only %d inlinks", inDegree))
	}
	if depthReduction != nil && *depthReduction >= 2 {
		parts = append(parts, fmt.Sprintf("would reduce click depth by %d", *depthReduction))
	}
	if clickDepth != nil && *clickDepth > 3 {
		parts = append(parts, fmt.Sprintf("target is %d clicks deep", *clickDepth))
	}
	if pageRank >= 5 {
		parts = append(parts, fmt.Sprintf("high-value target (PR %.1f)", pageRank))
	}
	if len(parts) == 0 {
		return "potential linking opportunity"
	}
	return strings.Join(parts, "; ")
}

// ── Semantic Link Distance ───────────────────────────────

// SemanticLinkPair represents a link pair with its semantic similarity.
type SemanticLinkPair struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Similarity float64 `json:"similarity"`
}

// SemanticLinkAnalysis holds the results of semantic link distance analysis.
type SemanticLinkAnalysis struct {
	WeakLinks        []SemanticLinkPair `json:"weakLinks"`
	StrongLinks      []SemanticLinkPair `json:"strongLinks"`
	TotalLinks       int                `json:"totalLinks"`
	AvgSemSimilarity float64            `json:"avgSemSimilarity"`
	WeakLinksCount   int                `json:"weakLinksCount"`
	StrongLinksCount int                `json:"strongLinksCount"`
}

// AnalyzeSemanticLinkDistance analyzes the semantic relevance of internal links.
// Uses graph.URLs so no PageData slice is needed.
func AnalyzeSemanticLinkDistance(
	embeddings map[string][]float64,
	graph *AdjacencyGraph,
	weakThreshold float64,
	strongThreshold float64,
) SemanticLinkAnalysis {
	if weakThreshold == 0 {
		weakThreshold = 0.15
	}
	if strongThreshold == 0 {
		strongThreshold = 0.6
	}

	totalLinks := 0
	totalSim := 0.0
	var weakLinks, strongLinks []SemanticLinkPair

	for i := 0; i < graph.N; i++ {
		srcURL := graph.URLs[i]
		srcVec, ok := embeddings[srcURL]
		if !ok {
			continue
		}

		for _, j := range graph.Neighbors(i) {
			if int(j) >= graph.N {
				continue
			}
			targetURL := graph.URLs[j]
			tgtVec, ok := embeddings[targetURL]
			if !ok {
				continue
			}

			sim := CosineSimilarity(srcVec, tgtVec)
			totalLinks++
			totalSim += sim

			rounded := math.Round(sim*1000) / 1000
			if sim < weakThreshold {
				weakLinks = append(weakLinks, SemanticLinkPair{Source: srcURL, Target: targetURL, Similarity: rounded})
			} else if sim > strongThreshold {
				strongLinks = append(strongLinks, SemanticLinkPair{Source: srcURL, Target: targetURL, Similarity: rounded})
			}
		}
	}

	sort.Slice(weakLinks, func(i, j int) bool {
		return weakLinks[i].Similarity < weakLinks[j].Similarity
	})
	sort.Slice(strongLinks, func(i, j int) bool {
		return strongLinks[i].Similarity > strongLinks[j].Similarity
	})

	if len(weakLinks) > 50 {
		weakLinks = weakLinks[:50]
	}
	if len(strongLinks) > 50 {
		strongLinks = strongLinks[:50]
	}

	avgSim := 0.0
	if totalLinks > 0 {
		avgSim = math.Round((totalSim/float64(totalLinks))*1000) / 1000
	}

	return SemanticLinkAnalysis{
		TotalLinks:       totalLinks,
		AvgSemSimilarity: avgSim,
		WeakLinks:        weakLinks,
		WeakLinksCount:   len(weakLinks),
		StrongLinks:      strongLinks,
		StrongLinksCount: len(strongLinks),
	}
}
