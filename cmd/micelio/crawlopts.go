package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/extract"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// startCrawl runs a crawl built by crawl, list or sitemap. analyse is false
// with --no-analysis. Tests replace it to inspect the config.
var startCrawl = func(config types.CrawlConfig, analyse bool) error {
	var crawlStore *storage.CrawlStore
	if config.DBPath != "" {
		var err error
		crawlStore, err = storage.NewCrawlStore(config.DBPath)
		if err != nil {
			return fmt.Errorf("open crawl database: %w", err)
		}
		defer crawlStore.Close()
	}
	if !analyse {
		return runCrawlWithProgress(config, nil, crawlStore)
	}
	ppCfg := analysis.PostProcessConfigFromCrawl(config)
	return runCrawlWithProgress(config, &ppCfg, crawlStore)
}

// crawlDefaults holds the flag defaults that differ between crawl, list and
// sitemap. spider adds --max-pages and --depth (crawl only).
type crawlDefaults struct {
	concurrency int
	delayMs     int
	output      string
	spider      bool
}

// crawlOptions holds every crawl/analysis flag shared by crawl, list and
// sitemap: the same options the dashboard Setup page sends.
type crawlOptions struct {
	configPath          string
	noAnalysis          bool
	noSoft404Probe      bool
	dbOnly              bool
	concurrency         int
	maxPages            int
	maxDepth            int
	delayMs             int
	retries             int
	userAgent           string
	outputPath          string
	outputFormat        string
	proxy               string
	maxErrors           int
	timeout             int
	headers             []string
	cookies             string
	includePattern      []string
	excludePattern      []string
	pathOnlyFilters     bool
	allowedDomains      []string
	noRobots            bool
	showBlockedInternal bool
	jsRendering         bool
	ngrams              bool
	language            string
	linkIntelligence    bool
	liNoCentrality      bool
	liMaxSuggestions    int
	embeddings          bool
	embeddingModel      string
	embeddingProvider   string
	embeddingKey        string
	similarityThreshold float64
	segments            []string
	psi                 bool
	psiKey              string
	aiPrompt            string
	aiProvider          string
	aiModel             string
	aiKey               string
	gsc                 bool
	gscProperty         string
	gscKeyFile          string
	gscDays             int
	gscBqDataset        string
	ga4                 bool
	ga4Property         string
	ga4KeyFile          string
	ga4Days             int
	crux                bool
	cruxKey             string
	cruxFormFactor      string
	plausible           bool
	plausibleSiteID     string
	plausibleAPIKey     string
	plausibleDays       int
	plausibleHost       string
	customExtractions   []string
	customSearches      []string
	snippetPaths        []string
	checkExternal       bool
	pageWeight          bool
	fullAnchors         bool
	fullPageWeight      bool
	htmlReport          bool
	htmlOpen            bool
	sitemapOut          bool
	saveHTML            bool
	saveRendered        bool
	dbPath              string
	noDb                bool
	resumeCrawl         bool
	adaptiveRate        bool
	delayFactor         float64
	stealth             bool
	discoverSitemaps    bool
	torControlPort      string
	torPassword         string
	torRotateEvery      int
	renderBlockRes      bool
	renderTimeout       int
}

