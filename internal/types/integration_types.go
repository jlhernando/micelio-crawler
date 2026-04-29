package types

// --- Integration Data ---

// PageSpeedData holds PageSpeed Insights results.
type PageSpeedData struct {
	Error            string  `json:"error,omitempty"`
	PerformanceScore float64 `json:"performanceScore"`
	LCP              float64 `json:"lcp"`
	FID              float64 `json:"fid"`
	INP              float64 `json:"inp"`
	CLS              float64 `json:"cls"`
	TTFB             float64 `json:"ttfb"`
	SpeedIndex       float64 `json:"speedIndex"`
	TBT              float64 `json:"tbt"`
}

// GscData holds Google Search Console metrics for a URL.
type GscData struct {
	Impressions int     `json:"impressions"`
	Clicks      int     `json:"clicks"`
	CTR         float64 `json:"ctr"`
	Position    float64 `json:"position"`
}

// Ga4Data holds Google Analytics 4 metrics for a URL.
type Ga4Data struct {
	Sessions           int     `json:"sessions"`
	Pageviews          int     `json:"pageviews"`
	BounceRate         float64 `json:"bounceRate"`
	Conversions        int     `json:"conversions"`
	ActiveUsers        int     `json:"activeUsers"`
	EngagementRate     float64 `json:"engagementRate"`
	AvgSessionDuration float64 `json:"avgSessionDuration"`
}

// CruxData holds Chrome UX Report metrics for a URL.
type CruxData struct {
	LCPMs      *float64       `json:"lcpMs"`
	FIDMs      *float64       `json:"fidMs"`
	INPMs      *float64       `json:"inpMs"`
	CLS        *float64       `json:"cls"`
	TTFBMs     *float64       `json:"ttfbMs"`
	FCPMs      *float64       `json:"fcpMs"`
	FormFactor CrUXFormFactor `json:"formFactor"`
}

// PlausibleData holds Plausible Analytics metrics for a URL.
type PlausibleData struct {
	TimeOnPage     *float64 `json:"timeOnPage"`
	ScrollDepth    *float64 `json:"scrollDepth"`
	Visitors       int      `json:"visitors"`
	Visits         int      `json:"visits"`
	Pageviews      int      `json:"pageviews"`
	BounceRate     float64  `json:"bounceRate"`
	VisitDuration  float64  `json:"visitDuration"`
	ViewsPerVisit  float64  `json:"viewsPerVisit"`
	Conversions    int      `json:"conversions"`
	ConversionRate float64  `json:"conversionRate"`
}

// AIVisibilityData holds AI visibility metrics for a URL (e.g., AI Overviews in GSC).
type AIVisibilityData struct {
	Queries          []AIOverviewQuery `json:"queries,omitempty"`
	AIImpressions    int               `json:"aiImpressions"`
	AIClicks         int               `json:"aiClicks"`
	AICTR            float64           `json:"aiCtr"`
	InAIOverview     bool              `json:"inAiOverview"`
}

// AIOverviewQuery holds a single query that triggered an AI Overview appearance.
type AIOverviewQuery struct {
	Query       string  `json:"query"`
	Impressions int     `json:"impressions"`
	Clicks      int     `json:"clicks"`
	CTR         float64 `json:"ctr"`
	Position    float64 `json:"position"`
}

// --- Sitemap ---

// SitemapAuditData holds sitemap presence for a URL.
type SitemapAuditData struct {
	SitemapLastmod string `json:"sitemapLastmod,omitempty"`
	InSitemap      bool   `json:"inSitemap"`
}

// NewsSitemapEntry holds news-specific sitemap data.
type NewsSitemapEntry struct {
	Title               string `json:"title"`
	PublicationName     string `json:"publicationName"`
	PublicationLanguage string `json:"publicationLanguage"`
	PublicationDate     string `json:"publicationDate,omitempty"`
	Keywords            string `json:"keywords,omitempty"`
	Genres              string `json:"genres,omitempty"`
	StockTickers        string `json:"stockTickers,omitempty"`
}

