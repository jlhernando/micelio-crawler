package analysis

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestGenerateReportBasicStats(t *testing.T) {
	pages := []*types.PageData{
		{
			URL:        "https://example.com/",
			StatusCode: 200,
			Title:      &types.TextLength{Text: "Home", Length: 4},
			MetaDescription: &types.TextLength{Text: "Welcome", Length: 7},
			Headings:   types.HeadingData{H1: []string{"Welcome"}},
			WordCount:  500,
			Depth:      0,
			ResponseTimeMs: 150,
			InternalLinks:  []string{"https://example.com/about"},
			Indexability:   types.IndexabilityData{Indexable: true},
		},
		{
			URL:        "https://example.com/about",
			StatusCode: 200,
			Title:      &types.TextLength{Text: "About", Length: 5},
			Headings:   types.HeadingData{H1: []string{"About Us"}},
			WordCount:  300,
			Depth:      1,
			ResponseTimeMs: 200,
			Indexability:   types.IndexabilityData{Indexable: true},
		},
		{
			URL:        "https://example.com/missing",
			StatusCode: 404,
			Depth:      1,
		},
	}

	stats := GenerateReport(pages, 5000, ReportConfig{SeedURL: "https://example.com/"})

	if stats.TotalPages != 3 {
		t.Errorf("TotalPages = %d, want 3", stats.TotalPages)
	}
	if stats.StatusCodes[200] != 2 {
		t.Errorf("StatusCodes[200] = %d, want 2", stats.StatusCodes[200])
	}
	if stats.StatusCodes[404] != 1 {
		t.Errorf("StatusCodes[404] = %d, want 1", stats.StatusCodes[404])
	}
	if len(stats.BrokenLinks) != 1 {
		t.Errorf("BrokenLinks = %d, want 1", len(stats.BrokenLinks))
	}
	if stats.CrawlDurationMs != 5000 {
		t.Errorf("CrawlDurationMs = %d, want 5000", stats.CrawlDurationMs)
	}
	if stats.PagesWithoutDescription != 2 {
		t.Errorf("PagesWithoutDescription = %d, want 2", stats.PagesWithoutDescription)
	}
}

func TestGenerateReportIndexability(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://example.com/noindex", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: false, Reason: "noindex"}},
		{URL: "https://example.com/canonical", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: false, Reason: "canonicalized"}},
	}
	stats := GenerateReport(pages, 1000, ReportConfig{})
	if stats.IndexabilityStats.Indexable != 1 {
		t.Errorf("Indexable = %d, want 1", stats.IndexabilityStats.Indexable)
	}
	if stats.IndexabilityStats.NonIndexable != 2 {
		t.Errorf("NonIndexable = %d, want 2", stats.IndexabilityStats.NonIndexable)
	}
	if stats.IndexabilityStats.Reasons["noindex"] != 1 {
		t.Errorf("Reasons[noindex] = %d, want 1", stats.IndexabilityStats.Reasons["noindex"])
	}
}

func TestGenerateReportOrphanPages(t *testing.T) {
	pages := []*types.PageData{
		{
			URL:           "https://example.com/",
			StatusCode:    200,
			Depth:         0,
			InternalLinks: []string{"https://example.com/linked"},
			Indexability:  types.IndexabilityData{Indexable: true},
		},
		{
			URL:          "https://example.com/linked",
			StatusCode:   200,
			Depth:        1,
			Indexability:  types.IndexabilityData{Indexable: true},
		},
		{
			URL:          "https://example.com/orphan",
			StatusCode:   200,
			Depth:        2,
			Indexability:  types.IndexabilityData{Indexable: true},
		},
	}
	stats := GenerateReport(pages, 1000, ReportConfig{})
	if len(stats.OrphanPages) != 1 {
		t.Errorf("OrphanPages = %d, want 1", len(stats.OrphanPages))
	}
	if len(stats.OrphanPages) > 0 && stats.OrphanPages[0] != "https://example.com/orphan" {
		t.Errorf("OrphanPages[0] = %q, want orphan URL", stats.OrphanPages[0])
	}
}

