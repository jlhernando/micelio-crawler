package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func filteredTestPages() []*types.PageData {
	title := func(s string) *types.TextLength { return &types.TextLength{Text: s, Length: len(s)} }
	desc := func(s string) *types.TextLength { return &types.TextLength{Text: s, Length: len(s)} }
	return []*types.PageData{
		{
			URL: "https://ex.com/p/1", StatusCode: 200, TemplateType: "product", Depth: 2,
			Title: title("Shared product title for testing"), MetaDescription: desc("d1"),
			Headings:     types.HeadingData{H1: []string{"a"}},
			Indexability: types.IndexabilityData{Indexable: true}, WordCount: 120, ResponseTimeMs: 400,
			InternalLinkCount: 10, ExternalLinkCount: 1, PageRank: 0.2, URLClassification: "active",
		},
		{
			URL: "https://ex.com/p/2", StatusCode: 200, TemplateType: "product", Depth: 3,
			Title:        title("Shared product title for testing"),
			Headings:     types.HeadingData{H1: []string{"a", "b"}},
			Indexability: types.IndexabilityData{Indexable: false, Reason: "noindex"}, WordCount: 800, ResponseTimeMs: 3500,
			InternalLinkCount: 5, PageRank: 0.1, URLClassification: "visible",
		},
		{
			URL: "https://ex.com/p/3", StatusCode: 404, TemplateType: "product", Depth: 3,
			Indexability: types.IndexabilityData{Indexable: false, Reason: "status_404"}, ResponseTimeMs: 900,
		},
		{URL: "https://ex.com/p/4", TemplateType: "product", RobotsBlocked: true},
		{
			URL: "https://ex.com/c/1", StatusCode: 200, TemplateType: "category", Depth: 1,
			Title: title("Category one page title here ok"), MetaDescription: desc("d1"),
			Headings:     types.HeadingData{H1: []string{"c"}},
			Indexability: types.IndexabilityData{Indexable: true}, WordCount: 50, ResponseTimeMs: 100,
			InternalLinkCount: 40, PageRank: 0.5,
		},
	}
}

func filteredTestFullStats() map[string]interface{} {
	return map[string]interface{}{
		"totalPages":       4,
		"imagesMissingAlt": 7,
		"pagesWithoutOg":   1,
		"orphanPages":      []string{"https://ex.com/p/2", "https://ex.com/c/1"},
		"soft404Pages":     []interface{}{"https://ex.com/c/1"},
		"brokenLinks": []interface{}{
			map[string]interface{}{"url": "https://ex.com/p/3", "statusCode": 404.0, "foundOn": []interface{}{"https://ex.com/c/1"}},
			map[string]interface{}{"url": "https://ex.com/x", "statusCode": 500.0},
		},
		"urlIssueStats": map[string]interface{}{
			"uppercase": []interface{}{"https://ex.com/p/1", "https://ex.com/c/1"},
			"params":    []interface{}{"https://ex.com/c/1"},
		},
		"pageRankScores": map[string]interface{}{
			"https://ex.com/p/1": 0.2, "https://ex.com/p/2": 0.1, "https://ex.com/c/1": 0.5,
		},
	}
}