// registerCrawlFlags adds the shared crawl flags to cmd.
func registerCrawlFlags(cmd *cobra.Command, o *crawlOptions, d crawlDefaults) {
	f := cmd.Flags()
	f.StringVar(&o.configPath, "config", "", "Crawl config JSON in the dashboard format (flags you set override it)")
	f.IntVarP(&o.concurrency, "concurrency", "c", d.concurrency, "Concurrent requests")
	if d.spider {
		f.IntVar(&o.maxPages, "max-pages", 1000, "Maximum pages")
		f.IntVarP(&o.maxDepth, "depth", "d", 10, "Maximum crawl depth")
	}
	f.IntVar(&o.delayMs, "delay", d.delayMs, "Delay between requests (ms)")
	f.IntVar(&o.retries, "retries", types.DefaultRetries, "Retries per failed URL (network error, timeout, 408, 429, 5xx); 0 disables")
	f.StringVarP(&o.userAgent, "user-agent", "u", "Micelio/1.0", "User-Agent")
	f.StringVarP(&o.outputPath, "output", "o", d.output, "Output file path")
	f.StringVarP(&o.outputFormat, "format", "f", "", "Output format (jsonl or csv)")
	f.StringVar(&o.proxy, "proxy", "", "HTTP/SOCKS5 proxy URL (use 'tor' for Tor)")
	f.IntVar(&o.maxErrors, "max-errors", 0, "Stop after N errors")
	f.IntVar(&o.timeout, "timeout", 0, "Stop after N seconds")
	f.StringArrayVarP(&o.headers, "header", "H", nil, "Custom header (Name: Value)")
	f.StringVar(&o.cookies, "cookies", "", "Cookie string")
	f.StringArrayVar(&o.includePattern, "include", nil, "Include URL pattern (regex)")
	f.StringArrayVar(&o.excludePattern, "exclude", nil, "Exclude URL pattern (regex)")
	f.BoolVar(&o.pathOnlyFilters, "path-only-filters", false, "Match --include/--exclude against the URL path only (no query string)")
	f.StringArrayVar(&o.allowedDomains, "allowed-domains", nil, "Additional allowed domains")
	f.BoolVar(&o.noRobots, "no-robots", false, "Ignore robots.txt restrictions")
	f.BoolVar(&o.showBlockedInternal, "show-blocked-internal", false, "Include robots-blocked URLs in output")
	f.BoolVar(&o.jsRendering, "js", false, "Enable JS rendering with headless Chrome")
	f.BoolVar(&o.renderBlockRes, "render-block-resources", true, "Block images/fonts/analytics during JS rendering")
	f.IntVar(&o.renderTimeout, "render-timeout", 0, "JS render timeout in seconds (default 30)")
	f.BoolVar(&o.ngrams, "ngrams", false, "Enable n-gram analysis")
	f.StringVar(&o.language, "language", "en", "Language for n-gram stopword filtering")
	f.BoolVar(&o.linkIntelligence, "link-intelligence", false, "Enable link graph analysis (click depth, centrality, suggestions)")
	f.BoolVar(&o.liNoCentrality, "li-no-centrality", false, "Skip centrality computation (faster for large sites)")
	f.IntVar(&o.liMaxSuggestions, "li-max-suggestions", 20, "Max internal link suggestions")
	f.BoolVar(&o.embeddings, "embeddings", false, "Enable embedding-based similarity analysis")
	f.StringVar(&o.embeddingModel, "embedding-model", "", "Embedding model name")
	f.StringVar(&o.embeddingProvider, "embedding-provider", "openai", "Embedding provider (openai or ollama)")
	f.StringVar(&o.embeddingKey, "embedding-key", "", "Embedding API key")
	f.Float64Var(&o.similarityThreshold, "similarity-threshold", 0.9, "Similarity threshold for embeddings")
	f.StringArrayVar(&o.segments, "segment", nil, "URL segment rule (name:pattern)")
	// PageSpeed Insights
	f.BoolVar(&o.psi, "psi", false, "Enable PageSpeed Insights per-page analysis")
	f.StringVar(&o.psiKey, "psi-key", "", "PageSpeed Insights API key")
	// AI analysis
	f.StringVar(&o.aiPrompt, "ai-prompt", "", "AI analysis prompt (enables AI per-page analysis)")
	f.StringVar(&o.aiProvider, "ai-provider", "openai", "AI provider (openai, anthropic, ollama)")
	f.StringVar(&o.aiModel, "ai-model", "", "AI model name")
	f.StringVar(&o.aiKey, "ai-key", "", "AI API key (or use env: OPENAI_API_KEY, ANTHROPIC_API_KEY)")
	// Google Search Console
	f.BoolVar(&o.gsc, "gsc", false, "Enable Google Search Console data enrichment")
	f.StringVar(&o.gscProperty, "gsc-property", "", "GSC property URL")
	f.StringVar(&o.gscKeyFile, "gsc-key-file", "", "Path to GSC service account JSON key file")
	f.IntVar(&o.gscDays, "gsc-days", 90, "GSC data lookback days")
	f.StringVar(&o.gscBqDataset, "gsc-bq", "", "Use BigQuery bulk export (project.dataset format)")
	// Google Analytics 4
	f.BoolVar(&o.ga4, "ga4", false, "Enable Google Analytics 4 data enrichment")
	f.StringVar(&o.ga4Property, "ga4-property", "", "GA4 property ID")
	f.StringVar(&o.ga4KeyFile, "ga4-key-file", "", "Path to GA4 service account JSON key file")
	f.IntVar(&o.ga4Days, "ga4-days", 90, "GA4 data lookback days")
	// Chrome UX Report
	f.BoolVar(&o.crux, "crux", false, "Enable Chrome UX Report data enrichment")
	f.StringVar(&o.cruxKey, "crux-key", "", "CrUX API key")
	f.StringVar(&o.cruxFormFactor, "crux-form-factor", "ALL", "CrUX form factor (PHONE, DESKTOP, ALL)")
	// Plausible Analytics
	f.BoolVar(&o.plausible, "plausible", false, "Enable Plausible Analytics data enrichment")
	f.StringVar(&o.plausibleSiteID, "plausible-site-id", "", "Plausible site ID")
	f.StringVar(&o.plausibleAPIKey, "plausible-api-key", "", "Plausible API key")
	f.IntVar(&o.plausibleDays, "plausible-days", 30, "Plausible data lookback days")
	f.StringVar(&o.plausibleHost, "plausible-host", "", "Custom Plausible host URL")
	// Custom data extraction
	f.StringArrayVar(&o.customExtractions, "extract", nil, "Custom CSS extraction (name:selector)")
	f.StringArrayVar(&o.customSearches, "search", nil, "Search for text or /regex/ in source")
	f.StringArrayVar(&o.snippetPaths, "snippet", nil, "JS snippet to run in headless Chrome (requires --js)")
	// Output features
	f.BoolVar(&o.checkExternal, "check-external", false, "Check external links for broken URLs")
	f.BoolVar(&o.pageWeight, "page-weight", false, "Analyze page weight by fetching resource sizes via HEAD requests")
	f.BoolVar(&o.fullAnchors, "full-anchors", false, "Include full anchor data in output (default: stripped to save space)")
	f.BoolVar(&o.fullPageWeight, "full-page-weight", false, "Include per-resource detail in page weight (default: aggregate only)")
	f.BoolVar(&o.htmlReport, "html", false, "Generate interactive HTML report")
	f.BoolVar(&o.htmlOpen, "html-open", false, "Auto-open HTML report in browser")
	f.BoolVar(&o.sitemapOut, "sitemap-out", false, "Generate XML sitemap from crawled indexable pages")
	f.BoolVar(&o.saveHTML, "save-html", false, "Store each page's raw HTML in the crawl database")
	f.BoolVar(&o.saveRendered, "save-rendered", false, "Store each page's rendered HTML in the crawl database (with --js)")
	// SQLite persistence
	f.StringVar(&o.dbPath, "db", "", "SQLite database path for crawl persistence (auto-derived if not set)")
	f.BoolVar(&o.noDb, "no-db", false, "Disable automatic crawl database creation")
	f.BoolVar(&o.noAnalysis, "no-analysis", false, "Only crawl and save; skip post-crawl analysis (run it later with micelio import)")
	f.BoolVar(&o.dbOnly, "db-only", false, "Write only the crawl database, no JSONL/CSV output")
	f.BoolVar(&o.noSoft404Probe, "no-soft404-probe", false, "Skip the one request for a page that cannot exist (soft-404 check)")
	f.BoolVar(&o.resumeCrawl, "resume", false, "Resume an interrupted crawl (requires --db)")
	// Performance
	f.BoolVar(&o.adaptiveRate, "adaptive-rate", false, "Dynamically adjust request rate based on server feedback")
	f.Float64Var(&o.delayFactor, "delay-factor", 0, "Heritrix-style delay: factor * avg response time (e.g., 5 = 5x response time gap). Requires --adaptive-rate")
	f.BoolVar(&o.stealth, "stealth", false, "Mimic Chrome TLS/HTTP fingerprint to evade bot detection")
	f.BoolVar(&o.discoverSitemaps, "discover-sitemaps", false, "Parse XML sitemaps to discover and crawl orphan pages")
	// Tor
	f.StringVar(&o.torControlPort, "tor-control-port", "", "Tor control port address (default 127.0.0.1:9051)")
	f.StringVar(&o.torPassword, "tor-password", "", "Tor control port password")
	f.IntVar(&o.torRotateEvery, "tor-rotate", 0, "Rotate Tor circuit every N requests (default 50)")
}

