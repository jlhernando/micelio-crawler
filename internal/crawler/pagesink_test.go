package crawler

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

func sinkTestPage(i int) *types.PageData {
	return &types.PageData{
		URL:         fmt.Sprintf("https://ex.com/p%d", i),
		StatusCode:  200,
		ETag:        fmt.Sprintf(`"e%d"`, i),
		ContentHash: fmt.Sprintf("h%d", i),
		Title:       &types.TextLength{Text: strings.Repeat("title ", 10), Length: 60},
		InternalLinks: []string{
			"https://ex.com/", "https://ex.com/a", "https://ex.com/b",
		},
		BodyText:  strings.Repeat("lorem ipsum dolor sit amet ", 100),
		CrawledAt: time.Now().UTC(),
	}
}

// Concurrent workers feed the sink; after Close every page is in the DB
// (with cache headers, hashes and HTML) and in the JSONL file exactly once.
func TestPageSinkWritesEverything(t *testing.T) {
	dir := t.TempDir()
	db, err := storage.NewCrawlStore(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := filepath.Join(dir, "out.jsonl")
	w, err := storage.NewResultWriter(out, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var errs atomic.Int32
	sink := newPageSink(w, db, &errs)

	const n = 1234 // not a multiple of the batch size
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < n; i += 8 {
				html := ""
				if i%10 == 0 {
					html = "<html>" + fmt.Sprint(i) + "</html>"
				}
				sink.put(sink.encode(sinkTestPage(i), html, ""))
			}
		}(g)
	}
	wg.Wait()
	sink.Close()
	sink.Close() // idempotent
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if c, _ := db.PageCount(); c != n {
		t.Errorf("db pages = %d, want %d", c, n)
	}
	if h, _ := db.GetCacheHeaders(); len(h) != n {
		t.Errorf("cache headers = %d, want %d", len(h), n)
	}
	if h, _ := db.GetContentHashes(); len(h) != n {
		t.Errorf("content hashes = %d, want %d", len(h), n)
	}
	src, _, err := db.GetPageHTML("https://ex.com/p20")
	if err != nil || src != "<html>20</html>" {
		t.Errorf("page html = %q, %v", src, err)
	}
	pages, err := storage.ReadJSONLPages(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != n {
		t.Errorf("jsonl pages = %d, want %d", len(pages), n)
	}
	seen := map[string]bool{}
	for _, p := range pages {
		if seen[p.URL] {
			t.Fatalf("duplicate %s", p.URL)
		}
		seen[p.URL] = true
	}
	if errs.Load() != 0 {
		t.Errorf("errors = %d", errs.Load())
	}
}

// Flush makes everything queued so far durable without stopping the sink
// (a partial batch must not wait for 200 pages).
func TestPageSinkFlushCommitsPartialBatch(t *testing.T) {
	db, err := storage.NewCrawlStore(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var errs atomic.Int32
	sink := newPageSink(nil, db, &errs)
	defer sink.Close()
	for i := 0; i < 7; i++ {
		sink.put(sink.encode(sinkTestPage(i), "", ""))
	}
	sink.Flush()
	if c, _ := db.PageCount(); c != 7 {
		t.Errorf("after Flush db pages = %d, want 7", c)
	}
	// The timer also commits a partial batch on its own (paused crawls).
	sink.put(sink.encode(sinkTestPage(100), "", ""))
	deadline := time.Now().Add(5 * sinkFlushInterval)
	for time.Now().Before(deadline) {
		if c, _ := db.PageCount(); c == 8 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("partial batch not committed by the flush timer")
}

// BenchmarkPageOutput compares the old per-page path (JSON encode + zstd +
// SQLite commit + JSONL write, all under one mutex) with the sink, using 16
// concurrent workers.
func BenchmarkPageOutput(b *testing.B) {
	run := func(b *testing.B, store func(db *storage.CrawlStore, w *storage.ResultWriter) (func(*types.PageData), func())) {
		dir := b.TempDir()
		db, err := storage.NewCrawlStore(filepath.Join(dir, "c.db"))
		if err != nil {
			b.Fatal(err)
		}
		defer db.Close()
		w, _ := storage.NewResultWriter(filepath.Join(dir, "o.jsonl"), "jsonl")
		defer w.Close()
		put, done := store(db, w)
		b.ResetTimer()
		var next atomic.Int64
		var wg sync.WaitGroup
		for g := 0; g < 16; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := next.Add(1)
					if i > int64(b.N) {
						return
					}
					put(sinkTestPage(int(i)))
				}
			}()
		}
		wg.Wait()
		done()
	}
	b.Run("locked-per-page", func(b *testing.B) {
		run(b, func(db *storage.CrawlStore, w *storage.ResultWriter) (func(*types.PageData), func()) {
			var mu sync.Mutex
			return func(p *types.PageData) {
				mu.Lock()
				_ = w.Write(p)
				_ = db.InsertPage(p)
				mu.Unlock()
			}, func() {}
		})
	})
	b.Run("sink", func(b *testing.B) {
		run(b, func(db *storage.CrawlStore, w *storage.ResultWriter) (func(*types.PageData), func()) {
			var errs atomic.Int32
			sink := newPageSink(w, db, &errs)
			return func(p *types.PageData) { sink.put(sink.encode(p, "", "")) }, sink.Close
		})
	})
}
