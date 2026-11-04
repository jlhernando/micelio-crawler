package storage

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// 150K sitemap URLs in 27-locale clusters survive a round trip through the
// crawl DB, sharing one stored set per cluster.
func TestSitemapHreflangRoundTrip(t *testing.T) {
	store, err := NewCrawlStore(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if h, err := store.LoadSitemapHreflang(); err != nil || h != nil {
		t.Fatalf("empty DB: %v, %v", h, err)
	}

	locales := 27
	clusters := 150_000 / locales
	var h types.SitemapHreflang
	for c := 0; c < clusters; c++ {
		alts := make([]types.HreflangEntry, locales)
		for l := 0; l < locales; l++ {
			alts[l] = types.HreflangEntry{Lang: fmt.Sprintf("l%d", l), Href: fmt.Sprintf("https://example.com/%d/product-%d", l, c)}
		}
		for l := 0; l < locales; l++ {
			h.Add(alts[l].Href, alts)
		}
	}
	if len(h.Sets) != clusters {
		t.Fatalf("sets = %d, want %d", len(h.Sets), clusters)
	}
	if err := store.SaveSitemapHreflang(&h); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadSitemapHreflang()
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != h.Len() || len(got.Sets) != len(h.Sets) {
		t.Fatalf("loaded %d urls / %d sets, want %d / %d", got.Len(), len(got.Sets), h.Len(), len(h.Sets))
	}
	alts := got.Alternates("https://example.com/3/product-42")
	if len(alts) != locales || alts[0].Href != "https://example.com/0/product-42" || alts[0].Source != types.HreflangSourceSitemap {
		t.Errorf("alternates = %v", alts)
	}
}
