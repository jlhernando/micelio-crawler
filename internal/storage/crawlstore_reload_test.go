package storage

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// Reload-for-analysis must keep SchemaValidation and RenderDiffs: the reporter
// builds SchemaValidationStats / RenderCompareStats from them after reload.
func TestGetAllPagesKeepsSchemaValidationAndRenderDiffs(t *testing.T) {
	store, err := NewCrawlStore(filepath.Join(t.TempDir(), "reload.db"))
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	page := &types.PageData{
		URL:        "https://example.com/a",
		StatusCode: 200,
		CrawledAt:  time.Now().UTC(),
		SchemaValidation: []types.SchemaValidationEntry{
			{Type: "Product", Format: types.FormatJSONLD, RichResultEligible: true},
		},
		RenderDiffs:   []types.RenderDiff{{Field: "title", Original: "Old", Rendered: "New"}},
		ExternalLinks: []string{"https://other.test/"},
		JSErrors:      []string{"boom"},
	}
	if err := store.InsertPage(page); err != nil {
		t.Fatalf("InsertPage: %v", err)
	}

	pages, err := store.GetAllPages()
	if err != nil || len(pages) != 1 {
		t.Fatalf("GetAllPages: %v (n=%d)", err, len(pages))
	}
	got := pages[0]
	if len(got.SchemaValidation) != 1 || got.SchemaValidation[0].Type != "Product" {
		t.Errorf("SchemaValidation lost on reload: %+v", got.SchemaValidation)
	}
	if len(got.RenderDiffs) != 1 || got.RenderDiffs[0].Field != "title" {
		t.Errorf("RenderDiffs lost on reload: %+v", got.RenderDiffs)
	}
	// Heavy fields are still stripped.
	if got.ExternalLinks != nil || got.JSErrors != nil {
		t.Errorf("heavy fields should still be stripped: ext=%v js=%v", got.ExternalLinks, got.JSErrors)
	}
	if got.ExternalLinkCount != 1 {
		t.Errorf("ExternalLinkCount = %d, want 1", got.ExternalLinkCount)
	}
}

// The per-page hook (GetPagesForAnalysis' each) sees every page in rowid order, with its anchors,
// before the page is kept; what it clears stays cleared in the result.
func TestPageLoadHookOrderAndClear(t *testing.T) {
	store, err := NewCrawlStore(filepath.Join(t.TempDir(), "each.db"))
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()
	for _, u := range []string{"https://example.com/b", "https://example.com/a", "https://example.com/c"} {
		p := &types.PageData{URL: u, StatusCode: 200, CrawledAt: time.Now().UTC(),
			Anchors: []types.AnchorData{{Href: u + "/x", Text: "x " + u, IsInternal: true}}}
		if err := store.InsertPage(p); err != nil {
			t.Fatalf("InsertPage: %v", err)
		}
	}
	var seen []string
	pages, err := store.getAllPages(t.Context(), nil, func(p *types.PageData) {
		if len(p.Anchors) != 1 || p.Anchors[0].Text != "x "+p.URL {
			t.Errorf("hook got anchors %+v for %s", p.Anchors, p.URL)
		}
		seen = append(seen, p.URL)
		p.Anchors = nil
	}, pageLoadOpts{})
	if err != nil || len(pages) != 3 {
		t.Fatalf("getAllPages: %v (n=%d)", err, len(pages))
	}
	for i, p := range pages {
		if seen[i] != p.URL {
			t.Errorf("hook order %v, pages order differs at %d (%s)", seen, i, p.URL)
		}
		if p.Anchors != nil {
			t.Errorf("anchors of %s kept after hook cleared them", p.URL)
		}
	}
	if seen[0] != "https://example.com/b" {
		t.Errorf("hook order %v, want insertion order", seen)
	}
}

