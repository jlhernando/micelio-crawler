package storage

import (
	"encoding/json"
	"fmt"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// ContentChangeCounts counts the final change flags without retaining page
// objects, URLs, or link arrays. It supports compressed and legacy text rows.
func (s *CrawlStore) ContentChangeCounts() (changed, unchanged int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	err = sqlitex.Execute(s.conn, `SELECT data FROM pages`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			var data []byte
			if stmt.ColumnType(0) == sqlite.TypeBlob {
				blob := make([]byte, stmt.ColumnLen(0))
				stmt.ColumnBytes(0, blob)
				var decodeErr error
				data, decodeErr = decompressBlob(blob)
				if decodeErr != nil {
					return fmt.Errorf("decompress content change row: %w", decodeErr)
				}
			} else {
				data = []byte(stmt.ColumnText(0))
			}
			var row struct {
				ContentChanged *bool `json:"contentChanged"`
			}
			if err := json.Unmarshal(data, &row); err != nil {
				return err
			}
			if row.ContentChanged != nil {
				if *row.ContentChanged {
					changed++
				} else {
					unchanged++
				}
			}
			return nil
		},
	})
	if err != nil {
		return 0, 0, err
	}
	return changed, unchanged, nil
}
