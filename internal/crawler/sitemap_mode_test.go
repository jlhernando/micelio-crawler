package crawler

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// urlsetXML builds a <urlset> sitemap body for the given page URLs.
func urlsetXML(locs ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, l := range locs {
		fmt.Fprintf(&b, `<url><loc>%s</loc></url>`, l)
	}
	b.WriteString(`</urlset>`)
	return b.String()
}

// sitemapModeSite serves an XML sitemap listing three pages plus an unlisted
// "orphan" page that the listed pages link to (to verify no spidering).
func sitemapModeSite(t *testing.T) *httptest.Server {
	t.Helper()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.xml" {
			w.Header().Set("Content-Type", "application/xml")
			locs := make([]string, 0, 3)
			for i := 1; i <= 3; i++ {
				locs = append(locs, fmt.Sprintf("%s/page%d", base, i))
			}
			fmt.Fprint(w, urlsetXML(locs...))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		// Every page links to an orphan that is NOT in the sitemap.
		fmt.Fprintf(w, `<html><head><title>%s</title></head><body><a href="%s/orphan">orphan</a></body></html>`, r.URL.Path, base)
	}))
	base = srv.URL
	return srv
}

func crawledURLSet(pages []*types.PageData) map[string]bool {
	m := make(map[string]bool, len(pages))
	for _, p := range pages {
		m[p.URL] = true
	}
	return m
}

func TestSitemapModeCrawlsListedURLs(t *testing.T) {
	srv := sitemapModeSite(t)
	defer srv.Close()

	config := types.CrawlConfig{
		Mode:        types.ModeSitemap,
		SitemapURLs: []string{srv.URL + "/sitemap.xml"},
		MaxPages:    50,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}

	if len(result.Pages) != 3 {
		t.Fatalf("expected 3 pages crawled, got %d", len(result.Pages))
	}
	got := crawledURLSet(result.Pages)
	for u := range got {
		if strings.Contains(u, "sitemap.xml") {
			t.Errorf("sitemap file itself should not be crawled as a page: %s", u)
		}
		if strings.Contains(u, "/orphan") {
			t.Errorf("sitemap mode must not spider out to unlisted pages: %s", u)
		}
	}
	for i := 1; i <= 3; i++ {
		want := fmt.Sprintf("%s/page%d", srv.URL, i)
		if !got[want] {
			t.Errorf("expected page %s to be crawled, missing", want)
		}
	}
}

// TestSitemapModeSeedFallback verifies that when no explicit SitemapURLs are
// given, the SeedURL is treated as the sitemap (the CLI path).
func TestSitemapModeSeedFallback(t *testing.T) {
	srv := sitemapModeSite(t)
	defer srv.Close()

	config := types.CrawlConfig{
		SeedURL:     srv.URL + "/sitemap.xml",
		Mode:        types.ModeSitemap,
		MaxPages:    50,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if len(result.Pages) != 3 {
		t.Fatalf("expected 3 pages crawled from seed sitemap, got %d", len(result.Pages))
	}
}

// TestSitemapModeModeInferredFromSitemapURLs covers the L1 footgun: a caller
// that supplies SitemapURLs without a Mode should still crawl them, not nothing.
func TestSitemapModeModeInferredFromSitemapURLs(t *testing.T) {
	srv := sitemapModeSite(t)
	defer srv.Close()

	config := types.CrawlConfig{
		SitemapURLs: []string{srv.URL + "/sitemap.xml"}, // Mode left empty
		MaxPages:    50,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if len(result.Pages) != 3 {
		t.Fatalf("expected 3 pages crawled with inferred sitemap mode, got %d", len(result.Pages))
	}
}

// TestSitemapModeMultiDomain verifies that a sitemap listing URLs on a host
// different from the sitemap host (and across multiple hosts) crawls them all —
// the cross-domain case this feature exists for. The "localhost" alias resolves
// to the same 127.0.0.1 listener but is a distinct hostname for isInternal.
func TestSitemapModeMultiDomain(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		altHost := strings.Replace(base, "127.0.0.1", "localhost", 1)
		if r.URL.Path == "/sitemap.xml" {
			w.Header().Set("Content-Type", "application/xml")
			// One page on 127.0.0.1, one on the localhost alias.
			fmt.Fprint(w, urlsetXML(base+"/a", altHost+"/b"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>%s</title></head><body>ok</body></html>`, r.URL.Path)
	}))
	defer srv.Close()
	base = srv.URL
	altHost := strings.Replace(base, "127.0.0.1", "localhost", 1)

	config := types.CrawlConfig{
		Mode:        types.ModeSitemap,
		SitemapURLs: []string{srv.URL + "/sitemap.xml"},
		MaxPages:    50,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	got := crawledURLSet(result.Pages)
	if !got[base+"/a"] {
		t.Errorf("expected %s/a (seed host) to be crawled", base)
	}
	if !got[altHost+"/b"] {
		t.Errorf("expected %s/b (second host) to be crawled — AddAllowedDomains regression", altHost)
	}
}

// TestSitemapModeAllowedDomainsFilter covers M2: an explicit AllowedDomains
// bounds the crawl, so sitemap URLs outside it are skipped.
func TestSitemapModeAllowedDomainsFilter(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		altHost := strings.Replace(base, "127.0.0.1", "localhost", 1)
		if r.URL.Path == "/sitemap.xml" {
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, urlsetXML(base+"/a", altHost+"/b"))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>%s</title></head><body>ok</body></html>`, r.URL.Path)
	}))
	defer srv.Close()
	base = srv.URL
	altHost := strings.Replace(base, "127.0.0.1", "localhost", 1)

	config := types.CrawlConfig{
		Mode:           types.ModeSitemap,
		SitemapURLs:    []string{srv.URL + "/sitemap.xml"},
		AllowedDomains: []string{"127.0.0.1"}, // exclude the localhost alias
		MaxPages:       50,
		Concurrency:    2,
		UserAgent:      "TestBot/1.0",
		SkipSSRF:       true,
	}

	result, err := Crawl(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	got := crawledURLSet(result.Pages)
	if !got[base+"/a"] {
		t.Errorf("expected allowed-domain page %s/a to be crawled", base)
	}
	if got[altHost+"/b"] {
		t.Errorf("page %s/b is outside AllowedDomains and must not be crawled", altHost)
	}
	if len(result.Pages) != 1 {
		t.Errorf("expected exactly 1 page within AllowedDomains, got %d", len(result.Pages))
	}
}

// TestSitemapModeNestedIndex verifies a <sitemapindex> is followed to its child
// sitemaps.
func TestSitemapModeNestedIndex(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><sitemap><loc>%s/child.xml</loc></sitemap></sitemapindex>`, base)
		case "/child.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, urlsetXML(base+"/p1", base+"/p2"))
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><head><title>%s</title></head><body>ok</body></html>`, r.URL.Path)
		}
	}))
	defer srv.Close()
	base = srv.URL

	config := types.CrawlConfig{
		Mode:        types.ModeSitemap,
		SitemapURLs: []string{srv.URL + "/index.xml"},
		MaxPages:    50,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	got := crawledURLSet(result.Pages)
	if !got[base+"/p1"] || !got[base+"/p2"] {
		t.Errorf("expected both child-sitemap pages crawled, got %v", got)
	}
	for u := range got {
		if strings.Contains(u, ".xml") {
			t.Errorf("sitemap/index file should not be crawled as a page: %s", u)
		}
	}
}

