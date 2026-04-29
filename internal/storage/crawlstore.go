package storage

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/micelio/micelio/internal/types"
)

// Package-level zstd encoder/decoder for SQLite blob compression.
// Reused across calls to avoid allocation overhead.
var (
	zstdEnc     *zstd.Encoder
	zstdDec     *zstd.Decoder
	zstdInitErr error
	zstdOnce    sync.Once
)

func initZstdCodecs() {
	zstdOnce.Do(func() {
		zstdEnc, zstdInitErr = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if zstdInitErr != nil {
			return
		}
		zstdDec, zstdInitErr = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(10<<20))
	})
}

// CrawlStore provides SQLite-backed persistence for crawl pages and queue state.
// This enables --resume for interrupted crawls.
// CrawlStore is safe for concurrent use from multiple goroutines.
type CrawlStore struct {
	mu   sync.Mutex
	conn *sqlite.Conn
}

// NewCrawlStore opens or creates a crawl database at the given path.
func NewCrawlStore(dbPath string) (*CrawlStore, error) {
	// Open without OpenWAL so page_size PRAGMA takes effect on new databases.
	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenCreate|sqlite.OpenReadWrite)
	if err != nil {
		return nil, fmt.Errorf("open crawl db: %w", err)
	}

	// Performance PRAGMAs — page_size must be set before WAL and before any tables.
	sqlitex.Execute(conn, "PRAGMA page_size = 8192;", nil)
	sqlitex.Execute(conn, "PRAGMA journal_mode = WAL;", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { return nil },
	})
	sqlitex.Execute(conn, "PRAGMA synchronous = NORMAL;", nil)
	sqlitex.Execute(conn, "PRAGMA cache_size = -16000;", nil)
	sqlitex.Execute(conn, "PRAGMA temp_store = MEMORY;", nil)
	sqlitex.Execute(conn, "PRAGMA mmap_size = 268435456;", nil)

	if err := initCrawlSchema(conn); err != nil {
		conn.Close()
		return nil, err
	}
	migrateCrawlSchema(conn)

	return &CrawlStore{conn: conn}, nil
}

// Close closes the database connection.
func (s *CrawlStore) Close() error {
	return s.conn.Close()
}

func initCrawlSchema(conn *sqlite.Conn) error {
	return sqlitex.ExecuteScript(conn, `
		CREATE TABLE IF NOT EXISTS pages (
			url TEXT PRIMARY KEY,
			data BLOB NOT NULL,
			status_code INTEGER NOT NULL DEFAULT 0,
			crawled_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS queue (
			url TEXT PRIMARY KEY,
			depth INTEGER NOT NULL DEFAULT 0,
			referrer TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			requeues INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_queue_status ON queue(status);
		CREATE INDEX IF NOT EXISTS idx_queue_pending ON queue(depth) WHERE status = 'pending';
		CREATE TABLE IF NOT EXISTS crawl_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS cache_headers (
			url TEXT PRIMARY KEY,
			etag TEXT NOT NULL DEFAULT '',
			last_modified TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS page_html (
			url TEXT PRIMARY KEY,
			source_html BLOB,
			rendered_html BLOB
		);
		CREATE TABLE IF NOT EXISTS content_hashes (
			url TEXT PRIMARY KEY,
			hash TEXT NOT NULL
		);
	`, nil)
}

// migrateCrawlSchema applies additive schema migrations for existing databases.
func migrateCrawlSchema(conn *sqlite.Conn) {
	// v1 → v2: add requeues column to queue table
	sqlitex.Execute(conn, `ALTER TABLE queue ADD COLUMN requeues INTEGER NOT NULL DEFAULT 0`, nil)
	// v2 → v3: page_index for instant merge (10 fields vs full JSON deserialization)
	sqlitex.ExecuteScript(conn, `
		CREATE TABLE IF NOT EXISTS page_index (
			url TEXT PRIMARY KEY,
			status_code INTEGER NOT NULL DEFAULT 0,
			indexable INTEGER NOT NULL DEFAULT 0,
			canonical TEXT NOT NULL DEFAULT '',
			depth INTEGER NOT NULL DEFAULT 0,
			inlinks INTEGER NOT NULL DEFAULT 0,
			word_count INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL DEFAULT '',
			has_noindex INTEGER NOT NULL DEFAULT 0,
			is_redirect INTEGER NOT NULL DEFAULT 0
		);
	`, nil)
}

