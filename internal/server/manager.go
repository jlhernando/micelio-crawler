package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/micelio/micelio/internal/alerts"
	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/logs"
	"github.com/micelio/micelio/internal/scheduler"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// ProgressEvent is a WebSocket progress message.
type ProgressEvent struct {
	Data    map[string]interface{} `json:"data"`
	CrawlID string                 `json:"crawlId"`
	Type    string                 `json:"type"`
}

// CrawlResults holds stats and an on-demand page loader for a completed crawl.
// Pages are not kept in memory; they are loaded from the crawl DB when requested.
type CrawlResults struct {
	Stats           map[string]interface{}   `json:"stats"`
	Pages           []*types.PageData        `json:"pages"`           // populated only for the active/recent crawl
	Graph           *analysis.AdjacencyGraph `json:"-"`               // CSR graph for edge rendering after InternalLinks freed
	AnchorStats     map[string]interface{}   `json:"-"`               // pre-computed anchor stats (survives stripPagesForCache)
	DirTree         map[string]interface{}   `json:"-"`               // pre-computed directory tree (survives stripPagesForCache)
	TemplateTypes   []string                 `json:"-"`               // cached distinct template types
	InlinkIndex     map[string][]string      `json:"-"`               // targetURL → []sourceURL for O(1) inlink lookups
	DBPath          string                   `json:"-"`               // path to crawl DB for lazy loading
	PageCount       int                      `json:"pageCount"`
	AnalysisPending bool                     `json:"analysisPending"` // true while deferred analysis (link intel, embeddings) is running
	PagesReady      bool                     `json:"pagesReady"`      // false while pages are loading from DB in background
	loadingProgress atomic.Int32                                      // 0-100 progress of background page loading (lock-free)
	loadCancel      context.CancelFunc                                // cancels background page loading goroutine
	mu              sync.RWMutex                                      // protects Pages during deferred analysis
}

// ActiveCrawl tracks a running crawl job.
type ActiveCrawl struct {
	StartedAt   time.Time
	Config      map[string]interface{}
	Cancel      context.CancelFunc
	ID          string
	StatusCodes map[string]int // status code group counts (2xx, 3xx, 4xx, 5xx, err)
	PageCount   int
	ErrorCount  int
	Queued      int // URLs in queue waiting to be crawled
	TotalSeen   int // total unique URLs discovered
	Excluded    int // URLs filtered out by rules
	Paused      bool
	Stopping    bool         // true = stop crawling but still run analysis
	pauseCh     chan struct{} // closed when unpaused; recreated on each pause
	pauseMu     sync.Mutex
}

// WaitIfPaused blocks until the crawl is unpaused or the context is cancelled.
// Returns true if the caller should continue, false if cancelled.
func (a *ActiveCrawl) WaitIfPaused(ctx context.Context) bool {
	a.pauseMu.Lock()
	ch := a.pauseCh
	a.pauseMu.Unlock()
	if ch == nil {
		return true
	}
	select {
	case <-ch:
		return true
	case <-ctx.Done():
		return false
	}
}

// ProgressCallback broadcasts progress events to WebSocket clients.
type ProgressCallback func(event ProgressEvent)

const maxCachedResults = 5

// CrawlManager manages the crawl lifecycle (single active crawl model).
type CrawlManager struct {
	store       *UiStore
	active      *ActiveCrawl
	results     map[string]*CrawlResults
	schedulers  map[string]*scheduler.Scheduler
	onProgress  ProgressCallback
	resultOrder []string
	logBotIPs   map[string]map[string]map[string]struct{} // jobID -> bot -> IPs
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.RWMutex
}

// NewCrawlManager creates a new crawl manager.
func NewCrawlManager(store *UiStore) *CrawlManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &CrawlManager{
		store:      store,
		results:    make(map[string]*CrawlResults),
		schedulers: make(map[string]*scheduler.Scheduler),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Close cancels background goroutines and releases resources.
func (m *CrawlManager) Close() {
	m.cancel()
}

// TrackScheduler registers a running scheduler for lifecycle management.
func (m *CrawlManager) TrackScheduler(s *scheduler.Scheduler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := s.GetState()
	m.schedulers[state.ID] = s
}

// StopScheduler stops and removes a tracked scheduler.
func (m *CrawlManager) StopScheduler(id string) {
	m.mu.Lock()
	s, ok := m.schedulers[id]
	if ok {
		delete(m.schedulers, id)
	}
	m.mu.Unlock()
	if ok {
		s.Stop()
	}
}

// StopAllSchedulers stops all running schedulers (called on server shutdown).
func (m *CrawlManager) StopAllSchedulers() {
	m.mu.Lock()
	all := make([]*scheduler.Scheduler, 0, len(m.schedulers))
	for _, s := range m.schedulers {
		all = append(all, s)
	}
	m.schedulers = make(map[string]*scheduler.Scheduler)
	m.mu.Unlock()
	for _, s := range all {
		s.Stop()
	}
}

// IsSchedulerActive checks if a scheduler is running for the given ID.
func (m *CrawlManager) IsSchedulerActive(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.schedulers[id]
	return ok
}

// getAlertNotifyConfig reads webhook/Slack URLs from settings in a single DB call.
func (m *CrawlManager) getAlertNotifyConfig() alerts.NotifyConfig {
	settings, err := m.store.GetSettings()
	if err != nil {
		return alerts.NotifyConfig{}
	}
	cfg := alerts.NotifyConfig{}
	if v, ok := settings["alertWebhookUrl"].(string); ok {
		cfg.WebhookURL = v
	}
	if v, ok := settings["alertSlackUrl"].(string); ok {
		cfg.SlackURL = v
	}
	return cfg
}

// SetProgressCallback sets the function called for each progress event.
func (m *CrawlManager) SetProgressCallback(cb ProgressCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onProgress = cb
}

// StartCrawl begins a new crawl. Returns error if one is already running.
func (m *CrawlManager) StartCrawl(crawlID string, config map[string]interface{}) error {
	m.mu.Lock()
	if m.active != nil {
		activeID := m.active.ID
		m.mu.Unlock()
		return fmt.Errorf("crawl already running: %s", activeID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.active = &ActiveCrawl{
		ID:          crawlID,
		Config:      config,
		StartedAt:   time.Now(),
		Cancel:      cancel,
		StatusCodes: make(map[string]int),
	}
	m.mu.Unlock()

	if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{"status": "running"}); err != nil {
		log.Printf("store: update crawl %s status: %v", crawlID, err)
	}
	// Add "started" event (only for new crawls — resumes add their own event)
	if _, isResume := config["resume"]; !isResume {
		m.store.AddCrawlEvent(crawlID, "started", "")
	}

	go m.runCrawl(ctx, crawlID, config)
	return nil
}

// PauseCrawl pauses the active crawl.
func (m *CrawlManager) PauseCrawl(crawlID string) error {
	m.mu.Lock()
	if m.active == nil || m.active.ID != crawlID {
		m.mu.Unlock()
		return fmt.Errorf("no active crawl with id %s", crawlID)
	}
	m.active.pauseMu.Lock()
	m.active.Paused = true
	m.active.pauseCh = make(chan struct{})
	m.active.pauseMu.Unlock()
	m.mu.Unlock()

	if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{"status": "paused"}); err != nil {
		log.Printf("store: update crawl %s status: %v", crawlID, err)
	}
	m.store.AddCrawlEvent(crawlID, "paused", fmt.Sprintf("%d pages", m.active.PageCount))
	m.emit(ProgressEvent{CrawlID: crawlID, Type: "paused", Data: map[string]interface{}{"crawlId": crawlID}})
	return nil
}

// ResumeCrawl resumes a paused crawl.
func (m *CrawlManager) ResumeCrawl(crawlID string) error {
	m.mu.Lock()
	if m.active == nil || m.active.ID != crawlID {
		m.mu.Unlock()
		return fmt.Errorf("no active crawl with id %s", crawlID)
	}
	m.active.pauseMu.Lock()
	m.active.Paused = false
	if m.active.pauseCh != nil {
		close(m.active.pauseCh)
		m.active.pauseCh = nil
	}
	m.active.pauseMu.Unlock()
	m.mu.Unlock()

	if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{"status": "running"}); err != nil {
		log.Printf("store: update crawl %s status: %v", crawlID, err)
	}
	m.emit(ProgressEvent{CrawlID: crawlID, Type: "resumed", Data: map[string]interface{}{"crawlId": crawlID}})
	return nil
}

