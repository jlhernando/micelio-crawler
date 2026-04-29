package analysis

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// ── URL Structure Tests ──

func TestAnalyzeURLStructure(t *testing.T) {
	data := AnalyzeURLStructure("https://example.com/blog/post-1?tag=go&sort=date")
	if data.Scheme != "https" {
		t.Errorf("Scheme = %q, want 'https'", data.Scheme)
	}
	if data.PathDepth != 2 {
		t.Errorf("PathDepth = %d, want 2", data.PathDepth)
	}
	if data.LastSegment != "post-1" {
		t.Errorf("LastSegment = %q, want 'post-1'", data.LastSegment)
	}
	if data.ParameterCount != 2 {
		t.Errorf("ParameterCount = %d, want 2", data.ParameterCount)
	}
}

func TestAnalyzeURLStructureExtension(t *testing.T) {
	data := AnalyzeURLStructure("https://example.com/page.html")
	if data.FileExtension != "html" {
		t.Errorf("FileExtension = %q, want 'html'", data.FileExtension)
	}
	// Non-web extension
	data2 := AnalyzeURLStructure("https://example.com/image.png")
	if data2.FileExtension != "" {
		t.Errorf("FileExtension for .png = %q, want empty", data2.FileExtension)
	}
}

func TestBuildURLStructureStats(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/blog/a"},
		{URL: "https://example.com/blog/b?tag=go"},
		{URL: "https://example.com/about"},
	}
	stats := BuildURLStructureStats(pages)
	if stats == nil {
		t.Fatal("Expected stats, got nil")
	}
	if stats.TotalURLs != 3 {
		t.Errorf("TotalURLs = %d, want 3", stats.TotalURLs)
	}
	if stats.URLsWithParams != 1 {
		t.Errorf("URLsWithParams = %d, want 1", stats.URLsWithParams)
	}
}

// ── Sitemap Tests ──

func TestGenerateSitemap(t *testing.T) {
	now := time.Now()
	pages := []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true}, CrawledAt: now},
		{URL: "https://example.com/about", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true}, CrawledAt: now},
		{URL: "https://example.com/404", StatusCode: 404}, // Should be excluded
	}
	result := GenerateSitemap(pages, SitemapOptions{})
	if result.URLCount != 2 {
		t.Errorf("URLCount = %d, want 2", result.URLCount)
	}
	if !strings.Contains(result.XML, "<loc>https://example.com/</loc>") {
		t.Error("Missing homepage URL in sitemap")
	}
	if strings.Contains(result.XML, "404") {
		t.Error("404 page should be excluded")
	}
}

func TestSitemapExcludesNonIndexable(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: false, Reason: "noindex"}},
	}
	result := GenerateSitemap(pages, SitemapOptions{})
	if result.URLCount != 0 {
		t.Errorf("Non-indexable page should be excluded, got URLCount = %d", result.URLCount)
	}
}

// ── Segmentation Tests ──

func TestParseSegment(t *testing.T) {
	seg, err := ParseSegment("blog:/blog/.*")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if seg.Name != "blog" {
		t.Errorf("Name = %q, want 'blog'", seg.Name)
	}
	if !seg.Pattern.MatchString("/blog/post-1") {
		t.Error("Pattern should match /blog/post-1")
	}
}

func TestParseSegmentInvalid(t *testing.T) {
	_, err := ParseSegment("nocolon")
	if err == nil {
		t.Error("Expected error for missing colon")
	}
}

