package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func makePages() []*types.PageData {
	return []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200, ResponseTimeMs: 100, Depth: 0, WordCount: 500, Inlinks: 10, PageRank: 5.0,
			Title: &types.TextLength{Text: "Home", Length: 4}, MetaDescription: &types.TextLength{Text: "Welcome home", Length: 12},
			Headings: types.HeadingData{H1: []string{"Welcome"}}, Indexability: types.IndexabilityData{Indexable: true},
			TemplateType: "page", InternalLinks: []string{"https://example.com/about", "https://example.com/blog"},
			Anchors: []types.AnchorData{{Text: "About", IsInternal: true}, {Text: "Blog", IsInternal: true}},
		},
		{URL: "https://example.com/about", StatusCode: 200, ResponseTimeMs: 200, Depth: 1, WordCount: 300, Inlinks: 5, PageRank: 2.0,
			Title: &types.TextLength{Text: "About Us", Length: 8}, MetaDescription: &types.TextLength{Text: "About our company", Length: 17},
			Headings: types.HeadingData{H1: []string{"About Us"}}, Indexability: types.IndexabilityData{Indexable: true},
			TemplateType: "page", InternalLinks: []string{"https://example.com/"},
			Anchors: []types.AnchorData{{Text: "Home", IsInternal: true}, {Text: "Twitter", IsInternal: false}},
		},
		{URL: "https://example.com/blog", StatusCode: 200, ResponseTimeMs: 300, Depth: 1, WordCount: 800, Inlinks: 3, PageRank: 1.0,
			Headings: types.HeadingData{H1: []string{"Blog", "Latest Posts"}}, Indexability: types.IndexabilityData{Indexable: true},
			TemplateType: "article", InternalLinks: []string{"https://example.com/"},
		},
		{URL: "https://example.com/old", StatusCode: 500, ResponseTimeMs: 50, Depth: 2, WordCount: 0, Inlinks: 0,
			Title: &types.TextLength{Text: "Server Error", Length: 12}, Indexability: types.IndexabilityData{Indexable: false},
			TemplateType: "page",
		},
		{URL: "https://example.com/deep/nested/path", StatusCode: 301, ResponseTimeMs: 10, Depth: 5, WordCount: 0, Inlinks: 1,
			Indexability: types.IndexabilityData{Indexable: false}, TemplateType: "other",
		},
	}
}

func TestFilterAndSortPages_NoFilter(t *testing.T) {
	pages := makePages()
	result := filterAndSortPages(pages, "", "", "", "", "", "", "", "", "asc")
	if len(result) != len(pages) {
		t.Errorf("expected %d pages, got %d", len(pages), len(result))
	}
}

func TestFilterAndSortPages_StatusFilter(t *testing.T) {
	pages := makePages()
	result := filterAndSortPages(pages, "", "2xx", "", "", "", "", "", "", "asc")
	for _, p := range result {
		if p.StatusCode < 200 || p.StatusCode >= 300 {
			t.Errorf("unexpected status code %d for 2xx filter", p.StatusCode)
		}
	}
	if len(result) != 3 {
		t.Errorf("expected 3 2xx pages, got %d", len(result))
	}
}

func TestFilterAndSortPages_UnknownStatus(t *testing.T) {
	pages := makePages()
	result := filterAndSortPages(pages, "", "1xx", "", "", "", "", "", "", "asc")
	if len(result) != 0 {
		t.Errorf("expected 0 pages for unknown status filter, got %d", len(result))
	}
}

func TestFilterAndSortPages_Search(t *testing.T) {
	pages := makePages()
	result := filterAndSortPages(pages, "blog", "", "", "", "", "", "", "", "asc")
	if len(result) != 1 {
		t.Errorf("expected 1 page matching 'blog', got %d", len(result))
	}
	if result[0].URL != "https://example.com/blog" {
		t.Errorf("expected blog page, got %s", result[0].URL)
	}
}

func TestFilterAndSortPages_IssueFilter(t *testing.T) {
	pages := makePages()

	// 5xx filter
	result := filterAndSortPages(pages, "", "", "", "", "", "5xx", "", "", "asc")
	if len(result) != 1 {
		t.Errorf("expected 1 5xx page, got %d", len(result))
	}

	// missing-title
	result = filterAndSortPages(pages, "", "", "", "", "", "missing-title", "", "", "asc")
	count := 0
	for _, p := range pages {
		if p.Title == nil {
			count++
		}
	}
	if len(result) != count {
		t.Errorf("expected %d missing-title pages, got %d", count, len(result))
	}

	// multiple-h1
	result = filterAndSortPages(pages, "", "", "", "", "", "multiple-h1", "", "", "asc")
	if len(result) != 1 {
		t.Errorf("expected 1 multiple-h1 page, got %d", len(result))
	}

	// deep
	result = filterAndSortPages(pages, "", "", "", "", "", "deep", "", "", "asc")
	if len(result) != 1 {
		t.Errorf("expected 1 deep page, got %d", len(result))
	}

	// unknown issue matches nothing
	result = filterAndSortPages(pages, "", "", "", "", "", "nonexistent", "", "", "asc")
	if len(result) != 0 {
		t.Errorf("expected 0 for unknown issue, got %d", len(result))
	}
}

