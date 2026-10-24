package server

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// filteredStatsResult is the payload for GET /api/crawl/{id}/stats/filtered.
//
// Stats holds only the keys that can be computed correctly for the subset.
// Everything else is left out on purpose: cached pages have been stripped
// (stripPagesForCache) and, after link intelligence, replaced by small stubs,
// so images, Open Graph, schema, hreflang, content hashes, GSC/GA4 data,
// URL classification and similar fields are gone. Re-running the full
// reporter on them reports those metrics as zero. The dashboard merges these
// keys over the unfiltered stats and labels the rest as whole-site values.
type filteredStatsResult struct {
	Stats      map[string]interface{}
	PageCount  int
	ScopedKeys []string
}

// computeTemplateFilteredStats builds stats for the pages of one template.
// It only reads pages and full; it never mutates either.
//
// Per-page fields used (all survive stripPagesForCache and the LI stub):
// URL, StatusCode, RobotsBlocked, Depth, TemplateType, Indexability, Title,
// MetaDescription, Headings.H1, WordCount, ResponseTimeMs, RedirectChain,
// InternalLinks/InternalLinkCount, ExternalLinks/ExternalLinkCount, PageRank,
// AutoSegments, Inlinks.
//
// URL lists that were computed from fields no longer on cached pages
// (orphans, broken links, soft 404s, security lists, URL issues, PageRank
// scores) are derived by filtering the full stats to the subset's URLs.
func computeTemplateFilteredStats(full map[string]interface{}, pages []*types.PageData, template string) filteredStatsResult {
	subset := make([]*types.PageData, 0)
	inSubset := make(map[string]bool)
	for _, p := range pages {
		if p != nil && p.TemplateType == template {
			subset = append(subset, p)
			inSubset[p.URL] = true
		}
	}

	// Title/description duplicate counts are site-wide: a page in the subset
	// counts as duplicate when its text appears on another crawled page.
	titleCounts := make(map[string]int)
	descCounts := make(map[string]int)
	for _, p := range pages {
		if p == nil || p.RobotsBlocked || p.StatusCode != 200 {
			continue
		}
		if p.Title != nil && p.Title.Text != "" {
			titleCounts[p.Title.Text]++
		}
		if p.MetaDescription != nil && p.MetaDescription.Text != "" {
			descCounts[p.MetaDescription.Text]++
		}
	}

	var (
		totalPages, robotsBlocked                        int
		withoutTitle, withoutDesc, withoutH1, multipleH1 int
		titleTooLong, titleTooShort, descTooLong         int
		dupTitle, dupDesc                                int
		totalInternal, totalExternal                     int
		indexable, nonIndexable                          int
		statusCodes                                      = make(map[int]int)
		depthDist                                        = make(map[int]int)
		indexReasons                                     = make(map[string]int)
		responseTimes                                    []float64
		buckets                                          types.ResponseTimeBuckets
		slowPages                                        []types.URLResponseTime
		thinPages                                        []types.URLWordCount
		redirectChains, longRedirectChains               []types.RedirectChainInfo
		pageRankFromPages                                = make(map[string]float64)
		pageRankHist                                     = &types.PageRankHistogram{Buckets: make([]int, len(types.PageRankBucketBounds))}
		subsetOrphans                                    []string
	)

	for _, p := range subset {
		if p.PageRank > 0 {
			// Pages carry their score; the URL map is only kept for small
			// subsets (same rule as the stored stats).
			if len(pageRankFromPages) <= types.PageRankScoresInlineMax {
				pageRankFromPages[p.URL] = p.PageRank
			}
			pageRankHist.Add(p.PageRank)
		}
		if !p.RobotsBlocked && p.StatusCode == 200 && p.Inlinks == 0 && p.Depth > 0 {
			subsetOrphans = append(subsetOrphans, p.URL)
		}
		if p.RobotsBlocked {
			robotsBlocked++
			continue
		}
		totalPages++
		statusCodes[p.StatusCode]++
		depthDist[p.Depth]++

		if p.Title == nil || p.Title.Text == "" {
			withoutTitle++
		}
		if p.MetaDescription == nil || p.MetaDescription.Text == "" {
			withoutDesc++
		}
		if len(p.Headings.H1) == 0 {
			withoutH1++
		}
		if p.StatusCode == 200 {
			if len(p.Headings.H1) > 1 {
				multipleH1++
			}
			if p.Title != nil && p.Title.Text != "" {
				if titleCounts[p.Title.Text] > 1 {
					dupTitle++
				}
				if p.Title.Length > 60 {
					titleTooLong++
				} else if p.Title.Length > 0 && p.Title.Length < 30 {
					titleTooShort++
				}
			}
			if p.MetaDescription != nil && p.MetaDescription.Text != "" {
				if descCounts[p.MetaDescription.Text] > 1 {
					dupDesc++
				}
				if p.MetaDescription.Length > 160 {
					descTooLong++
				}
			}
			if p.WordCount > 0 && p.WordCount < 200 {
				thinPages = append(thinPages, types.URLWordCount{URL: p.URL, WordCount: p.WordCount})
			}
		}

		if len(p.InternalLinks) > 0 {
			totalInternal += len(p.InternalLinks)
		} else {
			totalInternal += p.InternalLinkCount
		}
		if len(p.ExternalLinks) > 0 {
			totalExternal += len(p.ExternalLinks)
		} else {
			totalExternal += p.ExternalLinkCount
		}

		if p.ResponseTimeMs > 0 {
			rt := float64(p.ResponseTimeMs)
			responseTimes = append(responseTimes, rt)
			switch {
			case rt < 500:
				buckets.Fast++
			case rt < 1000:
				buckets.Medium++
			case rt < 2000:
				buckets.Slow++
			default:
				buckets.Slowest++
			}
		}
		if p.ResponseTimeMs > 3000 {
			slowPages = append(slowPages, types.URLResponseTime{URL: p.URL, ResponseTimeMs: p.ResponseTimeMs})
		}

		if n := len(p.RedirectChain); n > 0 {
			redirectChains = append(redirectChains, types.RedirectChainInfo{URL: p.URL, Chain: p.RedirectChain, Hops: n})
			if n > 2 {
				longRedirectChains = append(longRedirectChains, types.RedirectChainInfo{URL: p.URL, Hops: n})
			}
		}

		if p.Indexability.Indexable {
			indexable++
		} else {
			nonIndexable++
			if p.Indexability.Reason != "" {
				indexReasons[p.Indexability.Reason]++
			}
		}
	}

	var percentiles types.PercentileData
	if len(responseTimes) > 0 {
		sort.Float64s(responseTimes)
		percentiles = types.PercentileData{
			P50: subsetPercentile(responseTimes, 50),
			P90: subsetPercentile(responseTimes, 90),
			P99: subsetPercentile(responseTimes, 99),
		}
	}
	sort.Slice(slowPages, func(i, j int) bool { return slowPages[i].ResponseTimeMs > slowPages[j].ResponseTimeMs })
	if len(slowPages) > 50 {
		slowPages = slowPages[:50]
	}
	totalThin := len(thinPages)
	sort.Slice(thinPages, func(i, j int) bool { return thinPages[i].WordCount < thinPages[j].WordCount })
	if len(thinPages) > 50 {
		thinPages = thinPages[:50]
	}

	templateDist := map[string]int{}
	if len(subset) > 0 {
		templateDist[template] = len(subset)
	}

	typed := map[string]interface{}{
		"totalPages":                totalPages,
		"robotsBlockedCount":        robotsBlocked,
		"statusCodes":               statusCodes,
		"depthDistribution":         depthDist,
		"templateTypeDistribution":  templateDist,
		"indexabilityStats":         types.IndexabilityStats{Indexable: indexable, NonIndexable: nonIndexable, Reasons: indexReasons},
		"pagesWithoutTitle":         withoutTitle,
		"pagesWithoutDescription":   withoutDesc,
		"pagesWithoutH1":            withoutH1,
		"multipleH1Count":           multipleH1,
		"titleTooLongCount":         titleTooLong,
		"titleTooShortCount":        titleTooShort,
		"descriptionTooLongCount":   descTooLong,
		"duplicateTitleCount":       dupTitle,
		"duplicateDescriptionCount": dupDesc,
		"totalInternalLinks":        totalInternal,
		"totalExternalLinks":        totalExternal,
		"responseTimePercentiles":   percentiles,
		"responseTimeBuckets":       buckets,
		"slowPages":                 nonNilSlice(slowPages),
		"thinContentPages":          nonNilSlice(thinPages),
		"totalThinContentPages":     totalThin,
		"redirectChains":            nonNilSlice(redirectChains),
		"longRedirectChains":        nonNilSlice(longRedirectChains),
		"urlStructureStats":         analysis.BuildURLStructureStats(subset),
	}
	// Some cached pages lose their link counts (slices freed, counts not
	// kept). A zero subset total next to a non-zero site total means the
	// data is missing, not that the pages have no links: omit it.
	if totalInternal == 0 && statNumber(full["totalInternalLinks"]) > 0 {
		delete(typed, "totalInternalLinks")
	}
	if totalExternal == 0 && statNumber(full["totalExternalLinks"]) > 0 {
		delete(typed, "totalExternalLinks")
	}
	if seg := analysis.BuildAutoSegmentStats(subset); len(seg) > 0 {
		typed["autoSegmentStats"] = seg
	}

	// Round-trip through JSON so values match the shape of the stored
	// (JSON-decoded) full stats and capStatsForAPI can handle them.
	out := make(map[string]interface{}, len(typed)+12)
	if data, err := json.Marshal(typed); err == nil {
		_ = json.Unmarshal(data, &out)
	}

	// Lists derived from the full stats, filtered to the subset's URLs.
	// Values in full may be typed (set by backfills) or JSON-decoded, so
	// normalize each one to its JSON-decoded form first.
	orphanTotal := orphanCountFromMap(full)
	full = normalizeStatsKeys(full, "orphanPages", "soft404Pages", "nonHttpsPages",
		"mixedContentPages", "brokenLinks", "urlIssueStats")
	for _, key := range []string{"orphanPages", "soft404Pages", "nonHttpsPages", "mixedContentPages"} {
		if list, ok := full[key].([]interface{}); ok {
			out[key] = filterURLList(list, inSubset)
		}
	}
	// The stored orphan list is capped (types.StatsListCap). When it was cut,
	// filtering it would undercount: derive the subset's orphans from the
	// pages' graph inlink counts instead.
	if list, ok := full["orphanPages"].([]interface{}); ok && orphanTotal > len(list) {
		kept := make([]interface{}, 0, min(len(subsetOrphans), types.StatsListCap))
		for _, u := range subsetOrphans[:min(len(subsetOrphans), types.StatsListCap)] {
			kept = append(kept, u)
		}
		out["orphanPages"] = kept
		out["totalOrphanPages"] = len(subsetOrphans)
	}
	if list, ok := full["brokenLinks"].([]interface{}); ok {
		kept := make([]interface{}, 0)
		for _, item := range list {
			if m, ok := item.(map[string]interface{}); ok {
				if u, _ := m["url"].(string); inSubset[u] {
					kept = append(kept, item)
				}
			}
		}
		out["brokenLinks"] = kept
	}
	if issues, ok := full["urlIssueStats"].(map[string]interface{}); ok {
		filtered := make(map[string]interface{})
		for issue, v := range issues {
			if list, ok := v.([]interface{}); ok {
				if kept := filterURLList(list, inSubset); len(kept) > 0 {
					filtered[issue] = kept
				}
			}
		}
		out["urlIssueStats"] = filtered
	}
	// PageRank comes from the pages (reloads assign it from the stored
	// scores), so the full stats map is never copied or re-encoded here.
	if pageRankHist.Total > 0 {
		out["pageRankHistogram"] = toJSONValue(pageRankHist)
		if pageRankHist.Total <= types.PageRankScoresInlineMax {
			out["pageRankScores"] = scoresToJSONMap(pageRankFromPages)
		}
	}

	// capStatsForAPI adds total* counts only when a list is truncated. Set
	// them always so the dashboard never falls back to the site-wide total.
	for key, countKey := range map[string]string{
		"redirectChains":     "totalRedirectChains",
		"longRedirectChains": "totalLongRedirectChains",
		"brokenLinks":        "totalBrokenLinks",
		"nonHttpsPages":      "totalNonHttpsPages",
	} {
		if list, ok := out[key].([]interface{}); ok {
			out[countKey] = len(list)
		}
	}
	if list, ok := out["orphanPages"].([]interface{}); ok {
		if _, set := out["totalOrphanPages"]; !set {
			out["totalOrphanPages"] = len(list)
		}
	}
	if pageRankHist.Total > 0 {
		out["totalPageRankEntries"] = pageRankHist.Total
	}

	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return filteredStatsResult{Stats: out, PageCount: len(subset), ScopedKeys: keys}
}

// normalizeStatsKeys returns a new map holding the given keys of stats in
// their JSON-decoded form ([]interface{}, map[string]interface{}, ...).
func normalizeStatsKeys(stats map[string]interface{}, keys ...string) map[string]interface{} {
	out := make(map[string]interface{}, len(keys))
	for _, k := range keys {
		v, ok := stats[k]
		if !ok || v == nil {
			continue
		}
		data, err := json.Marshal(v)
		if err != nil {
			continue
		}
		var decoded interface{}
		if json.Unmarshal(data, &decoded) == nil {
			out[k] = decoded
		}
	}
	return out
}

func filterURLList(list []interface{}, keep map[string]bool) []interface{} {
	out := make([]interface{}, 0)
	for _, item := range list {
		if u, ok := item.(string); ok && keep[u] {
			out = append(out, u)
		}
	}
	return out
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func subsetPercentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p / 100.0) * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

// statNumber reads a numeric stats value that may be JSON-decoded (float64)
// or typed (int).
func statNumber(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
