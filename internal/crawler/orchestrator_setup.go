package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/integration"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// crawlSession holds all shared state for a single crawl execution.
type crawlSession struct {
	config    types.CrawlConfig
	crawlDB   CrawlStore
	startTime time.Time

	// HTTP
	fetchOpts    FetchOptions
	cacheHeaders map[string]types.CacheEntry

	// Queue
	queue   *CrawlQueue
	seedURL string

	soft404Probes []types.Soft404Probe
	sem           *dynSem // worker slots; resizable (Tuner)

	// Storage
	writer *storage.ResultWriter
	sink   *pageSink // single writer for writer + crawlDB while workers run

	// Browser
	renderer   *browser.Renderer
	browserSem chan struct{}

	// Integration clients
	psiClient  *integration.PSIClient
	aiCfg      *analysis.AIConfig
	psiLimiter *time.Ticker
	aiLimiter  *time.Ticker

	// Robots & delay
	robotsChecker *RobotsChecker  // seed origin (sitemap discovery, logging)
	robots        *robotsRegistry // per-origin robots.txt, fetched lazily
	delay         time.Duration
	adaptiveRL    *AdaptiveRateLimiter
	hostRL        *hostRateLimiter // per-host token buckets (delay, Crawl-delay, Retry-After)
	torManager    *TorManager

	// N-grams
	ngramAnalyzer *analysis.NgramAnalyzer

	// Anchor stats collected per page before Anchors are dropped
	anchorAgg *analysis.AnchorAggregator

	// DNS cache
	dnsCache *dnsCache

	// Change detection
	prevContentHashes map[string]string

	// Crawl state
	canReloadFromDisk bool
	pages             []*types.PageData
	pagesMu           sync.Mutex
	errorCount        atomic.Int32
	crawled           atomic.Int32
	resumeOffset      int32
	rateStart         time.Time
	onProgress        OnProgress
	onPhase           func(phase string, loaded, total int) // progress during resume/analysis loading

	// Sitemap discovery
	discoveredSitemapURLs []string

	// Disk accumulators
	extLinksDisk  *externalLinksDisk
	resRefsDisk   *resourceRefsDisk
	intLinksDisk  *internalLinksDisk
	dlqDisk       *deadLetterDisk
	transferSizes map[string]int64

	// Crawl budget
	pathBudgets []pathBudgetCounter
}

func newCrawlSession(ctx context.Context, config types.CrawlConfig, onProgress OnProgress, store []CrawlStore) (_ *crawlSession, err error) {
	s := &crawlSession{
		config:        config,
		startTime:     time.Now(),
		onProgress:    onProgress,
		transferSizes: make(map[string]int64),
		rateStart:     time.Now(),
	}

	// Extract optional crawl store
	if len(store) > 0 && store[0] != nil {
		s.crawlDB = store[0]
	}

	s.applyConfigDefaults()

	ok := false
	defer func() {
		if !ok {
			if err != nil && s.queue != nil {
				coverage := s.queue.CoverageSnapshot()
				coverage.Final = true
				err = &coverageSetupError{error: err, coverage: coverage}
			}
			s.cleanup()
		}
	}()

	if err := s.setupHTTPClient(); err != nil {
		return nil, err
	}
	if err := s.setupQueue(); err != nil {
		return nil, err
	}
	s.setupDiskAccumulators() // before resumption: intLinksDisk needed for resumed pages
	if err := s.setupResumption(); err != nil {
		return nil, err
	}
	if err := s.setupWriter(); err != nil {
		return nil, err
	}
	s.setupBrowser()
	s.setupIntegrationClients()
	s.setupNgrams()
	s.anchorAgg = analysis.NewAnchorAggregator()
	s.setupRobotsAndDelay(ctx)
	s.setupSitemapDiscovery(ctx)
	s.setupSoft404Probe(ctx)
	if err := s.seedSitemapMode(ctx); err != nil {
		return nil, err
	}

	ok = true
	return s, nil
}

