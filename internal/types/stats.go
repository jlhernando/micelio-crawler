package types

// --- Sitemap Stats ---

// SitemapStats holds aggregate sitemap audit results.
type SitemapStats struct {
	StatusBreakdown       map[int]int             `json:"statusBreakdown"`
	RedirectsInSitemap    []string                `json:"redirectsInSitemap"`
	OrphanURLs            []string                `json:"orphanUrls"`
	MissingFromSitemap    []string                `json:"missingFromSitemap"`
	NonIndexableInSitemap []string                `json:"nonIndexableInSitemap"`
	SitemapErrors         []string                `json:"sitemapErrors"`
	UncrawledSitemapURLs  []string                `json:"uncrawledSitemapUrls"`
	DuplicateAcross       []DuplicateSitemapEntry `json:"duplicateAcrossSitemaps"`
	ValidationWarnings    []string                `json:"validationWarnings"`
	LastmodStats          LastmodStats            `json:"lastmodStats"`
	Coverage              SitemapCoverage         `json:"coverage"`
	TotalSitemapURLs      int                     `json:"totalSitemapUrls"`
	ImageEntryCount       int                     `json:"imageEntryCount"`
	VideoEntryCount       int                     `json:"videoEntryCount"`
	NewsEntryCount        int                     `json:"newsEntryCount"`
}

// LastmodStats holds sitemap lastmod analysis.
type LastmodStats struct {
	Missing int `json:"missing"`
	Stale   int `json:"stale"`
	Future  int `json:"future"`
	Invalid int `json:"invalid"`
}

// DuplicateSitemapEntry holds a URL found in multiple sitemaps.
type DuplicateSitemapEntry struct {
	URL     string   `json:"url"`
	Sources []string `json:"sources"`
}

// SitemapCoverage holds sitemap coverage metrics.
type SitemapCoverage struct {
	CrawledAndInSitemap int `json:"crawledAndInSitemap"`
	CrawledNotInSitemap int `json:"crawledNotInSitemap"`
	InSitemapNotCrawled int `json:"inSitemapNotCrawled"`
}

// --- Crawl Stats Support Types ---

// PercentileData holds response time percentiles.
type PercentileData struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
}

// ResponseTimeBuckets holds response time distribution in speed buckets.
type ResponseTimeBuckets struct {
	Fast    int `json:"fast"`    // <500ms
	Medium  int `json:"medium"`  // 500ms-1s
	Slow    int `json:"slow"`    // 1s-2s
	Slowest int `json:"slowest"` // >2s
}

// InlinkBuckets holds inlink count distribution across pages.
type InlinkBuckets struct {
	Zero       int `json:"zero"`       // 0 inlinks
	One        int `json:"one"`        // 1 inlink
	TwoToFive  int `json:"twoToFive"`  // 2-5 inlinks
	SixToTwenty int `json:"sixToTwenty"` // 6-20 inlinks
	TwentyPlus int `json:"twentyPlus"` // 20+ inlinks
}

// URLResponseTime is a URL with its response time.
type URLResponseTime struct {
	URL            string `json:"url"`
	ResponseTimeMs int64  `json:"responseTimeMs"`
}

// URLWordCount is a URL with its word count.
type URLWordCount struct {
	URL       string `json:"url"`
	WordCount int    `json:"wordCount"`
}

// BrokenLink holds a broken link and where it was found.
type BrokenLink struct {
	URL        string   `json:"url"`
	FoundOn    []string `json:"foundOn"`
	StatusCode int      `json:"statusCode"`
}

// RedirectChainInfo holds a URL with its redirect chain.
type RedirectChainInfo struct {
	URL   string        `json:"url"`
	Chain []RedirectHop `json:"chain"`
	Hops  int           `json:"hops,omitempty"`
}

// HashDuplicateGroup is a group of URLs sharing the same content hash.
type HashDuplicateGroup struct {
	Hash string   `json:"hash"`
	URLs []string `json:"urls"`
}

// NonDescriptiveAnchor holds a non-descriptive anchor reference.
type NonDescriptiveAnchor struct {
	URL     string `json:"url"`
	Text    string `json:"text"`
	FoundOn string `json:"foundOn"`
}

// HreflangIssue holds a URL with its hreflang problem.
type HreflangIssue struct {
	URL   string `json:"url"`
	Issue string `json:"issue"`
}

