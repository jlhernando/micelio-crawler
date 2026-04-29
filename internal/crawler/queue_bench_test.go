package crawler

import (
	"fmt"
	"testing"
)

func BenchmarkQueueEnqueue(b *testing.B) {
	q := NewCrawlQueue("https://example.com", 10, 1000000, nil)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		q.Enqueue(fmt.Sprintf("https://example.com/page-%d", i), i%5, nil)
	}
}

func BenchmarkQueueEnqueueDequeue(b *testing.B) {
	q := NewCrawlQueue("https://example.com", 10, 1000000, nil)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		url := fmt.Sprintf("https://example.com/page-%d", i)
		q.Enqueue(url, 1, nil)
		q.Dequeue()
	}
}

func BenchmarkQueueHas(b *testing.B) {
	q := NewCrawlQueue("https://example.com", 10, 100000, nil)
	for i := 0; i < 10000; i++ {
		q.Enqueue(fmt.Sprintf("https://example.com/page-%d", i), 1, nil)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		q.Has(fmt.Sprintf("https://example.com/page-%d", i%10000))
	}
}

func BenchmarkNormalizeURL(b *testing.B) {
	urls := []string{
		"https://example.com/page",
		"https://example.com/page/",
		"HTTPS://EXAMPLE.COM/Page?b=2&a=1",
		"https://example.com/page?utm_source=test&key=value",
		"https://example.com/path/to/deep/page#section",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		NormalizeURL(urls[i%len(urls)])
	}
}