// RestartCrawl resumes a crawl that was interrupted (e.g. by app restart).
// It loads the original config, sets resume=true, and starts a new crawl job
// that reuses the existing crawl DB to skip already-crawled URLs.
// If configOverride is provided, it replaces the stored config (allowing
// the user to change settings before resuming).
func (m *CrawlManager) RestartCrawl(oldJobID string, configOverride map[string]interface{}) (string, error) {
	oldJob, err := m.store.GetCrawlJob(oldJobID)
	if err != nil {
		return "", fmt.Errorf("get old crawl: %w", err)
	}
	if oldJob == nil {
		return "", fmt.Errorf("crawl %s not found", oldJobID)
	}
	if oldJob.Status == "running" {
		return "", fmt.Errorf("crawl %s is still running", oldJobID)
	}
	if oldJob.DBPath == "" {
		return "", fmt.Errorf("crawl %s has no database — cannot resume", oldJobID)
	}
	// Check that the DB file exists
	if _, statErr := os.Stat(oldJob.DBPath); statErr != nil {
		return "", fmt.Errorf("crawl database not found: %s", oldJob.DBPath)
	}

	// Use override config if provided, otherwise use the stored config
	config := oldJob.Config
	if configOverride != nil && len(configOverride) > 0 {
		config = configOverride
	}
	config["resume"] = true
	config["dbPath"] = oldJob.DBPath

	// Clean up any duplicate jobs sharing the same DB (from previous resume attempts)
	if _, cleanErr := m.store.DeleteCrawlJobsByDBPath(oldJobID, oldJob.DBPath); cleanErr != nil {
		log.Printf("store: clean up duplicate jobs for %s: %v", oldJobID, cleanErr)
	}

	// Reuse the existing job — update its config and status, add "resumed" event
	if err := m.store.UpdateCrawlJob(oldJobID, map[string]interface{}{
		"status":       "running",
		"config":       config,
		"completed_at": nil,
		"duration_ms":  nil,
	}); err != nil {
		return "", fmt.Errorf("update job for resume: %w", err)
	}
	m.store.AddCrawlEvent(oldJobID, "resumed", fmt.Sprintf("from %d pages", oldJob.PageCount))

	if err := m.StartCrawl(oldJobID, config); err != nil {
		// Rollback status
		m.store.UpdateCrawlJob(oldJobID, map[string]interface{}{"status": "failed"})
		return "", fmt.Errorf("start resumed crawl: %w", err)
	}

	return oldJobID, nil
}

// CancelCrawl cancels the active crawl.
func (m *CrawlManager) CancelCrawl(crawlID string) error {
	m.mu.Lock()
	if m.active == nil || m.active.ID != crawlID {
		m.mu.Unlock()
		return fmt.Errorf("no active crawl with id %s", crawlID)
	}
	active := m.active
	m.active = nil
	m.mu.Unlock()

	// Unblock any paused state before cancelling
	active.pauseMu.Lock()
	if active.pauseCh != nil {
		close(active.pauseCh)
		active.pauseCh = nil
	}
	active.pauseMu.Unlock()

	active.Cancel()
	elapsed := time.Since(active.StartedAt).Milliseconds()
	if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{
		"status": "cancelled", "durationMs": elapsed,
	}); err != nil {
		log.Printf("store: update crawl %s status: %v", crawlID, err)
	}
	m.store.AddCrawlEvent(crawlID, "cancelled", fmt.Sprintf("%d pages", active.PageCount))
	m.emit(ProgressEvent{CrawlID: crawlID, Type: "cancelled", Data: map[string]interface{}{"crawlId": crawlID}})
	return nil
}

// StopCrawl gracefully stops the active crawl and runs post-crawl analysis
// on whatever pages have been crawled so far.
func (m *CrawlManager) StopCrawl(crawlID string) error {
	m.mu.Lock()
	if m.active == nil || m.active.ID != crawlID {
		m.mu.Unlock()
		return fmt.Errorf("no active crawl with id %s", crawlID)
	}
	m.active.Stopping = true
	active := m.active
	// Don't clear m.active — runCrawl will handle completion
	m.mu.Unlock()

	// Unblock any paused state before stopping
	active.pauseMu.Lock()
	if active.pauseCh != nil {
		close(active.pauseCh)
		active.pauseCh = nil
	}
	active.pauseMu.Unlock()

	active.Cancel()
	m.emit(ProgressEvent{CrawlID: crawlID, Type: "stopping", Data: map[string]interface{}{"crawlId": crawlID}})
	return nil
}

// GetStatus returns the active crawl status or nil.
func (m *CrawlManager) GetStatus() *ActiveCrawl {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// GetActiveStatusCodes returns a copy of status code counts for the given crawl ID,
// or nil if the crawl is not active.
func (m *CrawlManager) GetActiveStatusCodes(crawlID string) map[string]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil || m.active.ID != crawlID {
		return nil
	}
	codes := make(map[string]int, len(m.active.StatusCodes))
	for k, v := range m.active.StatusCodes {
		codes[k] = v
	}
	return codes
}

