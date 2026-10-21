package extract

import (
	"fmt"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

func TestExtractLinkZonesAndLang(t *testing.T) {
	page := ExtractPageData(testHTML, ExtractionOptions{
		PageURL:    "https://example.com/test",
		FinalURL:   "https://example.com/test",
		StatusCode: 200,
	})
	if page.HTMLLang != "en" {
		t.Errorf("HTMLLang = %q, want en", page.HTMLLang)
	}
	z := page.LinkZones
	if z == nil {
		t.Fatal("LinkZones is nil")
	}
	if z.Navigation != 2 || z.Content != 1 || z.Footer != 1 {
		t.Errorf("zones = %+v, want nav 2, content 1, footer 1 (internal links only)", *z)
	}
}

func TestZoneClassHints(t *testing.T) {
	html := `<html><body><main><article>
		<nav class="breadcrumbs"><a href="/">Home</a><a href="/shoes/">Shoes</a></nav>
		<p>Body <a href="/guide">guide</a></p>
		<section class="related-posts"><a href="/r1">R1</a></section>
		<ol class="comment-list"><li><a href="/u/bob">bob</a></li></ol>
		</article></main>
		<div class="site-footer"><a href="/terms">Terms</a></div>
		<div id="sidebar"><a href="/s1">S1</a></div></body></html>`
	page := ExtractPageData(html, ExtractionOptions{PageURL: "https://ex.com/a", FinalURL: "https://ex.com/a", StatusCode: 200})
	want := types.LinkZoneCounts{Content: 1, Breadcrumb: 2, Related: 1, Comments: 1, Footer: 1, Sidebar: 1}
	if page.LinkZones == nil || *page.LinkZones != want {
		t.Errorf("zones = %+v, want %+v", page.LinkZones, want)
	}
}

func productPage(name string, specs int) string {
	var b strings.Builder
	b.WriteString(`<html><body><header class="top"><nav><a href="/">Home</a></nav></header><main><div class="product">`)
	fmt.Fprintf(&b, `<h1 class="product-title">%s</h1><div class="gallery"><figure class="photo"><img src="a.jpg"></figure></div>`, name)
	b.WriteString(`<div class="buy-box"><span class="price">10</span><form class="add-to-cart"><select class="size"></select><button class="btn primary">Add</button></form></div><ul class="specs">`)
	for i := 0; i < specs; i++ {
		fmt.Fprintf(&b, `<li class="spec">Spec %d</li>`, i)
	}
	b.WriteString(`</ul><section class="reviews"><h2>Reviews</h2><div class="review"></div></section></div></main><footer><a href="/terms">Terms</a></footer></body></html>`)
	return b.String()
}

func articlePage(title string) string {
	return `<html><body><header class="top"><nav><a href="/">Home</a></nav></header><main><article class="post">
	<h1 class="entry-title">` + title + `</h1><div class="entry-meta"><span class="author">Ann</span></div>
	<div class="entry-content"><p>Text</p><h2>Part</h2><p>More</p><figure class="wp-block-image"><img src="x.jpg"></figure></div>
	<div class="share"><button class="share-btn">Share</button></div><section class="comments"><h3>Comments</h3><ol class="comment-list"></ol></section>
	</article><aside class="widget-area"><div class="widget"><ul class="recent"><li>x</li></ul></div></aside></main><footer><a href="/terms">Terms</a></footer></body></html>`
}

func TestDOMFingerprintSeparatesTemplates(t *testing.T) {
	fp := func(html string) uint64 {
		return ExtractPageData(html, ExtractionOptions{PageURL: "https://ex.com/x", FinalURL: "https://ex.com/x", StatusCode: 200}).DOMFingerprint
	}
	p1, p2 := fp(productPage("Shoe A", 3)), fp(productPage("Boot B", 12))
	a1 := fp(articlePage("Post one"))
	if p1 == 0 || a1 == 0 {
		t.Fatal("fingerprint should be set for structured pages")
	}
	if d := analysis.HammingDistance(p1, p2); d > 8 {
		t.Errorf("same template distance = %d, want <= 8", d)
	}
	if d := analysis.HammingDistance(p1, a1); d <= 8 {
		t.Errorf("different templates distance = %d, want > 8", d)
	}
	if got := fp(`<html><body><p>tiny</p></body></html>`); got != 0 {
		t.Errorf("tiny page fingerprint = %d, want 0", got)
	}
}

func TestCommerceSignalsSplitProductAndListing(t *testing.T) {
	product := `<html><body><main><article class="product_page"><h1>Book</h1><p class="price_color">£51</p>
		<form><button type="submit" class="btn btn-primary">Add to basket</button></form></article></main></body></html>`
	p := ExtractPageData(product, ExtractionOptions{PageURL: "https://ex.com/catalogue/book_1/index.html", FinalURL: "https://ex.com/catalogue/book_1/index.html", StatusCode: 200})
	if p.ProductSignals == 0 {
		t.Error("product page ProductSignals = 0, want > 0")
	}
	if got := analysis.DetectTemplateType(&p, "https://ex.com/"); got != analysis.TemplateProduct {
		t.Errorf("product page template = %q, want product", got)
	}

	var b strings.Builder
	b.WriteString(`<html><body><main><ol class="row">`)
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, `<li><article class="product_pod"><a href="/b%d">Book %d</a><button class="btn">Add to basket</button></article></li>`, i, i)
	}
	b.WriteString(`</ol></main></body></html>`)
	l := ExtractPageData(b.String(), ExtractionOptions{PageURL: "https://ex.com/catalogue/category/travel/index.html", FinalURL: "https://ex.com/catalogue/category/travel/index.html", StatusCode: 200})
	if l.ListingSignals < 2 {
		t.Errorf("listing page ListingSignals = %d, want >= 2", l.ListingSignals)
	}
	if l.ProductSignals != 0 {
		t.Errorf("listing page should not get product signals, ProductSignals = %d", l.ProductSignals)
	}
}

