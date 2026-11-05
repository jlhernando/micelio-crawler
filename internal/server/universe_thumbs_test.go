package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

func TestUniverseThumbRefusesNonWebURLs(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := linkedPages()
	for _, p := range pages {
		p.AutoSegments = &types.PageSegments{Dir1: "/"}
	}
	iter := diskIter(pages)
	stripLinks(pages)
	g := analysis.BuildAdjacencyListFromDisk(pages, iter)
	for _, p := range pages {
		p.URL = "file:///etc/passwd"
	}
	m.mu.Lock()
	m.results["fx"] = &CrawlResults{Pages: pages, Graph: g, PagesReady: true}
	m.mu.Unlock()
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/fx/universe/thumb?i=0", nil))
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") == "image/jpeg" {
		t.Errorf("file:// thumbnail: status %d", rec.Code)
	}
	for _, u := range []string{"file:///etc/passwd", "chrome://settings", "data:text/html,x", "https:///nohost", "javascript:alert(1)"} {
		if isWebURL(u) {
			t.Errorf("isWebURL(%q) = true", u)
		}
	}
	if !isWebURL("HTTPS://example.com/a") {
		t.Error("https URL refused")
	}
}
