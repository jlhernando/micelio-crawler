package analysis

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

func buildImageAuditStats(pages []*types.PageData) *types.ImageAuditStats {
	audit := &types.ImageAuditStats{}
	for _, p := range pages {
		if p.StatusCode != 200 {
			continue
		}
		for _, img := range p.Images {
			audit.TotalImages++
			if img.Alt == nil {
				audit.MissingAltAttribute++
			} else if *img.Alt == "" {
				audit.EmptyAlt++
			} else if len(*img.Alt) > 125 {
				audit.AltTooLong = append(audit.AltTooLong, types.ImageAltIssue{
					URL:       p.URL,
					Src:       img.Src,
					AltLength: len(*img.Alt),
				})
			}
			if img.MissingWidth || img.MissingHeight {
				audit.MissingDimensions++
			}
		}
	}
	if audit.TotalImages == 0 {
		return nil
	}
	return audit
}

func buildRedirectStats(pages []*types.PageData) *types.RedirectStats {
	rs := &types.RedirectStats{
		ByStatusCode: make(map[int]int),
	}
	targetCount := make(map[string]int)

	for _, p := range pages {
		if len(p.RedirectChain) == 0 {
			continue
		}
		rs.TotalRedirects++
		hops := len(p.RedirectChain)
		rs.TotalHops += hops
		if hops > rs.MaxChainLength {
			rs.MaxChainLength = hops
		}

		for _, hop := range p.RedirectChain {
			rs.ByStatusCode[hop.StatusCode]++

			if hop.StatusCode == 301 || hop.StatusCode == 302 || hop.StatusCode == 307 || hop.StatusCode == 308 {
				fromParsed, _ := url.Parse(hop.URL)
				toParsed, _ := url.Parse(p.FinalURL)
				if fromParsed != nil && toParsed != nil {
					if fromParsed.Scheme == "http" && toParsed.Scheme == "https" {
						rs.HTTPToHTTPS = append(rs.HTTPToHTTPS, types.RedirectChainInfo{URL: p.URL, Chain: p.RedirectChain})
					}
					if (strings.HasPrefix(fromParsed.Host, "www.") && !strings.HasPrefix(toParsed.Host, "www.")) ||
						(!strings.HasPrefix(fromParsed.Host, "www.") && strings.HasPrefix(toParsed.Host, "www.")) {
						rs.WWWNormalization = append(rs.WWWNormalization, types.RedirectChainInfo{URL: p.URL, Chain: p.RedirectChain})
					}
					if fromParsed.Hostname() != toParsed.Hostname() {
						rs.CrossDomain = append(rs.CrossDomain, types.RedirectChainInfo{URL: p.URL, Chain: p.RedirectChain})
					}
				}

				if hop.StatusCode == 302 || hop.StatusCode == 307 {
					rs.TemporaryRedirects = append(rs.TemporaryRedirects, types.RedirectChainInfo{URL: p.URL, Chain: p.RedirectChain})
				}
			}
		}

		if p.FinalURL != "" {
			targetCount[p.FinalURL]++
		}
	}

	if rs.TotalRedirects > 0 {
		rs.AvgChainLength = float64(rs.TotalHops) / float64(rs.TotalRedirects)
	}

	rs.TopRedirectTargets = sortedURLCounts(targetCount, 20)

	// NavBoost risk: 302/307 redirects lose click signal equity (DOJ: NavBoost 13-month window)
	rs.NavBoostAtRisk = len(rs.TemporaryRedirects)
	// Long chains (>2 hops) dilute NavBoost signals
	for _, p := range pages {
		if len(p.RedirectChain) > 2 {
			rs.NavBoostDiluted++
		}
	}

	if rs.TotalRedirects == 0 {
		return nil
	}
	return rs
}