// NearDuplicateGroup is a group of near-duplicate URLs (SimHash).
type NearDuplicateGroup struct {
	URLs       []string `json:"urls"`
	Similarity int      `json:"similarity"`
}

// PerformanceScores holds min/avg/max performance scores.
type PerformanceScores struct {
	Avg float64 `json:"avg"`
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// --- Crawl Stats (main aggregation) ---

// CrawlStats is the main aggregation structure for crawl results.
// Kept as a struct with pointer fields for optional sections.
type CrawlStats struct {
	PageRankScores           map[string]float64     `json:"pageRankScores"`
	RenderCompareStats       *RenderCompareStats    `json:"renderCompareStats"`
	PageWeightStats          *PageWeightStats       `json:"pageWeightStats"`
	CruxStats                *CruxStats             `json:"cruxStats"`
	URLStructureStats        *URLStructureStats     `json:"urlStructureStats"`
	TemplateTypeDistribution map[string]int         `json:"templateTypeDistribution"`
	CanonicalStats           *CanonicalStats        `json:"canonicalStats"`
	RedirectStats            *RedirectStats         `json:"redirectStats"`
	NgramStats               *NgramStats            `json:"ngramStats"`
	EmbeddingStats           *EmbeddingStats        `json:"embeddingStats"`
	LinkIntelligenceStats    *LinkIntelligenceStats `json:"linkIntelligenceStats"`
	ImageAuditStats          *ImageAuditStats       `json:"imageAuditStats"`
	CustomSearchSummary      map[string]SearchStat  `json:"customSearchSummary"`
	StatusCodes              map[int]int            `json:"statusCodes"`
	GscStats                 *GscStats              `json:"gscStats"`
	Ga4Stats                 *Ga4Stats              `json:"ga4Stats"`
	SchemaValidationStats    *SchemaValidationStats `json:"schemaValidationStats"`
	TextToCodeStats          *TextToCodeStats       `json:"textToCodeStats"`
	PerformanceScores        *PerformanceScores     `json:"performanceScores"`
	URLIssueStats            map[string][]string    `json:"urlIssueStats"`
	ReadabilityStats         *ReadabilityStats      `json:"readabilityStats"`
	PlausibleStats           *PlausibleStats        `json:"plausibleStats"`
	SeoFunnelStats           *SeoFunnelStats        `json:"seoFunnelStats"`
	AIVisibilityStats        *AIVisibilityStats     `json:"aiVisibilityStats,omitempty"`
	FreshnessStats           *FreshnessStats        `json:"freshnessStats,omitempty"`
	ContentRichnessStats     *ContentRichnessStats  `json:"contentRichnessStats,omitempty"`
	EEATStats                *EEATStats             `json:"eeatStats,omitempty"`
	PassageReadinessStats    *PassageReadinessStats `json:"passageReadinessStats,omitempty"`
	AIReadinessStats         *AIReadinessStats      `json:"aiReadinessStats,omitempty"`
	TopicalityStats          *TopicalityStats       `json:"topicalityStats,omitempty"`
	AnchorHealthStats        *AnchorHealthStats     `json:"anchorHealthStats,omitempty"`
	AlertSummary             *AlertSummary          `json:"alertSummary,omitempty"`
	DepthDistribution        map[int]int            `json:"depthDistribution"`
	SitemapStats             *SitemapStats          `json:"sitemapStats"`
	BrokenExternalLinks      []ExternalLinkResult   `json:"brokenExternalLinks"`
	LongRedirectChains       []RedirectChainInfo    `json:"longRedirectChains"`
	MixedContentPages        []string               `json:"mixedContentPages"`
	IndexabilityStats        IndexabilityStats      `json:"indexabilityStats"`
	RedirectChains           []RedirectChainInfo    `json:"redirectChains"`
	BrokenLinks              []BrokenLink           `json:"brokenLinks"`
	NonDescriptiveAnchors    []NonDescriptiveAnchor `json:"nonDescriptiveAnchors"`
	DuplicateContentGroups   []HashDuplicateGroup   `json:"duplicateContentGroups"`
	ThinContentPages         []URLWordCount         `json:"thinContentPages"`
	NonHTTPSPages            []string               `json:"nonHttpsPages"`
	NearDuplicateGroups      []NearDuplicateGroup   `json:"nearDuplicateGroups"`
	SegmentStats             []SegmentStat          `json:"segmentStats"`
	HreflangIssues           []HreflangIssue        `json:"hreflangIssues"`
	Soft404Pages             []string               `json:"soft404Pages"`
	OrphanPages              []string               `json:"orphanPages"`
	OrphanMethodology         string                 `json:"orphanMethodology,omitempty"` // "reporter" (basic) or "graph" (redirect-aware via link intelligence)
	SlowPages                []URLResponseTime      `json:"slowPages"`
	LinkAnalysis             LinkAnalysis           `json:"linkAnalysis"`
	ResponseTimePercentiles  PercentileData         `json:"responseTimePercentiles"`
	ResponseTimeBuckets      ResponseTimeBuckets    `json:"responseTimeBuckets"`
	ImagesMissingAlt         int                    `json:"imagesMissingAlt"`
	TotalExternalLinks       int                    `json:"totalExternalLinks"`
	PagesWithStructuredData  int                    `json:"pagesWithStructuredData"`
	TotalInternalLinks       int                    `json:"totalInternalLinks"`
	TotalImages              int                    `json:"totalImages"`
	CrawlDurationMs          int64                  `json:"crawlDurationMs"`
	PagesWithoutOG           int                    `json:"pagesWithoutOg"`
	PagesWithAIAnalysis      int                    `json:"pagesWithAiAnalysis"`
	TotalPages               int                    `json:"totalPages"`
	PagesWithoutH1           int                    `json:"pagesWithoutH1"`
	PagesWithoutDescription  int                    `json:"pagesWithoutDescription"`
	PagesWithoutTitle        int                    `json:"pagesWithoutTitle"`
	RobotsBlockedCount       int                    `json:"robotsBlockedCount"`
	DuplicateTitleCount      int                    `json:"duplicateTitleCount"`
	DuplicateDescriptionCount int                   `json:"duplicateDescriptionCount"`
	MultipleH1Count          int                    `json:"multipleH1Count"`
	TitleTooLongCount        int                    `json:"titleTooLongCount"`
	TitleTooShortCount       int                    `json:"titleTooShortCount"`
	DescriptionTooLongCount  int                    `json:"descriptionTooLongCount"`
}

// SearchStat holds found/total for custom search.
type SearchStat struct {
	Found int `json:"found"`
	Total int `json:"total"`
}

// PageWeightStats holds aggregate page weight metrics.
type PageWeightStats struct {
	ByType              map[string]TypeWeightSummary `json:"byType"`
	HeaviestPages       []URLBytesEntry              `json:"heaviestPages"`
	OversizedPages      []URLBytesEntry              `json:"oversizedPages"`
	TruncationRiskPages []URLBytesEntry              `json:"truncationRiskPages"` // HTML body > 2MB (Googlebot limit)
	AvgTotalBytes       int64                        `json:"avgTotalBytes"`
}

// URLBytesEntry holds a URL with its byte size.
type URLBytesEntry struct {
	URL        string `json:"url"`
	TotalBytes int64  `json:"totalBytes"`
}

// IndexabilityStats holds aggregate indexability metrics.
type IndexabilityStats struct {
	Reasons      map[string]int `json:"reasons"`
	Indexable    int            `json:"indexable"`
	NonIndexable int            `json:"nonIndexable"`
}

// ReadabilityStats holds aggregate readability metrics.
type ReadabilityStats struct {
	Difficult     []URLScoreEntry `json:"difficult"`
	VeryEasy      []URLScoreEntry `json:"veryEasy"`
	AvgScore      float64         `json:"avgScore"`
	// Q* quality tier distribution (DOJ: Q* 0-1 scale, 0.4 threshold for rich results).
	QualityTier1  int             `json:"qualityTier1"`  // High quality (Q* proxy > 0.7)
	QualityTier2  int             `json:"qualityTier2"`  // Acceptable (Q* proxy 0.4-0.7)
	QualityTier3  int             `json:"qualityTier3"`  // Below rich-result threshold (< 0.4)
}

// URLScoreEntry holds a URL with a numeric score.
type URLScoreEntry struct {
	URL   string  `json:"url"`
	Score float64 `json:"score"`
}

// LinkAnalysis holds aggregate link structure metrics.
type LinkAnalysis struct {
	DeadEndPages            []string   `json:"deadEndPages"`
	LinksToNonIndexable     []LinkPair `json:"linksToNonIndexable"`
	NofollowedInternalLinks int        `json:"nofollowedInternalLinks"`
	FollowedInternalLinks   int        `json:"followedInternalLinks"`
}

// LinkPair holds a from->to URL pair.
type LinkPair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// TextToCodeStats holds text-to-code ratio metrics.
type TextToCodeStats struct {
	ContentPoor []URLRatioEntry `json:"contentPoor"`
	AvgRatio    float64         `json:"avgRatio"`
}

// URLRatioEntry holds a URL with its ratio.
type URLRatioEntry struct {
	URL   string  `json:"url"`
	Ratio float64 `json:"ratio"`
}

// SchemaValidationStats holds aggregate schema validation metrics.
type SchemaValidationStats struct {
	RichResultEligible   map[string]int `json:"richResultEligible"`
	TypeDistribution     map[string]int `json:"typeDistribution"`
	TopIssues            []MessageCount `json:"topIssues"`
	PagesWithSchema      int            `json:"pagesWithSchema"`
	PagesWithValidSchema int            `json:"pagesWithValidSchema"`
	PagesWithErrors      int            `json:"pagesWithErrors"`
}

// MessageCount holds a message with its occurrence count.
type MessageCount struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

// GscStats holds aggregate GSC metrics.
type GscStats struct {
	TopByClicks      []GscTopEntry `json:"topByClicks"`
	ZombiePages      []string      `json:"zombiePages"`
	PagesWithData    int           `json:"pagesWithData"`
	TotalImpressions int           `json:"totalImpressions"`
	TotalClicks      int           `json:"totalClicks"`
	AvgCTR           float64       `json:"avgCtr"`
	AvgPosition      float64       `json:"avgPosition"`
	Days             int           `json:"days"`
}

// GscTopEntry holds a top GSC URL entry.
type GscTopEntry struct {
	URL         string  `json:"url"`
	Clicks      int     `json:"clicks"`
	Impressions int     `json:"impressions"`
	Position    float64 `json:"position"`
}

// Ga4Stats holds aggregate GA4 metrics.
type Ga4Stats struct {
	TopByPageviews    []Ga4TopEntry `json:"topByPageviews"`
	NoTrafficPages    []string      `json:"noTrafficPages"`
	PagesWithData     int           `json:"pagesWithData"`
	TotalSessions     int           `json:"totalSessions"`
	TotalPageviews    int           `json:"totalPageviews"`
	TotalConversions  int           `json:"totalConversions"`
	AvgBounceRate     float64       `json:"avgBounceRate"`
	AvgEngagementRate float64       `json:"avgEngagementRate"`
	Days              int           `json:"days"`
}

// Ga4TopEntry holds a top GA4 URL entry.
type Ga4TopEntry struct {
	URL        string  `json:"url"`
	Pageviews  int     `json:"pageviews"`
	Sessions   int     `json:"sessions"`
	BounceRate float64 `json:"bounceRate"`
}

// RenderCompareStats holds aggregate render comparison metrics.
type RenderCompareStats struct {
	FieldDiffCounts map[string]int `json:"fieldDiffCounts"`
	CriticalDiffs   []CriticalDiff `json:"criticalDiffs"`
	PagesCompared   int            `json:"pagesCompared"`
	PagesWithDiffs  int            `json:"pagesWithDiffs"`
}

// CriticalDiff holds a critical render difference.
type CriticalDiff struct {
	URL      string `json:"url"`
	Field    string `json:"field"`
	Original string `json:"original"`
	Rendered string `json:"rendered"`
}

// SegmentStat holds metrics for a URL segment.
type SegmentStat struct {
	StatusCodes        map[int]int `json:"statusCodes"`
	Name               string      `json:"name"`
	AvgWordCount       float64     `json:"avgWordCount"`
	AvgResponseTimeMs  float64     `json:"avgResponseTimeMs"`
	Indexable          int         `json:"indexable"`
	NonIndexable       int         `json:"nonIndexable"`
	PageCount          int         `json:"pageCount"`
	TotalInternalLinks int         `json:"totalInternalLinks"`
	TotalExternalLinks int         `json:"totalExternalLinks"`
	PagesWithErrors    int         `json:"pagesWithErrors"`
	TotalImpressions   int         `json:"totalImpressions"`
	TotalClicks        int         `json:"totalClicks"`
	TotalSessions      int         `json:"totalSessions"`
	TotalPageviews     int         `json:"totalPageviews"`
}

// ImageAuditStats holds aggregate image audit metrics.
type ImageAuditStats struct {
	AltTooLong          []ImageAltIssue  `json:"altTooLong"`
	OversizedImages     []ImageSizeIssue `json:"oversizedImages"`
	TotalImages         int              `json:"totalImages"`
	MissingAltAttribute int              `json:"missingAltAttribute"`
	EmptyAlt            int              `json:"emptyAlt"`
	MissingDimensions   int              `json:"missingDimensions"`
	TotalImageBytes     int64            `json:"totalImageBytes"`
}

// ImageAltIssue holds an image with an overly long alt attribute.
type ImageAltIssue struct {
	URL       string `json:"url"`
	Src       string `json:"src"`
	AltLength int    `json:"altLength"`
}

// ImageSizeIssue holds an image with excessive file size.
type ImageSizeIssue struct {
	URL       string `json:"url"`
	Src       string `json:"src"`
	SizeBytes int64  `json:"sizeBytes"`
}

// LinkIntelligenceStats holds aggregate link intelligence metrics.
type LinkIntelligenceStats struct {
	LinkPositionDistribution     map[string]int         `json:"linkPositionDistribution"`
	SemanticLinkAnalysis         *SemanticLinkAnalysis  `json:"semanticLinkAnalysis"`
	ClickDepthDistribution       map[int]int            `json:"clickDepthDistribution"`
	CentralitySkipReason         string                 `json:"centralitySkipReason"`
	HitsSkipReason               string                 `json:"hitsSkipReason"`
	PagesWithNoContentLinks      []string               `json:"pagesWithNoContentLinks"`
	SinglePointOfFailure         []string               `json:"singlePointOfFailure"`
	UnreachablePages             []string               `json:"unreachablePages"`
	DilutionWarnings             []DilutionWarningEntry `json:"dilutionWarnings"`
	MostIsolated                 []URLScoreEntry        `json:"mostIsolated"`
	MostConnected                []URLScoreEntry        `json:"mostConnected"`
	NearOrphans                  []NearOrphanEntry      `json:"nearOrphans"`
	TopBridges                   []URLScoreEntry        `json:"topBridges"`
	TopAuthorities               []URLScoreEntry        `json:"topAuthorities"`
	TopHubs                      []URLScoreEntry        `json:"topHubs"`
	LinkSuggestions              []LinkSuggestion       `json:"linkSuggestions"`
	LinkSuggestionsCount         int                    `json:"linkSuggestionsCount"`
	PagesWithNoContentLinksCount int                    `json:"pagesWithNoContentLinksCount"`
	AvgClickDepth                float64                `json:"avgClickDepth"`
	UnreachablePagesCount        int                    `json:"unreachablePagesCount"`
	DilutionWarningsCount        int                    `json:"dilutionWarningsCount"`
	NearOrphansCount             int                    `json:"nearOrphansCount"`
	MaxClickDepth                int                    `json:"maxClickDepth"`
	Non2xxWithInlinks            int                    `json:"non2xxWithInlinks"`
	InlinkBuckets                InlinkBuckets          `json:"inlinkBuckets"`
	CentralitySkipped            bool                   `json:"centralitySkipped"`
	HitsSkipped                  bool                   `json:"hitsSkipped"`
}

// NearOrphanEntry holds a near-orphan page with its metrics.
type NearOrphanEntry struct {
	WorstSourceDepth *int   `json:"worstSourceDepth"`
	URL              string `json:"url"`
	InDegree         int    `json:"inDegree"`
}

// DilutionWarningEntry holds a page with link dilution issues.
// Source: Reasonable Surfer patent (US7716225), DOJ (log transform).
type DilutionWarningEntry struct {
	URL           string          `json:"url"`
	Warning       DilutionWarning `json:"warning"`
	OutDegree     int             `json:"outDegree"`
	EquityPerLink float64         `json:"equityPerLink,omitempty"` // Log-decay equity fraction per outlink
}

// RedirectStats holds aggregate redirect analysis.
type RedirectStats struct {
	ByStatusCode       map[int]int         `json:"byStatusCode"`
	SelfRedirects      []string            `json:"selfRedirects"`
	HTTPToHTTPS        []RedirectChainInfo `json:"httpToHttps"`
	WWWNormalization   []RedirectChainInfo `json:"wwwNormalization"`
	TrailingSlash      []RedirectChainInfo `json:"trailingSlash"`
	CrossDomain        []RedirectChainInfo `json:"crossDomain"`
	ChainLoops         []RedirectChainInfo `json:"chainLoops"`
	TemporaryRedirects []RedirectChainInfo `json:"temporaryRedirects"`
	LongestChains      []LongestChain      `json:"longestChains"`
	TopRedirectTargets []URLCountEntry     `json:"topRedirectTargets"`
	AvgChainLength     float64             `json:"avgChainLength"`
	MaxChainLength     int                 `json:"maxChainLength"`
	TotalHops          int                 `json:"totalHops"`
	TotalRedirects     int                 `json:"totalRedirects"`
	// NavBoost signal risk: 302/307 redirects lose NavBoost click signals (DOJ: 13-month rolling window).
	NavBoostAtRisk     int                 `json:"navBoostAtRisk"`
	// Long chains dilute NavBoost signals per hop.
	NavBoostDiluted    int                 `json:"navBoostDiluted"`
}

// LongestChain holds a redirect chain with its final URL.
type LongestChain struct {
	URL      string        `json:"url"`
	FinalURL string        `json:"finalUrl"`
	Chain    []RedirectHop `json:"chain"`
}

// URLCountEntry holds a URL with a count.
type URLCountEntry struct {
	URL   string `json:"url"`
	Count int    `json:"count"`
}

// CanonicalStats holds aggregate canonical tag analysis.
type CanonicalStats struct {
	CrossDomain              []CanonicalPair        `json:"crossDomain"`
	HTTPHTTPSMismatch        []CanonicalPair        `json:"httpHttpsMismatch"`
	TopCanonicalTargets      []URLCountEntry        `json:"topCanonicalTargets"`
	CanonicalToNon200        []CanonicalStatusIssue `json:"canonicalToNon200"`
	RelativeCanonicals       []RelativeCanonical    `json:"relativeCanonicals"`
	CanonicalChains          []CanonicalChain       `json:"canonicalChains"`
	CanonicalToNonIndexable  []CanonicalPair        `json:"canonicalToNonIndexable"`
	CanonicalWithQueryString []CanonicalPair        `json:"canonicalWithQueryString"`
	CanonicalLoops           []CanonicalLoop        `json:"canonicalLoops"`
	TotalWithCanonical       int                    `json:"totalWithCanonical"`
	TotalWithoutCanonical    int                    `json:"totalWithoutCanonical"`
	MultipleCanonicals       int                    `json:"multipleCanonicals"`
	Canonicalized            int                    `json:"canonicalized"`
	SelfReferencing          int                    `json:"selfReferencing"`
}

// CanonicalChain holds a canonical chain.
type CanonicalChain struct {
	URL   string   `json:"url"`
	Chain []string `json:"chain"`
}

// CanonicalLoop holds a canonical loop.
type CanonicalLoop struct {
	URL  string   `json:"url"`
	Loop []string `json:"loop"`
}

// CanonicalStatusIssue holds a canonical pointing to a non-200 page.
type CanonicalStatusIssue struct {
	URL          string `json:"url"`
	Canonical    string `json:"canonical"`
	TargetStatus int    `json:"targetStatus"`
}

// CanonicalPair holds a URL-canonical pair.
type CanonicalPair struct {
	URL       string `json:"url"`
	Canonical string `json:"canonical"`
	RawHref   string `json:"rawHref,omitempty"`
}

// RelativeCanonical holds a URL with a relative canonical href.
type RelativeCanonical struct {
	URL     string `json:"url"`
	RawHref string `json:"rawHref"`
}

// CruxStats holds aggregate CrUX metrics.
type CruxStats struct {
	AvgLCPMs      *float64        `json:"avgLcpMs"`
	AvgINPMs      *float64        `json:"avgInpMs"`
	AvgCLS        *float64        `json:"avgCls"`
	AvgTTFBMs     *float64        `json:"avgTtfbMs"`
	AvgFCPMs      *float64        `json:"avgFcpMs"`
	FormFactor    string          `json:"formFactor"`
	WorstLCP      []CruxURLMetric `json:"worstLcp"`
	WorstCLS      []CruxURLCLS    `json:"worstCls"`
	WorstINP      []CruxURLMetric `json:"worstInp"`
	PoorLCP       int             `json:"poorLcp"`
	PoorINP       int             `json:"poorInp"`
	GoodCLS       int             `json:"goodCls"`
	PoorCLS       int             `json:"poorCls"`
	GoodINP       int             `json:"goodInp"`
	PagesWithData int             `json:"pagesWithData"`
	GoodLCP       int             `json:"goodLcp"`
}

// CruxURLMetric holds a URL with a CrUX metric value (ms).
type CruxURLMetric struct {
	URL   string  `json:"url"`
	Value float64 `json:"lcpMs,omitempty"` // field name varies
}

// CruxURLCLS holds a URL with its CLS value.
type CruxURLCLS struct {
	URL string  `json:"url"`
	CLS float64 `json:"cls"`
}

// SeoFunnelStats holds the SEO funnel classification counts.
type SeoFunnelStats struct {
	Crawled      int     `json:"crawled"`
	Renderable   int     `json:"renderable"`
	Indexable    int     `json:"indexable"`
	Visible      int     `json:"visible"`
	Active       int     `json:"active"`
	NonIndexable int     `json:"nonIndexable"`
	PctRenderable float64 `json:"pctRenderable"`
	PctIndexable  float64 `json:"pctIndexable"`
	PctVisible    float64 `json:"pctVisible"`
	PctActive     float64 `json:"pctActive"`
	// Per-segment breakdown (by template type)
	Segments map[string]*SeoFunnelSegment `json:"segments,omitempty"`
}

// SeoFunnelSegment holds funnel counts for a specific content segment.
type SeoFunnelSegment struct {
	Crawled      int     `json:"crawled"`
	Renderable   int     `json:"renderable"`
	Indexable    int     `json:"indexable"`
	Visible      int     `json:"visible"`
	Active       int     `json:"active"`
	NonIndexable int     `json:"nonIndexable"`
	PctActive    float64 `json:"pctActive"`
}

// AIVisibilityStats holds aggregate AI visibility metrics.
type AIVisibilityStats struct {
	TopByClicks      []AIVisibilityEntry `json:"topByClicks"`
	TopQueries       []AIOverviewQuery   `json:"topQueries"`
	PagesInAIOverview int                `json:"pagesInAiOverview"`
	TotalAIImpressions int              `json:"totalAiImpressions"`
	TotalAIClicks      int              `json:"totalAiClicks"`
	AvgAICTR           float64          `json:"avgAiCtr"`
}

// AIVisibilityEntry holds a URL with its AI visibility metrics.
type AIVisibilityEntry struct {
	URL           string  `json:"url"`
	AIImpressions int     `json:"aiImpressions"`
	AIClicks      int     `json:"aiClicks"`
	AICTR         float64 `json:"aiCtr"`
}

// AlertSummary holds all triggered alerts for a crawl.
type AlertSummary struct {
	Alerts    []AlertResult `json:"alerts"`
	Critical  int           `json:"critical"`
	Warnings  int           `json:"warnings"`
	Info      int           `json:"info"`
	Timestamp string        `json:"timestamp"`
}

// AlertResult holds the outcome of evaluating one alert rule.
type AlertResult struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Severity string  `json:"severity"`
	Metric   string  `json:"metric"`
	Value    float64 `json:"value"`
	Message  string  `json:"message"`
}

