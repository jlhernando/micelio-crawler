package analysis

import (
	"reflect"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestBuildWordCountBuckets(t *testing.T) {
	if BuildWordCountBuckets(nil) != nil {
		t.Fatal("expected nil for no pages")
	}
	wcs := []int{0, 99, 100, 299, 300, 499, 500, 999, 1000, 1999, 2000, 50000, -5}
	pages := make([]*types.PageData, 0, len(wcs)+1)
	for _, wc := range wcs {
		pages = append(pages, &types.PageData{WordCount: wc})
	}
	pages = append(pages, nil)
	got := BuildWordCountBuckets(pages)
	if got == nil {
		t.Fatal("expected buckets")
	}
	if want := []int{0, 100, 300, 500, 1000, 2000}; !reflect.DeepEqual(got.Edges, want) {
		t.Errorf("edges = %v, want %v", got.Edges, want)
	}
	// -5 is clamped to 0 and lands in the first bucket.
	if want := []int{3, 2, 2, 2, 2, 2}; !reflect.DeepEqual(got.Counts, want) {
		t.Errorf("counts = %v, want %v", got.Counts, want)
	}
	if got.Pages != len(wcs) {
		t.Errorf("pages = %d, want %d", got.Pages, len(wcs))
	}
	total := 0
	for _, wc := range wcs {
		total += max(wc, 0)
	}
	if want := float64(total) / float64(len(wcs)); got.AvgWords != want {
		t.Errorf("avg = %v, want %v", got.AvgWords, want)
	}
}
