package report

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func makeTestStats(total, indexable, withoutTitle, withoutDesc, withoutH1 int) *types.CrawlStats {
	return &types.CrawlStats{
		TotalPages:              total,
		IndexabilityStats:       types.IndexabilityStats{Indexable: indexable, NonIndexable: total - indexable},
		StatusCodes:             map[int]int{200: indexable, 404: total - indexable},
		PagesWithoutTitle:       withoutTitle,
		PagesWithoutDescription: withoutDesc,
		PagesWithoutH1:          withoutH1,
		ResponseTimePercentiles: types.PercentileData{P50: 200, P90: 800, P99: 2000},
	}
}

func makeTestPage(url string, status int, title string, indexable bool) *types.PageData {
	p := &types.PageData{
		URL:        url,
		StatusCode: status,
		Indexability: types.IndexabilityData{
			Indexable: indexable,
		},
	}
	if title != "" {
		p.Title = &types.TextLength{Text: title, Length: len(title)}
	}
	return p
}

func TestComputeHealthScore(t *testing.T) {
	stats := makeTestStats(100, 95, 2, 5, 1)

	score := ComputeHealthScore(stats)
	if score <= 0 || score > 100 {
		t.Errorf("expected health score between 0 and 100, got %f", score)
	}

	// Perfect site should score high
	perfect := makeTestStats(100, 100, 0, 0, 0)
	perfect.StatusCodes = map[int]int{200: 100}
	perfectScore := ComputeHealthScore(perfect)
	if perfectScore < 70 {
		t.Errorf("expected perfect site to score > 70, got %f", perfectScore)
	}

	// Bad site should score low
	bad := makeTestStats(100, 10, 80, 90, 70)
	bad.StatusCodes = map[int]int{200: 10, 404: 60, 500: 30}
	badScore := ComputeHealthScore(bad)
	if badScore > 50 {
		t.Errorf("expected bad site to score < 50, got %f", badScore)
	}

	// Empty
	emptyScore := ComputeHealthScore(&types.CrawlStats{})
	if emptyScore != 0 {
		t.Errorf("expected 0 for empty stats, got %f", emptyScore)
	}
}

func TestComputeComparison(t *testing.T) {
	oldPages := []*types.PageData{
		makeTestPage("https://example.com/", 200, "Home", true),
		makeTestPage("https://example.com/about", 200, "About", true),
		makeTestPage("https://example.com/removed", 200, "Removed", true),
	}
	newPages := []*types.PageData{
		makeTestPage("https://example.com/", 200, "Home - Updated", true),
		makeTestPage("https://example.com/about", 200, "About", true),
		makeTestPage("https://example.com/new", 200, "New Page", true),
	}

	oldStats := makeTestStats(3, 3, 0, 0, 0)
	newStats := makeTestStats(3, 3, 0, 0, 0)

	report := ComputeComparison(oldPages, oldStats, newPages, newStats)

	if report == nil {
		t.Fatal("expected non-nil report")
	}

	// URLs section
	if report.URLs == nil {
		t.Fatal("expected URLs diff")
	}
	if len(report.URLs.AddedURLs) != 1 {
		t.Errorf("expected 1 added URL, got %d", len(report.URLs.AddedURLs))
	}
	if len(report.URLs.RemovedURLs) != 1 {
		t.Errorf("expected 1 removed URL, got %d", len(report.URLs.RemovedURLs))
	}
	if len(report.URLs.ChangedURLs) != 1 {
		t.Errorf("expected 1 changed URL, got %d", len(report.URLs.ChangedURLs))
	}

	// Coverage
	if report.Coverage.TotalPages.Old != 3 || report.Coverage.TotalPages.New != 3 {
		t.Errorf("expected 3→3 pages, got %d→%d", report.Coverage.TotalPages.Old, report.Coverage.TotalPages.New)
	}
	if report.Coverage.NewURLs != 1 {
		t.Errorf("expected 1 new URL, got %d", report.Coverage.NewURLs)
	}
	if report.Coverage.DisappearedURLs != 1 {
		t.Errorf("expected 1 disappeared URL, got %d", report.Coverage.DisappearedURLs)
	}

	// Health score should exist
	if report.HealthScore.OldScore == 0 && report.HealthScore.NewScore == 0 {
		t.Error("expected non-zero health scores")
	}
}

