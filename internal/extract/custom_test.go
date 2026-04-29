package extract

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"

	"github.com/micelio/micelio/internal/types"
)

func TestRunCustomExtractions(t *testing.T) {
	html := `<html><body>
		<h1>Title</h1>
		<div class="price">$29.99</div>
		<div class="price">$39.99</div>
		<span class="sku">SKU-123</span>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))

	rules := []types.CustomExtractionRule{
		{Name: "prices", Type: "css", Selector: ".price"},
		{Name: "sku", Type: "css", Selector: ".sku"},
		{Name: "missing", Type: "css", Selector: ".nonexistent"},
	}

	results := RunCustomExtractions(doc, rules)

	if len(results["prices"]) != 2 {
		t.Fatalf("expected 2 prices, got %d", len(results["prices"]))
	}
	if results["prices"][0] != "$29.99" {
		t.Fatalf("expected $29.99, got %q", results["prices"][0])
	}
	if len(results["sku"]) != 1 || results["sku"][0] != "SKU-123" {
		t.Fatalf("unexpected sku: %v", results["sku"])
	}
	if len(results["missing"]) != 0 {
		t.Fatalf("expected empty for missing selector")
	}
}

func TestRunCustomExtractionsEmpty(t *testing.T) {
	results := RunCustomExtractions(nil, nil)
	if results != nil {
		t.Fatal("expected nil for empty rules")
	}
}

func TestRunCustomSearches(t *testing.T) {
	html := `<html><body><p>Buy our premium product at $29.99 today!</p></body></html>`

	rules := []types.CustomSearchRule{
		{Name: "has-price", Pattern: "$29.99", IsRegex: false},
		{Name: "has-missing", Pattern: "foobar", IsRegex: false},
		{Name: "regex-price", Pattern: `\$\d+\.\d{2}`, IsRegex: true},
		{Name: "regex-no-match", Pattern: `\bxyz\b`, IsRegex: true},
	}

	results := RunCustomSearches(html, rules)

	if !results["has-price"] {
		t.Fatal("expected has-price to be true")
	}
	if results["has-missing"] {
		t.Fatal("expected has-missing to be false")
	}
	if !results["regex-price"] {
		t.Fatal("expected regex-price to match")
	}
	if results["regex-no-match"] {
		t.Fatal("expected regex-no-match to be false")
	}
}

func TestRunCustomSearchesEmpty(t *testing.T) {
	results := RunCustomSearches("", nil)
	if results != nil {
		t.Fatal("expected nil for empty rules")
	}
}

func TestParseExtractionRule(t *testing.T) {
	tests := []struct {
		input   string
		name    string
		typ     string
		sel     string
		wantErr bool
	}{
		{"prices:.price", "prices", "css", ".price", false},
		{"sku:css:.sku", "sku", "css", ".sku", false},
		{"title:h1", "title", "css", "h1", false},
		{"invalid", "", "", "", true},
		{":selector", "", "", "", true},
	}
	for _, tc := range tests {
		rule, err := ParseExtractionRule(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseExtractionRule(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseExtractionRule(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if rule.Name != tc.name || rule.Type != tc.typ || rule.Selector != tc.sel {
			t.Errorf("ParseExtractionRule(%q) = {%q, %q, %q}, want {%q, %q, %q}",
				tc.input, rule.Name, rule.Type, rule.Selector, tc.name, tc.typ, tc.sel)
		}
	}
}

func TestParseSearchRule(t *testing.T) {
	// Plain text
	rule, err := ParseSearchRule("hello")
	if err != nil {
		t.Fatal(err)
	}
	if rule.IsRegex || rule.Pattern != "hello" {
		t.Fatalf("unexpected rule: %+v", rule)
	}

	// Regex
	rule, err = ParseSearchRule("/\\d+/")
	if err != nil {
		t.Fatal(err)
	}
	if !rule.IsRegex || rule.Pattern != `\d+` {
		t.Fatalf("unexpected rule: %+v", rule)
	}

	// Invalid regex
	_, err = ParseSearchRule("/[invalid/")
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestRegexSearchReDoSProtection(t *testing.T) {
	// This should not hang — tests the sample limit + timeout
	largeHTML := strings.Repeat("a", 1_000_000)
	result := regexSearch(largeHTML, "a+")
	if !result {
		t.Fatal("expected match")
	}
}
