package server

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// Orphan lists are capped at analysis time with the real total stored; the
// API cap must not replace that total with the capped list length.
func TestCapStatsForAPIKeepsAnalysisTotals(t *testing.T) {
	list := make([]interface{}, types.StatsListCap)
	for i := range list {
		list[i] = "https://ex.com/p"
	}
	out := capStatsForAPI(map[string]interface{}{
		"orphanPages":      list,
		"totalOrphanPages": float64(54321),
	})
	if got := statNumber(out["totalOrphanPages"]); got != 54321 {
		t.Errorf("totalOrphanPages = %v, want 54321", got)
	}
	if got := len(out["orphanPages"].([]interface{})); got != 200 {
		t.Errorf("orphanPages len = %d, want 200", got)
	}

	// Old stored stats without a total still get one from the list.
	out = capStatsForAPI(map[string]interface{}{"orphanPages": list})
	if got := statNumber(out["totalOrphanPages"]); got != float64(types.StatsListCap) {
		t.Errorf("legacy totalOrphanPages = %v, want %d", got, types.StatsListCap)
	}
}

func TestOrphanCountFromMap(t *testing.T) {
	cases := []struct {
		stats map[string]interface{}
		want  int
	}{
		{map[string]interface{}{"orphanPages": []interface{}{"a", "b"}}, 2},
		{map[string]interface{}{"orphanPages": []interface{}{"a"}, "totalOrphanPages": float64(9)}, 9},
		{map[string]interface{}{"orphanPages": []string{"a", "b", "c"}, "totalOrphanPages": 3}, 3},
		{map[string]interface{}{}, 0},
	}
	for i, c := range cases {
		if got := orphanCountFromMap(c.stats); got != c.want {
			t.Errorf("case %d: got %d, want %d", i, got, c.want)
		}
	}
}

// Filtered stats never copy the stored PageRank map: they rebuild scores
// (and a histogram) from the subset's pages.
func TestFilteredStatsPageRankFromPages(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://ex.com/a", StatusCode: 200, TemplateType: "blog", PageRank: 4, Inlinks: 1, Depth: 1},
		{URL: "https://ex.com/b", StatusCode: 200, TemplateType: "blog", PageRank: 0.5, Inlinks: 2, Depth: 1},
		{URL: "https://ex.com/c", StatusCode: 200, TemplateType: "product", PageRank: 9, Inlinks: 1, Depth: 1},
	}
	full := map[string]interface{}{
		"pageRankScores": map[string]interface{}{"https://ex.com/a": 1.0, "https://ex.com/zzz": 2.0},
	}
	res := computeTemplateFilteredStats(full, pages, "blog")
	pr, _ := res.Stats["pageRankScores"].(map[string]interface{})
	if len(pr) != 2 || pr["https://ex.com/a"] != 4.0 {
		t.Errorf("pageRankScores = %v", pr)
	}
	h, _ := res.Stats["pageRankHistogram"].(map[string]interface{})
	if h == nil || h["total"] != 2.0 {
		t.Errorf("pageRankHistogram = %v", h)
	}
}