// saveMeta stores a key-value pair in crawl metadata (caller must hold s.mu).
func (s *CrawlStore) saveMeta(key, value string) error {
	return sqlitex.Execute(s.conn,
		`INSERT OR REPLACE INTO crawl_meta (key, value) VALUES (?, ?)`,
		&sqlitex.ExecOptions{Args: []any{key, value}},
	)
}

// getMeta retrieves a metadata value (caller must hold s.mu). Returns ("", nil) if not found.
func (s *CrawlStore) getMeta(key string) (string, error) {
	var value string
	var found bool
	err := sqlitex.Execute(s.conn,
		`SELECT value FROM crawl_meta WHERE key = ?`,
		&sqlitex.ExecOptions{
			Args: []any{key},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				value = stmt.ColumnText(0)
				found = true
				return nil
			},
		},
	)
	if err != nil {
		return "", err
	}
	if !found {
		return "", nil
	}
	return value, nil
}

// SaveMeta stores a key-value pair in crawl metadata.
func (s *CrawlStore) SaveMeta(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveMeta(key, value)
}

// GetMeta retrieves a metadata value. Returns ("", nil) if not found.
func (s *CrawlStore) GetMeta(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getMeta(key)
}

// SaveConfig serializes and stores the crawl config.
func (s *CrawlStore) SaveConfig(config types.CrawlConfig) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.saveMeta("config", string(data)); err != nil {
		return err
	}
	return s.saveMeta("startedAt", time.Now().UTC().Format(time.RFC3339))
}

// GetConfig retrieves the stored crawl config. Returns nil if not found.
func (s *CrawlStore) GetConfig() (*types.CrawlConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := s.getMeta("config")
	if err != nil || raw == "" {
		return nil, err
	}
	var cfg types.CrawlConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// SetStatus sets the crawl status (e.g., "running", "complete").
func (s *CrawlStore) SetStatus(status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveMeta("status", status)
}

// GetStatus returns the crawl status.
func (s *CrawlStore) GetStatus() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getMeta("status")
}

// InsertPage stores a crawled page with zstd-compressed JSON data.
// Also stores ETag/Last-Modified in cache_headers for conditional re-crawl.
func (s *CrawlStore) InsertPage(page *types.PageData) error {
	// Compression outside lock to minimize contention
	data, err := json.Marshal(page)
	if err != nil {
		return err
	}
	compressed, err := zstdCompressBlob(data)
	if err != nil {
		return fmt.Errorf("compress page data: %w", err)
	}
	crawledAt := page.CrawledAt.Format(time.RFC3339)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := sqlitex.Execute(s.conn,
		`INSERT OR REPLACE INTO pages (url, data, status_code, crawled_at) VALUES (?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{page.URL, compressed, page.StatusCode, crawledAt}},
	); err != nil {
		return err
	}
	// Store cache headers for conditional requests
	if page.ETag != "" || page.LastModified != "" {
		if err := sqlitex.Execute(s.conn,
			`INSERT OR REPLACE INTO cache_headers (url, etag, last_modified) VALUES (?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{page.URL, page.ETag, page.LastModified}},
		); err != nil {
			log.Printf("[crawlstore] warning: failed to save cache headers for %s: %v", page.URL, err)
		}
	}
	// Store content hash for change detection on re-crawl
	if page.ContentHash != "" {
		if err := sqlitex.Execute(s.conn,
			`INSERT OR REPLACE INTO content_hashes (url, hash) VALUES (?, ?)`,
			&sqlitex.ExecOptions{Args: []any{page.URL, page.ContentHash}},
		); err != nil {
			log.Printf("[crawlstore] warning: failed to save content hash for %s: %v", page.URL, err)
		}
	}
	return nil
}

// GetCacheHeaders returns all cached ETag/Last-Modified entries.
func (s *CrawlStore) GetCacheHeaders() (map[string]types.CacheEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]types.CacheEntry)
	err := sqlitex.Execute(s.conn,
		`SELECT url, etag, last_modified FROM cache_headers WHERE etag != '' OR last_modified != ''`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				result[stmt.ColumnText(0)] = types.CacheEntry{
					ETag:         stmt.ColumnText(1),
					LastModified: stmt.ColumnText(2),
				}
				return nil
			},
		},
	)
	return result, err
}

// GetContentHashes returns all stored content hashes (url -> MD5 hash).
func (s *CrawlStore) GetContentHashes() (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]string)
	err := sqlitex.Execute(s.conn,
		`SELECT url, hash FROM content_hashes`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				result[stmt.ColumnText(0)] = stmt.ColumnText(1)
				return nil
			},
		},
	)
	return result, err
}

