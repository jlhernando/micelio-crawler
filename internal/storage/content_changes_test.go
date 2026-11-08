package storage

import (
	"bytes"
	"compress/zlib"
	"path/filepath"
	"testing"

	"github.com/micelio/micelio/internal/types"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestContentChangeCountsFinalRowsAndLegacyFormats(t *testing.T) {
	db, err := NewCrawlStore(filepath.Join(t.TempDir(), "changes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	changed, unchanged := true, false
	if err := db.InsertPage(&types.PageData{URL: "https://example.test/zstd", ContentChanged: &changed}); err != nil {
		t.Fatal(err)
	}
	var legacy bytes.Buffer
	w := zlib.NewWriter(&legacy)
	if _, err := w.Write([]byte(`{"contentChanged":true,"internalLinks":["/a","/b"]}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		url  string
		data any
	}{
		{"https://example.test/zlib", legacy.Bytes()},
		{"https://example.test/text", `{"contentChanged":false}`},
		{"https://example.test/null", `{"contentChanged":null}`},
		{"https://example.test/missing", `{}`},
	} {
		if err := sqlitex.Execute(db.conn, `INSERT INTO pages (url,data,status_code,crawled_at) VALUES (?,?,200,'')`, &sqlitex.ExecOptions{Args: []any{row.url, row.data}}); err != nil {
			t.Fatal(err)
		}
	}
	assertCounts := func(wantChanged, wantUnchanged int) {
		t.Helper()
		c, u, err := db.ContentChangeCounts()
		if err != nil || c != wantChanged || u != wantUnchanged {
			t.Fatalf("counts=%d/%d err=%v; want %d/%d", c, u, err, wantChanged, wantUnchanged)
		}
	}
	assertCounts(2, 1)
	if err := db.InsertPage(&types.PageData{URL: "https://example.test/zstd", ContentChanged: &unchanged}); err != nil {
		t.Fatal(err)
	}
	assertCounts(1, 2)
	if err := db.InsertPage(&types.PageData{URL: "https://example.test/zstd"}); err != nil {
		t.Fatal(err)
	}
	assertCounts(1, 1)
}

func TestContentChangeCountsRejectsInvalidRows(t *testing.T) {
	for _, invalid := range []any{`{"contentChanged":`, `{"contentChanged":"true"}`, []byte{1, 2, 3}} {
		db, err := NewCrawlStore(filepath.Join(t.TempDir(), "invalid.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlitex.Execute(db.conn, `INSERT INTO pages (url,data,status_code,crawled_at) VALUES ('invalid',?,200,'')`, &sqlitex.ExecOptions{Args: []any{invalid}}); err != nil {
			db.Close()
			t.Fatal(err)
		}
		c, u, err := db.ContentChangeCounts()
		db.Close()
		if err == nil || c != 0 || u != 0 {
			t.Fatalf("invalid row returned counts=%d/%d err=%v", c, u, err)
		}
	}
}