func (s *crawlSession) applyConfigDefaults() {
	if s.config.SeedURL == "" && len(s.config.URLs) == 0 && len(s.config.SitemapURLs) == 0 {
		return // validated in Crawl()
	}
	// SitemapURLs is only consumed by sitemap mode; if a caller supplied one
	// without a mode, treat it as a sitemap crawl rather than silently crawling
	// nothing.
	if s.config.Mode == "" && len(s.config.SitemapURLs) > 0 {
		s.config.Mode = types.ModeSitemap
	}
	if s.config.Concurrency <= 0 {
		s.config.Concurrency = 5
	}
	if s.config.MaxPages <= 0 {
		s.config.MaxPages = 1000
	}
	if s.config.MaxDepth <= 0 {
		s.config.MaxDepth = 10
	}
	if s.config.UserAgent == "" {
		s.config.UserAgent = "Micelio/1.0"
	}
	if s.config.OutputFormat == "" {
		s.config.OutputFormat = types.FormatJSONL
	}
}

func (s *crawlSession) setupHTTPClient() error {
	s.dnsCache = newDNSCache(defaultDNSTTL)

	var proxyURL *url.URL

	// Tor proxy: set up TorManager and override proxy URL
	if IsTorProxy(s.config.Proxy) {
		s.torManager = NewTorManager(s.config.TorControlPort, s.config.TorPassword, s.config.TorRotateEvery)
		proxyURL = s.torManager.ProxyURL()
		fmt.Fprintf(os.Stderr, "  [tor] Using Tor SOCKS5 proxy, rotating every %d requests\n", s.torManager.rotateEvery)
	} else if s.config.Proxy != "" {
		var err error
		proxyURL, err = url.Parse(s.config.Proxy)
		if err != nil {
			return fmt.Errorf("invalid proxy URL: %w", err)
		}
	}

	timeout := defaultFetchTimeout
	if s.config.TimeoutSeconds > 0 {
		timeout = time.Duration(s.config.TimeoutSeconds) * time.Second
	}
	var client *http.Client
	if s.config.Stealth {
		var err error
		client, err = StealthClient(timeout, proxyURL)
		if err != nil {
			return fmt.Errorf("stealth client: %w", err)
		}
	} else if s.config.SkipSSRF {
		client = UnsafeClient(timeout, proxyURL)
	} else {
		client = newClient(timeout, proxyURL, true, s.dnsCache)
	}
	// Make sure the connection pool is not the bottleneck at high concurrency.
	if t, ok := client.Transport.(*http.Transport); ok {
		if t.MaxIdleConnsPerHost < s.config.Concurrency {
			t.MaxIdleConnsPerHost = s.config.Concurrency
		}
		if t.MaxConnsPerHost > 0 && t.MaxConnsPerHost < s.config.Concurrency {
			t.MaxConnsPerHost = s.config.Concurrency
		}
		if t.MaxIdleConns < s.config.Concurrency {
			t.MaxIdleConns = s.config.Concurrency
		}
	}

	s.fetchOpts = FetchOptions{
		UserAgent:     s.config.UserAgent,
		CustomHeaders: s.config.CustomHeaders,
		Cookies:       s.config.Cookies,
		Client:        client,
		Timeout:       timeout,
		SkipSSRF:      s.config.SkipSSRF,
		Stealth:       s.config.Stealth,
		// Retries are handled by processEntry: re-queued through the per-host
		// rate limiter so all workers back off together.
		NoRetry: true,
	}
	return nil
}

