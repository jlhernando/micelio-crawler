package server

import (
	"encoding/json"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// Anchor Cloud stats persisted in the stats JSON must be served after a
// restart, when reloaded pages no longer carry Anchors.
func TestAnchorStatsForUsesPersistedCloud(t *testing.T) {
	stats := types.CrawlStats{AnchorCloud: &types.AnchorCloudStats{
		Anchors:     []types.AnchorCloudEntry{{Text: "home", Count: 12, IsInternal: true}},
		TotalUnique: 7,
	}}
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	var statsMap map[string]interface{}
	if err := json.Unmarshal(data, &statsMap); err != nil {
		t.Fatal(err)
	}
	pages := []*types.PageData{{URL: "https://ex.com/", StatusCode: 200}} // no Anchors

	got := anchorStatsFor(statsMap, pages)
	out, _ := json.Marshal(got)
	var resp struct {
		Anchors []struct {
			Text  string `json:"text"`
			Count int    `json:"count"`
		} `json:"anchors"`
		TotalUnique int `json:"totalUnique"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.TotalUnique != 7 || len(resp.Anchors) != 1 || resp.Anchors[0].Text != "home" || resp.Anchors[0].Count != 12 {
		t.Errorf("unexpected anchor stats: %s", out)
	}

	// Old crawls without a persisted cloud fall back to page Anchors.
	pages[0].Anchors = []types.AnchorData{{Href: "https://ex.com/a", Text: "Shoes", IsInternal: true}}
	fallback := anchorStatsFor(map[string]interface{}{}, pages)
	if fallback["totalUnique"] != 1 {
		t.Errorf("fallback totalUnique = %v, want 1", fallback["totalUnique"])
	}
}
