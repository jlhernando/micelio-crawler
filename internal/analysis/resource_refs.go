package analysis

import (
	"context"
	"strconv"

	"github.com/micelio/micelio/internal/types"
)

// ResourceRefs stores each distinct (URL, type, size) entry once in Table
// (shared, never modified) and per page the indexes of its resources in
// order: 4 bytes per reference instead of 40 (25K pages of a large site: ~3M refs).
type ResourceRefs struct {
	Table []types.ResourceEntry
	Pages map[string][]uint32
	idx   map[string]uint32 // entry key -> index while adding
}

// NewResourceRefs returns empty refs to Add pages to.
func NewResourceRefs() *ResourceRefs {
	return &ResourceRefs{Pages: make(map[string][]uint32), idx: make(map[string]uint32)}
}

// CompactResourceRefs builds ResourceRefs from the per-page map form.
func CompactResourceRefs(m map[string][]types.ResourceEntry) *ResourceRefs {
	r := NewResourceRefs()
	for p, refs := range m {
		r.Add(p, refs)
	}
	return r.Done()
}

// Add stores a page's resources (nil stays nil, empty stays empty).
func (r *ResourceRefs) Add(pageURL string, refs []types.ResourceEntry) {
	var ids []uint32
	if refs != nil {
		ids = make([]uint32, len(refs))
	}
	var key []byte
	for i, e := range refs {
		key = append(append(append(key[:0], e.URL...), 0), e.Type...)
		if e.SizeBytes != nil {
			key = strconv.AppendInt(append(key, 0), *e.SizeBytes, 10)
		}
		id, ok := r.idx[string(key)]
		if !ok {
			id = uint32(len(r.Table))
			if e.SizeBytes != nil {
				v := *e.SizeBytes
				e.SizeBytes = &v
			}
			r.Table = append(r.Table, e)
			r.idx[string(key)] = id
		}
		ids[i] = id
	}
	r.Pages[pageURL] = ids
}

// Done ends adding: frees the key index and trims the table.
func (r *ResourceRefs) Done() *ResourceRefs {
	r.idx = nil
	r.Table = append([]types.ResourceEntry(nil), r.Table...)
	return r
}

// Len is the number of pages (0 for nil).
func (r *ResourceRefs) Len() int {
	if r == nil {
		return 0
	}
	return len(r.Pages)
}

// Expand returns the per-page map form: each page has its own slice and
// SizeBytes pointers; URL and type strings are shared.
func (r *ResourceRefs) Expand() map[string][]types.ResourceEntry {
	if r == nil {
		return nil
	}
	out := make(map[string][]types.ResourceEntry, len(r.Pages))
	for p, ids := range r.Pages {
		var refs []types.ResourceEntry
		if ids != nil {
			refs = make([]types.ResourceEntry, len(ids))
		}
		for i, id := range ids {
			refs[i] = r.Table[id]
			if s := refs[i].SizeBytes; s != nil {
				v := *s
				refs[i].SizeBytes = &v
			}
		}
		out[p] = refs
	}
	return out
}

// FetchResourceSizesCompact is FetchResourceSizes for ResourceRefs (not nil).
func FetchResourceSizesCompact(ctx context.Context, r *ResourceRefs, opts PageWeightOptions) (map[string]int64, PageWeightSummary) {
	counts := make(map[string]int)
	for _, ids := range r.Pages {
		for _, id := range ids {
			counts[r.Table[id].URL]++
		}
	}
	return fetchResourceSizeCounts(ctx, counts, opts)
}

// ApplyResourceSizesCompact sets the PageWeight ApplyResourceSizes sets on
// the expanded refs, but leaves Resources nil: the dashboard drops that
// list unread (stripPagesForCache, dropResourceDetail, crawl DB writes).
func ApplyResourceSizesCompact(pages []*types.PageData, r *ResourceRefs, transferSizes map[string]int64, sizeCache map[string]int64) {
	if r == nil {
		return
	}
	pageMap := make(map[string]*types.PageData, len(pages))
	for _, p := range pages {
		pageMap[p.URL] = p
	}
	for pageURL, ids := range r.Pages {
		page, ok := pageMap[pageURL]
		if !ok {
			continue
		}
		htmlBytes := transferSizes[pageURL]
		if htmlBytes == 0 {
			htmlBytes = page.BodySize
		}
		byType := map[string]types.TypeWeightSummary{"html": {Count: 1, Bytes: htmlBytes}}
		totalBytes := htmlBytes
		for _, id := range ids {
			e := &r.Table[id]
			size, ok := sizeCache[e.URL]
			if !ok && e.SizeBytes != nil {
				size = *e.SizeBytes
			}
			totalBytes += size
			entry := byType[string(e.Type)]
			entry.Count++
			entry.Bytes += size
			byType[string(e.Type)] = entry
		}
		page.PageWeight = &types.PageWeightData{TotalBytes: totalBytes, HTMLBytes: htmlBytes, ByType: byType}
	}
}
