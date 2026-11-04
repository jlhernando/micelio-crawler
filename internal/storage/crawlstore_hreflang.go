package storage

import (
	"encoding/json"
	"fmt"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/micelio/micelio/internal/types"
)

// Sitemap hreflang alternates are stored as zstd-compressed JSON in
// analysis_arrays (name sitemapHreflangArray), split in chunks so each one
// stays under the decoder's 10 MB cap.
const (
	sitemapHreflangArray = "sitemap_hreflang_v1"
	hreflangChunkBytes   = 4 << 20
)

// SaveSitemapHreflang replaces the stored sitemap hreflang alternates.
func (s *CrawlStore) SaveSitemapHreflang(h *types.SitemapHreflang) (err error) {
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Older crawl DBs may predate analysis_arrays.
	if err := sqlitex.ExecuteScript(s.conn, analysisSchema, nil); err != nil {
		return err
	}
	defer sqlitex.Save(s.conn)(&err)
	if err := sqlitex.Execute(s.conn, `DELETE FROM analysis_arrays WHERE name = ?`,
		&sqlitex.ExecOptions{Args: []any{sitemapHreflangArray}}); err != nil {
		return err
	}
	for seq, start := 0, 0; start < len(data); seq, start = seq+1, start+hreflangChunkBytes {
		blob, err := zstdCompressBlob(data[start:min(start+hreflangChunkBytes, len(data))])
		if err != nil {
			return err
		}
		if err := sqlitex.Execute(s.conn, `INSERT INTO analysis_arrays (name, seq, data) VALUES (?, ?, ?)`,
			&sqlitex.ExecOptions{Args: []any{sitemapHreflangArray, seq, blob}}); err != nil {
			return err
		}
	}
	return nil
}

// LoadSitemapHreflang returns the stored sitemap alternates, nil if none.
func (s *CrawlStore) LoadSitemapHreflang() (*types.SitemapHreflang, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var data []byte
	err := sqlitex.Execute(s.conn, `SELECT data FROM analysis_arrays WHERE name = ? ORDER BY seq`,
		&sqlitex.ExecOptions{
			Args: []any{sitemapHreflangArray},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				blob := make([]byte, stmt.ColumnLen(0))
				stmt.ColumnBytes(0, blob)
				raw, err := decompressBlob(blob)
				if err != nil {
					return fmt.Errorf("decompress sitemap hreflang: %w", err)
				}
				data = append(data, raw...)
				return nil
			},
		})
	if err != nil {
		// No analysis_arrays table: nothing stored.
		return nil, nil
	}
	if len(data) == 0 {
		return nil, nil
	}
	var h types.SitemapHreflang
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	if len(h.Set) != len(h.URLs) {
		return nil, fmt.Errorf("sitemap hreflang: %d urls, %d set ids", len(h.URLs), len(h.Set))
	}
	for _, id := range h.Set {
		if id < 0 || int(id) >= len(h.Sets) {
			return nil, fmt.Errorf("sitemap hreflang: bad set id %d", id)
		}
	}
	return &h, nil
}
