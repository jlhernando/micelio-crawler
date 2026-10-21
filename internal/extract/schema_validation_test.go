package extract

import (
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

func TestExtractPageData_SetsSchemaValidation(t *testing.T) {
	// Large description pushes the JSON-LD past jsonldMaxRaw (5KB): validation
	// must use the full script, not the truncated StructuredData.Raw.
	longDesc := strings.Repeat("x", 6000)
	html := `<html><head>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product","name":"Widget","description":"` + longDesc + `"}</script>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Article","headline":"Missing fields"}</script>
<script type="application/ld+json">[{"@context":"https://schema.org","@type":"BreadcrumbList","itemListElement":[]}]</script>
<script type="application/ld+json">{not json</script>
</head><body>
<div itemscope itemtype="https://schema.org/Recipe"><span itemprop="name">Soup</span></div>
</body></html>`

	page := ExtractPageData(html, ExtractionOptions{PageURL: "https://example.com/p", StatusCode: 200})
	if len(page.SchemaValidation) == 0 {
		t.Fatal("SchemaValidation not populated at extraction time")
	}
	byType := map[string]types.SchemaValidationEntry{}
	for _, sv := range page.SchemaValidation {
		byType[sv.Type] = sv
	}
	for _, want := range []string{"Product", "Article", "BreadcrumbList", "ParseError", "Recipe"} {
		if _, ok := byType[want]; !ok {
			t.Errorf("missing validation entry for %s; got %+v", want, page.SchemaValidation)
		}
	}
	if p := byType["Product"]; !p.RichResultEligible {
		t.Errorf("Product with name should be rich-result eligible despite >5KB JSON-LD, got %+v", p)
	}
	if a := byType["Article"]; a.RichResultEligible {
		t.Errorf("Article missing required fields should not be eligible, got %+v", a)
	}

	stats := analysis.BuildSchemaValidationStats([]*types.PageData{&page})
	if stats.PagesWithSchema != 1 || stats.PagesWithErrors != 1 {
		t.Errorf("stats PagesWithSchema/PagesWithErrors = %d/%d, want 1/1", stats.PagesWithSchema, stats.PagesWithErrors)
	}
	if stats.RichResultEligible["Product"] != 1 {
		t.Errorf("RichResultEligible[Product] = %d, want 1", stats.RichResultEligible["Product"])
	}
}

func TestExtractPageData_NoSchemaNoValidation(t *testing.T) {
	page := ExtractPageData(`<html><body><p>hi</p></body></html>`, ExtractionOptions{PageURL: "https://example.com/", StatusCode: 200})
	if page.SchemaValidation != nil {
		t.Errorf("expected nil SchemaValidation, got %+v", page.SchemaValidation)
	}
}

func TestValidatePageSchema_Capped(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><head><script type="application/ld+json">{"@context":"https://schema.org","@graph":[`)
	for i := 0; i < maxSchemaValidationEntries+20; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"@type":"Thing","name":"n"}`)
	}
	b.WriteString(`]}</script></head><body></body></html>`)
	page := ExtractPageData(b.String(), ExtractionOptions{PageURL: "https://example.com/", StatusCode: 200})
	if len(page.SchemaValidation) != maxSchemaValidationEntries {
		t.Errorf("len(SchemaValidation) = %d, want cap %d", len(page.SchemaValidation), maxSchemaValidationEntries)
	}
}