func TestStatusMigrations(t *testing.T) {
	oldPages := []*types.PageData{
		makeTestPage("https://example.com/ok", 200, "OK", true),
		makeTestPage("https://example.com/broke", 200, "Broke", true),
	}
	newPages := []*types.PageData{
		makeTestPage("https://example.com/ok", 200, "OK", true),
		makeTestPage("https://example.com/broke", 404, "", false),
	}

	oldStats := makeTestStats(2, 2, 0, 0, 0)
	newStats := makeTestStats(2, 1, 0, 0, 0)

	report := ComputeComparison(oldPages, oldStats, newPages, newStats)

	if len(report.HTTPStatus.Migrations) == 0 {
		t.Fatal("expected status migrations")
	}

	found := false
	for _, m := range report.HTTPStatus.Migrations {
		if m.From == "2xx" && m.To == "4xx" && m.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Error("expected migration from 2xx to 4xx")
	}

	// Should generate a critical finding
	hasCritical := false
	for _, f := range report.Findings {
		if f.Section == "httpStatus" && f.Severity == SeverityCritical {
			hasCritical = true
		}
	}
	if !hasCritical {
		t.Error("expected critical finding for 2xx→4xx migration")
	}
}

func TestIndexabilityChanges(t *testing.T) {
	oldPages := []*types.PageData{
		makeTestPage("https://example.com/a", 200, "A", true),
		makeTestPage("https://example.com/b", 200, "B", false),
	}
	newPages := []*types.PageData{
		makeTestPage("https://example.com/a", 200, "A", false), // became non-indexable
		makeTestPage("https://example.com/b", 200, "B", true),  // became indexable
	}

	oldStats := makeTestStats(2, 1, 0, 0, 0)
	newStats := makeTestStats(2, 1, 0, 0, 0)

	report := ComputeComparison(oldPages, oldStats, newPages, newStats)

	if len(report.Indexability.BecameNonIndexable) != 1 {
		t.Errorf("expected 1 page became non-indexable, got %d", len(report.Indexability.BecameNonIndexable))
	}
	if len(report.Indexability.BecameIndexable) != 1 {
		t.Errorf("expected 1 page became indexable, got %d", len(report.Indexability.BecameIndexable))
	}
}

func TestContentHashDetection(t *testing.T) {
	oldPages := []*types.PageData{
		{URL: "https://example.com/a", StatusCode: 200, ContentHash: "abc123",
			Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://example.com/b", StatusCode: 200, ContentHash: "def456",
			Indexability: types.IndexabilityData{Indexable: true}},
	}
	newPages := []*types.PageData{
		{URL: "https://example.com/a", StatusCode: 200, ContentHash: "abc123", // unchanged
			Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://example.com/b", StatusCode: 200, ContentHash: "ghi789", // changed
			Indexability: types.IndexabilityData{Indexable: true}},
	}

	oldStats := makeTestStats(2, 2, 0, 0, 0)
	newStats := makeTestStats(2, 2, 0, 0, 0)

	report := ComputeComparison(oldPages, oldStats, newPages, newStats)

	if report.Content.ModifiedPages != 1 {
		t.Errorf("expected 1 modified page, got %d", report.Content.ModifiedPages)
	}
	if report.Content.UnchangedPages != 1 {
		t.Errorf("expected 1 unchanged page, got %d", report.Content.UnchangedPages)
	}
}

