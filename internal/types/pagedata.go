package types

import "time"

// --- Fetch ---

// CacheEntry holds cached ETag/Last-Modified for conditional requests.
type CacheEntry struct {
	ETag         string
	LastModified string
}

// RedirectHop represents a single redirect in a chain.
type RedirectHop struct {
	URL        string `json:"url"`
	StatusCode int    `json:"statusCode"`
}

// FetchResult holds the raw HTTP response data.
type FetchResult struct {
	Headers        map[string]string `json:"headers"`
	URL            string            `json:"url"`
	FinalURL       string            `json:"finalUrl"`
	HTML           string            `json:"html"`
	RawHTML        string            `json:"rawHtml,omitempty"`
	ContentType    string            `json:"contentType"`
	Error          string            `json:"error,omitempty"`
	RedirectChain  []RedirectHop     `json:"redirectChain"`
	StatusCode     int               `json:"statusCode"`
	ResponseTimeMs int64             `json:"responseTimeMs"`
	TransferSize   int64             `json:"transferSize"`
	BodySize       int64             `json:"bodySize"` // uncompressed HTML body length in bytes
	NotModified    bool              `json:"notModified,omitempty"`
}

// --- Main Page Data ---

// PageData is the primary data structure for a crawled page.
type PageData struct {
	CrawledAt          time.Time               `json:"crawledAt"`
	CustomSearches     map[string]bool         `json:"customSearches,omitempty"`
	CanonicalRaw       *string                 `json:"canonicalRaw,omitempty"`
	SnippetResults     map[string]any          `json:"snippetResults,omitempty"`
	PlausibleData      *PlausibleData          `json:"plausibleData,omitempty"`
	AIVisibilityData   *AIVisibilityData       `json:"aiVisibilityData,omitempty"`
	Title              *TextLength             `json:"title"`
	MetaDescription    *TextLength             `json:"metaDescription"`
	Canonical          *string                 `json:"canonical,omitempty"`
	LinkIntelligence   *LinkIntelligenceData   `json:"linkIntelligence,omitempty"`
	SitemapData        *SitemapAuditData       `json:"sitemapData,omitempty"`
	MetaRobots         *string                 `json:"metaRobots,omitempty"`
	URLStructure       *URLStructureData       `json:"urlStructure,omitempty"`
	XRobotsTag         *string                 `json:"xRobotsTag,omitempty"`
	CruxData           *CruxData               `json:"cruxData,omitempty"`
	Ga4Data            *Ga4Data                `json:"ga4Data,omitempty"`
	GscData            *GscData                `json:"gscData,omitempty"`
	AIAnalysis         *string                 `json:"aiAnalysis,omitempty"`
	Pagespeed          *PageSpeedData          `json:"pagespeed,omitempty"`
	CustomExtractions  map[string][]string     `json:"customExtractions,omitempty"`
	Readability        *ReadabilityData        `json:"readability,omitempty"`
	Freshness          *FreshnessData          `json:"freshness,omitempty"`
	ContentRichness    *ContentRichnessData    `json:"contentRichness,omitempty"`
	EEAT               *EEATData               `json:"eeat,omitempty"`
	PassageReadiness   *PassageReadinessData   `json:"passageReadiness,omitempty"`
	AIReadiness        *AIReadinessData        `json:"aiReadiness,omitempty"`
	ConsensusReadiness *ConsensusReadinessData `json:"consensusReadiness,omitempty"`
	Topicality         *TopicalityData         `json:"topicality,omitempty"`
	AnchorHealth       *AnchorHealthData       `json:"anchorHealth,omitempty"`
	PageWeight         *PageWeightData         `json:"pageWeight,omitempty"`
	OpenGraph          map[string]string       `json:"openGraph,omitempty"`
	TwitterCard        map[string]string       `json:"twitterCard,omitempty"`
	Indexability       IndexabilityData        `json:"indexability"`
	SimhashFingerprint string                  `json:"simhashFingerprint,omitempty"`
	ContentHash        string                  `json:"contentHash,omitempty"`
	URL                string                  `json:"url"`
	BodyText           string                  `json:"-"`
	Error              string                  `json:"error,omitempty"`
	TemplateType       string                  `json:"templateType,omitempty"`
	FinalURL           string                  `json:"finalUrl"`
	Headings           HeadingData             `json:"headings"`
	Anchors            []AnchorData            `json:"anchors,omitempty"`
	Segments           []string                `json:"segments,omitempty"`
	RedirectChain      []RedirectHop           `json:"redirectChain,omitempty"`
	StructuredData     []StructuredDataEntry   `json:"structuredData,omitempty"`
	Hreflang           []HreflangEntry         `json:"hreflang,omitempty"`
	URLIssues          []string                `json:"urlIssues,omitempty"`
	InternalLinks      []string                `json:"internalLinks,omitempty"`
	RenderDiffs        []RenderDiff            `json:"renderDiffs,omitempty"`
	JSErrors           []string                `json:"jsErrors,omitempty"`
	SchemaValidation   []SchemaValidationEntry `json:"schemaValidation,omitempty"`
	Images             []ImageData             `json:"images,omitempty"`
	ExternalLinks      []string                `json:"externalLinks,omitempty"`
	Security           SecurityData            `json:"security"`
	ResponseTimeMs     int64                   `json:"responseTimeMs"`
	InternalLinkCount  int                     `json:"internalLinkCount,omitempty"`
	ExternalLinkCount  int                     `json:"externalLinkCount,omitempty"`
	Depth              int                     `json:"depth"`
	TextToCodeRatio    float64                 `json:"textToCodeRatio,omitempty"`
	CanonicalCount     int                     `json:"canonicalCount,omitempty"`
	WordCount          int                     `json:"wordCount"`
	Inlinks            int                     `json:"inlinks,omitempty"`
	PageRank           float64                 `json:"pageRank,omitempty"`
	StatusCode         int                     `json:"statusCode"`
	ListingSignals     int                     `json:"listingSignals,omitempty"`   // count of HTML elements matching listing/grid/results patterns
	AdDetailSignals    int                     `json:"adDetailSignals,omitempty"`  // net score: ad detail signals minus e-commerce signals
	OriginalityScore   int                     `json:"originalityScore,omitempty"` // 0-127, proxy for Google's OriginalContentScore (Leak)
	TruncatedElements  []string                `json:"truncatedElements,omitempty"` // SEO elements beyond Googlebot's 2MB cutoff
	BodySize           int64                   `json:"bodySize,omitempty"`          // uncompressed HTML body size in bytes
	IsSoft404          bool                    `json:"isSoft404,omitempty"`
	RobotsBlocked      bool                    `json:"robotsBlocked,omitempty"`
	URLClassification  string                  `json:"urlClassification,omitempty"`
	ETag               string                  `json:"etag,omitempty"`
	LastModified       string                  `json:"lastModified,omitempty"`
	NotModified        bool                    `json:"notModified,omitempty"`
	ContentChanged     *bool                   `json:"contentChanged,omitempty"`
}

