package analysis

import (
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/micelio/micelio/internal/types"
)


// anchorFixture builds a small site where pages carry full Anchors.
func anchorFixture() []*types.PageData {
	base := "https://ex.com"
	mk := func(path string, anchors ...types.AnchorData) *types.PageData {
		p := &types.PageData{URL: base + path, StatusCode: 200, Title: &types.TextLength{Text: "Page " + path}}
		seen := map[string]bool{}
		for _, a := range anchors {
			if a.IsInternal && !seen[a.Href] {
				seen[a.Href] = true
				p.InternalLinks = append(p.InternalLinks, a.Href)
			}
		}
		p.Anchors = anchors
		p.NofollowIntLinks = countNofollowForTest(anchors)
		p.Topicality = &types.TopicalityData{TitleBodyOverlap: 0.5}
		return p
	}
	in := func(path, text string) types.AnchorData {
		return types.AnchorData{Href: base + path, Text: text, IsInternal: true, IsNonDescriptive: isNonDescriptive(strings.ToLower(text))}
	}
	nf := func(path, text string) types.AnchorData {
		a := in(path, text)
		a.Rel = strPtr("NoFollow")
		return a
	}
	ext := func(href, text string) types.AnchorData {
		return types.AnchorData{Href: href, Text: text}
	}
	return []*types.PageData{
		mk("/", in("/a", "Shoes"), in("/a", "Shoes"), in("/b/", "Click here"), nf("/c", "Login"), ext("https://other.com", "Partner")),
		mk("/a", in("/", "Home"), in("/b", "Read more"), in("/b", "https://ex.com/b"), in("/c", "")),
		mk("/b", in("/", "Home"), in("/a", "Shoes"), in("/a", "Red shoes"), in("/a", "Shoes"), in("/a", "Shoes"), in("/a", "Shoes"), nf("/c", "Login")),
		mk("/c", in("/", "Home page"), in("/a", "here")),
	}
}

func countNofollowForTest(anchors []types.AnchorData) int {
	set := map[string]bool{}
	for _, a := range anchors {
		if a.IsInternal && a.Rel != nil && strings.Contains(strings.ToLower(*a.Rel), "nofollow") {
			set[a.Href] = true
		}
	}
	return len(set)
}

// stripAndAggregate feeds pages into an aggregator then drops their Anchors,
// mimicking the crawler (storePage) + DB reload flow.
func stripAndAggregate(pages []*types.PageData) *AnchorAggregator {
	agg := NewAnchorAggregator()
	for _, p := range pages {
		agg.AddPage(p.URL, p.StatusCode, p.Anchors)
		p.Anchors = nil
	}
	return agg
}

func sortedNonDesc(in []types.NonDescriptiveAnchor) []types.NonDescriptiveAnchor {
	out := append([]types.NonDescriptiveAnchor(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].URL+out[i].Text+out[i].FoundOn < out[j].URL+out[j].Text+out[j].FoundOn
	})
	return out
}

