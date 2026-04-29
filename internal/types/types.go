// Package types defines all shared data structures for the Micelio SEO crawler.
// Ported from src/types.ts — maintains JSON compatibility with the TypeScript version.
package types

// CrawlMode represents the type of crawl to perform.
type CrawlMode string

const (
	ModeSpider  CrawlMode = "spider"
	ModeList    CrawlMode = "list"
	ModeSitemap CrawlMode = "sitemap"
)

// OutputFormat represents the output file format.
type OutputFormat string

const (
	FormatJSONL OutputFormat = "jsonl"
	FormatCSV   OutputFormat = "csv"
)

// AIProvider represents supported AI providers.
type AIProvider string

const (
	AIProviderOpenAI    AIProvider = "openai"
	AIProviderAnthropic AIProvider = "anthropic"
	AIProviderOllama    AIProvider = "ollama"
	AIProviderNone      AIProvider = ""
)

// CrUXFormFactor represents Chrome UX Report device types.
type CrUXFormFactor string

const (
	FormFactorPhone   CrUXFormFactor = "PHONE"
	FormFactorDesktop CrUXFormFactor = "DESKTOP"
	FormFactorAll     CrUXFormFactor = "ALL"
	FormFactorNone    CrUXFormFactor = ""
)

// LinkPosition represents where a link appears in the page structure.
type LinkPosition string

const (
	LinkPosContent    LinkPosition = "content"
	LinkPosNavigation LinkPosition = "navigation"
	LinkPosFooter     LinkPosition = "footer"
	LinkPosSidebar    LinkPosition = "sidebar"
	LinkPosHeader     LinkPosition = "header"
	LinkPosOther      LinkPosition = "other"
)

// SitemapType represents the type of sitemap.
type SitemapType string

const (
	SitemapStandard SitemapType = "standard"
	SitemapNews     SitemapType = "news"
	SitemapVideo    SitemapType = "video"
	SitemapImage    SitemapType = "image"
	SitemapMixed    SitemapType = "mixed"
)

// CrawlState represents the lifecycle state of a crawl.
type CrawlState string

const (
	StatePending   CrawlState = "pending"
	StateCrawling  CrawlState = "crawling"
	StateCompleted CrawlState = "completed"
	StateFailed    CrawlState = "failed"
)

// StructuredDataFormat represents the format of structured data.
type StructuredDataFormat string

const (
	FormatJSONLD    StructuredDataFormat = "json-ld"
	FormatMicrodata StructuredDataFormat = "microdata"
)

// IssueSeverity represents validation issue severity.
type IssueSeverity string

const (
	SeverityError   IssueSeverity = "error"
	SeverityWarning IssueSeverity = "warning"
)

// ResourceType represents page weight resource types.
type ResourceType string

const (
	ResourceHTML       ResourceType = "html"
	ResourceImage      ResourceType = "image"
	ResourceScript     ResourceType = "script"
	ResourceStylesheet ResourceType = "stylesheet"
	ResourceFont       ResourceType = "font"
	ResourceVideo      ResourceType = "video"
	ResourceAudio      ResourceType = "audio"
	ResourceOther      ResourceType = "other"
)

// DilutionWarning represents link dilution severity.
type DilutionWarning string

const (
	DilutionExcessive DilutionWarning = "excessive"
	DilutionHigh      DilutionWarning = "high"
	DilutionModerate  DilutionWarning = "moderate"
)