func buildCanonicalStats(pages []*types.PageData, urlToPage map[string]*types.PageData) *types.CanonicalStats {
	cs := &types.CanonicalStats{}
	canonicalTargetCount := make(map[string]int)

	for _, p := range pages {
		if p.RobotsBlocked || p.StatusCode != 200 {
			continue
		}

		if p.Canonical == nil || *p.Canonical == "" {
			cs.TotalWithoutCanonical++
			continue
		}

		cs.TotalWithCanonical++
		canonical := *p.Canonical
		canonicalTargetCount[canonical]++

		if p.CanonicalCount > 1 {
			cs.MultipleCanonicals++
		}

		normalizedCanonical := normalizeTrailingSlash(canonical)
		normalizedURL := normalizeTrailingSlash(p.URL)
		normalizedFinal := normalizeTrailingSlash(p.FinalURL)
		if normalizedCanonical == normalizedURL || normalizedCanonical == normalizedFinal {
			cs.SelfReferencing++
		} else {
			cs.Canonicalized++
		}

		if target, ok := urlToPage[canonical]; ok {
			if target.StatusCode != 200 {
				cs.CanonicalToNon200 = append(cs.CanonicalToNon200, types.CanonicalStatusIssue{
					URL:          p.URL,
					Canonical:    canonical,
					TargetStatus: target.StatusCode,
				})
			}
			if !target.Indexability.Indexable {
				cs.CanonicalToNonIndexable = append(cs.CanonicalToNonIndexable, types.CanonicalPair{
					URL:       p.URL,
					Canonical: canonical,
				})
			}
		}

		pParsed, _ := url.Parse(p.URL)
		cParsed, _ := url.Parse(canonical)
		if pParsed != nil && cParsed != nil {
			if pParsed.Hostname() != cParsed.Hostname() {
				cs.CrossDomain = append(cs.CrossDomain, types.CanonicalPair{
					URL:       p.URL,
					Canonical: canonical,
				})
			}
			if pParsed.Scheme != cParsed.Scheme {
				cs.HTTPHTTPSMismatch = append(cs.HTTPHTTPSMismatch, types.CanonicalPair{
					URL:       p.URL,
					Canonical: canonical,
				})
			}
		}

		if p.CanonicalRaw != nil && *p.CanonicalRaw != "" {
			raw := *p.CanonicalRaw
			if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") && !strings.HasPrefix(raw, "//") {
				cs.RelativeCanonicals = append(cs.RelativeCanonicals, types.RelativeCanonical{
					URL:     p.URL,
					RawHref: raw,
				})
			}
		}

		if cParsed != nil && cParsed.RawQuery != "" {
			cs.CanonicalWithQueryString = append(cs.CanonicalWithQueryString, types.CanonicalPair{
				URL:       p.URL,
				Canonical: canonical,
			})
		}
	}

	cs.TopCanonicalTargets = sortedURLCounts(canonicalTargetCount, 20)

	if cs.TotalWithCanonical == 0 && cs.TotalWithoutCanonical == 0 {
		return nil
	}
	return cs
}

func buildCruxStats(pages []*types.PageData) *types.CruxStats {
	cs := &types.CruxStats{}
	var lcpValues, inpValues, clsValues []float64

	for _, p := range pages {
		if p.CruxData == nil {
			continue
		}
		cs.PagesWithData++
		if p.CruxData.LCPMs != nil && *p.CruxData.LCPMs > 0 {
			lcpValues = append(lcpValues, *p.CruxData.LCPMs)
		}
		if p.CruxData.INPMs != nil && *p.CruxData.INPMs > 0 {
			inpValues = append(inpValues, *p.CruxData.INPMs)
		}
		if p.CruxData.CLS != nil {
			clsValues = append(clsValues, *p.CruxData.CLS)
		}
	}

	if cs.PagesWithData == 0 {
		return nil
	}

	if len(lcpValues) > 0 {
		avg := avgFloat(lcpValues)
		cs.AvgLCPMs = &avg
		for _, v := range lcpValues {
			if v <= 2500 {
				cs.GoodLCP++
			} else if v > 4000 {
				cs.PoorLCP++
			}
		}
	}
	if len(inpValues) > 0 {
		avg := avgFloat(inpValues)
		cs.AvgINPMs = &avg
		for _, v := range inpValues {
			if v <= 200 {
				cs.GoodINP++
			} else if v > 500 {
				cs.PoorINP++
			}
		}
	}
	if len(clsValues) > 0 {
		avg := avgFloat(clsValues)
		cs.AvgCLS = &avg
		for _, v := range clsValues {
			if v <= 0.1 {
				cs.GoodCLS++
			} else if v > 0.25 {
				cs.PoorCLS++
			}
		}
	}

	return cs
}

