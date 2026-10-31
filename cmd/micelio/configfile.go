package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/micelio/micelio/internal/server"
	"github.com/micelio/micelio/internal/types"
)

// loadConfigFile reads a crawl config JSON in the dashboard format (the body
// of POST /api/crawl/start, or a schedule's crawlConfig) and maps it with
// the same rules as the dashboard (url, limit, depth, delay, urlList).
// Keys missing from the file take their value from defaults (the config
// built from the flags), so a file without respectRobots or delay keeps the
// CLI defaults instead of zero values. limit is the page limit the file
// itself sets ("limit" or "maxPages"), 0 when it sets none: the merged
// config always holds one, since ConfigFromMap turns 0 into 1000.
func loadConfigFile(path string, defaults types.CrawlConfig) (config types.CrawlConfig, limit int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return types.CrawlConfig{}, 0, fmt.Errorf("read config %s: %w", path, err)
	}
	var file map[string]interface{}
	if err := json.Unmarshal(data, &file); err != nil {
		return types.CrawlConfig{}, 0, fmt.Errorf("parse config %s: %w", path, err)
	}
	if file == nil {
		return types.CrawlConfig{}, 0, fmt.Errorf("parse config %s: want a JSON object", path)
	}
	// ConfigFromMap adds the canonical keys (seedUrl, maxPages, delayMs...)
	// for the dashboard names, so they win over the defaults below.
	server.ConfigFromMap(file)
	if n, ok := file["maxPages"].(float64); ok && n > 0 {
		limit = int(n)
	}

	merged := map[string]interface{}{}
	if raw, err := json.Marshal(defaults); err == nil {
		_ = json.Unmarshal(raw, &merged)
	}
	for k, v := range file {
		merged[k] = v
	}
	return server.ConfigFromMap(merged), limit, nil
}

// copyField copies the field(s) a flag fills from src into dst.
type copyField func(dst, src *types.CrawlConfig)