func TestComputeTemplateFilteredStats_DoesNotMutateAndCounts(t *testing.T) {
	pages := filteredTestPages()
	full := filteredTestFullStats()
	beforePages, _ := json.Marshal(pages)
	beforeFull, _ := json.Marshal(full)

	res := computeTemplateFilteredStats(full, pages, "product")

	afterPages, _ := json.Marshal(pages)
	afterFull, _ := json.Marshal(full)
	if string(beforePages) != string(afterPages) {
		t.Fatalf("pages were mutated:\nbefore %s\nafter  %s", beforePages, afterPages)
	}
	if string(beforeFull) != string(afterFull) {
		t.Fatalf("full stats were mutated")
	}

	// Decode the way the dashboard sees it (numbers as float64).
	var s map[string]interface{}
	raw, _ := json.Marshal(res.Stats)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	num := func(k string) float64 {
		t.Helper()
		v, ok := s[k].(float64)
		if !ok {
			t.Fatalf("key %q missing or not a number: %#v", k, s[k])
		}
		return v
	}
	if res.PageCount != 4 {
		t.Errorf("pageCount = %d, want 4", res.PageCount)
	}
	wantNums := map[string]float64{
		"totalPages":                3,
		"robotsBlockedCount":        1,
		"pagesWithoutTitle":         1,
		"pagesWithoutDescription":   2,
		"pagesWithoutH1":            1,
		"multipleH1Count":           1,
		"duplicateTitleCount":       2,
		"duplicateDescriptionCount": 1, // "d1" is shared with the category page
		"titleTooShortCount":        0,
		"totalInternalLinks":        15,
		"totalExternalLinks":        1,
		"totalThinContentPages":     1,
		"totalOrphanPages":          1,
		"totalBrokenLinks":          1,
	}
	for k, want := range wantNums {
		if got := num(k); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}

	codes := s["statusCodes"].(map[string]interface{})
	if codes["200"] != 2.0 || codes["404"] != 1.0 {
		t.Errorf("statusCodes = %v", codes)
	}
	idx := s["indexabilityStats"].(map[string]interface{})
	if idx["indexable"] != 1.0 || idx["nonIndexable"] != 2.0 {
		t.Errorf("indexabilityStats = %v", idx)
	}
	if td := s["templateTypeDistribution"].(map[string]interface{}); len(td) != 1 || td["product"] != 4.0 {
		t.Errorf("templateTypeDistribution = %v", td)
	}
	if slow := s["slowPages"].([]interface{}); len(slow) != 1 {
		t.Errorf("slowPages = %v", slow)
	}
	if orphans := s["orphanPages"].([]interface{}); len(orphans) != 1 || orphans[0] != "https://ex.com/p/2" {
		t.Errorf("orphanPages = %v", orphans)
	}
	if soft := s["soft404Pages"].([]interface{}); len(soft) != 0 {
		t.Errorf("soft404Pages = %v", soft)
	}
	issues := s["urlIssueStats"].(map[string]interface{})
	if len(issues) != 1 || len(issues["uppercase"].([]interface{})) != 1 {
		t.Errorf("urlIssueStats = %v", issues)
	}
	if pr := s["pageRankScores"].(map[string]interface{}); len(pr) != 2 || pr["https://ex.com/p/1"] != 0.2 {
		t.Errorf("pageRankScores = %v", pr)
	}

	// Metrics that cannot be computed from cached pages must be omitted,
	// never reported as zero.
	for _, k := range []string{"imagesMissingAlt", "pagesWithoutOg", "pagesWithStructuredData",
		"hreflangIssues", "duplicateContentGroups", "seoFunnelStats", "canonicalStats",
		"gscStats", "ga4Stats", "linkIntelligenceStats", "totalImages"} {
		if _, ok := s[k]; ok {
			t.Errorf("key %q should be omitted for a filtered subset", k)
		}
	}
	scoped := map[string]bool{}
	for _, k := range res.ScopedKeys {
		scoped[k] = true
	}
	for k := range s {
		if !scoped[k] {
			t.Errorf("key %q not listed in scopedKeys", k)
		}
	}
}

func TestFilteredStatsEndpoint_TemplateSubset(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := filteredTestPages()
	before, _ := json.Marshal(pages)
	m.results["c1"] = &CrawlResults{Stats: filteredTestFullStats(), Pages: pages, PageCount: len(pages), PagesReady: true}
	m.resultOrder = append(m.resultOrder, "c1")

	req := httptest.NewRequest("GET", "/api/crawl/c1/stats/filtered?template=product", nil)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Stats      map[string]interface{} `json:"stats"`
		PageCount  int                    `json:"pageCount"`
		ScopedKeys []string               `json:"scopedKeys"`
		Template   string                 `json:"template"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.PageCount != 4 || body.Template != "product" || len(body.ScopedKeys) == 0 {
		t.Fatalf("unexpected body: pageCount=%d template=%q scoped=%d", body.PageCount, body.Template, len(body.ScopedKeys))
	}
	if body.Stats["totalPages"] != 3.0 {
		t.Errorf("totalPages = %v, want 3", body.Stats["totalPages"])
	}
	if _, ok := body.Stats["imagesMissingAlt"]; ok {
		t.Errorf("imagesMissingAlt should be omitted")
	}
	after, _ := json.Marshal(pages)
	if string(before) != string(after) {
		t.Fatalf("endpoint mutated cached pages")
	}
	for _, p := range pages {
		if p.URL == "https://ex.com/c/1" && p.URLClassification != "" {
			t.Errorf("URLClassification was rewritten on cached page: %q", p.URLClassification)
		}
	}
}

func TestComputeTemplateFilteredStats_OmitsMissingLinkCounts(t *testing.T) {
	pages := []*types.PageData{{URL: "https://ex.com/p/1", StatusCode: 200, TemplateType: "product"}}
	full := map[string]interface{}{"totalInternalLinks": 120.0, "totalExternalLinks": 0.0}
	res := computeTemplateFilteredStats(full, pages, "product")
	if _, ok := res.Stats["totalInternalLinks"]; ok {
		t.Errorf("totalInternalLinks should be omitted when cached pages lost their link counts")
	}
	if _, ok := res.Stats["totalExternalLinks"]; !ok {
		t.Errorf("totalExternalLinks = 0 is genuine when the site total is 0")
	}
}
