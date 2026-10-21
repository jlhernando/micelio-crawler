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
		if dim == "rule" {
			for _, name := range p.Segments {
				if name == value {
					out = append(out, p)
					break
				}
			}
			continue
		}
		if p.AutoSegments.Value(dim) == value {
			out = append(out, p)
		}
	}
	return out
}

// applyGraphInlinks fills missing inlink counts from the link graph's
// in-degree. Pages reloaded from a crawl DB lose their InternalLinks, so the
// usual inlink index comes back empty. Returns true when any page changed.
func applyGraphInlinks(pages []*types.PageData, g *analysis.AdjacencyGraph) bool {
	if g == nil || g.N == 0 {
		return false
	}
	inDegree := make([]int32, g.N)
	for i := 0; i < g.N; i++ {
		for _, t := range g.Neighbors(i) {
			if int(t) != i {
				inDegree[t]++
			}
		}
	}
	changed := false
	for _, p := range pages {
		if p.Inlinks != 0 {
			continue
		}
		if idx, ok := g.URLIndex[p.URL]; ok && inDegree[idx] > 0 {
			p.Inlinks = int(inDegree[idx])
			changed = true
		}
	}
	return changed
}

// segmentStatsAfterGraph refreshes authority and segment stats once graph
// inlinks are known. Returns the JSON-ready stats value, or nil.
func segmentStatsAfterGraph(pages []*types.PageData, g *analysis.AdjacencyGraph) interface{} {
	if !applyGraphInlinks(pages, g) {
		return nil
	}
	analysis.AssignAuthoritySegments(pages, nil)
	data, err := json.Marshal(analysis.BuildAutoSegmentStats(pages))
	if err != nil {
		return nil
	}
	var out []interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}
