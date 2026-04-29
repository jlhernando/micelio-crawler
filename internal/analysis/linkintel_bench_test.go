package analysis

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func BenchmarkBuildAdjacencyList_1000(b *testing.B) {
	pages := buildPageGraph(1000)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		BuildAdjacencyList(pages)
	}
}

func BenchmarkComputeClickDepth_1000(b *testing.B) {
	pages := buildPageGraph(1000)
	graph := BuildAdjacencyList(pages)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputeClickDepth(pages[0].URL, graph)
	}
}

func BenchmarkComputeHits_500(b *testing.B) {
	pages := buildPageGraph(500)
	graph := BuildAdjacencyList(pages)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputeHits(50, 0.0001, graph)
	}
}

func BenchmarkComputeBetweennessCentrality_500(b *testing.B) {
	pages := buildPageGraph(500)
	graph := BuildAdjacencyList(pages)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputeBetweennessCentrality(5000, graph)
	}
}

func BenchmarkComputeClosenessCentrality_500(b *testing.B) {
	pages := buildPageGraph(500)
	graph := BuildAdjacencyList(pages)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ComputeClosenessCentrality(5000, graph)
	}
}

func BenchmarkGenerateLinkSuggestions(b *testing.B) {
	pages := buildPageGraph(200)
	for i, p := range pages {
		p.WordCount = 300 + (i % 500)
		p.Indexability = types.IndexabilityData{Indexable: true}
	}

	graph := BuildAdjacencyList(pages)
	inDeg := make([]int, graph.N)
	for _, j := range graph.Edges {
		inDeg[j]++
	}

	// Build wordCounts and indexable arrays
	wordCounts := make([]int32, graph.N)
	indexable := make([]bool, graph.N)
	for i, p := range pages {
		wordCounts[i] = int32(p.WordCount)
		indexable[i] = p.Indexability.Indexable
	}

	embeddings := make(map[string][]float64, len(pages))
	for _, p := range pages {
		vec := make([]float64, 128)
		for j := range vec {
			vec[j] = float64(len(p.URL)%10+j) / 100.0
		}
		embeddings[p.URL] = vec
	}

	pageRanks := ComputePageRank(pages, DefaultPageRankOptions())
	clickDepths := ComputeClickDepth(pages[0].URL, graph)
	opts := DefaultLinkSuggestionsOptions()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		GenerateLinkSuggestions(graph, embeddings, clickDepths, pageRanks, inDeg, wordCounts, indexable, opts)
	}
}

func BenchmarkCosineSimilarity(b *testing.B) {
	a := make([]float64, 1536)
	c := make([]float64, 1536)
	for i := range a {
		a[i] = float64(i) / 1536.0
		c[i] = float64(1536-i) / 1536.0
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		CosineSimilarity(a, c)
	}
}
