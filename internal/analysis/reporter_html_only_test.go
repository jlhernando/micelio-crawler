package analysis

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// onPageFixture mixes HTML pages with URLs that can never have a title,
// description or H1 (jlhernando/micelio-crawler#82).
func onPageFixture() []*types.PageData {
	long := &types.TextLength{Text: "This is a very long title that exceeds sixty characters easily", Length: 62}
	return []*types.PageData{
		{URL: "https://ex.com/img.jpg", StatusCode: 200, ContentType: "image/jpeg"},
		{URL: "https://ex.com/doc.pdf", StatusCode: 200, ContentType: "application/pdf"},
		{URL: "https://ex.com/moved", StatusCode: 301, ContentType: "text/html", Title: long},
		{URL: "https://ex.com/gone", StatusCode: 404, ContentType: "text/html", Title: long},
		{URL: "https://ex.com/blocked", StatusCode: 200, ContentType: "text/html", RobotsBlocked: true},
		{URL: "https://ex.com/html", StatusCode: 200, ContentType: "text/html"},
		{URL: "https://ex.com/xhtml", StatusCode: 200, ContentType: "application/xhtml+xml"},
		{URL: "https://ex.com/charset", StatusCode: 200, ContentType: "text/html; charset=utf-8",
			Title: long, Headings: types.HeadingData{H1: []string{"A", "B"}}},
		{URL: "https://ex.com/old", StatusCode: 200}, // old row, no Content-Type
		{URL: "https://ex.com/old.png", StatusCode: 200},
	}
}

func TestGenerateReportOnPageChecksHTMLOnly(t *testing.T) {
	stats := GenerateReport(onPageFixture(), 1000, ReportConfig{})
	// Counted: html, xhtml, old (no title, no description, no H1) and
	// charset (title + 2 H1s, no description).
	if stats.PagesWithoutTitle != 3 {
		t.Errorf("PagesWithoutTitle = %d, want 3", stats.PagesWithoutTitle)
	}
	if stats.PagesWithoutDescription != 4 {
		t.Errorf("PagesWithoutDescription = %d, want 4", stats.PagesWithoutDescription)
	}
	if stats.PagesWithoutH1 != 3 {
		t.Errorf("PagesWithoutH1 = %d, want 3", stats.PagesWithoutH1)
	}
	if stats.MultipleH1Count != 1 {
		t.Errorf("MultipleH1Count = %d, want 1", stats.MultipleH1Count)
	}
	// The redirect and the 404 carry long titles but are not 2xx HTML.
	if stats.TitleTooLongCount != 1 {
		t.Errorf("TitleTooLongCount = %d, want 1", stats.TitleTooLongCount)
	}
	if stats.TotalPages != 9 || stats.RobotsBlockedCount != 1 {
		t.Errorf("TotalPages/RobotsBlocked = %d/%d, want 9/1", stats.TotalPages, stats.RobotsBlockedCount)
	}
}