func (m *CrawlManager) runCrawl(ctx context.Context, crawlID string, config map[string]interface{}) {
	clearActive := func() {
		m.mu.Lock()
		if m.active != nil && m.active.ID == crawlID {
			m.active = nil
		}
		m.mu.Unlock()
	}
	defer clearActive()

	startTime := time.Now()

	// Convert map config to CrawlConfig
	crawlConfig := configFromMap(config)

	// Memory optimization: use SQLite crawl DB so the crawler keeps only
	// lightweight LinkEntry structs in memory instead of full PageData.
	// Pages persist in the crawl DB for lazy loading after analysis.
	job, _ := m.store.GetCrawlJob(crawlID)
	var crawlStore *storage.CrawlStore
	if job != nil && job.DBPath != "" {
		var storeErr error
		crawlStore, storeErr = storage.NewCrawlStore(job.DBPath)
		if storeErr != nil {
			log.Printf("crawl %s: failed to create crawl store: %v — falling back to temp JSONL", crawlID, storeErr)
		}
	}
	if crawlStore != nil {
		defer crawlStore.Close()
	}

	// Fallback to temp JSONL if crawl store unavailable
	if crawlStore == nil && crawlConfig.OutputPath == "" {
		tmpFile, tmpErr := os.CreateTemp("", "micelio-crawl-*.jsonl")
		if tmpErr != nil {
			log.Printf("crawl %s: failed to create temp file: %v — falling back to in-memory", crawlID, tmpErr)
		} else {
			crawlConfig.OutputPath = tmpFile.Name()
			crawlConfig.OutputFormat = types.FormatJSONL
			tmpFile.Close() // writer will reopen it
			defer os.Remove(crawlConfig.OutputPath)
		}
	}

	maxPages := crawlConfig.MaxPages

	// Capture active crawl reference for pause support
	m.mu.RLock()
	activeCrawl := m.active
	m.mu.RUnlock()

	var pauseFn crawler.PauseFunc
	if activeCrawl != nil {
		pauseFn = activeCrawl.WaitIfPaused
	}

	// Run crawler (pass crawl store if available for SQLite persistence)
	var storeArg []crawler.CrawlStore
	if crawlStore != nil {
		storeArg = []crawler.CrawlStore{crawlStore}
	}
	result, err := crawler.CrawlWithOptions(ctx, crawlConfig, func(p crawler.CrawlProgress) {
		completed := p.Crawled - p.Errors
		failed := p.Errors
		pending := maxPages - p.Crawled
		if pending < 0 {
			pending = p.Queued
		}
		elapsed := time.Since(startTime).Milliseconds()

		// Update active crawl counters
		m.mu.Lock()
		if m.active != nil && m.active.ID == crawlID {
			m.active.PageCount = p.Crawled
			m.active.ErrorCount = p.Errors
			m.active.Queued = p.Queued
			m.active.TotalSeen = p.TotalSeen
			m.active.Excluded = p.Excluded
			// Accumulate status code group
			sc := p.StatusCode
			var group string
			switch {
			case sc >= 500:
				group = "5xx"
			case sc >= 400:
				group = "4xx"
			case sc >= 300:
				group = "3xx"
			case sc >= 200:
				group = "2xx"
			default:
				group = "err"
			}
			m.active.StatusCodes[group]++
		}
		m.mu.Unlock()

		// Update DB periodically (every 10 pages)
		if p.Crawled%10 == 0 {
			if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{
				"pageCount": p.Crawled, "errorCount": p.Errors,
			}); err != nil {
				log.Printf("[manager] warning: failed to update crawl progress: %v", err)
			}
		}

		// Aggregate progress event
		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: "progress",
			Data: map[string]interface{}{
				"completed": completed,
				"failed":    failed,
				"pending":   pending,
				"total":     maxPages,
				"elapsedMs": elapsed,
				"crawled":   p.Crawled,
				"queued":    p.Queued,
				"totalSeen": p.TotalSeen,
				"excluded":      p.Excluded,
				"excludedStats": p.ExcludedStats,
				"rate":          p.Rate,
			},
		})

		// Per-page event
		pageType := "page"
		if p.PageError != "" || p.StatusCode >= 400 {
			pageType = "error"
		}
		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: pageType,
			Data: map[string]interface{}{
				"url":            p.CurrentURL,
				"statusCode":     p.StatusCode,
				"responseTimeMs": p.ResponseTimeMs,
				"error":          p.PageError,
			},
		})
	}, storeArg, pauseFn, func(phase string, loaded, total int) {
		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: "progress",
			Data: map[string]interface{}{
				"phase":   phase,
				"loaded":  loaded,
				"total":   total,
				"crawled": 0,
			},
		})
	})

	elapsed := time.Since(startTime).Milliseconds()
	now := time.Now().UTC().Format(time.RFC3339)

	// Check if the crawl was stopped (graceful) or cancelled (hard).
	// StopCrawl sets Stopping=true — we still run analysis on partial results.
	// CancelCrawl clears m.active — bail out entirely.
	m.mu.RLock()
	stopping := m.active != nil && m.active.Stopping
	m.mu.RUnlock()

	if ctx.Err() == context.Canceled && !stopping {
		log.Printf("crawl %s cancelled by user", crawlID)
		return
	}
	if stopping {
		log.Printf("crawl %s stopped by user — running analysis on partial results", crawlID)
	}

	var pages []*types.PageData
	var pageCount int
	// When stopping, treat partial results as success for analysis purposes
	success := (err == nil || stopping) && result != nil
	if err != nil && !stopping {
		log.Printf("crawl %s error: %v", crawlID, err)
		if err2 := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{
			"status": "failed", "completedAt": now, "durationMs": elapsed,
		}); err2 != nil {
			log.Printf("store: update crawl %s failure: %v", crawlID, err2)
		}
		m.store.AddCrawlEvent(crawlID, "failed", err.Error())
	}
	if success {
		pages = result.Pages
		pageCount = len(pages)
		if err2 := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{
			"status": "completed", "completedAt": now,
			"pageCount": pageCount, "durationMs": elapsed,
		}); err2 != nil {
			log.Printf("store: update crawl %s completion: %v", crawlID, err2)
		}
		m.store.AddCrawlEvent(crawlID, "completed", fmt.Sprintf("%d pages", pageCount))
	}

	// Cache results for successful crawls and graceful stops
	if success {
		// Run core analysis (fast: reporter + n-grams + field stripping)
		postCfg := analysis.PostProcessConfigFromCrawl(crawlConfig)
		postCfg.CrawlDurationMs = elapsed
		postCfg.NgramAnalyzer = result.NgramAnalyzer
		postCfg.InternalLinksIter = result.InternalLinksIter
		if len(result.DiscoveredSitemapURLs) > 0 {
			postCfg.SitemapEntries = result.DiscoveredSitemapURLs
		}
		intLinksClose := result.InternalLinksClose
		// Compute page weight before releasing CrawlResult
		if crawlConfig.PageWeight && len(result.ResourceRefs) > 0 {
			analysis.ComputePageWeight(pages, result.ResourceRefs, result.TransferSizes, crawlConfig.Concurrency, crawlConfig.UserAgent)
		} else if len(result.ResourceRefs) > 0 {
			// Even without full page weight mode, populate from available data
			analysis.AssignPageWeightFromRefs(pages, result.ResourceRefs, result.TransferSizes)
		}
		// Release CrawlResult (external links, resource refs, transfer sizes)
		// before post-crawl analysis to reduce peak RSS overlap.
		result = nil
		// When stopping, the original context is cancelled — use a fresh one for analysis.
		analysisCtx := ctx
		if stopping {
			analysisCtx = m.ctx
		}
		crawlStats := analysis.RunCoreAnalysis(analysisCtx, pages, postCfg)

		// Assign PageRank scores to individual pages for the /pages API
		assignPageRankScores(pages, crawlStats.PageRankScores)

		// Evaluate alert rules against crawl stats
		crawlStats.AlertSummary = alerts.Evaluate(&crawlStats)
		if crawlStats.AlertSummary != nil {
			log.Printf("[alerts] %d alerts triggered (%d critical, %d warnings)",
				len(crawlStats.AlertSummary.Alerts), crawlStats.AlertSummary.Critical, crawlStats.AlertSummary.Warnings)
			// Send notifications if configured
			notifyCfg := m.getAlertNotifyConfig()
			if notifyCfg.WebhookURL != "" || notifyCfg.SlackURL != "" {
				go alerts.Notify(notifyCfg, crawlStats.AlertSummary, crawlConfig.SeedURL)
			}
		}

		// Convert CrawlStats to map for JSON serialization
		statsData, _ := json.Marshal(crawlStats)
		var statsMap map[string]interface{}
		json.Unmarshal(statsData, &statsMap)

		// Persist stats to UI DB (survives server restarts)
		if persistErr := m.store.SaveCrawlStats(crawlID, statsData); persistErr != nil {
			log.Printf("store: persist stats for crawl %s: %v", crawlID, persistErr)
		}

		// Determine DB path for lazy page loading
		dbPath := ""
		if job != nil {
			dbPath = job.DBPath
		}

		// Check if deferred analysis (link intelligence, embeddings) is needed
		needsDeferred := crawlConfig.LinkIntelligence || (crawlConfig.Embeddings && crawlConfig.EmbeddingKey != "")

		// Pre-compute aggregations before stripping (Anchors, InternalLinks, etc.)
		anchorStats := buildAnchorStats(pages)
		dirTree := buildDirectoryTree(pages)
		tmplTypes := collectTemplateTypes(pages)
		inlinkIdx := buildInlinkIndex(pages)
		assignInlinksFromIndex(pages, inlinkIdx)

		// Build page_index for instant merge lookups (before stripping)
		if crawlStore != nil {
			if idxErr := crawlStore.BuildPageIndex(pages); idxErr != nil {
				log.Printf("crawl %s: build page index: %v", crawlID, idxErr)
			} else {
				fmt.Fprintf(os.Stderr, "  [page-index] Built index with %d entries for instant merge\n", len(pages))
			}
		}

		// Don't strip pages yet if deferred analysis needs them
		if !needsDeferred {
			stripPagesForCache(pages)
		}

		newResult := &CrawlResults{
			Pages:           pages,
			Stats:           statsMap,
			AnchorStats:     anchorStats,
			DirTree:         dirTree,
			TemplateTypes:   tmplTypes,
			InlinkIndex:     inlinkIdx,
			DBPath:          dbPath,
			PageCount:       pageCount,
			AnalysisPending: needsDeferred,
			PagesReady:      true,
		}
		newResult.loadingProgress.Store(100)

		m.mu.Lock()
		// Cancel old result's background loading if present (e.g. from GetQuickStats)
		if old := m.results[crawlID]; old != nil && old.loadCancel != nil {
			old.loadCancel()
		}
		m.results[crawlID] = newResult
		m.resultOrder = append(m.resultOrder, crawlID)
		for len(m.resultOrder) > maxCachedResults {
			m.evictOldestResult()
		}
		m.mu.Unlock()

		// Release core analysis memory back to OS
		debug.FreeOSMemory()

		// Clear active before emitting complete so listeners see no active crawl.
		clearActive()

		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: "complete",
			Data: map[string]interface{}{
				"crawlId":         crawlID,
				"totalPages":      pageCount,
				"durationMs":      elapsed,
				"analysisPending": needsDeferred,
			},
		})

		// Launch deferred analysis (link intelligence, embeddings) in background.
		// Pass crawlStats directly to avoid JSON round-trip precision loss.
		if needsDeferred {
			go func() {
				defer func() {
					if intLinksClose != nil {
						intLinksClose()
					}
				}()
				m.runDeferredAnalysis(crawlID, pages, postCfg, crawlStats)
			}()
		} else if intLinksClose != nil {
			intLinksClose()
		}

		return
	}

	// Clear active before emitting complete so listeners see no active crawl.
	clearActive()

	m.emit(ProgressEvent{
		CrawlID: crawlID, Type: "complete",
		Data: map[string]interface{}{"crawlId": crawlID, "totalPages": pageCount, "durationMs": elapsed},
	})
}

