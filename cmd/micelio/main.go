package main

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/extract"
	"github.com/micelio/micelio/internal/integration"
	"github.com/micelio/micelio/internal/logs"
	"github.com/micelio/micelio/internal/report"
	"github.com/micelio/micelio/internal/scheduler"
	"github.com/micelio/micelio/internal/server"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
	"github.com/micelio/micelio/internal/webhook"
)

var version = "dev"
var sourceDir = "" // set at build time via -ldflags "-X main.sourceDir=..."

func init() {
	// Use MADV_DONTNEED instead of the default MADV_FREE so that freed heap
	// pages are immediately returned to the OS.  Without this, RSS keeps growing
	// even though the live heap is small because MADV_FREE pages remain resident
	// until the OS reclaims them under memory pressure.
	//
	// Note: setting GODEBUG in init() takes effect for Go 1.21+ because the
	// runtime re-reads GODEBUG from the environment at startup via godebug package.
	// For best results, also set GODEBUG=madvdontneed=1 in the shell environment.
	if v := os.Getenv("GODEBUG"); v == "" {
		os.Setenv("GODEBUG", "madvdontneed=1")
	} else if !strings.Contains(v, "madvdontneed") {
		os.Setenv("GODEBUG", v+",madvdontneed=1")
	}
}

func main() {
	var memLimit string

	rootCmd := &cobra.Command{
		Use:     "micelio",
		Short:   "Micelio SEO Crawler (Go)",
		Version: version,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if memLimit != "" {
				if limit, err := parseMemLimit(memLimit); err == nil {
					debug.SetMemoryLimit(limit)
				} else {
					fmt.Fprintf(os.Stderr, "warning: invalid --memlimit %q: %v\n", memLimit, err)
				}
			}
		},
	}

	rootCmd.PersistentFlags().StringVar(&memLimit, "memlimit", "", "soft memory limit (e.g. 2GiB, 512MiB)")

	rootCmd.AddCommand(
		crawlCmd(),
		listCmd(),
		sitemapCmd(),
		headCmd(),
		uiCmd(),
		robotsTestCmd(),
		verifyBotCmd(),
		generateSitemapCmd(),
		diffCmd(),
		scheduleCmd(),
		schedulesCmd(),
		gscAuthCmd(),
		logsCmd(),
		buildCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── Spider Crawl ──

func crawlCmd() *cobra.Command {
	var (
		concurrency         int
		maxPages            int
		maxDepth            int
		delayMs             int
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
	)

	cmd := &cobra.Command{
		Use:   "crawl <url>",
		Short: "Spider crawl a website with SEO analysis",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			seedURL := ensureScheme(args[0])
			customHeaders := parseHeaders(headers)
			format := resolveFormat(outputFormat, outputPath)

			// Fix #9: Validate cruxFormFactor
			if cruxFormFactor != "" {
				switch types.CrUXFormFactor(cruxFormFactor) {
				case types.FormFactorPhone, types.FormFactorDesktop, types.FormFactorAll:
					// valid
				default:
					return fmt.Errorf("invalid crux-form-factor %q: must be PHONE, DESKTOP, or ALL", cruxFormFactor)
				}
			}

			// Parse custom extraction rules
			var parsedExtractions []types.CustomExtractionRule
			for _, raw := range customExtractions {
				rule, err := extract.ParseExtractionRule(raw)
				if err != nil {
					return err
				}
				parsedExtractions = append(parsedExtractions, rule)
			}
			var parsedSearches []types.CustomSearchRule
			for _, raw := range customSearches {
				rule, err := extract.ParseSearchRule(raw)
				if err != nil {
					return err
				}
				parsedSearches = append(parsedSearches, rule)
			}

			// Fix #8: Env var fallbacks for API keys
			psiKeyResolved := envFallback(psiKey, "PSI_API_KEY")
			aiKeyResolved := envFallback(aiKey, "OPENAI_API_KEY", "ANTHROPIC_API_KEY")
			embeddingKeyResolved := envFallback(embeddingKey, "OPENAI_API_KEY")
			cruxKeyResolved := envFallback(cruxKey, "CRUX_API_KEY")
			plausibleAPIKeyResolved := envFallback(plausibleAPIKey, "PLAUSIBLE_API_KEY")

			config := types.CrawlConfig{
				SeedURL:             seedURL,
				Mode:                types.ModeSpider,
				MaxDepth:            maxDepth,
				MaxPages:            maxPages,
				Concurrency:         concurrency,
				DelayMs:             delayMs,
				DelayExplicit:       cmd.Flags().Changed("delay"),
				UserAgent:           userAgent,
				OutputPath:          outputPath,
				OutputFormat:        format,
				CustomHeaders:       customHeaders,
				Cookies:             cookies,
				IncludePatterns:     includePattern,
				ExcludePatterns:     excludePattern,
				Proxy:               proxy,
				MaxErrors:           maxErrors,
				TimeoutSeconds:      timeout,
				AllowedDomains:      allowedDomains,
				RespectRobots:       !noRobots,
				ShowBlockedInternal: showBlockedInternal,
				JSRendering:         jsRendering,
				RenderBlockRes:      renderBlockRes,
				RenderTimeoutSec:    renderTimeout,
				Ngrams:              ngrams,
				Language:            language,
				LinkIntelligence:    linkIntelligence,
				LINoCentrality:      liNoCentrality,
				LIMaxSuggestions:    liMaxSuggestions,
				Embeddings:          embeddings,
				EmbeddingModel:      embeddingModel,
				EmbeddingProvider:   embeddingProvider,
				EmbeddingKey:        embeddingKeyResolved,
				SimilarityThreshold: similarityThreshold,
				SegmentRules:        parseSegmentFlags(segments),
				PSI:                 psi,
				PSIKey:              psiKeyResolved,
				AIPrompt:            aiPrompt,
				AIProvider:          types.AIProvider(aiProvider),
				AIModel:             aiModel,
				AIKey:               aiKeyResolved,
				GSC:                 gsc,
				GSCProperty:         gscProperty,
				GSCKeyFile:          gscKeyFile,
				GSCDays:             gscDays,
				GSCBqDataset:        gscBqDataset,
				GA4:                 ga4,
				GA4Property:         ga4Property,
				GA4KeyFile:          ga4KeyFile,
				GA4Days:             ga4Days,
				CrUX:                crux,
				CrUXKey:             cruxKeyResolved,
				CrUXFormFactor:      types.CrUXFormFactor(cruxFormFactor),
				Plausible:           plausible,
				PlausibleSiteID:     plausibleSiteID,
				PlausibleAPIKey:     plausibleAPIKeyResolved,
				PlausibleDays:       plausibleDays,
				PlausibleHost:       plausibleHost,
				CustomExtractions:   parsedExtractions,
				CustomSearches:      parsedSearches,
				SnippetPaths:        snippetPaths,
				CheckExternal:       checkExternal,
				PageWeight:          pageWeight,
				FullAnchors:         fullAnchors,
				FullPageWeight:      fullPageWeight,
				HTMLReport:          htmlReport,
				HTMLOpen:            htmlOpen,
				SitemapOut:          sitemapOut,
				DBPath:              dbPath,
				NoDB:                noDb,
				Resume:              resumeCrawl,
				AdaptiveRate:        adaptiveRate,
				DelayFactor:         delayFactor,
				Stealth:             stealth,
				DiscoverSitemaps:    discoverSitemaps,
				TorControlPort:      torControlPort,
				TorPassword:         torPassword,
				TorRotateEvery:      torRotateEvery,
			}

			// Validate --resume requires a DB (explicit or auto-derived)
			if config.Resume && config.NoDB {
				return fmt.Errorf("--resume is incompatible with --no-db")
			}

			// Warn if both --gsc and --gsc-bq are set
			if config.GSC && config.GSCBqDataset != "" {
				fmt.Fprintf(os.Stderr, "  [warn] --gsc and --gsc-bq both set; using GSC API (--gsc takes precedence)\n")
			}

			// Open SQLite crawl store if --db specified
			var crawlStore *storage.CrawlStore
			if config.DBPath != "" {
				var err error
				crawlStore, err = storage.NewCrawlStore(config.DBPath)
				if err != nil {
					return fmt.Errorf("open crawl database: %w", err)
				}
				defer crawlStore.Close()
			}

			// Fix #5: Derive PostProcessConfig from CrawlConfig (no field duplication)
			ppCfg := analysis.PostProcessConfigFromCrawl(config)
			return runCrawlWithProgress(config, &ppCfg, crawlStore)
		},
	}

	addCommonCrawlFlags(cmd, &concurrency, &maxPages, &maxDepth, &delayMs,
		&userAgent, &outputPath, &outputFormat, &proxy, &maxErrors, &timeout,
		&headers, &cookies, &includePattern, &excludePattern, &allowedDomains)
	cmd.Flags().BoolVar(&noRobots, "no-robots", false, "Ignore robots.txt restrictions")
	cmd.Flags().BoolVar(&showBlockedInternal, "show-blocked-internal", false, "Include robots-blocked URLs in output")
	cmd.Flags().BoolVar(&jsRendering, "js", false, "Enable JS rendering with headless Chrome")
	cmd.Flags().BoolVar(&renderBlockRes, "render-block-resources", true, "Block images/fonts/analytics during JS rendering")
	cmd.Flags().IntVar(&renderTimeout, "render-timeout", 0, "JS render timeout in seconds (default 30)")
	cmd.Flags().BoolVar(&ngrams, "ngrams", false, "Enable n-gram analysis")
	cmd.Flags().StringVar(&language, "language", "en", "Language for n-gram stopword filtering")
	cmd.Flags().BoolVar(&linkIntelligence, "link-intelligence", false, "Enable link graph analysis (click depth, centrality, suggestions)")
	cmd.Flags().BoolVar(&liNoCentrality, "li-no-centrality", false, "Skip centrality computation (faster for large sites)")
	cmd.Flags().IntVar(&liMaxSuggestions, "li-max-suggestions", 20, "Max internal link suggestions")
	cmd.Flags().BoolVar(&embeddings, "embeddings", false, "Enable embedding-based similarity analysis")
	cmd.Flags().StringVar(&embeddingModel, "embedding-model", "", "Embedding model name")
	cmd.Flags().StringVar(&embeddingProvider, "embedding-provider", "openai", "Embedding provider (openai or ollama)")
	cmd.Flags().StringVar(&embeddingKey, "embedding-key", "", "Embedding API key")
	cmd.Flags().Float64Var(&similarityThreshold, "similarity-threshold", 0.9, "Similarity threshold for embeddings")
	cmd.Flags().StringArrayVar(&segments, "segment", nil, "URL segment rule (name:pattern)")
	// PageSpeed Insights
	cmd.Flags().BoolVar(&psi, "psi", false, "Enable PageSpeed Insights per-page analysis")
	cmd.Flags().StringVar(&psiKey, "psi-key", "", "PageSpeed Insights API key")
	// AI analysis
	cmd.Flags().StringVar(&aiPrompt, "ai-prompt", "", "AI analysis prompt (enables AI per-page analysis)")
	cmd.Flags().StringVar(&aiProvider, "ai-provider", "openai", "AI provider (openai, anthropic, ollama)")
	cmd.Flags().StringVar(&aiModel, "ai-model", "", "AI model name")
	cmd.Flags().StringVar(&aiKey, "ai-key", "", "AI API key (or use env: OPENAI_API_KEY, ANTHROPIC_API_KEY)")
	// Google Search Console
	cmd.Flags().BoolVar(&gsc, "gsc", false, "Enable Google Search Console data enrichment")
	cmd.Flags().StringVar(&gscProperty, "gsc-property", "", "GSC property URL")
	cmd.Flags().StringVar(&gscKeyFile, "gsc-key-file", "", "Path to GSC service account JSON key file")
	cmd.Flags().IntVar(&gscDays, "gsc-days", 90, "GSC data lookback days")
	cmd.Flags().StringVar(&gscBqDataset, "gsc-bq", "", "Use BigQuery bulk export (project.dataset format)")
	// Google Analytics 4
	cmd.Flags().BoolVar(&ga4, "ga4", false, "Enable Google Analytics 4 data enrichment")
	cmd.Flags().StringVar(&ga4Property, "ga4-property", "", "GA4 property ID")
	cmd.Flags().StringVar(&ga4KeyFile, "ga4-key-file", "", "Path to GA4 service account JSON key file")
	cmd.Flags().IntVar(&ga4Days, "ga4-days", 90, "GA4 data lookback days")
	// Chrome UX Report
	cmd.Flags().BoolVar(&crux, "crux", false, "Enable Chrome UX Report data enrichment")
	cmd.Flags().StringVar(&cruxKey, "crux-key", "", "CrUX API key")
	cmd.Flags().StringVar(&cruxFormFactor, "crux-form-factor", "ALL", "CrUX form factor (PHONE, DESKTOP, ALL)")
	// Plausible Analytics
	cmd.Flags().BoolVar(&plausible, "plausible", false, "Enable Plausible Analytics data enrichment")
	cmd.Flags().StringVar(&plausibleSiteID, "plausible-site-id", "", "Plausible site ID")
	cmd.Flags().StringVar(&plausibleAPIKey, "plausible-api-key", "", "Plausible API key")
	cmd.Flags().IntVar(&plausibleDays, "plausible-days", 30, "Plausible data lookback days")
	cmd.Flags().StringVar(&plausibleHost, "plausible-host", "", "Custom Plausible host URL")
	// Custom data extraction
	cmd.Flags().StringArrayVar(&customExtractions, "extract", nil, "Custom CSS extraction (name:selector)")
	cmd.Flags().StringArrayVar(&customSearches, "search", nil, "Search for text or /regex/ in source")
	cmd.Flags().StringArrayVar(&snippetPaths, "snippet", nil, "JS snippet to run in headless Chrome (requires --js)")
	// Output features
	cmd.Flags().BoolVar(&checkExternal, "check-external", false, "Check external links for broken URLs")
	cmd.Flags().BoolVar(&pageWeight, "page-weight", false, "Analyze page weight by fetching resource sizes via HEAD requests")
	cmd.Flags().BoolVar(&fullAnchors, "full-anchors", false, "Include full anchor data in output (default: stripped to save space)")
	cmd.Flags().BoolVar(&fullPageWeight, "full-page-weight", false, "Include per-resource detail in page weight (default: aggregate only)")
	cmd.Flags().BoolVar(&htmlReport, "html", false, "Generate interactive HTML report")
	cmd.Flags().BoolVar(&htmlOpen, "html-open", false, "Auto-open HTML report in browser")
	cmd.Flags().BoolVar(&sitemapOut, "sitemap-out", false, "Generate XML sitemap from crawled indexable pages")
	// SQLite persistence
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path for crawl persistence (auto-derived if not set)")
	cmd.Flags().BoolVar(&noDb, "no-db", false, "Disable automatic crawl database creation")
	cmd.Flags().BoolVar(&resumeCrawl, "resume", false, "Resume an interrupted crawl (requires --db)")
	// Performance
	cmd.Flags().BoolVar(&adaptiveRate, "adaptive-rate", false, "Dynamically adjust request rate based on server feedback")
	cmd.Flags().Float64Var(&delayFactor, "delay-factor", 0, "Heritrix-style delay: factor * avg response time (e.g., 5 = 5x response time gap). Requires --adaptive-rate")
	cmd.Flags().BoolVar(&stealth, "stealth", false, "Mimic Chrome TLS/HTTP fingerprint to evade bot detection")
	cmd.Flags().BoolVar(&discoverSitemaps, "discover-sitemaps", false, "Parse XML sitemaps to discover and crawl orphan pages")
	// Tor
	cmd.Flags().StringVar(&torControlPort, "tor-control-port", "", "Tor control port address (default 127.0.0.1:9051)")
	cmd.Flags().StringVar(&torPassword, "tor-password", "", "Tor control port password")
	cmd.Flags().IntVar(&torRotateEvery, "tor-rotate", 0, "Rotate Tor circuit every N requests (default 50)")

	return cmd
}

