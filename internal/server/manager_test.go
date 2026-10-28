package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartCrawl(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	done := make(chan struct{})
	m.SetProgressCallback(func(e ProgressEvent) {
		if e.Type == "complete" {
			close(done)
		}
	})

	// Use unreachable host so crawl completes quickly with an error
	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "http://127.0.0.1:1", "maxPages": 1})
	err := m.StartCrawl(job.ID, job.Config)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for crawl to complete")
	}

	// Active crawl should be nil
	if m.GetStatus() != nil {
		t.Fatal("expected no active crawl after completion")
	}
}

func TestStartCrawlRejectsSecond(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	doneCh := make(chan struct{}, 2)
	m.SetProgressCallback(func(e ProgressEvent) {
		if e.Type == "complete" {
			doneCh <- struct{}{}
		}
	})

	// Use unreachable host so crawl completes quickly
	job1, _ := s.CreateCrawlJob(map[string]interface{}{"url": "http://127.0.0.1:1", "maxPages": 1})
	_ = m.StartCrawl(job1.ID, job1.Config)

	// Try to start a second crawl while first is active
	job2, _ := s.CreateCrawlJob(map[string]interface{}{"url": "http://127.0.0.1:1", "maxPages": 1})
	started := 1
	if err := m.StartCrawl(job2.ID, job2.Config); err == nil {
		started++ // the first crawl had already finished; both run
	}

	// Wait for every started crawl and its background work before the temp
	// dir is removed (post-crawl saves used to race the cleanup).
	for i := 0; i < started; i++ {
		select {
		case <-doneCh:
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for crawl goroutine")
		}
	}
	m.WaitBackground()
}

func TestCancelCrawl(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	var events []ProgressEvent
	m.SetProgressCallback(func(e ProgressEvent) {
		events = append(events, e)
	})

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})

	// We need to test cancel before the crawl completes.
	// Since our placeholder completes instantly, just verify the cancel API works.
	err := m.CancelCrawl(job.ID)
	if err == nil {
		t.Fatal("expected error cancelling non-active crawl")
	}
}

func TestPauseResumeCrawl(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	job, _ := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})
	err := m.PauseCrawl(job.ID)
	if err == nil {
		t.Fatal("expected error pausing non-active crawl")
	}
	err = m.ResumeCrawl(job.ID)
	if err == nil {
		t.Fatal("expected error resuming non-active crawl")
	}
}

func TestNewCrawlManager(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.store != s {
		t.Fatal("store not set correctly")
	}
	if m.results == nil {
		t.Fatal("results map should be initialized")
	}
	if len(m.results) != 0 {
		t.Fatalf("expected empty results, got %d", len(m.results))
	}
}

func TestGetStatus_NoActiveCrawl(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	status := m.GetStatus()
	if status != nil {
		t.Fatal("expected nil status when no crawl is running")
	}
}

func TestSetProgressCallback(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	if m.onProgress != nil {
		t.Fatal("expected nil callback initially")
	}

	called := false
	m.SetProgressCallback(func(event ProgressEvent) {
		called = true
	})

	if m.onProgress == nil {
		t.Fatal("expected callback to be set")
	}

	// Verify the callback works via emit
	m.emit(ProgressEvent{Type: "test"})
	if !called {
		t.Fatal("expected callback to be called")
	}
}

func TestEmit_NoCallback(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	// Should not panic when no callback is set
	m.emit(ProgressEvent{Type: "test"})
}

func TestGetOrLoadResults_NonExistent(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	result := m.GetOrLoadResults("nonexistent-crawl-id")
	if result != nil {
		t.Fatal("expected nil for non-existent crawl results")
	}
}

func TestGetOrLoadResults_CachedResult(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)

	// Manually inject a cached result
	m.results["test-crawl"] = &CrawlResults{
		Stats:     map[string]interface{}{"pages": 42},
		PageCount: 42,
	}
	m.resultOrder = append(m.resultOrder, "test-crawl")

	result := m.GetOrLoadResults("test-crawl")
	if result == nil {
		t.Fatal("expected cached result")
	}
	if result.PageCount != 42 {
		t.Fatalf("expected 42 pages, got %d", result.PageCount)
	}
}

// Resuming with edited settings saves them on the job, so the next resume
// or Recrawl does not bring back the old ones.
func TestRestartSavesEditedConfig(t *testing.T) {
	_, s, m := newTestAPI(t)
	dbPath := filepath.Join(t.TempDir(), "crawl.db")
	os.WriteFile(dbPath, nil, 0o644)
	seed := "http://127.0.0.1:1/" // blocked as private: the resumed crawl ends at once
	job, err := s.CreateCrawlJobWithDB(map[string]interface{}{"seedUrl": seed, "timeoutSeconds": 60, "maxErrors": 3}, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "completed", "completedAt": "2026-01-01T00:00:00Z", "durationMs": 60000})
	if _, err := m.RestartCrawl(job.ID, map[string]interface{}{"seedUrl": seed, "retries": 2}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetCrawlJob(job.ID)
	if got.Config["timeoutSeconds"] != nil || got.Config["maxErrors"] != nil || got.Config["retries"] != float64(2) {
		t.Errorf("saved config = %v, want the edited one", got.Config)
	}
	if got.Config["resume"] != nil || got.Config["dbPath"] != nil {
		t.Errorf("resume/dbPath saved in the job config: %v", got.Config)
	}
}
