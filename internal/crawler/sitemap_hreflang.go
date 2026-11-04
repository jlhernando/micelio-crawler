package crawler

import (
	"fmt"
	"os"

	"github.com/micelio/micelio/internal/types"
)

// sitemapHreflangSaver is the crawl DB side of sitemap hreflang storage.
type sitemapHreflangSaver interface {
	SaveSitemapHreflang(h *types.SitemapHreflang) error
}

// saveSitemapHreflang stores the sitemap alternates in the crawl DB so
// `micelio import` sees them. Runs once the sitemaps are read.
func (s *crawlSession) saveSitemapHreflang() {
	n := s.sitemapHreflang.Len()
	if n == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "  [sitemap] hreflang alternates for %d URL(s) (%d distinct sets)\n", n, len(s.sitemapHreflang.Sets))
	if saver, ok := s.crawlDB.(sitemapHreflangSaver); ok {
		if err := saver.SaveSitemapHreflang(&s.sitemapHreflang); err != nil {
			fmt.Fprintf(os.Stderr, "  [sitemap] save hreflang: %v\n", err)
		}
	}
}

func (s *crawlSession) sitemapHreflangResult() *types.SitemapHreflang {
	if s.sitemapHreflang.Len() == 0 {
		return nil
	}
	return &s.sitemapHreflang
}
