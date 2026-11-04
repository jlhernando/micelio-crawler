package server

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/logs"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func mergePages(n int, prefix string) []logs.MergedPage {
	out := make([]logs.MergedPage, n)
	for i := range out {
		out[i] = logs.MergedPage{URL: fmt.Sprintf("https://a.test/%s/%d", prefix, i), Segment: "crawl_waste", LogHits: 1, InLogs: true}
	}
	return out
}

func writeMerge(s *UiStore, logID, crawlID string, pages []logs.MergedPage) error {
	ch := make(chan logs.MergedPage, 10)
	errc := make(chan error, 1)
	go func() { errc <- s.WriteMergePagesBulk(logID, crawlID, ch, nil) }()
	for _, p := range pages {
		ch <- p
	}
	close(ch)
	return <-errc
}

func mergeCount(t *testing.T, s *UiStore, logID, crawlID string) int64 {
	t.Helper()
	_, total, err := s.QueryMergePages(logID, crawlID, MergeQueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	return total
}

// Running a merge replaces only its own (log, crawl) pair: other cached
// merges stay. Before, the table was dropped and recreated on every merge.
func TestWriteMergePagesBulkKeepsOtherPairs(t *testing.T) {
	s := newTestStore(t)
	if err := writeMerge(s, "log1", "crawl1", mergePages(3, "a")); err != nil {
		t.Fatal(err)
	}
	if err := writeMerge(s, "log2", "crawl2", mergePages(2, "b")); err != nil {
		t.Fatal(err)
	}
	if n := mergeCount(t, s, "log1", "crawl1"); n != 3 {
		t.Errorf("first merge has %d rows after a second merge, want 3", n)
	}
	// Re-running a pair replaces its rows.
	if err := writeMerge(s, "log1", "crawl1", mergePages(1, "c")); err != nil {
		t.Fatal(err)
	}
	if n := mergeCount(t, s, "log1", "crawl1"); n != 1 {
		t.Errorf("re-run merge has %d rows, want 1", n)
	}
	if n := mergeCount(t, s, "log2", "crawl2"); n != 2 {
		t.Errorf("other merge has %d rows, want 2", n)
	}
}

// A writer that fails must not leave the producer blocked on a full channel.
func TestWriteMergePagesBulkFailureDoesNotBlockProducer(t *testing.T) {
	cases := map[string]func(t *testing.T, s *UiStore){
		// The transaction cannot begin: nothing was read before the fix.
		"begin": func(t *testing.T, s *UiStore) {
			if err := sqlitex.Execute(s.conn, "BEGIN", nil); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { sqlitex.Execute(s.conn, "ROLLBACK", nil) })
		},
		// An insert fails part way: reading stopped there before the fix.
		"insert": func(t *testing.T, s *UiStore) {
			if err := sqlitex.ExecuteScript(s.conn, `CREATE TRIGGER fail_merge BEFORE INSERT ON log_merge_pages
				WHEN NEW.url LIKE '%/5' BEGIN SELECT RAISE(ABORT, 'disk full'); END;`, nil); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			setup(t, s)
			ch := make(chan logs.MergedPage, 1)
			errc := make(chan error, 1)
			go func() { errc <- s.WriteMergePagesBulk("log1", "crawl1", ch, nil) }()
			sent := make(chan struct{})
			go func() {
				for _, p := range mergePages(1000, "x") {
					ch <- p
				}
				close(ch)
				close(sent)
			}()
			select {
			case <-sent:
			case <-time.After(10 * time.Second):
				t.Fatal("producer blocked after the writer failed")
			}
			if err := <-errc; err == nil {
				t.Error("writer reported success")
			}
		})
	}
}

// failCommits makes every COMMIT on conn fail: a trigger on table inserts
// a row that breaks a deferred foreign key, which is only checked at COMMIT.
func failCommits(t *testing.T, s *UiStore, conns []*sqlite.Conn, table, event string) {
	t.Helper()
	for _, c := range conns {
		if err := sqlitex.Execute(c, "PRAGMA foreign_keys = ON", nil); err != nil {
			t.Fatal(err)
		}
	}
	script := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS fk_parent (id TEXT PRIMARY KEY);
		CREATE TABLE IF NOT EXISTS fk_child (pid TEXT REFERENCES fk_parent(id) DEFERRABLE INITIALLY DEFERRED);
		CREATE TRIGGER fail_commit_%[1]s AFTER %[2]s ON %[1]s BEGIN INSERT INTO fk_child VALUES ('missing'); END;`, table, event)
	if err := sqlitex.ExecuteScript(s.conn, script, nil); err != nil {
		t.Fatal(err)
	}
}

// A failed COMMIT is reported as an error, not as success.
func TestStoreTransactionsReportCommitFailure(t *testing.T) {
	t.Run("UpdateSettings", func(t *testing.T) {
		s := newTestStore(t)
		failCommits(t, s, []*sqlite.Conn{s.conn}, "settings", "INSERT")
		if err := s.UpdateSettings(map[string]interface{}{"newKey": 1}); err == nil {
			t.Error("UpdateSettings returned nil after a failed commit")
		}
	})
	t.Run("DeleteLogJob", func(t *testing.T) {
		s := newTestStore(t)
		job, err := s.CreateLogJob("access.log", 10)
		if err != nil {
			t.Fatal(err)
		}
		failCommits(t, s, []*sqlite.Conn{s.conn}, "log_jobs", "DELETE")
		// The caller deletes the uploaded files on nil.
		if err := s.DeleteLogJob(job.ID); err == nil {
			t.Error("DeleteLogJob returned nil after a failed commit")
		}
		if got, _ := s.GetLogJob(job.ID); got == nil {
			t.Error("log job gone although the commit failed")
		}
	})
	t.Run("UpsertLogURLStatsBatch", func(t *testing.T) {
		s := newTestStore(t)
		uc, err := sqlite.OpenConn(filepath.Join(s.uiDir, "test.db"), sqlite.OpenReadWrite|sqlite.OpenWAL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { uc.Close() })
		s.urlConn = uc
		failCommits(t, s, []*sqlite.Conn{s.conn, uc}, "log_url_stats", "INSERT")
		if err := s.UpsertLogURLStatsBatch("job1", []logs.URLStat{{Path: "/a", Hits: 1}}); err == nil {
			t.Error("UpsertLogURLStatsBatch returned nil after a failed commit")
		}
	})
	t.Run("SaveMergePagesBatch", func(t *testing.T) {
		s := newTestStore(t)
		failCommits(t, s, []*sqlite.Conn{s.conn}, "log_merge_pages", "INSERT")
		if err := s.SaveMergePagesBatch("log1", "crawl1", mergePages(2, "a")); err == nil {
			t.Error("SaveMergePagesBatch returned nil after a failed commit")
		}
	})
	t.Run("WriteMergePagesBulk", func(t *testing.T) {
		s := newTestStore(t)
		failCommits(t, s, []*sqlite.Conn{s.conn}, "log_merge_pages", "INSERT")
		if err := writeMerge(s, "log1", "crawl1", mergePages(2, "a")); err == nil {
			t.Error("WriteMergePagesBulk returned nil after a failed commit")
		}
	})
}
