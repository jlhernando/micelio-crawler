package storage

import (
	"compress/zlib"
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/micelio/micelio/internal/types"
)

// TestCrawlStoreZstdCompression verifies zstd compression is used for new inserts
// and that data round-trips correctly.
func TestCrawlStoreZstdCompression(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-zstd.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	page := &types.PageData{
		URL:        "https://example.com/zstd-test",
		StatusCode: 200,
		CrawledAt:  time.Now().UTC(),
		WordCount:  500,
	}
	if err := store.InsertPage(page); err != nil {
		t.Fatalf("InsertPage: %v", err)
	}

	// Read raw blob to verify it's zstd-compressed (magic: 0x28 0xB5 0x2F 0xFD)
	var rawBlob []byte
	err = sqlitex.Execute(store.conn,
		`SELECT data FROM pages WHERE url = ?`,
		&sqlitex.ExecOptions{
			Args: []any{"https://example.com/zstd-test"},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				rawBlob = make([]byte, stmt.ColumnLen(0))
				stmt.ColumnBytes(0, rawBlob)
				return nil
			},
		},
	)
	if err != nil {
		t.Fatalf("SELECT raw blob: %v", err)
	}
	if len(rawBlob) < 4 {
		t.Fatal("blob too short")
	}
	if rawBlob[0] != 0x28 || rawBlob[1] != 0xB5 || rawBlob[2] != 0x2F || rawBlob[3] != 0xFD {
		t.Errorf("expected zstd magic bytes, got: %x %x %x %x", rawBlob[0], rawBlob[1], rawBlob[2], rawBlob[3])
	}

	// Verify round-trip
	pages, err := store.GetAllPages()
	if err != nil {
		t.Fatalf("GetAllPages: %v", err)
	}
	if len(pages) != 1 || pages[0].URL != "https://example.com/zstd-test" {
		t.Errorf("unexpected pages: %v", pages)
	}
}