// ── List Crawl ──

func listCmd() *cobra.Command {
	var (
		concurrency  int
		delayMs      int
		userAgent    string
		outputPath   string
		outputFormat string
		proxy        string
		maxErrors    int
		timeout      int
		headers      []string
		cookies      string
		stealth      bool
	)

	cmd := &cobra.Command{
		Use:   "list <file>",
		Short: "Crawl a list of URLs from a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			urls, err := readURLsFromFile(args[0])
			if err != nil {
				return err
			}
			format := resolveFormat(outputFormat, outputPath)

			config := types.CrawlConfig{
				SeedURL:        urls[0],
				Mode:           types.ModeList,
				URLs:           urls,
				MaxDepth:       0,
				MaxPages:       len(urls),
				Concurrency:    concurrency,
				DelayMs:        delayMs,
				UserAgent:      userAgent,
				OutputPath:     outputPath,
				OutputFormat:   format,
				CustomHeaders:  parseHeaders(headers),
				Cookies:        cookies,
				Proxy:          proxy,
				MaxErrors:      maxErrors,
				TimeoutSeconds: timeout,
				Stealth:        stealth,
			}

			return runCrawlWithProgress(config, nil, nil)
		},
	}

	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 3, "Concurrent requests")
	cmd.Flags().IntVar(&delayMs, "delay", 100, "Delay between requests (ms)")
	cmd.Flags().StringVarP(&userAgent, "user-agent", "u", "Micelio/1.0", "User-Agent")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "output.jsonl", "Output file path")
	cmd.Flags().StringVarP(&outputFormat, "format", "f", "", "Output format (jsonl or csv)")
	cmd.Flags().StringVar(&proxy, "proxy", "", "HTTP/SOCKS5 proxy URL (use 'tor' for Tor)")
	cmd.Flags().IntVar(&maxErrors, "max-errors", 0, "Stop after N errors")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "Stop after N seconds")
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil, "Custom header")
	cmd.Flags().StringVar(&cookies, "cookies", "", "Cookie string")
	cmd.Flags().BoolVar(&stealth, "stealth", false, "Mimic Chrome TLS/HTTP fingerprint to evade bot detection")

	return cmd
}

