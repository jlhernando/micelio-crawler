package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestLightenPages(t *testing.T) {
	pages := []*types.PageData{
		{
			URL:        "https://example.com/",
			StatusCode: 200,
			Title:      &types.TextLength{Text: "Home", Length: 4},
			MetaDescription: &types.TextLength{Text: "Welcome", Length: 7},
			Headings:   types.HeadingData{H1: []string{"Welcome Home"}},
			Depth:      0,
			WordCount:  500,
			InternalLinks: []string{"/about", "/contact"},
			ExternalLinks: []string{"https://twitter.com"},
			Inlinks:    10,
			PageRank:   0.15,
			Indexability: types.IndexabilityData{Indexable: true},
			URLIssues:  []string{"missing-h2"},
		},
		{
			URL:        "https://example.com/about",
			StatusCode: 200,
			Depth:      1,
			WordCount:  300,
			GscData:    &types.GscData{Impressions: 1000, Clicks: 50, CTR: 0.05, Position: 8.0},
			Ga4Data:    &types.Ga4Data{Sessions: 200, Pageviews: 250},
		},
	}

	light := LightenPages(pages)
	if len(light) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(light))
	}

	p0 := light[0]
	if p0.Title != "Home" {
		t.Fatalf("expected Home, got %s", p0.Title)
	}
	if p0.InternalLinks != 2 {
		t.Fatalf("expected 2 internal links, got %d", p0.InternalLinks)
	}
	if !p0.Indexable {
		t.Fatal("expected indexable")
	}
	if len(p0.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(p0.Issues))
	}

	p1 := light[1]
	if p1.GscImpressions == nil || *p1.GscImpressions != 1000 {
		t.Fatalf("expected 1000 GSC impressions, got %v", p1.GscImpressions)
	}
	if p1.Ga4Sessions == nil || *p1.Ga4Sessions != 200 {
		t.Fatalf("expected 200 GA4 sessions, got %v", p1.Ga4Sessions)
	}
}

func TestGenerateHTML(t *testing.T) {
	stats := &types.CrawlStats{
		TotalPages:             2,
		PagesWithoutTitle:      0,
		PagesWithoutH1:         1,
		PagesWithoutDescription: 1,
		StatusCodes:            map[int]int{200: 2},
		DepthDistribution:      map[int]int{0: 1, 1: 1},
	}

	pages := []*types.PageData{
		{
			URL:        "https://example.com/",
			StatusCode: 200,
			Title:      &types.TextLength{Text: "Home", Length: 4},
			Depth:      0,
			WordCount:  500,
			Indexability: types.IndexabilityData{Indexable: true},
		},
	}

	var buf bytes.Buffer
	err := GenerateHTML(&buf, "https://example.com", stats, pages)
	if err != nil {
		t.Fatal(err)
	}

	html := buf.String()
	if !strings.Contains(html, "Micelio Crawl Report") {
		t.Fatal("expected report title in HTML")
	}
	if !strings.Contains(html, "https://example.com") {
		t.Fatal("expected seed URL in HTML")
	}
	if !strings.Contains(html, "Home") {
		t.Fatal("expected page title in embedded data")
	}
	if !strings.Contains(html, "</html>") {
		t.Fatal("expected closing HTML tag")
	}
}
