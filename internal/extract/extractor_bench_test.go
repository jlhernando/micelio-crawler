package extract

import (
	"fmt"
	"strings"
	"testing"
)

// buildHTML generates a realistic HTML page with the given number of links and paragraphs.
func buildHTML(numLinks, numParagraphs int) string {
	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html lang="en"><head>
<title>Benchmark Page - SEO Analysis Tool</title>
<meta name="description" content="This is a benchmark page for testing HTML extraction performance in Micelio.">
<meta name="robots" content="index, follow">
<link rel="canonical" href="https://example.com/bench">
<link rel="alternate" hreflang="es" href="https://example.com/es/bench">
<link rel="alternate" hreflang="fr" href="https://example.com/fr/bench">
<meta property="og:title" content="Benchmark Page">
<meta property="og:description" content="Testing extraction speed">
<meta property="og:type" content="article">
<meta name="twitter:card" content="summary">
<script type="application/ld+json">{"@type":"Article","headline":"Benchmark","author":{"@type":"Person","name":"Test"}}</script>
</head><body>
<header><nav>`)

	for i := 0; i < numLinks/4; i++ {
		fmt.Fprintf(&sb, `<a href="/nav/page-%d">Nav Link %d</a>`, i, i)
	}
	sb.WriteString(`</nav></header><main>
<h1>Main Heading for Benchmark</h1>
<h2>Subheading One</h2>`)

	for i := 0; i < numParagraphs; i++ {
		sb.WriteString(`<p>This is paragraph content for benchmarking the HTML extraction module. It contains multiple sentences with varying complexity. The readability analyzer should process this text efficiently. Each paragraph adds more content for word counting and text analysis. This benchmarks the performance of the goquery-based extraction pipeline.</p>`)
	}

	sb.WriteString(`<h2>Subheading Two</h2>`)

	for i := 0; i < numLinks/2; i++ {
		fmt.Fprintf(&sb, `<a href="/internal/page-%d">Internal Link %d</a>`, i, i)
	}
	for i := 0; i < numLinks/4; i++ {
		fmt.Fprintf(&sb, `<a href="https://external.com/page-%d">External Link %d</a>`, i, i)
	}

	for i := 0; i < 5; i++ {
		fmt.Fprintf(&sb, `<img src="/images/photo-%d.jpg" alt="Photo %d" width="800" height="600">`, i, i)
	}

	sb.WriteString(`</main><footer>`)
	for i := 0; i < numLinks/8+1; i++ {
		fmt.Fprintf(&sb, `<a href="/footer/page-%d">Footer %d</a>`, i, i)
	}
	sb.WriteString(`</footer></body></html>`)
	return sb.String()
}

func BenchmarkExtractPageData_Small(b *testing.B) {
	html := buildHTML(10, 3)
	opts := ExtractionOptions{
		PageURL:    "https://example.com/bench",
		FinalURL:   "https://example.com/bench",
		StatusCode: 200,
		Depth:      1,
		Headers:    map[string]string{"content-type": "text/html"},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ExtractPageData(html, opts)
	}
}

func BenchmarkExtractPageData_Medium(b *testing.B) {
	html := buildHTML(50, 20)
	opts := ExtractionOptions{
		PageURL:    "https://example.com/bench",
		FinalURL:   "https://example.com/bench",
		StatusCode: 200,
		Depth:      2,
		Headers:    map[string]string{"content-type": "text/html", "strict-transport-security": "max-age=31536000"},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ExtractPageData(html, opts)
	}
}

func BenchmarkExtractPageData_Large(b *testing.B) {
	html := buildHTML(200, 100)
	opts := ExtractionOptions{
		PageURL:    "https://example.com/bench",
		FinalURL:   "https://example.com/bench",
		StatusCode: 200,
		Depth:      3,
		Headers:    map[string]string{"content-type": "text/html"},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ExtractPageData(html, opts)
	}
}

func BenchmarkExtractBodyText(b *testing.B) {
	html := buildHTML(50, 50)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ExtractBodyText(html)
	}
}
