package report

import (
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/micelio/micelio/internal/types"
)

// Severity levels for comparison findings.
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
	SeverityStable   = "stable"
)

// ComparisonReport is the full comparison between two crawls of the same domain.
type ComparisonReport struct {
	HealthScore  HealthScoreSection  `json:"healthScore"`
	Coverage     CoverageSection     `json:"coverage"`
	Funnel       *FunnelSection      `json:"funnel,omitempty"`
	HTTPStatus   HTTPStatusSection   `json:"httpStatus"`
	Indexability IndexabilitySection `json:"indexability"`
	OnPageSEO    OnPageSEOSection    `json:"onPageSeo"`
	Content      ContentSection      `json:"content"`
	Links        LinksSection        `json:"links"`
	InternalLink InternalLinkSection `json:"internalLink"`
	Redirects    RedirectSection     `json:"redirects"`
	Performance  PerformanceSection  `json:"performance"`
	Schema       *SchemaSection      `json:"schema,omitempty"`
	Images       ImageSection        `json:"images"`
	Security     SecuritySection     `json:"security"`
	Hreflang     HreflangSection     `json:"hreflang"`
	Sitemap      *SitemapSection     `json:"sitemap,omitempty"`
	Visibility   *VisibilitySection  `json:"visibility,omitempty"`
	Ngrams       *NgramSection       `json:"ngrams,omitempty"`
	Embeddings   *EmbeddingSection   `json:"embeddings,omitempty"`
	Segments     []SegmentComparison `json:"segments,omitempty"`
	Findings     []Finding           `json:"findings"`
	URLs         *DiffResult         `json:"urls"`
}

