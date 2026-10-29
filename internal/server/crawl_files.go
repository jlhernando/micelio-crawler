package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Deleting a crawl removes its crawl DB file too. Before, History removed
// the job but left <id>.db (often hundreds of MB) in ~/.micelio/ui/crawls.

// DeleteCrawl deletes a crawl job, its cached results and, when no other job
// uses it, its crawl DB file. A running crawl must be cancelled first.
func (m *CrawlManager) DeleteCrawl(id string) (bool, error) {
	m.mu.RLock()
	running := m.active != nil && m.active.ID == id
	m.mu.RUnlock()
	if running {
		return false, fmt.Errorf("crawl %s is running: cancel it first", id)
	}
	job, err := m.store.GetCrawlJob(id)
	if err != nil {
		return false, err
	}
	deleted, err := m.store.DeleteCrawlJob(id)
	if err != nil || !deleted {
		return deleted, err
	}
	m.mu.Lock()
	if old := m.results[id]; old != nil && old.loadCancel != nil {
		old.loadCancel()
	}
	delete(m.results, id)
	delete(m.resultSize, id)
	for i, o := range m.resultOrder {
		if o == id {
			m.resultOrder = append(m.resultOrder[:i], m.resultOrder[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	if job != nil {
		m.store.removeCrawlFile(job.DBPath)
	}
	return true, nil
}

// removeCrawlFile deletes a crawl DB (and its -wal/-shm) when it is inside
// the crawls directory and no job uses it any more. Files elsewhere (a
// custom dbPath) are the user's and are left alone.
func (s *UiStore) removeCrawlFile(path string) {
	if path == "" || !s.ownsCrawlFile(path) || s.crawlFileInUse(path) {
		return
	}
	var size int64
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if fi, err := os.Stat(p); err == nil {
			size += fi.Size()
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("[store] remove %s: %v", p, err)
		}
	}
	log.Printf("[store] removed crawl file %s (%.1f MB)", filepath.Base(path), float64(size)/(1<<20))
}

func (s *UiStore) ownsCrawlFile(path string) bool {
	dir, err := filepath.Abs(s.crawlDir)
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(path)
	return err == nil && filepath.Dir(abs) == dir
}

func (s *UiStore) crawlFileInUse(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	_ = sqlitex.Execute(s.conn, `SELECT count(*) FROM crawl_jobs WHERE db_path = ?`, &sqlitex.ExecOptions{
		Args:       []interface{}{path},
		ResultFunc: func(stmt *sqlite.Stmt) error { n = stmt.ColumnInt(0); return nil },
	})
	return n > 0
}

// removeOrphanCrawlFiles deletes <uuid>.db files in the crawls directory that
// no job uses: left behind by deletes before this was fixed. Runs at start,
// before any crawl, so no file can be in the middle of being created.
func (s *UiStore) removeOrphanCrawlFiles() {
	entries, err := os.ReadDir(s.crawlDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		id, ok := strings.CutSuffix(name, ".db")
		if !ok || e.IsDir() || uuid.Validate(id) != nil {
			continue
		}
		s.removeCrawlFile(filepath.Join(s.crawlDir, name))
	}
}