// SavePageHTML stores zstd-compressed source and/or rendered HTML for a page.
func (s *CrawlStore) SavePageHTML(url string, sourceHTML, renderedHTML string) error {
	// Compression outside lock to minimize contention
	var compSource, compRendered []byte
	var err error
	if sourceHTML != "" {
		compSource, err = zstdCompressBlob([]byte(sourceHTML))
		if err != nil {
			return fmt.Errorf("compress source html: %w", err)
		}
	}
	if renderedHTML != "" {
		compRendered, err = zstdCompressBlob([]byte(renderedHTML))
		if err != nil {
			return fmt.Errorf("compress rendered html: %w", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return sqlitex.Execute(s.conn,
		`INSERT OR REPLACE INTO page_html (url, source_html, rendered_html) VALUES (?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{url, compSource, compRendered}},
	)
}

// GetPageHTML retrieves the stored source and rendered HTML for a page.
func (s *CrawlStore) GetPageHTML(url string) (sourceHTML, renderedHTML string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = sqlitex.Execute(s.conn,
		`SELECT source_html, rendered_html FROM page_html WHERE url = ?`,
		&sqlitex.ExecOptions{
			Args: []any{url},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				if stmt.ColumnType(0) == sqlite.TypeBlob && stmt.ColumnLen(0) > 0 {
					blob := make([]byte, stmt.ColumnLen(0))
					stmt.ColumnBytes(0, blob)
					data, err := decompressBlob(blob)
					if err != nil {
						return err
					}
					sourceHTML = string(data)
				}
				if stmt.ColumnType(1) == sqlite.TypeBlob && stmt.ColumnLen(1) > 0 {
					blob := make([]byte, stmt.ColumnLen(1))
					stmt.ColumnBytes(1, blob)
					data, err := decompressBlob(blob)
					if err != nil {
						return err
					}
					renderedHTML = string(data)
				}
				return nil
			},
		},
	)
	return
}

// GetAllPages returns all stored pages, decompressing data.
// Delegates to GetAllPagesWithProgress with no context cancellation or progress reporting.
func (s *CrawlStore) GetAllPages() ([]*types.PageData, error) {
	return s.GetAllPagesWithProgress(context.Background(), nil)
}

// GetAllPagesWithProgress is like GetAllPages but supports context cancellation
// and progress reporting. The callback receives the count of pages loaded so far,
// called every 500 pages. Pass a cancellable context to abort early (e.g. on LRU eviction).
func (s *CrawlStore) GetAllPagesWithProgress(ctx context.Context, onProgress func(loaded int)) ([]*types.PageData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pages []*types.PageData
	loaded := 0
	err := sqlitex.Execute(s.conn,
		`SELECT data FROM pages ORDER BY rowid`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				var jsonData []byte
				colType := stmt.ColumnType(0)
				if colType == sqlite.TypeBlob {
					blob := make([]byte, stmt.ColumnLen(0))
					stmt.ColumnBytes(0, blob)
					var err error
					jsonData, err = decompressBlob(blob)
					if err != nil {
						return fmt.Errorf("decompress page data: %w", err)
					}
				} else {
					jsonData = []byte(stmt.ColumnText(0))
				}
				var page types.PageData
				if err := json.Unmarshal(jsonData, &page); err != nil {
					return err
				}
				page.BodyText = ""
				if page.PageWeight != nil {
					page.PageWeight.Resources = nil
				}
				// Strip heavy fields to reduce memory for large crawls.
				// Pre-compute link counts before stripping arrays.
				if page.InternalLinkCount == 0 && len(page.InternalLinks) > 0 {
					page.InternalLinkCount = len(page.InternalLinks)
				}
				page.InternalLinks = nil
				if page.ExternalLinkCount == 0 && len(page.ExternalLinks) > 0 {
					page.ExternalLinkCount = len(page.ExternalLinks)
				}
				page.ExternalLinks = nil
				page.SnippetResults = nil
				page.JSErrors = nil
				page.SchemaValidation = nil
				page.RenderDiffs = nil
				page.CustomExtractions = nil
				pages = append(pages, &page)
				loaded++
				if onProgress != nil && loaded%500 == 0 {
					onProgress(loaded)
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return nil
			},
		},
	)
	return pages, err
}

// StreamPages iterates over all stored pages one at a time, calling fn for each.
// Unlike GetAllPages, this does not accumulate pages in memory — each page is
// deserialized, passed to fn, and then eligible for GC. This is critical for
// large crawls (700K+ pages) where loading all pages would require 15-20GB of RAM.
func (s *CrawlStore) StreamPages(fn func(page *types.PageData, index int) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := 0
	return sqlitex.Execute(s.conn,
		`SELECT data FROM pages ORDER BY rowid`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				var jsonData []byte
				colType := stmt.ColumnType(0)
				if colType == sqlite.TypeBlob {
					blob := make([]byte, stmt.ColumnLen(0))
					stmt.ColumnBytes(0, blob)
					var err error
					jsonData, err = decompressBlob(blob)
					if err != nil {
						return fmt.Errorf("decompress page data: %w", err)
					}
				} else {
					jsonData = []byte(stmt.ColumnText(0))
				}
				var page types.PageData
				if err := json.Unmarshal(jsonData, &page); err != nil {
					return err
				}
				page.BodyText = ""
				if page.PageWeight != nil {
					page.PageWeight.Resources = nil
				}
				err := fn(&page, idx)
				idx++
				return err
			},
		},
	)
}

// GetPagesRange returns a slice of pages between offset and offset+limit from the SQLite DB.
// This is used to serve paginated results directly from disk while background loading is in progress.
func (s *CrawlStore) GetPagesRange(offset, limit int, search string) ([]*types.PageData, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Get total count
	var total int
	countSQL := `SELECT COUNT(*) FROM pages`
	if search != "" {
		countSQL = `SELECT COUNT(*) FROM pages WHERE url LIKE '%' || ? || '%'`
	}
	countOpts := &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			total = stmt.ColumnInt(0)
			return nil
		},
	}
	if search != "" {
		countOpts.Args = []interface{}{search}
	}
	if err := sqlitex.Execute(s.conn, countSQL, countOpts); err != nil {
		return nil, 0, err
	}
	// Get page range
	querySQL := `SELECT data FROM pages ORDER BY rowid LIMIT ? OFFSET ?`
	queryArgs := []interface{}{limit, offset}
	if search != "" {
		querySQL = `SELECT data FROM pages WHERE url LIKE '%' || ? || '%' ORDER BY rowid LIMIT ? OFFSET ?`
		queryArgs = []interface{}{search, limit, offset}
	}
	var pages []*types.PageData
	err := sqlitex.Execute(s.conn, querySQL, &sqlitex.ExecOptions{
		Args: queryArgs,
		ResultFunc: func(stmt *sqlite.Stmt) error {
			var jsonData []byte
			colType := stmt.ColumnType(0)
			if colType == sqlite.TypeBlob {
				blob := make([]byte, stmt.ColumnLen(0))
				stmt.ColumnBytes(0, blob)
				var err error
				jsonData, err = decompressBlob(blob)
				if err != nil {
					return fmt.Errorf("decompress page data: %w", err)
				}
			} else {
				jsonData = []byte(stmt.ColumnText(0))
			}
			var page types.PageData
			if err := json.Unmarshal(jsonData, &page); err != nil {
				return err
			}
			page.BodyText = ""
			if page.PageWeight != nil {
				page.PageWeight.Resources = nil
			}
			pages = append(pages, &page)
			return nil
		},
	})
	return pages, total, err
}

// PageCount returns the number of stored pages.
func (s *CrawlStore) PageCount() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	err := sqlitex.Execute(s.conn,
		`SELECT COUNT(*) FROM pages`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt(0)
				return nil
			},
		},
	)
	return count, err
}

// PageIndexEntry holds lightweight crawl page data for fast merge lookups.
type PageIndexEntry struct {
	URL        string
	Canonical  string
	Title      string
	StatusCode int
	Depth      int
	Inlinks    int
	WordCount  int
	Indexable  bool
	HasNoindex bool
	IsRedirect bool
}

// BuildPageIndex populates the page_index table from page data.
// Called during post-crawl analysis when pages are in memory.
func (s *CrawlStore) BuildPageIndex(pages []*types.PageData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Clear old index
	if err := sqlitex.Execute(s.conn, `DELETE FROM page_index`, nil); err != nil {
		return err
	}
	endFn, err := sqlitex.ImmediateTransaction(s.conn)
	if err != nil {
		return err
	}
	defer endFn(&err)
	for _, p := range pages {
		if p == nil {
			continue
		}
		canonical := ""
		if p.Canonical != nil {
			canonical = *p.Canonical
		}
		title := ""
		if p.Title != nil {
			title = p.Title.Text
		}
		hasNoindex := 0
		if p.MetaRobots != nil {
			mr := *p.MetaRobots
			for i := 0; i <= len(mr)-7; i++ {
				if (mr[i] == 'n' || mr[i] == 'N') && (mr[i+1] == 'o' || mr[i+1] == 'O') &&
					(mr[i+6] == 'x' || mr[i+6] == 'X') {
					hasNoindex = 1
					break
				}
			}
		}
		isRedirect := 0
		if p.StatusCode >= 300 && p.StatusCode < 400 {
			isRedirect = 1
		}
		if err := sqlitex.Execute(s.conn,
			`INSERT OR REPLACE INTO page_index (url, status_code, indexable, canonical, depth, inlinks, word_count, title, has_noindex, is_redirect) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{
				p.URL, p.StatusCode, p.Indexability.Indexable, canonical, p.Depth, p.Inlinks, p.WordCount, title, hasNoindex, isRedirect,
			}}); err != nil {
			return err
		}
	}
	return nil
}

// GetPageIndex loads all entries from page_index into a slice.
// Returns nil if the table is empty or doesn't exist.
func (s *CrawlStore) GetPageIndex() ([]PageIndexEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var entries []PageIndexEntry
	err := sqlitex.Execute(s.conn,
		`SELECT url, status_code, indexable, canonical, depth, inlinks, word_count, title, has_noindex, is_redirect FROM page_index`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				entries = append(entries, PageIndexEntry{
					URL:        stmt.ColumnText(0),
					StatusCode: stmt.ColumnInt(1),
					Indexable:  stmt.ColumnInt(2) != 0,
					Canonical:  stmt.ColumnText(3),
					Depth:      stmt.ColumnInt(4),
					Inlinks:    stmt.ColumnInt(5),
					WordCount:  stmt.ColumnInt(6),
					Title:      stmt.ColumnText(7),
					HasNoindex: stmt.ColumnInt(8) != 0,
					IsRedirect: stmt.ColumnInt(9) != 0,
				})
				return nil
			},
		})
	return entries, err
}

