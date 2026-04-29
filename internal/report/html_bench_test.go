package report

import (
	"fmt"
	"io"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func buildBenchPages(n int) []*types.PageData {
	pages := make([]*types.PageData, n)
	for i := 0; i < n; i++ {
		title := types.TextLength{Text: fmt.Sprintf("Page Title %d - SEO Optimized", i), Length: 30}
		desc := types.TextLength{Text: fmt.Sprintf("Meta description for page %d with detailed content about SEO analysis.", i), Length: 70}
		canonical := fmt.Sprintf("https://example.com/page-%d", i)

		links := make([]string, 5)
		for j := 0; j < 5; j++ {
			links[j] = fmt.Sprintf("https://example.com/page-%d", (i+j+1)%n)
		}

		extLinks := []string{
			"https://external.com/ref-1",
			"https://external.com/ref-2",
		}

		var issues []string
		if i%10 == 0 {
			issues = []string{"missing-h1", "title-too-long"}
		}

		pages[i] = &types.PageData{
			URL:             fmt.Sprintf("https://example.com/page-%d", i),
			StatusCode:      200,
			Title:           &title,
			MetaDescription: &desc,
			Canonical:       &canonical,
			Headings:        types.HeadingData{H1: []string{fmt.Sprintf("Heading %d", i)}},
			Depth:           i % 5,
			WordCount:       300 + i%500,
			ResponseTimeMs:  int64(50 + i%200),
			InternalLinks:   links,
			ExternalLinks:   extLinks,
			Inlinks:         3 + i%10,
			PageRank:        float64(i%10) + 0.5,
			Indexability:    types.IndexabilityData{Indexable: true},
			URLIssues:       issues,
		}
	}
	return pages
}

func BenchmarkLightenPages_100(b *testing.B) {
	pages := buildBenchPages(100)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		LightenPages(pages)
	}
}

func BenchmarkLightenPages_1000(b *testing.B) {
	pages := buildBenchPages(1000)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		LightenPages(pages)
	}
}

func BenchmarkGenerateHTML_100(b *testing.B) {
	pages := buildBenchPages(100)
	stats := &types.CrawlStats{TotalPages: 100}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		GenerateHTML(io.Discard, "https://example.com", stats, pages)
	}
}

func BenchmarkGenerateHTML_1000(b *testing.B) {
	pages := buildBenchPages(1000)
	stats := &types.CrawlStats{TotalPages: 1000}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		GenerateHTML(io.Discard, "https://example.com", stats, pages)
	}
}