// ── Sitemap Crawl ──

func sitemapCmd() *cobra.Command {
	var (
		concurrency  int
		maxPages     int
		delayMs      int
		userAgent    string
		outputPath   string
		outputFormat string
		proxy        string
		headers      []string
		cookies      string
		stealth      bool
	)

	cmd := &cobra.Command{
		Use:   "sitemap <url...>",
		Short: "Crawl all URLs from XML sitemap(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			limit := maxPages
			if limit == 0 {
				limit = 100000
			}
			format := resolveFormat(outputFormat, outputPath)

			config := types.CrawlConfig{
				SeedURL:       args[0],
				Mode:          types.ModeSitemap,
				SitemapURLs:   args,
				MaxDepth:      0,
				MaxPages:      limit,
				Concurrency:   concurrency,
				DelayMs:       delayMs,
				UserAgent:     userAgent,
				OutputPath:    outputPath,
				OutputFormat:  format,
				CustomHeaders: parseHeaders(headers),
				Cookies:       cookies,
				Proxy:         proxy,
				Stealth:       stealth,
			}

			return runCrawlWithProgress(config, nil, nil)
		},
	}

	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 3, "Concurrent requests")
	cmd.Flags().IntVarP(&maxPages, "limit", "l", 0, "Max pages (0 = unlimited)")
	cmd.Flags().IntVar(&delayMs, "delay", 200, "Delay between requests (ms)")
	cmd.Flags().StringVarP(&userAgent, "user-agent", "u", "Micelio/1.0", "User-Agent")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "output.jsonl", "Output file path")
	cmd.Flags().StringVarP(&outputFormat, "format", "f", "", "Output format")
	cmd.Flags().StringVar(&proxy, "proxy", "", "HTTP/SOCKS5 proxy URL (use 'tor' for Tor)")
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil, "Custom header")
	cmd.Flags().StringVar(&cookies, "cookies", "", "Cookie string")
	cmd.Flags().BoolVar(&stealth, "stealth", false, "Mimic Chrome TLS/HTTP fingerprint to evade bot detection")

	return cmd
}

// ── HEAD Crawl ──

func headCmd() *cobra.Command {
	var (
		concurrency int
		delayMs     int
		userAgent   string
		outputPath  string
		csvOut      bool
		proxy       string
		headers     []string
		cookies     string
	)

	cmd := &cobra.Command{
		Use:   "head <file>",
		Short: "HEAD-only crawl for status/header checking",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			urls, err := readURLsFromFile(args[0])
			if err != nil {
				return err
			}

			format := types.FormatJSONL
			if csvOut {
				format = types.FormatCSV
			}

			config := types.CrawlConfig{
				SeedURL:       urls[0],
				Mode:          types.ModeList,
				URLs:          urls,
				MaxDepth:      0,
				MaxPages:      len(urls),
				Concurrency:   concurrency,
				DelayMs:       delayMs,
				UserAgent:     userAgent,
				OutputPath:    outputPath,
				OutputFormat:  format,
				CustomHeaders: parseHeaders(headers),
				Cookies:       cookies,
				Proxy:         proxy,
				HeadOnly:      true,
			}

			return runCrawlWithProgress(config, nil, nil)
		},
	}

	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 5, "Concurrent requests")
	cmd.Flags().IntVar(&delayMs, "delay", 0, "Delay between requests (ms)")
	cmd.Flags().StringVarP(&userAgent, "user-agent", "u", "Micelio/1.0", "User-Agent")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "head-output.jsonl", "Output file path")
	cmd.Flags().BoolVar(&csvOut, "csv", false, "Output as CSV")
	cmd.Flags().StringVar(&proxy, "proxy", "", "HTTP/SOCKS5 proxy URL (use 'tor' for Tor)")
	cmd.Flags().StringArrayVarP(&headers, "header", "H", nil, "Custom header")
	cmd.Flags().StringVar(&cookies, "cookies", "", "Cookie string")

	return cmd
}

// ── UI Server ──

func uiCmd() *cobra.Command {
	var (
		port      int
		host      string
		authToken string
	)

	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Launch the web dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Set a default soft memory limit for the long-running UI server
			// if no --memlimit was provided. This makes the GC more aggressive
			// and prevents unbounded RSS growth during large crawls.
			if current := debug.SetMemoryLimit(-1); current == math.MaxInt64 {
				const defaultUIMemLimit = 4 * 1024 * 1024 * 1024 // 4 GiB
				debug.SetMemoryLimit(defaultUIMemLimit)
				fmt.Fprintf(os.Stderr, "  [memory] Default GOMEMLIMIT set to 4 GiB (override with --memlimit)\n")
			}
			// Auth token from flag or env
			if authToken == "" {
				authToken = os.Getenv("MICELIO_AUTH_TOKEN")
			}
			fmt.Fprintf(os.Stderr, "\n  Micelio %s — starting web UI on %s:%d\n\n", version, host, port)
			return server.StartUIServerWithOptions(server.ServerOptions{
				Port:      port,
				Host:      host,
				AuthToken: authToken,
			}, dashboardFS())
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 3100, "Port number")
	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "Bind address (use 0.0.0.0 for all interfaces)")
	cmd.Flags().StringVar(&authToken, "auth-token", "", "Bearer token for API authentication (or MICELIO_AUTH_TOKEN env)")
	return cmd
}

// ── Robots Test ──

// knownUserAgents is the default set of bot user-agents to test.
var knownUserAgents = []string{
	"Googlebot", "Googlebot-Image", "Googlebot-News", "Googlebot-Video",
	"Google-Extended", "Google-Agent",
	"Bingbot", "Slurp", "DuckDuckBot", "Baiduspider", "YandexBot",
	"GPTBot", "ChatGPT-User", "CCBot", "Applebot",
	"AhrefsBot", "SemrushBot", "MJ12bot", "PetalBot",
	"facebookexternalhit", "Twitterbot",
}

