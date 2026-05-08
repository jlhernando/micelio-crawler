package analysis

import (
	"math"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// ReportConfig holds options for report generation.
type ReportConfig struct {
	SeedURL                string
	ExternalLinkResults    []types.ExternalLinkResult
	SegmentRules           []Segment
	SitemapWarnings        []string
	SitemapEntries         []string
	SitemapExtensionCounts SitemapExtCounts
	TotalSitemapURLs       int
	GscDays                int
	Ga4Days                int
	// InternalLinksIter, if non-nil, supplies internal-link edges from disk.
	// Required for orphan detection when pages have InternalLinks stripped
	// (always-on disk streaming during crawl). Without this, every non-seed
	// page would be flagged as orphan.
	InternalLinksIter func(fn func(source string, targets []string)) error
}

// SitemapExtCounts holds sitemap extension type counts.
type SitemapExtCounts struct {
	News  int
	Video int
	Image int
}

// GenerateReport aggregates all crawled pages into a CrawlStats summary.
func GenerateReport(pages []*types.PageData, durationMs int64, cfg ReportConfig) types.CrawlStats {
	stats := types.CrawlStats{
		StatusCodes:         make(map[int]int),
		DepthDistribution:   make(map[int]int),
		CustomSearchSummary: make(map[string]types.SearchStat),
		CrawlDurationMs:     durationMs,
	}

	if cfg.GscDays == 0 {
		cfg.GscDays = 90
	}
	if cfg.Ga4Days == 0 {
		cfg.Ga4Days = 90
	}

	// Index maps (pre-allocated with known sizes)
	n := len(pages)
	urlToPage := make(map[string]*types.PageData, n)
	inlinkCount := make(map[string]int, n)
	contentHashMap := make(map[string][]string, n/2)
	responseTimes := make([]float64, 0, n)
	performanceScores := make([]float64, 0, n/4)
	nonDescAnchors := make(map[string]bool, 16)

	// Track indexability
	indexReasons := make(map[string]int, 16)
	var indexableCount, nonIndexableCount int
	// Track duplicate titles/descriptions (populated in main loop, aggregated after)
	titleCounts := make(map[string]int, n)
	descCounts := make(map[string]int, n)

	for _, p := range pages {
		urlToPage[p.URL] = p

		// Skip robots-blocked from most metrics
		if p.RobotsBlocked {
			stats.RobotsBlockedCount++
			continue
		}

		stats.TotalPages++
		stats.StatusCodes[p.StatusCode]++
		stats.DepthDistribution[p.Depth]++

		// Title
		if p.Title == nil || p.Title.Text == "" {
			stats.PagesWithoutTitle++
		}
		// Meta description
		if p.MetaDescription == nil || p.MetaDescription.Text == "" {
			stats.PagesWithoutDescription++
		}
		// H1
		if len(p.Headings.H1) == 0 {
			stats.PagesWithoutH1++
		}

		// SEO issue counters (only for 200 pages)
		if p.StatusCode == 200 {
			if len(p.Headings.H1) > 1 {
				stats.MultipleH1Count++
			}
			if p.Title != nil && p.Title.Text != "" {
				titleCounts[p.Title.Text]++
				if p.Title.Length > 60 {
					stats.TitleTooLongCount++
				} else if p.Title.Length > 0 && p.Title.Length < 30 {
					stats.TitleTooShortCount++
				}
			}
			if p.MetaDescription != nil && p.MetaDescription.Text != "" {
				descCounts[p.MetaDescription.Text]++
				if p.MetaDescription.Length > 160 {
					stats.DescriptionTooLongCount++
				}
			}
		}

		// Images
		for _, img := range p.Images {
			stats.TotalImages++
			if img.Alt == nil || *img.Alt == "" {
				stats.ImagesMissingAlt++
			}
		}

		// Links — use counts if arrays were stripped at load
		if len(p.InternalLinks) > 0 {
			stats.TotalInternalLinks += len(p.InternalLinks)
			for _, link := range p.InternalLinks {
				normalized := normalizeTrailingSlash(link)
				inlinkCount[normalized]++
			}
		} else {
			stats.TotalInternalLinks += p.InternalLinkCount
		}
		if len(p.ExternalLinks) > 0 {
			stats.TotalExternalLinks += len(p.ExternalLinks)
		} else {
			stats.TotalExternalLinks += p.ExternalLinkCount
		}

		// Response times
		if p.ResponseTimeMs > 0 {
			responseTimes = append(responseTimes, float64(p.ResponseTimeMs))
		}

		// Redirect chains
		if len(p.RedirectChain) > 0 {
			stats.RedirectChains = append(stats.RedirectChains, types.RedirectChainInfo{
				URL:   p.URL,
				Chain: p.RedirectChain,
				Hops:  len(p.RedirectChain),
			})
			if len(p.RedirectChain) > 2 {
				stats.LongRedirectChains = append(stats.LongRedirectChains, types.RedirectChainInfo{
					URL:  p.URL,
					Hops: len(p.RedirectChain),
				})
			}
		}

		// Broken pages (4xx/5xx)
		if p.StatusCode >= 400 {
			stats.BrokenLinks = append(stats.BrokenLinks, types.BrokenLink{
				URL:        p.URL,
				StatusCode: p.StatusCode,
				FoundOn:    []string{},
			})
		}

		// Structured data
		if len(p.StructuredData) > 0 {
			stats.PagesWithStructuredData++
		}

		// Open Graph
		if len(p.OpenGraph) == 0 {
			stats.PagesWithoutOG++
		}

		// Thin content
		if p.StatusCode == 200 && p.WordCount < 200 && p.WordCount > 0 {
			stats.ThinContentPages = append(stats.ThinContentPages, types.URLWordCount{
				URL:       p.URL,
				WordCount: p.WordCount,
			})
		}

		// Content hash duplicates
		if p.ContentHash != "" && p.StatusCode == 200 {
			contentHashMap[p.ContentHash] = append(contentHashMap[p.ContentHash], p.URL)
		}

		// Hreflang issues
		for _, h := range p.Hreflang {
			if h.Href == "" {
				stats.HreflangIssues = append(stats.HreflangIssues, types.HreflangIssue{
					URL:   p.URL,
					Issue: "missing href for lang " + h.Lang,
				})
			}
		}

		// Non-descriptive anchors
		for _, anchor := range p.Anchors {
			text := strings.ToLower(strings.TrimSpace(anchor.Text))
			if isNonDescriptive(text) {
				key := anchor.Href + "|" + text + "|" + p.URL
				if !nonDescAnchors[key] {
					nonDescAnchors[key] = true
					stats.NonDescriptiveAnchors = append(stats.NonDescriptiveAnchors, types.NonDescriptiveAnchor{
						URL:     anchor.Href,
						Text:    text,
						FoundOn: p.URL,
					})
				}
			}
		}

		// Security
		if p.Security.HasMixedContent {
			stats.MixedContentPages = append(stats.MixedContentPages, p.URL)
		}
		if !p.Security.IsHTTPS {
			stats.NonHTTPSPages = append(stats.NonHTTPSPages, p.URL)
		}

		// Indexability
		if p.Indexability.Indexable {
			indexableCount++
		} else {
			nonIndexableCount++
			if p.Indexability.Reason != "" {
				indexReasons[p.Indexability.Reason]++
			}
		}

		// Soft 404
		if p.IsSoft404 {
			stats.Soft404Pages = append(stats.Soft404Pages, p.URL)
		}

		// Performance scores (PageSpeed)
		if p.Pagespeed != nil && p.Pagespeed.PerformanceScore > 0 {
			performanceScores = append(performanceScores, p.Pagespeed.PerformanceScore)
		}

		// AI analysis
		if p.AIAnalysis != nil && *p.AIAnalysis != "" {
			stats.PagesWithAIAnalysis++
		}

		// Custom searches
		for searchName, found := range p.CustomSearches {
			s := stats.CustomSearchSummary[searchName]
			s.Total++
			if found {
				s.Found++
			}
			stats.CustomSearchSummary[searchName] = s
		}

		// Slow pages
		if p.ResponseTimeMs > 3000 {
			stats.SlowPages = append(stats.SlowPages, types.URLResponseTime{
				URL:            p.URL,
				ResponseTimeMs: p.ResponseTimeMs,
			})
		}
	}

	// Indexability stats
	stats.IndexabilityStats = types.IndexabilityStats{
		Indexable:    indexableCount,
		NonIndexable: nonIndexableCount,
		Reasons:      indexReasons,
	}

	// Aggregate duplicate title/description counts from maps populated in main loop
	for _, count := range titleCounts {
		if count > 1 {
			stats.DuplicateTitleCount += count
		}
	}
	for _, count := range descCounts {
		if count > 1 {
			stats.DuplicateDescriptionCount += count
		}
	}

	// Response time percentiles + buckets
	if len(responseTimes) > 0 {
		sort.Float64s(responseTimes)
		stats.ResponseTimePercentiles = types.PercentileData{
			P50: percentile(responseTimes, 50),
			P90: percentile(responseTimes, 90),
			P99: percentile(responseTimes, 99),
		}
		for _, rt := range responseTimes {
			switch {
			case rt < 500:
				stats.ResponseTimeBuckets.Fast++
			case rt < 1000:
				stats.ResponseTimeBuckets.Medium++
			case rt < 2000:
				stats.ResponseTimeBuckets.Slow++
			default:
				stats.ResponseTimeBuckets.Slowest++
			}
		}
	}

	// Slow pages: sort by response time desc, top 50
	sort.Slice(stats.SlowPages, func(i, j int) bool {
		return stats.SlowPages[i].ResponseTimeMs > stats.SlowPages[j].ResponseTimeMs
	})
	if len(stats.SlowPages) > 50 {
		stats.SlowPages = stats.SlowPages[:50]
	}

	// Performance scores
	if len(performanceScores) > 0 {
		minS, maxS := performanceScores[0], performanceScores[0]
		sum := 0.0
		for _, s := range performanceScores {
			sum += s
			if s < minS {
				minS = s
			}
			if s > maxS {
				maxS = s
			}
		}
		stats.PerformanceScores = &types.PerformanceScores{
			Avg: sum / float64(len(performanceScores)),
			Min: minS,
			Max: maxS,
		}
	}

	// Duplicate content groups
	for hash, urls := range contentHashMap {
		if len(urls) > 1 {
			stats.DuplicateContentGroups = append(stats.DuplicateContentGroups, types.HashDuplicateGroup{
				Hash: hash,
				URLs: urls,
			})
		}
	}

	// Near-duplicate groups (SimHash) — computed before originality so we can penalize near-dupes
	var simHashItems []SimhashItem
	for _, p := range pages {
		if p.SimhashFingerprint != "" && p.StatusCode == 200 {
			var fp uint64
			for _, c := range p.SimhashFingerprint {
				fp <<= 4
				switch {
				case c >= '0' && c <= '9':
					fp |= uint64(c - '0')
				case c >= 'a' && c <= 'f':
					fp |= uint64(c-'a') + 10
				case c >= 'A' && c <= 'F':
					fp |= uint64(c-'A') + 10
				}
			}
			simHashItems = append(simHashItems, SimhashItem{URL: p.URL, Fingerprint: fp})
		}
	}
	nearDupeURLs := make(map[string]bool)
	if len(simHashItems) > 1 {
		ndGroups := FindNearDuplicates(simHashItems, 90)
		for _, g := range ndGroups {
			stats.NearDuplicateGroups = append(stats.NearDuplicateGroups, types.NearDuplicateGroup{
				URLs:       g.URLs,
				Similarity: g.Similarity,
			})
			for _, u := range g.URLs {
				nearDupeURLs[u] = true
			}
		}
	}

	// Originality score (Leak: OriginalContentScore 0-127 scale)
	// 127 = fully unique, lower for pages with exact/near duplicates
	for _, p := range pages {
		if p.StatusCode != 200 || p.ContentHash == "" {
			continue
		}
		dupeCount := len(contentHashMap[p.ContentHash])
		switch {
		case dupeCount <= 1:
			p.OriginalityScore = 127
		case dupeCount == 2:
			p.OriginalityScore = 80
		case dupeCount <= 5:
			p.OriginalityScore = 40
		default:
			p.OriginalityScore = 10
		}
		// Reduce for near-duplicate SimHash matches
		if p.SimhashFingerprint != "" && nearDupeURLs[p.URL] && p.OriginalityScore > 60 {
			p.OriginalityScore = 60
		}
	}

	// If pages had InternalLinks stripped (always-on disk streaming during crawl),
	// inlinkCount is empty here — fall back to the disk iterator so orphan
	// detection sees real edges instead of flagging every non-seed page.
	if cfg.InternalLinksIter != nil && len(inlinkCount) == 0 {
		_ = cfg.InternalLinksIter(func(_ string, targets []string) {
			for _, link := range targets {
				inlinkCount[normalizeTrailingSlash(link)]++
			}
		})
	}

	// Orphan pages: no internal links pointing to them (excluding seed URL)
	seedNorm := normalizeTrailingSlash(cfg.SeedURL)
	for _, p := range pages {
		if p.RobotsBlocked || p.StatusCode != 200 {
			continue
		}
		normalized := normalizeTrailingSlash(p.URL)
		isSeed := (seedNorm != "" && normalized == seedNorm) || (seedNorm == "" && p.Depth == 0)
		if inlinkCount[normalized] == 0 && !isSeed {
			stats.OrphanPages = append(stats.OrphanPages, p.URL)
		}
	}

	// Populate FoundOn for broken links (skip if InternalLinks stripped at load)
	if len(stats.BrokenLinks) > 0 && len(pages) > 0 && len(pages[0].InternalLinks) > 0 {
		brokenIdx := make(map[string]int, len(stats.BrokenLinks))
		for i, bl := range stats.BrokenLinks {
			brokenIdx[normalizeTrailingSlash(bl.URL)] = i
		}
		normCache := make(map[string]string, 256)
		for _, p := range pages {
			for _, link := range p.InternalLinks {
				norm, ok := normCache[link]
				if !ok {
					norm = normalizeTrailingSlash(link)
					normCache[link] = norm
				}
				if idx, ok := brokenIdx[norm]; ok {
					if len(stats.BrokenLinks[idx].FoundOn) < 10 {
						stats.BrokenLinks[idx].FoundOn = append(stats.BrokenLinks[idx].FoundOn, p.URL)
					}
				}
			}
		}
	}

	// Broken external links
	for _, ext := range cfg.ExternalLinkResults {
		if ext.StatusCode >= 400 || ext.Error != "" {
			stats.BrokenExternalLinks = append(stats.BrokenExternalLinks, ext)
		}
	}

	// URL issue stats
	urlIssues := make(map[string][]string)
	for _, p := range pages {
		for _, issue := range p.URLIssues {
			urlIssues[issue] = append(urlIssues[issue], p.URL)
		}
	}
	if len(urlIssues) > 0 {
		stats.URLIssueStats = urlIssues
	}

	// Link analysis
	stats.LinkAnalysis = buildLinkAnalysis(pages)

	// Readability stats
	stats.ReadabilityStats = buildReadabilityStats(pages)

	// Text-to-code ratio stats
	stats.TextToCodeStats = buildTextToCodeStats(pages)

	// Image audit stats
	stats.ImageAuditStats = buildImageAuditStats(pages)

	// Schema validation stats
	stats.SchemaValidationStats = BuildSchemaValidationStats(pages)

	// Template type distribution
	if cfg.SeedURL != "" {
		for _, p := range pages {
			if p.TemplateType == "" && p.StatusCode == 200 {
				p.TemplateType = DetectTemplateType(p, cfg.SeedURL)
			}
		}
	}
	stats.TemplateTypeDistribution = BuildTemplateStats(pages)

	// URL structure stats
	stats.URLStructureStats = BuildURLStructureStats(pages)

	// Segment stats
	if len(cfg.SegmentRules) > 0 {
		segStats := BuildSegmentStats(pages, cfg.SegmentRules)
		for _, ss := range segStats {
			stats.SegmentStats = append(stats.SegmentStats, types.SegmentStat{
				Name:               ss.Name,
				PageCount:          ss.PageCount,
				StatusCodes:        ss.StatusCodes,
				AvgResponseTimeMs:  ss.AvgResponseTimeMs,
				Indexable:          ss.Indexable,
				NonIndexable:       ss.NonIndexable,
				AvgWordCount:       ss.AvgWordCount,
				TotalInternalLinks: ss.TotalInternalLinks,
				TotalExternalLinks: ss.TotalExternalLinks,
				PagesWithErrors:    ss.PagesWithErrors,
			})
		}
	}

	// PageRank
	if cfg.InternalLinksIter != nil {
		stats.PageRankScores = ComputePageRankFromGraph(BuildAdjacencyListFromDisk(pages, cfg.InternalLinksIter), PageRankOptions{})
	} else {
		stats.PageRankScores = ComputePageRank(pages, PageRankOptions{})
	}

	// Delegate to builders
	stats.RedirectStats = buildRedirectStats(pages)
	stats.CanonicalStats = buildCanonicalStats(pages, urlToPage)
	stats.CruxStats = buildCruxStats(pages)
	stats.GscStats = buildGscStats(pages, cfg.GscDays)
	stats.Ga4Stats = buildGa4Stats(pages, cfg.Ga4Days)
	stats.PlausibleStats = buildPlausibleStats(pages, cfg.Ga4Days)
	ClassifyURLs(pages)
	stats.SeoFunnelStats = buildSeoFunnelStats(pages)
	stats.AIVisibilityStats = buildAIVisibilityStats(pages)
	stats.RenderCompareStats = buildRenderCompareStats(pages)
	stats.PageWeightStats = buildPageWeightStats(pages)
	stats.FreshnessStats = buildFreshnessStats(pages)
	stats.ContentRichnessStats = buildContentRichnessStats(pages)
	stats.EEATStats = buildEEATStats(pages)
	stats.PassageReadinessStats = buildPassageReadinessStats(pages)
	stats.AIReadinessStats = buildAIReadinessStats(pages)

	if cfg.TotalSitemapURLs > 0 {
		stats.SitemapStats = buildSitemapComparisonStats(pages, cfg)
	}

	// Thin content: sort and cap at 50
	sort.Slice(stats.ThinContentPages, func(i, j int) bool {
		return stats.ThinContentPages[i].WordCount < stats.ThinContentPages[j].WordCount
	})
	if len(stats.ThinContentPages) > 50 {
		stats.ThinContentPages = stats.ThinContentPages[:50]
	}

	// Non-descriptive anchors: cap at 50
	if len(stats.NonDescriptiveAnchors) > 50 {
		stats.NonDescriptiveAnchors = stats.NonDescriptiveAnchors[:50]
	}

	// Anchor health (Patent US7533092, DOJ BayesSpam)
	buildAnchorHealth(pages, urlToPage)
	stats.AnchorHealthStats = buildAnchorHealthStats(pages)

	return stats
}

// ── Inline stats builders (small, specific to GenerateReport flow) ──

func buildLinkAnalysis(pages []*types.PageData) types.LinkAnalysis {
	var deadEndPages []string
	var linksToNonIndexable []types.LinkPair
	nofollowed := 0
	followed := 0
	indexableMap := make(map[string]bool)
	for _, p := range pages {
		normalized := normalizeTrailingSlash(p.URL)
		indexableMap[normalized] = p.Indexability.Indexable
	}
	for _, p := range pages {
		if p.RobotsBlocked || p.StatusCode != 200 {
			continue
		}
		if len(p.InternalLinks) == 0 && p.InternalLinkCount == 0 {
			deadEndPages = append(deadEndPages, p.URL)
		}
		// Nofollow/followed and linksToNonIndexable require actual link arrays
		if len(p.InternalLinks) > 0 {
			nofollowSet := make(map[string]bool)
			for _, a := range p.Anchors {
				if a.IsInternal && a.Rel != nil && strings.Contains(strings.ToLower(*a.Rel), "nofollow") {
					nofollowSet[a.Href] = true
				}
			}
			for _, link := range p.InternalLinks {
				if nofollowSet[link] {
					nofollowed++
				} else {
					followed++
				}
				normalized := normalizeTrailingSlash(link)
				if indexable, exists := indexableMap[normalized]; exists && !indexable {
					linksToNonIndexable = append(linksToNonIndexable, types.LinkPair{
						From: p.URL,
						To:   link,
					})
				}
			}
		} else {
			// Links stripped — count all as followed (nofollow ratio unavailable)
			followed += p.InternalLinkCount
		}
	}
	if len(linksToNonIndexable) > 50 {
		linksToNonIndexable = linksToNonIndexable[:50]
	}
	return types.LinkAnalysis{
		DeadEndPages:            deadEndPages,
		NofollowedInternalLinks: nofollowed,
		FollowedInternalLinks:   followed,
		LinksToNonIndexable:     linksToNonIndexable,
	}
}

func buildReadabilityStats(pages []*types.PageData) *types.ReadabilityStats {
	var readScores []float64
	var difficult []types.URLScoreEntry
	var veryEasy []types.URLScoreEntry
	var tier1, tier2, tier3 int
	for _, p := range pages {
		if p.Readability != nil && p.StatusCode == 200 {
			readScores = append(readScores, p.Readability.FleschKincaid)
			if p.Readability.FleschKincaid < 30 {
				difficult = append(difficult, types.URLScoreEntry{URL: p.URL, Score: p.Readability.FleschKincaid})
			} else if p.Readability.FleschKincaid > 80 {
				veryEasy = append(veryEasy, types.URLScoreEntry{URL: p.URL, Score: p.Readability.FleschKincaid})
			}
			// Q* quality tier proxy (DOJ: Q* 0-1 scale, 0.4 threshold)
			// Composite: readability (accessible=good) + word count + content richness
			qProxy := 0.0
			// Readable content (FK Grade Level: 6-12 = accessible for most adults)
			if p.Readability.FleschKincaid >= 4 && p.Readability.FleschKincaid <= 14 {
				qProxy += 0.35
			} else if p.Readability.FleschKincaid < 4 {
				qProxy += 0.25 // Very easy, still OK
			}
			// Content depth
			if p.WordCount >= 300 {
				qProxy += 0.25
			} else if p.WordCount >= 100 {
				qProxy += 0.15
			}
			// Content richness
			if p.ContentRichness != nil && p.ContentRichness.RichnessScore >= 0.3 {
				qProxy += 0.2
			}
			// Structure (headings)
			if len(p.Headings.H2) >= 2 {
				qProxy += 0.2
			} else if len(p.Headings.H1) > 0 {
				qProxy += 0.1
			}
			switch {
			case qProxy >= 0.7:
				tier1++
			case qProxy >= 0.4:
				tier2++
			default:
				tier3++
			}
		}
	}
	if len(readScores) == 0 {
		return nil
	}
	return &types.ReadabilityStats{
		AvgScore:     avgFloat(readScores),
		Difficult:    difficult,
		VeryEasy:     veryEasy,
		QualityTier1: tier1,
		QualityTier2: tier2,
		QualityTier3: tier3,
	}
}

func buildTextToCodeStats(pages []*types.PageData) *types.TextToCodeStats {
	var ratios []float64
	var contentPoor []types.URLRatioEntry
	for _, p := range pages {
		if p.StatusCode == 200 && p.TextToCodeRatio > 0 {
			ratios = append(ratios, p.TextToCodeRatio)
			if p.TextToCodeRatio < 0.1 {
				contentPoor = append(contentPoor, types.URLRatioEntry{URL: p.URL, Ratio: p.TextToCodeRatio})
			}
		}
	}
	if len(ratios) == 0 {
		return nil
	}
	return &types.TextToCodeStats{
		AvgRatio:    avgFloat(ratios),
		ContentPoor: contentPoor,
	}
}

// ── Helper functions ──

func normalizeTrailingSlash(u string) string {
	return strings.TrimRight(u, "/")
}

var nonDescriptiveTexts = map[string]bool{
	"click here": true, "here": true, "read more": true, "learn more": true,
	"more": true, "link": true, "this": true, "this page": true,
}

func isNonDescriptive(text string) bool {
	return nonDescriptiveTexts[text]
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p / 100.0) * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}