// HasPageIndex returns true if the page_index table has any entries.
func (s *CrawlStore) HasPageIndex() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	sqlitex.Execute(s.conn,
		`SELECT COUNT(*) FROM page_index`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt(0)
				return nil
			},
		})
	return count > 0
}

// Enqueue adds a URL to the queue with status 'pending'.
// Returns false if the URL already exists in the queue.
func (s *CrawlStore) Enqueue(url string, depth int, referrer *string) (bool, error) {
	var ref any
	if referrer != nil {
		ref = *referrer
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := sqlitex.Execute(s.conn,
		`INSERT OR IGNORE INTO queue (url, depth, referrer, status) VALUES (?, ?, ?, 'pending')`,
		&sqlitex.ExecOptions{Args: []any{url, depth, ref}},
	)
	if err != nil {
		return false, err
	}
	return s.conn.Changes() > 0, nil
}

// Dequeue returns the next pending URL and marks it as 'visited'.
// Returns nil if no pending URLs exist.
func (s *CrawlStore) Dequeue() (*types.QueueEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var entry *types.QueueEntry

	endFn, err := sqlitex.ImmediateTransaction(s.conn)
	if err != nil {
		return nil, err
	}
	defer endFn(&err)

	err = sqlitex.Execute(s.conn,
		`SELECT url, depth, referrer FROM queue WHERE status = 'pending' LIMIT 1`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				e := types.QueueEntry{
					URL:   stmt.ColumnText(0),
					Depth: stmt.ColumnInt(1),
				}
				if stmt.ColumnType(2) != sqlite.TypeNull {
					ref := stmt.ColumnText(2)
					e.Referrer = &ref
				}
				entry = &e
				return nil
			},
		},
	)
	if err != nil {
		return nil, err
	}

	if entry != nil {
		err = sqlitex.Execute(s.conn,
			`UPDATE queue SET status = 'visited' WHERE url = ?`,
			&sqlitex.ExecOptions{Args: []any{entry.URL}},
		)
		if err != nil {
			return nil, err
		}
	}

	return entry, nil
}

