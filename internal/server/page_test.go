package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

type pageResponse struct {
	Page        *types.PageData `json:"page"`
	Outlinks    []string        `json:"outlinks"`
	InlinkCount int             `json:"inlinkCount"`
}

func getPage(t *testing.T, api http.Handler, crawlID, pageURL string) (int, pageResponse) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/crawl/"+crawlID+"/page?url="+url.QueryEscape(pageURL), nil)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	var got pageResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec.Code, got
}

// GET /api/crawl/{id}/page returns one page and its internal outlinks, from
// the page itself or, once links were stripped, from the link graph.
func TestPageEndpoint(t *testing.T) {
	api, _, m := newTestAPI(t)

	withLinks := linkedPages()
	stripped := linkedPages()
	iter := diskIter(stripped)
	stripLinks(stripped)
	g := analysis.BuildAdjacencyListFromDisk(stripped, iter)
	ix := newInlinkIndex(g, stripped)
	assignInlinks(stripped, ix)

	m.mu.Lock()
	m.cacheResultLocked("links", &CrawlResults{Pages: withLinks, PagesReady: true}, len(withLinks))
	m.cacheResultLocked("graph", &CrawlResults{Pages: stripped, Graph: g, InlinkIndex: ix, PagesReady: true}, len(stripped))
	m.mu.Unlock()

	for _, id := range []string{"links", "graph"} {
		code, got := getPage(t, api, id, "https://ex.com/")
		if code != http.StatusOK || got.Page == nil {
			t.Fatalf("%s: status %d, page %v", id, code, got.Page)
		}
		if got.Page.URL != "https://ex.com/" {
			t.Errorf("%s: url = %q", id, got.Page.URL)
		}
		want := []string{"https://ex.com/cat/", "https://ex.com/cat/p1", "https://ex.com/cat/p2"}
		for _, w := range want {
			if !slices.Contains(got.Outlinks, w) {
				t.Errorf("%s: outlinks %v missing %s", id, got.Outlinks, w)
			}
		}
		if got.Page.InternalLinks != nil {
			t.Errorf("%s: page should not repeat internalLinks", id)
		}
	}

	// The same URL variants as /page-inlinks: no trailing slash finds /cat/.
	code, got := getPage(t, api, "graph", "https://ex.com/cat")
	if code != http.StatusOK || got.Page == nil || got.Page.URL != "https://ex.com/cat/" {
		t.Fatalf("variant lookup: status %d, page %+v", code, got.Page)
	}
	if got.InlinkCount != 2 {
		t.Errorf("inlinkCount(/cat/) = %d, want 2", got.InlinkCount)
	}

	if code, _ := getPage(t, api, "graph", "https://ex.com/nope"); code != http.StatusNotFound {
		t.Errorf("unknown url: status %d, want 404", code)
	}
	if code, _ := getPage(t, api, "missing-crawl", "https://ex.com/"); code != http.StatusNotFound {
		t.Errorf("unknown crawl: status %d, want 404", code)
	}
	req := httptest.NewRequest("GET", "/api/crawl/graph/page", nil)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no url: status %d, want 400", rec.Code)
	}
}