func buildGscStats(pages []*types.PageData, days int) *types.GscStats {
	gs := &types.GscStats{Days: days}
	var positions []float64

	for _, p := range pages {
		if p.GscData == nil {
			continue
		}
		gs.PagesWithData++
		gs.TotalImpressions += p.GscData.Impressions
		gs.TotalClicks += p.GscData.Clicks
		positions = append(positions, p.GscData.Position)
	}

	if gs.PagesWithData == 0 {
		return nil
	}

	if gs.TotalImpressions > 0 {
		gs.AvgCTR = float64(gs.TotalClicks) / float64(gs.TotalImpressions)
	}
	gs.AvgPosition = avgFloat(positions)

	for _, p := range pages {
		if p.StatusCode == 200 && p.Indexability.Indexable && p.GscData != nil && p.GscData.Impressions == 0 {
			gs.ZombiePages = append(gs.ZombiePages, p.URL)
		}
	}

	return gs
}

func buildGa4Stats(pages []*types.PageData, days int) *types.Ga4Stats {
	ga := &types.Ga4Stats{Days: days}
	var bounceRates []float64
	var engagementRates []float64

	for _, p := range pages {
		if p.Ga4Data == nil {
			continue
		}
		ga.PagesWithData++
		ga.TotalSessions += p.Ga4Data.Sessions
		ga.TotalPageviews += p.Ga4Data.Pageviews
		ga.TotalConversions += p.Ga4Data.Conversions
		bounceRates = append(bounceRates, p.Ga4Data.BounceRate)
		engagementRates = append(engagementRates, p.Ga4Data.EngagementRate)
	}

	if ga.PagesWithData == 0 {
		return nil
	}

	ga.AvgBounceRate = avgFloat(bounceRates)
	ga.AvgEngagementRate = avgFloat(engagementRates)

	for _, p := range pages {
		if p.StatusCode == 200 && p.Indexability.Indexable && (p.Ga4Data == nil || p.Ga4Data.Pageviews == 0) {
			ga.NoTrafficPages = append(ga.NoTrafficPages, p.URL)
		}
	}
	if len(ga.NoTrafficPages) > 50 {
		ga.NoTrafficPages = ga.NoTrafficPages[:50]
	}

	return ga
}

func buildPlausibleStats(pages []*types.PageData, days int) *types.PlausibleStats {
	ps := &types.PlausibleStats{Days: days}
	var bounceRates []float64
	var visitDurations []float64

	for _, p := range pages {
		if p.PlausibleData == nil {
			continue
		}
		ps.PagesWithData++
		ps.TotalVisitors += p.PlausibleData.Visitors
		ps.TotalVisits += p.PlausibleData.Visits
		ps.TotalPageviews += p.PlausibleData.Pageviews
		bounceRates = append(bounceRates, p.PlausibleData.BounceRate)
		visitDurations = append(visitDurations, p.PlausibleData.VisitDuration)
	}

	if ps.PagesWithData == 0 {
		return nil
	}

	ps.AvgBounceRate = avgFloat(bounceRates)
	ps.AvgVisitDuration = avgFloat(visitDurations)

	return ps
}

func buildRenderCompareStats(pages []*types.PageData) *types.RenderCompareStats {
	rc := &types.RenderCompareStats{
		FieldDiffCounts: make(map[string]int),
	}

	for _, p := range pages {
		if len(p.RenderDiffs) == 0 {
			continue
		}
		rc.PagesCompared++
		rc.PagesWithDiffs++
		for _, diff := range p.RenderDiffs {
			rc.FieldDiffCounts[diff.Field]++
			if diff.Field == "title" || diff.Field == "canonical" || diff.Field == "metaRobots" {
				rc.CriticalDiffs = append(rc.CriticalDiffs, types.CriticalDiff{
					URL:      p.URL,
					Field:    diff.Field,
					Original: diff.Original,
					Rendered: diff.Rendered,
				})
			}
		}
	}

	if rc.PagesCompared == 0 {
		return nil
	}
	return rc
}

