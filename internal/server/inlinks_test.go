package server

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// linkedPages returns a small site whose pages carry InternalLinks (as stored
// in the crawl DB). Callers strip them to mimic a reload.
//
//	/            -> /cat/, /cat/p1, /cat/p2
//	/cat/        -> /cat/p1, /cat/p2, /
//	/cat/p1      -> /cat/p2, /cat (no slash; resolves to /cat/)
//	/cat/p2      -> /
//	/lonely      (no inlinks)
func linkedPages() []*types.PageData {
	idx := types.IndexabilityData{Indexable: true}
	return []*types.PageData{
		{URL: "https://ex.com/", StatusCode: 200, Depth: 0, Indexability: idx,
			InternalLinks: []string{"https://ex.com/cat/", "https://ex.com/cat/p1", "https://ex.com/cat/p2"}},
		{URL: "https://ex.com/cat/", StatusCode: 200, Depth: 1, Indexability: idx,
			InternalLinks: []string{"https://ex.com/cat/p1", "https://ex.com/cat/p2", "https://ex.com/"}},
		{URL: "https://ex.com/cat/p1", StatusCode: 200, Depth: 2, Indexability: idx,
			InternalLinks: []string{"https://ex.com/cat/p2", "https://ex.com/cat"}},
		{URL: "https://ex.com/cat/p2", StatusCode: 200, Depth: 2, Indexability: idx,
			InternalLinks: []string{"https://ex.com/"}},
		{URL: "https://ex.com/lonely", StatusCode: 200, Depth: 3, Indexability: idx},
	}
}

func diskIter(pages []*types.PageData) func(fn func(string, []string)) error {
	links := make(map[string][]string, len(pages))
	for _, p := range pages {
		links[p.URL] = p.InternalLinks
	}
	return func(fn func(string, []string)) error {
		for _, p := range pages {
			if l := links[p.URL]; len(l) > 0 {
				fn(p.URL, l)
			}
		}
		return nil
	}
}

func stripLinks(pages []*types.PageData) {
	for _, p := range pages {
		p.InternalLinkCount = len(p.InternalLinks)
		p.InternalLinks = nil
	}
}