func TestAnchorAggregatorMatchesInMemoryBuilders(t *testing.T) {
	want := GenerateReport(anchorFixture(), 0, ReportConfig{})
	wantPages := anchorFixture()
	GenerateReport(wantPages, 0, ReportConfig{})

	pages := anchorFixture()
	agg := stripAndAggregate(pages)
	got := GenerateReport(pages, 0, ReportConfig{AnchorAggregator: agg})

	if want.AnchorCloud == nil || len(want.AnchorCloud.Anchors) == 0 {
		t.Fatal("in-memory anchor cloud should not be empty")
	}
	if !reflect.DeepEqual(want.AnchorCloud, got.AnchorCloud) {
		t.Errorf("anchor cloud mismatch\nwant %+v\ngot  %+v", want.AnchorCloud, got.AnchorCloud)
	}
	if len(want.NonDescriptiveAnchors) == 0 {
		t.Fatal("expected non-descriptive anchors in fixture")
	}
	if !reflect.DeepEqual(sortedNonDesc(want.NonDescriptiveAnchors), sortedNonDesc(got.NonDescriptiveAnchors)) {
		t.Errorf("non-descriptive mismatch\nwant %+v\ngot  %+v", want.NonDescriptiveAnchors, got.NonDescriptiveAnchors)
	}
	if want.AnchorHealthStats == nil || got.AnchorHealthStats == nil {
		t.Fatalf("anchor health stats nil: want=%v got=%v", want.AnchorHealthStats, got.AnchorHealthStats)
	}
	if *want.AnchorHealthStats != *got.AnchorHealthStats {
		t.Errorf("anchor health stats mismatch\nwant %+v\ngot  %+v", *want.AnchorHealthStats, *got.AnchorHealthStats)
	}
	for i := range pages {
		if !reflect.DeepEqual(wantPages[i].AnchorHealth, pages[i].AnchorHealth) {
			t.Errorf("page %s anchor health: want %+v got %+v", pages[i].URL, wantPages[i].AnchorHealth, pages[i].AnchorHealth)
		}
	}
	if want.LinkAnalysis.NofollowedInternalLinks != 2 {
		t.Errorf("fixture nofollowed = %d, want 2", want.LinkAnalysis.NofollowedInternalLinks)
	}
	if want.LinkAnalysis.NofollowedInternalLinks != got.LinkAnalysis.NofollowedInternalLinks ||
		want.LinkAnalysis.FollowedInternalLinks != got.LinkAnalysis.FollowedInternalLinks {
		t.Errorf("link analysis mismatch: want %d/%d got %d/%d",
			want.LinkAnalysis.NofollowedInternalLinks, want.LinkAnalysis.FollowedInternalLinks,
			got.LinkAnalysis.NofollowedInternalLinks, got.LinkAnalysis.FollowedInternalLinks)
	}
	// Page /a receives "Shoes" 5 of 6 meaningful anchors -> over-optimized.
	if got.AnchorHealthStats.PagesOverOptimized != 1 {
		t.Errorf("PagesOverOptimized = %d, want 1", got.AnchorHealthStats.PagesOverOptimized)
	}
}

func TestNofollowCountsWithStrippedLinks(t *testing.T) {
	pages := anchorFixture()
	stripAndAggregate(pages)
	for _, p := range pages {
		p.InternalLinkCount = len(p.InternalLinks)
		p.InternalLinks = nil
	}
	la := buildLinkAnalysis(pages)
	if la.NofollowedInternalLinks != 2 {
		t.Errorf("nofollowed = %d, want 2", la.NofollowedInternalLinks)
	}
	total := 0
	for _, p := range pages {
		total += p.InternalLinkCount
	}
	if la.FollowedInternalLinks != total-2 {
		t.Errorf("followed = %d, want %d", la.FollowedInternalLinks, total-2)
	}
}

func TestGenerateReportWithoutAnchorsOrAggregator(t *testing.T) {
	pages := anchorFixture()
	for _, p := range pages {
		p.Anchors = nil
	}
	stats := GenerateReport(pages, 0, ReportConfig{})
	if stats.AnchorCloud != nil || stats.AnchorHealthStats != nil || len(stats.NonDescriptiveAnchors) != 0 {
		t.Errorf("expected empty anchor stats, got cloud=%v health=%v nondesc=%d",
			stats.AnchorCloud, stats.AnchorHealthStats, len(stats.NonDescriptiveAnchors))
	}
}

func TestAnchorAggregatorPerTargetCap(t *testing.T) {
	agg := NewAnchorAggregator()
	var anchors []types.AnchorData
	for i := 0; i < maxTextsPerTarget+10; i++ {
		anchors = append(anchors, types.AnchorData{Href: "https://ex.com/t", Text: fmt.Sprintf("text %d", i), IsInternal: true})
	}
	agg.AddPage("https://ex.com/", 200, anchors)
	page := &types.PageData{URL: "https://ex.com/t/", StatusCode: 200}
	agg.ApplyAnchorHealth([]*types.PageData{page})
	if page.AnchorHealth == nil {
		t.Fatal("expected anchor health for target (trailing slash normalised)")
	}
	if page.AnchorHealth.InboundAnchorDiversity != 1 {
		t.Errorf("diversity = %v, want 1 (overflow counted as distinct)", page.AnchorHealth.InboundAnchorDiversity)
	}
	tgt := agg.targets[onlyTarget(agg)]
	if len(tgt.texts) != maxTextsPerTarget || len(tgt.overflowHashes) != 10 {
		t.Errorf("texts=%d overflow=%d", len(tgt.texts), len(tgt.overflowHashes))
	}
}

