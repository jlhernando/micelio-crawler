package server

import (
	"encoding/json"
	"log"
	"math"

	"github.com/micelio/micelio/internal/alerts"
	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// loadedAggregates holds what a reload computes from pages and the link graph.
type loadedAggregates struct {
	anchorStats  map[string]interface{}
	dirTree      map[string]interface{}
	tmplTypes    []string
	inlinkIdx    *inlinkIndex
	graph        *analysis.AdjacencyGraph
	statsUpdates map[string]interface{} // nil value = delete key
	persist      bool                   // stats changed in a way worth saving
}

// prepareLoadedPages finishes pages reloaded from a crawl DB. Order matters:
// the link graph is built first, then PageRank and inlinks, then segments and
// the directory tree, so every aggregate sees the final values. stats is read
// but not modified; changes come back in statsUpdates. Must run before
// stripPagesForCache (anchors and segments need the full pages).
func prepareLoadedPages(crawlID string, pages []*types.PageData, stats map[string]interface{}, dbPath, seedURL string, jobConfig map[string]interface{}) loadedAggregates {
	agg := loadedAggregates{statsUpdates: map[string]interface{}{}}
	merged := make(map[string]interface{}, len(stats)+4)
	for k, v := range stats {
		merged[k] = v
	}
	set := func(k string, v interface{}) {
		merged[k] = v
		agg.statsUpdates[k] = v
	}

	// Crawls analysed by this version stored the link graph and per-page
	// scores in the crawl DB: no second decode of every page for links, no
	// PageRank map in stats, no HITS recompute.
	storedGraph, scoresOK := loadStoredAnalysis(dbPath, pages)
	agg.graph = storedGraph
	if agg.graph == nil {
		agg.graph = buildGraphFromCrawlDB(pages, dbPath)
	}
	if !scoresOK {
		assignPageRankFromStatsMap(pages, merged)
		if scores := recomputeFlatPageRank(pages, agg.graph); scores != nil {
			for k, v := range pageRankStatsUpdates(scores) {
				set(k, v)
			}
			agg.persist = true
			log.Printf("backfill: recomputed flat PageRank for crawl %s from link graph", crawlID)
		}
		if restoreHits(pages, agg.graph) {
			log.Printf("backfill: recomputed authority/hub scores for crawl %s from link graph", crawlID)
		}
	}
	if (storedGraph == nil || !scoresOK) && agg.graph != nil {
		// Older crawl: store what was just rebuilt so the next reload is
		// fast, then drop the per-URL PageRank map from stats.
		if snapshotAnalysis(pages, agg.graph, storedGraph == nil).save(dbPath) {
			if _, hasMap := merged["pageRankScores"]; hasMap && len(pages) > types.PageRankScoresInlineMax {
				set("pageRankScores", nil)
				agg.persist = true
			}
			if _, ok := merged["pageRankHistogram"]; !ok {
				h := types.NewPageRankHistogram(func(yield func(float64)) {
					for _, p := range pages {
						yield(p.PageRank)
					}
				})
				set("pageRankHistogram", toJSONValue(h))
				agg.persist = true
			}
			log.Printf("backfill: stored link graph and page scores for crawl %s in its crawl DB", crawlID)
		}
	}

	agg.inlinkIdx = newInlinkIndex(agg.graph, pages)
	assignInlinks(pages, agg.inlinkIdx)

	// Automatic segments are not stored with pages; recompute on every load.
	// Authority buckets use the PageRank and inlinks assigned above.
	for k, v := range backfillAutoSegments(pages, jobConfig) {
		set(k, v)
	}

	agg.anchorStats = anchorStatsFor(stats, pages)
	agg.dirTree = buildDirectoryTree(pages)
	agg.tmplTypes = collectTemplateTypes(pages)

	if agg.graph != nil && len(stats) > 0 {
		if backfillGraphOrphanStats(crawlID, merged, agg.graph, seedURL, pages) {
			for _, k := range []string{"orphanPages", "totalOrphanPages", "orphanMethodology", "alertSummary"} {
				agg.statsUpdates[k] = merged[k] // nil when removed
			}
			agg.persist = true
		}
	}
	return agg
}

// applyStatsUpdates writes updates into stats; nil values delete the key.
func applyStatsUpdates(stats map[string]interface{}, updates map[string]interface{}) {
	if stats == nil {
		return
	}
	for k, v := range updates {
		if v == nil {
			delete(stats, k)
		} else {
			stats[k] = v
		}
	}
}

// buildGraphFromCrawlDB reads the link graph from the crawl DB (pages
// reloaded from it no longer carry InternalLinks).
func buildGraphFromCrawlDB(pages []*types.PageData, dbPath string) *analysis.AdjacencyGraph {
	if dbPath == "" || len(pages) == 0 {
		return nil
	}
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		return nil
	}
	defer cs.Close()
	return analysis.BuildAdjacencyListFromDisk(pages, func(fn func(source string, targets []string)) error {
		return cs.IterateInternalLinks(fn)
	})
}

