package server

import (
	"context"
	"github.com/micelio/micelio/internal/scheduler"
	"testing"
	"time"
)

func sharedWaitStatus(t *testing.T, s *UiStore, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if job, err := s.GetCrawlJob(id); err == nil && job != nil && job.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job did not reach %s", want)
}

func TestSharedSupervisorResumesOnlyOwnPauses(t *testing.T) {
	_, s, m := newTestAPI(t)
	config := map[string]interface{}{"seedUrl": "https://example.com/"}
	job, err := s.CreateCrawlJob(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "running"}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.active = &ActiveCrawl{ID: job.ID, Config: config, Cancel: func() {}, StatusCodes: map[string]int{}}
	m.mu.Unlock()
	t.Cleanup(func() { m.mu.Lock(); m.active = nil; m.mu.Unlock() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctl := scheduler.NewRunControl()
	done := make(chan struct{})
	go func() { defer close(done); m.superviseJob(ctx, job.ID, ctl) }()
	ctl.Set(true, "check failed")
	sharedWaitStatus(t, s, job.ID, "paused")
	ctl.Set(false, "")
	sharedWaitStatus(t, s, job.ID, "running")
	if err := m.PauseCrawl(job.ID); err != nil {
		t.Fatal(err)
	}
	ctl.Set(true, "check failed again")
	time.Sleep(150 * time.Millisecond)
	ctl.Set(false, "")
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	current, _ := s.GetCrawlJob(job.ID)
	if current.Status != "paused" {
		t.Fatalf("resumed a user pause: %s", current.Status)
	}
}

func TestSharedSupervisorReturnsWhenJobDeleted(t *testing.T) {
	_, _, m := newTestAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctl := scheduler.NewRunControl()
	ctl.Set(true, "check failed")
	done := make(chan bool, 1)
	go func() { job, _, stopped := m.superviseJob(ctx, "deleted-job", ctl); done <- job == nil && !stopped }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("deleted job was not reported")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor waited for a deleted job")
	}
}