func TestFilterAndSortPages_Sort(t *testing.T) {
	pages := makePages()

	// Sort by status code desc
	result := filterAndSortPages(pages, "", "", "", "", "", "", "", "statusCode", "desc")
	if len(result) != 5 {
		t.Fatalf("expected 5, got %d", len(result))
	}
	if result[0].StatusCode != 500 {
		t.Errorf("expected 500 first, got %d", result[0].StatusCode)
	}

	// Sort by depth asc
	result = filterAndSortPages(pages, "", "", "", "", "", "", "", "depth", "asc")
	if result[0].Depth != 0 {
		t.Errorf("expected depth 0 first, got %d", result[0].Depth)
	}
}

func TestCollectTemplateTypes(t *testing.T) {
	pages := makePages()
	types := collectTemplateTypes(pages)
	if len(types) < 2 {
		t.Errorf("expected at least 2 template types, got %d", len(types))
	}
}

func TestBuildAnchorStats(t *testing.T) {
	pages := makePages()
	stats := buildAnchorStats(pages)
	if _, ok := stats["anchors"]; !ok {
		t.Fatal("missing anchors in result")
	}
	total, ok := stats["totalUnique"].(int)
	if !ok || total == 0 {
		t.Error("expected non-zero totalUnique")
	}
}

func TestBuildDirectoryTree(t *testing.T) {
	pages := makePages()
	tree := buildDirectoryTree(pages)
	root, ok := tree["tree"]
	if !ok {
		t.Fatal("missing tree in result")
	}
	node := root.(*dirNode)
	if node.Pages != len(pages) {
		t.Errorf("expected root pages=%d, got %d", len(pages), node.Pages)
	}
}

func TestBuildInlinkIndex(t *testing.T) {
	pages := makePages()
	idx := buildInlinkIndex(pages)

	// Home links to about and blog
	sources := idx.Sources("https://example.com/about")
	if len(sources) == 0 {
		t.Error("expected inlinks for /about")
	}
	found := false
	for _, s := range sources {
		if s == "https://example.com/" {
			found = true
		}
	}
	if !found {
		t.Error("expected home page as source for /about inlinks")
	}
}

func TestInlinkVariants(t *testing.T) {
	v := inlinkVariants("https://example.com/about")
	if len(v) != 2 {
		t.Errorf("expected 2 variants, got %d", len(v))
	}
	v = inlinkVariants("https://example.com/about/")
	if len(v) != 2 {
		t.Errorf("expected 2 variants, got %d", len(v))
	}
}

func TestPageMatchesIssue_DefaultFalse(t *testing.T) {
	p := &types.PageData{URL: "https://example.com/"}
	if pageMatchesIssue(p, "unknown-issue-type", nil) {
		t.Error("expected unknown issue type to return false")
	}
}

func TestBuildDirectoryTreeIsCapped(t *testing.T) {
	var pages []*types.PageData
	for d := 0; d < 80; d++ {
		for i := 0; i < 200; i++ {
			pages = append(pages, &types.PageData{URL: fmt.Sprintf("https://ex.com/d%d/p%d", d, i), StatusCode: 200, Inlinks: 1})
		}
	}
	root := buildDirectoryTree(pages)["tree"].(*dirNode)
	count := 0
	var walk func(n *dirNode)
	walk = func(n *dirNode) {
		count++
		if len(n.Children) > maxTreeChildren {
			t.Errorf("%s has %d children", n.Path, len(n.Children))
		}
		sum := 0
		for _, c := range n.Children {
			sum += c.Pages
			walk(c)
		}
		if len(n.Children) > 0 && sum != n.Pages {
			t.Errorf("%s children pages %d != %d", n.Path, sum, n.Pages)
		}
	}
	walk(root)
	if count > maxTreeNodes {
		t.Errorf("tree has %d nodes, cap %d", count, maxTreeNodes)
	}
	if root.Pages != len(pages) || root.TotalInlinks != len(pages) {
		t.Errorf("root pages=%d inlinks=%d", root.Pages, root.TotalInlinks)
	}
}

