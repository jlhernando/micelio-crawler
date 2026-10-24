package server

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/micelio/micelio/internal/types"
)

// pageQuery is a /pages (or filtered CSV) filter + sort request.
type pageQuery struct {
	search, status, indexable, template, classification, issue, robotsBlocked string
	sortField, sortOrder, segment                                             string
}

func pageQueryFromValues(q url.Values) pageQuery {
	pq := pageQuery{
		search:         strings.ToLower(q.Get("search")),
		status:         q.Get("status"),
		indexable:      q.Get("indexable"),
		template:       q.Get("template"),
		classification: q.Get("classification"),
		issue:          q.Get("issue"),
		robotsBlocked:  q.Get("robotsBlocked"),
		sortField:      q.Get("sort"),
		sortOrder:      q.Get("order"),
		segment:        q.Get("segment"),
	}
	if pq.sortField != "" && !validSortFields[pq.sortField] {
		pq.sortField = "" // ignore unknown sort fields
	}
	if pq.sortOrder != "asc" && pq.sortOrder != "desc" {
		pq.sortOrder = "asc"
	}
	return pq
}

func (pq pageQuery) key() string {
	return strings.Join([]string{pq.search, pq.status, pq.indexable, pq.template, pq.classification,
		pq.issue, pq.robotsBlocked, pq.sortField, pq.sortOrder, pq.segment}, "\x00")
}

// pageIndices returns the indices of pages matching pq, in sort order.
// Ties keep page order, so pagination is stable.
func pageIndices(pages []*types.PageData, pq pageQuery) []int32 {
	noFilter := pq.search == "" &&
		(pq.status == "" || pq.status == "all") &&
		(pq.indexable == "" || pq.indexable == "all") &&
		(pq.template == "" || pq.template == "all") &&
		(pq.classification == "" || pq.classification == "all") &&
		(pq.robotsBlocked == "" || pq.robotsBlocked == "all") &&
		pq.issue == ""

	var dupSet map[string]bool
	if pq.issue == "duplicate-titles" || pq.issue == "duplicate-descriptions" {
		dupSet = computeDuplicateSet(pages, pq.issue)
	}
	segDim, segValue, segOK := strings.Cut(pq.segment, ":")
	segOK = segOK && segDim != ""

	idx := make([]int32, 0, len(pages))
	for i, p := range pages {
		if !noFilter && !pageMatchesFilters(p, pq.search, pq.status, pq.indexable, pq.template, pq.classification, pq.issue, pq.robotsBlocked, dupSet) {
			continue
		}
		if segOK && !pageInSegment(p, segDim, segValue) {
			continue
		}
		idx = append(idx, int32(i))
	}
	sortPageIndices(pages, idx, pq.sortField, pq.sortOrder)
	return idx
}

// sortPageIndices sorts idx (indices into pages) by field. Text keys are
// lower-cased once up front, not on every comparison.
func sortPageIndices(pages []*types.PageData, idx []int32, field, order string) {
	if field == "" {
		return
	}
	var cmp func(a, b *types.PageData) int
	num := func(x, y float64) int {
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
	switch field {
	case "url":
		cmp = func(a, b *types.PageData) int { return strings.Compare(a.URL, b.URL) }
	case "statusCode":
		cmp = func(a, b *types.PageData) int { return a.StatusCode - b.StatusCode }
	case "responseTimeMs":
		cmp = func(a, b *types.PageData) int { return num(float64(a.ResponseTimeMs), float64(b.ResponseTimeMs)) }
	case "title":
		keys := make(map[int32]string, len(idx))
		for _, i := range idx {
			if t := pages[i].Title; t != nil {
				keys[i] = strings.ToLower(t.Text)
			}
		}
		less := func(x, y int32) int { return strings.Compare(keys[x], keys[y]) }
		desc := order == "desc"
		sort.SliceStable(idx, func(i, j int) bool {
			c := less(idx[i], idx[j])
			if desc {
				return c > 0
			}
			return c < 0
		})
		return
	case "wordCount":
		cmp = func(a, b *types.PageData) int { return a.WordCount - b.WordCount }
	case "depth":
		cmp = func(a, b *types.PageData) int { return a.Depth - b.Depth }
	case "inlinks":
		cmp = func(a, b *types.PageData) int { return a.Inlinks - b.Inlinks }
	case "pageRank":
		cmp = func(a, b *types.PageData) int { return num(a.PageRank, b.PageRank) }
	case "htmlBytes", "totalBytes":
		get := func(p *types.PageData) float64 {
			if p.PageWeight == nil {
				return 0
			}
			if field == "htmlBytes" {
				return float64(p.PageWeight.HTMLBytes)
			}
			return float64(p.PageWeight.TotalBytes)
		}
		cmp = func(a, b *types.PageData) int { return num(get(a), get(b)) }
	case "bodySize":
		cmp = func(a, b *types.PageData) int { return num(float64(a.BodySize), float64(b.BodySize)) }
	default:
		return
	}
	desc := order == "desc"
	sort.SliceStable(idx, func(i, j int) bool {
		c := cmp(pages[idx[i]], pages[idx[j]])
		if desc {
			return c > 0
		}
		return c < 0
	})
}

// pageViewCache keeps recent filter/sort results per crawl as page indices
// (4 bytes per row), so paging through a sorted or filtered table does not
// copy and re-sort every page on each request.
type pageViewCache struct {
	mu      sync.Mutex
	gen     string
	entries map[string][]int32
	order   []string
}

const maxPageViews = 8

// pageView returns cached indices for pq, computing them on a miss. The
// cache resets whenever the page set changes (pages replaced or reloaded,
// deferred analysis finished). Caller holds r.mu (read).
func (r *CrawlResults) pageView(pq pageQuery) []int32 {
	gen := ""
	if len(r.Pages) > 0 {
		gen = fmt.Sprintf("%p/%d/%t/%t", r.Pages[0], len(r.Pages), r.AnalysisPending, r.PagesReady)
	}
	key := pq.key()
	c := &r.views
	c.mu.Lock()
	if c.gen != gen {
		c.gen, c.entries, c.order = gen, nil, nil
	}
	if idx, ok := c.entries[key]; ok {
		c.mu.Unlock()
		return idx
	}
	c.mu.Unlock()

	idx := pageIndices(r.Pages, pq)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen != gen {
		return idx // pages changed meanwhile; do not cache a stale view
	}
	if c.entries == nil {
		c.entries = map[string][]int32{}
	}
	if _, ok := c.entries[key]; !ok {
		c.order = append(c.order, key)
		if len(c.order) > maxPageViews {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.entries[key] = idx
	return idx
}