// overlayFields maps each crawl flag to the CrawlConfig field(s) it fills.
// Flags with no field of their own (config, db-only, no-analysis) are not here.
var overlayFields = map[string]copyField{
	"concurrency": func(d, s *types.CrawlConfig) { d.Concurrency = s.Concurrency },
	"max-pages":   func(d, s *types.CrawlConfig) { d.MaxPages = s.MaxPages },
	"limit":       func(d, s *types.CrawlConfig) { d.MaxPages = s.MaxPages }, // sitemap
	"depth":       func(d, s *types.CrawlConfig) { d.MaxDepth = s.MaxDepth },
	"delay": func(d, s *types.CrawlConfig) {
		d.DelayMs, d.DelayExplicit = s.DelayMs, s.DelayExplicit
	},
	"user-agent": func(d, s *types.CrawlConfig) { d.UserAgent = s.UserAgent },
	// The format is derived from the output path when --format is not set.
	"output": func(d, s *types.CrawlConfig) {
		d.OutputPath, d.OutputFormat = s.OutputPath, s.OutputFormat
	},
	"format":                func(d, s *types.CrawlConfig) { d.OutputFormat = s.OutputFormat },
	"proxy":                 func(d, s *types.CrawlConfig) { d.Proxy = s.Proxy },
	"max-errors":            func(d, s *types.CrawlConfig) { d.MaxErrors = s.MaxErrors },
	"timeout":               func(d, s *types.CrawlConfig) { d.TimeoutSeconds = s.TimeoutSeconds },
	"header":                func(d, s *types.CrawlConfig) { d.CustomHeaders = s.CustomHeaders },
	"cookies":               func(d, s *types.CrawlConfig) { d.Cookies = s.Cookies },
	"include":               func(d, s *types.CrawlConfig) { d.IncludePatterns = s.IncludePatterns },
	"exclude":               func(d, s *types.CrawlConfig) { d.ExcludePatterns = s.ExcludePatterns },
	"path-only-filters":     func(d, s *types.CrawlConfig) { d.PathOnlyFilters = s.PathOnlyFilters },
	"allowed-domains":       func(d, s *types.CrawlConfig) { d.AllowedDomains = s.AllowedDomains },
	"no-robots":             func(d, s *types.CrawlConfig) { d.RespectRobots = s.RespectRobots },
	"show-blocked-internal": func(d, s *types.CrawlConfig) { d.ShowBlockedInternal = s.ShowBlockedInternal },
	"retries":               func(d, s *types.CrawlConfig) { d.Retries = s.Retries },

	"js":                     func(d, s *types.CrawlConfig) { d.JSRendering = s.JSRendering },
	"render-block-resources": func(d, s *types.CrawlConfig) { d.RenderBlockRes = s.RenderBlockRes },
	"render-timeout":         func(d, s *types.CrawlConfig) { d.RenderTimeoutSec = s.RenderTimeoutSec },
	"snippet":                func(d, s *types.CrawlConfig) { d.SnippetPaths = s.SnippetPaths },

	"ngrams":               func(d, s *types.CrawlConfig) { d.Ngrams = s.Ngrams },
	"language":             func(d, s *types.CrawlConfig) { d.Language = s.Language },
	"link-intelligence":    func(d, s *types.CrawlConfig) { d.LinkIntelligence = s.LinkIntelligence },
	"li-no-centrality":     func(d, s *types.CrawlConfig) { d.LINoCentrality = s.LINoCentrality },
	"li-max-suggestions":   func(d, s *types.CrawlConfig) { d.LIMaxSuggestions = s.LIMaxSuggestions },
	"embeddings":           func(d, s *types.CrawlConfig) { d.Embeddings = s.Embeddings },
	"embedding-model":      func(d, s *types.CrawlConfig) { d.EmbeddingModel = s.EmbeddingModel },
	"embedding-provider":   func(d, s *types.CrawlConfig) { d.EmbeddingProvider = s.EmbeddingProvider },
	"embedding-key":        func(d, s *types.CrawlConfig) { d.EmbeddingKey = s.EmbeddingKey },
	"similarity-threshold": func(d, s *types.CrawlConfig) { d.SimilarityThreshold = s.SimilarityThreshold },
	"segment":              func(d, s *types.CrawlConfig) { d.SegmentRules = s.SegmentRules },

	"psi":         func(d, s *types.CrawlConfig) { d.PSI = s.PSI },
	"psi-key":     func(d, s *types.CrawlConfig) { d.PSIKey = s.PSIKey },
	"ai-prompt":   func(d, s *types.CrawlConfig) { d.AIPrompt = s.AIPrompt },
	"ai-provider": func(d, s *types.CrawlConfig) { d.AIProvider = s.AIProvider },
	"ai-model":    func(d, s *types.CrawlConfig) { d.AIModel = s.AIModel },
	"ai-key":      func(d, s *types.CrawlConfig) { d.AIKey = s.AIKey },

	"gsc":          func(d, s *types.CrawlConfig) { d.GSC = s.GSC },
	"gsc-property": func(d, s *types.CrawlConfig) { d.GSCProperty = s.GSCProperty },
	"gsc-key-file": func(d, s *types.CrawlConfig) { d.GSCKeyFile = s.GSCKeyFile },
	"gsc-days":     func(d, s *types.CrawlConfig) { d.GSCDays = s.GSCDays },
	"gsc-bq":       func(d, s *types.CrawlConfig) { d.GSCBqDataset = s.GSCBqDataset },
	"ga4":          func(d, s *types.CrawlConfig) { d.GA4 = s.GA4 },
	"ga4-property": func(d, s *types.CrawlConfig) { d.GA4Property = s.GA4Property },
	"ga4-key-file": func(d, s *types.CrawlConfig) { d.GA4KeyFile = s.GA4KeyFile },
	"ga4-days":     func(d, s *types.CrawlConfig) { d.GA4Days = s.GA4Days },

	"crux":              func(d, s *types.CrawlConfig) { d.CrUX = s.CrUX },
	"crux-key":          func(d, s *types.CrawlConfig) { d.CrUXKey = s.CrUXKey },
	"crux-form-factor":  func(d, s *types.CrawlConfig) { d.CrUXFormFactor = s.CrUXFormFactor },
	"plausible":         func(d, s *types.CrawlConfig) { d.Plausible = s.Plausible },
	"plausible-site-id": func(d, s *types.CrawlConfig) { d.PlausibleSiteID = s.PlausibleSiteID },
	"plausible-api-key": func(d, s *types.CrawlConfig) { d.PlausibleAPIKey = s.PlausibleAPIKey },
	"plausible-days":    func(d, s *types.CrawlConfig) { d.PlausibleDays = s.PlausibleDays },
	"plausible-host":    func(d, s *types.CrawlConfig) { d.PlausibleHost = s.PlausibleHost },

	"extract":          func(d, s *types.CrawlConfig) { d.CustomExtractions = s.CustomExtractions },
	"search":           func(d, s *types.CrawlConfig) { d.CustomSearches = s.CustomSearches },
	"check-external":   func(d, s *types.CrawlConfig) { d.CheckExternal = s.CheckExternal },
	"page-weight":      func(d, s *types.CrawlConfig) { d.PageWeight = s.PageWeight },
	"full-anchors":     func(d, s *types.CrawlConfig) { d.FullAnchors = s.FullAnchors },
	"full-page-weight": func(d, s *types.CrawlConfig) { d.FullPageWeight = s.FullPageWeight },
	"html":             func(d, s *types.CrawlConfig) { d.HTMLReport = s.HTMLReport },
	"html-open":        func(d, s *types.CrawlConfig) { d.HTMLOpen = s.HTMLOpen },
	"sitemap-out":      func(d, s *types.CrawlConfig) { d.SitemapOut = s.SitemapOut },
	"save-html":        func(d, s *types.CrawlConfig) { d.SaveHTML = s.SaveHTML },
	"save-rendered":    func(d, s *types.CrawlConfig) { d.SaveRendered = s.SaveRendered },

	"db":               func(d, s *types.CrawlConfig) { d.DBPath = s.DBPath },
	"no-db":            func(d, s *types.CrawlConfig) { d.NoDB = s.NoDB },
	"resume":           func(d, s *types.CrawlConfig) { d.Resume = s.Resume },
	"no-soft404-probe": func(d, s *types.CrawlConfig) { d.NoSoft404Probe = s.NoSoft404Probe },

	"adaptive-rate":     func(d, s *types.CrawlConfig) { d.AdaptiveRate = s.AdaptiveRate },
	"delay-factor":      func(d, s *types.CrawlConfig) { d.DelayFactor = s.DelayFactor },
	"stealth":           func(d, s *types.CrawlConfig) { d.Stealth = s.Stealth },
	"discover-sitemaps": func(d, s *types.CrawlConfig) { d.DiscoverSitemaps = s.DiscoverSitemaps },
	"tor-control-port":  func(d, s *types.CrawlConfig) { d.TorControlPort = s.TorControlPort },
	"tor-password":      func(d, s *types.CrawlConfig) { d.TorPassword = s.TorPassword },
	"tor-rotate":        func(d, s *types.CrawlConfig) { d.TorRotateEvery = s.TorRotateEvery },
}

// overlayFlags returns base with every flag the user set on cmd copied from
// fromFlags, the config built from the command's flags. Flags left at their
// default never override base. API keys empty in base take the value of
// fromFlags, which already holds the environment fallback.
func overlayFlags(cmd *cobra.Command, base, fromFlags types.CrawlConfig) types.CrawlConfig {
	flags := cmd.Flags()
	for name, copyFn := range overlayFields {
		if flags.Lookup(name) != nil && flags.Changed(name) {
			copyFn(&base, &fromFlags)
		}
	}
	// NoDB is never read from the file (json:"-").
	base.NoDB = fromFlags.NoDB

	// A key in the file wins over the environment; a missing one does not.
	fillKey := func(dst *string, src string) {
		if *dst == "" {
			*dst = src
		}
	}
	fillKey(&base.PSIKey, fromFlags.PSIKey)
	fillKey(&base.AIKey, fromFlags.AIKey)
	fillKey(&base.EmbeddingKey, fromFlags.EmbeddingKey)
	fillKey(&base.CrUXKey, fromFlags.CrUXKey)
	fillKey(&base.PlausibleAPIKey, fromFlags.PlausibleAPIKey)
	return base
}
