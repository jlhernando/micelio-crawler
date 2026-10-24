package storage

import (
	"encoding/json"
	"fmt"
	"time"

	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/micelio/micelio/internal/types"
)

// EncodedPage is a page ready for InsertEncodedPages: JSON-encoded and
// zstd-compressed by the caller (typically a crawl worker), so the shared
// writer only runs SQL.
type EncodedPage struct {
	URL          string
	CrawledAt    string
	ETag         string
	LastModified string
	ContentHash  string
	Data         []byte // zstd(JSON(page))
	SourceHTML   []byte // zstd, optional
	RenderedHTML []byte // zstd, optional
	StatusCode   int
	HasHTML      bool
}

// EncodePage compresses a page's JSON for storage. pageJSON must be
// json.Marshal(page); pass nil to have it marshalled here.
func EncodePage(page *types.PageData, pageJSON []byte) (EncodedPage, error) {
	if pageJSON == nil {
		var err error
		if pageJSON, err = json.Marshal(page); err != nil {
			return EncodedPage{}, err
		}
	}
	compressed, err := zstdCompressBlob(pageJSON)
	if err != nil {
		return EncodedPage{}, fmt.Errorf("compress page data: %w", err)
	}
	return EncodedPage{
		URL:          page.URL,
		CrawledAt:    page.CrawledAt.Format(time.RFC3339),
		ETag:         page.ETag,
		LastModified: page.LastModified,
		ContentHash:  page.ContentHash,
		Data:         compressed,
		StatusCode:   page.StatusCode,
	}, nil
}

// AttachHTML compresses source and/or rendered HTML into the page record so
// it is written in the same transaction (see SavePageHTML).
func (e *EncodedPage) AttachHTML(sourceHTML, renderedHTML string) error {
	var err error
	if sourceHTML != "" {
		if e.SourceHTML, err = zstdCompressBlob([]byte(sourceHTML)); err != nil {
			return fmt.Errorf("compress source html: %w", err)
		}
	}
	if renderedHTML != "" {
		if e.RenderedHTML, err = zstdCompressBlob([]byte(renderedHTML)); err != nil {
			return fmt.Errorf("compress rendered html: %w", err)
		}
	}
	e.HasHTML = e.SourceHTML != nil || e.RenderedHTML != nil
	return nil
}

// InsertEncodedPages writes a batch of pages (plus cache headers, content
// hashes and optional HTML) in one transaction. One commit per batch instead
// of 1-3 per page is what keeps the crawl DB off the per-page critical path.
func (s *CrawlStore) InsertEncodedPages(batch []EncodedPage) (err error) {
	if len(batch) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer sqlitex.Save(s.conn)(&err)
	for i := range batch {
		if err = s.insertEncoded(&batch[i]); err != nil {
			return err
		}
	}
	return nil
}

// insertEncoded writes one page (caller holds s.mu).
func (s *CrawlStore) insertEncoded(e *EncodedPage) error {
	if err := sqlitex.Execute(s.conn,
		`INSERT OR REPLACE INTO pages (url, data, status_code, crawled_at) VALUES (?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{e.URL, e.Data, e.StatusCode, e.CrawledAt}},
	); err != nil {
		return err
	}
	if e.ETag != "" || e.LastModified != "" {
		if err := sqlitex.Execute(s.conn,
			`INSERT OR REPLACE INTO cache_headers (url, etag, last_modified) VALUES (?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{e.URL, e.ETag, e.LastModified}},
		); err != nil {
			return fmt.Errorf("save cache headers for %s: %w", e.URL, err)
		}
	}
	if e.ContentHash != "" {
		if err := sqlitex.Execute(s.conn,
			`INSERT OR REPLACE INTO content_hashes (url, hash) VALUES (?, ?)`,
			&sqlitex.ExecOptions{Args: []any{e.URL, e.ContentHash}},
		); err != nil {
			return fmt.Errorf("save content hash for %s: %w", e.URL, err)
		}
	}
	if e.HasHTML {
		if err := sqlitex.Execute(s.conn,
			`INSERT OR REPLACE INTO page_html (url, source_html, rendered_html) VALUES (?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{e.URL, e.SourceHTML, e.RenderedHTML}},
		); err != nil {
			return err
		}
	}
	return nil
}