func TestAnchorAggregatorOverflowCountsTextsOnce(t *testing.T) {
	agg := NewAnchorAggregator()
	// 1000 inbound anchors that reuse 20 distinct texts: diversity must be 20/1000.
	for i := 0; i < 1000; i++ {
		anchor := types.AnchorData{Href: "https://ex.com/", Text: fmt.Sprintf("text %d", i%20), IsInternal: true}
		agg.AddPage(fmt.Sprintf("https://ex.com/p%d", i), 200, []types.AnchorData{anchor})
	}
	page := &types.PageData{URL: "https://ex.com/", StatusCode: 200}
	agg.ApplyAnchorHealth([]*types.PageData{page})
	if got := page.AnchorHealth.InboundAnchorDiversity; got != 0.02 {
		t.Errorf("diversity = %v, want 0.02", got)
	}
}

func onlyTarget(a *AnchorAggregator) uint64 {
	var found uint64
	for h := range a.targets {
		found = h
	}
	return found
}

func TestAnchorAggregatorNonDescriptiveCapAndNonOK(t *testing.T) {
	agg := NewAnchorAggregator()
	for i := 0; i < 80; i++ {
		agg.AddPage(fmt.Sprintf("https://ex.com/p%d", i), 200, []types.AnchorData{
			{Href: "https://ex.com/x", Text: "Click here", IsInternal: true},
			{Href: "https://ex.com/x", Text: "Click here", IsInternal: true}, // duplicate on same page
		})
	}
	if n := len(agg.NonDescriptiveAnchors()); n != maxNonDescriptiveAnchors {
		t.Errorf("non-descriptive = %d, want %d", n, maxNonDescriptiveAnchors)
	}
	// 404 sources do not feed anchor health.
	agg2 := NewAnchorAggregator()
	agg2.AddPage("https://ex.com/404", 404, []types.AnchorData{{Href: "https://ex.com/x", Text: "X", IsInternal: true}})
	if len(agg2.targets) != 0 {
		t.Errorf("non-200 source should not add targets")
	}
	if c := agg2.AnchorCloud(); c == nil || c.TotalUnique != 0 {
		// "x" is 1 rune: excluded from the cloud.
		t.Errorf("cloud = %+v", c)
	}
}

func TestAnchorAggregatorTextPruning(t *testing.T) {
	agg := NewAnchorAggregator()
	// A frequent text must survive pruning.
	frequent := make([]types.AnchorData, 5)
	for i := range frequent {
		frequent[i] = types.AnchorData{Href: "https://ex.com/", Text: "Home", IsInternal: true}
	}
	agg.AddPage("https://ex.com/a", 200, frequent)
	n := maxAnchorTexts + 1000
	batch := make([]types.AnchorData, 0, 1000)
	for i := 0; i < n; i++ {
		batch = append(batch, types.AnchorData{Href: "https://other.com/", Text: fmt.Sprintf("unique anchor %d", i)})
		if len(batch) == cap(batch) {
			agg.AddPage("https://ex.com/b", 200, batch)
			batch = batch[:0]
		}
	}
	if len(agg.texts) > maxAnchorTexts {
		t.Errorf("text table %d exceeds cap %d", len(agg.texts), maxAnchorTexts)
	}
	cloud := agg.AnchorCloud()
	if cloud.Anchors[0].Text != "home" || cloud.Anchors[0].Count != 5 {
		t.Errorf("top anchor = %+v, want home x5", cloud.Anchors[0])
	}
	want := n + 1
	if diff := float64(cloud.TotalUnique-want) / float64(want); diff > 0.05 || diff < -0.05 {
		t.Errorf("TotalUnique estimate %d too far from %d", cloud.TotalUnique, want)
	}
	if len(cloud.Anchors) != anchorCloudLimit {
		t.Errorf("cloud entries = %d, want %d", len(cloud.Anchors), anchorCloudLimit)
	}
}

