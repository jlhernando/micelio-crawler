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
)

type sitemapURLSet struct {
	URLs []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Loc string `xml:"loc"`
}

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
	err := discoverSitemaps(ctx, seedURL, robotsSitemaps, client, userAgent, func(urls []string) {
		all = append(all, urls...)
	})
	return all, err
}

// discoverSitemaps is DiscoverSitemapURLs that hands over the new same-domain
// URLs of each sitemap file as soon as it is parsed, so a crawl can start
// on them while the rest download (a news site's index can take minutes).
func discoverSitemaps(ctx context.Context, seedURL string, robotsSitemaps []string, client *http.Client, userAgent string, emit func([]string)) error {
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
	keep := func(urls []string) {
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
		if err := fetchSitemap(ctx, smURL, client, userAgent, 0, keep); err != nil {
			fmt.Fprintf(os.Stderr, "  [sitemap-discovery] %s: %v\n", smURL, err)
		}
	}
	return nil
}

// fetchSitemap fetches and parses a single sitemap URL. Handles both urlset and sitemapindex.
// maxDepth prevents infinite recursion in nested sitemap indexes.
// Page URLs go to emit, one call per urlset file.
func fetchSitemap(ctx context.Context, sitemapURL string, client *http.Client, userAgent string, depth int, emit func([]string)) error {
	if depth > 3 {
		return fmt.Errorf("sitemap nesting too deep")
	}

	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sitemapURL, nil)
	if err != nil {
		return err
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	req.Header.Set("Accept", "application/xml, text/xml, */*")

	resp, err := client.Do(req)
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
		for _, u := range urlset.URLs {
			if loc := strings.TrimSpace(u.Loc); loc != "" {
				urls = append(urls, loc)
			}
		}
		emit(urls)
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
			if err := fetchSitemap(ctx, loc, client, userAgent, depth+1, emit); err != nil {
				fmt.Fprintf(os.Stderr, "  [sitemap-discovery] %s: %v\n", loc, err)
			}
		}
		return nil
	}

	return fmt.Errorf("not a valid XML sitemap")
}
