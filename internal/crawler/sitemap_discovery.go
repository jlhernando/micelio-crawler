package crawler

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/termsafe"
	"github.com/micelio/micelio/internal/types"
)

type sitemapURLSet struct {
	URLs []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc string `xml:"loc"`
	// <xhtml:link rel="alternate" hreflang="de-AT" href="..."/>. A tag
	// without a namespace matches any, so sitemaps that forget to declare
	// xmlns:xhtml still parse.
	Links []sitemapLink `xml:"link"`
}

type sitemapLink struct {
	Rel      string `xml:"rel,attr"`
	Hreflang string `xml:"hreflang,attr"`
	Href     string `xml:"href,attr"`
}

// sitemapAlternates returns the hreflang alternates of one sitemap <url>.
func sitemapAlternates(u sitemapURL) []types.HreflangEntry {
	var out []types.HreflangEntry
	for _, l := range u.Links {
		lang := strings.TrimSpace(l.Hreflang)
		if lang == "" || !strings.EqualFold(strings.TrimSpace(l.Rel), "alternate") {
			continue
		}
		out = append(out, types.HreflangEntry{Lang: lang, Href: strings.TrimSpace(l.Href), Source: types.HreflangSourceSitemap})
	}
	return out
}

// sitemapEmit receives the page URLs of one urlset file and, parallel to
// them, their hreflang alternates (nil when the file declares none).
type sitemapEmit func(urls []string, alts [][]types.HreflangEntry)

type sitemapIndex struct {
	Sitemaps []sitemapEntry `xml:"sitemap"`
}

type sitemapEntry struct {
	Loc string `xml:"loc"`
}

// DiscoverSitemapURLs fetches and parses XML sitemaps to discover page URLs.
// It uses sitemap URLs from robots.txt if available, otherwise falls back to common paths.
// Returns deduplicated URLs filtered to the same domain as seedURL.
func DiscoverSitemapURLs(ctx context.Context, seedURL string, robotsSitemaps []string, client *http.Client, userAgent string) ([]string, error) {
	var all []string
	err := discoverSitemaps(ctx, seedURL, robotsSitemaps, client, userAgent, nil, func(urls []string) {
		all = append(all, urls...)
	}, nil)
	return all, err
}

// discoverSitemaps is DiscoverSitemapURLs that hands over the new same-domain
// URLs of each sitemap file as soon as it is parsed, so a crawl can start
// on them while the rest download (a news site's index can take minutes).
// pace (FetchOptions.Pace, may be nil) spaces the downloads with the crawl's
// own requests to the same host. hreflang (may be nil) receives the
// alternates of every sitemap <url> that declares them, same-domain or not.
func discoverSitemaps(ctx context.Context, seedURL string, robotsSitemaps []string, client *http.Client, userAgent string, pace func(context.Context, string) func(), emit func([]string), hreflang *types.SitemapHreflang) error {
	parsed, err := url.Parse(seedURL)
	if err != nil {
		return fmt.Errorf("parse seed URL: %w", err)
	}
	seedDomain := parsed.Hostname()
	origin := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)

	// Determine sitemap URLs to fetch
	sitemapURLs := robotsSitemaps
	if len(sitemapURLs) == 0 {
		// Fallback: try common sitemap paths
		sitemapURLs = []string{
			origin + "/sitemap.xml",
			origin + "/sitemap_index.xml",
		}
	}

	seen := make(map[string]bool)
	keep := func(urls []string, alts [][]types.HreflangEntry) {
		if hreflang != nil {
			for i, a := range alts {
				hreflang.Add(urls[i], a)
			}
		}
		var out []string
		for _, u := range urls {
			normalized := normalizeURL(u)
			if seen[normalized] {
				continue
			}
			// Filter to same domain
			p, err := url.Parse(u)
			if err != nil || p.Hostname() != seedDomain {
				continue
			}
			seen[normalized] = true
			out = append(out, u)
		}
		if len(out) > 0 {
			emit(out)
		}
	}

	for _, smURL := range sitemapURLs {
		if ctx.Err() != nil {
			break
		}
		if err := fetchSitemap(ctx, smURL, client, userAgent, pace, 0, keep); err != nil {
			fmt.Fprintf(os.Stderr, "  [sitemap-discovery] %s: %s\n", termsafe.String(smURL), termsafe.String(err.Error()))
		}
	}
	return nil
}

// fetchSitemap fetches and parses a single sitemap URL. Handles both urlset and sitemapindex.
// maxDepth prevents infinite recursion in nested sitemap indexes.
// Page URLs go to emit, one call per urlset file.
func fetchSitemap(ctx context.Context, sitemapURL string, client *http.Client, userAgent string, pace func(context.Context, string) func(), depth int, emit sitemapEmit) error {
	if depth > 3 {
		return fmt.Errorf("sitemap nesting too deep")
	}

	if client == nil {
		client = DefaultClient(30*time.Second, nil)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sitemapURL, nil)
	if err != nil {
		return err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	req.Header.Set("Accept", "application/xml, text/xml, */*")

	resp, err := pacedDo(client, req, pace)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Handle gzip-compressed sitemaps (.xml.gz)
	var reader io.Reader = resp.Body
	if strings.HasSuffix(sitemapURL, ".gz") || resp.Header.Get("Content-Type") == "application/gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		reader = gz
	}

	body, err := io.ReadAll(io.LimitReader(reader, 50<<20)) // 50MB limit
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	// Try parsing as urlset first
	var urlset sitemapURLSet
	if xml.Unmarshal(body, &urlset) == nil && len(urlset.URLs) > 0 {
		urls := make([]string, 0, len(urlset.URLs))
		var alts [][]types.HreflangEntry
		for _, u := range urlset.URLs {
			loc := strings.TrimSpace(u.Loc)
			if loc == "" {
				continue
			}
			if a := sitemapAlternates(u); len(a) > 0 {
				if alts == nil {
					alts = make([][]types.HreflangEntry, len(urls), cap(urls))
				}
				alts = append(alts, a)
			} else if alts != nil {
				alts = append(alts, nil)
			}
			urls = append(urls, loc)
		}
		// Drop the parsed tree before handing over (large files).
		urlset.URLs = nil
		emit(urls, alts)
		return nil
	}

	// Try parsing as sitemapindex
	var idx sitemapIndex
	if xml.Unmarshal(body, &idx) == nil && len(idx.Sitemaps) > 0 {
		for _, sm := range idx.Sitemaps {
			loc := strings.TrimSpace(sm.Loc)
			if loc == "" {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			if err := fetchSitemap(ctx, loc, client, userAgent, pace, depth+1, emit); err != nil {
				fmt.Fprintf(os.Stderr, "  [sitemap-discovery] %s: %s\n", termsafe.String(loc), termsafe.String(err.Error()))
			}
		}
		return nil
	}

	return fmt.Errorf("not a valid XML sitemap")
}