func TestPageRankMovers(t *testing.T) {
	oldPages := []*types.PageData{
		makeTestPage("https://example.com/a", 200, "A", true),
		makeTestPage("https://example.com/b", 200, "B", true),
	}
	newPages := []*types.PageData{
		makeTestPage("https://example.com/a", 200, "A", true),
		makeTestPage("https://example.com/b", 200, "B", true),
	}

	oldStats := makeTestStats(2, 2, 0, 0, 0)
	oldStats.PageRankScores = map[string]float64{
		"https://example.com/a": 5.0,
		"https://example.com/b": 3.0,
	}

	newStats := makeTestStats(2, 2, 0, 0, 0)
	newStats.PageRankScores = map[string]float64{
		"https://example.com/a": 3.0, // dropped
		"https://example.com/b": 6.0, // rose
	}

	report := ComputeComparison(oldPages, oldStats, newPages, newStats)

	if len(report.Links.PageRankWinners) != 1 {
		t.Errorf("expected 1 winner, got %d", len(report.Links.PageRankWinners))
	}
	if len(report.Links.PageRankLosers) != 1 {
		t.Errorf("expected 1 loser, got %d", len(report.Links.PageRankLosers))
	}
}

func TestVisibilitySection(t *testing.T) {
	oldPages := []*types.PageData{
		{URL: "https://example.com/a", StatusCode: 200,
			Indexability: types.IndexabilityData{Indexable: true},
			GscData: &types.GscData{Clicks: 100, Impressions: 1000}},
	}
	newPages := []*types.PageData{
		{URL: "https://example.com/a", StatusCode: 200,
			Indexability: types.IndexabilityData{Indexable: true},
			GscData: &types.GscData{Clicks: 50, Impressions: 800}},
	}

	oldStats := makeTestStats(1, 1, 0, 0, 0)
	oldStats.GscStats = &types.GscStats{TotalClicks: 100, TotalImpressions: 1000, AvgCTR: 0.1}
	newStats := makeTestStats(1, 1, 0, 0, 0)
	newStats.GscStats = &types.GscStats{TotalClicks: 50, TotalImpressions: 800, AvgCTR: 0.0625}

	report := ComputeComparison(oldPages, oldStats, newPages, newStats)

	if report.Visibility == nil {
		t.Fatal("expected visibility section")
	}
	if report.Visibility.Clicks.Delta != -50 {
		t.Errorf("expected clicks delta -50, got %d", report.Visibility.Clicks.Delta)
	}
	if len(report.Visibility.Losers) != 1 {
		t.Errorf("expected 1 loser, got %d", len(report.Visibility.Losers))
	}
}

func TestNgramSection(t *testing.T) {
	oldStats := makeTestStats(10, 10, 0, 0, 0)
	oldStats.NgramStats = &types.NgramStats{
		Bigrams: []types.NgramEntry{
			{Term: "old topic", Count: 50, Pages: 10},
			{Term: "shared topic", Count: 30, Pages: 8},
		},
	}

	newStats := makeTestStats(10, 10, 0, 0, 0)
	newStats.NgramStats = &types.NgramStats{
		Bigrams: []types.NgramEntry{
			{Term: "new topic", Count: 40, Pages: 9},
			{Term: "shared topic", Count: 35, Pages: 8},
		},
	}

	report := ComputeComparison(nil, oldStats, nil, newStats)

	if report.Ngrams == nil {
		t.Fatal("expected ngrams section")
	}
	if len(report.Ngrams.EmergingTopics) != 1 {
		t.Errorf("expected 1 emerging topic, got %d", len(report.Ngrams.EmergingTopics))
	}
	if len(report.Ngrams.DecliningTopics) != 1 {
		t.Errorf("expected 1 declining topic, got %d", len(report.Ngrams.DecliningTopics))
	}
}

func TestFindingsSorting(t *testing.T) {
	oldPages := []*types.PageData{
		makeTestPage("https://example.com/broke1", 200, "B1", true),
		makeTestPage("https://example.com/broke2", 200, "B2", true),
		makeTestPage("https://example.com/broke3", 200, "B3", true),
	}
	newPages := []*types.PageData{
		makeTestPage("https://example.com/broke1", 500, "", false),
		makeTestPage("https://example.com/broke2", 404, "", false),
		makeTestPage("https://example.com/broke3", 404, "", false),
	}

	oldStats := makeTestStats(3, 3, 0, 0, 0)
	newStats := makeTestStats(3, 0, 3, 3, 3)
	newStats.StatusCodes = map[int]int{404: 2, 500: 1}

	report := ComputeComparison(oldPages, oldStats, newPages, newStats)

	if len(report.Findings) == 0 {
		t.Fatal("expected findings")
	}

	// First finding should be critical
	if report.Findings[0].Severity != SeverityCritical {
		t.Errorf("expected first finding to be critical, got %s", report.Findings[0].Severity)
	}
}