// PlausibleStats holds aggregate Plausible metrics.
type PlausibleStats struct {
	AvgScrollDepth   *float64            `json:"avgScrollDepth"`
	TopByPageviews   []PlausibleTopEntry `json:"topByPageviews"`
	NoTrafficPages   []string            `json:"noTrafficPages"`
	PagesWithData    int                 `json:"pagesWithData"`
	TotalVisitors    int                 `json:"totalVisitors"`
	TotalVisits      int                 `json:"totalVisits"`
	TotalPageviews   int                 `json:"totalPageviews"`
	TotalConversions int                 `json:"totalConversions"`
	AvgBounceRate    float64             `json:"avgBounceRate"`
	AvgVisitDuration float64             `json:"avgVisitDuration"`
	Days             int                 `json:"days"`
}

// PlausibleTopEntry holds a top Plausible URL entry.
type PlausibleTopEntry struct {
	URL        string  `json:"url"`
	Pageviews  int     `json:"pageviews"`
	Visitors   int     `json:"visitors"`
	BounceRate float64 `json:"bounceRate"`
}

// --- Rankpedia-informed aggregate stats ---

// FreshnessStats holds aggregate content freshness metrics.
type FreshnessStats struct {
	DateSourceBreakdown map[string]int `json:"dateSourceBreakdown"`
	AgeDistribution     map[string]int `json:"ageDistribution"`
	PagesWithDate       int            `json:"pagesWithDate"`
	PagesWithoutDate    int            `json:"pagesWithoutDate"`
	InconsistentDates   int            `json:"inconsistentDates"`
	StaleSitemapLastmod int            `json:"staleSitemapLastmod"`
}

