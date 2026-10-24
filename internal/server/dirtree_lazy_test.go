package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// Before a crawl's pages are loaded, lazy requests are answered from the
// page_index table with the same shape and paths as the in-memory index.
func TestLazyDirectoryTreeFromDB(t *testing.T) {
	api, s, _ := newTestAPI(t)
	pages := []*types.PageData{
		{URL: "https://ex.com/", StatusCode: 200, Inlinks: 1, Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://ex.com/a/b/c", StatusCode: 404, Inlinks: 2},
	}
	for i := 0; i < 30; i++ {
		pages = append(pages, &types.PageData{URL: fmt.Sprintf("https://ex.com/blog/p%d", i), StatusCode: 200, Inlinks: 1,
			Indexability: types.IndexabilityData{Indexable: true}})
	}
	dbPath := filepath.Join(t.TempDir(), "crawl.db")
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.BuildPageIndex(pages); err != nil {
		t.Fatal(err)
	}
	cs.Close()
	job, err := s.CreateCrawlJobWithDB(map[string]interface{}{"url": "https://ex.com/"}, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	type node struct {
		Name        string      `json:"name"`
		Path        string      `json:"path"`
		Pages       int         `json:"pages"`
		IsPage      bool        `json:"isPage"`
		Indexable   int         `json:"indexable"`
		StatusCodes map[int]int `json:"statusCodes"`
		ChildCount  int         `json:"childCount"`
		Children    []node      `json:"children"`
	}
	get := func(q string) (node, int) {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/"+job.ID+"/directory-tree?lazy=1&"+q, nil))
		var body struct {
			Lazy       bool `json:"lazy"`
			Tree       node `json:"tree"`
			TotalPages int  `json:"totalPages"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || !body.Lazy {
			t.Fatalf("%s: code=%d lazy=%v err=%v", q, rec.Code, body.Lazy, err)
		}
		return body.Tree, body.TotalPages
	}
	root, total := get("depth=2&limit=5")
	if total != len(pages) || root.Pages != len(pages) || root.Indexable != 31 || root.ChildCount != 2 {
		t.Fatalf("root = %+v total=%d", root, total)
	}
	if root.Children[0].Name != "blog" || root.Children[0].ChildCount != 30 || len(root.Children[0].Children) != dirTreeL2Size {
		t.Errorf("blog = %+v", root.Children[0])
	}
	ab, _ := get("path=/a/b")
	if ab.Pages != 1 || ab.StatusCodes[404] != 1 || len(ab.Children) != 1 || ab.Children[0].Path != "/a/b/c" {
		t.Errorf("/a/b = %+v", ab)
	}
	more, _ := get("path=/blog&offset=25")
	if len(more.Children) != 5 {
		t.Errorf("/blog offset 25 has %d children, want 5", len(more.Children))
	}
}