// TestCrawlStoreZlibBackwardCompat verifies that legacy zlib-compressed blobs
// can still be read after switching to zstd for new writes.
func TestCrawlStoreZlibBackwardCompat(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-zlib-compat.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	// Manually insert a zlib-compressed blob (simulating legacy data)
	page := &types.PageData{
		URL:        "https://example.com/zlib-legacy",
		StatusCode: 200,
		CrawledAt:  time.Now().UTC(),
	}
	data, _ := json.Marshal(page)
	var buf bytes.Buffer
	w, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	w.Write(data)
	w.Close()
	zlibBlob := buf.Bytes()

	// Verify it starts with zlib magic byte
	if zlibBlob[0] != 0x78 {
		t.Fatalf("expected zlib magic 0x78, got 0x%x", zlibBlob[0])
	}

	// Insert directly as zlib blob
	err = sqlitex.Execute(store.conn,
		`INSERT INTO pages (url, data, status_code, crawled_at) VALUES (?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{page.URL, zlibBlob, 200, time.Now().UTC().Format(time.RFC3339)}},
	)
	if err != nil {
		t.Fatalf("INSERT zlib blob: %v", err)
	}

	// Read back — should decompress zlib correctly
	pages, err := store.GetAllPages()
	if err != nil {
		t.Fatalf("GetAllPages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if pages[0].URL != "https://example.com/zlib-legacy" {
		t.Errorf("URL = %q", pages[0].URL)
	}
}

func TestCrawlStoreBasicOperations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-crawl.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	// Initially empty
	count, err := store.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 pages, got %d", count)
	}

	// Insert a page
	page := &types.PageData{
		URL:        "https://example.com/page1",
		StatusCode: 200,
		CrawledAt:  time.Now().UTC(),
	}
	if err := store.InsertPage(page); err != nil {
		t.Fatalf("InsertPage: %v", err)
	}

	count, err = store.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 page, got %d", count)
	}

	// Get all pages
	pages, err := store.GetAllPages()
	if err != nil {
		t.Fatalf("GetAllPages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected 1 page, got %d", len(pages))
	}
	if pages[0].URL != "https://example.com/page1" {
		t.Errorf("expected URL 'https://example.com/page1', got %q", pages[0].URL)
	}
	if pages[0].StatusCode != 200 {
		t.Errorf("expected status 200, got %d", pages[0].StatusCode)
	}
}

func TestCrawlStoreResume(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-resume.db")

	// First session: insert pages
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}

	for i, u := range []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"} {
		page := &types.PageData{
			URL:        u,
			StatusCode: 200,
			CrawledAt:  time.Now().UTC(),
			Depth:      i,
		}
		if err := store.InsertPage(page); err != nil {
			t.Fatalf("InsertPage %s: %v", u, err)
		}
	}
	store.Close()

	// Second session: resume
	store2, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore (resume): %v", err)
	}
	defer store2.Close()

	// Should have 3 pages
	count, err := store2.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 pages on resume, got %d", count)
	}

	// GetVisitedURLs should return page URLs
	visited, err := store2.GetVisitedURLs()
	if err != nil {
		t.Fatalf("GetVisitedURLs: %v", err)
	}
	if len(visited) != 3 {
		t.Errorf("expected 3 visited URLs, got %d", len(visited))
	}
}

func TestCrawlStoreMeta(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-meta.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	// Save and retrieve meta
	if err := store.SaveMeta("foo", "bar"); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	val, err := store.GetMeta("foo")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if val != "bar" {
		t.Errorf("expected 'bar', got %q", val)
	}

	// Missing key
	val, err = store.GetMeta("missing")
	if err != nil {
		t.Fatalf("GetMeta (missing): %v", err)
	}
	if val != "" {
		t.Errorf("expected empty string for missing key, got %q", val)
	}

	// Status
	if err := store.SetStatus("running"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	status, err := store.GetStatus()
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status != "running" {
		t.Errorf("expected 'running', got %q", status)
	}
}

func TestCrawlStoreConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-config.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	cfg := types.CrawlConfig{
		SeedURL:     "https://example.com",
		MaxPages:    500,
		Concurrency: 3,
	}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := store.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if loaded == nil {
		t.Fatal("GetConfig returned nil")
	}
	if loaded.SeedURL != "https://example.com" {
		t.Errorf("expected seed URL 'https://example.com', got %q", loaded.SeedURL)
	}
	if loaded.MaxPages != 500 {
		t.Errorf("expected MaxPages 500, got %d", loaded.MaxPages)
	}
}

func TestCrawlStoreUpsert(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-upsert.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	// Insert page
	page := &types.PageData{
		URL:        "https://example.com/page",
		StatusCode: 200,
		CrawledAt:  time.Now().UTC(),
	}
	if err := store.InsertPage(page); err != nil {
		t.Fatalf("InsertPage: %v", err)
	}

	// Update same URL with different status
	page2 := &types.PageData{
		URL:        "https://example.com/page",
		StatusCode: 301,
		CrawledAt:  time.Now().UTC(),
	}
	if err := store.InsertPage(page2); err != nil {
		t.Fatalf("InsertPage (upsert): %v", err)
	}

	// Should still be 1 page
	count, _ := store.PageCount()
	if count != 1 {
		t.Errorf("expected 1 page after upsert, got %d", count)
	}

	// Should have updated status
	pages, _ := store.GetAllPages()
	if pages[0].StatusCode != 301 {
		t.Errorf("expected status 301 after upsert, got %d", pages[0].StatusCode)
	}
}

func TestCrawlStoreQueueOperations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-queue.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	// Enqueue URLs
	ref := "https://example.com"
	added, err := store.Enqueue("https://example.com/a", 0, &ref)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !added {
		t.Error("first enqueue should succeed")
	}

	// Duplicate should fail
	added, err = store.Enqueue("https://example.com/a", 0, &ref)
	if err != nil {
		t.Fatalf("Enqueue (dup): %v", err)
	}
	if added {
		t.Error("duplicate enqueue should return false")
	}

	added, _ = store.Enqueue("https://example.com/b", 1, nil)
	if !added {
		t.Error("second URL enqueue should succeed")
	}

	// Pending count
	pending, err := store.PendingCount()
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if pending != 2 {
		t.Errorf("expected 2 pending, got %d", pending)
	}

	// HasSeen
	seen, err := store.HasSeen("https://example.com/a")
	if err != nil {
		t.Fatalf("HasSeen: %v", err)
	}
	if !seen {
		t.Error("expected HasSeen to return true for enqueued URL")
	}
	seen, _ = store.HasSeen("https://example.com/unknown")
	if seen {
		t.Error("expected HasSeen to return false for unknown URL")
	}

	// Dequeue
	entry, err := store.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil dequeue")
	}
	if entry.URL != "https://example.com/a" {
		t.Errorf("expected first URL, got %q", entry.URL)
	}
	if entry.Referrer == nil || *entry.Referrer != "https://example.com" {
		t.Error("expected referrer to be preserved")
	}

	// After dequeue, pending should be 1
	pending, _ = store.PendingCount()
	if pending != 1 {
		t.Errorf("expected 1 pending after dequeue, got %d", pending)
	}

	// Dequeue second
	entry, _ = store.Dequeue()
	if entry == nil || entry.URL != "https://example.com/b" {
		t.Error("expected second URL")
	}

	// Queue empty
	entry, _ = store.Dequeue()
	if entry != nil {
		t.Error("expected nil from empty queue")
	}
}

func TestCrawlStoreMarkVisitedPreservesData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-markvisited.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	// Enqueue with depth and referrer
	ref := "https://example.com"
	store.Enqueue("https://example.com/page", 3, &ref)

	// MarkVisited should preserve depth/referrer (ON CONFLICT DO UPDATE)
	if err := store.MarkVisited("https://example.com/page"); err != nil {
		t.Fatalf("MarkVisited: %v", err)
	}

	// Dequeue should return nil (already visited)
	entry, _ := store.Dequeue()
	if entry != nil {
		t.Error("expected nil dequeue after MarkVisited")
	}

	// PendingCount should be 0
	pending, _ := store.PendingCount()
	if pending != 0 {
		t.Errorf("expected 0 pending after MarkVisited, got %d", pending)
	}

	// MarkVisited on a new URL (no prior queue entry)
	if err := store.MarkVisited("https://example.com/new"); err != nil {
		t.Fatalf("MarkVisited (new): %v", err)
	}
	seen, _ := store.HasSeen("https://example.com/new")
	if !seen {
		t.Error("expected newly marked URL to be seen")
	}
}

func TestCrawlStoreEmptyResume(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-empty-resume.db")

	// First session: open, close without inserting
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	store.Close()

	// Second session: resume
	store2, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore (resume): %v", err)
	}
	defer store2.Close()

	count, err := store2.PageCount()
	if err != nil {
		t.Fatalf("PageCount: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 pages on empty resume, got %d", count)
	}

	visited, err := store2.GetVisitedURLs()
	if err != nil {
		t.Fatalf("GetVisitedURLs: %v", err)
	}
	if len(visited) != 0 {
		t.Errorf("expected 0 visited URLs, got %d", len(visited))
	}

	cfg, err := store2.GetConfig()
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg != nil {
		t.Error("expected nil config on empty resume")
	}
}

func TestCrawlStorePendingQueueRoundtrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-pending-queue.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	ref := "https://example.com"
	entries := []types.QueueEntry{
		{URL: "https://example.com/a", Depth: 0},
		{URL: "https://example.com/b", Depth: 2, Referrer: &ref, Requeues: 3},
		{URL: "https://example.com/c", Depth: 1},
	}

	if err := store.SavePendingQueue(entries); err != nil {
		t.Fatalf("SavePendingQueue: %v", err)
	}

	pending, err := store.PendingCount()
	if err != nil {
		t.Fatalf("PendingCount: %v", err)
	}
	if pending != 3 {
		t.Errorf("PendingCount = %d, want 3", pending)
	}

	loaded, err := store.GetPendingQueue()
	if err != nil {
		t.Fatalf("GetPendingQueue: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("GetPendingQueue returned %d entries, want 3", len(loaded))
	}

	// Should be ordered by depth
	if loaded[0].Depth != 0 || loaded[1].Depth != 1 || loaded[2].Depth != 2 {
		t.Errorf("depth order: %d,%d,%d, want 0,1,2", loaded[0].Depth, loaded[1].Depth, loaded[2].Depth)
	}
	// Referrer and Requeues preserved
	if loaded[2].Referrer == nil || *loaded[2].Referrer != ref {
		t.Error("referrer not preserved for depth-2 entry")
	}
	if loaded[2].Requeues != 3 {
		t.Errorf("requeues = %d, want 3", loaded[2].Requeues)
	}

	// ClearPendingQueue should remove all pending entries
	if err := store.ClearPendingQueue(); err != nil {
		t.Fatalf("ClearPendingQueue: %v", err)
	}
	pending, _ = store.PendingCount()
	if pending != 0 {
		t.Errorf("PendingCount after clear = %d, want 0", pending)
	}

	// Re-save for duplicate test
	if err := store.SavePendingQueue(entries); err != nil {
		t.Fatalf("SavePendingQueue (re-save): %v", err)
	}

	// Duplicate save should not create duplicates (INSERT OR IGNORE)
	if err := store.SavePendingQueue(entries); err != nil {
		t.Fatalf("SavePendingQueue (duplicate): %v", err)
	}
	pending, _ = store.PendingCount()
	if pending != 3 {
		t.Errorf("PendingCount after duplicate save = %d, want 3", pending)
	}
}

func TestCrawlStorePageHTML(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test-html.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	defer store.Close()

	// Save source HTML only
	err = store.SavePageHTML("https://example.com/page1", "<html><body>Hello</body></html>", "")
	if err != nil {
		t.Fatalf("SavePageHTML source: %v", err)
	}

	// Save both source and rendered
	err = store.SavePageHTML("https://example.com/page2", "<html>source</html>", "<html>rendered with JS</html>")
	if err != nil {
		t.Fatalf("SavePageHTML both: %v", err)
	}

	// Retrieve source-only page
	src, ren, err := store.GetPageHTML("https://example.com/page1")
	if err != nil {
		t.Fatalf("GetPageHTML: %v", err)
	}
	if src != "<html><body>Hello</body></html>" {
		t.Errorf("source = %q, want original HTML", src)
	}
	if ren != "" {
		t.Errorf("rendered = %q, want empty", ren)
	}

	// Retrieve both
	src2, ren2, err := store.GetPageHTML("https://example.com/page2")
	if err != nil {
		t.Fatalf("GetPageHTML: %v", err)
	}
	if src2 != "<html>source</html>" {
		t.Errorf("source = %q", src2)
	}
	if ren2 != "<html>rendered with JS</html>" {
		t.Errorf("rendered = %q", ren2)
	}

	// Non-existent page
	src3, ren3, err := store.GetPageHTML("https://example.com/missing")
	if err != nil {
		t.Fatalf("GetPageHTML missing: %v", err)
	}
	if src3 != "" || ren3 != "" {
		t.Errorf("expected empty for missing page, got src=%q ren=%q", src3, ren3)
	}
}