func robotsTestCmd() *cobra.Command {
	var (
		agents   string
		testURLs []string
		jsonOut  bool
	)

	cmd := &cobra.Command{
		Use:   "robots-test <url>",
		Short: "Test robots.txt access for multiple user-agents",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			siteURL := ensureScheme(args[0])
			base, err := url.Parse(siteURL)
			if err != nil {
				return fmt.Errorf("invalid URL: %w", err)
			}

			// Determine user-agents
			var uas []string
			if agents != "" {
				for _, a := range strings.Split(agents, ",") {
					a = strings.TrimSpace(a)
					if a != "" {
						uas = append(uas, a)
					}
				}
			} else {
				uas = knownUserAgents
			}

			// Fetch robots.txt
			// NOTE: URL is user-provided. In a server context, validate against SSRF
			// (e.g., block private IPs). CLI usage is trusted-user, so no filtering here.
			robotsURL := fmt.Sprintf("%s://%s/robots.txt", base.Scheme, base.Host)
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get(robotsURL)
			var robotsTxt string
			var statusCode int
			robotsUnavailable := false // 5xx = treat as fully restricted per Google spec
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not fetch robots.txt: %v\n", err)
				robotsUnavailable = true
			} else {
				statusCode = resp.StatusCode
				body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
				resp.Body.Close()
				if readErr != nil {
					fmt.Fprintf(os.Stderr, "Warning: could not read robots.txt body: %v\n", readErr)
					robotsUnavailable = true
				} else if statusCode == 200 {
					robotsTxt = string(body)
				} else if statusCode >= 500 {
					robotsUnavailable = true
					fmt.Fprintf(os.Stderr, "Warning: robots.txt returned %d — treating as fully restricted\n", statusCode)
				}
				// 4xx → treat as no robots.txt (allow all)
			}

			// Parse directives
			type directive struct {
				Agent     string `json:"agent"`
				Directive string `json:"directive"`
				Path      string `json:"path"`
			}
			var directives []directive
			var sitemapURLs []string

			if robotsTxt != "" {
				currentAgent := "*"
				for _, line := range strings.Split(robotsTxt, "\n") {
					trimmed := strings.TrimSpace(line)
					if trimmed == "" || strings.HasPrefix(trimmed, "#") {
						continue
					}
					if m := crawler.MatchDirective(trimmed, "User-agent"); m != "" {
						currentAgent = m
						continue
					}
					if m := crawler.MatchDirective(trimmed, "Sitemap"); m != "" {
						sitemapURLs = append(sitemapURLs, m)
						continue
					}
					for _, d := range []string{"Allow", "Disallow", "Crawl-delay"} {
						if m := crawler.MatchDirective(trimmed, d); m != "" {
							directives = append(directives, directive{Agent: currentAgent, Directive: d, Path: m})
							break
						}
					}
				}
			}

			// Default test URLs if none provided
			if len(testURLs) == 0 {
				testURLs = []string{
					fmt.Sprintf("%s://%s/", base.Scheme, base.Host),
					fmt.Sprintf("%s://%s/robots.txt", base.Scheme, base.Host),
					fmt.Sprintf("%s://%s/sitemap.xml", base.Scheme, base.Host),
					fmt.Sprintf("%s://%s/admin", base.Scheme, base.Host),
					fmt.Sprintf("%s://%s/wp-admin/", base.Scheme, base.Host),
					fmt.Sprintf("%s://%s/api/", base.Scheme, base.Host),
					fmt.Sprintf("%s://%s/search", base.Scheme, base.Host),
					fmt.Sprintf("%s://%s/login", base.Scheme, base.Host),
				}
			}

			// Test each URL against each user-agent
			type testResult struct {
				Allowed *bool  `json:"allowed"`
				URL     string `json:"url"`
				Agent   string `json:"agent"`
			}
			var results []testResult

			for _, testURL := range testURLs {
				for _, ua := range uas {
					if robotsUnavailable {
						results = append(results, testResult{URL: testURL, Agent: ua, Allowed: nil})
					} else {
						allowed := crawler.IsAllowedByRobots(robotsTxt, testURL, ua)
						results = append(results, testResult{URL: testURL, Agent: ua, Allowed: &allowed})
					}
				}
			}

			if jsonOut {
				output := map[string]interface{}{
					"robotstxtUrl":    robotsURL,
					"robotstxtStatus": statusCode,
					"userAgents":      uas,
					"urls":            testURLs,
					"results":         results,
					"directives":      directives,
					"sitemapUrls":     sitemapURLs,
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(output)
			}

			// Pretty-print table
			fmt.Printf("\nRobots.txt Test: %s (HTTP %d)\n\n", robotsURL, statusCode)

			if len(sitemapURLs) > 0 {
				fmt.Printf("Sitemaps:\n")
				for _, s := range sitemapURLs {
					fmt.Printf("  %s\n", s)
				}
				fmt.Println()
			}

			// Build lookup map for O(1) result access
			resultMap := make(map[string]*testResult, len(results))
			for i := range results {
				key := results[i].URL + "\x00" + results[i].Agent
				resultMap[key] = &results[i]
			}

			// Print matrix
			fmt.Printf("%-40s", "URL / Agent")
			for _, ua := range uas {
				fmt.Printf(" %-12s", truncate(ua, 12))
			}
			fmt.Println()
			fmt.Println(strings.Repeat("-", 40+13*len(uas)))

			for _, testURL := range testURLs {
				fmt.Printf("%-40s", truncate(testURL, 40))
				for _, ua := range uas {
					key := testURL + "\x00" + ua
					r := resultMap[key]
					if r == nil || r.Allowed == nil {
						fmt.Printf(" %-12s", "UNKNOWN")
					} else if *r.Allowed {
						fmt.Printf(" %-12s", "ALLOW")
					} else {
						fmt.Printf(" %-12s", "BLOCK")
					}
				}
				fmt.Println()
			}
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().StringVar(&agents, "agents", "", "Comma-separated user-agents (default: 21 major bots)")
	cmd.Flags().StringArrayVar(&testURLs, "url", nil, "URLs to test (default: common paths)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

// ── Verify Bot ──

func verifyBotCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "verify-bot <ip> [ip...]",
		Short: "Verify if IP addresses belong to known Google crawlers",
		Long: `Check IP addresses against Google's official crawler IP ranges.

Supports Googlebot, Google Special Crawlers, and Google User-Triggered Fetchers
(including Google-Agent). IP range data is fetched from Google's official JSON
endpoints and cached locally for 24 hours.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var results []crawler.BotVerifyResult
			for _, ip := range args {
				result := crawler.VerifyGoogleBot(ip)
				results = append(results, result)
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}

			for _, r := range results {
				if r.Error != "" {
					fmt.Fprintf(os.Stderr, "  %s  %s — error: %s\n", "\u2717", r.IP, r.Error)
					continue
				}
				if r.IsVerified {
					fmt.Printf("  %s  %s — %s (matched %s)\n", "\u2713", r.IP, r.BotCategory, r.MatchedCIDR)
				} else {
					fmt.Printf("  %s  %s — not a known Google crawler\n", "\u2717", r.IP)
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

// ── Generate Sitemap ──

func generateSitemapCmd() *cobra.Command {
	var (
		outputPath  string
		changefreq  string
		defaultPrio float64
		maxURLs     int
	)

	cmd := &cobra.Command{
		Use:   "generate-sitemap <input.jsonl>",
		Short: "Generate XML sitemap from JSONL crawl output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate changefreq
			validFreqs := map[string]bool{
				"always": true, "hourly": true, "daily": true, "weekly": true,
				"monthly": true, "yearly": true, "never": true, "": true,
			}
			if !validFreqs[changefreq] {
				return fmt.Errorf("invalid changefreq %q: must be one of always, hourly, daily, weekly, monthly, yearly, never", changefreq)
			}
			// Validate priority
			if defaultPrio < 0 || defaultPrio > 1.0 {
				return fmt.Errorf("invalid priority %.1f: must be between 0.0 and 1.0", defaultPrio)
			}

			pages, err := readJSONLPages(args[0])
			if err != nil {
				return fmt.Errorf("read input: %w", err)
			}

			// Filter: only 200 OK, indexable pages
			var eligible []*types.PageData
			for _, p := range pages {
				if p.StatusCode == 200 && p.Indexability.Indexable {
					eligible = append(eligible, p)
				}
			}

			// Sort by URL for deterministic output
			sort.Slice(eligible, func(i, j int) bool {
				return eligible[i].URL < eligible[j].URL
			})

			if maxURLs > 0 && len(eligible) > maxURLs {
				eligible = eligible[:maxURLs]
			}

			// Build XML sitemap
			type sitemapURL struct {
				XMLName    xml.Name `xml:"url"`
				Loc        string   `xml:"loc"`
				Changefreq string   `xml:"changefreq,omitempty"`
				Priority   string   `xml:"priority,omitempty"`
			}
			type urlset struct {
				XMLName xml.Name     `xml:"urlset"`
				XMLNS   string       `xml:"xmlns,attr"`
				URLs    []sitemapURL `xml:"url"`
			}

			sitemap := urlset{
				XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9",
			}

			for _, p := range eligible {
				su := sitemapURL{Loc: p.URL}
				if changefreq != "" {
					su.Changefreq = changefreq
				}
				if defaultPrio > 0 {
					su.Priority = fmt.Sprintf("%.1f", defaultPrio)
				}
				sitemap.URLs = append(sitemap.URLs, su)
			}

			f, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("create output: %w", err)
			}
			defer f.Close()

			f.WriteString(xml.Header)
			enc := xml.NewEncoder(f)
			enc.Indent("", "  ")
			if err := enc.Encode(sitemap); err != nil {
				return fmt.Errorf("encode sitemap: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Generated sitemap with %d URLs → %s\n", len(sitemap.URLs), outputPath)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", "sitemap.xml", "Output sitemap file path")
	cmd.Flags().StringVar(&changefreq, "changefreq", "", "Default changefreq (always, hourly, daily, weekly, monthly, yearly, never)")
	cmd.Flags().Float64Var(&defaultPrio, "priority", 0, "Default priority (0.0-1.0)")
	cmd.Flags().IntVar(&maxURLs, "max-urls", 50000, "Max URLs per sitemap")
	return cmd
}

// ── Diff ──

func diffCmd() *cobra.Command {
	var (
		htmlOutput string
		jsonOutput string
		full       bool
	)

	cmd := &cobra.Command{
		Use:   "diff <old.jsonl> <new.jsonl>",
		Short: "Compare two crawl JSONL files",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldPages, err := readJSONLPages(args[0])
			if err != nil {
				return fmt.Errorf("read old file: %w", err)
			}
			newPages, err := readJSONLPages(args[1])
			if err != nil {
				return fmt.Errorf("read new file: %w", err)
			}

			if full {
				// Full comparison: generate stats from pages, then compute comparison
				cfg := analysis.PostProcessConfig{}
				oldStats := analysis.RunCoreAnalysis(cmd.Context(), oldPages, cfg)
				newStats := analysis.RunCoreAnalysis(cmd.Context(), newPages, cfg)
				comparison := report.ComputeComparison(oldPages, &oldStats, newPages, &newStats)

				// Print summary
				report.PrintComparisonSummary(os.Stdout, comparison)

				if jsonOutput != "" {
					data, err := json.MarshalIndent(comparison, "", "  ")
					if err != nil {
						return fmt.Errorf("marshal comparison: %w", err)
					}
					if err := os.WriteFile(jsonOutput, data, 0644); err != nil {
						return fmt.Errorf("write JSON output: %w", err)
					}
					fmt.Fprintf(os.Stderr, "\nJSON comparison report: %s\n", jsonOutput)
				}
			} else {
				diff := report.ComputeDiff(oldPages, newPages)
				report.PrintDiffSummary(os.Stdout, diff)

				if htmlOutput != "" {
					f, err := os.Create(htmlOutput)
					if err != nil {
						return fmt.Errorf("create HTML output: %w", err)
					}
					defer f.Close()
					if err := report.GenerateDiffHTML(f, diff); err != nil {
						return fmt.Errorf("generate HTML diff: %w", err)
					}
					fmt.Fprintf(os.Stderr, "\nHTML diff report: %s\n", htmlOutput)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&htmlOutput, "html", "", "Write HTML diff report to file")
	cmd.Flags().StringVar(&jsonOutput, "json", "", "Write full comparison report as JSON")
	cmd.Flags().BoolVar(&full, "full", false, "Compute full comparison report (generates stats from pages)")
	return cmd
}

// ── Schedule ──

func scheduleCmd() *cobra.Command {
	var (
		cronExpr          string
		maxRuns           int
		outputDir         string
		webhookURL        string
		concurrency       int
		maxPages          int
		maxDepth          int
		delayMs           int
		userAgent         string
		outputFormat      string
		ngrams            bool
		language          string
		linkIntelligence  bool
		htmlReport        bool
		checkExternal     bool
		pageWeight        bool
		sitemapOut        bool
		customExtractions []string
		customSearches    []string
	)

	cmd := &cobra.Command{
		Use:   "schedule <url>",
		Short: "Run scheduled crawls on a cron schedule",
		Long:  "Run crawls on a cron schedule with state persistence and optional webhook notifications.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			seedURL := ensureScheme(args[0])

			format := types.FormatJSONL
			if outputFormat == "csv" {
				format = types.FormatCSV
			}

			// Parse custom extraction rules for scheduled crawls
			var parsedExtractions []types.CustomExtractionRule
			for _, raw := range customExtractions {
				rule, err := extract.ParseExtractionRule(raw)
				if err != nil {
					return err
				}
				parsedExtractions = append(parsedExtractions, rule)
			}
			var parsedSearches []types.CustomSearchRule
			for _, raw := range customSearches {
				rule, err := extract.ParseSearchRule(raw)
				if err != nil {
					return err
				}
				parsedSearches = append(parsedSearches, rule)
			}

			cfg := scheduler.Config{
				URL:       seedURL,
				CronExpr:  cronExpr,
				MaxRuns:   maxRuns,
				OutputDir: outputDir,
				CrawlConfig: types.CrawlConfig{
					SeedURL:           seedURL,
					Mode:              types.ModeSpider,
					MaxDepth:          maxDepth,
					MaxPages:          maxPages,
					Concurrency:       concurrency,
					DelayMs:           delayMs,
					DelayExplicit:     cmd.Flags().Changed("delay"),
					UserAgent:         userAgent,
					OutputFormat:      format,
					RespectRobots:     true,
					Ngrams:            ngrams,
					Language:          language,
					LinkIntelligence:  linkIntelligence,
					HTMLReport:        htmlReport,
					CheckExternal:     checkExternal,
					PageWeight:        pageWeight,
					SitemapOut:        sitemapOut,
					CustomExtractions: parsedExtractions,
					CustomSearches:    parsedSearches,
				},
			}

			if webhookURL != "" {
				cfg.Webhook = &webhook.Options{URL: webhookURL}
			}

			s, err := scheduler.New(cfg)
			if err != nil {
				return err
			}

			// Graceful shutdown
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				s.Stop()
			}()

			return s.Run()
		},
	}

	cmd.Flags().StringVar(&cronExpr, "cron", "@daily", "Cron expression (e.g., '0 2 * * *' or @daily)")
	cmd.Flags().IntVar(&maxRuns, "max-runs", 0, "Maximum runs (0 = unlimited)")
	cmd.Flags().StringVar(&outputDir, "output-dir", "scheduled-crawls", "Output directory for crawl results")
	cmd.Flags().StringVar(&webhookURL, "webhook", "", "Webhook URL for notifications")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 5, "Concurrent requests")
	cmd.Flags().IntVar(&maxPages, "max-pages", 1000, "Maximum pages per crawl")
	cmd.Flags().IntVarP(&maxDepth, "depth", "d", 10, "Maximum crawl depth")
	cmd.Flags().IntVar(&delayMs, "delay", 200, "Delay between requests (ms)")
	cmd.Flags().StringVarP(&userAgent, "user-agent", "u", "Micelio/1.0", "User-Agent")
	cmd.Flags().StringVarP(&outputFormat, "format", "f", "jsonl", "Output format (jsonl or csv)")
	cmd.Flags().BoolVar(&ngrams, "ngrams", false, "Enable n-gram analysis")
	cmd.Flags().StringVar(&language, "language", "en", "Language for n-gram stopword filtering")
	cmd.Flags().BoolVar(&linkIntelligence, "link-intelligence", false, "Enable link graph analysis")
	cmd.Flags().BoolVar(&htmlReport, "html", false, "Generate HTML report")
	cmd.Flags().BoolVar(&checkExternal, "check-external", false, "Check external links")
	cmd.Flags().BoolVar(&pageWeight, "page-weight", false, "Analyze page weight")
	cmd.Flags().BoolVar(&sitemapOut, "sitemap-out", false, "Generate XML sitemap")
	cmd.Flags().StringArrayVar(&customExtractions, "extract", nil, "Custom CSS extraction (name:selector)")
	cmd.Flags().StringArrayVar(&customSearches, "search", nil, "Search for text or /regex/ in source")
	return cmd
}

// ── Schedules List ──

func schedulesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schedules",
		Short: "List all scheduled crawls and their status",
		RunE: func(cmd *cobra.Command, args []string) error {
			schedules, err := scheduler.ListSchedules()
			if err != nil {
				return err
			}
			if len(schedules) == 0 {
				fmt.Println("No schedules found. Create one with: micelio schedule <url> --cron \"...\"")
				return nil
			}

			sep := "  " + strings.Repeat("-", 80)
			fmt.Printf("\n  Scheduled Crawls (%d)\n\n", len(schedules))
			fmt.Println(sep)

			for _, s := range schedules {
				status := "\033[33mNEW\033[0m"
				switch s.LastStatus {
				case "success":
					status = "\033[32mOK\033[0m"
				case "failed":
					status = "\033[31mFAIL\033[0m"
				}

				nextRun := "N/A"
				if s.NextRun != "" {
					if t, err := time.Parse(time.RFC3339, s.NextRun); err == nil {
						nextRun = t.Local().Format("2006-01-02 15:04:05")
					}
				}

				lastRun := "never"
				if s.LastRun != "" {
					if t, err := time.Parse(time.RFC3339, s.LastRun); err == nil {
						lastRun = t.Local().Format("2006-01-02 15:04:05")
					}
				}

				duration := "-"
				if s.LastDurationMs > 0 {
					duration = fmt.Sprintf("%ds", s.LastDurationMs/1000)
				}

				fmt.Printf("  %s\n", s.URL)
				fmt.Printf("    Schedule:  %s (%s)\n", describeCron(s.Cron), s.Cron)
				fmt.Printf("    Last run:  %s [%s] %d pages, %s\n", lastRun, status, s.LastPages, duration)
				fmt.Printf("    Next run:  %s\n", nextRun)
				fmt.Printf("    Total:     %d runs\n", s.TotalRuns)
				fmt.Printf("    Output:    %s\n", s.OutputDir)
				fmt.Println(sep)
			}
			return nil
		},
	}
}

func describeCron(expr string) string {
	switch strings.ToLower(strings.TrimSpace(expr)) {
	case "@hourly":
		return "Every hour"
	case "@daily", "@midnight":
		return "Every day at midnight"
	case "@weekly":
		return "Every Sunday at midnight"
	case "@monthly":
		return "First day of every month at midnight"
	case "@yearly", "@annually":
		return "January 1st at midnight"
	default:
		return "Cron: " + expr
	}
}

// ── GSC Auth ──

func gscAuthCmd() *cobra.Command {
	var (
		clientID     string
		clientSecret string
		revoke       bool
	)

	cmd := &cobra.Command{
		Use:   "gsc-auth",
		Short: "Authenticate with Google Search Console (OAuth2 flow)",
		Long: `Run the interactive OAuth2 flow to authenticate with Google Search Console.
This stores a token locally for GSC data enrichment during crawls.

For CI/server use, prefer --gsc-key-file with a service account instead.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if revoke {
				return integration.DeleteStoredGscToken()
			}
			return integration.RunGscOAuthFlow(clientID, clientSecret)
		},
	}

	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth2 client ID")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "OAuth2 client secret")
	cmd.Flags().BoolVar(&revoke, "revoke", false, "Delete stored token")
	return cmd
}

