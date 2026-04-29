package analysis

import (
	"fmt"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func buildPageGraph(n int) []*types.PageData {
	pages := make([]*types.PageData, n)
	for i := 0; i < n; i++ {
		pages[i] = &types.PageData{
			URL:        fmt.Sprintf("https://example.com/page-%d", i),
			StatusCode: 200,
			Depth:      i % 5,
		}
	}

	// Create a realistic link structure: each page links to 3-10 others
	for i := 0; i < n; i++ {
		linkCount := 3 + (i % 8)
		links := make([]string, 0, linkCount)
		for j := 0; j < linkCount; j++ {
			target := (i + j + 1) % n
			links = append(links, pages[target].URL)
		}
		pages[i].InternalLinks = links
	}

	return pages
}

func BenchmarkPageRank_100(b *testing.B) {
	pages := buildPageGraph(100)
	opts := DefaultPageRankOptions()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputePageRank(pages, opts)
	}
}

func BenchmarkPageRank_1000(b *testing.B) {
	pages := buildPageGraph(1000)
	opts := DefaultPageRankOptions()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputePageRank(pages, opts)
	}
}

func BenchmarkPageRank_10000(b *testing.B) {
	pages := buildPageGraph(10000)
	opts := DefaultPageRankOptions()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputePageRank(pages, opts)
	}
}