// --- HEAD-only Crawl ---

// HeadResult holds data from a HEAD-only request.
type HeadResult struct {
	XFrameOptions  *string           `json:"xFrameOptions"`
	ReferrerPolicy *string           `json:"referrerPolicy"`
	Headers        map[string]string `json:"headers"`
	CacheControl   *string           `json:"cacheControl"`
	XRobotsTag     *string           `json:"xRobotsTag"`
	LinkCanonical  *string           `json:"linkCanonical"`
	ContentLength  *int64            `json:"contentLength"`
	Server         string            `json:"server"`
	FinalURL       string            `json:"finalUrl"`
	ContentType    string            `json:"contentType"`
	URL            string            `json:"url"`
	Error          string            `json:"error,omitempty"`
	RedirectChain  []RedirectHop     `json:"redirectChain"`
	ResponseTimeMs int64             `json:"responseTimeMs"`
	StatusCode     int               `json:"statusCode"`
	CSP            bool              `json:"csp"`
	HSTS           bool              `json:"hsts"`
}

// --- Queue ---

// QueueEntry represents a URL waiting to be crawled.
type QueueEntry struct {
	Referrer *string `json:"referrer"`
	URL      string  `json:"url"`
	Depth    int     `json:"depth"`
	Requeues int     `json:"requeues,omitempty"` // number of times re-enqueued (e.g. after 429)
}

// DeadLetterEntry records a URL that failed permanently after all retries.
type DeadLetterEntry struct {
	FailedAt   time.Time `json:"failedAt"`
	URL        string    `json:"url"`
	Error      string    `json:"error"`
	Referrer   string    `json:"referrer,omitempty"`
	StatusCode int       `json:"statusCode,omitempty"`
	Depth      int       `json:"depth"`
	Requeues   int       `json:"requeues"`
}

// --- Diff ---

// DiffFieldChange represents a single field change between crawls.
type DiffFieldChange struct {
	OldValue any    `json:"oldValue"`
	NewValue any    `json:"newValue"`
	Field    string `json:"field"`
}

// DiffURLChange represents all changes for a single URL.
type DiffURLChange struct {
	URL     string            `json:"url"`
	Changes []DiffFieldChange `json:"changes"`
}

// DiffResult holds the complete diff between two crawl outputs.
type DiffResult struct {
	FieldSummary       map[string]int  `json:"fieldSummary"`
	OldFile            string          `json:"oldFile"`
	NewFile            string          `json:"newFile"`
	AddedURLs          []string        `json:"addedUrls"`
	RemovedURLs        []string        `json:"removedUrls"`
	ChangedURLs        []DiffURLChange `json:"changedUrls"`
	OldCount           int             `json:"oldCount"`
	NewCount           int             `json:"newCount"`
	UnchangedCount     int             `json:"unchangedCount"`
	URLMappingsApplied int             `json:"urlMappingsApplied"`
}

// --- External Link Check ---

// ExternalLinkResult holds the check result for an external link.
type ExternalLinkResult struct {
	URL        string   `json:"url"`
	Error      string   `json:"error,omitempty"`
	FoundOn    []string `json:"foundOn"`
	StatusCode int      `json:"statusCode"`
}