// VideoSitemapEntry holds video-specific sitemap data.
type VideoSitemapEntry struct {
	Duration       *int     `json:"duration,omitempty"`
	Rating         *float64 `json:"rating,omitempty"`
	ViewCount      *int     `json:"viewCount,omitempty"`
	FamilyFriendly *bool    `json:"familyFriendly,omitempty"`
	Live           *bool    `json:"live,omitempty"`
	ThumbnailLoc   string   `json:"thumbnailLoc"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	ContentLoc     string   `json:"contentLoc,omitempty"`
	PlayerLoc      string   `json:"playerLoc,omitempty"`
	ExpirationDate string   `json:"expirationDate,omitempty"`
	Platform       string   `json:"platform,omitempty"`
}

// ImageSitemapEntry holds image-specific sitemap data.
type ImageSitemapEntry struct {
	Loc         string `json:"loc"`
	Caption     string `json:"caption,omitempty"`
	GeoLocation string `json:"geoLocation,omitempty"`
	Title       string `json:"title,omitempty"`
	License     string `json:"license,omitempty"`
}

// SitemapEntry holds a single URL entry from a sitemap.
type SitemapEntry struct {
	URL         string              `json:"url"`
	Lastmod     string              `json:"lastmod,omitempty"`
	Changefreq  string              `json:"changefreq,omitempty"`
	Priority    string              `json:"priority,omitempty"`
	Source      string              `json:"source"`
	SitemapType SitemapType         `json:"sitemapType,omitempty"`
	News        []NewsSitemapEntry  `json:"news,omitempty"`
	Videos      []VideoSitemapEntry `json:"videos,omitempty"`
	Images      []ImageSitemapEntry `json:"images,omitempty"`
}

// --- Page Weight ---

// ResourceEntry holds data for a single page resource.
type ResourceEntry struct {
	SizeBytes *int64       `json:"sizeBytes"`
	URL       string       `json:"url"`
	Type      ResourceType `json:"type"`
}

// PageWeightData holds total page weight breakdown.
type PageWeightData struct {
	ByType     map[string]TypeWeightSummary `json:"byType"`
	Resources  []ResourceEntry              `json:"resources"`
	TotalBytes int64                        `json:"totalBytes"`
	HTMLBytes  int64                        `json:"htmlBytes"`
}

// TypeWeightSummary holds aggregated weight for a resource type.
type TypeWeightSummary struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

// --- Link Intelligence ---

// LinkIntelligenceData holds link graph metrics for a single page.
type LinkIntelligenceData struct {
	ClickDepth            *int    `json:"clickDepth"`
	InDegree              int     `json:"inDegree"`
	OutDegree             int     `json:"outDegree"`
	IsNearOrphan          bool    `json:"isNearOrphan"`
	LinkDilutionFactor    float64 `json:"linkDilutionFactor"`
	HubScore              float64 `json:"hubScore"`
	AuthorityScore        float64 `json:"authorityScore"`
	BetweennessCentrality float64 `json:"betweennessCentrality"`
	ClosenessCentrality   float64 `json:"closenessCentrality"`
	ContentLinksCount     int     `json:"contentLinksCount"`
	NavLinksCount         int     `json:"navLinksCount"`
	FooterLinksCount      int     `json:"footerLinksCount"`
	SidebarLinksCount     int     `json:"sidebarLinksCount"`
	HeaderLinksCount      int     `json:"headerLinksCount"`
	OtherLinksCount       int     `json:"otherLinksCount"`
}

// LinkSuggestion represents a recommended internal link.
type LinkSuggestion struct {
	SourceURL string                `json:"sourceUrl"`
	TargetURL string                `json:"targetUrl"`
	Reason    string                `json:"reason"`
	Signals   LinkSuggestionSignals `json:"signals"`
	Score     float64               `json:"score"`
}

// LinkSuggestionSignals holds the scoring signals for a link suggestion.
type LinkSuggestionSignals struct {
	SemanticSimilarity *float64 `json:"semanticSimilarity"`
	TargetClickDepth   *int     `json:"targetClickDepth"`
	DepthReduction     *int     `json:"depthReduction"`
	TargetInDegree     int      `json:"targetInDegree"`
	TargetPageRank     float64  `json:"targetPageRank"`
}

// SemanticLinkAnalysis holds aggregate semantic link metrics.
type SemanticLinkAnalysis struct {
	WeakLinks        []SemanticLinkPair `json:"weakLinks"`
	StrongLinks      []SemanticLinkPair `json:"strongLinks"`
	TotalLinks       int                `json:"totalLinks"`
	AvgSemSimilarity float64            `json:"avgSemSimilarity"`
	WeakLinksCount   int                `json:"weakLinksCount"`
	StrongLinksCount int                `json:"strongLinksCount"`
}

// SemanticLinkPair represents a source-target pair with similarity score.
type SemanticLinkPair struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Similarity float64 `json:"similarity"`
}

// --- URL Structure ---

// URLStructureData holds URL decomposition for a single page.
type URLStructureData struct {
	QueryParams      map[string]string `json:"queryParams,omitempty"`
	Scheme           string            `json:"scheme"`
	Hostname         string            `json:"hostname"`
	Port             string            `json:"port,omitempty"`
	LastSegment      string            `json:"lastSegment,omitempty"`
	FileExtension    string            `json:"fileExtension,omitempty"`
	PathSegments     []string          `json:"pathSegments,omitempty"`
	PathDepth        int               `json:"pathDepth"`
	ParameterCount   int               `json:"parameterCount,omitempty"`
	HasFragment      bool              `json:"hasFragment,omitempty"`
	HasTrailingSlash bool              `json:"hasTrailingSlash,omitempty"`
}

// URLStructureStats holds aggregate URL structure analysis.
type URLStructureStats struct {
	DepthDistribution     map[int]int      `json:"depthDistribution"`
	TopDirectories        []NamedCount     `json:"topDirectories"`
	TopParameters         []NamedCount     `json:"topParameters"`
	ExtensionDistribution []ExtensionCount `json:"extensionDistribution"`
	TotalURLs             int              `json:"totalUrls"`
	AvgPathDepth          float64          `json:"avgPathDepth"`
	MaxPathDepth          int              `json:"maxPathDepth"`
	URLsWithParams        int              `json:"urlsWithParams"`
	URLsWithTrailingSlash int              `json:"urlsWithTrailingSlash"`
}

// NamedCount is a generic name+count pair for top-N lists.
type NamedCount struct {
	Name  string `json:"directory,omitempty"` // "directory" or "parameter"
	Count int    `json:"count"`
}

// ExtensionCount holds extension distribution data.
type ExtensionCount struct {
	Extension string `json:"extension"`
	Count     int    `json:"count"`
}

// --- Embeddings ---

// EmbeddingResult holds a URL's embedding vector.
type EmbeddingResult struct {
	URL    string    `json:"url"`
	Vector []float64 `json:"vector"`
}

// SimilarPair represents two URLs with their similarity score.
type SimilarPair struct {
	URL1       string  `json:"url1"`
	URL2       string  `json:"url2"`
	Similarity float64 `json:"similarity"`
}

// EmbeddingStats holds aggregate embedding analysis results.
type EmbeddingStats struct {
	Provider              string                 `json:"provider"`
	Model                 string                 `json:"model"`
	SimilarPairs          []SimilarPair          `json:"similarPairs"`
	CannibalizationGroups []CannibalizationGroup `json:"cannibalizationGroups"`
	PagesEmbedded         int                    `json:"pagesEmbedded"`
	Dimensions            int                    `json:"dimensions"`
}

// CannibalizationGroup holds a group of URLs with high similarity.
type CannibalizationGroup struct {
	URLs       []string `json:"urls"`
	Similarity float64  `json:"similarity"`
}

// --- N-grams ---

// NgramEntry holds a single n-gram with its metrics.
type NgramEntry struct {
	Term  string  `json:"term"`
	Count int     `json:"count"`
	Pages int     `json:"pages"`
	TFIDF float64 `json:"tfidf"`
}

// NgramStats holds aggregate n-gram analysis results.
type NgramStats struct {
	Unigrams    []NgramEntry `json:"unigrams"`
	Bigrams     []NgramEntry `json:"bigrams"`
	Trigrams    []NgramEntry `json:"trigrams"`
	TotalPages  int          `json:"totalPages"`
	TotalTokens int          `json:"totalTokens"`
}