// runDeferredAnalysis runs link intelligence and embeddings in the background.
// Updates cached results and persists stats when complete.
// Receives crawlStats directly from RunCoreAnalysis to avoid JSON round-trip.
func (m *CrawlManager) runDeferredAnalysis(crawlID string, pages []*types.PageData, cfg analysis.PostProcessConfig, crawlStats types.CrawlStats) {
	// Panic recovery: if analysis panics, clear pending flag so UI doesn't hang.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("deferred analysis panic for crawl %s: %v", crawlID, r)
			m.mu.Lock()
			if res, ok := m.results[crawlID]; ok {
				res.AnalysisPending = false
			}
			m.mu.Unlock()
			stripPagesForCache(pages)
			m.emit(ProgressEvent{
				CrawlID: crawlID, Type: "analysis_complete",
				Data: map[string]interface{}{"crawlId": crawlID, "error": fmt.Sprintf("%v", r)},
			})
		}
	}()

	m.emit(ProgressEvent{
		CrawlID: crawlID, Type: "analysis_progress",
		Data: map[string]interface{}{"crawlId": crawlID, "phase": "link_intelligence"},
	})

	// Set LI phase progress callback for granular WebSocket updates
	cfg.OnLIPhase = func(phase string) {
		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: "analysis_progress",
			Data: map[string]interface{}{"crawlId": crawlID, "phase": "li_" + phase},
		})
	}

	// Persist stats after each LI phase so completed phases survive a crash.
	cfg.OnPersist = func() {
		data, err := json.Marshal(crawlStats)
		if err != nil {
			log.Printf("store: marshal LI stats for crawl %s: %v", crawlID, err)
			return
		}
		if err := m.store.SaveCrawlStats(crawlID, data); err != nil {
			log.Printf("store: persist LI phase for crawl %s: %v", crawlID, err)
		}
		var statsMap map[string]interface{}
		if err := json.Unmarshal(data, &statsMap); err != nil {
			return
		}
		m.mu.RLock()
		res := m.results[crawlID]
		m.mu.RUnlock()
		if res != nil {
			res.mu.Lock()
			res.Stats = statsMap
			res.mu.Unlock()
		}
	}

	// Run deferred analysis (link intelligence + embeddings)
	graph := analysis.RunDeferredAnalysis(m.ctx, pages, cfg, &crawlStats)

	// Convert updated stats to map for JSON API
	updatedData, err := json.Marshal(crawlStats)
	if err != nil {
		log.Printf("store: marshal deferred stats for crawl %s: %v", crawlID, err)
		return
	}
	var updatedMap map[string]interface{}
	if err := json.Unmarshal(updatedData, &updatedMap); err != nil {
		log.Printf("store: unmarshal deferred stats for crawl %s: %v", crawlID, err)
	}

	// Persist updated stats to DB
	if persistErr := m.store.SaveCrawlStats(crawlID, updatedData); persistErr != nil {
		log.Printf("store: persist deferred stats for crawl %s: %v", crawlID, persistErr)
	}

	// Lock the result during strip+update to prevent HTTP handlers from
	// reading partially-stripped pages.
	m.mu.RLock()
	res := m.results[crawlID]
	m.mu.RUnlock()
	if res != nil {
		res.mu.Lock()
		stripPagesForCache(pages)
		res.Stats = updatedMap
		res.Graph = graph
		res.AnalysisPending = false
		res.mu.Unlock()
	} else {
		stripPagesForCache(pages)
	}

	// Release deferred analysis memory
	debug.FreeOSMemory()

	m.emit(ProgressEvent{
		CrawlID: crawlID, Type: "analysis_complete",
		Data: map[string]interface{}{"crawlId": crawlID},
	})

	fmt.Fprintf(os.Stderr, "  [deferred] Analysis complete for crawl %s\n", crawlID)
}

