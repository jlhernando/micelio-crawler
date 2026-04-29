package analysis

import (
	"strings"
	"testing"
)

func buildCorpus(pages, wordsPerPage int) []string {
	words := []string{
		"search", "engine", "optimization", "crawl", "index", "page", "link",
		"content", "metadata", "title", "description", "heading", "anchor",
		"internal", "external", "redirect", "canonical", "sitemap", "robots",
		"performance", "speed", "analysis", "report", "dashboard", "monitor",
		"keyword", "ranking", "traffic", "impression", "click", "position",
		"semantic", "similarity", "embedding", "vector", "dimension", "score",
		"graph", "node", "edge", "centrality", "pagerank", "authority", "hub",
	}

	corpus := make([]string, pages)
	for i := 0; i < pages; i++ {
		var sb strings.Builder
		for j := 0; j < wordsPerPage; j++ {
			if j > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(words[(i+j)%len(words)])
			if j%15 == 14 {
				sb.WriteString(". ")
			}
		}
		corpus[i] = sb.String()
	}
	return corpus
}

func BenchmarkNgrams_10Pages(b *testing.B) {
	corpus := buildCorpus(10, 500)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a := NewNgramAnalyzer("en")
		for _, text := range corpus {
			a.AddPage(text)
		}
		a.GetResults(50)
	}
}

func BenchmarkNgrams_100Pages(b *testing.B) {
	corpus := buildCorpus(100, 500)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a := NewNgramAnalyzer("en")
		for _, text := range corpus {
			a.AddPage(text)
		}
		a.GetResults(50)
	}
}

func BenchmarkNgrams_1000Pages(b *testing.B) {
	corpus := buildCorpus(1000, 300)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a := NewNgramAnalyzer("en")
		for _, text := range corpus {
			a.AddPage(text)
		}
		a.GetResults(50)
	}
}

func BenchmarkTokenize(b *testing.B) {
	text := strings.Repeat("search engine optimization provides significant value for website visibility and organic traffic growth. ", 50)
	sw := getStopwords("en")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tokenize(text, sw)
	}
}