func (s *crawlSession) setupQueue() error {
	queueOpts := &QueueOptions{
		EnforceInternal: true,
		AllowedDomains:  s.config.AllowedDomains,
		PathOnlyFilters: s.config.PathOnlyFilters,
	}
	for _, p := range s.config.IncludePatterns {
		re, err := compilePattern(p)
		if err != nil {
			return fmt.Errorf("invalid include pattern %q: %w", p, err)
		}
		queueOpts.IncludePatterns = append(queueOpts.IncludePatterns, re)
	}
	for _, p := range s.config.ExcludePatterns {
		re, err := compilePattern(p)
		if err != nil {
			return fmt.Errorf("invalid exclude pattern %q: %w", p, err)
		}
		queueOpts.ExcludePatterns = append(queueOpts.ExcludePatterns, re)
	}

	s.seedURL = s.config.SeedURL
	if s.seedURL == "" && len(s.config.URLs) > 0 {
		s.seedURL = s.config.URLs[0]
	}

	s.queue = NewCrawlQueue(s.seedURL, s.config.MaxDepth, s.config.MaxPages, queueOpts)

	if s.config.Mode == types.ModeList && len(s.config.URLs) > 0 {
		for _, u := range s.config.URLs {
			s.queue.Enqueue(u, 0, nil)
		}
	} else if s.config.Mode == types.ModeSitemap {
		// The seed is a sitemap file, not a crawlable page. The page URLs it
		// lists are fetched and enqueued later by seedSitemapMode.
	} else {
		s.queue.Enqueue(s.seedURL, 0, nil)
	}
	return nil
}

func (s *crawlSession) setupResumption() error {
	s.canReloadFromDisk = (s.config.OutputPath != "" && s.config.OutputFormat == types.FormatJSONL) || s.crawlDB != nil

	if s.crawlDB == nil {
		return nil
	}

	isResuming := false
	resumeCount := 0

	if s.config.Resume {
		pageCount, err := s.crawlDB.PageCount()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] resume: count pages: %v\n", err)
		} else if pageCount > 0 {
			visitedURLs, err := s.crawlDB.GetVisitedURLs()
			if err != nil {
				fmt.Fprintf(os.Stderr, "  [warn] resume: load visited URLs: %v\n", err)
			} else {
				for _, u := range visitedURLs {
					s.queue.MarkVisited(u)
				}
			}
			// Stream pages one at a time to avoid loading all into memory.
			// At 700K+ pages, GetAllPages would need 15-20GB of RAM.
			if s.onPhase != nil {
				s.onPhase("resume_loading", 0, pageCount)
			}
			// In sitemap mode the queue has no seed URL, so its seed domain is
			// empty here. Re-point it to a previously-crawled page before the
			// pending queue is restored below, otherwise EnqueueEntry's
			// internal-domain check would drop the restored cross-domain URLs.
			nonSpiderSeedSet := s.queue.SeedDomain() != ""
			streamErr := s.crawlDB.StreamPages(func(rp *types.PageData, idx int) error {
				if s.config.Mode == types.ModeSpider || s.config.Mode == "" {
					for _, link := range rp.InternalLinks {
						ref := rp.URL
						s.queue.Enqueue(link, rp.Depth+1, &ref)
					}
				} else if !nonSpiderSeedSet {
					if p, perr := url.Parse(rp.URL); perr == nil && p.Hostname() != "" {
						s.queue.UpdateSeedDomain(p.Hostname())
						nonSpiderSeedSet = true
					}
				}
				if s.intLinksDisk != nil && len(rp.InternalLinks) > 0 {
					s.intLinksDisk.AddPage(rp.URL, rp.InternalLinks)
				}
				resumeCount++
				if resumeCount%10000 == 0 {
					fmt.Fprintf(os.Stderr, "  [resume] Streaming pages: %d / %d\n", resumeCount, pageCount)
					if s.onPhase != nil {
						s.onPhase("resume_loading", resumeCount, pageCount)
					}
				}
				return nil
			})
			if streamErr != nil {
				fmt.Fprintf(os.Stderr, "  [warn] resume: stream pages: %v\n", streamErr)
			}
			pending, _ := s.crawlDB.PendingCount()
			fmt.Fprintf(os.Stderr, "  [resume] %d pages already in database, %d URLs pending\n", pageCount, pending)
			isResuming = true
		}
	}

	var chErr error
	s.cacheHeaders, chErr = s.crawlDB.GetCacheHeaders()
	if chErr != nil {
		fmt.Fprintf(os.Stderr, "  [warn] db: load cache headers: %v\n", chErr)
	} else if len(s.cacheHeaders) > 0 {
		fmt.Fprintf(os.Stderr, "  [conditional] Loaded %d cached ETag/Last-Modified entries\n", len(s.cacheHeaders))
	}

	// Load content hashes for change detection
	if s.config.Resume {
		hashes, hashErr := s.crawlDB.GetContentHashes()
		if hashErr != nil {
			fmt.Fprintf(os.Stderr, "  [warn] db: load content hashes: %v\n", hashErr)
		} else if len(hashes) > 0 {
			s.prevContentHashes = hashes
			fmt.Fprintf(os.Stderr, "  [change-detect] Loaded %d content hashes for change detection\n", len(hashes))
		}
	}

	if err := s.crawlDB.SaveConfig(s.config); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] db: save config: %v\n", err)
	}
	if err := s.crawlDB.SetStatus("running"); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] db: set status: %v\n", err)
	}

	if isResuming && resumeCount > 0 {
		s.resumeOffset = int32(resumeCount)
		s.crawled.Store(s.resumeOffset)
		// Load pending queue entries saved from a previous interrupted crawl
		pendingEntries, pendErr := s.crawlDB.GetPendingQueue()
		if pendErr != nil {
			fmt.Fprintf(os.Stderr, "  [warn] resume: load pending queue: %v\n", pendErr)
		} else if len(pendingEntries) > 0 {
			for _, pe := range pendingEntries {
				s.queue.EnqueueEntry(pe)
			}
			// Clear loaded entries to avoid stale rows on subsequent resumes
			if clrErr := s.crawlDB.ClearPendingQueue(); clrErr != nil {
				fmt.Fprintf(os.Stderr, "  [warn] resume: clear pending queue: %v\n", clrErr)
			}
			fmt.Fprintf(os.Stderr, "  [resume] Restored %d pending URLs from previous session\n", len(pendingEntries))
		}
		fmt.Fprintf(os.Stderr, "  [resume] Re-seeded queue from %d resumed pages (streamed)\n", resumeCount)
		// No need to keep resumedPages in memory — pages are already in DB
		// and will be reloaded from disk for analysis
	}

	return nil
}

