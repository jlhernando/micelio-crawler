package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// TestComparisonDeterministicOnTies builds two crawls full of ties (equal
// migration counts, equal PageRank and click deltas, equal n-gram counts,
// equal segment sizes) and checks the comparison is identical on every run,
// although its inputs are collected through Go maps.
func TestComparisonDeterministicOnTies(t *testing.T) {
	build := func() string {
		var oldPages, newPages []*types.PageData
		for i := 0; i < 60; i++ {
			u := fmt.Sprintf("https://example.com/p%02d", i)
			op := makeTestPage(u, 200, "T", true)
			op.PageRank = 1
			op.GscData = &types.GscData{Clicks: 10}
			np := makeTestPage(u, []int{404, 500, 301}[i%3], "", i%2 == 0)
			np.PageRank = 2
			np.GscData = &types.GscData{Clicks: 15}
			oldPages = append(oldPages, op)
			newPages = append(newPages, np)
		}
		var oldBi, newBi []types.NgramEntry
		var oldSeg, newSeg []types.SegmentStat
		for i := 0; i < 40; i++ {
			oldBi = append(oldBi, types.NgramEntry{Term: fmt.Sprintf("old term %02d", i), Count: 5})
			newBi = append(newBi, types.NgramEntry{Term: fmt.Sprintf("new term %02d", i), Count: 5})
			oldSeg = append(oldSeg, types.SegmentStat{Name: fmt.Sprintf("seg%02d", i), PageCount: 3})
			newSeg = append(newSeg, types.SegmentStat{Name: fmt.Sprintf("seg%02d", i), PageCount: 3})
		}
		oldStats := makeTestStats(60, 60, 0, 0, 0)
		newStats := makeTestStats(60, 30, 0, 0, 0)
		oldStats.NgramStats = &types.NgramStats{Bigrams: oldBi}
		newStats.NgramStats = &types.NgramStats{Bigrams: newBi}
		oldStats.SegmentStats = oldSeg
		newStats.SegmentStats = newSeg
		r := ComputeComparison(oldPages, oldStats, newPages, newStats)
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := build()
	for run := 1; run < 30; run++ {
		if got := build(); got != first {
			t.Fatalf("run %d differs from run 0", run)
		}
	}
	if !strings.Contains(first, `"urls":["https://example.com/p00"`) {
		t.Errorf("migration samples should be the first URLs in order: %.300s", first)
	}
}

func TestDiffTextSummaryDeterministic(t *testing.T) {
	d := &DiffResult{FieldSummary: map[string]int{}}
	for i := 0; i < 20; i++ {
		d.FieldSummary[fmt.Sprintf("field%02d", i)] = 3
	}
	var first string
	for run := 0; run < 30; run++ {
		var sb strings.Builder
		PrintDiffSummary(&sb, d)
		if run == 0 {
			first = sb.String()
		} else if sb.String() != first {
			t.Fatalf("run %d differs", run)
		}
	}
}