// Mirrors the production crawl path: pages have InternalLinks=nil (stripped after
// streaming to disk) and edges are supplied via InternalLinksIter. Without the
// iterator wired into orphan detection, every non-seed page would be flagged.
func TestGenerateReportOrphanPagesWithIterator(t *testing.T) {
	pages := []*types.PageData{
		{
			URL:               "https://example.com/",
			StatusCode:        200,
			Depth:             0,
			InternalLinkCount: 2,
			Indexability:      types.IndexabilityData{Indexable: true},
		},
		{
			URL:               "https://example.com/a",
			StatusCode:        200,
			Depth:             1,
			InternalLinkCount: 1,
			Indexability:      types.IndexabilityData{Indexable: true},
		},
		{
			URL:               "https://example.com/b",
			StatusCode:        200,
			Depth:             1,
			InternalLinkCount: 0,
			Indexability:      types.IndexabilityData{Indexable: true},
		},
		{
			URL:               "https://example.com/orphan",
			StatusCode:        200,
			Depth:             2,
			InternalLinkCount: 0,
			Indexability:      types.IndexabilityData{Indexable: true},
		},
	}
	edges := map[string][]string{
		"https://example.com/":  {"https://example.com/a", "https://example.com/b"},
		"https://example.com/a": {"https://example.com/b"},
	}
	iter := func(fn func(source string, targets []string)) error {
		for src, tgts := range edges {
			fn(src, tgts)
		}
		return nil
	}
	stats := GenerateReport(pages, 1000, ReportConfig{
		SeedURL:           "https://example.com/",
		InternalLinksIter: iter,
	})
	if len(stats.OrphanPages) != 1 {
		t.Fatalf("OrphanPages = %d (%v), want 1", len(stats.OrphanPages), stats.OrphanPages)
	}
	if stats.OrphanPages[0] != "https://example.com/orphan" {
		t.Errorf("OrphanPages[0] = %q, want orphan URL", stats.OrphanPages[0])
	}
}

func TestGenerateReportPageRank(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", StatusCode: 200, InternalLinks: []string{"https://example.com/b"}},
		{URL: "https://example.com/b", StatusCode: 200, InternalLinks: []string{"https://example.com/a"}},
	}
	stats := GenerateReport(pages, 1000, ReportConfig{})
	if len(stats.PageRankScores) != 2 {
		t.Errorf("PageRankScores has %d entries, want 2", len(stats.PageRankScores))
	}
}

func TestPercentile(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p50 := percentile(data, 50)
	if p50 < 5 || p50 > 6 {
		t.Errorf("P50 = %f, want ~5.5", p50)
	}
	p99 := percentile(data, 99)
	if p99 < 9 {
		t.Errorf("P99 = %f, want ~10", p99)
	}
}

func TestClassifyURLs(t *testing.T) {
	pages := []*types.PageData{
		// Active: has GSC clicks
		{URL: "https://example.com/", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true},
			GscData: &types.GscData{Impressions: 100, Clicks: 10}},
		// Visible: has impressions but no clicks/sessions
		{URL: "https://example.com/visible", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true},
			GscData: &types.GscData{Impressions: 50, Clicks: 0}},
		// Indexable: no analytics data
		{URL: "https://example.com/indexable", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true}},
		// Non-indexable
		{URL: "https://example.com/noindex", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: false, Reason: "noindex"}},
		// Active via GA4
		{URL: "https://example.com/ga4-active", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true},
			Ga4Data: &types.Ga4Data{Sessions: 5}},
		// Robots blocked
		{URL: "https://example.com/blocked", StatusCode: 200, RobotsBlocked: true},
		// Active via Plausible
		{URL: "https://example.com/plausible-active", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true},
			PlausibleData: &types.PlausibleData{Visitors: 3}},
	}

	ClassifyURLs(pages)

	checks := []struct {
		idx  int
		want string
	}{
		{0, "active"},
		{1, "visible"},
		{2, "indexable"},
		{3, "non-indexable"},
		{4, "active"},
		{5, "robots-blocked"},
		{6, "active"},
	}
	for _, c := range checks {
		if pages[c.idx].URLClassification != c.want {
			t.Errorf("page[%d] (%s) classification = %q, want %q", c.idx, pages[c.idx].URL, pages[c.idx].URLClassification, c.want)
		}
	}
}

