package server

import (
	"errors"
	"sync"
	"testing"

	"github.com/micelio/micelio/internal/logs"
)

func logCompleteEvents(m *CrawlManager) func() []map[string]interface{} {
	var mu sync.Mutex
	var got []map[string]interface{}
	m.SetProgressCallback(func(e ProgressEvent) {
		if e.Type != "log_complete" {
			return
		}
		mu.Lock()
		got = append(got, e.Data)
		mu.Unlock()
	})
	return func() []map[string]interface{} {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]interface{}(nil), got...)
	}
}

func urlStream(n int, failAfter error) func(fn func(logs.URLStat) error) error {
	return func(fn func(logs.URLStat) error) error {
		for i := 0; i < n; i++ {
			if err := fn(logs.URLStat{Path: "/p" + string(rune('a'+i)), Hits: 1, BotHits: 1}); err != nil {
				return err
			}
		}
		return failAfter
	}
}

// A write failure while saving URL stats fails the job: no successful
// log_complete, status "failed", and the partial URL table is dropped so a
// merge falls back to the capped summary instead of trusting it.
func TestLogJobPersistenceFailureIsNotAnnouncedComplete(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	events := logCompleteEvents(m)
	job, err := s.CreateLogJob("access.log", 100)
	if err != nil {
		t.Fatal(err)
	}

	m.finishLogJob(job.ID, &logs.LogStats{TotalLines: 10}, urlStream(3, errors.New("disk full")),
		func() map[string]interface{} { return map[string]interface{}{"durationMs": int64(5), "totalLines": 10} },
		map[string]interface{}{"totalLines": 10})

	got, _ := s.GetLogJob(job.ID)
	if got.Status != "failed" || got.ErrorMsg == nil {
		t.Fatalf("job status = %q err %v, want failed with a message", got.Status, got.ErrorMsg)
	}
	if n, _ := s.CountLogURLStats(job.ID); n != 0 {
		t.Fatalf("partial URL table kept with %d rows", n)
	}
	ev := events()
	if len(ev) != 1 || ev[0]["error"] == nil {
		t.Fatalf("log_complete events = %v, want one with an error", ev)
	}
}

func TestLogJobPersistenceSuccess(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	events := logCompleteEvents(m)
	job, _ := s.CreateLogJob("access.log", 100)

	m.finishLogJob(job.ID, &logs.LogStats{TotalLines: 10}, urlStream(3, nil),
		func() map[string]interface{} { return map[string]interface{}{"durationMs": int64(5), "totalLines": 10} },
		map[string]interface{}{"totalLines": 10})

	got, _ := s.GetLogJob(job.ID)
	if got.Status != "completed" {
		t.Fatalf("job status = %q", got.Status)
	}
	if n, _ := s.CountLogURLStats(job.ID); n != 3 {
		t.Fatalf("URL rows = %d, want 3", n)
	}
	ev := events()
	if len(ev) != 1 || ev[0]["error"] != nil || ev[0]["totalLines"] != 10 {
		t.Fatalf("log_complete events = %v", ev)
	}
}
