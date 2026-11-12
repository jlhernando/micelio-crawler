package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/micelio/micelio/internal/crawler"
)

// A restart refused because a crawl is active (another one, or the same
// crawl still paused in memory) leaves the job record exactly as it was:
// status, completion fields, config, events and jobs sharing its DB.
func TestRestartRefusedLeavesJobUntouched(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	dbPath := filepath.Join(t.TempDir(), "crawl.db")
	os.WriteFile(dbPath, nil, 0o644)
	job, err := s.CreateCrawlJobWithDB(map[string]interface{}{"seedUrl": "https://example.com/", "limit": float64(10)}, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "completed", "completedAt": "2026-01-01T00:00:00Z", "durationMs": 60000})
	dup, _ := s.CreateCrawlJobWithDB(map[string]interface{}{"seedUrl": "https://example.com/"}, dbPath)
	before, _ := s.GetCrawlJob(job.ID)

	for _, activeID := range []string{"other-crawl", job.ID} {
		m.mu.Lock()
		m.active = &ActiveCrawl{ID: activeID, Cancel: func() {}, Config: map[string]interface{}{}, StatusCodes: map[string]int{}}
		m.mu.Unlock()
		_, err := m.RestartCrawl(job.ID, map[string]interface{}{"seedUrl": "https://example.com/", "limit": float64(99)})
		m.mu.Lock()
		m.active = nil
		m.mu.Unlock()
		if err == nil {
			t.Fatalf("restart with %s active succeeded", activeID)
		}

		got, _ := s.GetCrawlJob(job.ID)
		if got.Status != "completed" || got.CompletedAt == nil || got.DurationMs == nil || *got.DurationMs != 60000 {
			t.Fatalf("%s active: job status/completion changed: %s %v %v", activeID, got.Status, got.CompletedAt, got.DurationMs)
		}
		if got.Config["limit"] != float64(10) {
			t.Fatalf("%s active: config overwritten: %v", activeID, got.Config)
		}
		if len(got.Events) != len(before.Events) {
			t.Fatalf("%s active: events added: %+v", activeID, got.Events)
		}
		if d, _ := s.GetCrawlJob(dup.ID); d == nil {
			t.Fatalf("%s active: job sharing the DB was deleted", activeID)
		}
	}
}

// Once the crawl loop has returned (job "completed", analysis running),
// resume, cancel, stop and tune do not touch the crawl loop: they would
// overwrite the status. Cancel and stop reach the post-crawl run instead
// (cancel_analysis_test.go); with none registered they are refused.
func TestLifecycleRefusedDuringPostCrawlAnalysis(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	job, _ := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://example.com/"})
	s.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "completed", "completedAt": "2026-01-01T00:00:00Z"})
	cancelled := false
	active := &ActiveCrawl{ID: job.ID, finished: true, Cancel: func() { cancelled = true },
		Config: map[string]interface{}{}, StatusCodes: map[string]int{}}
	m.mu.Lock()
	m.active = active
	m.mu.Unlock()
	t.Cleanup(func() { m.mu.Lock(); m.active = nil; m.mu.Unlock() })

	calls := map[string]func() error{
		"resume": func() error { return m.ResumeCrawl(job.ID) },
		"cancel": func() error { return m.CancelCrawl(job.ID) },
		"stop":   func() error { return m.StopCrawl(job.ID) },
		"tune": func() error {
			_, err := m.TuneCrawl(job.ID, crawler.Tuning{DelayMs: 500})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, errCrawlFinished) {
			t.Errorf("%s: err = %v, want errCrawlFinished", name, err)
		}
	}
	got, _ := s.GetCrawlJob(job.ID)
	if got.Status != "completed" {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if cancelled || active.Stopping {
		t.Error("analysis context cancelled or crawl marked stopping")
	}
	if m.GetStatus() != active {
		t.Error("active crawl cleared during analysis")
	}
}

// Cancel or stop clicked while a restart prepares the job: the crawl does
// not start, "running" is not saved over the cancel, and the slot is free.
func TestStartCrawlCancelledDuringPrepare(t *testing.T) {
	for _, action := range []string{"cancel", "stop"} {
		t.Run(action, func(t *testing.T) {
			s := newTestStore(t)
			m := newTestManager(t, s)
			job, _ := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "http://127.0.0.1:1/"})
			prepare := func() error {
				s.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "running"})
				if action == "cancel" {
					return m.CancelCrawl(job.ID)
				}
				return m.StopCrawl(job.ID)
			}
			if err := m.startCrawl(job.ID, map[string]interface{}{"seedUrl": "http://127.0.0.1:1/", "resume": true}, prepare); err == nil {
				t.Fatal("crawl started after it was cancelled")
			}
			m.WaitBackground()
			got, _ := s.GetCrawlJob(job.ID)
			if got.Status != "cancelled" {
				t.Fatalf("status = %q, want cancelled", got.Status)
			}
			if a := m.GetStatus(); a != nil {
				t.Fatalf("slot still held by %s", a.ID)
			}
		})
	}
}