func TestBuildSeoFunnelStats(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true},
			TemplateType: "homepage",
			GscData:      &types.GscData{Impressions: 100, Clicks: 10}},
		{URL: "https://example.com/visible", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true},
			TemplateType: "article",
			GscData:      &types.GscData{Impressions: 50, Clicks: 0}},
		{URL: "https://example.com/indexable", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true},
			TemplateType: "article"},
		{URL: "https://example.com/noindex", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: false, Reason: "noindex"}},
		{URL: "https://example.com/ga4-active", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true},
			TemplateType: "article",
			Ga4Data:      &types.Ga4Data{Sessions: 5}},
		{URL: "https://example.com/error", StatusCode: 500, Indexability: types.IndexabilityData{Indexable: false, Reason: "5xx"}},
		{URL: "https://example.com/blocked", StatusCode: 200, RobotsBlocked: true},
	}

	ClassifyURLs(pages)
	fs := buildSeoFunnelStats(pages)
	if fs == nil {
		t.Fatal("expected non-nil SeoFunnelStats")
	}
	// 7 pages, 1 robots-blocked → 6 crawled
	if fs.Crawled != 6 {
		t.Errorf("Crawled = %d, want 6", fs.Crawled)
	}
	// 5 pages are 200 (excluding /error 500 and /blocked robots)
	if fs.Renderable != 5 {
		t.Errorf("Renderable = %d, want 5", fs.Renderable)
	}
	if fs.Indexable != 4 {
		t.Errorf("Indexable = %d, want 4", fs.Indexable)
	}
	if fs.Active != 2 {
		t.Errorf("Active = %d, want 2", fs.Active)
	}
	if fs.Visible != 1 {
		t.Errorf("Visible = %d, want 1", fs.Visible)
	}
	// noindex + 500 error = 2 non-indexable
	if fs.NonIndexable != 2 {
		t.Errorf("NonIndexable = %d, want 2", fs.NonIndexable)
	}
	// Segments: homepage(1), article(3), other(2)
	if fs.Segments == nil {
		t.Fatal("expected non-nil Segments")
	}
	if len(fs.Segments) != 3 {
		t.Errorf("Segments count = %d, want 3", len(fs.Segments))
	}
	if seg, ok := fs.Segments["article"]; ok {
		if seg.Crawled != 3 {
			t.Errorf("article segment Crawled = %d, want 3", seg.Crawled)
		}
		if seg.Active != 1 {
			t.Errorf("article segment Active = %d, want 1", seg.Active)
		}
	} else {
		t.Error("expected 'article' segment")
	}
}

func TestBuildSeoFunnelStatsNoPages(t *testing.T) {
	pages := []*types.PageData{}
	ClassifyURLs(pages)
	fs := buildSeoFunnelStats(pages)
	if fs != nil {
		t.Errorf("expected nil for empty pages, got %+v", fs)
	}
}

func TestGenerateReportSEOIssueStats(t *testing.T) {
	pages := []*types.PageData{
		// Duplicate title "Home" x2
		{URL: "https://example.com/a", StatusCode: 200,
			Title:           &types.TextLength{Text: "Home", Length: 4},
			MetaDescription: &types.TextLength{Text: "Desc A", Length: 6},
			Headings:        types.HeadingData{H1: []string{"One"}},
		},
		{URL: "https://example.com/b", StatusCode: 200,
			Title:           &types.TextLength{Text: "Home", Length: 4},
			MetaDescription: &types.TextLength{Text: "Desc A", Length: 6},
			Headings:        types.HeadingData{H1: []string{"One", "Two"}},
		},
		// Title too long (>60)
		{URL: "https://example.com/c", StatusCode: 200,
			Title:    &types.TextLength{Text: "This is a very long title that exceeds sixty characters easily", Length: 62},
			Headings: types.HeadingData{H1: []string{"Ok"}},
		},
		// Title too short (<30)
		{URL: "https://example.com/d", StatusCode: 200,
			Title:    &types.TextLength{Text: "Hi", Length: 2},
			Headings: types.HeadingData{H1: []string{"Ok"}},
		},
		// Description too long (>160)
		{URL: "https://example.com/e", StatusCode: 200,
			MetaDescription: &types.TextLength{Text: "long desc", Length: 161},
			Headings:        types.HeadingData{H1: []string{"Ok"}},
		},
		// Robots-blocked: should be excluded from counts
		{URL: "https://example.com/blocked", StatusCode: 200,
			RobotsBlocked: true,
			Title:         &types.TextLength{Text: "Home", Length: 4},
			Headings:      types.HeadingData{H1: []string{"One", "Two"}},
		},
	}

	stats := GenerateReport(pages, 1000, ReportConfig{})

	if stats.DuplicateTitleCount != 2 {
		t.Errorf("DuplicateTitleCount = %d, want 2", stats.DuplicateTitleCount)
	}
	if stats.DuplicateDescriptionCount != 2 {
		t.Errorf("DuplicateDescriptionCount = %d, want 2", stats.DuplicateDescriptionCount)
	}
	if stats.MultipleH1Count != 1 {
		t.Errorf("MultipleH1Count = %d, want 1", stats.MultipleH1Count)
	}
	if stats.TitleTooLongCount != 1 {
		t.Errorf("TitleTooLongCount = %d, want 1", stats.TitleTooLongCount)
	}
	if stats.TitleTooShortCount != 3 {
		t.Errorf("TitleTooShortCount = %d, want 3", stats.TitleTooShortCount)
	}
	if stats.DescriptionTooLongCount != 1 {
		t.Errorf("DescriptionTooLongCount = %d, want 1", stats.DescriptionTooLongCount)
	}
}

