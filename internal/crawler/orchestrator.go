package crawler

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime/debug"
	"sync"
	"time"

	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/integration"
	"github.com/micelio/micelio/internal/types"
)

// CrawlProgress reports crawl progress.
type CrawlProgress struct {
	CurrentURL     string          `json:"currentUrl"`
	PageError      string          `json:"pageError,omitempty"`
	Crawled        int             `json:"crawled"`
	Queued         int             `json:"queued"`
	TotalSeen      int             `json:"totalSeen"`
	Excluded       int             `json:"excluded"`
	ExcludedStats  []ExcludedStats `json:"excludedStats,omitempty"`
	Rate           float64         `json:"rate"`
	Errors         int             `json:"errors"`
	StatusCode     int             `json:"statusCode"`
	ResponseTimeMs int64           `json:"responseTimeMs"`
}

// CrawlResult holds the final crawl output.
type CrawlResult struct {
	ExternalLinks        map[string][]string
	ResourceRefs         map[string][]types.ResourceEntry
	TransferSizes        map[string]int64
	NgramAnalyzer        *analysis.NgramAnalyzer
	Pages                []*types.PageData
	DeadLetterQueue      []types.DeadLetterEntry
	DiscoveredSitemapURLs []string
	// InternalLinksIter yields per-page internal links from disk.
	// Used by BuildAdjacencyList to avoid holding links in memory.
	// Caller must call InternalLinksClose when done.
	InternalLinksIter  func(fn func(source string, targets []string)) error
	InternalLinksClose func()
	Duration           time.Duration
	Errors             int
	ChangedPages       int
	UnchangedPages     int
}

// OnProgress is a callback for crawl progress updates.
type OnProgress func(CrawlProgress)

// PauseFunc blocks while the crawl is paused. Returns true to continue, false if cancelled.
type PauseFunc func(ctx context.Context) bool

// PhaseFunc reports progress during long-running phases (resume loading, analysis loading).
// phase is a string like "resume_loading" or "analysis_loading".
// loaded and total indicate progress within the phase.
type PhaseFunc func(phase string, loaded, total int)

// CrawlStore is an interface for optional SQLite-backed crawl persistence.
type CrawlStore interface {
	InsertPage(page *types.PageData) error
	GetAllPages() ([]*types.PageData, error)
	GetAllPagesWithProgress(ctx context.Context, onProgress func(loaded int)) ([]*types.PageData, error)
	StreamPages(fn func(page *types.PageData, index int) error) error
	PageCount() (int, error)
	GetVisitedURLs() ([]string, error)
	PendingCount() (int, error)
	SavePendingQueue(entries []types.QueueEntry) error
	GetPendingQueue() ([]types.QueueEntry, error)
	ClearPendingQueue() error
	SaveConfig(config types.CrawlConfig) error
	SetStatus(status string) error
	GetCacheHeaders() (map[string]types.CacheEntry, error)
	GetContentHashes() (map[string]string, error)
	SavePageHTML(url string, sourceHTML, renderedHTML string) error
}

// CrawlOption configures optional crawl behavior.
type CrawlOption func(*crawlOptions)

type crawlOptions struct {
	store    CrawlStore
	pauseFn  PauseFunc
}

// WithStore sets the optional SQLite-backed crawl persistence.
func WithStore(s CrawlStore) CrawlOption {
	return func(o *crawlOptions) { o.store = s }
}

// WithPause sets a function that blocks while the crawl is paused.
func WithPause(fn PauseFunc) CrawlOption {
	return func(o *crawlOptions) { o.pauseFn = fn }
}

// Crawl runs a crawl with the given configuration.
// If store is non-nil, pages are persisted to SQLite for resume support.
func Crawl(ctx context.Context, config types.CrawlConfig, onProgress OnProgress, store ...CrawlStore) (*CrawlResult, error) {
	return CrawlWithOptions(ctx, config, onProgress, store, nil, nil)
}

