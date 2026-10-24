package server

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// refreshAutoSegmentStats re-buckets authority once PageRank and inlinks are on
// the pages, then rebuilds the per-segment stats.
func refreshAutoSegmentStats(pages []*types.PageData, stats *types.CrawlStats) {
	analysis.AssignAuthoritySegments(pages, nil)
	stats.AutoSegmentStats = analysis.BuildAutoSegmentStats(pages)
}

// backfillAutoSegments assigns automatic segments to pages loaded from a crawl
// DB. Segments are computed after the crawl and never persisted with pages, so
// this runs on every load. Must run before stripPagesForCache (needs
// StructuredData and Hreflang). Returns stats to merge into the stats map.
func backfillAutoSegments(pages []*types.PageData, config map[string]interface{}) map[string]interface{} {
	if len(pages) == 0 {
		return nil
	}
	labels := analysis.PropagateTemplateClusters(pages)
	analysis.AssignAutoSegments(pages, labels, analysis.AutoSegmentOptions{Rules: segmentRulesFromConfig(config)})
	segStats := analysis.BuildAutoSegmentStats(pages)
	data, err := json.Marshal(segStats)
	if err != nil {
		return nil
	}
	var out []interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return map[string]interface{}{
		"autoSegmentStats":         out,
		"templateTypeDistribution": analysis.BuildTemplateStats(pages),
	}
}

// segmentRulesFromConfig parses the user's "name:pattern" segment rules from a
// stored crawl config map.
func segmentRulesFromConfig(config map[string]interface{}) []analysis.Segment {
	raw, ok := config["segmentRules"].([]interface{})
	if !ok {
		return nil
	}
	var rules []analysis.Segment
	for _, r := range raw {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		pattern, _ := m["pattern"].(string)
		if name == "" || pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		rules = append(rules, analysis.Segment{Name: name, Pattern: re})
	}
	return rules
}

// filterBySegment keeps pages whose segment matches "dimension:value". The
// "rule" dimension matches the user's regex segment names.
func filterBySegment(pages []*types.PageData, spec string) []*types.PageData {
	dim, value, ok := strings.Cut(spec, ":")
	if !ok || dim == "" {
		return pages
	}
	out := make([]*types.PageData, 0, len(pages)/4)
	for _, p := range pages {
		if pageInSegment(p, dim, value) {
			out = append(out, p)
		}
	}
	return out
}

// pageInSegment reports whether p is in segment dim:value ("rule" = a
// user segment rule name, else an automatic segment dimension).
func pageInSegment(p *types.PageData, dim, value string) bool {
	if dim == "rule" {
		for _, name := range p.Segments {
			if name == value {
				return true
			}
		}
		return false
	}
	return p.AutoSegments.Value(dim) == value
}

// recomputeFlatPageRank repairs crawls whose stored PageRank is the same for
// every page (computed after InternalLinks were stripped). It recomputes from
// the on-disk link graph and assigns the scores to pages. Returns the new
// scores, or nil when PageRank already varies or the graph has no edges.
func recomputeFlatPageRank(pages []*types.PageData, g *analysis.AdjacencyGraph) map[string]float64 {
	if g == nil || len(g.Edges) == 0 || len(pages) < 2 {
		return nil
	}
	first := pages[0].PageRank
	for _, p := range pages[1:] {
		if p.PageRank != first {
			return nil
		}
	}
	scores := analysis.ComputePageRankGraph(g, analysis.PageRankOptions{})
	assignPageRankScores(pages, scores)
	return scores
}

// scoresToJSONMap converts scores to the generic map shape that JSON-decoded
// stats use, so API capping (capStatsForAPI) still recognises the field.
func scoresToJSONMap(scores map[string]float64) map[string]interface{} {
	out := make(map[string]interface{}, len(scores))
	for k, v := range scores {
		out[k] = v
	}
	return out
}
