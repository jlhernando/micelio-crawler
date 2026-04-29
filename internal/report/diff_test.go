package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestComputeDiff(t *testing.T) {
	oldPages := []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200, Title: &types.TextLength{Text: "Home"}, WordCount: 500},
		{URL: "https://example.com/about", StatusCode: 200, Title: &types.TextLength{Text: "About"}, WordCount: 300},
		{URL: "https://example.com/removed", StatusCode: 200, WordCount: 100},
	}
	newPages := []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200, Title: &types.TextLength{Text: "Home Updated"}, WordCount: 600},
		{URL: "https://example.com/about", StatusCode: 200, Title: &types.TextLength{Text: "About"}, WordCount: 300},
		{URL: "https://example.com/new-page", StatusCode: 200, WordCount: 400},
	}

	diff := ComputeDiff(oldPages, newPages)

	if diff.OldCount != 3 {
		t.Fatalf("expected old count 3, got %d", diff.OldCount)
	}
	if diff.NewCount != 3 {
		t.Fatalf("expected new count 3, got %d", diff.NewCount)
	}
	if len(diff.AddedURLs) != 1 || diff.AddedURLs[0] != "https://example.com/new-page" {
		t.Fatalf("expected 1 added URL, got %v", diff.AddedURLs)
	}
	if len(diff.RemovedURLs) != 1 || diff.RemovedURLs[0] != "https://example.com/removed" {
		t.Fatalf("expected 1 removed URL, got %v", diff.RemovedURLs)
	}
	if len(diff.ChangedURLs) != 1 {
		t.Fatalf("expected 1 changed URL, got %d", len(diff.ChangedURLs))
	}
	if diff.ChangedURLs[0].URL != "https://example.com/" {
		t.Fatalf("expected changed URL to be /, got %s", diff.ChangedURLs[0].URL)
	}
	if len(diff.ChangedURLs[0].Changes) != 2 { // title + wordCount
		t.Fatalf("expected 2 changes, got %d", len(diff.ChangedURLs[0].Changes))
	}
	if diff.UnchangedCount != 1 {
		t.Fatalf("expected 1 unchanged, got %d", diff.UnchangedCount)
	}
	if diff.FieldSummary["title"] != 1 {
		t.Fatalf("expected title in field summary")
	}
	if diff.FieldSummary["wordCount"] != 1 {
		t.Fatalf("expected wordCount in field summary")
	}
}

func TestComputeDiffEmpty(t *testing.T) {
	diff := ComputeDiff(nil, nil)
	if diff.OldCount != 0 || diff.NewCount != 0 {
		t.Fatal("expected zero counts")
	}
	if len(diff.AddedURLs) != 0 || len(diff.RemovedURLs) != 0 || len(diff.ChangedURLs) != 0 {
		t.Fatal("expected empty diff")
	}
}

func TestPrintDiffSummary(t *testing.T) {
	diff := &DiffResult{
		OldCount:       100,
		NewCount:       105,
		AddedURLs:      []string{"a", "b", "c", "d", "e"},
		RemovedURLs:    nil,
		ChangedURLs:    []URLDiff{{URL: "x", Changes: []FieldDiff{{Field: "title"}}}},
		UnchangedCount: 95,
		FieldSummary:   map[string]int{"title": 1},
	}

	var buf bytes.Buffer
	PrintDiffSummary(&buf, diff)
	output := buf.String()

	if !strings.Contains(output, "100 → 105") {
		t.Fatal("expected page counts in summary")
	}
	if !strings.Contains(output, "Added:     5") {
		t.Fatal("expected added count")
	}
	if !strings.Contains(output, "title") {
		t.Fatal("expected field summary")
	}
}

func TestLifecycleSummary(t *testing.T) {
	oldPages := []*types.PageData{
		// Disappeared with traffic
		{URL: "https://example.com/popular", StatusCode: 200,
			Indexability: types.IndexabilityData{Indexable: true},
			GscData: &types.GscData{Clicks: 50, Impressions: 1000}},
		// Disappeared without traffic (removed)
		{URL: "https://example.com/old-page", StatusCode: 200,
			Indexability: types.IndexabilityData{Indexable: true}},
		// Disappeared 404 page
		{URL: "https://example.com/broken", StatusCode: 404},
		// Stays
		{URL: "https://example.com/", StatusCode: 200},
	}
	newPages := []*types.PageData{
		{URL: "https://example.com/", StatusCode: 200},
		// New with traffic (GSC data from indexing)
		{URL: "https://example.com/new-hit", StatusCode: 200,
			GscData: &types.GscData{Clicks: 5, Impressions: 200}},
		// New without traffic
		{URL: "https://example.com/fresh", StatusCode: 200},
	}

	diff := ComputeDiff(oldPages, newPages)

	if diff.Lifecycle == nil {
		t.Fatal("expected lifecycle summary")
	}

	// 3 disappeared URLs
	if len(diff.RemovedURLs) != 3 {
		t.Errorf("RemovedURLs = %d, want 3", len(diff.RemovedURLs))
	}

	// 1 disappeared with traffic
	if len(diff.Lifecycle.DisappearedWithTraffic) != 1 {
		t.Errorf("DisappearedWithTraffic = %d, want 1", len(diff.Lifecycle.DisappearedWithTraffic))
	}
	if len(diff.Lifecycle.DisappearedWithTraffic) > 0 {
		dwt := diff.Lifecycle.DisappearedWithTraffic[0]
		if dwt.URL != "https://example.com/popular" {
			t.Errorf("DisappearedWithTraffic[0].URL = %q, want popular", dwt.URL)
		}
		if dwt.Clicks != 50 {
			t.Errorf("Clicks = %d, want 50", dwt.Clicks)
		}
		if dwt.Reason != "removed" {
			t.Errorf("Reason = %q, want removed", dwt.Reason)
		}
	}

	// 1 new with traffic
	if len(diff.Lifecycle.NewWithTraffic) != 1 {
		t.Errorf("NewWithTraffic = %d, want 1", len(diff.Lifecycle.NewWithTraffic))
	}

	// Disappeared reasons
	if diff.Lifecycle.DisappearedReasons["removed"] != 2 {
		t.Errorf("DisappearedReasons[removed] = %d, want 2", diff.Lifecycle.DisappearedReasons["removed"])
	}
	if diff.Lifecycle.DisappearedReasons["client-error"] != 1 {
		t.Errorf("DisappearedReasons[client-error] = %d, want 1", diff.Lifecycle.DisappearedReasons["client-error"])
	}
}

func TestGenerateDiffHTML(t *testing.T) {
	diff := &DiffResult{
		OldCount:       50,
		NewCount:       55,
		AddedURLs:      []string{"https://example.com/new"},
		RemovedURLs:    []string{"https://example.com/old"},
		ChangedURLs:    []URLDiff{{URL: "https://example.com/", Changes: []FieldDiff{{Field: "title", OldValue: "Old", NewValue: "New"}}}},
		UnchangedCount: 48,
		FieldSummary:   map[string]int{"title": 1},
	}

	var buf bytes.Buffer
	err := GenerateDiffHTML(&buf, diff)
	if err != nil {
		t.Fatal(err)
	}

	html := buf.String()
	if !strings.Contains(html, "Crawl Diff Report") {
		t.Fatal("expected diff report title")
	}
	if !strings.Contains(html, "</html>") {
		t.Fatal("expected closing HTML tag")
	}
}
