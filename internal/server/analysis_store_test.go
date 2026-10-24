package server

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// siteDB writes n linked pages to a crawl DB and returns its path.
func siteDB(t testing.TB, n int) string {
	t.Helper()
	url := func(i int) string {
		if i == 0 {
			return "https://ex.com/"
		}
		return "https://ex.com/d" + strconv.Itoa(i%50) + "/p" + strconv.Itoa(i)
	}
	pages := make([]*types.PageData, n)
	for i := range pages {
		links := []string{url(0), url((i*7 + 1) % n), url((i*13 + 5) % n), url((i + 1) % n)}
		pages[i] = &types.PageData{URL: url(i), StatusCode: 200, Depth: i % 5, InternalLinks: links,
			Title: &types.TextLength{Text: "Page " + strconv.Itoa(i)}}
	}
	path := t.TempDir() + "/crawl.db"
	cs, err := storage.NewCrawlStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	for start := 0; start < n; start += 500 {
		batch := []storage.EncodedPage{}
		for _, p := range pages[start:min(start+500, n)] {
			e, err := storage.EncodePage(p, nil)
			if err != nil {
				t.Fatal(err)
			}
			batch = append(batch, e)
		}
		if err := cs.InsertEncodedPages(batch); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func loadPages(t testing.TB, path string) []*types.PageData {
	t.Helper()
	cs, err := storage.NewCrawlStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	pages, err := cs.GetAllPages()
	if err != nil {
		t.Fatal(err)
	}
	return pages
}

// An old crawl (PageRank map in stats, nothing stored in the crawl DB) is
// migrated on first reload; the second reload reads graph and scores back
// from the crawl DB and gets identical results.
func TestReloadStoresAndReusesAnalysis(t *testing.T) {
	const n = 800
	path := siteDB(t, n)

	// First load: the old way, then stored.
	pages1 := loadPages(t, path)
	g := analysis.BuildAdjacencyListFromDisk(pages1, func(fn func(string, []string)) error {
		cs, _ := storage.NewCrawlStore(path)
		defer cs.Close()
		return cs.IterateInternalLinks(fn)
	})
	scores := analysis.ComputePageRankGraph(g, analysis.PageRankOptions{})
	stats := map[string]interface{}{"pageRankScores": scoresToJSONMap(scores), "totalPages": float64(n)}
	agg1 := prepareLoadedPages("c1", pages1, stats, path, "https://ex.com/", nil)
	if agg1.graph == nil {
		t.Fatal("no graph on first load")
	}
	if v, ok := agg1.statsUpdates["pageRankScores"]; !ok || v != nil {
		t.Errorf("pageRankScores not dropped from stats after migration: %v", ok)
	}
	if agg1.statsUpdates["pageRankHistogram"] == nil {
		t.Error("pageRankHistogram not added")
	}
	applyStatsUpdates(stats, agg1.statsUpdates)

	// Second load: from the stored arrays, no stats map.
	pages2 := loadPages(t, path)
	agg2 := prepareLoadedPages("c1", pages2, stats, path, "https://ex.com/", nil)
	if agg2.graph == nil {
		t.Fatal("no graph on second load")
	}
	if !slices.Equal(agg1.graph.Offsets, agg2.graph.Offsets) || !slices.Equal(agg1.graph.Edges, agg2.graph.Edges) {
		t.Error("stored graph differs from rebuilt graph")
	}
	for i := range pages1 {
		if pages1[i].PageRank != pages2[i].PageRank || pages2[i].PageRank != scores[pages2[i].URL] {
			t.Fatalf("page %d PageRank %v vs %v (want %v)", i, pages1[i].PageRank, pages2[i].PageRank, scores[pages2[i].URL])
		}
		if pages1[i].Inlinks != pages2[i].Inlinks {
			t.Fatalf("page %d inlinks %d vs %d", i, pages1[i].Inlinks, pages2[i].Inlinks)
		}
		a1, a2 := pages1[i].LinkIntelligence, pages2[i].LinkIntelligence
		if (a1 == nil) != (a2 == nil) || (a1 != nil && (a1.AuthorityScore != a2.AuthorityScore || a1.HubScore != a2.HubScore)) {
			t.Fatalf("page %d HITS differ", i)
		}
	}
	if agg2.graph.URLIndex[pages2[5].URL] != 5 {
		t.Error("URL index not rebuilt")
	}
}

// Stored arrays are ignored when the page order no longer matches.
func TestStoredAnalysisIgnoredOnPageMismatch(t *testing.T) {
	path := siteDB(t, 50)
	pages := loadPages(t, path)
	g := analysis.BuildAdjacencyListFromDisk(pages, func(fn func(string, []string)) error {
		cs, _ := storage.NewCrawlStore(path)
		defer cs.Close()
		return cs.IterateInternalLinks(fn)
	})
	if !snapshotAnalysis(pages, g, true).save(path) {
		t.Fatal("save failed")
	}
	pages[0], pages[1] = pages[1], pages[0]
	if g2, ok := loadStoredAnalysis(path, pages); g2 != nil || ok {
		t.Error("stored analysis used for a different page order")
	}
}

// TestPerfReload times a reload of a large synthetic crawl the old way
// (decode every page again for links + PageRank from a stats map + HITS)
// and from stored arrays.
// Run with: MICELIO_PERF=200000 go test ./internal/server -run TestPerfReload -v
func TestPerfReload(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("MICELIO_PERF"))
	if n == 0 {
		t.Skip("set MICELIO_PERF=<pages> to run")
	}
	path := siteDB(t, n)
	pages := loadPages(t, path)
	g := buildGraphFromCrawlDB(pages, path)
	scores := analysis.ComputePageRankGraph(g, analysis.PageRankOptions{})
	stats := map[string]interface{}{"pageRankScores": scoresToJSONMap(scores), "totalPages": float64(n)}

	start := time.Now()
	old := loadPages(t, path)
	oldGraph := buildGraphFromCrawlDB(old, path)
	assignPageRankFromStatsMap(old, stats)
	restoreHits(old, oldGraph)
	oldDur := time.Since(start)

	snap := snapshotAnalysis(old, oldGraph, true)
	if !snap.save(path) {
		t.Fatal("save failed")
	}
	start = time.Now()
	fresh := loadPages(t, path)
	if g2, ok := loadStoredAnalysis(path, fresh); g2 == nil || !ok {
		t.Fatal("stored analysis not loaded")
	}
	newDur := time.Since(start)
	fmt.Printf("PERF reload pages=%d old=%s stored=%s\n", n, oldDur.Round(time.Millisecond), newDur.Round(time.Millisecond))
}
