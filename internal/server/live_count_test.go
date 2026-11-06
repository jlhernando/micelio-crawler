package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// The sidebar reads page counts from /api/crawls. The DB copy is written
// every 10 pages, so the running crawl must report its live in-memory count
// (what the Monitor shows); other crawls keep their stored counts.
func TestListCrawlsReportsLiveCountForActiveCrawl(t *testing.T) {
	api, s, m := newTestAPI(t)
	running, err := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://a.test/"})
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://b.test/"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCrawlJob(done.ID, map[string]interface{}{"status": "completed", "pageCount": 42, "errorCount": 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCrawlJob(running.ID, map[string]interface{}{"status": "running"}); err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	m.active = &ActiveCrawl{ID: running.ID, Config: map[string]interface{}{}, StatusCodes: map[string]int{"2xx": 6, "4xx": 1}, PageCount: 7, ErrorCount: 1}
	m.mu.Unlock()
	defer func() { // no crawl goroutine to cancel at cleanup
		m.mu.Lock()
		m.active = nil
		m.mu.Unlock()
	}()

	type job struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		PageCount  int    `json:"pageCount"`
		ErrorCount int    `json:"errorCount"`
	}
	list := func() map[string]job {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawls", nil))
		if rec.Code != 200 {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Crawls []job `json:"crawls"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		out := map[string]job{}
		for _, j := range body.Crawls {
			out[j.ID] = j
		}
		return out
	}

	got := list()
	if j := got[running.ID]; j.PageCount != 7 || j.ErrorCount != 1 {
		t.Errorf("running crawl = %+v, want live 7 pages / 1 error", j)
	}
	if j := got[done.ID]; j.PageCount != 42 || j.ErrorCount != 3 {
		t.Errorf("completed crawl = %+v, want stored 42 / 3", j)
	}

	// The status endpoint agrees.
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/"+running.ID+"/status", nil))
	var st job
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.PageCount != 7 {
		t.Errorf("status pageCount = %d, want 7", st.PageCount)
	}

	// Paused crawls keep showing the live count too.
	if err := s.UpdateCrawlJob(running.ID, map[string]interface{}{"status": "paused"}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.active.Paused = true
	m.active.PageCount = 9
	m.mu.Unlock()
	if j := list()[running.ID]; j.PageCount != 9 {
		t.Errorf("paused crawl pageCount = %d, want 9", j.PageCount)
	}

	// Once the crawl is no longer active, the stored final count is used.
	m.mu.Lock()
	m.active = nil
	m.mu.Unlock()
	if err := s.UpdateCrawlJob(running.ID, map[string]interface{}{"status": "completed", "pageCount": 12}); err != nil {
		t.Fatal(err)
	}
	if j := list()[running.ID]; j.PageCount != 12 || j.Status != "completed" {
		t.Errorf("finished crawl = %+v, want stored 12 completed", j)
	}
}
