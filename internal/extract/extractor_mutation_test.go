package extract

import (
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/micelio/micelio/internal/types"
)

// Every extraction step reads one shared parsed document; body text and EEAT
// no longer work on a clone. A step that removes or edits nodes would change
// what later steps see, so the tree must be identical after extraction.
func TestExtractionDoesNotMutateDocument(t *testing.T) {
	html := buildHTML(50, 20) + `
<html lang="en"><head>
<script type="application/ld+json">{"@type":"Product","name":"x"}</script>
<style>.a{}</style><noscript><img src="/n.png"></noscript>
<link rel="alternate" hreflang="fr" href="/fr/">
</head><body>
<nav><a href="/a">A</a></nav>
<div class="product-card listing"><a href="/products/shoe-1">Shoe</a><img src="/i.jpg?utm_source=x" data-src="/real.jpg"></div>
<article><h1>Title</h1><p>Text <script>var x=1</script> more.</p><template><p>hidden</p></template></article>
<footer>By Jane, updated 2026-01-01 <a href="/about">About</a></footer>
<svg><text>svg text</text></svg><iframe src="/f"></iframe>
</body></html>`
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	before, err := goquery.OuterHtml(doc.Selection)
	if err != nil {
		t.Fatal(err)
	}
	opts := ExtractionOptions{
		PageURL:           "https://example.com/page",
		StatusCode:        200,
		CustomExtractions: []types.CustomExtractionRule{{Name: "h1", Type: "css", Selector: "h1"}},
		CustomSearches:    []types.CustomSearchRule{{Name: "more", Pattern: "more"}},
	}
	extractFromDoc(doc, html, opts, opts.PageURL, true)
	after, err := goquery.OuterHtml(doc.Selection)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("extraction changed the shared document (%d → %d bytes)", len(before), len(after))
	}
}
