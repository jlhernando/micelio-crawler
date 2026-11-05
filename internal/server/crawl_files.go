package server

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Deleting a crawl removes its crawl DB file too. Before, History removed
// the job but left <id>.db (often hundreds of MB) in ~/.micelio/ui/crawls.

// DeleteCrawl deletes a crawl job, its cached results and, when no other job
// uses it, its crawl DB file. A running crawl must be cancelled first.
func (m *CrawlManager) DeleteCrawl(id string) (bool, error) {
	// The checks and the deleting mark are one step under mu, so no crawl,
	// restart, import or analysis of this job can start until it is gone.
	m.mu.Lock()
	switch {
	case m.active != nil && m.active.ID == id:
		m.mu.Unlock()
		return false, fmt.Errorf("crawl %s is running: cancel it first", id)
	case m.jobHolds[id] > m.softHolds[id]:
		m.mu.Unlock()
		return false, fmt.Errorf("%w: crawl %s is still being imported or analysed, try again when it ends", ErrCrawlBusy, id)
	case m.deleting[id]:
		m.mu.Unlock()
		return false, fmt.Errorf("%w: crawl %s is already being deleted", ErrCrawlBusy, id)
	}
	if m.deleting == nil {
		m.deleting = map[string]bool{}
	}
	m.deleting[id] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.deleting, id)
		m.mu.Unlock()
	}()

	job, err := m.store.GetCrawlJob(id)
	if err != nil {
		return false, err
	}
	deleted, err := m.store.DeleteCrawlJob(id)
	if err != nil || !deleted {
		return deleted, err
	}
	m.mu.Lock()
	m.cancelDeferredLocked(id) // its external check and link intelligence
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
	if job != nil && m.jobHolds[id] > 0 {
		// Cancelled deferred analysis may still read the crawl DB: remove
		// it when its hold ends (holdJobLocked's release).
		if m.pendingRemove == nil {
			m.pendingRemove = map[string]string{}
		}
		m.pendingRemove[id] = job.DBPath
		job = nil
	}
	m.mu.Unlock()
	if job != nil {
		m.store.removeCrawlFile(job.DBPath)
	}
	return true, nil
}

// ErrCrawlBusy means a crawl cannot be deleted yet: work still uses it.
var ErrCrawlBusy = errors.New("crawl busy")

// holdJob marks work that uses crawl id's job or DB (a crawl, restart,
// import, deferred analysis, result load), so DeleteCrawl refuses until the
// returned release runs. It fails while the crawl is being deleted.
func (m *CrawlManager) holdJob(id string) (release func(), err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleting[id] {
		return nil, fmt.Errorf("%w: crawl %s is being deleted", ErrCrawlBusy, id)
	}
	return m.holdJobLocked(id), nil
}

// holdJobLocked adds a hold without the deleting check: for work started
// by a caller that already holds the job. Caller holds mu.
func (m *CrawlManager) holdJobLocked(id string) func() {
	if m.jobHolds == nil {
		m.jobHolds = map[string]int{}
	}
	m.jobHolds[id]++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			var remove string
			if m.jobHolds[id]--; m.jobHolds[id] <= 0 {
				delete(m.jobHolds, id)
				remove = m.pendingRemove[id]
				delete(m.pendingRemove, id)
			}
			m.mu.Unlock()
			if remove != "" {
				m.store.removeCrawlFile(remove)
			}
		})
	}
}

// retainSoft is retainJob for work DeleteCrawl cancels instead of waiting
// for (deferred analysis): the crawl can be deleted while it runs, and its
// DB file is removed when the work ends.
func (m *CrawlManager) retainSoft(id string) func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.softHolds == nil {
		m.softHolds = map[string]int{}
	}
	m.softHolds[id]++
	release := m.holdJobLocked(id)
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			if m.softHolds[id]--; m.softHolds[id] <= 0 {
				delete(m.softHolds, id)
			}
			m.mu.Unlock()
			release()
		})
	}
}

// retainJob adds a hold for work handed off by a holder (see holdJobLocked).
func (m *CrawlManager) retainJob(id string) func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.holdJobLocked(id)
}

// removeCrawlFile deletes a crawl DB (and its -wal/-shm) when it is inside
// the crawls directory and no job uses it any more. Files elsewhere (a
// custom dbPath) are the user's and are left alone.
func (s *UiStore) removeCrawlFile(path string) {
	if path == "" || !s.ownsCrawlFile(path) {
		return
	}
	// A failed lookup is no proof the file is unused: keep it.
	if inUse, err := s.crawlFileInUse(path); err != nil || inUse {
		if err != nil {
			log.Printf("[store] keep crawl file %s: cannot check its jobs: %v", filepath.Base(path), err)
		}
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

func (s *UiStore) crawlFileInUse(path string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	err := sqlitex.Execute(s.conn, `SELECT count(*) FROM crawl_jobs WHERE db_path = ?`, &sqlitex.ExecOptions{
		Args:       []interface{}{path},
		ResultFunc: func(stmt *sqlite.Stmt) error { n = stmt.ColumnInt(0); return nil },
	})
	return n > 0, err
}

// removeOrphanCrawlFiles deletes <uuid>.db files in the crawls directory that
// no job uses: left behind by deletes before this was fixed. Runs at start,
// before any crawl, so no file can be in the middle of being created.
func (s *UiStore) removeOrphanCrawlFiles() {
	entries, err := os.ReadDir(s.crawlDir)
	if err != nil {
		return
	}
	// If the jobs table cannot be read, no file is known to be orphaned.
	if _, err := s.crawlFileInUse(""); err != nil {
		log.Printf("[store] skip orphan crawl file cleanup: %v", err)
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