// ── Log Analysis ──

func logsCmd() *cobra.Command {
	var (
		formatHint string
		workers    int
		outputFile string
		jsonOutput bool
	)

	cmd := &cobra.Command{
		Use:   "logs <file1> [file2...]",
		Short: "Analyze server access log files",
		Long: `Parse and analyze one or more server access log files.

Supports Apache Combined/CLF, Nginx, CloudFront (TSV/CSV), Cloudflare (JSON),
ALB/ELB, and W3C/IIS formats. Format is auto-detected unless --format is set.

Multiple files are merged into a single unified analysis.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Verify all files exist
			for _, f := range args {
				if _, err := os.Stat(f); err != nil {
					return fmt.Errorf("file not found: %s", f)
				}
			}

			startTime := time.Now()
			fmt.Fprintf(os.Stderr, "Analyzing %d file(s)...\n", len(args))

			var lastProgress time.Time
			result, err := logs.AnalyzeMulti(args, logs.Format(formatHint), workers,
				func(mp logs.MultiProgress) {
					if time.Since(lastProgress) < 2*time.Second {
						return
					}
					lastProgress = time.Now()
					pct := float64(0)
					if mp.TotalSize > 0 {
						pct = float64(mp.TotalBytes) / float64(mp.TotalSize) * 100
					}
					fmt.Fprintf(os.Stderr, "\r  %s lines | %.1f%% | %d/%d files done",
						formatCount(mp.TotalLines), pct, mp.FilesDone, mp.FilesTotal)
				}, nil)
			if err != nil {
				return fmt.Errorf("analysis failed: %w", err)
			}

			elapsed := time.Since(startTime)
			fmt.Fprintf(os.Stderr, "\r  %s lines | 100%% | %d/%d files done\n",
				formatCount(result.Stats.TotalLines), len(result.Files), len(result.Files))
			fmt.Fprintf(os.Stderr, "\nCompleted in %s (format: %s)\n", formatElapsed(elapsed), result.Format)

			// Per-file summary
			for _, f := range result.Files {
				status := "OK"
				if f.Error != "" {
					status = "FAILED: " + f.Error
				}
				fmt.Fprintf(os.Stderr, "  %s: %s lines [%s]\n", f.Filename, formatCount(f.Lines), status)
			}

			// Print stats
			stats := result.Stats
			fmt.Fprintf(os.Stderr, "\n── Summary ──\n")
			fmt.Fprintf(os.Stderr, "  Total hits:    %s\n", formatCount(stats.TotalHits))
			fmt.Fprintf(os.Stderr, "  Human hits:    %s\n", formatCount(stats.HumanHits))
			fmt.Fprintf(os.Stderr, "  Bot hits:      %s\n", formatCount(stats.TotalHits-stats.HumanHits))
			fmt.Fprintf(os.Stderr, "  Total bytes:   %s\n", formatBytesHuman(stats.TotalBytes))
			fmt.Fprintf(os.Stderr, "  Date range:    %s to %s\n", stats.DateRange[0][:10], stats.DateRange[1][:10])
			fmt.Fprintf(os.Stderr, "  Unique bots:   %d\n", len(stats.BotHits))

			// Top bots
			type botEntry struct {
				name string
				hits int64
			}
			var bots []botEntry
			for name, bs := range stats.BotHits {
				bots = append(bots, botEntry{name, bs.Hits})
			}
			sort.Slice(bots, func(i, j int) bool { return bots[i].hits > bots[j].hits })
			fmt.Fprintf(os.Stderr, "\n── Top Bots ──\n")
			for i, b := range bots {
				if i >= 10 {
					break
				}
				pct := float64(b.hits) / float64(stats.TotalHits) * 100
				fmt.Fprintf(os.Stderr, "  %-25s %10s  (%.1f%%)\n", b.name, formatCount(b.hits), pct)
			}

			// JSON output
			if jsonOutput || outputFile != "" {
				data, _ := json.MarshalIndent(stats, "", "  ")
				if outputFile != "" {
					if err := os.WriteFile(outputFile, data, 0644); err != nil {
						return fmt.Errorf("write output: %w", err)
					}
					fmt.Fprintf(os.Stderr, "\nStats written to %s\n", outputFile)
				} else {
					fmt.Println(string(data))
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&formatHint, "format", "f", "", "Log format hint (apache_combined, apache_clf, nginx_combined, cloudfront, cloudflare, alb, w3c)")
	cmd.Flags().IntVarP(&workers, "workers", "w", 0, "Number of parallel workers (default: GOMAXPROCS)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Write JSON stats to file")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Print JSON stats to stdout")

	return cmd
}

func formatCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func formatBytesHuman(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m < 60 {
		if s > 0 {
			return fmt.Sprintf("%dm %ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	}
	h := m / 60
	rm := m % 60
	return fmt.Sprintf("%dh %dm", h, rm)
}

// ── Build ──

func buildCmd() *cobra.Command {
	var skipDashboard bool

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Rebuild and install micelio from source",
		Long: `Rebuild the micelio binary from its source directory and install it to $GOPATH/bin.
The source directory is embedded at compile time. Use --skip-dashboard to only rebuild the Go binary (faster).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sourceDir == "" {
				return fmt.Errorf("source directory not embedded in this binary — rebuild with 'make install' from the source directory first")
			}

			// Verify source directory exists
			if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
				return fmt.Errorf("source directory not found: %s", sourceDir)
			}

			target := "build-with-dashboard"
			if skipDashboard {
				target = "build"
			}

			fmt.Fprintf(os.Stderr, "  Building from %s...\n", sourceDir)

			// Run make <target> && make install
			makeCmd := exec.Command("make", target)
			makeCmd.Dir = sourceDir
			makeCmd.Stdout = os.Stdout
			makeCmd.Stderr = os.Stderr
			if err := makeCmd.Run(); err != nil {
				return fmt.Errorf("build failed: %w", err)
			}

			installCmd := exec.Command("make", "install")
			installCmd.Dir = sourceDir
			installCmd.Stdout = os.Stdout
			installCmd.Stderr = os.Stderr
			if err := installCmd.Run(); err != nil {
				return fmt.Errorf("install failed: %w", err)
			}

			fmt.Fprintf(os.Stderr, "\n  Done! micelio rebuilt and installed.\n")
			return nil
		},
	}

	cmd.Flags().BoolVar(&skipDashboard, "skip-dashboard", false, "Skip dashboard rebuild (Go binary only, faster)")
	return cmd
}