// TestSitemapModeGzip verifies a gzip-compressed sitemap (.xml.gz) is parsed.
func TestSitemapModeGzip(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".gz") {
			w.Header().Set("Content-Type", "application/gzip")
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			gz.Write([]byte(urlsetXML(base+"/g1", base+"/g2")))
			gz.Close()
			w.Write(buf.Bytes())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>%s</title></head><body>ok</body></html>`, r.URL.Path)
	}))
	defer srv.Close()
	base = srv.URL

	config := types.CrawlConfig{
		Mode:        types.ModeSitemap,
		SitemapURLs: []string{srv.URL + "/sitemap.xml.gz"},
		MaxPages:    50,
		Concurrency: 2,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}

	result, err := Crawl(context.Background(), config, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	got := crawledURLSet(result.Pages)
	if !got[base+"/g1"] || !got[base+"/g2"] {
		t.Errorf("expected both gzip-sitemap pages crawled, got %v", got)
	}
}

// TestSitemapModeResume verifies that resuming a sitemap crawl skips
// already-crawled URLs (dedup) and that the restored queue survives the seed
// domain being unknown at queue-creation time (L5).
func TestSitemapModeResume(t *testing.T) {
	srv := sitemapModeSite(t) // 3 pages
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "sitemap-resume.db")

	store1, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore: %v", err)
	}
	config := types.CrawlConfig{
		Mode:        types.ModeSitemap,
		SitemapURLs: []string{srv.URL + "/sitemap.xml"},
		MaxPages:    2, // crawl only 2 of the 3 listed pages
		Concurrency: 1,
		UserAgent:   "TestBot/1.0",
		SkipSSRF:    true,
	}
	result1, err := Crawl(context.Background(), config, nil, store1)
	if err != nil {
		t.Fatalf("first crawl: %v", err)
	}
	if len(result1.Pages) != 2 {
		t.Fatalf("expected 2 pages on first (capped) crawl, got %d", len(result1.Pages))
	}
	store1.Close()

	store2, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		t.Fatalf("NewCrawlStore (resume): %v", err)
	}
	defer store2.Close()
	config.MaxPages = 10
	config.Resume = true

	if _, err := Crawl(context.Background(), config, nil, store2); err != nil {
		t.Fatalf("resume crawl: %v", err)
	}

	// All 3 unique pages should now be in the DB, with none re-crawled.
	dbCount, _ := store2.PageCount()
	if dbCount != 3 {
		t.Errorf("expected 3 unique pages after resume (no duplicates), got %d", dbCount)
	}
}