// ContentRichnessStats holds aggregate content richness metrics.
type ContentRichnessStats struct {
	AvgRichnessScore  float64 `json:"avgRichnessScore"`
	AvgHeadingDepth   float64 `json:"avgHeadingDepth"`
	LowRichnessPages  int     `json:"lowRichnessPages"`
	HighRichnessPages int     `json:"highRichnessPages"`
	PagesWithNoLists  int     `json:"pagesWithNoLists"`
	PagesWithTables   int     `json:"pagesWithTables"`
	PagesWithVideo    int     `json:"pagesWithVideo"`
}

// EEATStats holds aggregate E-E-A-T metrics.
type EEATStats struct {
	UniqueAuthors      int     `json:"uniqueAuthors"`
	PagesWithAuthor    int     `json:"pagesWithAuthor"`
	PagesWithoutAuthor int     `json:"pagesWithoutAuthor"`
	AvgCitationCount   float64 `json:"avgCitationCount"`
	AuthorCoverage     float64 `json:"authorCoverage"`
	HasAboutPage       bool    `json:"hasAboutPage"`
	HasContactPage     bool    `json:"hasContactPage"`
	HasEditorialPolicy bool    `json:"hasEditorialPolicy"`
}

// PassageReadinessStats holds aggregate passage readiness metrics.
type PassageReadinessStats struct {
	AvgPassageScore      float64 `json:"avgPassageScore"`
	AvgSectionsCount     float64 `json:"avgSectionsCount"`
	PagesWithFAQ         int     `json:"pagesWithFaq"`
	PagesWithHowTo       int     `json:"pagesWithHowTo"`
	PagesWithDefinitions int     `json:"pagesWithDefinitions"`
}