// buildConfig returns the crawl config described by the flags. Mode and
// target (seed, URLs, sitemaps) are set by the command.
func (o *crawlOptions) buildConfig(cmd *cobra.Command) (types.CrawlConfig, error) {
	var parsedExtractions []types.CustomExtractionRule
	for _, raw := range o.customExtractions {
		rule, err := extract.ParseExtractionRule(raw)
		if err != nil {
			return types.CrawlConfig{}, err
		}
		parsedExtractions = append(parsedExtractions, rule)
	}
	var parsedSearches []types.CustomSearchRule
	for _, raw := range o.customSearches {
		rule, err := extract.ParseSearchRule(raw)
		if err != nil {
			return types.CrawlConfig{}, err
		}
		parsedSearches = append(parsedSearches, rule)
	}

	config := types.CrawlConfig{
		MaxDepth:            o.maxDepth,
		MaxPages:            o.maxPages,
		Concurrency:         o.concurrency,
		DelayMs:             o.delayMs,
		DelayExplicit:       cmd.Flags().Changed("delay"),
		UserAgent:           o.userAgent,
		OutputPath:          o.outputPath,
		OutputFormat:        resolveFormat(o.outputFormat, o.outputPath),
		CustomHeaders:       parseHeaders(o.headers),
		Cookies:             o.cookies,
		IncludePatterns:     o.includePattern,
		ExcludePatterns:     o.excludePattern,
		PathOnlyFilters:     o.pathOnlyFilters,
		Proxy:               o.proxy,
		MaxErrors:           o.maxErrors,
		TimeoutSeconds:      o.timeout,
		AllowedDomains:      o.allowedDomains,
		RespectRobots:       !o.noRobots,
		ShowBlockedInternal: o.showBlockedInternal,
		JSRendering:         o.jsRendering,
		RenderBlockRes:      o.renderBlockRes,
		RenderTimeoutSec:    o.renderTimeout,
		Ngrams:              o.ngrams,
		Language:            o.language,
		LinkIntelligence:    o.linkIntelligence,
		LINoCentrality:      o.liNoCentrality,
		LIMaxSuggestions:    o.liMaxSuggestions,
		Embeddings:          o.embeddings,
		EmbeddingModel:      o.embeddingModel,
		EmbeddingProvider:   o.embeddingProvider,
		EmbeddingKey:        envFallback(o.embeddingKey, "OPENAI_API_KEY"),
		SimilarityThreshold: o.similarityThreshold,
		SegmentRules:        parseSegmentFlags(o.segments),
		PSI:                 o.psi,
		PSIKey:              envFallback(o.psiKey, "PSI_API_KEY"),
		AIPrompt:            o.aiPrompt,
		AIProvider:          types.AIProvider(o.aiProvider),
		AIModel:             o.aiModel,
		AIKey:               envFallback(o.aiKey, "OPENAI_API_KEY", "ANTHROPIC_API_KEY"),
		GSC:                 o.gsc,
		GSCProperty:         o.gscProperty,
		GSCKeyFile:          o.gscKeyFile,
		GSCDays:             o.gscDays,
		GSCBqDataset:        o.gscBqDataset,
		GA4:                 o.ga4,
		GA4Property:         o.ga4Property,
		GA4KeyFile:          o.ga4KeyFile,
		GA4Days:             o.ga4Days,
		CrUX:                o.crux,
		CrUXKey:             envFallback(o.cruxKey, "CRUX_API_KEY"),
		CrUXFormFactor:      types.CrUXFormFactor(o.cruxFormFactor),
		Plausible:           o.plausible,
		PlausibleSiteID:     o.plausibleSiteID,
		PlausibleAPIKey:     envFallback(o.plausibleAPIKey, "PLAUSIBLE_API_KEY"),
		PlausibleDays:       o.plausibleDays,
		PlausibleHost:       o.plausibleHost,
		CustomExtractions:   parsedExtractions,
		CustomSearches:      parsedSearches,
		SnippetPaths:        o.snippetPaths,
		CheckExternal:       o.checkExternal,
		PageWeight:          o.pageWeight,
		FullAnchors:         o.fullAnchors,
		FullPageWeight:      o.fullPageWeight,
		HTMLReport:          o.htmlReport,
		HTMLOpen:            o.htmlOpen,
		SitemapOut:          o.sitemapOut,
		SaveHTML:            o.saveHTML,
		SaveRendered:        o.saveRendered,
		DBPath:              o.dbPath,
		NoDB:                o.noDb,
		Resume:              o.resumeCrawl,
		NoSoft404Probe:      o.noSoft404Probe,
		AdaptiveRate:        o.adaptiveRate,
		DelayFactor:         o.delayFactor,
		Stealth:             o.stealth,
		DiscoverSitemaps:    o.discoverSitemaps,
		TorControlPort:      o.torControlPort,
		TorPassword:         o.torPassword,
		TorRotateEvery:      o.torRotateEvery,
	}
	// Retries stays nil (types.DefaultRetries) unless the flag was set.
	if cmd.Flags().Changed("retries") {
		r := o.retries
		config.Retries = &r
	}
	return config, nil
}

