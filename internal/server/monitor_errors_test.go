package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCrawlErrorFor(t *testing.T) {
	cases := []struct {
		code    int
		pageErr string
		want    string
		ok      bool
	}{
		{200, "", "", false},
		{301, "", "", false},
		{404, "", "HTTP 404", true},
		{503, "", "HTTP 503", true},
		{0, "dial tcp: connection refused", "dial tcp: connection refused", true},
		{500, "read: reset", "read: reset", true},
	}
	for _, c := range cases {
		got, ok := crawlErrorFor("https://a.test/x", c.code, c.pageErr)
		if ok != c.ok || got.Error != c.want {
			t.Errorf("crawlErrorFor(%d, %q) = %+v, %v; want %q, %v", c.code, c.pageErr, got, ok, c.want, c.ok)
		}
	}
}

// A Monitor opened mid-crawl gets the failed pages seen so far, including
// 4xx/5xx answers, newest kept when there are more than the cap, with
// credentials in URLs masked.
func TestStatusReturnsRecentErrors(t *testing.T) {
	api, s, m := newTestAPI(t)
	job, err := s.CreateCrawlJob(map[string]interface{}{"seedUrl": "https://a.test/"})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.active = &ActiveCrawl{ID: job.ID, Config: map[string]interface{}{}, StatusCodes: map[string]int{"4xx": 1}}
	m.mu.Unlock()
	defer func() { // no crawl goroutine to cancel at cleanup
		m.mu.Lock()
		m.active = nil
		m.mu.Unlock()
	}()

	add := func(url string, code int, pageErr string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if ce, ok := crawlErrorFor(url, code, pageErr); ok {
			if len(m.active.RecentErrors) >= maxRecentErrors {
				m.active.RecentErrors = m.active.RecentErrors[1:]
			}
			m.active.RecentErrors = append(m.active.RecentErrors, ce)
		}
	}
	add("https://user:SECRET@a.test/private", 401, "")
	for i := 0; i < maxRecentErrors+5; i++ {
		add(fmt.Sprintf("https://a.test/missing/%d", i), 404, "")
	}

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/"+job.ID+"/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Error("credentials leaked in recentErrors")
	}
	var body struct {
		RecentErrors []CrawlError `json:"recentErrors"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.RecentErrors) != maxRecentErrors {
		t.Fatalf("got %d errors, want %d", len(body.RecentErrors), maxRecentErrors)
	}
	last := body.RecentErrors[len(body.RecentErrors)-1]
	if last.URL != fmt.Sprintf("https://a.test/missing/%d", maxRecentErrors+4) || last.Error != "HTTP 404" || last.StatusCode != 404 {
		t.Errorf("newest error = %+v", last)
	}
}