func TestLazyDirectoryTree(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := []*types.PageData{{URL: "https://ex.com/", StatusCode: 200}, {URL: "https://ex.com/blog", StatusCode: 200, PageRank: 1}}
	for i := 0; i < 150; i++ {
		pages = append(pages, &types.PageData{URL: fmt.Sprintf("https://ex.com/blog/p%d", i), StatusCode: 200, PageRank: float64(i)})
	}
	pages = append(pages, &types.PageData{URL: "https://ex.com/a/b/c", StatusCode: 404})
	m.mu.Lock()
	m.results["dt"] = &CrawlResults{Pages: pages, PagesReady: true}
	m.mu.Unlock()

	type node struct {
		Name        string `json:"name"`
		Pages       int    `json:"pages"`
		IsPage      bool   `json:"isPage"`
		HasChildren bool   `json:"hasChildren"`
		ChildCount  int    `json:"childCount"`
		Children    []node `json:"children"`
	}
	get := func(q string) node {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/dt/directory-tree?lazy=1&"+q, nil))
		var body struct {
			Lazy bool `json:"lazy"`
			Tree node `json:"tree"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || !body.Lazy {
			t.Fatalf("%s: lazy=%v err=%v", q, body.Lazy, err)
		}
		return body.Tree
	}
	root := get("depth=2")
	if root.Pages != len(pages) || root.ChildCount != 2 || !root.IsPage {
		t.Fatalf("root = %+v", root)
	}
	blog := root.Children[0]
	if blog.Name != "blog" || !blog.IsPage || blog.Pages != 151 || blog.ChildCount != 150 || len(blog.Children) != dirTreeL2Size {
		t.Errorf("blog = %s pages=%d count=%d loaded=%d", blog.Name, blog.Pages, blog.ChildCount, len(blog.Children))
	}
	if blog.Children[0].Name != "p149" { // highest PageRank first
		t.Errorf("first blog page = %s", blog.Children[0].Name)
	}
	more := get("path=/blog&offset=100&limit=100")
	if len(more.Children) != 50 {
		t.Errorf("page 2 of /blog has %d children, want 50", len(more.Children))
	}
	ab := get("path=/a/b")
	if ab.Pages != 1 || len(ab.Children) != 1 || ab.Children[0].Name != "c" || ab.Children[0].HasChildren {
		t.Errorf("/a/b = %+v", ab)
	}
}

// Cached pages lose GSC data when stripped; the lazy tree takes it from the
// full tree built before stripping and flags what that tree does not cover.
func TestLazyDirectoryTreeKeepsTraffic(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := []*types.PageData{
		{URL: "https://ex.com/a/x", StatusCode: 200, GscData: &types.GscData{Clicks: 7}},
		{URL: "https://ex.com/a/y", StatusCode: 200, GscData: &types.GscData{Clicks: 3}},
		{URL: "https://ex.com/a/y?v=2", StatusCode: 200},
	}
	tree := buildDirectoryTree(pages)
	for _, p := range pages {
		p.GscData = nil // what stripPagesForCache does
	}
	m.mu.Lock()
	m.results["tr"] = &CrawlResults{Pages: pages, PagesReady: true, DirTree: tree}
	m.mu.Unlock()

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/tr/directory-tree?lazy=1&path=/a", nil))
	var body struct {
		Tree struct {
			GscClicks int `json:"gscClicks"`
			Children  []struct {
				Name           string `json:"name"`
				GscClicks      int    `json:"gscClicks"`
				TrafficMissing bool   `json:"trafficMissing"`
			} `json:"children"`
		} `json:"tree"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Tree.GscClicks != 10 {
		t.Errorf("/a clicks = %d, want 10", body.Tree.GscClicks)
	}
	got := map[string]int{}
	for _, c := range body.Tree.Children {
		if c.TrafficMissing {
			got[c.Name] = -1
		} else {
			got[c.Name] = c.GscClicks
		}
	}
	if got["x"] != 7 || got["y"] != -1 || got["y?v=2"] != -1 {
		t.Errorf("children traffic = %v", got)
	}
}

// The index is rebuilt when pages are stripped in place after it was built,
// and encoded paths match the full tree so traffic lookups still work.
func TestLazyDirectoryTreeRebuildsAfterStrip(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := []*types.PageData{
		{URL: "https://ex.com/caf%C3%A9/x", StatusCode: 200, GscData: &types.GscData{Clicks: 4}},
		{URL: "https://ex.com/caf%C3%A9/y", StatusCode: 200, GscData: &types.GscData{Clicks: 1}},
	}
	res := &CrawlResults{Pages: pages, PagesReady: true, DirTree: buildDirectoryTree(pages)}
	m.mu.Lock()
	m.results["st"] = res
	m.mu.Unlock()
	clicks := func() map[string]string {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/crawl/st/directory-tree?lazy=1&path="+url.QueryEscape("/café"), nil))
		var body struct {
			Tree struct {
				Children []struct {
					Name           string `json:"name"`
					GscClicks      int    `json:"gscClicks"`
					TrafficMissing bool   `json:"trafficMissing"`
				} `json:"children"`
			} `json:"tree"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, c := range body.Tree.Children {
			out[c.Name] = fmt.Sprint(c.GscClicks, c.TrafficMissing)
		}
		return out
	}
	if got := clicks(); got["x"] != "4 false" {
		t.Fatalf("before strip: %v", got)
	}
	for _, p := range pages {
		p.GscData = nil
	}
	if got := clicks(); got["x"] != "4 false" || got["y"] != "1 false" {
		t.Errorf("after strip: %v", got)
	}
}
