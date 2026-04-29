package analysis

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestValidateProductValid(t *testing.T) {
	raw := `{
		"@context": "https://schema.org",
		"@type": "Product",
		"name": "Widget",
		"image": "https://example.com/widget.jpg",
		"description": "A great widget",
		"offers": {
			"@type": "Offer",
			"price": "19.99",
			"priceCurrency": "USD",
			"availability": "InStock"
		}
	}`
	entries := []types.StructuredDataEntry{{Type: "Product", Format: types.FormatJSONLD, Raw: raw}}
	results := ValidateStructuredData(entries)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].Type != "Product" {
		t.Errorf("Type = %q, want 'Product'", results[0].Type)
	}
	if !results[0].RichResultEligible {
		t.Error("Product with all required fields should be rich result eligible")
	}
}

func TestValidateProductMissingRequired(t *testing.T) {
	raw := `{"@context": "https://schema.org", "@type": "Product"}`
	entries := []types.StructuredDataEntry{{Type: "Product", Format: types.FormatJSONLD, Raw: raw}}
	results := ValidateStructuredData(entries)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	hasNameError := false
	for _, issue := range results[0].Issues {
		if issue.Severity == types.SeverityError && issue.Path == "name" {
			hasNameError = true
		}
	}
	if !hasNameError {
		t.Error("Expected error for missing 'name' property")
	}
	if results[0].RichResultEligible {
		t.Error("Product missing required fields should not be rich result eligible")
	}
}

func TestValidateInvalidJSON(t *testing.T) {
	raw := `{invalid json`
	entries := []types.StructuredDataEntry{{Type: "Product", Format: types.FormatJSONLD, Raw: raw}}
	results := ValidateStructuredData(entries)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].Type != "ParseError" {
		t.Errorf("Type = %q, want 'ParseError'", results[0].Type)
	}
}

func TestValidateGraphContainer(t *testing.T) {
	raw := `{
		"@context": "https://schema.org",
		"@graph": [
			{"@type": "WebSite", "name": "Example", "url": "https://example.com"},
			{"@type": "Organization", "name": "Acme Corp"}
		]
	}`
	entries := []types.StructuredDataEntry{{Type: "WebSite", Format: types.FormatJSONLD, Raw: raw}}
	results := ValidateStructuredData(entries)
	if len(results) != 2 {
		t.Fatalf("Expected 2 results from @graph, got %d", len(results))
	}
	if results[0].Type != "WebSite" {
		t.Errorf("First result type = %q, want 'WebSite'", results[0].Type)
	}
	if results[1].Type != "Organization" {
		t.Errorf("Second result type = %q, want 'Organization'", results[1].Type)
	}
}

func TestValidateFAQPage(t *testing.T) {
	raw := `{
		"@context": "https://schema.org",
		"@type": "FAQPage",
		"mainEntity": [
			{
				"@type": "Question",
				"name": "What is Go?",
				"acceptedAnswer": {"@type": "Answer", "text": "A programming language"}
			}
		]
	}`
	entries := []types.StructuredDataEntry{{Type: "FAQPage", Format: types.FormatJSONLD, Raw: raw}}
	results := ValidateStructuredData(entries)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if !results[0].RichResultEligible {
		t.Error("Valid FAQPage should be rich result eligible")
	}
}

func TestValidateArticleBadDate(t *testing.T) {
	raw := `{
		"@context": "https://schema.org",
		"@type": "Article",
		"headline": "Test",
		"image": "https://example.com/img.jpg",
		"datePublished": "January 1 2025",
		"author": {"@type": "Person", "name": "Alice"}
	}`
	entries := []types.StructuredDataEntry{{Type: "Article", Format: types.FormatJSONLD, Raw: raw}}
	results := ValidateStructuredData(entries)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	hasDateError := false
	for _, issue := range results[0].Issues {
		if issue.Severity == types.SeverityError && issue.Path == "datePublished" {
			hasDateError = true
		}
	}
	if !hasDateError {
		t.Error("Expected error for non-ISO date format")
	}
	if results[0].RichResultEligible {
		t.Error("Article with bad date should not be rich result eligible")
	}
}

