package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/micelio/micelio/internal/scheduler"
)

// A scheduled run creates a normal crawl job (visible in History) tagged with
// its schedule, and reports the job's outcome.
func TestJobRunnerCreatesCrawlJob(t *testing.T) {
	_, s, m := newTestAPI(t)
	r := jobRunner{store: s, manager: m}
	cfg := scheduler.Config{
		URL: "https://example.com/", CronExpr: "@daily", Name: "nightly",
		CrawlMap: map[string]interface{}{"seedUrl": "https://example.com/", "maxPages": 5, "excludePatterns": []string{".*"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := r.Run(ctx, cfg, scheduler.NewRunControlForTest())
	if res.Status != "success" || res.CrawlID == "" {
		t.Fatalf("result = %+v", res)
	}
	job, err := s.GetCrawlJob(res.CrawlID)
	if err != nil || job == nil || job.Status != "completed" {
		t.Fatalf("job = %+v err=%v", job, err)
	}
	sched, _ := job.Config["schedule"].(map[string]interface{})
	if sched["scheduleName"] != "nightly" {
		t.Errorf("job not tagged with its schedule: %v", job.Config["schedule"])
	}
}

// A duplicate of a running schedule gets 409 without overwriting the saved
// config of the running one (it would apply after a restart).
func TestScheduleDuplicateKeepsSavedConfig(t *testing.T) {
	fakeHome(t)
	t.Chdir(t.TempDir())
	api, _, m := newTestAPI(t)
	defer m.StopAllSchedulers()
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/schedules", strings.NewReader(body)))
		return rec
	}
	if rec := post(`{"cron":"@daily","name":"n","config":{"seedUrl":"https://example.com/","limit":10}}`); rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(`{"cron":"@daily","name":"n","config":{"seedUrl":"https://example.com/","limit":99}}`); rec.Code != 409 {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body.String())
	}
	states, err := scheduler.ListSchedules()
	if err != nil || len(states) != 1 {
		t.Fatalf("states = %v, %v", states, err)
	}
	if got := states[0].CrawlConfig["limit"]; got != float64(10) {
		t.Fatalf("saved limit = %v, want 10 (duplicate overwrote it)", got)
	}
}

// Identical POSTs sent at once start one scheduler: the others get 409, and
// the one that runs is the one the manager tracks (so it can be stopped).
func TestScheduleConcurrentCreateStartsOne(t *testing.T) {
	fakeHome(t)
	t.Chdir(t.TempDir())
	api, _, m := newTestAPI(t)
	defer m.StopAllSchedulers()
	const n = 16
	codes := make([]int, n)
	ids := make([]string, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/schedules", strings.NewReader(
				`{"cron":"@daily","name":"race","config":{"seedUrl":"https://example.com/"}}`)))
			codes[i] = rec.Code
			var body map[string]any
			json.Unmarshal(rec.Body.Bytes(), &body)
			ids[i], _ = body["id"].(string)
		}(i)
	}
	close(start)
	wg.Wait()
	ok, conflict := 0, 0
	var id string
	for i, c := range codes {
		switch c {
		case 200:
			ok++
			id = ids[i]
		case 409:
			conflict++
		default:
			t.Fatalf("unexpected status %d", c)
		}
	}
	if ok != 1 || conflict != n-1 {
		t.Fatalf("%d created, %d conflicts; want 1 and %d", ok, conflict, n-1)
	}
	m.mu.RLock()
	tracked, reserved := len(m.schedulers), len(m.scheduleReserved)
	m.mu.RUnlock()
	if tracked != 1 || reserved != 0 || m.trackedScheduler(id) == nil {
		t.Fatalf("tracked %d, reserved %d, id %q tracked=%v", tracked, reserved, id, m.trackedScheduler(id) != nil)
	}
}

// A delete or shutdown that arrives while a schedule is being created (ID
// reserved, scheduler.New still saving) stops it: the scheduler is never
// tracked or run, and a deleted one leaves no saved state behind.
func TestScheduleStoppedWhileBeingCreated(t *testing.T) {
	for _, tc := range []string{"delete", "shutdown"} {
		t.Run(tc, func(t *testing.T) {
			fakeHome(t)
			t.Chdir(t.TempDir())
			_, s, m := newTestAPI(t)
			afterScheduleReserved = func(id string) {
				if tc == "delete" {
					m.DeleteScheduler(id)
					scheduler.DeleteSchedule(id)
				} else {
					m.StopAllSchedulers()
				}
			}
			t.Cleanup(func() { afterScheduleReserved = nil })

			_, err := m.startUISchedule(s, scheduleRequest{Cron: "@daily", Name: tc,
				Config: map[string]interface{}{"seedUrl": "https://example.com/"}})
			if !errors.Is(err, errScheduleCancelled) {
				t.Fatalf("err = %v, want errScheduleCancelled", err)
			}
			m.mu.RLock()
			tracked, reserved := len(m.schedulers), len(m.scheduleReserved)
			m.mu.RUnlock()
			if tracked != 0 || reserved != 0 {
				t.Fatalf("tracked %d, reserved %d after cancel", tracked, reserved)
			}
			states, _ := scheduler.ListSchedules()
			if tc == "delete" && len(states) != 0 {
				t.Fatalf("deleted schedule left saved state: %v", states)
			}
		})
	}
}

func TestScheduleAPIRejectsCommandsAndBadRanges(t *testing.T) {
	api, _, _ := newTestAPI(t)
	post := func(path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return rec
	}
	if rec := post("/api/schedules", `{"url":"https://example.com/","cron":"@daily","precheck":{"command":"touch /tmp/pwned"}}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "CLI") {
		t.Errorf("command accepted: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/schedules/check", `{"egressCidrs":["nope"]}`); rec.Code != 400 {
		t.Errorf("bad range accepted: %d", rec.Code)
	}
	if rec := post("/api/schedules", `{"url":"http://127.0.0.1/","cron":"@daily"}`); rec.Code != 400 {
		t.Errorf("private seed accepted: %d", rec.Code)
	}
	if rec := post("/api/schedules", `{"cron":"not a cron","config":{"seedUrl":"https://example.com/"}}`); rec.Code != 400 {
		t.Errorf("bad cron accepted: %d", rec.Code)
	}
}
