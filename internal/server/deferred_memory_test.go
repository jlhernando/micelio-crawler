package server

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/types"
)

// deferredBigPages builds n linked pages carrying the structs deferred
// analysis writes.
func deferredBigPages(n int) []*types.PageData {
	pages := make([]*types.PageData, n)
	for i := range pages {
		pages[i] = &types.PageData{
			URL:          fmt.Sprintf("https://ex.com/p%d", i),
			StatusCode:   200,
			Title:        &types.TextLength{Text: "Page"},
			Topicality:   &types.TopicalityData{},
			AutoSegments: &types.PageSegments{Dir1: "p"},
			Inlinks:      1 + i%10,
			InternalLinks: []string{
				fmt.Sprintf("https://ex.com/p%d", (i+1)%n),
				fmt.Sprintf("https://ex.com/p%d", (i+7)%n),
			},
		}
	}
	return pages
}

func allocated(f func()) int64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return int64(after.TotalAlloc - before.TotalAlloc)
}

// Deferred analysis used to copy every page (~1.6 KB each with the structs
// it owns) even with link intelligence off. Now it copies nothing without
// link intelligence and, with it, only the page slice; the structs stubs
// share with cached pages are copied only where a value changes.
func TestDeferredWorkMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 100K pages")
	}
	const n = 100_000
	pages := deferredBigPages(n)
	analysis.AssignAuthoritySegments(pages, nil) // as the core analysis does
	old := allocated(func() {                    // what PR #101 did
		work := make([]*types.PageData, len(pages))
		for i, p := range pages {
			cp := *p
			top := *p.Topicality
			cp.Topicality = &top
			seg := *p.AutoSegments
			cp.AutoSegments = &seg
			work[i] = &cp
		}
		runtime.KeepAlive(work)
	})

	res := &CrawlResults{Pages: pages}
	cfg := analysis.PostProcessConfig{}
	off := allocated(func() { runtime.KeepAlive(deferredWork(res, &cfg)) })
	if off > 64<<10 {
		t.Errorf("link intelligence off: %d bytes allocated, want ~0", off)
	}

	cfg = analysis.PostProcessConfig{LinkIntelligence: true, Graph: analysis.BuildLinkGraph(pages, nil)}
	var work []*types.PageData
	on := allocated(func() { work = deferredWork(res, &cfg) })
	if on > 16*n {
		t.Errorf("link intelligence on: %d bytes allocated before the analysis, want ~8/page", on)
	}

	segs := make([]*types.PageSegments, n)
	for i, p := range pages {
		segs[i] = p.AutoSegments
	}
	run := allocated(func() {
		var st types.CrawlStats
		analysis.RunDeferredAnalysis(context.Background(), work, cfg, &st)
	})
	// Copy-on-write of the shared structs: unchanged buckets copy nothing
	// (the bytes are the sorted value list the bucketing always builds).
	reseg := allocated(func() { analysis.AssignAuthoritySegmentsShared(work, nil) })
	copied := 0
	for i, p := range pages {
		if p == work[i] {
			t.Fatal("cached page not replaced by a stub")
		}
		if p.AutoSegments != segs[i] {
			t.Fatal("cached page written")
		}
		if work[i].AutoSegments != segs[i] {
			copied++
		}
	}
	t.Logf("100K pages: PR #101 copy %.1f MB; now LI off %d B, LI on %.2f MB slice; authority refresh %.1f MB, %d structs copied (link intelligence itself %.1f MB)",
		float64(old)/1e6, off, float64(on)/1e6, float64(reseg)/1e6, copied, float64(run)/1e6)
	if copied != 0 {
		t.Errorf("%d unchanged authority buckets copied", copied)
	}
}

// A page whose authority bucket changes gets its own AutoSegments; the
// struct the cached page holds is left alone.
func TestAssignAuthoritySegmentsSharedCopiesOnWrite(t *testing.T) {
	cached := []*types.PageData{
		{URL: "a", StatusCode: 200, PageRank: 1, AutoSegments: &types.PageSegments{Authority: "low"}},
		{URL: "b", StatusCode: 200, PageRank: 2, AutoSegments: &types.PageSegments{Authority: "medium"}},
		{URL: "c", StatusCode: 200, PageRank: 3, AutoSegments: &types.PageSegments{Authority: "low"}},
	}
	stubs := make([]*types.PageData, len(cached))
	for i, p := range cached {
		cp := *p
		stubs[i] = &cp
	}
	analysis.AssignAuthoritySegmentsShared(stubs, nil)
	if stubs[1].AutoSegments != cached[1].AutoSegments {
		t.Error("unchanged bucket was copied")
	}
	if stubs[2].AutoSegments == cached[2].AutoSegments || stubs[2].AutoSegments.Authority != "medium" {
		t.Errorf("changed bucket: got %q, shared %v", stubs[2].AutoSegments.Authority, stubs[2].AutoSegments == cached[2].AutoSegments)
	}
	if cached[2].AutoSegments.Authority != "low" {
		t.Error("cached struct written")
	}
}