// CrawlWithOptions runs a crawl with the given configuration and options.
func CrawlWithOptions(ctx context.Context, config types.CrawlConfig, onProgress OnProgress, store []CrawlStore, pauseFn PauseFunc, phaseFn PhaseFunc) (*CrawlResult, error) {
	if config.SeedURL == "" && len(config.URLs) == 0 {
		return nil, fmt.Errorf("no seed URL or URL list provided")
	}

	s, err := newCrawlSession(ctx, config, onProgress, store)
	if err != nil {
		return nil, err
	}
	s.onPhase = phaseFn
	defer s.cleanup()

	// Main crawl loop
	sem := make(chan struct{}, s.config.Concurrency)
	var wg sync.WaitGroup
	cancelled := false
	lastScavenge := int32(0)
	const scavengeInterval = 1000 // force OS memory return every N pages

	for !cancelled {
		if s.shouldAbort() {
			break
		}

		// Block while paused
		if pauseFn != nil {
			if !pauseFn(ctx) {
				cancelled = true
				continue
			}
		}

		entry := s.queue.Dequeue()
		if entry == nil {
			wg.Wait()
			entry = s.queue.Dequeue()
			if entry == nil {
				break
			}
		}

		select {
		case <-ctx.Done():
			cancelled = true
			continue
		default:
		}

		// Periodically force the Go runtime to return memory to the OS.
		// Without this, macOS MADV_FREE pages inflate RSS even though
		// they are logically free.
		if n := s.crawled.Load(); n-lastScavenge >= scavengeInterval {
			lastScavenge = n
			debug.FreeOSMemory()
		}

		sem <- struct{}{}
		wg.Add(1)

		go func(e types.QueueEntry) {
			defer func() {
				<-sem
				wg.Done()
			}()
			s.processEntry(ctx, e)
		}(*entry)
	}

	wg.Wait()

	// Persist pending queue on interruption for later resume
	interrupted := cancelled || s.shouldAbort()
	if interrupted {
		s.flushPendingQueue()
	}

	// Mark crawl status
	if s.crawlDB != nil {
		status := "complete"
		if interrupted {
			status = "interrupted"
		}
		if err := s.crawlDB.SetStatus(status); err != nil {
			fmt.Fprintf(os.Stderr, "\n  [warn] db: set %s status: %v\n", status, err)
		}
	}

	// GC checkpoint before post-crawl
	prevGOGC := debug.SetGCPercent(50)
	debug.FreeOSMemory()

	// Load disk-backed maps
	externalLinks := s.loadExternalLinks()
	resourceRefs := s.loadResourceRefs()
	dlqEntries := s.loadDeadLetterQueue()

	// Reload pages from disk for post-crawl analysis
	s.postCrawlReload()

	// Batch integrations with fresh context
	postCtx, postCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer postCancel()
	runBatchIntegrations(postCtx, s.pages, s.config)

	// Restore GOGC
	debug.SetGCPercent(prevGOGC)
	debug.FreeOSMemory()

	// Count changed/unchanged pages
	var changedPages, unchangedPages int
	for _, p := range s.pages {
		if p.ContentChanged != nil {
			if *p.ContentChanged {
				changedPages++
			} else {
				unchangedPages++
			}
		}
	}

	result := &CrawlResult{
		Pages:                 s.pages,
		Duration:              time.Since(s.startTime),
		Errors:                int(s.errorCount.Load()),
		ExternalLinks:         externalLinks,
		ResourceRefs:          resourceRefs,
		TransferSizes:         s.transferSizes,
		NgramAnalyzer:         s.ngramAnalyzer,
		DeadLetterQueue:       dlqEntries,
		DiscoveredSitemapURLs: s.discoveredSitemapURLs,
		ChangedPages:          changedPages,
		UnchangedPages:        unchangedPages,
	}
	if s.intLinksDisk != nil {
		result.InternalLinksIter = s.intLinksDisk.Iterate
		result.InternalLinksClose = s.intLinksDisk.Close
	}
	return result, nil
}

func (s *crawlSession) loadExternalLinks() map[string][]string {
	if s.extLinksDisk == nil {
		return nil
	}
	links, err := s.extLinksDisk.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] reload extlinks: %v\n", err)
		return make(map[string][]string)
	}
	fmt.Fprintf(os.Stderr, "  [memory] Loaded %d external links from disk\n", len(links))
	return links
}

func (s *crawlSession) loadDeadLetterQueue() []types.DeadLetterEntry {
	if s.dlqDisk == nil {
		return nil
	}
	entries, err := s.dlqDisk.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] reload dlq: %v\n", err)
		return nil
	}
	if len(entries) > 0 {
		fmt.Fprintf(os.Stderr, "  [dlq] %d URLs failed permanently\n", len(entries))
	}
	return entries
}

func (s *crawlSession) loadResourceRefs() map[string][]types.ResourceEntry {
	if s.resRefsDisk == nil {
		return nil
	}
	refs, err := s.resRefsDisk.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] reload resrefs: %v\n", err)
		return make(map[string][]types.ResourceEntry)
	}
	fmt.Fprintf(os.Stderr, "  [memory] Loaded %d page resource refs from disk\n", len(refs))
	return refs
}

// normalizeURL strips trailing slashes for consistent lookup.
func normalizeURL(u string) string {
	if len(u) > 1 && u[len(u)-1] == '/' {
		return u[:len(u)-1]
	}
	return u
}