// backfillGraphOrphanStats repairs stored stats from crawls whose orphan list
// came from the reporter's link arrays (empty after reload, so nearly every
// page was flagged). It recomputes orphans from the graph, then re-evaluates
// alerts that were built from the bogus list. Returns
// true when stats changed. Cheap no-op for crawls that are already correct.
func backfillGraphOrphanStats(crawlID string, stats map[string]interface{}, g *analysis.AdjacencyGraph, seedURL string, pages []*types.PageData) bool {
	if stats == nil || g == nil {
		return false
	}
	changed := false
	if methodology, _ := stats["orphanMethodology"].(string); methodology != "graph" {
		oldCount := orphanCountFromMap(stats)
		var typed types.CrawlStats
		typed.SetOrphanPages(analysis.ComputeGraphOrphans(g, seedURL))
		orphans := typed.OrphanPages
		if orphans == nil {
			orphans = []string{}
		}
		stats["orphanPages"] = orphans
		stats["totalOrphanPages"] = typed.TotalOrphanPages
		stats["orphanMethodology"] = "graph"
		changed = true
		log.Printf("backfill: recomputed orphans for crawl %s (%d → %d, methodology: graph)", crawlID, oldCount, typed.TotalOrphanPages)
	}
	if !changed && !orphanAlertStale(stats) {
		return false
	}

	typed := statsMapToCrawlStats(stats)
	if summary := alerts.Evaluate(typed); summary != nil {
		stats["alertSummary"] = toJSONValue(summary)
	} else {
		delete(stats, "alertSummary")
	}
	log.Printf("backfill: re-evaluated alerts for crawl %s after orphan correction", crawlID)
	return true
}

// orphanAlertStale reports whether the stored orphan alert disagrees with the
// stored orphan list (e.g. alerts evaluated before graph orphans existed).
func orphanAlertStale(stats map[string]interface{}) bool {
	total, _ := stats["totalPages"].(float64)
	if total <= 0 {
		return false
	}
	rate := float64(orphanCountFromMap(stats)) / total * 100
	var stored *float64
	if summary, ok := stats["alertSummary"].(map[string]interface{}); ok {
		list, _ := summary["alerts"].([]interface{})
		for _, raw := range list {
			a, ok := raw.(map[string]interface{})
			if !ok || a["id"] != "orphan-pages" {
				continue
			}
			if v, ok := a["value"].(float64); ok {
				stored = &v
			}
		}
	}
	if stored == nil {
		// Alert missing: stale only if the current list would trigger it.
		return rate > 5
	}
	return rate <= 5 || math.Abs(*stored-rate) > 0.05
}

// orphanCountFromMap returns the orphan total from stored stats: the
// totalOrphanPages count when the list was capped, else the list length.
func orphanCountFromMap(stats map[string]interface{}) int {
	n := lenAny(stats["orphanPages"])
	switch t := stats["totalOrphanPages"].(type) {
	case float64:
		n = max(n, int(t))
	case int:
		n = max(n, t)
	}
	return n
}

// pageRankStatsUpdates returns the stats keys describing recomputed
// PageRank: the histogram always, the URL map only for small crawls (nil
// deletes a stale map left by an older version).
func pageRankStatsUpdates(scores map[string]float64) map[string]interface{} {
	var tmp types.CrawlStats
	tmp.PageRankHistogram = types.NewPageRankHistogram(func(yield func(float64)) {
		for _, v := range scores {
			yield(v)
		}
	})
	out := map[string]interface{}{"pageRankHistogram": toJSONValue(tmp.PageRankHistogram), "pageRankScores": nil}
	if len(scores) <= types.PageRankScoresInlineMax {
		out["pageRankScores"] = scoresToJSONMap(scores)
	}
	return out
}

// lenAny returns the length of a JSON-decoded or typed string list.
func lenAny(v interface{}) int {
	switch l := v.(type) {
	case []interface{}:
		return len(l)
	case []string:
		return len(l)
	}
	return 0
}

// toJSONValue converts a typed value to its generic JSON-decoded form so it
// sits in a stats map like the rest of the stored fields.
func toJSONValue(v interface{}) interface{} {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// restoreHits recomputes HITS authority and hub scores. They are computed
// after pages are written to the crawl DB, so pages reloaded from it have
// none and every authority/hub view showed 0. Same limits as the crawl:
// skipped above 200K pages, 30 iterations above 100K. Returns true when
// scores were assigned.
func restoreHits(pages []*types.PageData, g *analysis.AdjacencyGraph) bool {
	if g == nil || g.N != len(pages) || g.N == 0 || g.N > 200_000 {
		return false
	}
	for _, p := range pages {
		if li := p.LinkIntelligence; li != nil && (li.AuthorityScore > 0 || li.HubScore > 0) {
			return false // already present
		}
	}
	iter := 100
	if g.N > 100_000 {
		iter = 30
	}
	scores := analysis.ComputeHits(iter, 1e-6, g)
	for i, p := range pages {
		s, ok := scores[g.URLs[i]]
		if !ok {
			continue
		}
		if p.LinkIntelligence == nil {
			p.LinkIntelligence = &types.LinkIntelligenceData{}
		}
		p.LinkIntelligence.AuthorityScore = s.AuthorityScore
		p.LinkIntelligence.HubScore = s.HubScore
	}
	return len(scores) > 0
}