func TestEnrichTopicalityAnchorsFromAggregator(t *testing.T) {
	pages := anchorFixture()
	agg := stripAndAggregate(pages)
	graph := BuildAdjacencyList(pages)
	EnrichTopicalityAnchors(pages, graph, agg)
	// Page "/" (title "Page /") receives Home / Home page anchors: no term overlap.
	// Page "/a" receives "Shoes"/"Red shoes": no overlap with "Page /a" either,
	// so check a page whose title matches its anchors.
	pages[1].Title = &types.TextLength{Text: "Shoes"}
	pages[1].Topicality.AnchorTitleRelevance = 0
	EnrichTopicalityAnchors(pages, graph, agg)
	// Inbound to /a: Shoes x6 (rel 1), Red shoes x1 (rel 0.5), here (non-descriptive, skipped).
	want := (6*1.0 + 0.5) / 7
	got := pages[1].Topicality.AnchorTitleRelevance
	if d := got - want; d > 0.001 || d < -0.001 {
		t.Errorf("AnchorTitleRelevance = %v, want %.3f", got, want)
	}
}

func TestAnchorAggregatorConcurrent(t *testing.T) {
	agg := NewAnchorAggregator()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				agg.AddPage(fmt.Sprintf("https://ex.com/%d/%d", w, i), 200, []types.AnchorData{
					{Href: "https://ex.com/", Text: "Home", IsInternal: true},
				})
			}
		}(w)
	}
	wg.Wait()
	if agg.Pages() != 1600 {
		t.Errorf("pages = %d", agg.Pages())
	}
	if c := agg.AnchorCloud(); c.Anchors[0].Count != 1600 {
		t.Errorf("home count = %d", c.Anchors[0].Count)
	}
}

func BenchmarkAnchorAggregatorAddPage(b *testing.B) {
	anchors := make([]types.AnchorData, 100)
	for j := range anchors {
		anchors[j] = types.AnchorData{Href: fmt.Sprintf("https://ex.com/p/%d", j*31), Text: fmt.Sprintf("Product %d", j), IsInternal: true}
	}
	agg := NewAnchorAggregator()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		agg.AddPage("https://ex.com/", 200, anchors)
	}
}

// TestAnchorAggregatorMemoryPerPage reports aggregator heap per crawled page on
// a template-heavy synthetic site (100 anchors/page: 40 nav, 60 content links).
func TestAnchorAggregatorMemoryPerPage(t *testing.T) {
	if testing.Short() {
		t.Skip("memory measurement")
	}
	const nPages = 20000
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	agg := NewAnchorAggregator()
	anchors := make([]types.AnchorData, 100)
	for i := 0; i < nPages; i++ {
		for j := 0; j < 40; j++ {
			anchors[j] = types.AnchorData{Href: fmt.Sprintf("https://ex.com/nav/%d", j), Text: fmt.Sprintf("Nav %d", j), IsInternal: true}
		}
		for j := 40; j < 100; j++ {
			target := (i*7 + j*13) % nPages
			anchors[j] = types.AnchorData{Href: fmt.Sprintf("https://ex.com/p/%d", target), Text: fmt.Sprintf("Product %d", target), IsInternal: true}
		}
		agg.AddPage(fmt.Sprintf("https://ex.com/p/%d", i), 200, anchors)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	perPage := float64(after.HeapAlloc-before.HeapAlloc) / nPages
	t.Logf("anchor aggregator: %.0f bytes/page (%d pages, %d targets, %d texts)", perPage, nPages, len(agg.targets), len(agg.texts))
	runtime.KeepAlive(agg)
	if perPage > 1024 {
		t.Errorf("aggregator uses %.0f bytes/page, want < 1KB", perPage)
	}
}