func (s *crawlSession) setupWriter() error {
	if s.config.OutputPath == "" {
		return nil
	}
	var err error
	s.writer, err = storage.NewResultWriter(s.config.OutputPath, string(s.config.OutputFormat))
	if err != nil {
		return fmt.Errorf("create writer: %w", err)
	}
	// Write resumed pages to output by streaming from DB (avoids loading all into memory)
	if s.config.Resume && s.crawlDB != nil && s.resumeOffset > 0 {
		written := 0
		streamErr := s.crawlDB.StreamPages(func(rp *types.PageData, idx int) error {
			if writeErr := s.writer.Write(rp); writeErr != nil {
				fmt.Fprintf(os.Stderr, "  [warn] resume write: %v\n", writeErr)
			}
			written++
			return nil
		})
		if streamErr != nil {
			fmt.Fprintf(os.Stderr, "  [warn] resume: stream pages to writer: %v\n", streamErr)
		} else if written > 0 {
			fmt.Fprintf(os.Stderr, "  [resume] Wrote %d pages to output file\n", written)
		}
	}
	return nil
}

func (s *crawlSession) setupBrowser() {
	if !s.config.JSRendering {
		return
	}
	cfg := browser.RenderConfig{
		BlockResources: s.config.RenderBlockRes,
	}
	if s.config.RenderTimeoutSec > 0 {
		cfg.Timeout = time.Duration(s.config.RenderTimeoutSec) * time.Second
	}
	s.renderer = browser.NewRenderer(s.config.Proxy, cfg)
	s.browserSem = make(chan struct{}, 4)
	features := "max 4 tabs"
	if cfg.BlockResources {
		features += ", resource-blocking"
	}
	if cfg.Timeout > 0 {
		features += fmt.Sprintf(", timeout=%v", cfg.Timeout)
	}
	fmt.Fprintf(os.Stderr, "  [browser] Headless Chrome enabled for JS rendering (%s)\n", features)
}