// ── Shared Helpers ──

func addCommonCrawlFlags(cmd *cobra.Command, concurrency, maxPages, maxDepth, delayMs *int,
	userAgent, outputPath, outputFormat, proxy *string, maxErrors, timeout *int,
	headers *[]string, cookies *string, includePattern, excludePattern, allowedDomains *[]string) {

	cmd.Flags().IntVarP(concurrency, "concurrency", "c", 5, "Concurrent requests")
	cmd.Flags().IntVar(maxPages, "max-pages", 1000, "Maximum pages")
	cmd.Flags().IntVarP(maxDepth, "depth", "d", 10, "Maximum crawl depth")
	cmd.Flags().IntVar(delayMs, "delay", 200, "Delay between requests (ms)")
	cmd.Flags().StringVarP(userAgent, "user-agent", "u", "Micelio/1.0", "User-Agent")
	cmd.Flags().StringVarP(outputPath, "output", "o", "", "Output file path")
	cmd.Flags().StringVarP(outputFormat, "format", "f", "", "Output format (jsonl or csv)")
	cmd.Flags().StringVar(proxy, "proxy", "", "HTTP/SOCKS5 proxy URL (use 'tor' for Tor)")
	cmd.Flags().IntVar(maxErrors, "max-errors", 0, "Stop after N errors")
	cmd.Flags().IntVar(timeout, "timeout", 0, "Stop after N seconds")
	cmd.Flags().StringArrayVarP(headers, "header", "H", nil, "Custom header (Name: Value)")
	cmd.Flags().StringVar(cookies, "cookies", "", "Cookie string")
	cmd.Flags().StringArrayVar(includePattern, "include", nil, "Include URL pattern (regex)")
	cmd.Flags().StringArrayVar(excludePattern, "exclude", nil, "Exclude URL pattern (regex)")
	cmd.Flags().StringArrayVar(allowedDomains, "allowed-domains", nil, "Additional allowed domains")
}