func buildSitemapComparisonStats(pages []*types.PageData, cfg ReportConfig) *types.SitemapStats {
	ss := &types.SitemapStats{
		TotalSitemapURLs: cfg.TotalSitemapURLs,
		NewsEntryCount:   cfg.SitemapExtensionCounts.News,
		VideoEntryCount:  cfg.SitemapExtensionCounts.Video,
		ImageEntryCount:  cfg.SitemapExtensionCounts.Image,
		StatusBreakdown:  make(map[int]int),
	}

	if len(cfg.SitemapWarnings) > 0 {
		ss.ValidationWarnings = cfg.SitemapWarnings
	}

	sitemapURLs := make(map[string]bool)
	for _, u := range cfg.SitemapEntries {
		sitemapURLs[normalizeTrailingSlash(u)] = true
	}

	crawledURLs := make(map[string]bool)
	for _, p := range pages {
		if p.StatusCode == 200 {
			normalized := normalizeTrailingSlash(p.URL)
			crawledURLs[normalized] = true

			if !sitemapURLs[normalized] && p.Indexability.Indexable {
				ss.MissingFromSitemap = append(ss.MissingFromSitemap, p.URL)
			}

			if sitemapURLs[normalized] && !p.Indexability.Indexable {
				ss.NonIndexableInSitemap = append(ss.NonIndexableInSitemap, p.URL)
			}
		}
		if sitemapURLs[normalizeTrailingSlash(p.URL)] {
			ss.StatusBreakdown[p.StatusCode]++
		}
	}

	for u := range sitemapURLs {
		if !crawledURLs[u] {
			ss.UncrawledSitemapURLs = append(ss.UncrawledSitemapURLs, u)
		}
	}

	crawledAndInSitemap := 0
	for u := range crawledURLs {
		if sitemapURLs[u] {
			crawledAndInSitemap++
		}
	}
	ss.Coverage = types.SitemapCoverage{
		CrawledAndInSitemap: crawledAndInSitemap,
		CrawledNotInSitemap: len(crawledURLs) - crawledAndInSitemap,
		InSitemapNotCrawled: len(sitemapURLs) - crawledAndInSitemap,
	}

	return ss
}

func buildPageWeightStats(pages []*types.PageData) *types.PageWeightStats {
	var totalBytes int64
	count := 0
	byType := make(map[string]types.TypeWeightSummary)
	var heaviest []types.URLBytesEntry
	var oversized []types.URLBytesEntry
	var truncationRisk []types.URLBytesEntry

	for _, p := range pages {
		if p.PageWeight == nil || p.StatusCode != 200 {
			continue
		}
		count++
		totalBytes += p.PageWeight.TotalBytes
		heaviest = append(heaviest, types.URLBytesEntry{
			URL:        p.URL,
			TotalBytes: p.PageWeight.TotalBytes,
		})
		if p.PageWeight.TotalBytes > 3*1024*1024 {
			oversized = append(oversized, types.URLBytesEntry{
				URL:        p.URL,
				TotalBytes: p.PageWeight.TotalBytes,
			})
		}
		// Googlebot truncation risk: HTML body > 2MB (uncompressed)
		if p.BodySize > 2*1024*1024 {
			truncationRisk = append(truncationRisk, types.URLBytesEntry{
				URL:        p.URL,
				TotalBytes: p.BodySize,
			})
		}
		for rtype, tw := range p.PageWeight.ByType {
			s := byType[rtype]
			s.Count += tw.Count
			s.Bytes += tw.Bytes
			byType[rtype] = s
		}
	}

	if count == 0 {
		return nil
	}

	sort.Slice(heaviest, func(i, j int) bool {
		return heaviest[i].TotalBytes > heaviest[j].TotalBytes
	})
	if len(heaviest) > 20 {
		heaviest = heaviest[:20]
	}

	return &types.PageWeightStats{
		AvgTotalBytes:       totalBytes / int64(count),
		HeaviestPages:       heaviest,
		ByType:              byType,
		OversizedPages:      oversized,
		TruncationRiskPages: truncationRisk,
	}
}

