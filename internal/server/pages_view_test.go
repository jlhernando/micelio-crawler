package server

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func viewTestPages(n int) []*types.PageData {
	pages := make([]*types.PageData, n)
	for i := range pages {
		title := "Title " + strconv.Itoa((i*7919)%n)
		if i%3 == 0 {
			title = strings.ToUpper(title)
		}
		pages[i] = &types.PageData{
			URL:        "https://ex.com/p" + strconv.Itoa(i),
			StatusCode: []int{200, 200, 301, 404}[i%4],
			Title:      &types.TextLength{Text: title, Length: len(title)},
			Depth:      i % 6,
			PageRank:   float64((i * 31) % 97),
		}
	}
	return pages
}

func TestPageViewMatchesSortedFilter(t *testing.T) {
	pages := viewTestPages(500)
	r := &CrawlResults{Pages: pages, PagesReady: true}
	q := url.Values{"status": {"2xx"}, "sort": {"title"}, "order": {"desc"}}
	got := r.pageView(pageQueryFromValues(q))

	var want []*types.PageData
	for _, p := range pages {
		if p.StatusCode == 200 {
			want = append(want, p)
		}
	}
	sort.SliceStable(want, func(i, j int) bool {
		return strings.ToLower(want[i].Title.Text) > strings.ToLower(want[j].Title.Text)
	})
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d", len(got), len(want))
	}
	for k := range want {
		if pages[got[k]] != want[k] {
			t.Fatalf("row %d: %s, want %s", k, pages[got[k]].URL, want[k].URL)
		}
	}

	// Cached: the same slice comes back.
	again := r.pageView(pageQueryFromValues(q))
	if &again[0] != &got[0] {
		t.Error("second request was not served from the cache")
	}
	// New page set: cache resets.
	r.Pages = viewTestPages(500)
	fresh := r.pageView(pageQueryFromValues(q))
	if len(fresh) > 0 && &fresh[0] == &got[0] {
		t.Error("cache not invalidated after pages changed")
	}
	// Deferred analysis finishing also resets it.
	r.AnalysisPending = true
	a := r.pageView(pageQueryFromValues(q))
	r.AnalysisPending = false
	if b := r.pageView(pageQueryFromValues(q)); &a[0] == &b[0] {
		t.Error("cache not invalidated after analysis state changed")
	}
}

func TestPageViewCacheBounded(t *testing.T) {
	r := &CrawlResults{Pages: viewTestPages(50), PagesReady: true}
	for i := 0; i < 20; i++ {
		r.pageView(pageQueryFromValues(url.Values{"search": {strconv.Itoa(i)}}))
	}
	if len(r.views.entries) > maxPageViews || len(r.views.order) > maxPageViews {
		t.Errorf("cache holds %d entries", len(r.views.entries))
	}
}

// BenchmarkPagesSortedPage compares serving one page of a title-sorted table
// the old way (copy + sort all pages, lower-casing in the comparator) with
// the cached index view.
func BenchmarkPagesSortedPage(b *testing.B) {
	pages := viewTestPages(200000)
	q := url.Values{"sort": {"title"}, "order": {"asc"}}
	b.Run("old", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			out := make([]*types.PageData, len(pages))
			copy(out, pages)
			sort.Slice(out, func(i, j int) bool {
				return strings.ToLower(out[i].Title.Text) < strings.ToLower(out[j].Title.Text)
			})
			_ = out[100:200]
		}
	})
	r := &CrawlResults{Pages: pages, PagesReady: true}
	b.Run("cached", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			v := r.pageView(pageQueryFromValues(q))
			rows := make([]*types.PageData, 0, 100)
			for _, k := range v[100:200] {
				rows = append(rows, pages[k])
			}
		}
	})
}
