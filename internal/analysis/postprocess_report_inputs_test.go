package analysis

import (
	"context"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// RunCoreAnalysis must report external link results, schema validation and
// render diffs before it strips the per-page fields.
func TestRunCoreAnalysisReportsExternalSchemaAndRenderInputs(t *testing.T) {
	product := "Product"
	pages := []*types.PageData{
		{
			URL: "https://example.com/", StatusCode: 200,
			SchemaValidation: []types.SchemaValidationEntry{
				{Type: "Product", Format: types.FormatJSONLD, RichResultEligible: true, RichResultType: &product},
			},
			RenderDiffs: []types.RenderDiff{
				{Field: "title", Original: "Raw", Rendered: "Rendered"},
				{Field: "h1", Original: "", Rendered: "Hello"},
				{Field: "meta_robots", Original: "index", Rendered: "noindex"},
			},
		},
		{URL: "https://example.com/b", StatusCode: 200},
	}
	cfg := PostProcessConfig{
		SeedURL: "https://example.com/",
		ExternalLinkResults: []types.ExternalLinkResult{
			{URL: "https://gone.test/", StatusCode: 404, FoundOn: []string{"https://example.com/"}},
			{URL: "https://dns.test/", Error: "no such host", FoundOn: []string{"https://example.com/b"}},
			{URL: "https://ok.test/", StatusCode: 200},
		},
	}

	stats := RunCoreAnalysis(context.Background(), pages, cfg)

	if len(stats.BrokenExternalLinks) != 2 {
		t.Errorf("BrokenExternalLinks = %+v, want 2 entries", stats.BrokenExternalLinks)
	}
	if stats.SchemaValidationStats == nil || stats.SchemaValidationStats.PagesWithSchema != 1 ||
		stats.SchemaValidationStats.RichResultEligible["Product"] != 1 {
		t.Errorf("SchemaValidationStats = %+v, want 1 page with eligible Product", stats.SchemaValidationStats)
	}
	rc := stats.RenderCompareStats
	if rc == nil || rc.PagesWithDiffs != 1 || rc.FieldDiffCounts["title"] != 1 || len(rc.CriticalDiffs) != 2 {
		t.Errorf("RenderCompareStats = %+v, want 1 page, title + meta_robots diffs critical", rc)
	}
	// Fields are released after reporting.
	if pages[0].SchemaValidation != nil || pages[0].RenderDiffs != nil {
		t.Error("RunCoreAnalysis should strip SchemaValidation/RenderDiffs after reporting")
	}
}