// HasSeen checks if a URL exists in the queue (any status).
func (s *CrawlStore) HasSeen(url string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found bool
	err := sqlitex.Execute(s.conn,
		`SELECT 1 FROM queue WHERE url = ?`,
		&sqlitex.ExecOptions{
			Args: []any{url},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				found = true
				return nil
			},
		},
	)
	return found, err
}

// MarkVisited marks a URL as visited without adding page data.
// Uses ON CONFLICT to preserve existing depth/referrer data.
func (s *CrawlStore) MarkVisited(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sqlitex.Execute(s.conn,
		`INSERT INTO queue (url, depth, status) VALUES (?, 0, 'visited')
		 ON CONFLICT(url) DO UPDATE SET status = 'visited'`,
		&sqlitex.ExecOptions{Args: []any{url}},
	)
}

// PendingCount returns the number of pending URLs in the queue.
func (s *CrawlStore) PendingCount() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	err := sqlitex.Execute(s.conn,
		`SELECT COUNT(*) FROM queue WHERE status = 'pending'`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				count = stmt.ColumnInt(0)
				return nil
			},
		},
	)
	return count, err
}

// SavePendingQueue bulk-inserts queue entries with status 'pending'.
// Used to persist the in-memory frontier on shutdown for later resume.
func (s *CrawlStore) SavePendingQueue(entries []types.QueueEntry) error {
	if len(entries) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	endFn, err := sqlitex.ImmediateTransaction(s.conn)
	if err != nil {
		return err
	}
	defer endFn(&err)

	for _, e := range entries {
		var ref any
		if e.Referrer != nil {
			ref = *e.Referrer
		}
		if err = sqlitex.Execute(s.conn,
			`INSERT OR IGNORE INTO queue (url, depth, referrer, requeues, status) VALUES (?, ?, ?, ?, 'pending')`,
			&sqlitex.ExecOptions{Args: []any{e.URL, e.Depth, ref, e.Requeues}},
		); err != nil {
			return err
		}
	}
	return nil
}

