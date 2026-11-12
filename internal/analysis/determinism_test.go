package analysis

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// determinismRuns is how many times each builder runs. Go randomises map
// iteration, so an order that leaks from a map shows up within a few runs.
const determinismRuns = 40

// sameEveryRun runs build repeatedly and fails when any run's JSON differs
// from the first.
func sameEveryRun(t *testing.T, build func(run int) any) {
	t.Helper()
	first, err := json.Marshal(build(0))
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run < determinismRuns; run++ {
		got, err := json.Marshal(build(run))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(first) {
			t.Fatalf("run %d differs from run 0:\n%s\nvs\n%s", run, got, first)
		}
	}
}

// shuffledPages returns the same pages in a different order per run.
func shuffledPages(pages []*types.PageData, run int) []*types.PageData {
	out := append([]*types.PageData(nil), pages...)
	rand.New(rand.NewSource(int64(run))).Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func TestNgramTopNDeterministicOnTies(t *testing.T) {
	// 300 terms, every one with the same count: the top 50 cut is all ties.
	sameEveryRun(t, func(int) any {
		m := map[string]*ngramData{}
		for i := 0; i < 300; i++ {
			m[fmt.Sprintf("term%03d", i)] = &ngramData{count: 7, pages: 3}
		}
		m["winner"] = &ngramData{count: 9, pages: 3}
		return buildEntries(m, 10, 1000, 50)
	})
	got := buildEntries(map[string]*ngramData{
		"b": {count: 5, pages: 2}, "a": {count: 5, pages: 2}, "z": {count: 6, pages: 2},
	}, 10, 100, 3)
	if got[0].Term != "z" || got[1].Term != "a" || got[2].Term != "b" {
		t.Fatalf("want count desc then term asc, got %+v", got)
	}
}

func TestNgramPruneKeepsSameTermsOnTies(t *testing.T) {
	sameEveryRun(t, func(int) any {
		m := map[string]*ngramData{}
		for i := 0; i < 2*maxEntries+500; i++ {
			m[fmt.Sprintf("t%06d", i)] = &ngramData{count: 1, pages: 1}
		}
		pruneIfNeeded(m)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		return len(keys) == maxEntries && m["t000000"] != nil && m[fmt.Sprintf("t%06d", maxEntries-1)] != nil && m[fmt.Sprintf("t%06d", maxEntries)] == nil
	})
}

func TestSortedCountsDeterministicOnTies(t *testing.T) {
	freq := map[string]int{}
	for i := 0; i < 100; i++ {
		freq[fmt.Sprintf("https://example.com/p%02d", i)] = 3
	}
	freq["https://example.com/top"] = 10
	sameEveryRun(t, func(int) any {
		return []any{sortedURLCounts(freq, 20), sortedNamedCounts(freq, 20), sortedExtCounts(freq)}
	})
	got := sortedURLCounts(freq, 3)
	if got[0].URL != "https://example.com/top" || got[1].URL != "https://example.com/p00" || got[2].URL != "https://example.com/p01" {
		t.Fatalf("want count desc then URL asc, got %+v", got)
	}
}

func TestReportListsDeterministicOnTies(t *testing.T) {
	var pages []*types.PageData
	for i := 0; i < 60; i++ {
		u := fmt.Sprintf("https://example.com/d%02d/page?k%02d=1", i%30, i%25)
		p := &types.PageData{
			URL:            u,
			StatusCode:     200,
			ResponseTimeMs: 4000,
			WordCount:      10,
			ContentHash:    fmt.Sprintf("hash%d", i%20), // 20 groups of 3
			FinalURL:       fmt.Sprintf("https://example.com/target%02d", i%30),
			RedirectChain:  []types.RedirectHop{{URL: u, StatusCode: 301}},
			SchemaValidation: []types.SchemaValidationEntry{{
				Type: "Article",
				Issues: []types.SchemaValidationIssue{
					{Severity: types.SeverityWarning, Message: fmt.Sprintf("Missing property p%02d", i%30)},
				},
			}},
		}
		pages = append(pages, p)
	}
	sameEveryRun(t, func(run int) any {
		ps := shuffledPages(pages, 0) // same input order; maps inside differ per run
		st := GenerateReport(ps, 0, ReportConfig{SeedURL: "https://example.com/"})
		return map[string]any{
			"redirectTargets":  st.RedirectStats.TopRedirectTargets,
			"schemaIssues":     BuildSchemaValidationStats(ps).TopIssues,
			"duplicateGroups":  st.DuplicateContentGroups,
			"topDirectories":   st.URLStructureStats.TopDirectories,
			"topParameters":    st.URLStructureStats.TopParameters,
			"slowPages":        st.SlowPages,
			"thinContentPages": st.ThinContentPages,
		}
	})
}

func TestDuplicateGroupsOrderedBySizeThenURL(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://e.com/z1", StatusCode: 200, ContentHash: "h1"},
		{URL: "https://e.com/z2", StatusCode: 200, ContentHash: "h1"},
		{URL: "https://e.com/a1", StatusCode: 200, ContentHash: "h2"},
		{URL: "https://e.com/a2", StatusCode: 200, ContentHash: "h2"},
		{URL: "https://e.com/m1", StatusCode: 200, ContentHash: "h3"},
		{URL: "https://e.com/m2", StatusCode: 200, ContentHash: "h3"},
		{URL: "https://e.com/m3", StatusCode: 200, ContentHash: "h3"},
	}
	st := GenerateReport(pages, 0, ReportConfig{SeedURL: "https://e.com/"})
	var firsts []string
	for _, g := range st.DuplicateContentGroups {
		firsts = append(firsts, g.URLs[0])
	}
	if want := []string{"https://e.com/m1", "https://e.com/a1", "https://e.com/z1"}; !reflect.DeepEqual(firsts, want) {
		t.Fatalf("groups = %v, want %v", firsts, want)
	}
}