func TestBuildAIVisibilityStats(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200, AIVisibilityData: &types.AIVisibilityData{
			InAIOverview: true, AIImpressions: 1000, AIClicks: 50, AICTR: 0.05,
			Queries: []types.AIOverviewQuery{
				{Query: "example query", Impressions: 500, Clicks: 25, CTR: 0.05, Position: 3.2},
				{Query: "another query", Impressions: 500, Clicks: 25, CTR: 0.05, Position: 4.1},
			},
		}},
		{URL: "https://example.com/about", StatusCode: 200, AIVisibilityData: &types.AIVisibilityData{
			InAIOverview: true, AIImpressions: 200, AIClicks: 10, AICTR: 0.05,
			Queries: []types.AIOverviewQuery{
				{Query: "example query", Impressions: 200, Clicks: 10, CTR: 0.05, Position: 5.0},
			},
		}},
		// No AI data
		{URL: "https://example.com/other", StatusCode: 200},
	}

	stats := GenerateReport(pages, 1000, ReportConfig{})
	if stats.AIVisibilityStats == nil {
		t.Fatal("expected non-nil AIVisibilityStats")
	}
	if stats.AIVisibilityStats.PagesInAIOverview != 2 {
		t.Errorf("PagesInAIOverview = %d, want 2", stats.AIVisibilityStats.PagesInAIOverview)
	}
	if stats.AIVisibilityStats.TotalAIImpressions != 1200 {
		t.Errorf("TotalAIImpressions = %d, want 1200", stats.AIVisibilityStats.TotalAIImpressions)
	}
	if stats.AIVisibilityStats.TotalAIClicks != 60 {
		t.Errorf("TotalAIClicks = %d, want 60", stats.AIVisibilityStats.TotalAIClicks)
	}
	// "example query" appears in both pages, should be merged
	if len(stats.AIVisibilityStats.TopQueries) != 2 {
		t.Errorf("TopQueries = %d, want 2", len(stats.AIVisibilityStats.TopQueries))
	}
}

func TestBuildAIVisibilityStatsNil(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200},
	}
	stats := GenerateReport(pages, 1000, ReportConfig{})
	if stats.AIVisibilityStats != nil {
		t.Errorf("expected nil AIVisibilityStats when no AI data, got %+v", stats.AIVisibilityStats)
	}
}

func TestGenerateReportDuplicateContent(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://example.com/a", StatusCode: 200, ContentHash: "abc123"},
		{URL: "https://example.com/b", StatusCode: 200, ContentHash: "abc123"},
		{URL: "https://example.com/c", StatusCode: 200, ContentHash: "def456"},
	}
	stats := GenerateReport(pages, 1000, ReportConfig{})
	if len(stats.DuplicateContentGroups) != 1 {
		t.Errorf("DuplicateContentGroups = %d, want 1", len(stats.DuplicateContentGroups))
	}
	if len(stats.DuplicateContentGroups) > 0 && len(stats.DuplicateContentGroups[0].URLs) != 2 {
		t.Errorf("Group size = %d, want 2", len(stats.DuplicateContentGroups[0].URLs))
	}
}
