package server

import (
	"context"
	"net/http/httptest"
	"strings"
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
