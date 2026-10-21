package storage

import (
	"path/filepath"
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
