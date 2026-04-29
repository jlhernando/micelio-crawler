package analysis

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkSimhash(b *testing.B) {
	text := strings.Repeat("This is a sample text for simhash fingerprinting to detect near-duplicate content across multiple pages. ", 20)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Simhash(text)
	}
}

func BenchmarkHammingDistance(b *testing.B) {
	a := Simhash("first document with unique content about search optimization")
	c := Simhash("second document with different content about crawl analysis")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		HammingDistance(a, c)
	}
}

func BenchmarkFindNearDuplicates_100(b *testing.B) {
	items := make([]SimhashItem, 100)
	base := "This is the base text for generating near-duplicate fingerprints in batch."
	for i := 0; i < 100; i++ {
		text := base
		if i%5 == 0 {
			text = fmt.Sprintf("Variant %d: %s", i, base)
		}
		items[i] = SimhashItem{
			URL:         fmt.Sprintf("https://example.com/page-%d", i),
			Fingerprint: Simhash(text),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		FindNearDuplicates(items, 90)
	}
}

func BenchmarkFindNearDuplicates_1000(b *testing.B) {
	items := make([]SimhashItem, 1000)
	base := "This is the base text for generating near-duplicate fingerprints in batch."
	for i := 0; i < 1000; i++ {
		text := fmt.Sprintf("Page %d content variant: %s word%d", i, base, i%50)
		items[i] = SimhashItem{
			URL:         fmt.Sprintf("https://example.com/page-%d", i),
			Fingerprint: Simhash(text),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		FindNearDuplicates(items, 90)
	}
}
