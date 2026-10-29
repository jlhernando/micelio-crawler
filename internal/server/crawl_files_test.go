package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
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
