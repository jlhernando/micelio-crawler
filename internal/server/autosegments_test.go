package server

import (
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

func TestFilterBySegment(t *testing.T) {
	pages := []*types.PageData{
		{URL: "a", AutoSegments: &types.PageSegments{Dir1: "/blog/", Template: "blog_post"}, Segments: []string{"news"}},
		{URL: "b", AutoSegments: &types.PageSegments{Dir1: "/shop/", Template: "pdp"}},
		{URL: "c"},
	}
	if got := filterBySegment(pages, "dir1:/blog/"); len(got) != 1 || got[0].URL != "a" {
		t.Errorf("dir1 filter = %v", got)
	}
	if got := filterBySegment(pages, "template:pdp"); len(got) != 1 || got[0].URL != "b" {
		t.Errorf("template filter = %v", got)
	}
	if got := filterBySegment(pages, "rule:news"); len(got) != 1 || got[0].URL != "a" {
		t.Errorf("rule filter = %v", got)
	}
	if got := filterBySegment(pages, "malformed"); len(got) != 3 {
		t.Errorf("malformed spec should not filter, got %d", len(got))
	}
}

func TestBackfillAutoSegmentsUsesConfigRules(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://ex.com/blog/a", StatusCode: 200, WordCount: 500, TemplateType: "article"},
		{URL: "https://ex.com/x", StatusCode: 200, WordCount: 50, TemplateType: "other"},
	}
	cfg := map[string]interface{}{"segmentRules": []interface{}{
		map[string]interface{}{"name": "blog", "pattern": "/blog/"},
		map[string]interface{}{"name": "bad", "pattern": "("},
	}}
	out := backfillAutoSegments(pages, cfg)
	if out == nil || out["autoSegmentStats"] == nil {
		t.Fatal("expected autoSegmentStats in backfill output")
	}
	if pages[0].AutoSegments == nil || pages[0].AutoSegments.Template != "blog_post" {
		t.Errorf("segments not assigned: %+v", pages[0].AutoSegments)
	}
	if len(pages[0].Segments) != 1 || pages[0].Segments[0] != "blog" {
		t.Errorf("rule segments = %v", pages[0].Segments)
	}
}

func TestRecomputeFlatPageRank(t *testing.T) {
	pages := []*types.PageData{{URL: "a", PageRank: 10}, {URL: "b", PageRank: 10}, {URL: "c", PageRank: 10}}
	g := analysis.BuildAdjacencyListFromDisk(pages, func(fn func(string, []string)) error {
		fn("a", []string{"b", "c"})
		fn("b", []string{"c"})
		return nil
	})
	if recomputeFlatPageRank(pages, g) == nil {
		t.Fatal("flat PageRank should be recomputed")
	}
	if !(pages[2].PageRank > pages[1].PageRank && pages[1].PageRank > pages[0].PageRank) {
		t.Errorf("PageRank order wrong: a=%v b=%v c=%v", pages[0].PageRank, pages[1].PageRank, pages[2].PageRank)
	}
	if recomputeFlatPageRank(pages, g) != nil {
		t.Error("varied PageRank must not be recomputed")
	}
}
