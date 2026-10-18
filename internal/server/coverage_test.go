package server

import (
	"encoding/json"
	"github.com/micelio/micelio/internal/types"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	"zombiezen.com/go/sqlite"
)

func TestCoverageSurvivesReopenAndLegacyIsUnknown(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ui.db")
	open := func() *UiStore {
		conn, err := sqlite.OpenConn(dbPath, sqlite.OpenCreate|sqlite.OpenReadWrite|sqlite.OpenWAL)
		if err != nil {
			t.Fatal(err)
		}
		store := &UiStore{conn: conn, uiDir: dir, crawlDir: dir}
		if err := store.migrate(); err != nil {
			t.Fatal(err)
		}
		return store
	}
	s := open()
	job, err := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.GetCrawlJob(job.ID)
	if err != nil || len(legacy.Coverage) != 0 {
		t.Fatal("legacy coverage must be unknown", err)
	}
	snapshot := &types.CrawlCoverage{Final: true, AcceptedURLs: 1, Reasons: []types.ExclusionReason{{Reason: "robots", Count: 1, Examples: []string{"https://example.com/"}}}}
	if err := s.SaveCrawlCoverage(job.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	s.conn.Close()
	s = open()
	defer s.conn.Close()
	got, err := s.GetCrawlJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var restored types.CrawlCoverage
	if err := json.Unmarshal(got.Coverage, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.Final || restored.Reasons[0].Reason != "robots" || restored.Reasons[0].Count != 1 {
		t.Fatalf("lost coverage: %+v", restored)
	}
	if _, err := s.DeleteCrawlJob(job.ID); err != nil {
		t.Fatal(err)
	}
	if job, err := s.GetCrawlJob(job.ID); err != nil || job != nil {
		t.Fatal("job not removed", err)
	}
}

func TestFilteredZeroPageCrawlPersistsCoverageThroughAPI(t *testing.T) {
	api, s, m := newTestAPI(t)
	job, err := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://example.com/", "maxPages": 10, "excludePatterns": []string{".*"}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	m.SetProgressCallback(func(e ProgressEvent) {
		if e.Type == "complete" {
			close(done)
		}
	})
	if err := m.StartCrawl(job.ID, job.Config); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("crawl did not finish")
	}
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/"+job.ID+"/status", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var response struct {
		PageCount int                  `json:"pageCount"`
		Coverage  *types.CrawlCoverage `json:"coverage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.PageCount != 0 || response.Coverage == nil || !response.Coverage.Final || len(response.Coverage.Reasons) != 1 || response.Coverage.Reasons[0].Reason != "exclude_filter" {
		t.Fatalf("missing zero-page coverage: %s", rec.Body.String())
	}
}