// GetPendingQueue returns all pending queue entries ordered by depth (shallowest first).
func (s *CrawlStore) GetPendingQueue() ([]types.QueueEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var entries []types.QueueEntry
	err := sqlitex.Execute(s.conn,
		`SELECT url, depth, referrer, requeues FROM queue WHERE status = 'pending' ORDER BY depth`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				e := types.QueueEntry{
					URL:      stmt.ColumnText(0),
					Depth:    stmt.ColumnInt(1),
					Requeues: stmt.ColumnInt(3),
				}
				if stmt.ColumnType(2) != sqlite.TypeNull {
					ref := stmt.ColumnText(2)
					e.Referrer = &ref
				}
				entries = append(entries, e)
				return nil
			},
		},
	)
	return entries, err
}

// ClearPendingQueue deletes all pending entries from the queue table.
// Called after loading pending entries on resume to avoid stale rows.
func (s *CrawlStore) ClearPendingQueue() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sqlitex.Execute(s.conn, `DELETE FROM queue WHERE status = 'pending'`, nil)
}

// GetVisitedURLs returns all URLs that have been crawled (from pages table).
// This is used for resume support — marking already-crawled URLs in the queue.
func (s *CrawlStore) GetVisitedURLs() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var urls []string
	err := sqlitex.Execute(s.conn,
		`SELECT url FROM pages`,
		&sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				urls = append(urls, stmt.ColumnText(0))
				return nil
			},
		},
	)
	return urls, err
}

// zstdCompressBlob compresses data using zstd (default level).
// Uses a reusable encoder for efficiency.
func zstdCompressBlob(data []byte) ([]byte, error) {
	initZstdCodecs()
	if zstdInitErr != nil {
		return nil, zstdInitErr
	}
	return zstdEnc.EncodeAll(data, make([]byte, 0, len(data)/2)), nil
}

// decompressBlob detects the compression format and decompresses accordingly.
// Supports zstd (magic: 0x28B52FFD), zlib (magic: 0x78), and uncompressed data.
func decompressBlob(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	// Zstd frame magic: 0xFD2FB528 (little-endian) → bytes: 0x28 0xB5 0x2F 0xFD
	if len(data) >= 4 && data[0] == 0x28 && data[1] == 0xB5 && data[2] == 0x2F && data[3] == 0xFD {
		initZstdCodecs()
		if zstdInitErr != nil {
			return nil, zstdInitErr
		}
		return zstdDec.DecodeAll(data, make([]byte, 0, len(data)*4))
	}
	// Zlib magic byte: 0x78
	if data[0] == 0x78 {
		return zlibDecompress(data)
	}
	// Uncompressed (plain JSON)
	return data, nil
}

// zlibDecompress decompresses zlib-compressed data (legacy format, capped at 10MB).
func zlibDecompress(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 10<<20)) // 10MB max per page
}
