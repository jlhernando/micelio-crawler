package extract

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

const testHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <title>Test Page Title</title>
  <meta name="description" content="A test meta description for SEO">
  <link rel="canonical" href="https://example.com/test">
  <meta name="robots" content="index, follow">
  <link rel="alternate" hreflang="en" href="https://example.com/en/test">
  <link rel="alternate" hreflang="es" href="https://example.com/es/test">
  <meta property="og:title" content="OG Test Title">
  <meta property="og:type" content="website">
  <meta name="twitter:card" content="summary">
  <script type="application/ld+json">{"@type": "Article", "name": "Test"}</script>
</head>
<body>
  <header>
    <nav>
      <a href="/about">About</a>
      <a href="/contact">Contact</a>
    </nav>
  </header>
  <main>
    <h1>Main Heading</h1>
    <h2>Sub Heading One</h2>
    <p>This is the main content of the test page with enough words to count.</p>
    <a href="/blog/post-1">Read the first blog post</a>
    <a href="https://external.com/page">External Link</a>
    <img src="/images/photo.jpg" alt="A test photo" width="800" height="600">
    <img src="/images/logo.png">
    <h2>Sub Heading Two</h2>
    <p>More content here for word count and readability testing purposes. This adds more text to test the extraction.</p>
  </main>
  <footer>
    <a href="/privacy">Privacy Policy</a>
  </footer>
