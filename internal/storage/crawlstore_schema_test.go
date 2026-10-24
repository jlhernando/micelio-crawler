package storage

import (
	"path/filepath"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// A DB created before schema versioning (user_version 0, no
// analysis_arrays) is migrated on open; later opens skip the schema work.
func TestCrawlStoreSchemaVersioning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	conn, err := sqlite.OpenConn(path, sqlite.OpenCreate|sqlite.OpenReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	if err := initCrawlSchema(conn); err != nil {
		t.Fatal(err)
	}
	conn.Close()

	cs, err := NewCrawlStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := crawlSchemaVersionOf(cs.conn); v != crawlSchemaVersion {
		t.Errorf("user_version = %d, want %d", v, crawlSchemaVersion)
	}
	if err := cs.SavePageScores(1, []float64{1}, nil, nil, false); err != nil {
		t.Errorf("analysis table missing after migration: %v", err)
	}
	cs.Close()

	cs, err = NewCrawlStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	n := 0
	sqlitex.Execute(cs.conn, "SELECT count(*) FROM pragma_table_info('queue') WHERE name = 'requeues'", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { n = stmt.ColumnInt(0); return nil },
	})
	if n != 1 {
		t.Error("queue.requeues column missing")
	}
}
