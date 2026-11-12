package logs

import (
	"encoding/json"
	"fmt"
	"testing"
)

// tiedAggregator has many URLs and bots with equal hit counts, so any order
// taken from Go's randomised map iteration shows up across runs.
func tiedAggregator() *aggregator {
	a := newAggregator()
	for i := 0; i < 200; i++ {
		a.urlHits[fmt.Sprintf("/api/item/%03d", i)] = &urlAccum{hits: 4, botHits: 4, status: 200}
		a.urlHits[fmt.Sprintf("/page/%03d", i)] = &urlAccum{hits: 4, botHits: 4, status: 200}
	}
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("bot%02d", i)
		a.stats.BotHits[name] = &BotStats{Hits: 100, StatusCodes: map[int]int64{}}
		hm := &[7][24]int64{}
		hm[0][i] = int64(i + 1)
		a.botHeatmaps[name] = hm
	}
	return a
}

func TestLogListsDeterministicOnTies(t *testing.T) {
	snapshot := func() string {
		a := tiedAggregator()
		st := a.finalize()
		if st.Waste == nil || st.Waste.ByType[WasteAPI] == nil || len(st.Waste.ByType[WasteAPI].TopURLs) != 5 {
			t.Fatalf("want 5 API waste sample URLs, got %+v", st.Waste)
		}
		b, err := json.Marshal(map[string]any{
			"topURLs": st.TopURLs,
			"waste":   st.Waste,
			"heatmap": st.BotHeatmap, // JSON sorts map keys
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := snapshot()
	for run := 1; run < 30; run++ {
		if got := snapshot(); got != first {
			t.Fatalf("run %d differs:\n%s\nvs\n%s", run, got, first)
		}
	}
	a := tiedAggregator()
	st := a.finalize()
	if len(st.BotHeatmap) != 5 {
		t.Fatalf("heatmap bots = %d, want 5", len(st.BotHeatmap))
	}
	for _, want := range []string{"bot00", "bot01", "bot02", "bot03", "bot04"} {
		if _, ok := st.BotHeatmap[want]; !ok {
			t.Fatalf("heatmap missing %s (ties go by name)", want)
		}
	}
}

func TestKeepTopWasteURL(t *testing.T) {
	hits := map[string]*urlAccum{
		"/a": {hits: 1}, "/b": {hits: 5}, "/c": {hits: 5}, "/d": {hits: 3}, "/e": {hits: 9}, "/f": {hits: 1}, "/g": {hits: 2},
	}
	var top []string
	for _, p := range []string{"/g", "/f", "/a", "/d", "/c", "/b", "/e"} {
		top = keepTopWasteURL(top, p, hits[p].hits, hits, 5)
	}
	want := []string{"/e", "/b", "/c", "/d", "/g"}
	if fmt.Sprint(top) != fmt.Sprint(want) {
		t.Fatalf("top = %v, want %v", top, want)
	}
}