func TestValidateMicrodata(t *testing.T) {
	entries := []types.StructuredDataEntry{{Type: "Product", Format: types.FormatMicrodata, Raw: ""}}
	results := ValidateStructuredData(entries)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].Format != types.FormatMicrodata {
		t.Errorf("Format = %q, want 'microdata'", results[0].Format)
	}
	if results[0].RichResultEligible {
		t.Error("Microdata should not be rich result eligible")
	}
	if results[0].RichResultType == nil || *results[0].RichResultType != "Product" {
		t.Error("Microdata Product should report rich result type")
	}
}

func TestValidateMissingContext(t *testing.T) {
	raw := `{"@type": "Product", "name": "Widget"}`
	entries := []types.StructuredDataEntry{{Type: "Product", Format: types.FormatJSONLD, Raw: raw}}
	results := ValidateStructuredData(entries)
	hasContextWarning := false
	for _, issue := range results[0].Issues {
		if issue.Severity == types.SeverityWarning && issue.Message == `Missing @context (should be "https://schema.org")` {
			hasContextWarning = true
		}
	}
	if !hasContextWarning {
		t.Error("Expected warning for missing @context")
	}
}

func TestValidateEventBadDate(t *testing.T) {
	raw := `{
		"@context": "https://schema.org",
		"@type": "Event",
		"name": "Concert",
		"startDate": "next Friday",
		"location": {"@type": "Place", "name": "Arena"}
	}`
	entries := []types.StructuredDataEntry{{Type: "Event", Format: types.FormatJSONLD, Raw: raw}}
	results := ValidateStructuredData(entries)
	hasDateError := false
	for _, issue := range results[0].Issues {
		if issue.Path == "startDate" && issue.Severity == types.SeverityError {
			hasDateError = true
		}
	}
	if !hasDateError {
		t.Error("Expected error for non-ISO startDate")
	}
}

func TestValidateBreadcrumbList(t *testing.T) {
	raw := `{
		"@context": "https://schema.org",
		"@type": "BreadcrumbList",
		"itemListElement": [
			{"@type": "ListItem", "position": 1, "name": "Home", "item": "https://example.com/"},
			{"@type": "ListItem", "position": 2, "name": "Blog"}
		]
	}`
	entries := []types.StructuredDataEntry{{Type: "BreadcrumbList", Format: types.FormatJSONLD, Raw: raw}}
	results := ValidateStructuredData(entries)
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if !results[0].RichResultEligible {
		t.Error("Valid BreadcrumbList should be rich result eligible")
	}
}

func TestBuildSchemaValidationStats(t *testing.T) {
	productType := "Product"
	pages := []*types.PageData{
		{
			SchemaValidation: []types.SchemaValidationEntry{
				{
					Type:               "Product",
					Format:             types.FormatJSONLD,
					RichResultEligible: true,
					RichResultType:     &productType,
				},
			},
		},
		{
			SchemaValidation: []types.SchemaValidationEntry{
				{
					Type:   "Article",
					Format: types.FormatJSONLD,
					Issues: []types.SchemaValidationIssue{
						{Severity: types.SeverityError, Message: "Missing required property \"headline\""},
					},
				},
			},
		},
	}
	stats := BuildSchemaValidationStats(pages)
	if stats.PagesWithSchema != 2 {
		t.Errorf("PagesWithSchema = %d, want 2", stats.PagesWithSchema)
	}
	if stats.PagesWithValidSchema != 1 {
		t.Errorf("PagesWithValidSchema = %d, want 1", stats.PagesWithValidSchema)
	}
	if stats.PagesWithErrors != 1 {
		t.Errorf("PagesWithErrors = %d, want 1", stats.PagesWithErrors)
	}
	if stats.RichResultEligible["Product"] != 1 {
		t.Errorf("RichResultEligible[Product] = %d, want 1", stats.RichResultEligible["Product"])
	}
}