func TestSinglePriceMarksProductPage(t *testing.T) {
	html := `<html><body><main><div class="product_main"><h1>A Light in the Attic</h1><p class="price_color">£51.77</p>
		<p class="instock availability">In stock</p></div><p>` + strings.Repeat("word ", 200) + `</p></main></body></html>`
	u := "https://ex.com/catalogue/a-light-in-the-attic_1000/index.html"
	p := ExtractPageData(html, ExtractionOptions{PageURL: u, FinalURL: u, StatusCode: 200})
	if got := analysis.DetectTemplateType(&p, "https://ex.com/"); got != analysis.TemplateProduct {
		t.Errorf("template = %q, want product", got)
	}
}

func TestProductPageWithRecentlyViewedCards(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><body><main><div class="product_main"><h1>Sharp Objects</h1><p class="price_color">£47.82</p></div>
		<p>` + strings.Repeat("word ", 200) + `</p><h2>Products you recently viewed</h2><ol class="row">`)
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&b, `<li><article class="product_pod"><a href="/b%d">B</a><p class="price_color">£1%d.00</p><button class="btn">Add to basket</button></article></li>`, i, i)
	}
	b.WriteString(`</ol></main></body></html>`)
	u := "https://ex.com/catalogue/sharp-objects_997/index.html"
	p := ExtractPageData(b.String(), ExtractionOptions{PageURL: u, FinalURL: u, StatusCode: 200})
	if got := analysis.DetectTemplateType(&p, "https://ex.com/"); got != analysis.TemplateProduct {
		t.Errorf("template = %q (listing=%d product=%d), want product", got, p.ListingSignals, p.ProductSignals)
	}
}

func TestWooCommerceProductAndShop(t *testing.T) {
	related := `<section class="related products"><ul class="products columns-4">`
	for i := 0; i < 4; i++ {
		related += fmt.Sprintf(`<li class="product type-product"><a href="/shop/p%d/">P</a><span class="price"><span class="woocommerce-Price-amount amount"><span class="woocommerce-Price-currencySymbol">£</span>1%d.00</span></span><a href="?add-to-cart=%d" class="button add_to_cart_button">Add to cart</a></li>`, i, i, i)
	}
	related += `</ul></section>`
	product := `<html><body><main><div id="product-759" class="product type-product"><div class="summary entry-summary"><h1 class="product_title">Bulbasaur</h1>
		<p class="price"><span class="woocommerce-Price-amount amount"><span class="woocommerce-Price-currencySymbol">£</span>63.00</span></p>
		<form class="cart"><button type="submit" class="single_add_to_cart_button button alt">Add to cart</button></form></div>` + related + `</div></main></body></html>`
	u := "https://ex.com/shop/bulbasaur/"
	p := ExtractPageData(product, ExtractionOptions{PageURL: u, FinalURL: u, StatusCode: 200})
	if got := analysis.DetectTemplateType(&p, "https://ex.com/"); got != analysis.TemplateProduct {
		t.Errorf("woo product = %q (listing=%d product=%d), want product", got, p.ListingSignals, p.ProductSignals)
	}

	shop := `<html><body><main><ul class="products columns-4">` + strings.ReplaceAll(related, `<section class="related products"><ul class="products columns-4">`, "") + `</main></body></html>`
	u = "https://ex.com/shop/page/2/"
	l := ExtractPageData(shop, ExtractionOptions{PageURL: u, FinalURL: u, StatusCode: 200})
	if got := analysis.DetectTemplateType(&l, "https://ex.com/"); got != analysis.TemplateListing {
		t.Errorf("woo shop page = %q (listing=%d product=%d), want listing", got, l.ListingSignals, l.ProductSignals)
	}
}
