package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func bigList(n int) []interface{} {
	out := make([]interface{}, n)
	for i := range out {
		out[i] = map[string]interface{}{"url": "https://example.com/p"}
	}
	return out
}

// TestStatsConcurrentRequests hits /stats concurrently. Before the fix,
// ensureRankpediaSEO and capNestedArray wrote into the shared Stats map under
// a read lock ("concurrent map writes" crash, caught by -race) and
// capNestedArray permanently truncated the cached lists.
func TestStatsConcurrentRequests(t *testing.T) {
	api, _, m := newTestAPI(t)

	stats := map[string]interface{}{
		"totalPages": float64(1000),
		"redirectStats": map[string]interface{}{
			"chains": bigList(500),
		},
		"canonicalStats": map[string]interface{}{
			"canonicalized": bigList(450),
		},
		"urlIssueStats": map[string]interface{}{
			"uppercase": bigList(300),
		},
	}
	m.mu.Lock()
	m.results["race"] = &CrawlResults{Stats: stats, PageCount: 1000, PagesReady: true}
	m.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/api/crawl/race/stats", nil)
			rec := httptest.NewRecorder()
			api.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("status %d: %s", rec.Code, rec.Body.String())
				return
			}
			var resp struct {
				Stats map[string]interface{} `json:"stats"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Errorf("decode: %v", err)
				return
			}
			rs, _ := resp.Stats["redirectStats"].(map[string]interface{})
			if chains, _ := rs["chains"].([]interface{}); len(chains) != 200 {
				t.Errorf("response chains = %d, want capped 200", len(chains))
			}
			if total, _ := rs["totalChains"].(float64); total != 500 {
				t.Errorf("totalChains = %v, want 500", total)
			}
		}()
	}
	wg.Wait()

	// The cached stats must keep their full lists.
	res := m.results["race"]
	res.mu.RLock()
	defer res.mu.RUnlock()
	if n := len(res.Stats["redirectStats"].(map[string]interface{})["chains"].([]interface{})); n != 500 {
		t.Errorf("cached redirect chains truncated to %d", n)
	}
	if n := len(res.Stats["urlIssueStats"].(map[string]interface{})["uppercase"].([]interface{})); n != 300 {
		t.Errorf("cached urlIssueStats truncated to %d", n)
	}
	if _, ok := res.Stats["redirectStats"].(map[string]interface{})["totalChains"]; ok {
		t.Error("cached stats were mutated (totalChains added)")
	}
}