// evictOldestResult removes the oldest cached result, cancelling any background loading.
// Must be called with m.mu held.
func (m *CrawlManager) evictOldestResult() {
	if len(m.resultOrder) == 0 {
		return
	}
	oldest := m.resultOrder[0]
	m.resultOrder = m.resultOrder[1:]
	if old := m.results[oldest]; old != nil && old.loadCancel != nil {
		old.loadCancel()
	}
	delete(m.results, oldest)
}

func (m *CrawlManager) emit(event ProgressEvent) {
	m.mu.RLock()
	cb := m.onProgress
	m.mu.RUnlock()
	if cb != nil {
		cb(event)
	}
}

// GetCachedResults returns results only if they're already in-memory cache.
// Returns nil if the result hasn't been loaded yet. Non-blocking.
func (m *CrawlManager) GetCachedResults(crawlID string) *CrawlResults {
	m.mu.RLock()
	r := m.results[crawlID]
	m.mu.RUnlock()
	return r
}

// GetOrLoadResults returns cached results, loading from DB if needed.
// If pages have been evicted from memory but stats exist, reload from crawl DB.
func (m *CrawlManager) GetOrLoadResults(crawlID string) *CrawlResults {
	// Check in-memory cache first
	m.mu.RLock()
	if r, ok := m.results[crawlID]; ok {
		m.mu.RUnlock()
		return r
	}
	m.mu.RUnlock()

	// Try loading from persisted stats + crawl DB
	job, err := m.store.GetCrawlJob(crawlID)
	if err != nil || job == nil || job.Status != "completed" {
		return nil
	}

	statsMap, err := m.store.LoadCrawlStats(crawlID)
	if err != nil || statsMap == nil {
		return nil
	}

	// Load pages from crawl DB
	var pages []*types.PageData
	var anchorStats, dirTree map[string]interface{}
	var tmplTypes []string
	var inlinkIdx map[string][]string
	var graph *analysis.AdjacencyGraph
	if job.DBPath != "" {
		cs, csErr := storage.NewCrawlStore(job.DBPath)
		if csErr == nil {
			var loadErr error
			pages, loadErr = cs.GetAllPages()
			cs.Close()
			if loadErr != nil {
				log.Printf("lazy load pages for crawl %s: %v", crawlID, loadErr)
			}
			// Assign PageRank scores from persisted stats to pages
			assignPageRankFromStatsMap(pages, statsMap)
			// Pre-compute aggregations before stripping (InternalLinks still available)
			anchorStats = buildAnchorStats(pages)
			dirTree = buildDirectoryTree(pages)
			tmplTypes = collectTemplateTypes(pages)
			inlinkIdx = buildInlinkIndex(pages)
			assignInlinksFromIndex(pages, inlinkIdx)
			stripPagesForCache(pages)
			// Build adjacency graph from DB (pages' InternalLinks were already
			// stripped by GetAllPages, so we read edges directly from the DB).
			cs2, cs2Err := storage.NewCrawlStore(job.DBPath)
			if cs2Err == nil {
				graph = analysis.BuildAdjacencyListFromDisk(pages, func(fn func(source string, targets []string)) error {
					return cs2.IterateInternalLinks(fn)
				})
				cs2.Close()
			}
			// Backfill: recompute orphans from graph inDegree for old crawls
			// that used the reporter's raw-URL matching (pre-graph methodology).
			if graph != nil {
				methodology, _ := statsMap["orphanMethodology"].(string)
				if methodology != "graph" {
					orphans := analysis.ComputeGraphOrphans(graph, job.SeedURL)
					var oldCount int
					if old, ok := statsMap["orphanPages"].([]interface{}); ok {
						oldCount = len(old)
					}
					statsMap["orphanPages"] = orphans
					statsMap["orphanMethodology"] = "graph"
					log.Printf("backfill: recomputed orphans for crawl %s (%d → %d, methodology: graph)",
						crawlID, oldCount, len(orphans))
					// Persist updated stats
					if updated, err := json.Marshal(statsMap); err == nil {
						if persistErr := m.store.SaveCrawlStats(crawlID, updated); persistErr != nil {
							log.Printf("backfill: persist orphan stats for crawl %s: %v", crawlID, persistErr)
						}
					}
				}
			}
		} else {
			log.Printf("lazy load: open crawl store for %s: %v", crawlID, csErr)
		}
	}

	result := &CrawlResults{
		Stats:       statsMap,
		Pages:       pages,
		Graph:       graph,
		AnchorStats: anchorStats,
		DirTree:     dirTree,
		TemplateTypes: tmplTypes,
		InlinkIndex: inlinkIdx,
		DBPath:      job.DBPath,
		PageCount:   job.PageCount,
		PagesReady:  true,
	}
	result.loadingProgress.Store(100)

	// Cache for future requests (double-check under write lock to avoid TOCTOU race)
	m.mu.Lock()
	if existing, ok := m.results[crawlID]; ok {
		m.mu.Unlock()
		return existing
	}
	m.results[crawlID] = result
	m.resultOrder = append(m.resultOrder, crawlID)
	for len(m.resultOrder) > maxCachedResults {
		m.evictOldestResult()
	}
	m.mu.Unlock()

	return result
}

