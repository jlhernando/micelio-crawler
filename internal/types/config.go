package types

// --- Configuration ---

// CustomExtractionRule defines a CSS-based extraction rule.
type CustomExtractionRule struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "css"
	Selector string `json:"selector"`
}

// CustomSearchRule defines a content search pattern.
type CustomSearchRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
	IsRegex bool   `json:"isRegex"`
}

// SegmentRule defines a URL segmentation rule.
type SegmentRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// PathBudgetRule limits how many pages are crawled under a URL path prefix.
type PathBudgetRule struct {
	Prefix   string `json:"prefix"`
	MaxPages int    `json:"maxPages"`
}

// CrawlConfig holds all configuration for a crawl session.
type CrawlConfig struct {
	CustomHeaders   map[string]string `json:"customHeaders"`
	Cookies         string            `json:"cookies"`
	DBPath          string            `json:"dbPath"`
	Mode            CrawlMode         `json:"mode"`
	PlausibleAPIKey string            `json:"plausibleApiKey"`
	PlausibleSiteID string            `json:"plausibleSiteId"`
	CrUXFormFactor  CrUXFormFactor    `json:"cruxFormFactor"`
	CrUXKey         string            `json:"cruxKey"`
	SitemapPriority string            `json:"sitemapPriority"`
	UserAgent       string            `json:"userAgent"`
	// Device is the device the crawl pretends to be (User-Agent, client
	// hints and, with JS rendering, the screen Chrome emulates). UserAgent,
	// when set, overrides the device's User-Agent. Nil keeps the old
	// behaviour: Micelio/1.0 and a 1920x1080 desktop render.
	Device *DeviceProfile `json:"device,omitempty"`
	// CompareDevice, when set, fetches every page a second time as this
	// device (rendered too with JS rendering) and stores the differences
	// in PageData.DeviceCompare. Links it alone sees are crawled as well.
	CompareDevice     *DeviceProfile `json:"compareDevice,omitempty"`
	SitemapChangefreq string         `json:"sitemapChangefreq"`
	OutputPath        string         `json:"outputPath"`
	// PDFReport is where the CLI writes the PDF report (empty = none).
	PDFReport string `json:"pdfReport,omitempty"`
	// BacklogCSV is where the CLI writes the SEO backlog for JIRA (empty = none).
	BacklogCSV        string                 `json:"backlogCsv,omitempty"`
	OutputFormat      OutputFormat           `json:"outputFormat"`
	Language          string                 `json:"language"`
	GSCProperty       string                 `json:"gscProperty"`
	PlausibleHost     string                 `json:"plausibleHost"`
	EmbeddingModel    string                 `json:"embeddingModel"`
	EmbeddingKey      string                 `json:"embeddingKey"`
	Proxy             string                 `json:"proxy"`
	SeedURL           string                 `json:"seedUrl"`
	EmbeddingProvider string                 `json:"embeddingProvider"`
	GA4KeyFile        string                 `json:"ga4KeyFile"`
	PSIKey            string                 `json:"psiKey"`
	AIPrompt          string                 `json:"aiPrompt"`
	AIProvider        AIProvider             `json:"aiProvider"`
	AIModel           string                 `json:"aiModel"`
	AIKey             string                 `json:"aiKey"`
	GA4Property       string                 `json:"ga4Property"`
	GSCBqDataset      string                 `json:"gscBqDataset"`
	GSCKeyFile        string                 `json:"gscKeyFile"`
	CustomSearches    []CustomSearchRule     `json:"customSearches"`
	SegmentRules      []SegmentRule          `json:"segmentRules"`
	IncludePatterns   []string               `json:"includePatterns"`
	URLs              []string               `json:"urls"`
	AllowedDomains    []string               `json:"allowedDomains"`
	ExcludePatterns   []string               `json:"excludePatterns"`
	PathOnlyFilters   bool                   `json:"pathOnlyFilters"`
	CustomExtractions []CustomExtractionRule `json:"customExtractions"`
	PathBudgets       []PathBudgetRule       `json:"pathBudgets"`
	SnippetPaths      []string               `json:"snippetPaths"`
	SitemapURLs       []string               `json:"sitemapUrls"`
	TimeoutSeconds    int                    `json:"timeoutSeconds"`
	GA4Days           int                    `json:"ga4Days"`
	PlausibleDays     int                    `json:"plausibleDays"`
	MaxErrors         int                    `json:"maxErrors"`
	// Retries is how many times a failed URL (network error, timeout, 408,
	// 429, 5xx) is fetched again before it is stored as failed. Nil means
	// DefaultRetries; 0 disables retries.
	Retries             *int    `json:"retries,omitempty"`
	MaxDepth            int     `json:"maxDepth"`
	MaxPages            int     `json:"maxPages"`
	Concurrency         int     `json:"concurrency"`
	LIMaxSuggestions    int     `json:"liMaxSuggestions"`
	LIMaxPages          int     `json:"liMaxPages"`
	GSCDays             int     `json:"gscDays"`
	SimilarityThreshold float64 `json:"similarityThreshold"`
	DelayFactor         float64 `json:"delayFactor"`
	DelayMs             int     `json:"delayMs"`
	Resume              bool    `json:"resume"`
	GSC                 bool    `json:"gsc"`
	SitemapOut          bool    `json:"sitemapOut"`
	JSRendering         bool    `json:"jsRendering"`
	DelayExplicit       bool    `json:"delayExplicit"`
	AdaptiveRate        bool    `json:"adaptiveRate"`
	CrUX                bool    `json:"crux"`
	LinkIntelligence    bool    `json:"linkIntelligence"`
	CheckExternal       bool    `json:"checkExternal"`
	Plausible           bool    `json:"plausible"`
	Embeddings          bool    `json:"embeddings"`
	Ngrams              bool    `json:"ngrams"`
	LINoCentrality      bool    `json:"liNoCentrality"`
	PSI                 bool    `json:"psi"`
	HTMLReport          bool    `json:"htmlReport"`
	GA4                 bool    `json:"ga4"`
	HTMLOpen            bool    `json:"htmlOpen"`
	PageWeight          bool    `json:"pageWeight"`
	RespectRobots       bool    `json:"respectRobots"`
	ShowBlockedInternal bool    `json:"showBlockedInternal"`
	HeadOnly            bool    `json:"headOnly"`
	FullAnchors         bool    `json:"fullAnchors"`
	FullPageWeight      bool    `json:"fullPageWeight"`
	SaveHTML            bool    `json:"saveHtml"`
	NoSoft404Probe      bool    `json:"noSoft404Probe,omitempty"` // skip the "page that cannot exist" request
	NoAgentCheck        bool    `json:"noAgentCheck,omitempty"`   // skip the agent-readiness probe (llms.txt, /.well-known, AI bots)
	SaveRendered        bool    `json:"saveRendered"`
	RenderBlockRes      bool    `json:"renderBlockResources"`
	RenderTimeoutSec    int     `json:"renderTimeoutSec"`
	Stealth             bool    `json:"stealth"`
	DiscoverSitemaps    bool    `json:"discoverSitemaps"`
	TorControlPort      string  `json:"torControlPort"`
	TorPassword         string  `json:"torPassword"`
	TorRotateEvery      int     `json:"torRotateEvery"`
	NoDB                bool    `json:"-"`
	SkipSSRF            bool    `json:"-"`
	// PlausibleHostTrusted lets PlausibleHost be a private or loopback
	// address (self-hosted Plausible). Set only by the CLI, never from JSON.
	PlausibleHostTrusted bool `json:"-"`
}

// DefaultRetries is used when CrawlConfig.Retries is not set.
const DefaultRetries = 2

// maxRetriesCap bounds Retries: each retry also pauses the host.
const maxRetriesCap = 10

// RetriesPerURL returns the effective number of retries per URL.
func (c *CrawlConfig) RetriesPerURL() int {
	if c.Retries == nil {
		return DefaultRetries
	}
	return min(max(*c.Retries, 0), maxRetriesCap)
}
