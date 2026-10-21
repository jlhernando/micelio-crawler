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

func TestApplyGraphInlinks(t *testing.T) {
	g := &analysis.AdjacencyGraph{
		URLIndex: map[string]int{"a": 0, "b": 1, "c": 2},
		URLs:     []string{"a", "b", "c"},
		Offsets:  []int32{0, 2, 3, 4},
		Edges:    []int32{1, 2, 2, 2}, // a->b, a->c, b->c, c->c (self link ignored)
		N:        3,
	}
	pages := []*types.PageData{{URL: "a"}, {URL: "b"}, {URL: "c"}, {URL: "d", Inlinks: 5}}
	if !applyGraphInlinks(pages, g) {
		t.Fatal("expected changes")
	}
	if pages[0].Inlinks != 0 || pages[1].Inlinks != 1 || pages[2].Inlinks != 2 || pages[3].Inlinks != 5 {
		t.Errorf("inlinks = %d %d %d %d, want 0 1 2 5", pages[0].Inlinks, pages[1].Inlinks, pages[2].Inlinks, pages[3].Inlinks)
	}
}
