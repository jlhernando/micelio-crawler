package server

import (
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
	sources := idx["https://example.com/about"]
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