// ClassifyURLs assigns URLClassification to each page based on analytics data.
// Call this before buildSeoFunnelStats to ensure classifications are set.
func ClassifyURLs(pages []*types.PageData) {
	for _, p := range pages {
		if p.RobotsBlocked {
			p.URLClassification = "robots-blocked"
			continue
		}
		if !p.Indexability.Indexable {
			p.URLClassification = "non-indexable"
			continue
		}

		// Active: has clicks from GSC or sessions from GA4/Plausible
		isActive := (p.GscData != nil && p.GscData.Clicks > 0) ||
			(p.Ga4Data != nil && p.Ga4Data.Sessions > 0) ||
			(p.PlausibleData != nil && p.PlausibleData.Visitors > 0)

		// Visible: has GSC impressions (even if no clicks)
		isVisible := p.GscData != nil && p.GscData.Impressions > 0

		if isActive {
			p.URLClassification = "active"
		} else if isVisible {
			p.URLClassification = "visible"
		} else {
			p.URLClassification = "indexable"
		}
	}
}

func buildSeoFunnelStats(pages []*types.PageData) *types.SeoFunnelStats {
	var crawled, renderable, indexable, visible, active, nonIndexable int
	segments := make(map[string]*types.SeoFunnelSegment)

	for _, p := range pages {
		if p.RobotsBlocked {
			continue
		}
		crawled++

		// Renderable: 2xx status (page loaded successfully)
		isRenderable := p.StatusCode >= 200 && p.StatusCode < 300
		if isRenderable {
			renderable++
		}

		// Segment tracking by template type
		seg := p.TemplateType
		if seg == "" {
			seg = "other"
		}
		s, ok := segments[seg]
		if !ok {
			s = &types.SeoFunnelSegment{}
			segments[seg] = s
		}
		s.Crawled++
		if isRenderable {
			s.Renderable++
		}

		switch p.URLClassification {
		case "active":
			indexable++
			active++
			s.Indexable++
			s.Active++
			s.Visible++ // active URLs are also visible
		case "visible":
			indexable++
			visible++
			s.Indexable++
			s.Visible++
		case "indexable":
			indexable++
			s.Indexable++
		case "non-indexable":
			nonIndexable++
			s.NonIndexable++
		}
	}

	if crawled == 0 {
		return nil
	}

	// Compute per-segment PctActive
	for _, s := range segments {
		if s.Crawled > 0 {
			s.PctActive = float64(s.Active) / float64(s.Crawled) * 100
		}
	}

	// Only include segments with >1 type (skip if everything is "other")
	if len(segments) <= 1 {
		segments = nil
	}

	visibleTotal := visible + active
	fs := &types.SeoFunnelStats{
		Crawled:       crawled,
		Renderable:    renderable,
		Indexable:     indexable,
		NonIndexable:  nonIndexable,
		Visible:       visible,
		Active:        active,
		PctRenderable: float64(renderable) / float64(crawled) * 100,
		PctIndexable:  float64(indexable) / float64(crawled) * 100,
		PctVisible:    float64(visibleTotal) / float64(crawled) * 100,
		PctActive:     float64(active) / float64(crawled) * 100,
		Segments:      segments,
	}

	return fs
}

