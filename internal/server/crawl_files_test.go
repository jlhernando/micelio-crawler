package server

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite/sqlitex"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Deleting a crawl from History removes its crawl DB, unless another job
// (a resume) still uses it or it lives outside the crawls directory.
func TestDeleteCrawlRemovesItsFile(t *testing.T) {
	api, s, _ := newTestAPI(t)
	del := func(id string) int {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/crawl/"+id, nil))
		return rec.Code
	}

	own, _ := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://a.test/"})
	touch(t, own.DBPath)
	touch(t, own.DBPath+"-wal")
	if code := del(own.ID); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	if exists(own.DBPath) || exists(own.DBPath+"-wal") {
		t.Error("crawl file left behind")
	}

	// A resumed crawl shares the file: it stays until the last job goes.
	first, _ := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://b.test/"})
	touch(t, first.DBPath)
	resumed, _ := s.CreateCrawlJobWithDB(map[string]interface{}{"seedUrl": "https://b.test/"}, first.DBPath)
	del(first.ID)
	if !exists(first.DBPath) {
		t.Fatal("file removed while a resumed job still uses it")
	}
	del(resumed.ID)
	if exists(first.DBPath) {
		t.Error("file kept after the last job was deleted")
	}

	// A custom path outside the crawls directory is the user's.
	custom := filepath.Join(t.TempDir(), "stress-test.db")
	touch(t, custom)
	job, _ := s.CreateCrawlJobWithDB(map[string]interface{}{"seedUrl": "https://c.test/"}, custom)
	del(job.ID)
	if !exists(custom) {
		t.Error("custom crawl file outside the crawls directory removed")
	}
}

// At start, <uuid>.db files no job uses are removed (left by old deletes);
// files in use and other names stay.
func TestRemoveOrphanCrawlFiles(t *testing.T) {
	s := newTestStore(t)
	kept, _ := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://a.test/"})
	touch(t, kept.DBPath)
	orphan := filepath.Join(s.crawlDir, uuid.NewString()+".db")
	touch(t, orphan)
	touch(t, orphan+"-shm")
	other := filepath.Join(s.crawlDir, "import-123.db")
	touch(t, other)

	s.removeOrphanCrawlFiles()
	if exists(orphan) || exists(orphan+"-shm") {
		t.Error("orphan crawl file kept")
	}
	if !exists(kept.DBPath) || !exists(other) {
		t.Error("removed a file that is in use or not a crawl DB")
	}
}

// A failed jobs lookup is no proof a crawl file is unused: keep the file.
func TestRemoveCrawlFileKeepsFileWhenLookupFails(t *testing.T) {
	s := newTestStore(t)
	job, _ := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://a.test/"})
	touch(t, job.DBPath)
	orphan := filepath.Join(s.crawlDir, uuid.NewString()+".db")
	touch(t, orphan)
	// Simulates an unreadable UI database.
	if err := sqlitex.Execute(s.conn, "ALTER TABLE crawl_jobs RENAME TO crawl_jobs_gone", nil); err != nil {
		t.Fatal(err)
	}
	s.removeCrawlFile(job.DBPath)
	s.removeOrphanCrawlFiles()
	if !exists(job.DBPath) {
		t.Error("crawl file deleted although its jobs could not be checked")
	}
	if !exists(orphan) {
		t.Error("startup cleanup deleted a file although the jobs could not be checked")
	}
}

