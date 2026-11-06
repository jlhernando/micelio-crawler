package logs

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeBenchLog writes n combined-format lines. Every 2nd line is a bot
// (Googlebot or ClaudeBot); paths repeat every uniq lines so the URL map
// grows to uniq entries (the shape of a real site: many hits per URL).
func writeBenchLog(tb testing.TB, path string, n, uniq int, gz bool) {
	tb.Helper()
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	var w *bufio.Writer
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(f)
		w = bufio.NewWriterSize(zw, 1<<20)
	} else {
		w = bufio.NewWriterSize(f, 1<<20)
	}
	uas := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)",
	}
	statuses := []int{200, 200, 200, 301, 404}
	for i := 0; i < n; i++ {
		ua := uas[i%len(uas)]
		day := 1 + (i/50000)%28
		fmt.Fprintf(w, "66.249.%d.%d - - [%02d/Apr/2026:%02d:%02d:%02d +0000] \"GET /section-%d/page-%d?x=%d HTTP/1.1\" %d %d \"-\" \"%s\"\n",
			(i/256)%256, i%256, day, (i/3600)%24, (i/60)%60, i%60,
			(i%uniq)%50, i%uniq, i%3, statuses[i%5], 1000+i%5000, ua)
	}
	if err := w.Flush(); err != nil {
		tb.Fatal(err)
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			tb.Fatal(err)
		}
	}
}

// BenchmarkAnalyzeMulti parses 3 files × 400K lines (1.2M lines, up to
// 300K distinct URLs) with the dashboard's settings (nil flusher).
func BenchmarkAnalyzeMulti(b *testing.B) {
	dir := b.TempDir()
	const perFile = 400_000
	var paths []string
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("access-%d.log", i))
		writeBenchLog(b, p, perFile, 100_000*(i+1), false)
		paths = append(paths, p)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := AnalyzeMulti(paths, "", 0, nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		if res.Stats.TotalHits != 3*perFile {
			b.Fatalf("hits = %d", res.Stats.TotalHits)
		}
	}
	b.ReportMetric(float64(3*perFile*b.N)/b.Elapsed().Seconds(), "lines/s")
}

// BenchmarkAnalyzeFullGzip parses one gzip file of 500K lines.
func BenchmarkAnalyzeFullGzip(b *testing.B) {
	p := filepath.Join(b.TempDir(), "access.log.gz")
	const n = 500_000
	writeBenchLog(b, p, n, 200_000, true)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := AnalyzeFull(p, "", nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		if res.Stats.TotalHits != n {
			b.Fatalf("hits = %d", res.Stats.TotalHits)
		}
	}
	b.ReportMetric(float64(n*b.N)/b.Elapsed().Seconds(), "lines/s")
}