func buildAIVisibilityStats(pages []*types.PageData) *types.AIVisibilityStats {
	var pagesInAI int
	var totalImpressions, totalClicks int
	var entries []types.AIVisibilityEntry
	allQueries := make(map[string]*types.AIOverviewQuery)

	for _, p := range pages {
		if p.AIVisibilityData == nil || !p.AIVisibilityData.InAIOverview {
			continue
		}
		pagesInAI++
		totalImpressions += p.AIVisibilityData.AIImpressions
		totalClicks += p.AIVisibilityData.AIClicks
		entries = append(entries, types.AIVisibilityEntry{
			URL:           p.URL,
			AIImpressions: p.AIVisibilityData.AIImpressions,
			AIClicks:      p.AIVisibilityData.AIClicks,
			AICTR:         p.AIVisibilityData.AICTR,
		})
		for _, q := range p.AIVisibilityData.Queries {
			if existing, ok := allQueries[q.Query]; ok {
				// Weighted average position
				totalImp := float64(existing.Impressions + q.Impressions)
				if totalImp > 0 {
					existing.Position = (existing.Position*float64(existing.Impressions) + q.Position*float64(q.Impressions)) / totalImp
				}
				existing.Impressions += q.Impressions
				existing.Clicks += q.Clicks
			} else {
				qCopy := q
				allQueries[q.Query] = &qCopy
			}
		}
	}

	if pagesInAI == 0 {
		return nil
	}

	// Sort entries by clicks desc
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].AIClicks > entries[j].AIClicks
	})
	if len(entries) > 50 {
		entries = entries[:50]
	}

	// Top queries
	var topQueries []types.AIOverviewQuery
	for _, q := range allQueries {
		if q.Impressions > 0 {
			q.CTR = float64(q.Clicks) / float64(q.Impressions)
		}
		topQueries = append(topQueries, *q)
	}
	sort.Slice(topQueries, func(i, j int) bool {
		return topQueries[i].Clicks > topQueries[j].Clicks
	})
	if len(topQueries) > 50 {
		topQueries = topQueries[:50]
	}

	var avgCTR float64
	if totalImpressions > 0 {
		avgCTR = float64(totalClicks) / float64(totalImpressions)
	}

	return &types.AIVisibilityStats{
		PagesInAIOverview:  pagesInAI,
		TotalAIImpressions: totalImpressions,
		TotalAIClicks:      totalClicks,
		AvgAICTR:           avgCTR,
		TopByClicks:        entries,
		TopQueries:         topQueries,
	}
}

// ── Rankpedia-informed stats builders ──

func buildFreshnessStats(pages []*types.PageData) *types.FreshnessStats {
	fs := &types.FreshnessStats{
		DateSourceBreakdown: make(map[string]int),
		AgeDistribution:     make(map[string]int),
	}
	for _, p := range pages {
		if p.StatusCode != 200 || p.RobotsBlocked {
			continue
		}
		if p.Freshness == nil || (p.Freshness.BylineDate == nil && p.Freshness.ModifiedDate == nil) {
			fs.PagesWithoutDate++
			continue
		}
		fs.PagesWithDate++
		if p.Freshness.DateSource != "" {
			fs.DateSourceBreakdown[p.Freshness.DateSource]++
		}
		if !p.Freshness.DateConsistency {
			fs.InconsistentDates++
		}
		days := p.Freshness.ContentAgeDays
		switch {
		case days < 30:
			fs.AgeDistribution["<30d"]++
		case days < 90:
			fs.AgeDistribution["30-90d"]++
		case days < 365:
			fs.AgeDistribution["90d-1y"]++
		case days < 730:
			fs.AgeDistribution["1-2y"]++
		default:
			fs.AgeDistribution["2y+"]++
		}
		if p.SitemapData != nil && p.SitemapData.InSitemap {
			if p.Freshness.ContentAgeDays > 365 {
				fs.StaleSitemapLastmod++
			}
		}
	}
	if fs.PagesWithDate == 0 && fs.PagesWithoutDate == 0 {
		return nil
	}
	return fs
}

func buildContentRichnessStats(pages []*types.PageData) *types.ContentRichnessStats {
	cr := &types.ContentRichnessStats{}
	var scores []float64
	var depths []float64
	for _, p := range pages {
		if p.StatusCode != 200 || p.ContentRichness == nil {
			continue
		}
		scores = append(scores, p.ContentRichness.RichnessScore)
		depths = append(depths, float64(p.ContentRichness.HeadingDepth))
		if p.ContentRichness.RichnessScore < 0.2 {
			cr.LowRichnessPages++
		}
		if p.ContentRichness.RichnessScore > 0.7 {
			cr.HighRichnessPages++
		}
		if p.ContentRichness.ListCount == 0 {
			cr.PagesWithNoLists++
		}
		if p.ContentRichness.TableCount > 0 {
			cr.PagesWithTables++
		}
		if p.ContentRichness.VideoEmbeds > 0 {
			cr.PagesWithVideo++
		}
	}
	if len(scores) == 0 {
		return nil
	}
	cr.AvgRichnessScore = avgFloat(scores)
	cr.AvgHeadingDepth = avgFloat(depths)
	return cr
}

