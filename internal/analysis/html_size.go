package analysis

import (
	"sort"

	"github.com/micelio/micelio/internal/types"
)

// htmlOver2MBListCap bounds the sample list kept in stats.
const htmlOver2MBListCap = 50

// HTMLOver2MB finds 2xx HTML pages larger than Googlebot's 2 MB fetch limit
// (types.GooglebotHTMLLimit). Googlebot reads only the first 2 MB of an HTML
// file and indexes it as if it were the whole page: text, links and
// structured data after the cutoff are not seen.
//
// The size is PageData.BodySize: the decompressed HTML body as served, before
// any JS rendering. Google applies the limit to uncompressed bytes, so a
// 600 KB gzip transfer of a 2.5 MB document is still cut. Google counts the
// HTTP response headers too; Micelio does not keep them per page, so a page
// a few KB under 2 MB may still be cut. A body that hit the crawler's read
// cap (types.HTMLBodyReadCap) is flagged AtLeast.
//
// Returns the biggest pages first (capped at 50) and the full count.
func HTMLOver2MB(pages []*types.PageData) ([]types.HTMLSizeEntry, int) {
	var out []types.HTMLSizeEntry
	for _, p := range pages {
		if p == nil || p.StatusCode < 200 || p.StatusCode >= 300 || p.BodySize <= types.GooglebotHTMLLimit {
			continue
		}
		out = append(out, types.HTMLSizeEntry{
			URL:         p.URL,
			Bytes:       p.BodySize,
			AtLeast:     p.BodySize >= types.HTMLBodyReadCap,
			CutElements: p.TruncatedElements,
		})
	}
	total := len(out)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].URL < out[j].URL
	})
	if len(out) > htmlOver2MBListCap {
		out = append([]types.HTMLSizeEntry(nil), out[:htmlOver2MBListCap]...)
	}
	return out, total
}