func TestBuildSegmentStats(t *testing.T) {
	seg, _ := ParseSegment("blog:/blog/")
	pages := []*types.PageData{
		{URL: "https://example.com/blog/post-1", StatusCode: 200, WordCount: 500, InternalLinks: []string{"a", "b"}, Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://example.com/blog/post-2", StatusCode: 200, WordCount: 300, Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://example.com/about", StatusCode: 200, WordCount: 100, Indexability: types.IndexabilityData{Indexable: true}},
	}
	stats := BuildSegmentStats(pages, []Segment{seg})
	if len(stats) != 1 {
		t.Fatalf("Expected 1 segment stat, got %d", len(stats))
	}
	if stats[0].PageCount != 2 {
		t.Errorf("Blog segment PageCount = %d, want 2", stats[0].PageCount)
	}
}

// ── Template Detection Tests ──

func TestDetectHomepage(t *testing.T) {
	page := &types.PageData{
		URL:        "https://example.com/",
		FinalURL:   "https://example.com/",
		StatusCode: 200,
		Depth:      0,
		Headings:   types.HeadingData{H1: []string{"Welcome"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateHomepage {
		t.Errorf("Template = %q, want 'homepage'", tpl)
	}
}

func TestDetectArticle(t *testing.T) {
	page := &types.PageData{
		URL:           "https://example.com/blog/my-great-post",
		FinalURL:      "https://example.com/blog/my-great-post",
		StatusCode:    200,
		Depth:         2,
		WordCount:     1200,
		InternalLinks: []string{"a"},
		Headings:      types.HeadingData{H1: []string{"My Great Post"}, H2: []string{"Intro", "Details", "Conclusion"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateArticle {
		t.Errorf("Template = %q, want 'article'", tpl)
	}
}

func TestDetectFAQ(t *testing.T) {
	page := &types.PageData{
		URL:        "https://example.com/faq/",
		FinalURL:   "https://example.com/faq/",
		StatusCode: 200,
		Depth:      1,
		WordCount:  500,
		Headings:   types.HeadingData{H1: []string{"Frequently Asked Questions"}, H2: []string{"Q1", "Q2", "Q3", "Q4", "Q5"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateFAQ {
		t.Errorf("Template = %q, want 'faq'", tpl)
	}
}

func TestDetectOtherLowConfidence(t *testing.T) {
	page := &types.PageData{
		URL:        "https://example.com/xyz",
		FinalURL:   "https://example.com/xyz",
		StatusCode: 200,
		Depth:      1,
		WordCount:  50,
		Headings:   types.HeadingData{},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateOther {
		t.Errorf("Template = %q, want 'other'", tpl)
	}
}

func TestDetectListingMarketplace(t *testing.T) {
	// Marketplace page with listing HTML signals and many internal links
	page := &types.PageData{
		URL:            "https://example.com/osobowe/babiak_975",
		FinalURL:       "https://example.com/osobowe/babiak_975",
		StatusCode:     200,
		Depth:          2,
		WordCount:      200,
		ListingSignals: 5,
		InternalLinks:  make([]string, 40),
		Headings:       types.HeadingData{H1: []string{"Cars in Babiak"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateListing {
		t.Errorf("Template = %q, want 'listing'", tpl)
	}
}

func TestDetectListingClassifieds(t *testing.T) {
	// Classifieds page with sibling URLs from the same parent directory (>= 15 threshold)
	links := make([]string, 20)
	for i := range links {
		links[i] = fmt.Sprintf("https://example.com/offers/item-%d", i)
	}
	page := &types.PageData{
		URL:           "https://example.com/offers/",
		FinalURL:      "https://example.com/offers/",
		StatusCode:    200,
		Depth:         1,
		WordCount:     150,
		InternalLinks: links,
		Headings:      types.HeadingData{H1: []string{"Latest Offers"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateListing {
		t.Errorf("Template = %q, want 'listing'", tpl)
	}
}

func TestDetectListingLinkDense(t *testing.T) {
	// Link-dense page: many internal links, low word count relative to links
	page := &types.PageData{
		URL:            "https://example.com/directory?sort=price&category=electronics",
		FinalURL:       "https://example.com/directory?sort=price&category=electronics",
		StatusCode:     200,
		Depth:          2,
		WordCount:      400,
		InternalLinks:  make([]string, 35),
		ListingSignals: 0,
		Headings:       types.HeadingData{H1: []string{"Electronics"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateListing {
		t.Errorf("Template = %q, want 'listing'", tpl)
	}
}

func TestDetectListingFilterParams(t *testing.T) {
	// URL with filter/sort query parameters + link density
	page := &types.PageData{
		URL:           "https://example.com/cars?brand=toyota&price=10000",
		FinalURL:      "https://example.com/cars?brand=toyota&price=10000",
		StatusCode:    200,
		Depth:         2,
		WordCount:     300,
		InternalLinks: make([]string, 25),
		Headings:      types.HeadingData{H1: []string{"Toyota Cars"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateListing {
		t.Errorf("Template = %q, want 'listing'", tpl)
	}
}

func TestDetectArticleNotListing(t *testing.T) {
	// Article with high link density should still be classified as article
	// when article signals (URL, word count, headings, structured data) dominate
	page := &types.PageData{
		URL:            "https://example.com/blog/top-50-products",
		FinalURL:       "https://example.com/blog/top-50-products",
		StatusCode:     200,
		Depth:          2,
		WordCount:      2000,
		InternalLinks:  make([]string, 25),
		ListingSignals: 0,
		Headings:       types.HeadingData{H1: []string{"Top 50 Products of 2025"}, H2: []string{"Intro", "Methodology", "Results"}},
		StructuredData: []types.StructuredDataEntry{{Type: "Article", Format: "json-ld"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateArticle {
		t.Errorf("Template = %q, want 'article' (article signals should dominate)", tpl)
	}
}

func TestDetectProductNotListing(t *testing.T) {
	// Product page with some listing signals should still classify as product
	// when product signals (URL, schema, OG) are stronger
	page := &types.PageData{
		URL:            "https://example.com/products/widget-pro",
		FinalURL:       "https://example.com/products/widget-pro",
		StatusCode:     200,
		Depth:          3,
		WordCount:      400,
		InternalLinks:  make([]string, 10),
		ListingSignals: 1,
		Images:         make([]types.ImageData, 3),
		Headings:       types.HeadingData{H1: []string{"Widget Pro"}},
		StructuredData: []types.StructuredDataEntry{{Type: "Product", Format: "json-ld"}},
		OpenGraph:      map[string]string{"type": "product"},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateProduct {
		t.Errorf("Template = %q, want 'product' (product signals should dominate)", tpl)
	}
}

func TestDetectAdDetailMarketplace(t *testing.T) {
	// Marketplace ad page (otomoto-style: /oferta/ URL + ad detail signals)
	page := &types.PageData{
		URL:             "https://example.com/osobowe/oferta/bmw-seria-1-ID6HFD3B.html",
		FinalURL:        "https://example.com/osobowe/oferta/bmw-seria-1-ID6HFD3B.html",
		StatusCode:      200,
		Depth:           3,
		WordCount:       350,
		AdDetailSignals: 3,
		Images:          make([]types.ImageData, 8),
		Headings:        types.HeadingData{H1: []string{"BMW Seria 1 2019"}},
		StructuredData:  []types.StructuredDataEntry{{Type: "Vehicle", Format: "json-ld"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateAdDetail {
		t.Errorf("Template = %q, want 'ad_detail'", tpl)
	}
}

func TestDetectAdDetailRealEstate(t *testing.T) {
	// Real estate listing detail page
	page := &types.PageData{
		URL:             "https://example.com/annonce/appartement-paris-12345.htm",
		FinalURL:        "https://example.com/annonce/appartement-paris-12345.htm",
		StatusCode:      200,
		Depth:           2,
		WordCount:       400,
		AdDetailSignals: 2,
		Images:          make([]types.ImageData, 10),
		Headings:        types.HeadingData{H1: []string{"Appartement Paris 3 pièces"}},
		StructuredData:  []types.StructuredDataEntry{{Type: "Apartment", Format: "json-ld"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateAdDetail {
		t.Errorf("Template = %q, want 'ad_detail'", tpl)
	}
}

func TestDetectAdDetailEbayItem(t *testing.T) {
	// eBay-style item page with numeric ID
	page := &types.PageData{
		URL:             "https://example.com/itm/vintage-camera-123456789",
		FinalURL:        "https://example.com/itm/vintage-camera-123456789",
		StatusCode:      200,
		Depth:           2,
		WordCount:       250,
		AdDetailSignals: 1,
		Images:          make([]types.ImageData, 6),
		Headings:        types.HeadingData{H1: []string{"Vintage Camera"}},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateAdDetail {
		t.Errorf("Template = %q, want 'ad_detail'", tpl)
	}
}

func TestDetectProductNotAdDetail(t *testing.T) {
	// E-commerce product page with add-to-cart signals should stay product, not ad_detail
	page := &types.PageData{
		URL:             "https://example.com/products/widget-pro",
		FinalURL:        "https://example.com/products/widget-pro",
		StatusCode:      200,
		Depth:           3,
		WordCount:       500,
		AdDetailSignals: -2, // negative = e-commerce signals detected
		Images:          make([]types.ImageData, 5),
		Headings:        types.HeadingData{H1: []string{"Widget Pro"}},
		StructuredData:  []types.StructuredDataEntry{{Type: "Product", Format: "json-ld"}},
		OpenGraph:       map[string]string{"type": "product"},
	}
	tpl := DetectTemplateType(page, "https://example.com/")
	if tpl != TemplateProduct {
		t.Errorf("Template = %q, want 'product' (e-commerce signals should dominate)", tpl)
	}
}