// runBatchIntegrations fetches GSC, GA4, CrUX, and Plausible data for all crawled pages.
func runBatchIntegrations(ctx context.Context, pages []*types.PageData, config types.CrawlConfig) {
	var urls []string
	for _, p := range pages {
		if p.StatusCode == 200 {
			urls = append(urls, p.URL)
		}
	}
	if len(urls) == 0 {
		return
	}

	urlToPage := make(map[string]*types.PageData, len(pages))
	for _, p := range pages {
		urlToPage[normalizeURL(p.URL)] = p
	}
	lookupPage := func(u string) *types.PageData {
		return urlToPage[normalizeURL(u)]
	}

	var wg sync.WaitGroup

	// Google Search Console
	if config.GSC && config.GSCKeyFile != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "\n  [warn] GSC integration panic: %v\n", r)
				}
			}()
			keyJSON, err := os.ReadFile(config.GSCKeyFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] GSC key file: %v\n", err)
				return
			}
			days := config.GSCDays
			if days == 0 {
				days = 90
			}
			client, err := integration.NewGscClient(keyJSON, config.GSCProperty, days)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] GSC client: %v\n", err)
				return
			}
			gscData, err := client.FetchBatch(ctx, urls)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] GSC fetch: %v\n", err)
				return
			}
			for u, data := range gscData {
				if p := lookupPage(u); p != nil {
					p.GscData = data
				}
			}
			fmt.Fprintf(os.Stderr, "\n  [gsc] Enriched %d pages with Search Console data\n", len(gscData))

			// Fetch AI Overview data (data may not be available for all properties)
			aiData, aiErr := client.FetchAIOverviewData(ctx, urls)
			if aiErr != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] GSC AI Overview: %v\n", aiErr)
			}
			if len(aiData) > 0 {
				for u, data := range aiData {
					if p := lookupPage(u); p != nil {
						p.AIVisibilityData = data
					}
				}
				fmt.Fprintf(os.Stderr, "\n  [gsc] Found %d pages in AI Overviews\n", len(aiData))
			}
		}()
	}

	// GSC via BigQuery bulk export
	if config.GSCBqDataset != "" && !config.GSC {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "\n  [warn] GSC BQ integration panic: %v\n", r)
				}
			}()
			accessToken, err := integration.LoadStoredGscToken(
				os.Getenv("MICELIO_GSC_CLIENT_ID"),
				os.Getenv("MICELIO_GSC_CLIENT_SECRET"),
			)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] GSC BQ token: %v\n", err)
			}
			if accessToken == "" {
				fmt.Fprintf(os.Stderr, "\n  [warn] GSC BigQuery: no credentials — run 'micelio gsc-auth' first\n")
				return
			}
			days := config.GSCDays
			if days == 0 {
				days = 90
			}
			bqData, err := integration.FetchGscFromBigQuery(ctx, integration.GscBqOptions{
				Dataset:     config.GSCBqDataset,
				Days:        days,
				URLs:        urls,
				AccessToken: accessToken,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] GSC BigQuery: %v\n", err)
				return
			}
			for u, data := range bqData {
				if p := lookupPage(u); p != nil {
					p.GscData = data
				}
			}
			fmt.Fprintf(os.Stderr, "\n  [gsc-bq] Enriched %d pages with BigQuery data\n", len(bqData))
		}()
	}

	// Google Analytics 4
	if config.GA4 && config.GA4KeyFile != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "\n  [warn] GA4 integration panic: %v\n", r)
				}
			}()
			keyJSON, err := os.ReadFile(config.GA4KeyFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] GA4 key file: %v\n", err)
				return
			}
			days := config.GA4Days
			if days == 0 {
				days = 90
			}
			client, err := integration.NewGA4Client(keyJSON, config.GA4Property, days)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] GA4 client: %v\n", err)
				return
			}
			ga4Data, err := client.FetchBatch(ctx, urls)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] GA4 fetch: %v\n", err)
				return
			}
			for u, data := range ga4Data {
				if p := lookupPage(u); p != nil {
					p.Ga4Data = data
				}
			}
			fmt.Fprintf(os.Stderr, "\n  [ga4] Enriched %d pages with Analytics data\n", len(ga4Data))
		}()
	}

	// Chrome UX Report
	if config.CrUX && config.CrUXKey != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "\n  [warn] CrUX integration panic: %v\n", r)
				}
			}()
			client := integration.NewCruxClient(config.CrUXKey, config.CrUXFormFactor, 5)
			cruxData := client.FetchBatch(ctx, urls)
			enriched := 0
			for u, data := range cruxData {
				if p := lookupPage(u); p != nil {
					p.CruxData = data
					enriched++
				}
			}
			if enriched > 0 {
				fmt.Fprintf(os.Stderr, "\n  [crux] Enriched %d pages with CrUX data\n", enriched)
			}
		}()
	}

	// Plausible Analytics
	if config.Plausible && config.PlausibleAPIKey != "" && config.PlausibleSiteID != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "\n  [warn] Plausible integration panic: %v\n", r)
				}
			}()
			days := config.PlausibleDays
			if days == 0 {
				days = 30
			}
			host := config.PlausibleHost
			if host == "" {
				host = "https://plausible.io"
			}
			client := integration.NewPlausibleClient(config.PlausibleAPIKey, config.PlausibleSiteID, days, host)
			plausibleData, err := client.FetchBatch(ctx, urls)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\n  [warn] Plausible fetch: %v\n", err)
				return
			}
			for u, data := range plausibleData {
				if p := lookupPage(u); p != nil {
					p.PlausibleData = data
				}
			}
			fmt.Fprintf(os.Stderr, "\n  [plausible] Enriched %d pages with Plausible data\n", len(plausibleData))
		}()
	}

	wg.Wait()
}

func compilePattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}