// resolveConfig builds the config from the flags and, with --config, lays
// the flags the user set over the file. fileLimit is the page limit the file
// sets, 0 when it sets none or without --config.
func (o *crawlOptions) resolveConfig(cmd *cobra.Command) (config types.CrawlConfig, fileLimit int, err error) {
	fromFlags, err := o.buildConfig(cmd)
	if err != nil {
		return types.CrawlConfig{}, 0, err
	}
	if o.configPath == "" {
		return fromFlags, 0, nil
	}
	base, fileLimit, err := loadConfigFile(o.configPath, fromFlags)
	if err != nil {
		return types.CrawlConfig{}, 0, err
	}
	config = overlayFlags(cmd, base, fromFlags)
	// NoDB is not part of the file format (json:"-").
	config.NoDB = config.NoDB || o.noDb
	return config, fileLimit, nil
}

// run applies the checks shared by crawl, list and sitemap and starts the
// crawl. config already holds the command's mode and target.
func (o *crawlOptions) run(cmd *cobra.Command, config types.CrawlConfig) error {
	if config.CrUXFormFactor != "" {
		switch config.CrUXFormFactor {
		case types.FormFactorPhone, types.FormFactorDesktop, types.FormFactorAll:
		default:
			return fmt.Errorf("invalid crux-form-factor %q: must be PHONE, DESKTOP, or ALL", config.CrUXFormFactor)
		}
	}

	// list and sitemap write output.jsonl by default; name their DB after
	// the site and mode instead of a shared output.db.
	if config.Mode != types.ModeSpider && !config.NoDB && config.DBPath == "" &&
		!cmd.Flags().Changed("output") && config.OutputPath == o.outputPath {
		config.DBPath = modeDBPath(config)
	}

	if o.dbOnly {
		if config.NoDB {
			return errors.New("--db-only is incompatible with --no-db")
		}
		config.DBPath = defaultDBPath(config) // named after -o, before it is cleared
		config.OutputPath = ""
	}

	// --resume needs a DB (explicit or auto-derived).
	if config.Resume && config.NoDB {
		return errors.New("--resume is incompatible with --no-db")
	}

	if config.GSC && config.GSCBqDataset != "" {
		fmt.Fprintf(os.Stderr, "  [warn] --gsc and --gsc-bq both set; using GSC API (--gsc takes precedence)\n")
	}

	// --no-analysis: crawl and store only; `micelio import` analyses the
	// database on another machine.
	if o.noAnalysis && (config.HTMLReport || config.SitemapOut) {
		fmt.Fprintf(os.Stderr, "  [warn] --no-analysis skips the HTML report and sitemap\n")
		config.HTMLReport, config.HTMLOpen, config.SitemapOut = false, false, false
	}
	return startCrawl(config, !o.noAnalysis)
}

// modeDBPath names a list or sitemap crawl DB after the seed host and mode
// (example.com-list.db), so it never mixes with the spider crawl's DB.
func modeDBPath(config types.CrawlConfig) string {
	host := "crawl"
	if u, err := url.Parse(config.SeedURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return host + "-" + string(config.Mode) + ".db"
}