func TestOmittedSections(t *testing.T) {
	oldStats := makeTestStats(10, 10, 0, 0, 0)
	newStats := makeTestStats(10, 10, 0, 0, 0)

	report := ComputeComparison(nil, oldStats, nil, newStats)

	// These sections should be nil when no data exists
	if report.Funnel != nil {
		t.Error("expected nil funnel when no funnel stats")
	}
	if report.Schema != nil {
		t.Error("expected nil schema when no schema stats")
	}
	if report.Sitemap != nil {
		t.Error("expected nil sitemap when no sitemap stats")
	}
	if report.Visibility != nil {
		t.Error("expected nil visibility when no GSC stats")
	}
	if report.Ngrams != nil {
		t.Error("expected nil ngrams when no ngram stats")
	}
	if report.Embeddings != nil {
		t.Error("expected nil embeddings when no embedding stats")
	}
}

func TestSegmentComparison(t *testing.T) {
	oldStats := makeTestStats(100, 90, 0, 0, 0)
	oldStats.SegmentStats = []types.SegmentStat{
		{Name: "/blog/", PageCount: 50, Indexable: 45, AvgWordCount: 1200, TotalClicks: 500},
		{Name: "/products/", PageCount: 50, Indexable: 45, AvgWordCount: 300, TotalClicks: 1000},
	}

	newStats := makeTestStats(110, 100, 0, 0, 0)
	newStats.SegmentStats = []types.SegmentStat{
		{Name: "/blog/", PageCount: 60, Indexable: 55, AvgWordCount: 1250, TotalClicks: 600},
		{Name: "/products/", PageCount: 50, Indexable: 45, AvgWordCount: 320, TotalClicks: 900},
	}

	report := ComputeComparison(nil, oldStats, nil, newStats)

	if len(report.Segments) != 2 {
		t.Errorf("expected 2 segments, got %d", len(report.Segments))
	}

	// Find blog segment
	var blog *SegmentComparison
	for i := range report.Segments {
		if report.Segments[i].Name == "/blog/" {
			blog = &report.Segments[i]
		}
	}
	if blog == nil {
		t.Fatal("expected /blog/ segment")
	}
	if blog.Pages.Delta != 10 {
		t.Errorf("expected blog pages delta 10, got %d", blog.Pages.Delta)
	}
	if blog.Clicks.Delta != 100 {
		t.Errorf("expected blog clicks delta 100, got %d", blog.Clicks.Delta)
	}
}

func TestSegmentDisappeared(t *testing.T) {
	oldStats := makeTestStats(100, 90, 0, 0, 0)
	oldStats.SegmentStats = []types.SegmentStat{
		{Name: "/blog/", PageCount: 50, Indexable: 45},
		{Name: "/legacy/", PageCount: 30, Indexable: 20, TotalClicks: 200},
	}

	newStats := makeTestStats(80, 70, 0, 0, 0)
	newStats.SegmentStats = []types.SegmentStat{
		{Name: "/blog/", PageCount: 60, Indexable: 55},
	}

	report := ComputeComparison(nil, oldStats, nil, newStats)

	if len(report.Segments) != 2 {
		t.Errorf("expected 2 segments (including disappeared), got %d", len(report.Segments))
	}

	var legacy *SegmentComparison
	for i := range report.Segments {
		if report.Segments[i].Name == "/legacy/" {
			legacy = &report.Segments[i]
		}
	}
	if legacy == nil {
		t.Fatal("expected /legacy/ segment to appear (disappeared segment)")
	}
	if legacy.Pages.Old != 30 || legacy.Pages.New != 0 {
		t.Errorf("expected legacy pages old=30 new=0, got old=%d new=%d", legacy.Pages.Old, legacy.Pages.New)
	}
	if legacy.Clicks.Old != 200 || legacy.Clicks.New != 0 {
		t.Errorf("expected legacy clicks old=200 new=0, got old=%d new=%d", legacy.Clicks.Old, legacy.Clicks.New)
	}
}