func TestGraphInlinkIndex(t *testing.T) {
	pages := linkedPages()
	iter := diskIter(pages)
	stripLinks(pages)
	g := analysis.BuildAdjacencyListFromDisk(pages, iter)

	ix := newInlinkIndex(g, pages)
	got := ix.Sources("https://ex.com/cat/p2")
	want := []string{"https://ex.com/", "https://ex.com/cat/", "https://ex.com/cat/p1"}
	if len(got) != len(want) {
		t.Fatalf("Sources(/cat/p2) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Sources[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// /cat/ gets links from / and from /cat/p1 (trailing slash resolved).
	if n := ix.Count("https://ex.com/cat/"); n != 2 {
		t.Errorf("Count(/cat/) = %d, want 2", n)
	}
	if s := ix.Sources("https://ex.com/lonely"); len(s) != 0 {
		t.Errorf("Sources(/lonely) = %v, want none", s)
	}
	if s := ix.Sources("https://ex.com/unknown"); len(s) != 0 {
		t.Errorf("Sources(unknown) = %v, want none", s)
	}

	assignInlinks(pages, ix)
	wantInlinks := []int{2, 2, 2, 3, 0}
	for i, p := range pages {
		if p.Inlinks != wantInlinks[i] {
			t.Errorf("%s Inlinks = %d, want %d", p.URL, p.Inlinks, wantInlinks[i])
		}
	}
}

func TestLegacyInlinkIndexFallback(t *testing.T) {
	pages := linkedPages()
	ix := newInlinkIndex(nil, pages)
	if n := ix.Count("https://ex.com/cat/p2"); n != 3 {
		t.Errorf("legacy Count = %d, want 3", n)
	}
}

func TestPageInlinksEndpointUsesGraph(t *testing.T) {
	api, _, m := newTestAPI(t)
	pages := linkedPages()
	iter := diskIter(pages)
	stripLinks(pages)
	g := analysis.BuildAdjacencyListFromDisk(pages, iter)
	res := &CrawlResults{Pages: pages, Graph: g, InlinkIndex: newInlinkIndex(g, pages), PagesReady: true}
	m.mu.Lock()
	m.results["graph-crawl"] = res
	m.mu.Unlock()

	req := httptest.NewRequest("GET", "/api/crawl/graph-crawl/page-inlinks?url="+url.QueryEscape("https://ex.com/cat/p1"), nil)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	var body struct {
		Sources []string `json:"sources"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sources) != 2 {
		t.Errorf("sources = %v, want / and /cat/", body.Sources)
	}
}

// writeCrawlDB stores pages (with InternalLinks) in a crawl DB.
func writeCrawlDB(t *testing.T, pages []*types.PageData) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crawl.db")
	cs, err := storage.NewCrawlStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for _, p := range pages {
		if err := cs.InsertPage(p); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestPrepareLoadedPagesUsesGraph(t *testing.T) {
	dbPath := writeCrawlDB(t, linkedPages())
	cs, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	pages, err := cs.GetAllPages()
	cs.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Stored stats from a pre-fix crawl: every non-seed page flagged orphan.
	stats := map[string]interface{}{
		"totalPages":        float64(5),
		"orphanPages":       []interface{}{"https://ex.com/cat/", "https://ex.com/cat/p1", "https://ex.com/cat/p2", "https://ex.com/lonely"},
		"orphanMethodology": "reporter",
		"alertSummary": map[string]interface{}{
			"alerts": []interface{}{map[string]interface{}{
				"id": "orphan-pages", "severity": "warning", "value": float64(80),
				"message": "80.0% of pages have no internal links pointing to them (4 pages)",
			}},
			"warnings": float64(1),
		},
	}

	agg := prepareLoadedPages("c1", pages, stats, dbPath, "https://ex.com/", nil)
	if agg.graph == nil || agg.inlinkIdx == nil {
		t.Fatal("expected graph and inlink index")
	}
	if pages[3].Inlinks != 3 {
		t.Errorf("/cat/p2 Inlinks = %d, want 3", pages[3].Inlinks)
	}
	if s := agg.inlinkIdx.Sources("https://ex.com/cat/p1"); len(s) != 2 {
		t.Errorf("Sources(/cat/p1) = %v, want 2", s)
	}

	// Directory tree is built after inlinks are known.
	root, _ := agg.dirTree["tree"].(*dirNode)
	if root == nil || root.TotalInlinks != 9 {
		t.Errorf("tree TotalInlinks = %v, want 9", root)
	}
	if root != nil && root.TotalInternalLinks == 0 {
		t.Error("tree TotalInternalLinks = 0, want link counts kept after reload")
	}

	// Orphans corrected; orphan alert gone; persisted.
	if !agg.persist {
		t.Error("expected persist after orphan backfill")
	}
	orphans, _ := agg.statsUpdates["orphanPages"].([]string)
	if len(orphans) != 1 || orphans[0] != "https://ex.com/lonely" {
		t.Errorf("orphanPages = %v, want [/lonely]", agg.statsUpdates["orphanPages"])
	}
	if agg.statsUpdates["orphanMethodology"] != "graph" {
		t.Errorf("orphanMethodology = %v", agg.statsUpdates["orphanMethodology"])
	}
	// 1 orphan of 5 pages = 20%, not the stored 80%.
	summary, _ := agg.statsUpdates["alertSummary"].(map[string]interface{})
	var orphanRate float64
	if summary != nil {
		for _, raw := range summary["alerts"].([]interface{}) {
			if a := raw.(map[string]interface{}); a["id"] == "orphan-pages" {
				orphanRate, _ = a["value"].(float64)
			}
		}
	}
	if orphanRate != 20 {
		t.Errorf("orphan alert rate = %v, want 20 (summary %v)", orphanRate, summary)
	}
	// Input stats must not be mutated (caller applies updates under lock).
	if stats["orphanMethodology"] != "reporter" {
		t.Error("prepareLoadedPages mutated the input stats map")
	}

	applyStatsUpdates(stats, agg.statsUpdates)
	if stats["orphanMethodology"] != "graph" {
		t.Error("applyStatsUpdates did not apply updates")
	}

	// Second load of corrected stats is a no-op for the backfill.
	agg2 := prepareLoadedPages("c1", pages, stats, dbPath, "https://ex.com/", nil)
	if _, ok := agg2.statsUpdates["alertSummary"]; ok {
		t.Error("alerts re-evaluated on an already-corrected crawl")
	}
}

func TestOrphanAlertStale(t *testing.T) {
	alert := func(v float64) map[string]interface{} {
		return map[string]interface{}{"alerts": []interface{}{map[string]interface{}{"id": "orphan-pages", "value": v}}}
	}
	cases := []struct {
		name  string
		stats map[string]interface{}
		want  bool
	}{
		{"matches", map[string]interface{}{"totalPages": float64(10), "orphanPages": []interface{}{1, 2, 3, 4, 5, 6}, "alertSummary": alert(60)}, false},
		{"bogus alert", map[string]interface{}{"totalPages": float64(10), "orphanPages": []interface{}{1}, "alertSummary": alert(99.8)}, true},
		{"missing alert below threshold", map[string]interface{}{"totalPages": float64(100), "orphanPages": []interface{}{1}}, false},
		{"missing alert above threshold", map[string]interface{}{"totalPages": float64(10), "orphanPages": []interface{}{1}}, true},
	}
	for _, c := range cases {
		if got := orphanAlertStale(c.stats); got != c.want {
			t.Errorf("%s: orphanAlertStale = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRestoreHitsOnReload(t *testing.T) {
	pages := linkedPages()
	iter := diskIter(pages)
	stripLinks(pages)
	g := analysis.BuildAdjacencyListFromDisk(pages, iter)
	if !restoreHits(pages, g) {
		t.Fatal("restoreHits did nothing")
	}
	// /cat/p2 is linked from 3 pages: highest authority.
	best := 0
	for i, p := range pages {
		if p.LinkIntelligence.AuthorityScore > pages[best].LinkIntelligence.AuthorityScore {
			best = i
		}
	}
	if pages[best].URL != "https://ex.com/cat/p2" {
		t.Errorf("top authority = %s", pages[best].URL)
	}
	if restoreHits(pages, g) {
		t.Error("restoreHits should skip pages that already have scores")
	}
}
