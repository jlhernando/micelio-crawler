package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

func TestPrintHTMLEmptyStats(t *testing.T) {
	var buf bytes.Buffer
	if err := GeneratePrintHTML(&buf, "https://ex.com/", nil, nil, PDFOptions{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"ex.com", "No data", "nothing to report", "PageRank was not computed"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "Action plan") || strings.Contains(out, "Beyond the basics") {
		t.Error("optional sheets shown without data")
	}
}

func TestBuildPDFData(t *testing.T) {
	lcp := 3100.0
	s := &types.CrawlStats{
		TotalPages:              100,
		StatusCodes:             map[int]int{200: 90, 301: 5, 404: 4, 500: 1},
		IndexabilityStats:       types.IndexabilityStats{Indexable: 80, NonIndexable: 20, Reasons: map[string]int{"noindex": 15, "canonicalized": 5}},
		PagesWithoutTitle:       2,
		PagesWithoutDescription: 10,
		ResponseTimeBuckets:     types.ResponseTimeBuckets{Fast: 70, Medium: 20, Slow: 10},
		ResponseTimePercentiles: types.PercentileData{P50: 320, P90: 900, P99: 1800},
		BrokenLinks:             []types.BrokenLink{{URL: "https://ex.com/gone", StatusCode: 404}},
		DepthDistribution:       map[int]int{0: 1, 1: 20, 2: 50, 9: 29},
		CruxStats:               &types.CruxStats{AvgLCPMs: &lcp},
	}
	pages := []*types.PageData{{URL: "https://ex.com/a", PageRank: 0.2}, {URL: "https://ex.com/b", PageRank: 0.5, Inlinks: 9}}
	d := BuildPDFData("https://www.ex.com/", s, pages, PDFOptions{Totals: map[string]int{"totalBrokenLinks": 40}, GeneratedAt: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)})

	if d.Domain != "ex.com" || d.Date != "3 November 2026" {
		t.Errorf("domain %q date %q", d.Domain, d.Date)
	}
	if d.Score <= 0 || d.Score > 100 {
		t.Errorf("score %d", d.Score)
	}
	if d.Issues[0].Severity != "critical" {
		t.Errorf("first issue %+v", d.Issues[0])
	}
	var broken int
	for _, is := range d.Issues {
		if is.ID == "broken-links" {
			broken = is.Count
		}
	}
	if broken != 40 {
		t.Errorf("broken links = %d, want capped total 40", broken)
	}
	if d.Depth[6].Count != 29 {
		t.Errorf("depth 6+ = %d", d.Depth[6].Count)
	}
	if d.TopPages[0].Path != "/b" || d.TopPages[0].Inlinks != 9 {
		t.Errorf("top page %+v", d.TopPages[0])
	}
	if len(d.Checks) != 1 {
		t.Errorf("checks %d", len(d.Checks))
	}

	var buf bytes.Buffer
	if err := GeneratePrintHTML(&buf, "https://www.ex.com/", s, pages, PDFOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), `<section class="sheet`); n != d.Sheets {
		t.Errorf("rendered %d sheets, data says %d", n, d.Sheets)
	}
}