func TestSitemapAndAIVisibilityListsDeterministic(t *testing.T) {
	var entries []string
	for i := 0; i < 50; i++ {
		entries = append(entries, fmt.Sprintf("https://example.com/s%02d", i))
	}
	var pages []*types.PageData
	for i := 0; i < 30; i++ {
		var qs []types.AIOverviewQuery
		for k := 0; k < 3; k++ {
			qs = append(qs, types.AIOverviewQuery{Query: fmt.Sprintf("q%02d", (i+k)%40), Clicks: 2, Impressions: 10})
		}
		pages = append(pages, &types.PageData{
			URL:              fmt.Sprintf("https://example.com/p%02d", i),
			StatusCode:       200,
			AIVisibilityData: &types.AIVisibilityData{AIClicks: 1, AIImpressions: 5, Queries: qs},
		})
	}
	sameEveryRun(t, func(int) any {
		cfg := ReportConfig{SitemapEntries: entries, TotalSitemapURLs: len(entries)}
		return []any{buildSitemapComparisonStats(pages, cfg).UncrawledSitemapURLs, buildAIVisibilityStats(pages)}
	})
}

func TestURLScoresTopNDeterministic(t *testing.T) {
	scores := map[string]float64{}
	for i := 0; i < 100; i++ {
		scores[fmt.Sprintf("https://example.com/%02d", i)] = 1.5
	}
	sameEveryRun(t, func(int) any {
		var s []urlScore
		for u, v := range scores {
			s = append(s, urlScore{u, v})
		}
		sortURLScores(s)
		return fmt.Sprint(s[:10])
	})
}

func TestCentralitySamplingDeterministic(t *testing.T) {
	a := sampleSourceNodes(20000, 5000)
	for i := 0; i < 5; i++ {
		if b := sampleSourceNodes(20000, 5000); !reflect.DeepEqual(a, b) {
			t.Fatal("sampled source nodes differ between runs")
		}
	}
	if len(a) != 500 {
		t.Fatalf("sample size = %d, want 500", len(a))
	}
}

func TestSimilarityEdgesIndependentOfArrivalOrder(t *testing.T) {
	var edges []simEdge
	for i := int32(0); i < 300; i++ {
		edges = append(edges, simEdge{i, i + 1, 0.9}) // all tied
	}
	ref := func(order []simEdge) ([]simEdge, []simEdge) {
		top := make([]simEdge, 0, maxSimilarPairs+1)
		kept := &edgeMaxHeap{}
		for _, e := range order {
			top = pushTopEdge(top, e)
			kept.keepLowest(e, 50)
		}
		return sortedEdges(top), sortedEdges(*kept)
	}
	wantTop, wantKept := ref(edges)
	for run := 0; run < 20; run++ {
		order := append([]simEdge(nil), edges...)
		rand.New(rand.NewSource(int64(run))).Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		gotTop, gotKept := ref(order)
		if !reflect.DeepEqual(gotTop, wantTop) || !reflect.DeepEqual(gotKept, wantKept) {
			t.Fatalf("run %d: kept edges depend on arrival order", run)
		}
	}
	if wantKept[len(wantKept)-1].i != 49 {
		t.Fatalf("kept should be the 50 lowest pairs, last = %+v", wantKept[len(wantKept)-1])
	}
	if wantTop[0].i != 0 || len(wantTop) != maxSimilarPairs {
		t.Fatalf("top should be the lowest pairs on ties, got first %+v len %d", wantTop[0], len(wantTop))
	}
}

func sortedEdges(e []simEdge) []simEdge {
	out := append([]simEdge(nil), e...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if lessEdgeIndex(out[j], out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestCannibalizationGroupsDeterministic(t *testing.T) {
	var urls []string
	for i := 0; i < 40; i++ {
		urls = append(urls, fmt.Sprintf("https://example.com/%02d", i))
	}
	sameEveryRun(t, func(int) any {
		sim := map[indexPair]float64{}
		for i := 0; i < 40; i += 2 {
			sim[indexPair{i, i + 1}] = 0.95 // 20 groups of 2
		}
		return buildCannibalizationGroups(urls, sim)
	})
}

func TestSubGraphOrdered(t *testing.T) {
	var pages []*types.PageData
	for i := 0; i < 30; i++ {
		var links []string
		for k := 1; k <= 4; k++ {
			links = append(links, fmt.Sprintf("https://example.com/%02d", (i+k)%30))
		}
		pages = append(pages, &types.PageData{URL: fmt.Sprintf("https://example.com/%02d", i), StatusCode: 200, InternalLinks: links})
	}
	g := BuildAdjacencyList(pages)
	sameEveryRun(t, func(int) any { return g.SubGraph(0, 3, 20) })
}

func TestLinkIntelListsDeterministicOnTies(t *testing.T) {
	sameEveryRun(t, func(int) any {
		var pages []*types.PageData
		for i := 0; i < 40; i++ {
			var links []string
			for k := 0; k < 40; k++ {
				if k != i {
					links = append(links, fmt.Sprintf("https://example.com/%02d", k))
				}
			}
			pages = append(pages, &types.PageData{URL: fmt.Sprintf("https://example.com/%02d", i), StatusCode: 200, InternalLinks: links})
		}
		g := BuildAdjacencyList(pages)
		return ComputeLinkDilution(g)
	})
}