func (s *crawlSession) setupIntegrationClients() {
	if s.config.PSI && s.config.PSIKey != "" {
		s.psiClient = integration.NewPSIClient(s.config.PSIKey)
		s.psiLimiter = time.NewTicker(200 * time.Millisecond)
		fmt.Fprintf(os.Stderr, "  [psi] PageSpeed Insights enabled\n")
	}

	if s.config.AIPrompt != "" {
		key, err := analysis.ResolveAIKey(analysis.AIProvider(s.config.AIProvider), s.config.AIKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] AI analysis disabled: %v\n", err)
		} else {
			s.aiCfg = &analysis.AIConfig{
				Provider: analysis.AIProvider(s.config.AIProvider),
				APIKey:   key,
				Model:    s.config.AIModel,
				Prompt:   s.config.AIPrompt,
			}
			s.aiLimiter = time.NewTicker(100 * time.Millisecond)
			fmt.Fprintf(os.Stderr, "  [ai] AI analysis enabled (%s)\n", s.config.AIProvider)
		}
	}
}

func (s *crawlSession) setupDiskAccumulators() {
	if s.config.CheckExternal {
		var err error
		s.extLinksDisk, err = newExternalLinksDisk()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] extlinks disk failed: %v — check-external disabled\n", err)
		}
	}
	if s.config.PageWeight {
		var err error
		s.resRefsDisk, err = newResourceRefsDisk()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] resrefs disk failed: %v — page-weight disabled\n", err)
		}
	}
	// Always stream internal links to disk to avoid holding InternalLinks slices in memory.
	// Saves ~5GB for 1M pages. Used by BuildAdjacencyList post-crawl.
	{
		var err error
		s.intLinksDisk, err = newInternalLinksDisk()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] intlinks disk failed: %v — links kept in memory\n", err)
		}
	}
	// Dead letter queue (always enabled, negligible cost)
	{
		var err error
		s.dlqDisk, err = newDeadLetterDisk()
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [warn] dlq disk failed: %v — dead letters not tracked\n", err)
		}
	}
	// Path budgets
	for _, rule := range s.config.PathBudgets {
		s.pathBudgets = append(s.pathBudgets, pathBudgetCounter{prefix: rule.Prefix, maxPages: rule.MaxPages})
	}
}

func (s *crawlSession) setupNgrams() {
	if !s.config.Ngrams {
		return
	}
	lang := s.config.Language
	if lang == "" {
		lang = "en"
	}
	s.ngramAnalyzer = analysis.NewNgramAnalyzer(lang)
}

// maxCrawlDelay caps robots.txt Crawl-delay so a hostile value cannot stall a crawl.
const maxCrawlDelay = 30 * time.Second

func (s *crawlSession) setupRobotsAndDelay(ctx context.Context) {
	s.delay = time.Duration(s.config.DelayMs) * time.Millisecond
	if s.delay > 0 {
		fmt.Fprintf(os.Stderr, "  [config] Request delay: %v\n", s.delay)
	}

	if s.config.AdaptiveRate {
		s.adaptiveRL = NewAdaptiveRateLimiter(s.delay, s.config.DelayFactor)
		if s.config.DelayFactor > 0 {
			fmt.Fprintf(os.Stderr, "  [config] Adaptive rate limiting enabled (floor: %v, Heritrix factor: %.1f)\n", s.adaptiveRL.baseRate, s.config.DelayFactor)
		} else {
			fmt.Fprintf(os.Stderr, "  [config] Adaptive rate limiting enabled (floor: %v)\n", s.adaptiveRL.baseRate)
		}
	}
	// Per-host token buckets: even spacing across all workers, never below the
	// configured delay or the host's robots.txt Crawl-delay. Always created so
	// Retry-After pauses work even without --delay / --adaptive-rate.
	floor := s.delay
	if s.adaptiveRL != nil && s.adaptiveRL.baseRate > floor {
		floor = s.adaptiveRL.baseRate
	}
	s.hostRL = newHostRateLimiter(floor, s.adaptiveRL)

	isSpiderMode := s.config.Mode == types.ModeSpider || s.config.Mode == ""
	if !isSpiderMode || !s.config.RespectRobots {
		return
	}

	newRC := func() *RobotsChecker {
		return &RobotsChecker{SkipSSRF: s.config.SkipSSRF, Client: s.fetchOpts.Client}
	}
	s.robots = newRobotsRegistry(s.config.UserAgent, newRC, s.applyRobotsCrawlDelay)

	// Fetch the seed origin eagerly (sitemap discovery + early warnings).
	s.robotsChecker = newRC()
	if err := s.robotsChecker.Init(ctx, s.seedURL, s.config.UserAgent); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] robots.txt fetch error: %v\n", err)
	}
	s.robots.add(robotsOrigin(s.seedURL), s.robotsChecker)
}

