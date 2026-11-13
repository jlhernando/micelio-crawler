package analysis

import (
	"context"
	"reflect"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// Compact refs give the same sizes, summary and page weights (but the
// per-resource list) as the map form; Expand gives the map back.
func TestResourceRefsCompactMatchesMap(t *testing.T) {
	ten, five := int64(10), int64(5)
	refs := func() map[string][]types.ResourceEntry {
		return map[string][]types.ResourceEntry{
			"https://s.test/a": {
				{URL: "https://s.test/app.css", Type: "stylesheet"},
				{URL: "https://s.test/app.css", Type: "stylesheet"}, // duplicate
				{URL: "https://s.test/logo.svg", Type: "image", SizeBytes: &ten},
				{URL: "https://cdn.test/x", Type: "script"},
			},
			"https://s.test/b": {
				{URL: "https://s.test/app.css", Type: "stylesheet"},
				{URL: "https://cdn.test/x", Type: "font"}, // same URL, other type
				{URL: "https://s.test/logo.svg", Type: "image", SizeBytes: &five},
				{URL: "https://s.test/gone.png", Type: "image"},
			},
			"https://s.test/c":      {},
			"https://s.test/d":      nil,
			"https://s.test/absent": {{URL: "https://s.test/app.css", Type: "stylesheet"}},
		}
	}
	sizer := ResourceSizer{Size: func(_ context.Context, u string) SizeResult {
		if n := map[string]int64{"https://s.test/app.css": 300, "https://cdn.test/x": 70}[u]; n > 0 {
			return SizeResult{Size: n, OK: true}
		}
		return SizeResult{}
	}}
	newPages := func() []*types.PageData {
		return []*types.PageData{{URL: "https://s.test/a", BodySize: 1000}, {URL: "https://s.test/b", BodySize: 2000},
			{URL: "https://s.test/c", BodySize: 3000}, {URL: "https://s.test/d", BodySize: 4000}}
	}
	transfer := map[string]int64{"https://s.test/b": 900}

	m := refs()
	wantSizes, wantSum := FetchResourceSizes(context.Background(), m, PageWeightOptions{Sizer: sizer})
	want := newPages()
	ApplyResourceSizes(want, m, transfer, wantSizes)
	for _, p := range want {
		p.PageWeight.Resources = nil // the compact form does not keep the list
	}

	c := CompactResourceRefs(refs())
	gotSizes, gotSum := FetchResourceSizesCompact(context.Background(), c, PageWeightOptions{Sizer: sizer})
	if !reflect.DeepEqual(gotSizes, wantSizes) || gotSum.Total != wantSum.Total || gotSum.Sized != wantSum.Sized {
		t.Fatalf("sizes %v %+v, want %v %+v", gotSizes, gotSum, wantSizes, wantSum)
	}
	got := newPages()
	ApplyResourceSizesCompact(got, c, transfer, gotSizes)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("page weights differ")
	}
	if len(c.Table) != 6 || !reflect.DeepEqual(c.Expand(), refs()) {
		t.Fatalf("table %d entries (want 6) or Expand differs", len(c.Table))
	}
}
