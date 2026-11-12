package storage

import (
	"bytes"
	"compress/zlib"
	"path/filepath"
	"strings"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// TestBlobScratchMatchesDecompressBlob reads rows of every encoding, big and
// small in turn, through one reused scratch and checks each against the
// per-row decompressBlob path: a shorter row after a longer one must not see
// stale bytes.
func TestBlobScratchMatchesDecompressBlob(t *testing.T) {
	conn, err := sqlite.OpenConn(filepath.Join(t.TempDir(), "b.db"), sqlite.OpenCreate|sqlite.OpenReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := sqlitex.ExecuteTransient(conn, `CREATE TABLE t (data)`, nil); err != nil {
		t.Fatal(err)
	}
	zlibOf := func(s string) []byte {
		var b bytes.Buffer
		w := zlib.NewWriter(&b)
		w.Write([]byte(s))
		w.Close()
		return b.Bytes()
	}
	zstdOf := func(s string) []byte {
		b, err := zstdCompressBlob([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	big := `{"url":"https://a/` + strings.Repeat("x", 200000) + `"}`
	want := []string{big, `{"url":"https://b/"}`, `{"url":"https://c/"}`, big[:5000] + `"}`, `{"url":"https://d/"}`, `{"url":"e"}`, `{"url":"f"}`}
	rows := []any{zstdOf(want[0]), zstdOf(want[1]), []byte(want[2]), zlibOf(want[3]), want[4], zstdOf(want[5]), zstdOf(want[6])}
	for _, r := range rows {
		if err := sqlitex.Execute(conn, `INSERT INTO t (data) VALUES (?)`, &sqlitex.ExecOptions{Args: []any{r}}); err != nil {
			t.Fatal(err)
		}
	}
	var sc blobScratch
	i := 0
	err = sqlitex.Execute(conn, `SELECT data FROM t ORDER BY rowid`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			got, err := sc.pageData(stmt)
			if err != nil {
				return err
			}
			if string(got) != want[i] {
				t.Errorf("row %d: got %d bytes %.40q, want %d bytes %.40q", i, len(got), got, len(want[i]), want[i])
			}
			i++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if i != len(want) {
		t.Fatalf("read %d rows, want %d", i, len(want))
	}
}