// applyRobotsCrawlDelay is called once per origin after its robots.txt is
// loaded. It raises that host's rate-limit floor to the Crawl-delay.
func (s *crawlSession) applyRobotsCrawlDelay(origin string, rc *RobotsChecker) {
	if rc.IsUnavailable() {
		fmt.Fprintf(os.Stderr, "  [warn] robots.txt for %s returned %d (or was unreachable): treating host as fully disallowed\n", origin, rc.StatusCode())
		return
	}
	d := rc.CrawlDelayDuration(s.config.UserAgent)
	if d <= 0 {
		return
	}
	if d > maxCrawlDelay {
		fmt.Fprintf(os.Stderr, "  [robots] %s Crawl-delay %v exceeds maximum, capping at %v\n", origin, d, maxCrawlDelay)
		d = maxCrawlDelay
	}
	host := extractDomain(origin)
	if s.hostRL != nil {
		s.hostRL.SetHostFloor(host, d)
	}
	if d > s.delay {
		fmt.Fprintf(os.Stderr, "  [robots] %s: using Crawl-delay %v\n", host, d)
	}
}

func (s *crawlSession) setupSitemapDiscovery(ctx context.Context) {
	if !s.config.DiscoverSitemaps {
		return
	}
	isSpiderMode := s.config.Mode == types.ModeSpider || s.config.Mode == ""
	if !isSpiderMode {
		return
	}

	var robotsSitemaps []string
	if s.robotsChecker != nil {
		robotsSitemaps = s.robotsChecker.SitemapURLs()
	}

	fmt.Fprintf(os.Stderr, "  [sitemap-discovery] Fetching sitemaps...\n")
	urls, err := DiscoverSitemapURLs(ctx, s.seedURL, robotsSitemaps, s.fetchOpts.Client, s.config.UserAgent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [sitemap-discovery] Error: %v\n", err)
		return
	}
	if len(urls) == 0 {
		fmt.Fprintf(os.Stderr, "  [sitemap-discovery] No sitemap URLs found\n")
		return
	}

	s.discoveredSitemapURLs = urls

	// Enqueue sitemap URLs into crawl queue at depth 0
	enqueued := 0
	ref := "sitemap"
	for _, u := range urls {
		if s.queue.Enqueue(u, 0, &ref) {
			enqueued++
		}
	}
	fmt.Fprintf(os.Stderr, "  [sitemap-discovery] Found %d URLs in sitemaps, %d new URLs enqueued\n", len(urls), enqueued)
}