</body>
</html>`

func TestExtractTitle(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if page.Title == nil {
		t.Fatal("Expected non-nil title")
	}
	if page.Title.Text != "Test Page Title" {
		t.Errorf("Title = %q, want %q", page.Title.Text, "Test Page Title")
	}
}

func TestExtractMetaDescription(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if page.MetaDescription == nil {
		t.Fatal("Expected non-nil meta description")
	}
	if page.MetaDescription.Text != "A test meta description for SEO" {
		t.Errorf("MetaDescription = %q", page.MetaDescription.Text)
	}
}

func TestExtractCanonical(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if page.Canonical == nil {
		t.Fatal("Expected non-nil canonical")
	}
	if *page.Canonical != "https://example.com/test" {
		t.Errorf("Canonical = %q", *page.Canonical)
	}
	if page.CanonicalCount != 1 {
		t.Errorf("CanonicalCount = %d, want 1", page.CanonicalCount)
	}
}

func TestExtractHeadings(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if len(page.Headings.H1) != 1 || page.Headings.H1[0] != "Main Heading" {
		t.Errorf("H1 = %v", page.Headings.H1)
	}
	if len(page.Headings.H2) != 2 {
		t.Errorf("H2 count = %d, want 2", len(page.Headings.H2))
	}
}

func TestExtractLinks(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if len(page.InternalLinks) < 4 {
		t.Errorf("InternalLinks count = %d, want >= 4", len(page.InternalLinks))
	}
	if len(page.ExternalLinks) != 1 {
		t.Errorf("ExternalLinks count = %d, want 1", len(page.ExternalLinks))
	}

	// Check anchor positions
	foundNav := false
	foundContent := false
	foundFooter := false
	for _, a := range page.Anchors {
		if a.Text == "About" && a.Position == types.LinkPosNavigation {
			foundNav = true
		}
		if a.Text == "Read the first blog post" && a.Position == types.LinkPosContent {
			foundContent = true
		}
		if a.Text == "Privacy Policy" && a.Position == types.LinkPosFooter {
			foundFooter = true
		}
	}
	if !foundNav {
		t.Error("Expected nav link with position=navigation")
	}
	if !foundContent {
		t.Error("Expected content link with position=content")
	}
	if !foundFooter {
		t.Error("Expected footer link with position=footer")
	}
}

func TestExtractImages(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if len(page.Images) != 2 {
		t.Fatalf("Images count = %d, want 2", len(page.Images))
	}

	// First image has alt
	if page.Images[0].MissingAlt {
		t.Error("First image should have alt")
	}
	if page.Images[0].Alt == nil || *page.Images[0].Alt != "A test photo" {
		t.Errorf("First image alt = %v", page.Images[0].Alt)
	}

	// Second image is missing alt
	if !page.Images[1].MissingAlt {
		t.Error("Second image should be missing alt")
	}
}

func TestExtractHreflang(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if len(page.Hreflang) != 2 {
		t.Fatalf("Hreflang count = %d, want 2", len(page.Hreflang))
	}
	if page.Hreflang[0].Lang != "en" {
		t.Errorf("Hreflang[0].Lang = %q", page.Hreflang[0].Lang)
	}
}

func TestExtractStructuredData(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if len(page.StructuredData) < 1 {
		t.Fatal("Expected at least 1 structured data entry")
	}
	if page.StructuredData[0].Type != "Article" {
		t.Errorf("StructuredData type = %q, want Article", page.StructuredData[0].Type)
	}
	if page.StructuredData[0].Format != types.FormatJSONLD {
		t.Errorf("StructuredData format = %q", page.StructuredData[0].Format)
	}
}

func TestExtractOpenGraph(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if page.OpenGraph["og:title"] != "OG Test Title" {
		t.Errorf("og:title = %q", page.OpenGraph["og:title"])
	}
}

func TestWordCount(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if page.WordCount < 20 {
		t.Errorf("WordCount = %d, expected >= 20", page.WordCount)
	}
}

func TestIndexability(t *testing.T) {
	// 200 + indexable
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})
	if !page.Indexability.Indexable {
		t.Error("Expected page to be indexable")
	}

	// noindex
	noindexHTML := `<html><head><meta name="robots" content="noindex"><title>Test</title></head><body>Content</body></html>`
	page2 := ExtractPageData(noindexHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})
	if page2.Indexability.Indexable {
		t.Error("Expected noindex page to be non-indexable")
	}
}

func TestURLIssues(t *testing.T) {
	issues := detectURLIssues("https://example.com/Path_With_Issues?utm_source=test")

	found := map[string]bool{}
	for _, i := range issues {
		found[i] = true
	}
	if !found["uppercase-in-path"] {
		t.Error("Expected uppercase-in-path issue")
	}
	if !found["underscores-in-path"] {
		t.Error("Expected underscores-in-path issue")
	}
	if !found["tracking-parameters"] {
		t.Error("Expected tracking-parameters issue")
	}
}

func TestSoft404(t *testing.T) {
	html404 := `<html><head><title>Page Not Found</title></head><body><h1>404 - Page Not Found</h1></body></html>`
	page := ExtractPageData(html404, ExtractionOptions{
		PageURL:    "https://example.com/missing",
		FinalURL:   "https://example.com/missing",
		StatusCode: 200,
	})

	if !page.IsSoft404 {
		t.Error("Expected soft 404 detection")
	}
}

func TestContentHash(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})

	if page.ContentHash == "" {
		t.Error("Expected non-empty content hash")
	}
	if len(page.ContentHash) != 32 {
		t.Errorf("ContentHash length = %d, want 32 (MD5 hex)", len(page.ContentHash))
	}
}

func TestCustomExtractions(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
		CustomExtractions: []types.CustomExtractionRule{
			{Name: "h1_text", Type: "css", Selector: "h1"},
		},
	})

	if vals, ok := page.CustomExtractions["h1_text"]; !ok || len(vals) != 1 || vals[0] != "Main Heading" {
		t.Errorf("CustomExtractions h1_text = %v", page.CustomExtractions["h1_text"])
	}
}

func TestCustomSearches(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
		CustomSearches: []types.CustomSearchRule{
			{Name: "has_og", Pattern: "og:title", IsRegex: false},
			{Name: "has_amp", Pattern: "amp-", IsRegex: false},
		},
	})

	if !page.CustomSearches["has_og"] {
		t.Error("Expected has_og to be true")
	}
	if page.CustomSearches["has_amp"] {
		t.Error("Expected has_amp to be false")
	}
}

func TestSecurity(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
		Headers:    map[string]string{"strict-transport-security": "max-age=31536000"},
	})

	if !page.Security.IsHTTPS {
		t.Error("Expected IsHTTPS to be true")
	}
	if !page.Security.HasHSTS {
		t.Error("Expected HasHSTS to be true")
	}
}

func TestExtractStructuredData_GraphExpansion(t *testing.T) {
	html := `<html><head>
	<script type="application/ld+json">{"@context":"https://schema.org","@graph":[
		{"@type":"Person","@id":"https://example.com/#person","name":"Jose Hernando"},
		{"@type":"WebSite","name":"Test"},
		{"@type":"BlogPosting","headline":"Post","author":{"@id":"https://example.com/#person"}},
		{"@type":"FAQPage","mainEntity":[{"@type":"Question","name":"Q?","acceptedAnswer":{"@type":"Answer","text":"A"}}]},
		{"@type":"BreadcrumbList","itemListElement":[]}
	]}</script>
	</head><body><p>This is a test paragraph with enough words to pass the minimum word count threshold for AI readiness extraction to run properly in the test suite and produce meaningful results.</p></body></html>`
	page := ExtractPageData(html, ExtractionOptions{PageURL: "https://example.com/test"})
	typeSet := map[string]bool{}
	for _, e := range page.StructuredData {
		typeSet[e.Type] = true
	}
	for _, want := range []string{"Person", "WebSite", "BlogPosting", "FAQPage", "BreadcrumbList"} {
		if !typeSet[want] {
			t.Errorf("expected type %q in structured data, got types: %v", want, typeSet)
		}
	}
	if page.AIReadiness == nil {
		t.Fatal("expected AIReadiness to be non-nil")
	}
	if !page.AIReadiness.HasFAQSchema {
		t.Error("expected HasFAQSchema=true from @graph with FAQPage")
	}
	// EEAT: author resolved from @id reference
	if page.EEAT == nil {
		t.Fatal("expected EEAT to be non-nil")
	}
	if !page.EEAT.HasAuthor {
		t.Error("expected HasAuthor=true from @graph BlogPosting with @id author reference")
	}
	if page.EEAT.AuthorName != "Jose Hernando" {
		t.Errorf("AuthorName = %q, want %q", page.EEAT.AuthorName, "Jose Hernando")
	}
	if page.EEAT.AuthorSchemaType != "Person" {
		t.Errorf("AuthorSchemaType = %q, want %q", page.EEAT.AuthorSchemaType, "Person")
	}
}