// GetQuickStats returns cached or persisted stats without loading pages.
// If pages aren't in memory, starts a background goroutine to load them.
// This makes the /stats endpoint return instantly even for 100K+ page crawls.
func (m *CrawlManager) GetQuickStats(crawlID string) *CrawlResults {
	// Check in-memory cache first
	m.mu.RLock()
	if r, ok := m.results[crawlID]; ok {
		m.mu.RUnlock()
		return r
	}
	m.mu.RUnlock()

	// Load only stats from persisted DB (fast, <50ms)
	job, err := m.store.GetCrawlJob(crawlID)
	if err != nil || job == nil || job.Status != "completed" {
		return nil
	}

	statsMap, err := m.store.LoadCrawlStats(crawlID)
	if err != nil || statsMap == nil {
		return nil
	}

	loadCtx, loadCancel := context.WithCancel(context.Background())
	result := &CrawlResults{
		Stats:      statsMap,
		DBPath:     job.DBPath,
		PageCount:  job.PageCount,
		loadCancel: loadCancel,
		// PagesReady defaults to false — pages not loaded yet
	}

	// Cache result (double-check under write lock to avoid TOCTOU race)
	m.mu.Lock()
	if existing, ok := m.results[crawlID]; ok {
		m.mu.Unlock()
		loadCancel() // don't leak the context
		return existing
	}
	m.results[crawlID] = result
	m.resultOrder = append(m.resultOrder, crawlID)
	for len(m.resultOrder) > maxCachedResults {
		m.evictOldestResult()
	}
	m.mu.Unlock()

	// Start background page loading with cancellable context
	go m.loadPagesInBackground(loadCtx, crawlID, result, job.SeedURL)

	return result
}

// loadPagesInBackground loads pages from the crawl DB in a background goroutine,
// updating loadingProgress atomically as pages are decompressed. When complete,
// it fills in the pre-computed aggregations (anchors, dir tree, etc.) and sets PagesReady=true.
// The context is cancelled when the result is evicted from the LRU cache.
func (m *CrawlManager) loadPagesInBackground(ctx context.Context, crawlID string, result *CrawlResults, seedURL string) {
	if result.DBPath == "" {
		result.mu.Lock()
		result.PagesReady = true
		result.mu.Unlock()
		result.loadingProgress.Store(100)
		return
	}

	cs, csErr := storage.NewCrawlStore(result.DBPath)
	if csErr != nil {
		log.Printf("background load: open crawl store for %s: %v", crawlID, csErr)
		result.mu.Lock()
		result.PagesReady = true
		result.mu.Unlock()
		result.loadingProgress.Store(100)
		return
	}

	total := result.PageCount
	pages, loadErr := cs.GetAllPagesWithProgress(ctx, func(loaded int) {
		if total > 0 {
			progress := (loaded * 100) / total
			result.loadingProgress.Store(int32(progress))
		}
	})
	cs.Close()

	// If context was cancelled (LRU eviction), stop early
	if ctx.Err() != nil {
		log.Printf("background page load cancelled for crawl %s", crawlID)
		return
	}

	if loadErr != nil {
		log.Printf("background load pages for crawl %s: %v", crawlID, loadErr)
	}

	// Mark decompression complete (100%) before aggregation phase
	result.loadingProgress.Store(100)

	// Assign PageRank scores from persisted stats to pages
	result.mu.RLock()
	if result.Stats != nil {
		assignPageRankFromStatsMap(pages, result.Stats)
	}
	result.mu.RUnlock()

	// Pre-compute aggregations before stripping
	anchorStats := buildAnchorStats(pages)
	dirTree := buildDirectoryTree(pages)
	tmplTypes := collectTemplateTypes(pages)
	inlinkIdx := buildInlinkIndex(pages)
	assignInlinksFromIndex(pages, inlinkIdx)

	// Build page_index for instant merge if it doesn't exist yet
	if result.DBPath != "" {
		if cs2, err := storage.NewCrawlStore(result.DBPath); err == nil {
			if !cs2.HasPageIndex() {
				if idxErr := cs2.BuildPageIndex(pages); idxErr != nil {
					log.Printf("background load: build page index for %s: %v", crawlID, idxErr)
				} else {
					fmt.Fprintf(os.Stderr, "  [page-index] Built index with %d entries (background)\n", len(pages))
				}
			}
			cs2.Close()
		}
	}

	stripPagesForCache(pages)

	// Build adjacency graph from DB
	var graph *analysis.AdjacencyGraph
	if result.DBPath != "" {
		if csGraph, err := storage.NewCrawlStore(result.DBPath); err == nil {
			graph = analysis.BuildAdjacencyListFromDisk(pages, func(fn func(source string, targets []string)) error {
				return csGraph.IterateInternalLinks(fn)
			})
			csGraph.Close()
		}
	}

	// Backfill orphans from graph
	if graph != nil {
		result.mu.RLock()
		methodology, _ := result.Stats["orphanMethodology"].(string)
		result.mu.RUnlock()

		if methodology != "graph" && seedURL != "" {
			orphans := analysis.ComputeGraphOrphans(graph, seedURL)
			var oldCount int
			result.mu.Lock()
			if old, ok := result.Stats["orphanPages"].([]interface{}); ok {
				oldCount = len(old)
			}
			result.Stats["orphanPages"] = orphans
			result.Stats["orphanMethodology"] = "graph"
			result.mu.Unlock()
			log.Printf("backfill: recomputed orphans for crawl %s (%d → %d, methodology: graph)",
				crawlID, oldCount, len(orphans))
			if updated, err := json.Marshal(result.Stats); err == nil {
				result.mu.RLock()
				if persistErr := m.store.SaveCrawlStats(crawlID, updated); persistErr != nil {
					log.Printf("backfill: persist orphan stats for crawl %s: %v", crawlID, persistErr)
				}
				result.mu.RUnlock()
			}
		}
	}

	result.mu.Lock()
	result.Pages = pages
	result.AnchorStats = anchorStats
	result.DirTree = dirTree
	result.TemplateTypes = tmplTypes
	result.InlinkIndex = inlinkIdx
	result.Graph = graph
	result.PagesReady = true
	result.PageCount = len(pages)
	result.mu.Unlock()

	debug.FreeOSMemory()
	log.Printf("background page load complete for crawl %s: %d pages", crawlID, len(pages))
}