// Finding is a single insight with severity.
type Finding struct {
	Section  string `json:"section"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Impact   int    `json:"impact"` // traffic-weighted importance (higher = more important)
}

// MetricDelta holds old, new values and the computed delta.
type MetricDelta struct {
	Old   float64 `json:"old"`
	New   float64 `json:"new"`
	Delta float64 `json:"delta"`
}

// IntDelta holds integer old/new/delta.
type IntDelta struct {
	Old   int `json:"old"`
	New   int `json:"new"`
	Delta int `json:"delta"`
}

// URLMetricChange holds a URL with old/new metric values.
type URLMetricChange struct {
	URL   string  `json:"url"`
	Old   float64 `json:"old"`
	New   float64 `json:"new"`
	Delta float64 `json:"delta"`
}

// --- Section Types ---

// HealthScoreSection holds the composite health score comparison.
type HealthScoreSection struct {
	OldScore float64     `json:"oldScore"`
	NewScore float64     `json:"newScore"`
	Delta    float64     `json:"delta"`
	Details  []ScoreItem `json:"details"`
}

// ScoreItem is a single component of the health score.
type ScoreItem struct {
	Name     string  `json:"name"`
	Weight   float64 `json:"weight"`
	OldScore float64 `json:"oldScore"`
	NewScore float64 `json:"newScore"`
}

// CoverageSection holds crawl coverage comparison.
type CoverageSection struct {
	TotalPages     IntDelta `json:"totalPages"`
	NewURLs        int      `json:"newUrls"`
	DisappearedURLs int     `json:"disappearedUrls"`
	PersistedURLs  int      `json:"persistedUrls"`
	RobotsBlocked  IntDelta `json:"robotsBlocked"`
}

// FunnelSection holds SEO funnel comparison.
type FunnelSection struct {
	OldFunnel *types.SeoFunnelStats `json:"oldFunnel"`
	NewFunnel *types.SeoFunnelStats `json:"newFunnel"`
}

// StatusMigration shows how URLs moved between status code groups.
type StatusMigration struct {
	From  string `json:"from"`  // e.g. "2xx"
	To    string `json:"to"`    // e.g. "4xx"
	Count int    `json:"count"`
	URLs  []string `json:"urls,omitempty"` // sample URLs (max 10)
}

// HTTPStatusSection holds HTTP status comparison.
type HTTPStatusSection struct {
	OldStatusCodes map[int]int       `json:"oldStatusCodes"`
	NewStatusCodes map[int]int       `json:"newStatusCodes"`
	Migrations     []StatusMigration `json:"migrations"`
	BrokenLinks    IntDelta          `json:"brokenLinks"`
	Soft404s       IntDelta          `json:"soft404s"`
}

// IndexabilitySection holds indexability comparison.
type IndexabilitySection struct {
	Indexable       IntDelta           `json:"indexable"`
	NonIndexable    IntDelta           `json:"nonIndexable"`
	BecameNonIndexable []string        `json:"becameNonIndexable,omitempty"`
	BecameIndexable    []string        `json:"becameIndexable,omitempty"`
}

// OnPageSEOSection holds on-page SEO element comparison.
type OnPageSEOSection struct {
	WithoutTitle       IntDelta `json:"withoutTitle"`
	WithoutDescription IntDelta `json:"withoutDescription"`
	WithoutH1          IntDelta `json:"withoutH1"`
	DuplicateTitles    IntDelta `json:"duplicateTitles"`
	DuplicateDescs     IntDelta `json:"duplicateDescs"`
	TitleTooLong       IntDelta `json:"titleTooLong"`
	TitleTooShort      IntDelta `json:"titleTooShort"`
	DescTooLong        IntDelta `json:"descTooLong"`
	MultipleH1         IntDelta `json:"multipleH1"`
	WithoutOG          IntDelta `json:"withoutOg"`
}

// ContentSection holds content quality comparison.
type ContentSection struct {
	ThinContentPages   IntDelta    `json:"thinContentPages"`
	DuplicateGroups    IntDelta    `json:"duplicateGroups"`
	NearDuplicateGroups IntDelta   `json:"nearDuplicateGroups"`
	Readability        MetricDelta `json:"readability"`
	TextToCode         MetricDelta `json:"textToCode"`
	ModifiedPages      int         `json:"modifiedPages"`
	UnchangedPages     int         `json:"unchangedPages"`
}

// LinksSection holds link intelligence comparison.
type LinksSection struct {
	OrphanPages       IntDelta          `json:"orphanPages"`
	NearOrphans       IntDelta          `json:"nearOrphans"`
	DilutionWarnings  IntDelta          `json:"dilutionWarnings"`
	PageRankWinners   []URLMetricChange `json:"pageRankWinners,omitempty"`
	PageRankLosers    []URLMetricChange `json:"pageRankLosers,omitempty"`
}

// InternalLinkSection holds internal linking comparison.
type InternalLinkSection struct {
	TotalInternal IntDelta `json:"totalInternal"`
	TotalExternal IntDelta `json:"totalExternal"`
	DeadEnds      IntDelta `json:"deadEnds"`
}

// RedirectSection holds redirect and canonical comparison.
type RedirectSection struct {
	RedirectChains    IntDelta `json:"redirectChains"`
	LongChains        IntDelta `json:"longChains"`
}

// PerformanceSection holds performance comparison.
type PerformanceSection struct {
	ResponseP50 MetricDelta  `json:"responseP50"`
	ResponseP90 MetricDelta  `json:"responseP90"`
	ResponseP99 MetricDelta  `json:"responseP99"`
	SlowPages   IntDelta     `json:"slowPages"`
	AvgWeight   *MetricDelta `json:"avgWeight,omitempty"`
}

// SchemaSection holds structured data comparison.
type SchemaSection struct {
	PagesWithSchema IntDelta `json:"pagesWithSchema"`
	PagesWithErrors IntDelta `json:"pagesWithErrors"`
	RichEligible    IntDelta `json:"richEligible"`
}

// ImageSection holds image audit comparison.
type ImageSection struct {
	TotalImages IntDelta `json:"totalImages"`
	MissingAlt  IntDelta `json:"missingAlt"`
}

// SecuritySection holds security comparison.
type SecuritySection struct {
	NonHTTPS     IntDelta `json:"nonHttps"`
	MixedContent IntDelta `json:"mixedContent"`
}

// HreflangSection holds hreflang comparison.
type HreflangSection struct {
	Issues IntDelta `json:"issues"`
}

// SitemapSection holds sitemap comparison.
type SitemapSection struct {
	TotalSitemapURLs     IntDelta `json:"totalSitemapUrls"`
	MissingFromSitemap   IntDelta `json:"missingFromSitemap"`
	NonIndexableInSitemap IntDelta `json:"nonIndexableInSitemap"`
}

// VisibilitySection holds search visibility comparison.
type VisibilitySection struct {
	Impressions IntDelta          `json:"impressions"`
	Clicks      IntDelta          `json:"clicks"`
	AvgCTR      MetricDelta       `json:"avgCtr"`
	AvgPosition MetricDelta       `json:"avgPosition"`
	ZombiePages IntDelta          `json:"zombiePages"`
	Winners     []URLMetricChange `json:"winners,omitempty"`
	Losers      []URLMetricChange `json:"losers,omitempty"`
}

// NgramSection holds n-gram topic evolution.
type NgramSection struct {
	EmergingTopics  []NgramChange `json:"emergingTopics"`
	DecliningTopics []NgramChange `json:"decliningTopics"`
}

// NgramChange holds a topic that appeared or disappeared.
type NgramChange struct {
	Term  string `json:"term"`
	Count int    `json:"count"`
	Pages int    `json:"pages"`
}

// EmbeddingSection holds embedding/cannibalization comparison.
type EmbeddingSection struct {
	SimilarPairs          IntDelta `json:"similarPairs"`
	CannibalizationGroups IntDelta `json:"cannibalizationGroups"`
}

// SegmentComparison holds per-segment comparison data.
type SegmentComparison struct {
	Name           string      `json:"name"`
	Pages          IntDelta    `json:"pages"`
	Indexable      IntDelta    `json:"indexable"`
	AvgWordCount   MetricDelta `json:"avgWordCount"`
	AvgResponseMs  MetricDelta `json:"avgResponseMs"`
	Clicks         IntDelta    `json:"clicks"`
}

// --- Computation ---

// ComputeComparison produces a full comparison report from two crawl result sets.
func ComputeComparison(
	oldPages []*types.PageData, oldStats *types.CrawlStats,
	newPages []*types.PageData, newStats *types.CrawlStats,
) *ComparisonReport {
	// Build URL maps
	oldMap := make(map[string]*types.PageData, len(oldPages))
	for _, p := range oldPages {
		oldMap[p.URL] = p
	}
	newMap := make(map[string]*types.PageData, len(newPages))
	for _, p := range newPages {
		newMap[p.URL] = p
	}

	// Compute basic diff
	diff := ComputeDiff(oldPages, newPages)

	report := &ComparisonReport{
		URLs: diff,
	}

	// Persisted URLs (in both)
	var persisted []string
	for url := range newMap {
		if _, ok := oldMap[url]; ok {
			persisted = append(persisted, url)
		}
	}

	report.HealthScore = compareHealthScore(oldStats, newStats)
	report.Coverage = compareCoverage(oldStats, newStats, diff)
	report.Funnel = compareFunnel(oldStats, newStats)
	report.HTTPStatus = compareHTTPStatus(oldStats, newStats, oldMap, newMap)
	report.Indexability = compareIndexability(oldStats, newStats, oldMap, newMap)
	report.OnPageSEO = compareOnPageSEO(oldStats, newStats)
	report.Content = compareContent(oldStats, newStats, oldMap, newMap, persisted)
	report.Links = compareLinks(oldStats, newStats)
	report.InternalLink = compareInternalLinks(oldStats, newStats)
	report.Redirects = compareRedirects(oldStats, newStats)
	report.Performance = comparePerformance(oldStats, newStats)
	report.Schema = compareSchema(oldStats, newStats)
	report.Images = compareImages(oldStats, newStats)
	report.Security = compareSecurity(oldStats, newStats)
	report.Hreflang = compareHreflang(oldStats, newStats)
	report.Sitemap = compareSitemap(oldStats, newStats)
	report.Visibility = compareVisibility(oldStats, newStats, oldMap, newMap)
	report.Ngrams = compareNgrams(oldStats, newStats)
	report.Embeddings = compareEmbeddings(oldStats, newStats)
	report.Segments = compareSegments(oldStats, newStats)

	// Collect findings from all sections
	report.Findings = collectFindings(report, oldStats, newStats, oldMap, newMap)

	// Sort findings: critical first, then warning, then info; within same severity by impact desc
	sort.Slice(report.Findings, func(i, j int) bool {
		si := severityRank(report.Findings[i].Severity)
		sj := severityRank(report.Findings[j].Severity)
		if si != sj {
			return si < sj
		}
		return report.Findings[i].Impact > report.Findings[j].Impact
	})

	return report
}

func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}

// ComputeHealthScore computes a composite health score (0-100) for a crawl.
func ComputeHealthScore(stats *types.CrawlStats) float64 {
	if stats == nil || stats.TotalPages == 0 {
		return 0
	}
	total := float64(stats.TotalPages)

	// Indexability rate (15%)
	indexRate := float64(stats.IndexabilityStats.Indexable) / total * 100

	// HTTP health (15%) — % of 2xx
	ok2xx := 0
	for code, count := range stats.StatusCodes {
		if code >= 200 && code < 300 {
			ok2xx += count
		}
	}
	httpRate := float64(ok2xx) / total * 100

	// Title coverage (10%)
	titleRate := (1 - float64(stats.PagesWithoutTitle)/total) * 100

	// Description coverage (8%)
	descRate := (1 - float64(stats.PagesWithoutDescription)/total) * 100

	// H1 coverage (7%)
	h1Rate := (1 - float64(stats.PagesWithoutH1)/total) * 100

	// Duplicate content (10%)
	dupPages := 0
	for _, g := range stats.DuplicateContentGroups {
		dupPages += len(g.URLs)
	}
	for _, g := range stats.NearDuplicateGroups {
		dupPages += len(g.URLs)
	}
	dupRate := (1 - math.Min(float64(dupPages)/total, 1)) * 100

	// Internal linking (10%) — based on no dead ends
	deadEnds := len(stats.LinkAnalysis.DeadEndPages)
	linkRate := (1 - math.Min(float64(deadEnds)/total, 1)) * 100

	// Response time (10%)
	rtScore := 100.0
	if stats.ResponseTimePercentiles.P90 > 0 {
		if stats.ResponseTimePercentiles.P90 > 3000 {
			rtScore = 0
		} else if stats.ResponseTimePercentiles.P90 > 1000 {
			rtScore = 100 * (1 - (stats.ResponseTimePercentiles.P90-1000)/2000)
		}
	}

	// Security (5%)
	secRate := (1 - math.Min(float64(len(stats.NonHTTPSPages)+len(stats.MixedContentPages))/total, 1)) * 100

	// Structured data (5%)
	schemaRate := float64(stats.PagesWithStructuredData) / total * 100

	// Image alt (5%)
	altRate := 100.0
	if stats.TotalImages > 0 {
		altRate = (1 - float64(stats.ImagesMissingAlt)/float64(stats.TotalImages)) * 100
	}

	score := indexRate*0.15 +
		httpRate*0.15 +
		titleRate*0.10 +
		descRate*0.08 +
		h1Rate*0.07 +
		dupRate*0.10 +
		linkRate*0.10 +
		rtScore*0.10 +
		secRate*0.05 +
		schemaRate*0.05 +
		altRate*0.05

	return math.Round(score*10) / 10
}

func compareHealthScore(oldStats, newStats *types.CrawlStats) HealthScoreSection {
	oldScore := ComputeHealthScore(oldStats)
	newScore := ComputeHealthScore(newStats)

	details := buildScoreDetails(oldStats, newStats)

	return HealthScoreSection{
		OldScore: oldScore,
		NewScore: newScore,
		Delta:    math.Round((newScore-oldScore)*10) / 10,
		Details:  details,
	}
}

func buildScoreDetails(oldStats, newStats *types.CrawlStats) []ScoreItem {
	type comp struct {
		name   string
		weight float64
		oldVal func() float64
		newVal func() float64
	}

	pctOf := func(num int, stats *types.CrawlStats) float64 {
		if stats == nil || stats.TotalPages == 0 {
			return 0
		}
		return float64(num) / float64(stats.TotalPages) * 100
	}
	invPctOf := func(num int, stats *types.CrawlStats) float64 {
		return 100 - pctOf(num, stats)
	}

	httpRate := func(stats *types.CrawlStats) float64 {
		if stats == nil || stats.TotalPages == 0 {
			return 0
		}
		ok2xx := 0
		for code, count := range stats.StatusCodes {
			if code >= 200 && code < 300 {
				ok2xx += count
			}
		}
		return float64(ok2xx) / float64(stats.TotalPages) * 100
	}

	dupRate := func(stats *types.CrawlStats) float64 {
		if stats == nil || stats.TotalPages == 0 {
			return 0
		}
		dupPages := 0
		for _, g := range stats.DuplicateContentGroups {
			dupPages += len(g.URLs)
		}
		for _, g := range stats.NearDuplicateGroups {
			dupPages += len(g.URLs)
		}
		return (1 - math.Min(float64(dupPages)/float64(stats.TotalPages), 1)) * 100
	}

	linkRate := func(stats *types.CrawlStats) float64 {
		if stats == nil || stats.TotalPages == 0 {
			return 0
		}
		return (1 - math.Min(float64(len(stats.LinkAnalysis.DeadEndPages))/float64(stats.TotalPages), 1)) * 100
	}

	rtScore := func(stats *types.CrawlStats) float64 {
		if stats == nil || stats.ResponseTimePercentiles.P90 <= 0 {
			return 100
		}
		if stats.ResponseTimePercentiles.P90 > 3000 {
			return 0
		}
		if stats.ResponseTimePercentiles.P90 > 1000 {
			return 100 * (1 - (stats.ResponseTimePercentiles.P90-1000)/2000)
		}
		return 100
	}

	secRate := func(stats *types.CrawlStats) float64 {
		if stats == nil || stats.TotalPages == 0 {
			return 0
		}
		return (1 - math.Min(float64(len(stats.NonHTTPSPages)+len(stats.MixedContentPages))/float64(stats.TotalPages), 1)) * 100
	}

	schemaRate := func(stats *types.CrawlStats) float64 {
		if stats == nil || stats.TotalPages == 0 {
			return 0
		}
		return float64(stats.PagesWithStructuredData) / float64(stats.TotalPages) * 100
	}

	altRate := func(stats *types.CrawlStats) float64 {
		if stats == nil || stats.TotalImages == 0 {
			return 100
		}
		return (1 - float64(stats.ImagesMissingAlt)/float64(stats.TotalImages)) * 100
	}

	comps := []comp{
		{"Indexability", 0.15,
			func() float64 { return pctOf(oldStats.IndexabilityStats.Indexable, oldStats) },
			func() float64 { return pctOf(newStats.IndexabilityStats.Indexable, newStats) }},
		{"HTTP Health", 0.15,
			func() float64 { return httpRate(oldStats) },
			func() float64 { return httpRate(newStats) }},
		{"Title Coverage", 0.10,
			func() float64 { return invPctOf(oldStats.PagesWithoutTitle, oldStats) },
			func() float64 { return invPctOf(newStats.PagesWithoutTitle, newStats) }},
		{"Description Coverage", 0.08,
			func() float64 { return invPctOf(oldStats.PagesWithoutDescription, oldStats) },
			func() float64 { return invPctOf(newStats.PagesWithoutDescription, newStats) }},
		{"H1 Coverage", 0.07,
			func() float64 { return invPctOf(oldStats.PagesWithoutH1, oldStats) },
			func() float64 { return invPctOf(newStats.PagesWithoutH1, newStats) }},
		{"Duplicate Content", 0.10,
			func() float64 { return dupRate(oldStats) },
			func() float64 { return dupRate(newStats) }},
		{"Internal Linking", 0.10,
			func() float64 { return linkRate(oldStats) },
			func() float64 { return linkRate(newStats) }},
		{"Response Time", 0.10,
			func() float64 { return rtScore(oldStats) },
			func() float64 { return rtScore(newStats) }},
		{"Security", 0.05,
			func() float64 { return secRate(oldStats) },
			func() float64 { return secRate(newStats) }},
		{"Structured Data", 0.05,
			func() float64 { return schemaRate(oldStats) },
			func() float64 { return schemaRate(newStats) }},
		{"Image Accessibility", 0.05,
			func() float64 { return altRate(oldStats) },
			func() float64 { return altRate(newStats) }},
	}

	var items []ScoreItem
	for _, c := range comps {
		items = append(items, ScoreItem{
			Name:     c.name,
			Weight:   c.weight,
			OldScore: math.Round(c.oldVal()*10) / 10,
			NewScore: math.Round(c.newVal()*10) / 10,
		})
	}
	return items
}

func compareCoverage(oldStats, newStats *types.CrawlStats, diff *DiffResult) CoverageSection {
	return CoverageSection{
		TotalPages:      intDelta(oldStats.TotalPages, newStats.TotalPages),
		NewURLs:         len(diff.AddedURLs),
		DisappearedURLs: len(diff.RemovedURLs),
		PersistedURLs:   diff.UnchangedCount + len(diff.ChangedURLs),
		RobotsBlocked:   intDelta(oldStats.RobotsBlockedCount, newStats.RobotsBlockedCount),
	}
}

func compareFunnel(oldStats, newStats *types.CrawlStats) *FunnelSection {
	if oldStats.SeoFunnelStats == nil && newStats.SeoFunnelStats == nil {
		return nil
	}
	return &FunnelSection{
		OldFunnel: oldStats.SeoFunnelStats,
		NewFunnel: newStats.SeoFunnelStats,
	}
}

func compareHTTPStatus(oldStats, newStats *types.CrawlStats, oldMap, newMap map[string]*types.PageData) HTTPStatusSection {
	// Compute status migrations for persisted URLs
	var migrations []StatusMigration
	migMap := make(map[[2]string][]string) // [oldGroup, newGroup] → URLs

	for url, newPage := range newMap {
		oldPage, ok := oldMap[url]
		if !ok {
			continue
		}
		oldGroup := statusGroup(oldPage.StatusCode)
		newGroup := statusGroup(newPage.StatusCode)
		if oldGroup != newGroup {
			key := [2]string{oldGroup, newGroup}
			migMap[key] = append(migMap[key], url)
		}
	}

	for key, urls := range migMap {
		sample := urls
		if len(sample) > 10 {
			sample = sample[:10]
		}
		migrations = append(migrations, StatusMigration{
			From:  key[0],
			To:    key[1],
			Count: len(urls),
			URLs:  sample,
		})
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Count > migrations[j].Count
	})

	return HTTPStatusSection{
		OldStatusCodes: oldStats.StatusCodes,
		NewStatusCodes: newStats.StatusCodes,
		Migrations:     migrations,
		BrokenLinks:    intDelta(len(oldStats.BrokenLinks), len(newStats.BrokenLinks)),
		Soft404s:       intDelta(len(oldStats.Soft404Pages), len(newStats.Soft404Pages)),
	}
}

func statusGroup(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "other"
	}
}

func compareIndexability(oldStats, newStats *types.CrawlStats, oldMap, newMap map[string]*types.PageData) IndexabilitySection {
	section := IndexabilitySection{
		Indexable:    intDelta(oldStats.IndexabilityStats.Indexable, newStats.IndexabilityStats.Indexable),
		NonIndexable: intDelta(oldStats.IndexabilityStats.NonIndexable, newStats.IndexabilityStats.NonIndexable),
	}

	// Find pages that changed indexability
	for url, newPage := range newMap {
		oldPage, ok := oldMap[url]
		if !ok {
			continue
		}
		if oldPage.Indexability.Indexable && !newPage.Indexability.Indexable {
			section.BecameNonIndexable = append(section.BecameNonIndexable, url)
		} else if !oldPage.Indexability.Indexable && newPage.Indexability.Indexable {
			section.BecameIndexable = append(section.BecameIndexable, url)
		}
	}

	sort.Strings(section.BecameNonIndexable)
	sort.Strings(section.BecameIndexable)

	// Limit to top 100
	if len(section.BecameNonIndexable) > 100 {
		section.BecameNonIndexable = section.BecameNonIndexable[:100]
	}
	if len(section.BecameIndexable) > 100 {
		section.BecameIndexable = section.BecameIndexable[:100]
	}

	return section
}

func compareOnPageSEO(oldStats, newStats *types.CrawlStats) OnPageSEOSection {
	return OnPageSEOSection{
		WithoutTitle:       intDelta(oldStats.PagesWithoutTitle, newStats.PagesWithoutTitle),
		WithoutDescription: intDelta(oldStats.PagesWithoutDescription, newStats.PagesWithoutDescription),
		WithoutH1:          intDelta(oldStats.PagesWithoutH1, newStats.PagesWithoutH1),
		DuplicateTitles:    intDelta(oldStats.DuplicateTitleCount, newStats.DuplicateTitleCount),
		DuplicateDescs:     intDelta(oldStats.DuplicateDescriptionCount, newStats.DuplicateDescriptionCount),
		TitleTooLong:       intDelta(oldStats.TitleTooLongCount, newStats.TitleTooLongCount),
		TitleTooShort:      intDelta(oldStats.TitleTooShortCount, newStats.TitleTooShortCount),
		DescTooLong:        intDelta(oldStats.DescriptionTooLongCount, newStats.DescriptionTooLongCount),
		MultipleH1:         intDelta(oldStats.MultipleH1Count, newStats.MultipleH1Count),
		WithoutOG:          intDelta(oldStats.PagesWithoutOG, newStats.PagesWithoutOG),
	}
}

func compareContent(oldStats, newStats *types.CrawlStats, oldMap, newMap map[string]*types.PageData, persisted []string) ContentSection {
	section := ContentSection{
		ThinContentPages:    intDelta(len(oldStats.ThinContentPages), len(newStats.ThinContentPages)),
		DuplicateGroups:     intDelta(len(oldStats.DuplicateContentGroups), len(newStats.DuplicateContentGroups)),
		NearDuplicateGroups: intDelta(len(oldStats.NearDuplicateGroups), len(newStats.NearDuplicateGroups)),
	}

	// Readability
	if oldStats.ReadabilityStats != nil && newStats.ReadabilityStats != nil {
		section.Readability = metricDelta(oldStats.ReadabilityStats.AvgScore, newStats.ReadabilityStats.AvgScore)
	}

	// Text to code ratio
	if oldStats.TextToCodeStats != nil && newStats.TextToCodeStats != nil {
		section.TextToCode = metricDelta(oldStats.TextToCodeStats.AvgRatio, newStats.TextToCodeStats.AvgRatio)
	}

	// Content change detection via content hash
	for _, url := range persisted {
		oldPage := oldMap[url]
		newPage := newMap[url]
		if oldPage.ContentHash != "" && newPage.ContentHash != "" {
			if oldPage.ContentHash != newPage.ContentHash {
				section.ModifiedPages++
			} else {
				section.UnchangedPages++
			}
		}
	}

	return section
}

func compareLinks(oldStats, newStats *types.CrawlStats) LinksSection {
	section := LinksSection{
		OrphanPages: intDelta(len(oldStats.OrphanPages), len(newStats.OrphanPages)),
	}

	if oldStats.LinkIntelligenceStats != nil && newStats.LinkIntelligenceStats != nil {
		section.NearOrphans = intDelta(
			oldStats.LinkIntelligenceStats.NearOrphansCount,
			newStats.LinkIntelligenceStats.NearOrphansCount,
		)
		section.DilutionWarnings = intDelta(
			oldStats.LinkIntelligenceStats.DilutionWarningsCount,
			newStats.LinkIntelligenceStats.DilutionWarningsCount,
		)
	}

	// PageRank movers — compare per-URL PageRank scores
	if oldStats.PageRankScores != nil && newStats.PageRankScores != nil {
		var changes []URLMetricChange
		for url, newPR := range newStats.PageRankScores {
			oldPR, ok := oldStats.PageRankScores[url]
			if !ok {
				continue
			}
			delta := newPR - oldPR
			if math.Abs(delta) > 0.1 { // significant change threshold
				changes = append(changes, URLMetricChange{
					URL:   url,
					Old:   math.Round(oldPR*100) / 100,
					New:   math.Round(newPR*100) / 100,
					Delta: math.Round(delta*100) / 100,
				})
			}
		}

		// Sort by absolute delta descending
		sort.Slice(changes, func(i, j int) bool {
			return math.Abs(changes[i].Delta) > math.Abs(changes[j].Delta)
		})

		for _, c := range changes {
			if c.Delta > 0 && len(section.PageRankWinners) < 20 {
				section.PageRankWinners = append(section.PageRankWinners, c)
			} else if c.Delta < 0 && len(section.PageRankLosers) < 20 {
				section.PageRankLosers = append(section.PageRankLosers, c)
			}
		}
	}

	return section
}

func compareInternalLinks(oldStats, newStats *types.CrawlStats) InternalLinkSection {
	return InternalLinkSection{
		TotalInternal: intDelta(oldStats.TotalInternalLinks, newStats.TotalInternalLinks),
		TotalExternal: intDelta(oldStats.TotalExternalLinks, newStats.TotalExternalLinks),
		DeadEnds:      intDelta(len(oldStats.LinkAnalysis.DeadEndPages), len(newStats.LinkAnalysis.DeadEndPages)),
	}
}

func compareRedirects(oldStats, newStats *types.CrawlStats) RedirectSection {
	return RedirectSection{
		RedirectChains: intDelta(len(oldStats.RedirectChains), len(newStats.RedirectChains)),
		LongChains:     intDelta(len(oldStats.LongRedirectChains), len(newStats.LongRedirectChains)),
	}
}

func comparePerformance(oldStats, newStats *types.CrawlStats) PerformanceSection {
	section := PerformanceSection{
		ResponseP50: metricDelta(oldStats.ResponseTimePercentiles.P50, newStats.ResponseTimePercentiles.P50),
		ResponseP90: metricDelta(oldStats.ResponseTimePercentiles.P90, newStats.ResponseTimePercentiles.P90),
		ResponseP99: metricDelta(oldStats.ResponseTimePercentiles.P99, newStats.ResponseTimePercentiles.P99),
		SlowPages:   intDelta(len(oldStats.SlowPages), len(newStats.SlowPages)),
	}

	if oldStats.PageWeightStats != nil && newStats.PageWeightStats != nil {
		d := metricDelta(float64(oldStats.PageWeightStats.AvgTotalBytes), float64(newStats.PageWeightStats.AvgTotalBytes))
		section.AvgWeight = &d
	}

	return section
}

func compareSchema(oldStats, newStats *types.CrawlStats) *SchemaSection {
	if oldStats.SchemaValidationStats == nil && newStats.SchemaValidationStats == nil {
		return nil
	}

	oldSch := safeSchemaStats(oldStats.SchemaValidationStats)
	newSch := safeSchemaStats(newStats.SchemaValidationStats)

	// Count total rich result eligible
	oldRich := 0
	for _, v := range oldSch.RichResultEligible {
		oldRich += v
	}
	newRich := 0
	for _, v := range newSch.RichResultEligible {
		newRich += v
	}

	return &SchemaSection{
		PagesWithSchema: intDelta(oldSch.PagesWithSchema, newSch.PagesWithSchema),
		PagesWithErrors: intDelta(oldSch.PagesWithErrors, newSch.PagesWithErrors),
		RichEligible:    intDelta(oldRich, newRich),
	}
}

func safeSchemaStats(s *types.SchemaValidationStats) types.SchemaValidationStats {
	if s == nil {
		return types.SchemaValidationStats{RichResultEligible: map[string]int{}}
	}
	result := *s
	if result.RichResultEligible == nil {
		result.RichResultEligible = map[string]int{}
	}
	return result
}

func compareImages(oldStats, newStats *types.CrawlStats) ImageSection {
	return ImageSection{
		TotalImages: intDelta(oldStats.TotalImages, newStats.TotalImages),
		MissingAlt:  intDelta(oldStats.ImagesMissingAlt, newStats.ImagesMissingAlt),
	}
}

func compareSecurity(oldStats, newStats *types.CrawlStats) SecuritySection {
	return SecuritySection{
		NonHTTPS:     intDelta(len(oldStats.NonHTTPSPages), len(newStats.NonHTTPSPages)),
		MixedContent: intDelta(len(oldStats.MixedContentPages), len(newStats.MixedContentPages)),
	}
}

func compareHreflang(oldStats, newStats *types.CrawlStats) HreflangSection {
	return HreflangSection{
		Issues: intDelta(len(oldStats.HreflangIssues), len(newStats.HreflangIssues)),
	}
}

func compareSitemap(oldStats, newStats *types.CrawlStats) *SitemapSection {
	if oldStats.SitemapStats == nil && newStats.SitemapStats == nil {
		return nil
	}

	oldSm := safeSitemapStats(oldStats.SitemapStats)
	newSm := safeSitemapStats(newStats.SitemapStats)

	return &SitemapSection{
		TotalSitemapURLs:      intDelta(oldSm.TotalSitemapURLs, newSm.TotalSitemapURLs),
		MissingFromSitemap:    intDelta(len(oldSm.MissingFromSitemap), len(newSm.MissingFromSitemap)),
		NonIndexableInSitemap: intDelta(len(oldSm.NonIndexableInSitemap), len(newSm.NonIndexableInSitemap)),
	}
}

func safeSitemapStats(s *types.SitemapStats) types.SitemapStats {
	if s == nil {
		return types.SitemapStats{}
	}
	return *s
}

func compareVisibility(oldStats, newStats *types.CrawlStats, oldMap, newMap map[string]*types.PageData) *VisibilitySection {
	if oldStats.GscStats == nil && newStats.GscStats == nil {
		return nil
	}

	oldGsc := safeGscStats(oldStats.GscStats)
	newGsc := safeGscStats(newStats.GscStats)

	section := &VisibilitySection{
		Impressions: intDelta(oldGsc.TotalImpressions, newGsc.TotalImpressions),
		Clicks:      intDelta(oldGsc.TotalClicks, newGsc.TotalClicks),
		AvgCTR:      metricDelta(oldGsc.AvgCTR, newGsc.AvgCTR),
		AvgPosition: metricDelta(oldGsc.AvgPosition, newGsc.AvgPosition),
		ZombiePages: intDelta(len(oldGsc.ZombiePages), len(newGsc.ZombiePages)),
	}

	// Traffic movers: compare per-URL click data
	var changes []URLMetricChange
	for url, newPage := range newMap {
		oldPage, ok := oldMap[url]
		if !ok || newPage.GscData == nil || oldPage.GscData == nil {
			continue
		}
		delta := float64(newPage.GscData.Clicks - oldPage.GscData.Clicks)
		if delta != 0 {
			changes = append(changes, URLMetricChange{
				URL:   url,
				Old:   float64(oldPage.GscData.Clicks),
				New:   float64(newPage.GscData.Clicks),
				Delta: delta,
			})
		}
	}

	// Sort descending by delta: winners at front, losers at back
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Delta > changes[j].Delta
	})

	// Winners: take from the front (positive deltas)
	for i := 0; i < len(changes) && i < 20; i++ {
		if changes[i].Delta <= 0 {
			break
		}
		section.Winners = append(section.Winners, changes[i])
	}

	// Losers: take from the back (negative deltas)
	for i := len(changes) - 1; i >= 0 && len(section.Losers) < 20; i-- {
		if changes[i].Delta >= 0 {
			break
		}
		section.Losers = append(section.Losers, changes[i])
	}

	return section
}

func safeGscStats(s *types.GscStats) types.GscStats {
	if s == nil {
		return types.GscStats{}
	}
	return *s
}

func compareNgrams(oldStats, newStats *types.CrawlStats) *NgramSection {
	if oldStats.NgramStats == nil && newStats.NgramStats == nil {
		return nil
	}

	section := &NgramSection{}

	oldBigrams := make(map[string]types.NgramEntry)
	newBigrams := make(map[string]types.NgramEntry)

	if oldStats.NgramStats != nil {
		for _, e := range oldStats.NgramStats.Bigrams {
			oldBigrams[e.Term] = e
		}
	}
	if newStats.NgramStats != nil {
		for _, e := range newStats.NgramStats.Bigrams {
			newBigrams[e.Term] = e
		}
	}

	// Emerging: in new top but not in old
	for term, e := range newBigrams {
		if _, ok := oldBigrams[term]; !ok {
			section.EmergingTopics = append(section.EmergingTopics, NgramChange{
				Term:  term,
				Count: e.Count,
				Pages: e.Pages,
			})
		}
	}

	// Declining: in old top but not in new
	for term, e := range oldBigrams {
		if _, ok := newBigrams[term]; !ok {
			section.DecliningTopics = append(section.DecliningTopics, NgramChange{
				Term:  term,
				Count: e.Count,
				Pages: e.Pages,
			})
		}
	}

	sort.Slice(section.EmergingTopics, func(i, j int) bool {
		return section.EmergingTopics[i].Count > section.EmergingTopics[j].Count
	})
	sort.Slice(section.DecliningTopics, func(i, j int) bool {
		return section.DecliningTopics[i].Count > section.DecliningTopics[j].Count
	})

	// Limit
	if len(section.EmergingTopics) > 30 {
		section.EmergingTopics = section.EmergingTopics[:30]
	}
	if len(section.DecliningTopics) > 30 {
		section.DecliningTopics = section.DecliningTopics[:30]
	}

	return section
}

func compareEmbeddings(oldStats, newStats *types.CrawlStats) *EmbeddingSection {
	if oldStats.EmbeddingStats == nil && newStats.EmbeddingStats == nil {
		return nil
	}

	oldE := safeEmbeddingStats(oldStats.EmbeddingStats)
	newE := safeEmbeddingStats(newStats.EmbeddingStats)

	return &EmbeddingSection{
		SimilarPairs:          intDelta(len(oldE.SimilarPairs), len(newE.SimilarPairs)),
		CannibalizationGroups: intDelta(len(oldE.CannibalizationGroups), len(newE.CannibalizationGroups)),
	}
}

func safeEmbeddingStats(s *types.EmbeddingStats) types.EmbeddingStats {
	if s == nil {
		return types.EmbeddingStats{}
	}
	return *s
}

func compareSegments(oldStats, newStats *types.CrawlStats) []SegmentComparison {
	oldSegMap := make(map[string]types.SegmentStat)
	for _, s := range oldStats.SegmentStats {
		oldSegMap[s.Name] = s
	}
	newSegMap := make(map[string]types.SegmentStat)
	for _, s := range newStats.SegmentStats {
		newSegMap[s.Name] = s
	}

	// Collect all segment names from both crawls
	segNames := make(map[string]bool)
	for _, s := range oldStats.SegmentStats {
		segNames[s.Name] = true
	}
	for _, s := range newStats.SegmentStats {
		segNames[s.Name] = true
	}

	var result []SegmentComparison
	for name := range segNames {
		old := oldSegMap[name]
		neu := newSegMap[name]
		result = append(result, SegmentComparison{
			Name:          name,
			Pages:         intDelta(old.PageCount, neu.PageCount),
			Indexable:     intDelta(old.Indexable, neu.Indexable),
			AvgWordCount:  metricDelta(old.AvgWordCount, neu.AvgWordCount),
			AvgResponseMs: metricDelta(old.AvgResponseTimeMs, neu.AvgResponseTimeMs),
			Clicks:        intDelta(old.TotalClicks, neu.TotalClicks),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Pages.New > result[j].Pages.New
	})

	return result
}

// collectFindings generates actionable insights from the comparison.
func collectFindings(r *ComparisonReport, oldStats, newStats *types.CrawlStats, oldMap, newMap map[string]*types.PageData) []Finding {
	var findings []Finding

	// Health score
	if r.HealthScore.Delta < -5 {
		findings = append(findings, Finding{
			Section:  "healthScore",
			Message:  fmt.Sprintf("Site health score dropped from %.1f to %.1f (%.1f points)", r.HealthScore.OldScore, r.HealthScore.NewScore, r.HealthScore.Delta),
			Severity: SeverityCritical,
			Impact:   100,
		})
	} else if r.HealthScore.Delta > 5 {
		findings = append(findings, Finding{
			Section:  "healthScore",
			Message:  fmt.Sprintf("Site health score improved from %.1f to %.1f (+%.1f points)", r.HealthScore.OldScore, r.HealthScore.NewScore, r.HealthScore.Delta),
			Severity: SeverityInfo,
			Impact:   100,
		})
	}

	// Coverage
	if r.Coverage.DisappearedURLs > 0 {
		pct := float64(r.Coverage.DisappearedURLs) / float64(max(r.Coverage.TotalPages.Old, 1)) * 100
		sev := SeverityInfo
		if pct > 10 {
			sev = SeverityCritical
		} else if pct > 5 {
			sev = SeverityWarning
		}
		findings = append(findings, Finding{
			Section:  "coverage",
			Message:  fmt.Sprintf("%d pages disappeared (%.1f%% of previous crawl)", r.Coverage.DisappearedURLs, pct),
			Severity: sev,
			Impact:   r.Coverage.DisappearedURLs,
		})
	}

	// Disappeared with traffic
	if r.URLs.Lifecycle != nil && len(r.URLs.Lifecycle.DisappearedWithTraffic) > 0 {
		totalClicks := 0
		for _, u := range r.URLs.Lifecycle.DisappearedWithTraffic {
			totalClicks += u.Clicks
		}
		findings = append(findings, Finding{
			Section:  "coverage",
			Message:  fmt.Sprintf("%d disappeared pages had traffic (%d total clicks)", len(r.URLs.Lifecycle.DisappearedWithTraffic), totalClicks),
			Severity: SeverityCritical,
			Impact:   totalClicks,
		})
	}

	// HTTP status regressions
	for _, m := range r.HTTPStatus.Migrations {
		if m.From == "2xx" && (m.To == "4xx" || m.To == "5xx") {
			findings = append(findings, Finding{
				Section:  "httpStatus",
				Message:  fmt.Sprintf("%d pages moved from %s to %s", m.Count, m.From, m.To),
				Severity: SeverityCritical,
				Impact:   m.Count * 10,
			})
		}
	}

	// Fixed pages
	for _, m := range r.HTTPStatus.Migrations {
		if (m.From == "4xx" || m.From == "5xx") && m.To == "2xx" {
			findings = append(findings, Finding{
				Section:  "httpStatus",
				Message:  fmt.Sprintf("%d pages fixed (moved from %s to %s)", m.Count, m.From, m.To),
				Severity: SeverityInfo,
				Impact:   m.Count,
			})
		}
	}

	// Broken links
	if r.HTTPStatus.BrokenLinks.Delta > 0 {
		findings = append(findings, Finding{
			Section:  "httpStatus",
			Message:  fmt.Sprintf("%d new broken links detected", r.HTTPStatus.BrokenLinks.Delta),
			Severity: SeverityWarning,
			Impact:   r.HTTPStatus.BrokenLinks.Delta,
		})
	} else if r.HTTPStatus.BrokenLinks.Delta < 0 {
		findings = append(findings, Finding{
			Section:  "httpStatus",
			Message:  fmt.Sprintf("%d broken links fixed", -r.HTTPStatus.BrokenLinks.Delta),
			Severity: SeverityInfo,
			Impact:   -r.HTTPStatus.BrokenLinks.Delta,
		})
	}

	// Indexability
	if r.Indexability.Indexable.Delta < 0 {
		findings = append(findings, Finding{
			Section:  "indexability",
			Message:  fmt.Sprintf("Indexable pages decreased by %d (from %d to %d)", -r.Indexability.Indexable.Delta, r.Indexability.Indexable.Old, r.Indexability.Indexable.New),
			Severity: SeverityWarning,
			Impact:   -r.Indexability.Indexable.Delta * 5,
		})
	}
	if len(r.Indexability.BecameNonIndexable) > 10 {
		findings = append(findings, Finding{
			Section:  "indexability",
			Message:  fmt.Sprintf("%d pages became non-indexable", len(r.Indexability.BecameNonIndexable)),
			Severity: SeverityWarning,
			Impact:   len(r.Indexability.BecameNonIndexable) * 3,
		})
	}

	// On-page SEO
	if r.OnPageSEO.WithoutTitle.Delta > 0 {
		findings = append(findings, Finding{
			Section:  "seo",
			Message:  fmt.Sprintf("%d more pages without title tags", r.OnPageSEO.WithoutTitle.Delta),
			Severity: SeverityWarning,
			Impact:   r.OnPageSEO.WithoutTitle.Delta * 3,
		})
	}
	if r.OnPageSEO.WithoutDescription.Delta > 0 {
		findings = append(findings, Finding{
			Section:  "seo",
			Message:  fmt.Sprintf("%d more pages without meta descriptions", r.OnPageSEO.WithoutDescription.Delta),
			Severity: SeverityWarning,
			Impact:   r.OnPageSEO.WithoutDescription.Delta * 2,
		})
	}

	// Content
	if r.Content.ThinContentPages.Delta > 0 {
		findings = append(findings, Finding{
			Section:  "content",
			Message:  fmt.Sprintf("%d more thin content pages detected", r.Content.ThinContentPages.Delta),
			Severity: SeverityWarning,
			Impact:   r.Content.ThinContentPages.Delta,
		})
	}
	if r.Content.DuplicateGroups.Delta > 0 {
		findings = append(findings, Finding{
			Section:  "content",
			Message:  fmt.Sprintf("%d new duplicate content groups", r.Content.DuplicateGroups.Delta),
			Severity: SeverityWarning,
			Impact:   r.Content.DuplicateGroups.Delta * 2,
		})
	}

	// Orphan pages
	if r.Links.OrphanPages.Delta > 0 {
		findings = append(findings, Finding{
			Section:  "links",
			Message:  fmt.Sprintf("%d new orphan pages (no internal links pointing to them)", r.Links.OrphanPages.Delta),
			Severity: SeverityWarning,
			Impact:   r.Links.OrphanPages.Delta * 3,
		})
	}

	// Performance
	if r.Performance.ResponseP90.Delta > 500 {
		findings = append(findings, Finding{
			Section:  "performance",
			Message:  fmt.Sprintf("Response time p90 increased by %.0fms (%.0f → %.0f)", r.Performance.ResponseP90.Delta, r.Performance.ResponseP90.Old, r.Performance.ResponseP90.New),
			Severity: SeverityWarning,
			Impact:   int(r.Performance.ResponseP90.Delta / 100),
		})
	} else if r.Performance.ResponseP90.Delta < -500 {
		findings = append(findings, Finding{
			Section:  "performance",
			Message:  fmt.Sprintf("Response time p90 improved by %.0fms", -r.Performance.ResponseP90.Delta),
			Severity: SeverityInfo,
			Impact:   int(-r.Performance.ResponseP90.Delta / 100),
		})
	}

	// Visibility
	if r.Visibility != nil {
		if r.Visibility.Clicks.Delta < 0 {
			pct := float64(-r.Visibility.Clicks.Delta) / float64(max(r.Visibility.Clicks.Old, 1)) * 100
			sev := SeverityWarning
			if pct > 10 {
				sev = SeverityCritical
			}
			findings = append(findings, Finding{
				Section:  "visibility",
				Message:  fmt.Sprintf("Total clicks decreased by %d (%.1f%%)", -r.Visibility.Clicks.Delta, pct),
				Severity: sev,
				Impact:   -r.Visibility.Clicks.Delta,
			})
		} else if r.Visibility.Clicks.Delta > 0 {
			findings = append(findings, Finding{
				Section:  "visibility",
				Message:  fmt.Sprintf("Total clicks increased by %d", r.Visibility.Clicks.Delta),
				Severity: SeverityInfo,
				Impact:   r.Visibility.Clicks.Delta,
			})
		}
	}

	// Security
	if r.Security.MixedContent.Delta > 0 {
		findings = append(findings, Finding{
			Section:  "security",
			Message:  fmt.Sprintf("%d new pages with mixed content", r.Security.MixedContent.Delta),
			Severity: SeverityWarning,
			Impact:   r.Security.MixedContent.Delta,
		})
	}

	return findings
}

// --- Output ---

// PrintComparisonSummary writes a text summary of the comparison report to w.
func PrintComparisonSummary(w io.Writer, r *ComparisonReport) {
	fmt.Fprintf(w, "Crawl Comparison Report\n")
	fmt.Fprintf(w, "=======================\n\n")

	// Health score
	fmt.Fprintf(w, "Health Score: %.1f → %.1f", r.HealthScore.OldScore, r.HealthScore.NewScore)
	if r.HealthScore.Delta > 0 {
		fmt.Fprintf(w, " (+%.1f ↑)\n", r.HealthScore.Delta)
	} else if r.HealthScore.Delta < 0 {
		fmt.Fprintf(w, " (%.1f ↓)\n", r.HealthScore.Delta)
	} else {
		fmt.Fprintf(w, " (unchanged)\n")
	}

	// Coverage
	fmt.Fprintf(w, "\nPages: %d → %d", r.Coverage.TotalPages.Old, r.Coverage.TotalPages.New)
	fmt.Fprintf(w, " (new: %d, disappeared: %d, persisted: %d)\n",
		r.Coverage.NewURLs, r.Coverage.DisappearedURLs, r.Coverage.PersistedURLs)

	// Key metrics
	fmt.Fprintf(w, "Indexable: %d → %d (%+d)\n", r.Indexability.Indexable.Old, r.Indexability.Indexable.New, r.Indexability.Indexable.Delta)
	fmt.Fprintf(w, "Response p90: %.0fms → %.0fms (%+.0fms)\n", r.Performance.ResponseP90.Old, r.Performance.ResponseP90.New, r.Performance.ResponseP90.Delta)

	// Findings
	if len(r.Findings) > 0 {
		critCount := 0
		warnCount := 0
		infoCount := 0
		for _, f := range r.Findings {
			switch f.Severity {
			case SeverityCritical:
				critCount++
			case SeverityWarning:
				warnCount++
			case SeverityInfo:
				infoCount++
			}
		}
		fmt.Fprintf(w, "\nFindings: %d critical, %d warnings, %d info\n", critCount, warnCount, infoCount)

		for _, f := range r.Findings {
			prefix := "  "
			switch f.Severity {
			case SeverityCritical:
				prefix = "  !! "
			case SeverityWarning:
				prefix = "  !  "
			case SeverityInfo:
				prefix = "     "
			}
			fmt.Fprintf(w, "%s%s\n", prefix, f.Message)
		}
	}
}

// --- Helpers ---

func intDelta(old, new int) IntDelta {
	return IntDelta{Old: old, New: new, Delta: new - old}
}

func metricDelta(old, new float64) MetricDelta {
	return MetricDelta{
		Old:   math.Round(old*100) / 100,
		New:   math.Round(new*100) / 100,
		Delta: math.Round((new-old)*100) / 100,
	}
}

