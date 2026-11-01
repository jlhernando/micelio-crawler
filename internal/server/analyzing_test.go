package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Every way a crawl reaches "completed" (a crawl, Stop & Analyze and resume
// all go through runCrawl; import through runImport) must report
// "analyzing" until its results are cached, and stop reporting it after.
func TestAnalyzingOnEveryPathToCompleted(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Test page title here</title></head><body><h1>Hi</h1><a href="/b">b</a></body></html>`)
	}))
	defer site.Close()

	paths := map[string]func(t *testing.T, m *CrawlManager, s *UiStore) string{
		"crawl": func(t *testing.T, m *CrawlManager, s *UiStore) string {
			m.SkipSSRF = true
			cfg := map[string]interface{}{"seedUrl": site.URL + "/", "limit": 5, "depth": 2, "concurrency": 1, "delay": 0}
			job, err := s.CreateCrawlJob(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := m.StartCrawl(job.ID, cfg); err != nil {
				t.Fatal(err)
			}
			return job.ID
		},
		"import": func(t *testing.T, m *CrawlManager, s *UiStore) string {
			src := filepath.Join(t.TempDir(), "site.db")
			writeTestCrawlDB(t, src)
			job, err := m.ImportCrawl(context.Background(), src, ImportOptions{})
			if err != nil {
				t.Fatal(err)
			}
			return job.ID
		},
	}
	for name, start := range paths {
		t.Run(name, func(t *testing.T) {
			api, s, m := newTestAPI(t)
			status := func(id string) map[string]any {
				rec := httptest.NewRecorder()
				api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/"+id+"/status", nil))
				var got map[string]any
				json.Unmarshal(rec.Body.Bytes(), &got)
				return got
			}
			var mu sync.Mutex
			var during map[string]any
			m.testBeforeCache = func(id string) {
				st := status(id)
				mu.Lock()
				during = st
				mu.Unlock()
			}
			id := start(t, m, s)

			deadline := time.Now().Add(30 * time.Second)
			for m.GetCachedResults(id) == nil && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			m.WaitBackground()
			mu.Lock()
			defer mu.Unlock()
			if during == nil {
				t.Fatal("analysis never reached the cache step")
			}
			if during["status"] != "completed" || during["analyzing"] != true {
				t.Errorf("before results are cached: status=%v analyzing=%v, want completed and true", during["status"], during["analyzing"])
			}
			if after := status(id); after["analyzing"] != nil {
				t.Errorf("after results are cached: analyzing=%v, want absent", after["analyzing"])
			}
		})
	}
}
