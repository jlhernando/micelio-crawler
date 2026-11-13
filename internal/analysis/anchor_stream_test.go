package analysis

import (
	"reflect"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// Streaming anchors page by page (Anchors dropped) gives the same report as
// keeping every page's Anchors in memory.
func TestAnchorStreamMatchesInMemoryAnchors(t *testing.T) {
	nf := "nofollow"
	long := strings.Repeat("palabra ", 15)
	mk := func() []*types.PageData {
		var pages []*types.PageData
		texts := []string{"Click here", "", "  https://ex.com/b", "https://ex.com/b ", "Shoes", "shoes ", "SHOES",
			"read more", long, "Boots", "x", "Más info", "here"}
		for i := 0; i < 12; i++ {
			p := &types.PageData{URL: "https://ex.com/p" + string(rune('a'+i)), StatusCode: 200}
			if i%5 == 4 {
				p.StatusCode = 404
			}
			if i == 7 {
				p.RobotsBlocked = true
			}
			for j, tx := range texts {
				href := "https://ex.com/p" + string(rune('a'+(i+j)%12))
				if j%3 == 0 {
					href += "/"
				}
				a := types.AnchorData{Href: href, Text: tx, IsInternal: j != 9, IsNonDescriptive: j == 7}
				if j == 4 {
					a.Rel = &nf
				}
				p.Anchors = append(p.Anchors, a)
				if i%2 == 0 {
					p.Anchors = append(p.Anchors, a) // duplicates
				}
			}
			for k := 0; k < 4; k++ { // over-optimized "shoes" towards pa
				p.Anchors = append(p.Anchors, types.AnchorData{Href: "https://ex.com/pa", Text: "Shoes", IsInternal: true})
			}
			pages = append(pages, p)
		}
		return pages
	}
	mem := mk()
	want := GenerateReport(mem, 0, ReportConfig{SeedURL: "https://ex.com/pa"})

	streamed := mk()
	st := NewAnchorStream()
	for _, p := range streamed {
		st.AddPage(p)
		p.Anchors = nil
	}
	got := GenerateReport(streamed, 0, ReportConfig{SeedURL: "https://ex.com/pa", AnchorStream: st, AnchorAggregator: NewAnchorAggregator()})

	if want.AnchorCloud == nil || len(want.NonDescriptiveAnchors) == 0 || want.AnchorHealthStats == nil {
		t.Fatalf("fixture too weak: %+v %+v %+v", want.AnchorCloud, want.NonDescriptiveAnchors, want.AnchorHealthStats)
	}
	if !reflect.DeepEqual(got.NonDescriptiveAnchors, want.NonDescriptiveAnchors) {
		t.Errorf("non-descriptive:\n got %+v\nwant %+v", got.NonDescriptiveAnchors, want.NonDescriptiveAnchors)
	}
	if !reflect.DeepEqual(got.AnchorCloud, want.AnchorCloud) {
		t.Errorf("cloud:\n got %+v\nwant %+v", got.AnchorCloud, want.AnchorCloud)
	}
	if !reflect.DeepEqual(got.AnchorHealthStats, want.AnchorHealthStats) {
		t.Errorf("health stats:\n got %+v\nwant %+v", got.AnchorHealthStats, want.AnchorHealthStats)
	}
	over := 0
	for i := range mem {
		if mem[i].AnchorHealth != nil && mem[i].AnchorHealth.OverOptimizedAnchors > 0 && mem[i].AnchorHealth.NakedURLAnchors > 0 {
			over++
		}
		if !reflect.DeepEqual(streamed[i].AnchorHealth, mem[i].AnchorHealth) {
			t.Errorf("%s health: got %+v want %+v", mem[i].URL, streamed[i].AnchorHealth, mem[i].AnchorHealth)
		}
	}
	if over == 0 {
		t.Error("fixture has no over-optimized page with naked URLs")
	}
}
