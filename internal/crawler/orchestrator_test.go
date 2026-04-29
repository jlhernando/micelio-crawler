package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// testSite creates a mock website with a given number of linked pages.
func testSite(t *testing.T, pageCount int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		path := r.URL.Path
		if path == "/" || path == "" {
			// Homepage with links to all pages
			html := "<html><head><title>Home</title></head><body>"
			for i := 1; i < pageCount; i++ {
				html += fmt.Sprintf(`<a href="/page%d">Page %d</a>`, i, i)
			}
			html += "</body></html>"
			fmt.Fprint(w, html)
			return
		}
		// Individual page
		fmt.Fprintf(w, `<html><head><title>%s</title></head><body><a href="/">Home</a></body></html>`, path)
	}))
}

func TestCrawlBasic(t *testing.T) {
	srv := testSite(t, 5)
	defer srv.Close()

	config := types.CrawlConfig{
		SeedURL:     srv.URL,
		Mode:        types.ModeSpider,
		MaxDepth:    2,
		MaxPages:    10,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	var progressCalls atomic.Int32
	result, err := Crawl(context.Background(), config, func(p CrawlProgress) {
		progressCalls.Add(1)
	})
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	if len(result.Pages) < 2 {
		t.Errorf("expected at least 2 pages, got %d", len(result.Pages))
	}
	if result.Duration <= 0 {
		t.Error("expected positive duration")
	}
	if progressCalls.Load() == 0 {
		t.Error("expected at least one progress callback")
	}

	// Verify we got the homepage
	foundHome := false
	for _, p := range result.Pages {
		if p.StatusCode == 200 && p.Title != nil && p.Title.Text == "Home" {
			foundHome = true
			break
		}
	}
	if !foundHome {
		t.Error("expected to find homepage with title 'Home'")
	}
}

func TestCrawlWithStore(t *testing.T) {
	srv := testSite(t, 4)
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "crawl.db")
	store, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	config := types.CrawlConfig{
		SeedURL:     srv.URL,
		Mode:        types.ModeSpider,
		MaxDepth:    2,
		MaxPages:    10,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(context.Background(), config, nil, store)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	// DB should have same pages as result
	dbCount, err := store.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if dbCount != len(result.Pages) {
		t.Errorf("DB has %d pages, result has %d", dbCount, len(result.Pages))
	}

	// Status should be "complete"
	status, err := store.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != "complete" {
		t.Errorf("expected status 'complete', got %q", status)
	}

	// Config should be stored
	cfg, err := store.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected stored config, got nil")
	}
	if cfg.SeedURL != srv.URL {
		t.Errorf("expected seed URL %q, got %q", srv.URL, cfg.SeedURL)
	}
}

func TestCrawlResume(t *testing.T) {
	// Create a server where homepage links to 6 pages
	srv := testSite(t, 7)
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "resume.db")

	// First crawl: limited to 3 pages
	store1, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}

	config := types.CrawlConfig{
		SeedURL:     srv.URL,
		Mode:        types.ModeSpider,
		MaxDepth:    2,
		MaxPages:    3,
		Concurrency: 1,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result1, err := Crawl(context.Background(), config, nil, store1)
	if err != nil {
		t.Fatalf("First crawl: %v", err)
	}
	firstCount := len(result1.Pages)
	if firstCount == 0 {
		t.Fatal("first crawl produced 0 pages")
	}
	store1.Close()

	// Second crawl: resume with higher limit
	store2, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore (resume): %v", err)
	}
	defer store2.Close()

	config.MaxPages = 10
	config.Resume = true

	result2, err := Crawl(context.Background(), config, nil, store2)
	if err != nil {
		t.Fatalf("Resume crawl: %v", err)
	}

	// Should have more pages than first crawl (resumed + new)
	if len(result2.Pages) <= firstCount {
		t.Errorf("resume should crawl more pages: first=%d, resumed=%d", firstCount, len(result2.Pages))
	}

	// DB page count should have grown (new pages were inserted)
	dbCount, _ := store2.PageCount()
	if dbCount <= firstCount {
		t.Errorf("DB should have more pages after resume: first=%d, now=%d", firstCount, dbCount)
	}
}

func TestCrawlCancellation(t *testing.T) {
	// Create a server with many pages and delay
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html")
		html := "<html><head><title>Slow</title></head><body>"
		for i := 0; i < 100; i++ {
			html += fmt.Sprintf(`<a href="/page%d">Page %d</a>`, i, i)
		}
		html += "</body></html>"
		fmt.Fprint(w, html)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	config := types.CrawlConfig{
		SeedURL:     srv.URL,
		Mode:        types.ModeSpider,
		MaxDepth:    5,
		MaxPages:    1000,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(ctx, config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	// Should have some pages but not all 1000
	if len(result.Pages) == 0 {
		t.Error("expected at least some pages before cancellation")
	}
	if len(result.Pages) >= 50 {
		t.Errorf("expected cancellation to limit pages, got %d", len(result.Pages))
	}
}

func TestCrawlListMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><head><title>%s</title></head><body>OK</body></html>", r.URL.Path)
	}))
	defer srv.Close()

	urls := []string{
		srv.URL + "/page1",
		srv.URL + "/page2",
		srv.URL + "/page3",
	}

	config := types.CrawlConfig{
		SeedURL:     urls[0],
		Mode:        types.ModeList,
		URLs:        urls,
		MaxPages:    10,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	if len(result.Pages) != 3 {
		t.Errorf("expected 3 pages in list mode, got %d", len(result.Pages))
	}
}

func TestCrawlMaxErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			html := "<html><body>"
			for i := 0; i < 20; i++ {
				html += fmt.Sprintf(`<a href="/err%d">Err %d</a>`, i, i)
			}
			html += "</body></html>"
			fmt.Fprint(w, html)
			return
		}
		// All other pages return 500
		w.WriteHeader(500)
		fmt.Fprint(w, "error")
	}))
	defer srv.Close()

	config := types.CrawlConfig{
		SeedURL:     srv.URL,
		Mode:        types.ModeSpider,
		MaxDepth:    2,
		MaxPages:    100,
		MaxErrors:   3,
		Concurrency: 1,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	// Should stop after MaxErrors
	if result.Errors < 3 {
		t.Errorf("expected at least 3 errors, got %d", result.Errors)
	}
	// Shouldn't crawl all 20 error pages
	if len(result.Pages) > 10 {
		t.Errorf("expected fewer pages due to MaxErrors, got %d", len(result.Pages))
	}
}