func buildEEATStats(pages []*types.PageData) *types.EEATStats {
	es := &types.EEATStats{}
	authors := make(map[string]bool)
	var citations []float64
	var contentPages int
	for _, p := range pages {
		if p.StatusCode != 200 || p.EEAT == nil {
			continue
		}
		contentPages++
		if p.EEAT.HasAuthor {
			es.PagesWithAuthor++
			if p.EEAT.AuthorName != "" {
				authors[p.EEAT.AuthorName] = true
			}
		} else {
			es.PagesWithoutAuthor++
		}
		citations = append(citations, float64(p.EEAT.CitationCount))
		if p.EEAT.HasAboutPage {
			es.HasAboutPage = true
		}
		if p.EEAT.HasContactPage {
			es.HasContactPage = true
		}
		if p.EEAT.HasEditorialPolicy {
			es.HasEditorialPolicy = true
		}
	}
	if contentPages == 0 {
		return nil
	}
	es.UniqueAuthors = len(authors)
	es.AvgCitationCount = avgFloat(citations)
	if contentPages > 0 {
		es.AuthorCoverage = float64(es.PagesWithAuthor) / float64(contentPages) * 100
	}
	return es
}

func buildPassageReadinessStats(pages []*types.PageData) *types.PassageReadinessStats {
	pr := &types.PassageReadinessStats{}
	var scores []float64
	var sections []float64
	for _, p := range pages {
		if p.StatusCode != 200 || p.PassageReadiness == nil {
			continue
		}
		scores = append(scores, p.PassageReadiness.PassageScore)
		sections = append(sections, float64(p.PassageReadiness.SectionsCount))
		if p.PassageReadiness.HasFAQStructure {
			pr.PagesWithFAQ++
		}
		if p.PassageReadiness.HasHowToStructure {
			pr.PagesWithHowTo++
		}
		if p.PassageReadiness.HasDefinitions {
			pr.PagesWithDefinitions++
		}
	}
	if len(scores) == 0 {
		return nil
	}
	pr.AvgPassageScore = avgFloat(scores)
	pr.AvgSectionsCount = avgFloat(sections)
	return pr
}

func buildAIReadinessStats(pages []*types.PageData) *types.AIReadinessStats {
	ar := &types.AIReadinessStats{}
	var scores []float64
	for _, p := range pages {
		if p.StatusCode != 200 || p.AIReadiness == nil {
			continue
		}
		scores = append(scores, p.AIReadiness.AIReadinessScore)
		if p.AIReadiness.HasConciseDefinition {
			ar.PagesWithConciseDefinition++
		}
		if p.AIReadiness.HasStructuredAnswer {
			ar.PagesWithStructuredAnswer++
		}
		if p.AIReadiness.HasFAQSchema {
			ar.PagesWithFAQSchema++
		}
		if p.AIReadiness.HasHowToSchema {
			ar.PagesWithHowToSchema++
		}
	}
	if len(scores) == 0 {
		return nil
	}
	ar.AvgAIReadinessScore = avgFloat(scores)
	return ar
}

func buildTopicalityStats(pages []*types.PageData) *types.TopicalityStats {
	ts := &types.TopicalityStats{}
	var titleBody, titleH1, consistency []float64
	for _, p := range pages {
		if p.StatusCode != 200 || p.Topicality == nil {
			continue
		}
		titleBody = append(titleBody, p.Topicality.TitleBodyOverlap)
		titleH1 = append(titleH1, p.Topicality.TitleH1Alignment)
		consistency = append(consistency, p.Topicality.TopicalConsistency)
		if p.Topicality.TopicalConsistency < 0.3 {
			ts.LowAlignmentPages++
		}
		if p.Topicality.TopicalConsistency >= 0.9 {
			ts.PerfectAlignmentPages++
		}
	}
	if len(consistency) == 0 {
		return nil
	}
	ts.AvgTitleBodyOverlap = avgFloat(titleBody)
	ts.AvgTitleH1Alignment = avgFloat(titleH1)
	ts.AvgTopicalConsistency = avgFloat(consistency)
	return ts
}

