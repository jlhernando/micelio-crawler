package analysis

import (
	"fmt"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestHTMLOver2MB(t *testing.T) {
	const limit = types.GooglebotHTMLLimit
	pages := []*types.PageData{
		{URL: "https://ex.com/at-limit", StatusCode: 200, BodySize: limit}, // exactly 2 MB: fully read
		{URL: "https://ex.com/under", StatusCode: 200, BodySize: limit - 1},
		{URL: "https://ex.com/over", StatusCode: 200, BodySize: limit + 1},
		{URL: "https://ex.com/huge", StatusCode: 200, BodySize: types.HTMLBodyReadCap, // hit the crawler's read cap
			TruncatedElements: []string{"canonical (at 2.4MB)"}},
		{URL: "https://ex.com/gone", StatusCode: 404, BodySize: limit * 2}, // error pages are not indexed anyway
		{URL: "https://ex.com/empty", StatusCode: 200},
		nil,
	}
	list, total := HTMLOver2MB(pages)
	if total != 2 || len(list) != 2 {
		t.Fatalf("total %d list %+v, want 2", total, list)
	}
	if list[0].URL != "https://ex.com/huge" || !list[0].AtLeast || len(list[0].CutElements) != 1 {
		t.Errorf("biggest first with cap flag and cut elements: %+v", list[0])
	}
	if list[1].URL != "https://ex.com/over" || list[1].Bytes != limit+1 || list[1].AtLeast {
		t.Errorf("just-over page: %+v", list[1])
	}
}

func TestHTMLOver2MBCapsList(t *testing.T) {
	var pages []*types.PageData
	for i := 0; i < 70; i++ {
		pages = append(pages, &types.PageData{URL: fmt.Sprintf("https://ex.com/p%02d", i), StatusCode: 200, BodySize: types.GooglebotHTMLLimit + int64(i) + 1})
	}
	list, total := HTMLOver2MB(pages)
	if total != 70 || len(list) != htmlOver2MBListCap {
		t.Fatalf("total %d len %d", total, len(list))
	}
	if list[0].URL != "https://ex.com/p69" {
		t.Errorf("first %s, want the biggest", list[0].URL)
	}
}

func TestGenerateReportHTMLOver2MB(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://ex.com/", StatusCode: 200, BodySize: 50_000, Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://ex.com/big", StatusCode: 200, BodySize: 3 * 1024 * 1024, Indexability: types.IndexabilityData{Indexable: true}},
	}
	stats := GenerateReport(pages, 1000, ReportConfig{SeedURL: "https://ex.com/"})
	if stats.TotalHTMLOver2MBPages != 1 || len(stats.HTMLOver2MBPages) != 1 || stats.HTMLOver2MBPages[0].URL != "https://ex.com/big" {
		t.Fatalf("html over 2 MB stats: %d %+v", stats.TotalHTMLOver2MBPages, stats.HTMLOver2MBPages)
	}
}