func runCrawlWithProgress(config types.CrawlConfig, ppCfg *analysis.PostProcessConfig, crawlStore *storage.CrawlStore) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "\nInterrupted — finishing in-flight requests...\n")
		cancel()
	}()

	// Auto-create crawl store if not provided and not disabled
	if crawlStore == nil && !config.NoDB {
		config.DBPath = defaultDBPath(config)
		var err error
		crawlStore, err = storage.NewCrawlStore(config.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] could not create crawl DB %q: %v — crawling without DB\n", config.DBPath, err)
		} else {
			defer crawlStore.Close()
		}
	}

	fmt.Fprintf(os.Stderr, "\n  Micelio %s — crawling %s\n", version, config.SeedURL)
	if crawlStore != nil {
		fmt.Fprintf(os.Stderr, "  Crawl DB: %s\n", config.DBPath)
	}
	fmt.Fprintf(os.Stderr, "  Concurrency: %d | Max pages: %d | Depth: %d\n\n",
		config.Concurrency, config.MaxPages, config.MaxDepth)

	// Build crawl store argument
	var storeArg []crawler.CrawlStore
	if crawlStore != nil {
		storeArg = append(storeArg, crawlStore)
	}

	result, err := crawler.Crawl(ctx, config, func(p crawler.CrawlProgress) {
		fmt.Fprintf(os.Stderr, "\r  [%d/%d] %.1f p/s — %s",
			p.Crawled, p.TotalSeen, p.Rate, truncate(p.CurrentURL, 60))
	}, storeArg...)
	if err != nil {
		return fmt.Errorf("crawl failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\n\n  Done! %d pages crawled in %s (%d errors)\n",
		len(result.Pages), result.Duration.Round(time.Millisecond), result.Errors)

	// Post-crawl: External link check
	var brokenExternal []types.ExternalLinkResult
	if config.CheckExternal && len(result.ExternalLinks) > 0 {
		fmt.Fprintf(os.Stderr, "\n  Checking %d external links...\n", len(result.ExternalLinks))
		brokenExternal = checkExternalLinks(result.ExternalLinks, config.Concurrency, config.UserAgent)
		if len(brokenExternal) > 0 {
			fmt.Fprintf(os.Stderr, "  External links: %d broken\n", len(brokenExternal))
		}
		result.ExternalLinks = nil
		debug.FreeOSMemory()
	}

	// Post-crawl: Page weight analysis
	if config.PageWeight && len(result.ResourceRefs) > 0 {
		fmt.Fprintf(os.Stderr, "  Analyzing page weight...\n")
		analysis.ComputePageWeight(result.Pages, result.ResourceRefs, result.TransferSizes, config.Concurrency, config.UserAgent)
		result.ResourceRefs = nil
		result.TransferSizes = nil
		debug.FreeOSMemory()
	}

	// Post-crawl analysis (n-grams, link intelligence, embeddings, etc.)
	var stats types.CrawlStats
	if ppCfg != nil {
		// Fix #2: Use fresh context for post-crawl (crawl ctx may be cancelled)
		postCtx, postCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer postCancel()

		ppCfg.CrawlDurationMs = result.Duration.Milliseconds()
		ppCfg.NgramAnalyzer = result.NgramAnalyzer
		if len(result.DiscoveredSitemapURLs) > 0 {
			ppCfg.SitemapEntries = result.DiscoveredSitemapURLs
		}
		if result.InternalLinksIter != nil {
			ppCfg.InternalLinksIter = result.InternalLinksIter
		}
		if result.InternalLinksClose != nil {
			defer result.InternalLinksClose()
		}
		stats, _ = analysis.RunPostCrawlAnalysis(postCtx, result.Pages, *ppCfg)
	}

	// Attach broken external links to stats
	if len(brokenExternal) > 0 {
		stats.BrokenExternalLinks = brokenExternal
	}

	// Rewrite output with enriched page data BEFORE stripping (rewrite needs full fields)
	if config.OutputPath != "" {
		enriched := false
		for _, p := range result.Pages {
			if p.GscData != nil || p.Ga4Data != nil || p.CruxData != nil || p.PlausibleData != nil ||
				p.LinkIntelligence != nil || p.PageWeight != nil {
				enriched = true
				break
			}
		}
		if enriched {
			fmt.Fprintf(os.Stderr, "  Rewriting output with enriched data...\n")
			// Atomic rewrite: write to temp file, rename on success (H1 fix)
			// Preserve compression extension so NewResultWriter detects it
			tmpPath := config.OutputPath + ".tmp"
			ext := filepath.Ext(config.OutputPath)
			if ext == ".gz" || ext == ".zst" {
				tmpPath = strings.TrimSuffix(config.OutputPath, ext) + ".tmp" + ext
			}
			rw, rwErr := storage.NewResultWriter(tmpPath, string(config.OutputFormat))
			if rwErr != nil {
				fmt.Fprintf(os.Stderr, "  [warn] Could not open output for rewrite: %v\n", rwErr)
			} else {
				if rewriteErr := rw.RewriteAll(result.Pages); rewriteErr != nil {
					fmt.Fprintf(os.Stderr, "  [warn] Rewrite failed: %v\n", rewriteErr)
					rw.Close()
					os.Remove(tmpPath)
				} else {
					rw.Close()
					if renameErr := os.Rename(tmpPath, config.OutputPath); renameErr != nil {
						fmt.Fprintf(os.Stderr, "  [warn] Rewrite rename failed: %v\n", renameErr)
					}
				}
			}
		}
	}

	// Print SEO funnel summary if available
	if stats.SeoFunnelStats != nil {
		f := stats.SeoFunnelStats
		fmt.Fprintf(os.Stderr, "\n  SEO Funnel:\n")
		fmt.Fprintf(os.Stderr, "    Crawled:       %d URLs\n", f.Crawled)
		fmt.Fprintf(os.Stderr, "    Renderable:    %d (%.1f%%)\n", f.Renderable, f.PctRenderable)
		fmt.Fprintf(os.Stderr, "    Indexable:     %d (%.1f%%)\n", f.Indexable, f.PctIndexable)
		visibleTotal := f.Visible + f.Active
		fmt.Fprintf(os.Stderr, "    Visible:       %d (%.1f%%) — have GSC impressions\n", visibleTotal, f.PctVisible)
		fmt.Fprintf(os.Stderr, "    Active:        %d (%.1f%%) — receive organic clicks\n", f.Active, f.PctActive)
		fmt.Fprintf(os.Stderr, "    Non-Indexable: %d\n", f.NonIndexable)
	}

	// Write stats summary (L2: only if we have meaningful analysis data)
	hasSummaryData := ppCfg != nil || len(brokenExternal) > 0
	if config.OutputPath != "" && hasSummaryData {
		summaryFile := summaryFilePath(config.OutputPath)
		if writeErr := writeStatsSummary(summaryFile, stats); writeErr != nil {
			fmt.Fprintf(os.Stderr, "  [warn] Failed to write summary: %v\n", writeErr)
		} else {
			fmt.Fprintf(os.Stderr, "  Summary: %s\n", summaryFile)
		}
	}

	// Generate HTML report
	if config.HTMLReport && config.OutputPath != "" {
		seedURL := config.SeedURL
		if seedURL == "" && len(config.URLs) > 0 {
			seedURL = config.URLs[0]
		}
		html, genErr := report.GenerateHTMLString(seedURL, &stats, result.Pages)
		if genErr != nil {
			fmt.Fprintf(os.Stderr, "  [warn] HTML report generation failed: %v\n", genErr)
		} else {
			htmlPath := htmlReportPath(config.OutputPath)
			if writeErr := os.WriteFile(htmlPath, []byte(html), 0644); writeErr != nil {
				fmt.Fprintf(os.Stderr, "  [warn] HTML report write failed: %v\n", writeErr)
			} else {
				fmt.Fprintf(os.Stderr, "  HTML report: %s\n", htmlPath)
				if config.HTMLOpen {
					openInBrowser(htmlPath)
				}
			}
		}
	}

	// Generate XML sitemap from indexable pages
	if config.SitemapOut && config.OutputPath != "" {
		sitemapPath := sitemapOutputPath(config.OutputPath)
		if sErr := generateSitemapFromPages(result.Pages, sitemapPath); sErr != nil {
			fmt.Fprintf(os.Stderr, "  [warn] Sitemap generation failed: %v\n", sErr)
		} else {
			fmt.Fprintf(os.Stderr, "  Sitemap: %s\n", sitemapPath)
		}
	}

	// Aggressively strip heavy fields — all consumers (output rewrite, HTML report, sitemap) are done.
	for _, p := range result.Pages {
		p.BodyText = ""
		p.Anchors = nil
		p.Images = nil
		p.StructuredData = nil
		p.OpenGraph = nil
		p.TwitterCard = nil
		p.Hreflang = nil
		p.CustomExtractions = nil
		p.CustomSearches = nil
		p.SnippetResults = nil
		p.URLIssues = nil
		p.RenderDiffs = nil
		p.SchemaValidation = nil
		p.Readability = nil
		p.PageWeight = nil
		p.Pagespeed = nil
		p.CruxData = nil
		p.GscData = nil
		p.Ga4Data = nil
		p.PlausibleData = nil
		p.AIAnalysis = nil
		p.ContentHash = ""
		p.SimhashFingerprint = ""
	}
	debug.FreeOSMemory()

	if config.OutputPath != "" {
		fmt.Fprintf(os.Stderr, "  Output: %s\n\n", config.OutputPath)
	}
	return nil
}