// buildAnchorHealth computes per-page AnchorHealthData from inbound anchor text.
// Must be called before anchors are stripped from pages.
func buildAnchorHealth(pages []*types.PageData, urlToPage map[string]*types.PageData) {
	// Build reverse map: target URL → list of inbound anchor texts
	type inboundAnchor struct {
		text string
	}
	inboundAnchors := make(map[string][]inboundAnchor)
	for _, p := range pages {
		if p.StatusCode != 200 {
			continue
		}
		for _, a := range p.Anchors {
			if !a.IsInternal {
				continue
			}
			target := normalizeTrailingSlash(a.Href)
			inboundAnchors[target] = append(inboundAnchors[target], inboundAnchor{text: a.Text})
		}
	}

	urlPattern := regexp.MustCompile(`^https?://`)

	for _, p := range pages {
		if p.StatusCode != 200 {
			continue
		}
		normalized := normalizeTrailingSlash(p.URL)
		anchors := inboundAnchors[normalized]
		if len(anchors) == 0 {
			continue
		}
		ah := &types.AnchorHealthData{}
		uniqueTexts := make(map[string]bool)
		for _, a := range anchors {
			text := strings.TrimSpace(a.text)
			lower := strings.ToLower(text)
			if text != "" {
				uniqueTexts[lower] = true
			}
			if isNonDescriptive(lower) || text == "" {
				ah.GenericAnchors++
			}
			if urlPattern.MatchString(text) {
				ah.NakedURLAnchors++
			}
		}
		if len(anchors) > 0 {
			ah.InboundAnchorDiversity = float64(len(uniqueTexts)) / float64(len(anchors))
		}
		// Over-optimized: same anchor text used >50% of the time (excluding generic/URL)
		if len(uniqueTexts) > 0 {
			textCounts := make(map[string]int)
			for _, a := range anchors {
				lower := strings.ToLower(strings.TrimSpace(a.text))
				if lower != "" && !isNonDescriptive(lower) && !urlPattern.MatchString(a.text) {
					textCounts[lower]++
				}
			}
			meaningful := len(anchors) - ah.GenericAnchors - ah.NakedURLAnchors
			for _, count := range textCounts {
				if meaningful > 3 && float64(count)/float64(meaningful) > 0.5 {
					ah.OverOptimizedAnchors++
				}
			}
		}
		p.AnchorHealth = ah
	}
}

func buildAnchorHealthStats(pages []*types.PageData) *types.AnchorHealthStats {
	as := &types.AnchorHealthStats{}
	var diversities []float64
	for _, p := range pages {
		if p.StatusCode != 200 || p.AnchorHealth == nil {
			continue
		}
		diversities = append(diversities, p.AnchorHealth.InboundAnchorDiversity)
		if p.AnchorHealth.OverOptimizedAnchors > 0 {
			as.PagesOverOptimized++
		}
		as.TotalGenericAnchors += p.AnchorHealth.GenericAnchors
		as.TotalNakedURLAnchors += p.AnchorHealth.NakedURLAnchors
	}
	if len(diversities) == 0 {
		return nil
	}
	as.AvgInboundDiversity = avgFloat(diversities)
	return as
}

// ── Utility ──

func avgFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func sortedURLCounts(freq map[string]int, n int) []types.URLCountEntry {
	type kv struct {
		url   string
		count int
	}
	items := make([]kv, 0, len(freq))
	for k, v := range freq {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].count > items[j].count
	})
	if n > 0 && len(items) > n {
		items = items[:n]
	}
	result := make([]types.URLCountEntry, len(items))
	for i, item := range items {
		result[i] = types.URLCountEntry{URL: item.url, Count: item.count}
	}
	return result
}
