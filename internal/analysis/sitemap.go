package analysis

import (
	"fmt"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/types"
)

const maxURLsPerSitemap = 50000

// SitemapOptions configures sitemap generation.
type SitemapOptions struct {
	Changefreq string
	Priority   string
}

// SitemapResult holds the output of a single sitemap generation.
type SitemapResult struct {
	XML           string `json:"xml"`
	URLCount      int    `json:"urlCount"`
	Truncated     bool   `json:"truncated"`
	TotalEligible int    `json:"totalEligible"`
}

// MultiSitemapResult holds the output of multi-sitemap generation.
type MultiSitemapResult struct {
	Index         string         `json:"index,omitempty"`
	Sitemaps      []SitemapEntry `json:"sitemaps"`
	TotalEligible int            `json:"totalEligible"`
}

// SitemapEntry is one sitemap file in a multi-sitemap.
type SitemapEntry struct {
	XML      string `json:"xml"`
	URLCount int    `json:"urlCount"`
}

// GenerateSitemap creates a single XML sitemap from crawled pages.
func GenerateSitemap(pages []*types.PageData, opts SitemapOptions) SitemapResult {
	eligible := filterEligible(pages)
	totalEligible := len(eligible)
	truncated := totalEligible > maxURLsPerSitemap
	if truncated {
		eligible = eligible[:maxURLsPerSitemap]
	}

	entries := make([]string, len(eligible))
	for i, p := range eligible {
		entries[i] = buildURLEntry(p, opts)
	}

	return SitemapResult{
		XML:           buildSitemapXML(entries),
		URLCount:      len(eligible),
		Truncated:     truncated,
		TotalEligible: totalEligible,
	}
}

// GenerateMultiSitemap creates multiple sitemaps with a sitemap index.
func GenerateMultiSitemap(pages []*types.PageData, baseURL string, baseName string, opts SitemapOptions) MultiSitemapResult {
	eligible := filterEligible(pages)
	if len(eligible) == 0 {
		return MultiSitemapResult{TotalEligible: 0}
	}

	var sitemaps []SitemapEntry

	for i := 0; i < len(eligible); i += maxURLsPerSitemap {
		end := i + maxURLsPerSitemap
		if end > len(eligible) {
			end = len(eligible)
		}
		chunk := eligible[i:end]

		entries := make([]string, len(chunk))
		for j, p := range chunk {
			entries[j] = buildURLEntry(p, opts)
		}

		sitemaps = append(sitemaps, SitemapEntry{
			XML:      buildSitemapXML(entries),
			URLCount: len(chunk),
		})
	}

	var index string
	if len(sitemaps) > 1 {
		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
		sb.WriteString(`<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
		today := time.Now().Format("2006-01-02")
		for i := range sitemaps {
			name := baseName + ".xml"
			if i > 0 {
				name = fmt.Sprintf("%s-%d.xml", baseName, i+1)
			}
			sb.WriteString(fmt.Sprintf("  <sitemap>\n    <loc>%s/%s</loc>\n    <lastmod>%s</lastmod>\n  </sitemap>\n",
				xmlEscape(baseURL), xmlEscape(name), today))
		}
		sb.WriteString("</sitemapindex>\n")
		index = sb.String()
	}

	return MultiSitemapResult{
		Sitemaps:      sitemaps,
		Index:         index,
		TotalEligible: len(eligible),
	}
}

func filterEligible(pages []*types.PageData) []*types.PageData {
	var eligible []*types.PageData
	for _, p := range pages {
		if p.StatusCode != 200 {
			continue
		}
		if !p.Indexability.Indexable {
			continue
		}
		if p.IsSoft404 {
			continue
		}
		if p.Error != "" {
			continue
		}
		// Self-canonicalizing check
		if p.Canonical != nil && *p.Canonical != "" {
			if *p.Canonical != p.URL && *p.Canonical != p.FinalURL {
				continue
			}
		}
		eligible = append(eligible, p)
	}
	return eligible
}

func buildURLEntry(p *types.PageData, opts SitemapOptions) string {
	loc := p.FinalURL
	if loc == "" {
		loc = p.URL
	}

	var sb strings.Builder
	sb.WriteString("  <url>\n")
	sb.WriteString(fmt.Sprintf("    <loc>%s</loc>\n", xmlEscape(loc)))

	if !p.CrawledAt.IsZero() {
		sb.WriteString(fmt.Sprintf("    <lastmod>%s</lastmod>\n", p.CrawledAt.Format("2006-01-02")))
	}
	if opts.Changefreq != "" {
		sb.WriteString(fmt.Sprintf("    <changefreq>%s</changefreq>\n", xmlEscape(opts.Changefreq)))
	}
	if opts.Priority != "" {
		sb.WriteString(fmt.Sprintf("    <priority>%s</priority>\n", xmlEscape(opts.Priority)))
	}

	sb.WriteString("  </url>")
	return sb.String()
}

func buildSitemapXML(entries []string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, e := range entries {
		sb.WriteString(e)
		sb.WriteByte('\n')
	}
	sb.WriteString("</urlset>\n")
	return sb.String()
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