// seedSitemapMode fetches the configured sitemap(s), extracts their page URLs,
// and enqueues them for crawling. Unlike setupSitemapDiscovery (a spider-mode
// orphan-finder), this is the seed source for ModeSitemap: the sitemap file
// itself is never crawled as a page, and discovered links are not followed
// (enqueueLinks is gated to spider mode).
//
// Scope:
//   - If config.AllowedDomains is set, it bounds the crawl: only page URLs on
//     those hosts are kept (the sitemap cannot widen the user's scope).
//   - Otherwise the sitemap's own page hosts define the scope. The sitemap is
//     often hosted on a different domain than the pages it lists (e.g. an
//     S3-hosted sitemap), so each discovered page host is registered as
//     internal before its URLs are enqueued.
//
// URLs are normalized with the same NormalizeURL the queue uses, so the count,
// the registered-domain set, and the queue all agree on what is a real page.
// Each sitemap file is enqueued as it is parsed (only the dedup set is retained)
// to bound memory on large sitemaps.
func (s *crawlSession) seedSitemapMode(ctx context.Context) error {
	if s.config.Mode != types.ModeSitemap {
		return nil
	}

	sitemaps := s.config.SitemapURLs
	if len(sitemaps) == 0 && s.seedURL != "" {
		sitemaps = []string{s.seedURL}
	}
	if len(sitemaps) == 0 {
		return fmt.Errorf("sitemap mode requires at least one sitemap URL")
	}

	// When the user scoped the crawl, the allow-list wins over sitemap contents.
	var allowFilter map[string]bool
	if len(s.config.AllowedDomains) > 0 {
		allowFilter = make(map[string]bool, len(s.config.AllowedDomains))
		for _, d := range s.config.AllowedDomains {
			allowFilter[d] = true
		}
	}

	ref := "sitemap"
	seen := make(map[string]bool)
	registered := make(map[string]bool)
	seedDomainSet := false
	var lastErr error
	fetched, enqueued, skippedScope := 0, 0, 0

	for _, sm := range sitemaps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		urls, err := fetchSitemap(ctx, sm, s.fetchOpts.Client, s.config.UserAgent, 0)
		if err != nil {
			lastErr = err
			fmt.Fprintf(os.Stderr, "  [sitemap] %s: %v\n", sm, err)
			continue
		}
		fetched++
		for _, u := range urls {
			// NormalizeURL rejects non-http(s) schemes and matches the queue's
			// own dedup key, so invalid locs never inflate counts or pollute
			// the registered-domain set.
			n := NormalizeURL(u)
			if n == "" {
				s.queue.RecordExclusion("invalid_url", "", u)
				continue
			}
			if seen[n] {
				continue
			}
			seen[n] = true

			p, perr := url.Parse(u)
			if perr != nil || p.Hostname() == "" {
				continue
			}
			host := p.Hostname()
			if allowFilter != nil && !allowFilter[host] {
				skippedScope++
				s.queue.RecordExclusion("domain", "", u)
				continue
			}
			// Register the host as internal before enqueuing so enforceInternal
			// admits it.
			if !registered[host] {
				registered[host] = true
				if !seedDomainSet {
					s.queue.UpdateSeedDomain(host)
					s.seedURL = u
					seedDomainSet = true
				} else {
					s.queue.AddAllowedDomains(host)
				}
			}
			if s.queue.Enqueue(u, 0, &ref) {
				enqueued++
			}
		}
		urls = nil // release this file before fetching the next
	}

	if skippedScope > 0 {
		fmt.Fprintf(os.Stderr, "  [sitemap] %d URL(s) skipped (outside AllowedDomains)\n", skippedScope)
	}
	fmt.Fprintf(os.Stderr, "  [sitemap] %d/%d sitemap(s) fetched → %d page URL(s) enqueued across %d host(s)\n",
		fetched, len(sitemaps), enqueued, len(registered))

	if enqueued == 0 {
		// On resume there is already work in the restored queue/DB, so a
		// transient sitemap outage must not abort the whole session.
		if s.config.Resume && s.crawlDB != nil {
			fmt.Fprintf(os.Stderr, "  [sitemap] no new URLs; continuing from resumed queue\n")
			return nil
		}
		switch {
		case lastErr != nil:
			return fmt.Errorf("sitemap fetch failed for all sitemap(s): %w", lastErr)
		case skippedScope > 0:
			return fmt.Errorf("all %d sitemap URL(s) were outside AllowedDomains", skippedScope)
		default:
			return fmt.Errorf("no page URLs found in sitemap(s): %s", strings.Join(sitemaps, ", "))
		}
	}
	return nil
}

// abortReason says why the crawl must stop early (max errors or time
// limit reached), or "" to keep going.
func (s *crawlSession) abortReason() string {
	if n := int(s.errorCount.Load()); s.config.MaxErrors > 0 && n >= s.config.MaxErrors {
		return fmt.Sprintf("max errors reached (%d failed pages)", n)
	}
	if limit := time.Duration(s.config.TimeoutSeconds) * time.Second; limit > 0 && time.Since(s.startTime) > limit {
		return fmt.Sprintf("time limit reached (%s)", limit)
	}
	return ""
}

