package server

import (
	"errors"
	"strings"
	"testing"
)

// A second dashboard on the same folder must not start: it would mark the
// first one's running crawls as failed.
func TestSecondDashboardRefusedWhileFirstRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	first, err := NewUiStore("pid 1, http://127.0.0.1:3100")
	if err != nil {
		t.Fatal(err)
	}
	job, err := first.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://example.com/"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "running"}); err != nil {
		t.Fatal(err)
	}

	_, err = NewUiStore("pid 2, http://127.0.0.1:3101")
	if !errors.Is(err, errUIDirBusy) || !strings.Contains(err.Error(), "pid 1, http://127.0.0.1:3100") {
		t.Fatalf("second store: err = %v, want busy naming the first", err)
	}
	if j, _ := first.GetCrawlJob(job.ID); j == nil || j.Status != "running" {
		t.Errorf("first dashboard's crawl = %+v, want still running", j)
	}

	// Tools that only read (import, schedule CLI) still open the store.
	if s, err := OpenUiStore(); err != nil {
		t.Errorf("OpenUiStore next to a dashboard: %v", err)
	} else {
		s.Close()
	}

	// Once the first one stops, a new dashboard starts and recovers.
	first.Close()
	second, err := NewUiStore("pid 3, http://127.0.0.1:3100")
	if err != nil {
		t.Fatalf("after the first closed: %v", err)
	}
	defer second.Close()
	if j, _ := second.GetCrawlJob(job.ID); j == nil || j.Status != "failed" {
		t.Errorf("crawl after restart = %+v, want failed (its goroutine is gone)", j)
	}
}