// GetPagesForAnalysis with skipAnchors returns the same pages as
// GetAllPagesWithProgress except that Anchors stay nil.
func TestGetPagesForAnalysisSkipAnchorsSameOtherwise(t *testing.T) {
	store, err := NewCrawlStore(filepath.Join(t.TempDir(), "noanchors.db"))
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()
	for _, u := range []string{"https://example.com/b", "https://example.com/a"} {
		p := &types.PageData{URL: u, StatusCode: 200, Depth: 2, CrawledAt: time.Now().UTC(),
			InternalLinks: []string{u + "/x", u + "/y"},
			Anchors:       []types.AnchorData{{Href: u + "/x", Text: "x", IsInternal: true}}}
		if err := store.InsertPage(p); err != nil {
			t.Fatalf("InsertPage: %v", err)
		}
	}
	full, err := store.GetAllPagesWithProgress(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	lean, err := store.GetPagesForAnalysis(t.Context(), nil, nil, true)
	if err != nil || len(lean) != len(full) {
		t.Fatalf("GetPagesForAnalysis: %v (n=%d)", err, len(lean))
	}
	for i := range full {
		if len(full[i].Anchors) != 1 || lean[i].Anchors != nil {
			t.Fatalf("anchors: full %v lean %v", full[i].Anchors, lean[i].Anchors)
		}
		full[i].Anchors = nil
		a, _ := json.Marshal(full[i])
		b, _ := json.Marshal(lean[i])
		if string(a) != string(b) {
			t.Errorf("page %d differs:\n%s\n%s", i, a, b)
		}
		if lean[i].InternalLinkCount != 2 {
			t.Errorf("InternalLinkCount = %d, want 2", lean[i].InternalLinkCount)
		}
	}
}

// GetPagesForAnalysis returns the same pages as the full reload except that
// structured data entries lose Raw (and Anchors with skipAnchors); nil and
// empty structuredData keep their shape.
func TestGetPagesForAnalysisDropsOnlySchemaRaw(t *testing.T) {
	store, err := NewCrawlStore(filepath.Join(t.TempDir(), "noraw.db"))
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()
	sds := [][]types.StructuredDataEntry{
		nil,
		{},
		{{Type: "Product", Format: types.FormatJSONLD, Raw: `{"@type":"Product","name":"é"}`},
			{Type: "BreadcrumbList", Format: types.FormatMicrodata}},
	}
	for i, sd := range sds {
		u := "https://example.com/" + string(rune('a'+i))
		p := &types.PageData{URL: u, StatusCode: 200, CrawledAt: time.Now().UTC(), StructuredData: sd,
			InternalLinks: []string{u + "/x"},
			Anchors:       []types.AnchorData{{Href: u + "/x", Text: "x", IsInternal: true}}}
		if err := store.InsertPage(p); err != nil {
			t.Fatalf("InsertPage: %v", err)
		}
	}
	for _, skip := range []bool{false, true} {
		full, err := store.GetAllPagesWithProgress(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		lean, err := store.GetPagesForAnalysis(t.Context(), nil, nil, skip)
		if err != nil || len(lean) != len(full) {
			t.Fatalf("GetPagesForAnalysis: %v (n=%d)", err, len(lean))
		}
		for i := range full {
			for j := range full[i].StructuredData {
				full[i].StructuredData[j].Raw = ""
			}
			if skip {
				full[i].Anchors = nil
			}
			if !reflect.DeepEqual(full[i], lean[i]) {
				t.Errorf("skip=%v page %d differs:\n%+v\n%+v", skip, i, full[i], lean[i])
			}
		}
		if len(lean[2].StructuredData) != 2 || lean[2].StructuredData[0].Type != "Product" {
			t.Errorf("entries: %#v", lean[2].StructuredData)
		}
	}
	// null, [] and a missing key decode as with the full decoder.
	for _, in := range []string{`{"url":"u","structuredData":null}`, `{"url":"u","structuredData":[]}`, `{"url":"u"}`,
		`{"url":"u","structuredData":[{"type":"A","format":"rdfa","raw":"{}"},{"raw":null}]}`} {
		for _, skip := range []bool{false, true} {
			want, err1 := decodeAnalysisPageOpt([]byte(in), pageLoadOpts{skipAnchors: skip})
			got, err2 := decodeAnalysisPageOpt([]byte(in), pageLoadOpts{skipAnchors: skip, dropSchemaRaw: true})
			if err1 != nil || err2 != nil {
				t.Fatalf("%s: %v %v", in, err1, err2)
			}
			for j := range want.StructuredData {
				want.StructuredData[j].Raw = ""
			}
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%s: %#v vs %#v", in, want.StructuredData, got.StructuredData)
			}
		}
	}
}