// GetLoadingProgress returns the current page loading state for a crawl.
// Returns found=false when the result is not in the in-memory cache (e.g. after LRU eviction).
func (m *CrawlManager) GetLoadingProgress(crawlID string) (found, pagesReady bool, loadingProgress, pageCount int) {
	m.mu.RLock()
	result, ok := m.results[crawlID]
	m.mu.RUnlock()
	if !ok {
		return false, false, 0, 0
	}
	result.mu.RLock()
	ready := result.PagesReady
	count := result.PageCount
	result.mu.RUnlock()
	return true, ready, int(result.loadingProgress.Load()), count
}

// stripPagesForCache removes fields not needed by the dashboard to reduce memory.
// assignPageRankScores copies PageRank scores from a map to individual pages.
// Used after core analysis (map[string]float64) and when reloading from DB (stats JSON map).
func assignPageRankScores(pages []*types.PageData, scores map[string]float64) {
	if len(scores) == 0 {
		return
	}
	for _, p := range pages {
		if score, ok := scores[p.URL]; ok {
			p.PageRank = score
		}
	}
}

// assignPageRankFromStatsMap extracts pageRankScores from a JSON stats map and assigns to pages.
func assignPageRankFromStatsMap(pages []*types.PageData, statsMap map[string]interface{}) {
	raw, ok := statsMap["pageRankScores"]
	if !ok {
		return
	}
	scoresMap, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	for _, p := range pages {
		if v, ok := scoresMap[p.URL]; ok {
			if f, ok := v.(float64); ok {
				p.PageRank = f
			}
		}
	}
}

// assignInlinksFromIndex sets Inlinks count on each page from the inlink index.
func assignInlinksFromIndex(pages []*types.PageData, inlinkIdx map[string][]string) {
	if len(inlinkIdx) == 0 {
		return
	}
	for _, p := range pages {
		if sources, ok := inlinkIdx[p.URL]; ok {
			p.Inlinks = len(sources)
		}
	}
}

func stripPagesForCache(pages []*types.PageData) {
	for _, p := range pages {
		p.Anchors = nil
		p.BodyText = ""
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
		p.ContentHash = ""
		p.SimhashFingerprint = ""
		if p.PageWeight != nil {
			p.PageWeight.Resources = nil // keep summary (TotalBytes, HTMLBytes, ByType)
		}
		p.Pagespeed = nil
		p.CruxData = nil
		p.GscData = nil
		p.Ga4Data = nil
		p.PlausibleData = nil
		p.AIAnalysis = nil
		p.Readability = nil
		// Keep LinkIntelligence — graph endpoint reads authority/hub/centrality scores.
	}
}

// configFromMap converts a JSON-like map to CrawlConfig.
func configFromMap(m map[string]interface{}) types.CrawlConfig {
	// Map dashboard field names to CrawlConfig JSON tags
	if v, ok := m["url"]; ok && m["seedUrl"] == nil {
		m["seedUrl"] = v
	}
	if v, ok := m["limit"]; ok && m["maxPages"] == nil {
		m["maxPages"] = v
	}
	if v, ok := m["depth"]; ok && m["maxDepth"] == nil {
		m["maxDepth"] = v
	}

	// Marshal to JSON then unmarshal to struct for reliable type conversion
	data, err := json.Marshal(m)
	if err != nil {
		log.Printf("configFromMap: marshal error: %v", err)
	}
	var config types.CrawlConfig
	if err := json.Unmarshal(data, &config); err != nil {
		log.Printf("configFromMap: unmarshal error: %v", err)
	}

	// Apply defaults
	if config.Concurrency <= 0 {
		config.Concurrency = 5
	}
	if config.MaxPages <= 0 {
		config.MaxPages = 1000
	}
	if config.MaxDepth <= 0 {
		config.MaxDepth = 10
	}
	if config.Mode == "" {
		config.Mode = types.ModeSpider
	}
	return config
}

// runLogAnalysis parses a server log file and stores aggregated stats.
func (m *CrawlManager) runLogAnalysis(jobID, filePath, filename string, formatHint logs.Format) {
	startTime := time.Now()

	// Throttle WS emission: at least one every 1MB of bytes OR 100K lines,
	// whichever arrives first. Keeps the UI lively on huge files without
	// flooding the socket.
	const (
		byteEmitThreshold int64 = 1 << 20
		lineEmitThreshold int64 = 100_000
	)
	var lastBytesEmit, lastLinesEmit int64

	// Clear any prior URL stats for this job.
	_ = m.store.SaveLogURLStats(jobID, nil)

	result, err := logs.AnalyzeFull(filePath, formatHint, func(processed, bytesRead, fileSize int64) {
		if bytesRead-lastBytesEmit < byteEmitThreshold && processed-lastLinesEmit < lineEmitThreshold {
			return
		}
		lastBytesEmit = bytesRead
		lastLinesEmit = processed
		m.store.UpdateLogJob(jobID, map[string]interface{}{
			"processedLines": processed,
		})
		m.emit(ProgressEvent{
			Type: "log_progress",
			Data: map[string]interface{}{
				"jobId":     jobID,
				"processed": processed,
				"bytesRead": bytesRead,
				"fileSize":  fileSize,
				// Legacy key kept for any old client (pre-upgrade UI).
				"total": fileSize,
			},
		})
	}, nil)

	elapsed := time.Since(startTime).Milliseconds()
	now := time.Now().UTC().Format(time.RFC3339)

	if err != nil {
		errMsg := err.Error()
		log.Printf("[logs] analysis failed jobID=%s filename=%s err=%s", jobID, filename, errMsg)
		m.store.UpdateLogJob(jobID, map[string]interface{}{
			"status": "failed", "completedAt": now, "durationMs": elapsed, "errorMsg": errMsg,
		})
		m.emit(ProgressEvent{
			Type: "log_complete",
			Data: map[string]interface{}{"jobId": jobID, "error": errMsg},
		})
		return
	}

	stats := result.Stats
	format := result.Format

	// Store bot IPs for later verification
	m.mu.Lock()
	if m.logBotIPs == nil {
		m.logBotIPs = make(map[string]map[string]map[string]struct{})
	}
	m.logBotIPs[jobID] = result.BotIPs
	m.mu.Unlock()

	// Stream URL stats to per-job SQLite table (no intermediate slice).
	if result.StreamURLs != nil {
		if err := m.store.StreamLogURLStats(jobID, result.StreamURLs); err != nil {
			log.Printf("[logs] URL stats save error: %v", err)
		}
		if len(stats.TopURLs) == 0 {
			if topURLs, err := m.store.QueryTopLogURLs(jobID, 500); err == nil && len(topURLs) > 0 {
				stats.TopURLs = topURLs
			}
		}
	}

	statsJSON, _ := json.Marshal(stats)
	m.store.SaveLogStats(jobID, statsJSON)

	m.store.UpdateLogJob(jobID, map[string]interface{}{
		"status": "completed", "completedAt": now, "durationMs": elapsed,
		"totalLines": stats.TotalLines, "processedLines": stats.TotalLines, "format": string(format),
	})

	m.emit(ProgressEvent{
		Type: "log_complete",
		Data: map[string]interface{}{"jobId": jobID, "totalLines": stats.TotalLines},
	})
}

