package server

import (
	"strconv"
	"strings"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// fillDB caps the database a few pages above its current size, so a merge
// can create its table but fails part way through its inserts.
func fillDB(t *testing.T, s *UiStore) {
	t.Helper()
	var pages int64
	if err := sqlitex.Execute(s.conn, "PRAGMA page_count", &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { pages = stmt.ColumnInt64(0); return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if err := sqlitex.ExecuteTransient(s.conn, "PRAGMA max_page_count = "+strconv.FormatInt(pages+4, 10), nil); err != nil {
		t.Fatal(err)
	}
}

// tableNames lists the tables of the store whose name starts with prefix.
func tableNames(t *testing.T, s *UiStore, prefix string) []string {
	t.Helper()
	var out []string
	if err := sqlitex.Execute(s.conn,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND substr(name, 1, length(?)) = ? ORDER BY name`,
		&sqlitex.ExecOptions{
			Args: []interface{}{prefix, prefix},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				out = append(out, stmt.ColumnText(0))
				return nil
			},
		}); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasTable(t *testing.T, s *UiStore, name string) bool {
	t.Helper()
	for _, n := range tableNames(t, s, name) {
		if n == name {
			return true
		}
	}
	return false
}

// Each (log, crawl) pair has its own table; a re-run rebuilds only that one.
func TestMergePairTablesCoexist(t *testing.T) {
	s := newTestStore(t)
	if hasTable(t, s, legacyMergeTable) {
		t.Fatal("fresh store has the legacy shared merge table")
	}
	pairs := []struct {
		log, crawl string
		n          int
	}{{"log1", "crawl1", 3}, {"log1", "crawl2", 2}, {"log2", "crawl1", 4}}
	for _, p := range pairs {
		if err := writeMerge(s, p.log, p.crawl, mergePages(p.n, p.crawl)); err != nil {
			t.Fatal(err)
		}
	}
	if got := tableNames(t, s, "log_merge_"); len(got) != 4 { // 3 pairs + log_merge_tables
		t.Fatalf("tables %v, want 3 pair tables + log_merge_tables", got)
	}
	// Re-run log1×crawl1 with other rows.
	if err := writeMerge(s, "log1", "crawl1", mergePages(5, "z")); err != nil {
		t.Fatal(err)
	}
	want := map[[2]string]int64{{"log1", "crawl1"}: 5, {"log1", "crawl2"}: 2, {"log2", "crawl1"}: 4}
	for k, n := range want {
		if got := mergeCount(t, s, k[0], k[1]); got != n {
			t.Errorf("%v has %d rows, want %d", k, got, n)
		}
	}
	rows, _, err := s.QueryMergePages("log1", "crawl1", MergeQueryOpts{SortBy: "url", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 || !strings.Contains(rows[0].URL, "/z/") {
		t.Errorf("re-run kept old rows: %+v", rows)
	}
	// Filters still work without the log_id/crawl_id columns.
	if _, n, _ := s.QueryMergePages("log2", "crawl1", MergeQueryOpts{Segment: "crawl_waste", URLSearch: "/3"}); n != 1 {
		t.Errorf("filtered count %d, want 1", n)
	}
	// A pair never merged has no rows and no error.
	if got := mergeCount(t, s, "log9", "crawl9"); got != 0 {
		t.Errorf("unknown pair has %d rows", got)
	}
	// ClearMergePages drops only its pair.
	if err := s.ClearMergePages("log1", "crawl2"); err != nil {
		t.Fatal(err)
	}
	if hasTable(t, s, mergeTable("log1", "crawl2")) || mergeCount(t, s, "log1", "crawl2") != 0 {
		t.Error("cleared pair still cached")
	}
	if mergeCount(t, s, "log1", "crawl1") != 5 {
		t.Error("clear removed another pair")
	}
}

// SaveMergePagesBatch appends to (and replaces by URL in) the pair table.
func TestSaveMergePagesBatchPairTable(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveMergePagesBatch("log1", "crawl1", mergePages(3, "a")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMergePagesBatch("log1", "crawl1", mergePages(4, "a")); err != nil {
		t.Fatal(err)
	}
	if n := mergeCount(t, s, "log1", "crawl1"); n != 4 {
		t.Errorf("%d rows, want 4 (same URLs replaced)", n)
	}
}

// Deleting a log job drops its pair tables; deleting a crawl drops its own.
func TestDeleteDropsMergeTables(t *testing.T) {
	s := newTestStore(t)
	logJob, err := s.CreateLogJob("access.log", 10)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateLogJob("other.log", 10)
	if err != nil {
		t.Fatal(err)
	}
	crawl, err := s.CreateCrawlJob(map[string]interface{}{"url": "https://a.test/"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]string{{logJob.ID, crawl.ID}, {logJob.ID, "crawlB"}, {other.ID, crawl.ID}, {other.ID, "crawlB"}} {
		if err := writeMerge(s, p[0], p[1], mergePages(2, "a")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteLogJob(logJob.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{crawl.ID, "crawlB"} {
		if hasTable(t, s, mergeTable(logJob.ID, c)) {
			t.Errorf("table of deleted log × %s still exists", c)
		}
		if mergeCount(t, s, other.ID, c) != 2 {
			t.Errorf("other log × %s lost its merge", c)
		}
	}
	if _, err := s.DeleteCrawlJob(crawl.ID); err != nil {
		t.Fatal(err)
	}
	if hasTable(t, s, mergeTable(other.ID, crawl.ID)) {
		t.Error("table of deleted crawl still exists")
	}
	if mergeCount(t, s, other.ID, "crawlB") != 2 {
		t.Error("deleting a crawl dropped another crawl's merge")
	}
	var reg int
	sqlitex.Execute(s.conn, `SELECT COUNT(*) FROM log_merge_tables`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { reg = stmt.ColumnInt(0); return nil },
	})
	if reg != 1 {
		t.Errorf("registry has %d rows, want 1", reg)
	}
}

// legacyMergeDDL is the shared table of older versions (fresh-install layout).
const legacyMergeDDL = `
	CREATE TABLE log_merge_pages (
		log_id TEXT NOT NULL, crawl_id TEXT NOT NULL, url TEXT NOT NULL,
		segment TEXT NOT NULL, reason TEXT,
		log_hits INTEGER NOT NULL DEFAULT 0, log_bot_hits INTEGER NOT NULL DEFAULT 0,
		log_human_hits INTEGER NOT NULL DEFAULT 0, log_top_bot TEXT, log_status INTEGER,
		crawl_status INTEGER, crawl_indexable INTEGER, crawl_canonical TEXT,
		crawl_depth INTEGER, crawl_inlinks INTEGER, crawl_word_count INTEGER,
		has_noindex INTEGER, in_crawl INTEGER, in_logs INTEGER,
		log_bytes INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (log_id, crawl_id, url)
	) WITHOUT ROWID;
	CREATE INDEX idx_lmp_segment ON log_merge_pages(log_id, crawl_id, segment, log_bot_hits DESC);
	CREATE INDEX idx_lmp_bot_hits ON log_merge_pages(log_id, crawl_id, log_bot_hits DESC);
	INSERT INTO log_merge_pages (log_id, crawl_id, url, segment, log_hits, log_bot_hits, log_top_bot, in_logs, log_bytes) VALUES
		('log1', 'crawl1', 'https://a.test/old/1', 'crawl_waste', 9, 7, 'Googlebot', 1, 512),
		('log1', 'crawl1', 'https://a.test/old/2', 'crawl_waste', 3, 1, NULL, 1, 0),
		('log2', 'crawl2', 'https://a.test/old/3', 'orphan_crawled', 1, 1, NULL, 1, 0);`

// Merges written by older versions into the shared table stay readable until
// their pair is re-run or deleted; the shared table is dropped once empty.
func TestLegacyMergeRows(t *testing.T) {
	s := newTestStore(t)
	if err := sqlitex.ExecuteScript(s.conn, legacyMergeDDL, nil); err != nil {
		t.Fatal(err)
	}
	// Startup keeps a legacy table that still has rows.
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	rows, total, err := s.QueryMergePages("log1", "crawl1", MergeQueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(rows) != 2 || rows[0].URL != "https://a.test/old/1" || rows[0].LogTopBot != "Googlebot" || rows[0].LogBytes != 512 {
		t.Fatalf("legacy rows: total %d %+v", total, rows)
	}
	// Re-running log1×crawl1 moves it to its own table and drops its legacy rows.
	if err := writeMerge(s, "log1", "crawl1", mergePages(1, "new")); err != nil {
		t.Fatal(err)
	}
	if n := mergeCount(t, s, "log1", "crawl1"); n != 1 {
		t.Errorf("re-run pair has %d rows, want 1", n)
	}
	if n := mergeCount(t, s, "log2", "crawl2"); n != 1 {
		t.Errorf("other legacy pair has %d rows, want 1", n)
	}
	var legacyRows int
	sqlitex.Execute(s.conn, `SELECT COUNT(*) FROM log_merge_pages`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { legacyRows = stmt.ColumnInt(0); return nil },
	})
	if legacyRows != 1 {
		t.Errorf("legacy table has %d rows, want 1", legacyRows)
	}
	// Deleting the last legacy log drops the legacy table.
	if err := s.DeleteLogJob("log2"); err != nil {
		t.Fatal(err)
	}
	if hasTable(t, s, legacyMergeTable) {
		t.Error("empty legacy table not dropped")
	}
	if n := mergeCount(t, s, "log1", "crawl1"); n != 1 {
		t.Errorf("pair table lost rows: %d", n)
	}
}

// Startup drops an empty legacy shared table.
func TestMigrateDropsEmptyLegacyMergeTable(t *testing.T) {
	s := newTestStore(t)
	if err := sqlitex.ExecuteScript(s.conn, legacyMergeDDL+`DELETE FROM log_merge_pages;`, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(); err != nil {
		t.Fatal(err)
	}
	if hasTable(t, s, legacyMergeTable) {
		t.Error("empty legacy table survived startup")
	}
}

// Table names come from a hash: any ID is safe and pairs never collide.
func TestMergeTableNameSafety(t *testing.T) {
	ids := [][2]string{
		{"ab", "c"}, {"a", "bc"},
		{`x"; DROP TABLE crawl_jobs; --`, "c"},
		{"", ""}, {"ü/ñ 日本", "crawl id with spaces"},
		{"0b9f6c1e-2c7d-4a57-9a4e-3b1c2d4e5f60", "1d2c3b4a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"},
	}
	seen := map[string]bool{}
	for _, p := range ids {
		name := mergeTable(p[0], p[1])
		if !validMergeTable(name) {
			t.Errorf("mergeTable(%q, %q) = %q is not a valid name", p[0], p[1], name)
		}
		if seen[name] {
			t.Errorf("mergeTable(%q, %q) collides", p[0], p[1])
		}
		seen[name] = true
		if mergeTable(p[0], p[1]) != name {
			t.Error("mergeTable is not stable")
		}
	}
	for _, bad := range []string{"", "log_merge_", "crawl_jobs", "log_merge_tables",
		"log_merge_0123456789abcdef0123456789ABCDEF", "log_merge_0123456789abcdef0123456789abcdef; DROP TABLE x"} {
		if validMergeTable(bad) {
			t.Errorf("validMergeTable(%q) = true", bad)
		}
	}
	// A hostile ID round-trips and leaves other tables alone.
	s := newTestStore(t)
	evil := ids[2][0]
	if err := writeMerge(s, evil, "c", mergePages(2, "e")); err != nil {
		t.Fatal(err)
	}
	if n := mergeCount(t, s, evil, "c"); n != 2 {
		t.Errorf("hostile pair has %d rows", n)
	}
	if !hasTable(t, s, "crawl_jobs") {
		t.Fatal("crawl_jobs dropped")
	}
	if err := s.DeleteLogJob(evil); err != nil {
		t.Fatal(err)
	}
	if hasTable(t, s, mergeTable(evil, "c")) {
		t.Error("hostile pair table not dropped")
	}
}