// A crawl that an import (and its deferred analysis) is still using cannot
// be deleted; once the work ends it can.
func TestDeleteCrawlWaitsForImport(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	src := filepath.Join(t.TempDir(), "site.db")
	writeTestCrawlDB(t, src)

	job, err := m.StartImport(src, ImportOptions{LinkIntelligence: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := m.DeleteCrawl(job.ID)
	if !errors.Is(err, ErrCrawlBusy) || deleted {
		t.Fatalf("delete during import = %v, %v; want ErrCrawlBusy", deleted, err)
	}
	m.WaitBackground()
	if got, _ := s.GetCrawlJob(job.ID); got == nil || got.Status != "completed" {
		t.Fatalf("import did not complete: %+v", got)
	}
	if deleted, err := m.DeleteCrawl(job.ID); err != nil || !deleted {
		t.Fatalf("delete after import = %v, %v", deleted, err)
	}
	if exists(job.DBPath) {
		t.Error("crawl file left behind")
	}
	if m.GetCachedResults(job.ID) != nil {
		t.Error("deleted crawl still cached")
	}
}

// While a crawl is being deleted, no crawl, restart or result load of it
// can start; holds taken before the delete make it refuse.
func TestDeleteCrawlBlocksNewWork(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	job, _ := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://a.test/"})
	if err := os.WriteFile(job.DBPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "completed"})
	s.SaveCrawlStats(job.ID, []byte(`{"totalPages":0}`))

	m.mu.Lock()
	m.deleting = map[string]bool{job.ID: true}
	m.mu.Unlock()
	if err := m.StartCrawl(job.ID, map[string]interface{}{"seedUrl": "https://a.test/"}); !errors.Is(err, ErrCrawlBusy) {
		t.Errorf("StartCrawl while deleting = %v", err)
	}
	if _, err := m.RestartCrawl(job.ID, nil); !errors.Is(err, ErrCrawlBusy) {
		t.Errorf("RestartCrawl while deleting = %v", err)
	}
	if r := m.GetOrLoadResults(job.ID); r != nil {
		t.Error("results loaded while deleting")
	}
	if r := m.GetQuickStats(job.ID); r != nil {
		t.Error("stats cached while deleting")
	}
	if _, err := m.DeleteCrawl(job.ID); !errors.Is(err, ErrCrawlBusy) {
		t.Errorf("second delete = %v", err)
	}
	m.mu.Lock()
	m.deleting = nil
	m.mu.Unlock()

	rel, err := m.holdJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteCrawl(job.ID); !errors.Is(err, ErrCrawlBusy) {
		t.Errorf("delete while held = %v", err)
	}
	rel()
	rel() // idempotent
	if deleted, err := m.DeleteCrawl(job.ID); err != nil || !deleted {
		t.Errorf("delete after release = %v, %v", deleted, err)
	}
	if len(m.jobHolds) != 0 || len(m.deleting) != 0 {
		t.Errorf("leftover state: holds %v deleting %v", m.jobHolds, m.deleting)
	}
}

// Deferred analysis does not make a delete wait: the delete cancels it and
// the crawl DB is removed when the analysis lets go of it. A hard hold
// (import, crawl) still refuses the delete.
func TestDeleteCrawlCancelsDeferredAndRemovesFileLater(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	job, _ := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://a.test/"})
	if err := os.WriteFile(job.DBPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "completed"})

	ctx, done := m.startDeferred(job.ID)
	soft := m.retainSoft(job.ID)
	hard, err := m.holdJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.DeleteCrawl(job.ID); !errors.Is(err, ErrCrawlBusy) {
		t.Errorf("delete with a hard hold = %v, want ErrCrawlBusy", err)
	}
	hard()

	ok, err := m.DeleteCrawl(job.ID)
	if err != nil || !ok {
		t.Fatalf("delete during deferred analysis = %v, %v", ok, err)
	}
	if ctx.Err() == nil {
		t.Error("deferred analysis not cancelled")
	}
	if _, err := os.Stat(job.DBPath); err != nil {
		t.Errorf("crawl DB removed while deferred analysis still holds it: %v", err)
	}
	done()
	soft()
	if _, err := os.Stat(job.DBPath); !os.IsNotExist(err) {
		t.Errorf("crawl DB kept after the last hold ended: %v", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.jobHolds)+len(m.softHolds)+len(m.pendingRemove) != 0 {
		t.Errorf("leftover state: holds %v soft %v pending %v", m.jobHolds, m.softHolds, m.pendingRemove)
	}
}
