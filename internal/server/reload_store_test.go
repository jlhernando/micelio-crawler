package server

import (
	"context"
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// The dashboard reload drops JSON-LD Raw and folds Images into ImageSummary,
// streams anchors and drops them, and the wrapper keeps the store's optional methods.
func TestAnalysisReloadStoreLeanPages(t *testing.T) {
	cs, err := storage.NewCrawlStore(t.TempDir() + "/crawl.db")
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	alt := "logo"
	page := &types.PageData{URL: "https://ex.com/", StatusCode: 200,
		Images: []types.ImageData{{Src: "/a.png", Alt: &alt}, {Src: "/b.png"}},
		StructuredData: []types.StructuredDataEntry{{Type: "Product", Format: types.FormatJSONLD,
			Raw: `{"@type":"Product","name":"x"}`}},
		Anchors: []types.AnchorData{{Href: "https://ex.com/a", Text: "A", IsInternal: true}},
	}
	if err := cs.InsertPage(page); err != nil {
		t.Fatal(err)
	}

	var anchors *analysis.AnchorStream
	var store crawler.CrawlStore = analysisReloadStore{cs, &anchors}
	if _, ok := store.(interface{ SaveMeta(key, value string) error }); !ok {
		t.Fatal("wrapper lost SaveMeta")
	}
	pages, err := store.GetAllPagesWithProgress(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages", len(pages))
	}
	p := pages[0]
	if p.Images != nil {
		t.Errorf("Images kept: %v", p.Images)
	}
	if p.ImageSummary == nil || p.ImageSummary.Total != 2 || p.ImageSummary.NoAltAttr != 1 {
		t.Errorf("ImageSummary = %+v", p.ImageSummary)
	}
	if len(p.StructuredData) != 1 || p.StructuredData[0].Raw != "" ||
		p.StructuredData[0].Type != "Product" || p.StructuredData[0].Format != types.FormatJSONLD {
		t.Errorf("StructuredData = %+v", p.StructuredData)
	}
	if p.Anchors != nil || anchors.Pages() != 1 {
		t.Errorf("Anchors = %+v, streamed pages %d", p.Anchors, anchors.Pages())
	}
}