// summaryFilePath derives a summary JSON path from the output path.
func summaryFilePath(outputPath string) string {
	ext := filepath.Ext(outputPath)
	return strings.TrimSuffix(outputPath, ext) + "-summary.json"
}

// writeStatsSummary writes CrawlStats to a JSON file.
func writeStatsSummary(path string, stats types.CrawlStats) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(stats)
}

// checkExternalLinks performs concurrent HEAD checks on external URLs.
func checkExternalLinks(extLinks map[string][]string, concurrency int, userAgent string) []types.ExternalLinkResult {
	var (
		results []types.ExternalLinkResult
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, concurrency)
		checked atomic.Int32
		total   = len(extLinks)
	)

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	for extURL, foundOn := range extLinks {
		wg.Add(1)
		sem <- struct{}{}
		go func(u string, pages []string) {
			defer func() { <-sem; wg.Done() }()

			req, err := http.NewRequest("HEAD", u, nil)
			if err != nil {
				mu.Lock()
				results = append(results, types.ExternalLinkResult{URL: u, StatusCode: 0, Error: err.Error(), FoundOn: pages})
				mu.Unlock()
				return
			}
			req.Header.Set("User-Agent", userAgent)

			resp, err := client.Do(req)
			statusCode := 0
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			} else {
				statusCode = resp.StatusCode
				resp.Body.Close()
			}

			if statusCode >= 400 || statusCode == 0 {
				mu.Lock()
				results = append(results, types.ExternalLinkResult{URL: u, StatusCode: statusCode, Error: errMsg, FoundOn: pages})
				mu.Unlock()
			}

			n := int(checked.Add(1))
			if n%10 == 0 {
				fmt.Fprintf(os.Stderr, "\r  External: %d/%d", n, total)
			}
		}(extURL, foundOn)
	}
	wg.Wait()
	fmt.Fprintf(os.Stderr, "\r  External: %d/%d done\n", total, total)
	return results
}

// htmlReportPath derives an HTML report path from the output path.
func htmlReportPath(outputPath string) string {
	ext := filepath.Ext(outputPath)
	return strings.TrimSuffix(outputPath, ext) + ".html"
}

// sitemapOutputPath derives a sitemap XML path from the output path.
func sitemapOutputPath(outputPath string) string {
	ext := filepath.Ext(outputPath)
	return strings.TrimSuffix(outputPath, ext) + "-sitemap.xml"
}

// openInBrowser opens a file in the system's default browser.
func openInBrowser(path string) {
	var cmd string
	var args []string
	absPath, _ := filepath.Abs(path)
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{absPath}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", absPath}
	default:
		cmd = "xdg-open"
		args = []string{absPath}
	}
	if err := exec.Command(cmd, args...).Start(); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] Could not auto-open report: %v\n", err)
	}
}

// generateSitemapFromPages generates an XML sitemap from crawled indexable pages.
func generateSitemapFromPages(pages []*types.PageData, outputPath string) error {
	result := analysis.GenerateSitemap(pages, analysis.SitemapOptions{})
	if result.URLCount == 0 {
		return fmt.Errorf("no indexable pages found for sitemap")
	}
	if result.Truncated {
		fmt.Fprintf(os.Stderr, "  [warn] Sitemap truncated: %d of %d eligible URLs (max 50,000)\n",
			result.URLCount, result.TotalEligible)
	}
	return os.WriteFile(outputPath, []byte(result.XML), 0644)
}

// envFallback returns the flag value if non-empty, otherwise checks env vars in order.
func envFallback(flagVal string, envVars ...string) string {
	if flagVal != "" {
		return flagVal
	}
	for _, env := range envVars {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return ""
}

// defaultDBPath derives a SQLite crawl DB path from the config.
// Priority: 1) config.DBPath (explicit --db), 2) output path with .db extension,
// 3) seed URL domain name with .db extension.
func defaultDBPath(config types.CrawlConfig) string {
	if config.DBPath != "" {
		return config.DBPath
	}
	if config.OutputPath != "" {
		base := config.OutputPath
		// Strip known extensions
		for _, ext := range []string{".zst", ".gz", ".jsonl", ".csv", ".json"} {
			base = strings.TrimSuffix(base, ext)
		}
		return base + ".db"
	}
	// Derive from seed URL domain
	if config.SeedURL != "" {
		if u, err := url.Parse(config.SeedURL); err == nil && u.Host != "" {
			return u.Host + ".db"
		}
	}
	return "crawl.db"
}

func ensureScheme(u string) string {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "https://" + u
	}
	return u
}

func parseHeaders(raw []string) map[string]string {
	headers := make(map[string]string)
	for _, h := range raw {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) == 2 {
			headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return headers
}

func resolveFormat(format, path string) types.OutputFormat {
	if format != "" {
		return types.OutputFormat(format)
	}
	// Strip compression extension before checking format
	p := path
	for _, ext := range []string{".gz", ".zst"} {
		p = strings.TrimSuffix(p, ext)
	}
	if strings.HasSuffix(p, ".csv") {
		return types.FormatCSV
	}
	return types.FormatJSONL
}

func readURLsFromFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open URL file: %w", err)
	}
	defer f.Close()

	var urls []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		urls = append(urls, ensureScheme(line))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs found in %s", path)
	}
	return urls, nil
}

func readJSONLPages(path string) ([]*types.PageData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Use shared decompression helper (supports .gz and .zst)
	reader, cleanup, err := storage.DecompressReader(f, path)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	var pages []*types.PageData
	var skipped int
	lineNum := 0
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1<<20), 10<<20) // 10 MB max line
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var page types.PageData
		if err := json.Unmarshal(line, &page); err != nil {
			skipped++
			continue
		}
		pages = append(pages, &page)
	}
	if err := scanner.Err(); err != nil {
		return pages, err
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "Warning: skipped %d malformed lines in %s\n", skipped, path)
	}
	return pages, nil
}

func parseSegmentFlags(segments []string) []types.SegmentRule {
	var rules []types.SegmentRule
	for _, s := range segments {
		idx := strings.Index(s, ":")
		if idx < 0 {
			fmt.Fprintf(os.Stderr, "  [warn] invalid segment %q (expected name:pattern)\n", s)
			continue
		}
		rules = append(rules, types.SegmentRule{
			Name:    strings.TrimSpace(s[:idx]),
			Pattern: strings.TrimSpace(s[idx+1:]),
		})
	}
	return rules
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}

// parseMemLimit parses a human-readable memory limit (e.g. "2GiB", "512MiB", "1GB").
func parseMemLimit(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty memory limit")
	}

	// Ordered longest-suffix-first to avoid "B" matching before "GB"/"GiB"/etc.
	suffixes := []struct {
		suffix string
		mult   int64
	}{
		{"TiB", 1024 * 1024 * 1024 * 1024},
		{"GiB", 1024 * 1024 * 1024},
		{"MiB", 1024 * 1024},
		{"KiB", 1024},
		{"TB", 1000 * 1000 * 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"KB", 1000},
		{"B", 1},
	}

	for _, entry := range suffixes {
		if strings.HasSuffix(s, entry.suffix) {
			numStr := strings.TrimSuffix(s, entry.suffix)
			num, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number %q: %w", numStr, err)
			}
			return int64(num * float64(entry.mult)), nil
		}
	}

	// Try plain number (bytes)
	num, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory limit %q", s)
	}
	return num, nil
}
