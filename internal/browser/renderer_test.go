package browser

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
)

func TestCompareRenderNoDiffs(t *testing.T) {
	html := `<html><head><title>Test</title></head><body><h1>Hello</h1><p>World</p></body></html>`
	diffs, err := CompareRender(html, html)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 0 {
		t.Fatalf("expected no diffs, got %d: %+v", len(diffs), diffs)
	}
}

func TestCompareRenderTitleDiff(t *testing.T) {
	raw := `<html><head><title>Original</title></head><body><h1>Hello</h1></body></html>`
	rendered := `<html><head><title>Updated</title></head><body><h1>Hello</h1></body></html>`
	diffs, err := CompareRender(raw, rendered)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, d := range diffs {
		if d.Field == "title" && d.Original == "Original" && d.Rendered == "Updated" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected title diff, got %+v", diffs)
	}
}

func TestCompareRenderH1Diff(t *testing.T) {
	raw := `<html><body><h1>Original H1</h1></body></html>`
	rendered := `<html><body><h1>Rendered H1</h1></body></html>`
	diffs, err := CompareRender(raw, rendered)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, d := range diffs {
		if d.Field == "h1" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected h1 diff")
	}
}

func TestCompareRenderMetaDescDiff(t *testing.T) {
	raw := `<html><head><meta name="description" content="old desc"></head><body></body></html>`
	rendered := `<html><head><meta name="description" content="new desc"></head><body></body></html>`
	diffs, err := CompareRender(raw, rendered)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, d := range diffs {
		if d.Field == "meta_description" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected meta_description diff")
	}
}

func TestCompareRenderJsonLdCount(t *testing.T) {
	raw := `<html><body></body></html>`
	rendered := `<html><body><script type="application/ld+json">{"@type":"Organization"}</script></body></html>`
	diffs, err := CompareRender(raw, rendered)
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, d := range diffs {
		if d.Field == "json_ld_count" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected json_ld_count diff")
	}
}

func TestIsSPALikely(t *testing.T) {
	// SPA-like: framework root with minimal text
	spaHTML := `<html><body><div id="root"></div><script src="bundle.js"></script></body></html>`
	if !IsSPALikely(spaHTML) {
		t.Fatal("expected SPA detection for root div with no content")
	}

	// Not SPA: lots of text content
	normalHTML := `<html><body><div id="root"><h1>Hello World</h1><p>` +
		"This is a normal page with lots of text content that should not be detected as a SPA. " +
		"It has paragraphs and headings and various content elements. " +
		"The page contains enough real text to indicate it was server-rendered with actual content." +
		`</p></div></body></html>`
	if IsSPALikely(normalHTML) {
		t.Fatal("expected non-SPA for content-rich page")
	}

	// Not SPA: no framework root
	noRootHTML := `<html><body><h1>Hello</h1></body></html>`
	if IsSPALikely(noRootHTML) {
		t.Fatal("expected non-SPA without framework root div")
	}
}

func TestIsLikelyInternalHref(t *testing.T) {
	tests := []struct {
		href string
		want bool
	}{
		{"/about", true},
		{"/page/1", true},
		{"about", true},
		{"../page", true},
		{"#section", false},
		{"//cdn.example.com/file", false},
		{"https://example.com/page", false},
		{"mailto:test@example.com", false},
		{"tel:+1234567890", false},
		{"javascript:void(0)", false},
	}
	for _, tc := range tests {
		got := isLikelyInternalHref(tc.href)
		if got != tc.want {
			t.Errorf("isLikelyInternalHref(%q) = %v, want %v", tc.href, got, tc.want)
		}
	}
}

func TestCountBodyWords(t *testing.T) {
	html := `<html><body>
		<nav>Navigation menu</nav>
		<h1>Main Content</h1>
		<p>This is the body text of the page.</p>
		<script>var x = 1;</script>
		<style>.foo { color: red; }</style>
		<footer>Footer content</footer>
	</body></html>`

	doc, _ := goquery.NewDocumentFromReader(strings.NewReader(html))
	words := countBodyWords(doc)

	// Should count words from h1 and p, but not nav/footer/script/style
	if words < 5 || words > 20 {
		t.Fatalf("expected ~10 words, got %d", words)
	}
}
