package storage

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestLinkGraphAndScoresRoundTrip(t *testing.T) {
	cs, err := NewCrawlStore(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	// More edges than one chunk so chunking is exercised.
	const n = 1000
	edges := make([]int32, analysisChunkLen+12345)
	offsets := make([]int32, n+1)
	for i := range edges {
		edges[i] = int32(i % n)
	}
	for i := 1; i <= n; i++ {
		offsets[i] = int32(len(edges) * i / n)
	}
	if err := cs.SaveLinkGraph(42, offsets, edges); err != nil {
		t.Fatal(err)
	}
	o2, e2, ok, err := cs.LoadLinkGraph(42, n)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if !slices.Equal(o2, offsets) || !slices.Equal(e2, edges) {
		t.Error("graph round trip mismatch")
	}
	if _, _, ok, _ := cs.LoadLinkGraph(43, n); ok {
		t.Error("loaded graph for a different URL hash")
	}
	if _, _, ok, _ := cs.LoadLinkGraph(42, n+1); ok {
		t.Error("loaded graph for a different page count")
	}

	pr := []float64{1.5, 0, 10}
	if err := cs.SavePageScores(7, pr, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	p2, a2, h2, ok, err := cs.LoadPageScores(7, 3)
	if err != nil || !ok || !slices.Equal(p2, pr) || a2 != nil || h2 != nil {
		t.Errorf("scores: %v %v %v ok=%v err=%v", p2, a2, h2, ok, err)
	}
	// Overwrite with HITS.
	if err := cs.SavePageScores(7, pr, []float64{.1, .2, .3}, []float64{.4, .5, .6}, true); err != nil {
		t.Fatal(err)
	}
	_, a2, h2, ok, _ = cs.LoadPageScores(7, 3)
	if !ok || !slices.Equal(a2, []float64{.1, .2, .3}) || !slices.Equal(h2, []float64{.4, .5, .6}) {
		t.Errorf("hits: %v %v", a2, h2)
	}
}