// AIReadinessStats holds aggregate AI Overview readiness metrics.
type AIReadinessStats struct {
	AvgAIReadinessScore        float64 `json:"avgAiReadinessScore"`
	PagesWithConciseDefinition int     `json:"pagesWithConciseDefinition"`
	PagesWithStructuredAnswer  int     `json:"pagesWithStructuredAnswer"`
	PagesWithFAQSchema         int     `json:"pagesWithFaqSchema"`
	PagesWithHowToSchema       int     `json:"pagesWithHowToSchema"`
}

// TopicalityStats holds aggregate T* alignment metrics.
type TopicalityStats struct {
	AvgTitleBodyOverlap    float64 `json:"avgTitleBodyOverlap"`
	AvgTitleH1Alignment    float64 `json:"avgTitleH1Alignment"`
	AvgTopicalConsistency  float64 `json:"avgTopicalConsistency"`
	LowAlignmentPages      int     `json:"lowAlignmentPages"`
	PerfectAlignmentPages  int     `json:"perfectAlignmentPages"`
}

// AnchorHealthStats holds aggregate anchor quality metrics.
type AnchorHealthStats struct {
	AvgInboundDiversity    float64 `json:"avgInboundDiversity"`
	PagesOverOptimized     int     `json:"pagesOverOptimized"`
	TotalGenericAnchors    int     `json:"totalGenericAnchors"`
	TotalNakedURLAnchors   int     `json:"totalNakedUrlAnchors"`
}