// runLogAnalysisMulti parses N log files in parallel with a shared aggregator
// and stores the combined stats. Used when the upload handler received more
// than one file; for single-file uploads this still works (N=1) but the
// overhead is negligible.
func (m *CrawlManager) runLogAnalysisMulti(jobID string, filePaths, filenames []string, formatHint logs.Format) {
	startTime := time.Now()

	const (
		byteEmitThreshold int64 = 2 << 20 // 2 MiB across all workers
		lineEmitThreshold int64 = 200_000 // 200K lines
	)
	var lastBytesEmit, lastLinesEmit int64

	// Worker count: cap at GOMAXPROCS, at most one per file.
	workers := runtime.GOMAXPROCS(0)
	if workers > len(filePaths) {
		workers = len(filePaths)
	}
	if workers < 1 {
		workers = 1
	}

	// Pre-seed the per-file metadata now with "parsing" status.
	perFile := make([]LogJobFile, len(filePaths))
	for i := range filePaths {
		sz, _ := os.Stat(filePaths[i])
		size := int64(0)
		if sz != nil {
			size = sz.Size()
		}
		perFile[i] = LogJobFile{
			Filename: filenames[i],
			Size:     size,
			Status:   "parsing",
		}
	}
	m.store.UpdateLogJob(jobID, map[string]interface{}{"files": perFile})

	// Clear any prior URL stats for this job.
	_ = m.store.SaveLogURLStats(jobID, nil)

	// No flusher: keep all URL stats in memory during parsing (GC is disabled
	// so the large map doesn't cause scan overhead). URLs are persisted in a
	// single bulk INSERT after parsing — ~10x faster than incremental UPSERT
	// which was the bottleneck (99% of CPU in profiles was SQLite B-tree lookups).
	parseStart := time.Now()
	result, err := logs.AnalyzeMulti(filePaths, formatHint, workers, func(mp logs.MultiProgress) {
		// Update the per-file slot.
		if mp.FileIndex >= 0 && mp.FileIndex < len(perFile) {
			perFile[mp.FileIndex].BytesRead = mp.BytesRead
			perFile[mp.FileIndex].Lines = mp.Processed
		}
		// Throttle WS emissions.
		if mp.TotalBytes-lastBytesEmit < byteEmitThreshold &&
			mp.TotalLines-lastLinesEmit < lineEmitThreshold {
			return
		}
		lastBytesEmit = mp.TotalBytes
		lastLinesEmit = mp.TotalLines
		m.store.UpdateLogJob(jobID, map[string]interface{}{
			"processedLines": mp.TotalLines,
			"files":          perFile,
		})
		m.emit(ProgressEvent{
			Type: "log_progress",
			Data: map[string]interface{}{
				"jobId":      jobID,
				"processed":  mp.TotalLines,
				"bytesRead":  mp.TotalBytes,
				"fileSize":   mp.TotalSize,
				"total":      mp.TotalSize,
				"filesDone":  mp.FilesDone,
				"filesTotal": mp.FilesTotal,
				"currentFile": map[string]interface{}{
					"index":     mp.FileIndex,
					"filename":  mp.Filename,
					"bytesRead": mp.BytesRead,
					"fileSize":  mp.FileSize,
					"lines":     mp.Processed,
				},
			},
		})
	}, nil)

	parseMs := time.Since(parseStart).Milliseconds()
	analysisStart := time.Now()

	elapsed := time.Since(startTime).Milliseconds()
	now := time.Now().UTC().Format(time.RFC3339)

	// Finalize per-file metadata from the result.
	if result != nil {
		for i, fr := range result.Files {
			if i >= len(perFile) {
				break
			}
			perFile[i].Lines = fr.Lines
			perFile[i].BytesRead = perFile[i].Size
			if fr.Error != "" {
				perFile[i].Status = "failed"
				perFile[i].Error = fr.Error
			} else {
				perFile[i].Status = "completed"
			}
		}
	}

	if err != nil {
		errMsg := err.Error()
		log.Printf("[logs] multi analysis failed jobID=%s files=%d err=%s", jobID, len(filePaths), errMsg)
		m.store.UpdateLogJob(jobID, map[string]interface{}{
			"status": "failed", "completedAt": now, "durationMs": elapsed,
			"parseMs": parseMs, "analysisMs": time.Since(analysisStart).Milliseconds(),
			"errorMsg": errMsg, "files": perFile,
		})
		m.emit(ProgressEvent{
			Type: "log_complete",
			Data: map[string]interface{}{"jobId": jobID, "error": errMsg},
		})
		return
	}

	stats := result.Stats
	format := result.Format

	// Store bot IPs for later verification.
	m.mu.Lock()
	if m.logBotIPs == nil {
		m.logBotIPs = make(map[string]map[string]map[string]struct{})
	}
	m.logBotIPs[jobID] = result.BotIPs
	m.mu.Unlock()

	// Stream URL stats to per-job SQLite table (no intermediate slice — saves ~2.5GB).
	if result.StreamURLs != nil {
		if err := m.store.StreamLogURLStats(jobID, result.StreamURLs); err != nil {
			log.Printf("[logs] URL stats save error: %v", err)
		}
		if len(stats.TopURLs) == 0 {
			if topURLs, err := m.store.QueryTopLogURLs(jobID, 500); err == nil && len(topURLs) > 0 {
				stats.TopURLs = topURLs
			}
		}
	}

	statsJSON, _ := json.Marshal(stats)
	m.store.SaveLogStats(jobID, statsJSON)

	analysisMs := time.Since(analysisStart).Milliseconds()
	m.store.UpdateLogJob(jobID, map[string]interface{}{
		"status": "completed", "completedAt": now, "durationMs": elapsed,
		"parseMs": parseMs, "analysisMs": analysisMs,
		"totalLines": stats.TotalLines, "processedLines": stats.TotalLines, "format": string(format),
		"files": perFile,
	})

	m.emit(ProgressEvent{
		Type: "log_complete",
		Data: map[string]interface{}{"jobId": jobID, "totalLines": stats.TotalLines, "filesTotal": len(filePaths)},
	})
}

// runBotVerification performs FCrDNS verification for a log analysis job.
func (m *CrawlManager) runBotVerification(jobID string) (map[string]*logs.VerificationStats, []*logs.VerificationResult, error) {
	m.mu.RLock()
	botIPs := m.logBotIPs[jobID]
	m.mu.RUnlock()

	if botIPs == nil {
		return nil, nil, fmt.Errorf("no bot IP data for job %s (data available only in current session)", jobID)
	}

	verifier := logs.NewBotVerifier()
	stats, spoofed := verifier.VerifyBotIPs(botIPs)
	return stats, spoofed, nil
}