func (s *crawlSession) cleanup() {
	if s.queue != nil {
		s.queue.Close()
	}
	s.sink.Close()
	if s.writer != nil {
		s.writer.Close()
	}
	if s.renderer != nil {
		s.renderer.Close()
	}
	if s.psiLimiter != nil {
		s.psiLimiter.Stop()
	}
	if s.aiLimiter != nil {
		s.aiLimiter.Stop()
	}
}

// pathBudgetCounter tracks pages crawled under a path prefix.
type pathBudgetCounter struct {
	prefix   string
	maxPages int
	count    atomic.Int32
}

func (s *crawlSession) checkPathBudget(rawURL string) (exceeded bool, rule string) {
	if len(s.pathBudgets) == 0 {
		return false, ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false, ""
	}
	path := parsed.Path
	// Match longest prefix
	bestIdx, bestLen := -1, 0
	for i := range s.pathBudgets {
		p := s.pathBudgets[i].prefix
		if strings.HasPrefix(path, p) && len(p) > bestLen {
			bestIdx = i
			bestLen = len(p)
		}
	}
	if bestIdx < 0 {
		return false, ""
	}
	// Atomic increment-then-check: budget is a soft limit, may overshoot by up to
	// concurrency count under heavy contention (acceptable for crawl budgets).
	b := &s.pathBudgets[bestIdx]
	n := int(b.count.Add(1))
	if n > b.maxPages {
		return true, fmt.Sprintf("%s (limit %d)", b.prefix, b.maxPages)
	}
	return false, ""
}

func (s *crawlSession) addToDeadLetter(e types.QueueEntry, statusCode int, errMsg string) {
	if s.dlqDisk == nil {
		return
	}
	ref := ""
	if e.Referrer != nil {
		ref = *e.Referrer
	}
	s.pagesMu.Lock()
	s.dlqDisk.Add(types.DeadLetterEntry{
		URL:        e.URL,
		Error:      errMsg,
		Referrer:   ref,
		StatusCode: statusCode,
		Depth:      e.Depth,
		Requeues:   e.Requeues,
		FailedAt:   time.Now().UTC(),
	})
	s.pagesMu.Unlock()
}

func (s *crawlSession) flushPendingQueue() {
	if s.crawlDB == nil || s.queue == nil {
		return
	}
	entries := s.queue.Drain()
	if len(entries) == 0 {
		return
	}
	if err := s.crawlDB.SavePendingQueue(entries); err != nil {
		fmt.Fprintf(os.Stderr, "\n  [warn] queue flush: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "\n  [queue] Flushed %d pending URLs to database for resume\n", len(entries))
}

func (s *crawlSession) postCrawlReload() {
	if !s.canReloadFromDisk || s.pages != nil {
		return
	}
	if s.writer != nil {
		if closeErr := s.writer.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] close writer before reload: %v\n", closeErr)
		}
	}
	if s.crawlDB != nil {
		pageCount, _ := s.crawlDB.PageCount()
		if s.onPhase != nil {
			s.onPhase("analysis_loading", 0, pageCount)
		}
		var loadErr error
		s.pages, loadErr = s.crawlDB.GetAllPagesWithProgress(context.Background(), func(loaded int) {
			if s.onPhase != nil && loaded%5000 == 0 {
				s.onPhase("analysis_loading", loaded, pageCount)
			}
		})
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] reload from db: %v — falling back to JSONL\n", loadErr)
		}
	}
	if s.pages == nil && s.config.OutputPath != "" && s.config.OutputFormat == types.FormatJSONL {
		var loadErr error
		s.pages, loadErr = storage.ReadJSONLPages(s.config.OutputPath)
		if loadErr != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] reload from JSONL: %v\n", loadErr)
		}
	}
	if s.pages != nil {
		fmt.Fprintf(os.Stderr, "  [memory] Reloaded %d pages from disk for post-crawl analysis\n", len(s.pages))
	} else {
		fmt.Fprintf(os.Stderr, "\n  [ERROR] Failed to reload pages from disk — post-crawl analysis will be skipped\n")
	}
}
