package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/micelio/micelio/internal/alerts"
	"github.com/micelio/micelio/internal/analysis"
	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/gctune"
	"github.com/micelio/micelio/internal/logs"
	"github.com/micelio/micelio/internal/scheduler"
	"github.com/micelio/micelio/internal/secrets"
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
	Pages           []*types.PageData        `json:"pages"` // populated only for the active/recent crawl
	Graph           *analysis.AdjacencyGraph `json:"-"`     // CSR graph for edge rendering after InternalLinks freed
	AnchorStats     map[string]interface{}   `json:"-"`     // pre-computed anchor stats (survives stripPagesForCache)
	DirTree         map[string]interface{}   `json:"-"`     // pre-computed directory tree (survives stripPagesForCache)
	TemplateTypes   []string                 `json:"-"`     // cached distinct template types
	InlinkIndex     *inlinkIndex             `json:"-"`     // targetURL → source URLs, read from the link graph
	DBPath          string                   `json:"-"`     // path to crawl DB for lazy loading
	PageCount       int                      `json:"pageCount"`
	AnalysisPending bool                     `json:"analysisPending"` // true while deferred analysis (link intel, embeddings) is running
	PagesReady      bool                     `json:"pagesReady"`      // false while pages are loading from DB in background
	// linkAnalysis: the deferred analysis runs link intelligence or
	// embeddings, so it goes on after Stop ends the network checks.
	linkAnalysis    bool
	loadingProgress atomic.Int32 // 0-100 progress of background page loading (lock-free)
	// Background external link check: URLs done / total (total 0 = not running).
	externalChecked atomic.Int64
	externalTotal   atomic.Int64
	// Background resource size check (page weight): done / total (total 0 = not running).
	pageWeightDone  atomic.Int64
	pageWeightTotal atomic.Int64
	loadCancel      context.CancelFunc // cancels background page loading goroutine
	mu              sync.RWMutex       // protects Pages during deferred analysis
	views           pageViewCache      // /pages filter+sort results (pages_view.go)
	wordBuckets     wordBucketCache    // legacy word-count histogram (wordcount.go)
}

// ActiveCrawl tracks a running crawl job.
type ActiveCrawl struct {
	// Tuner changes delay, concurrency and the page limit while it runs
	// (nil until the crawl session is set up).
	Tuner       *crawler.Tuner
	stopAt      time.Time // when Stop & Analyze was pressed
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
	// RecentErrors holds the latest failed pages (4xx, 5xx, network
	// errors), so a Monitor opened mid-crawl can list them.
	RecentErrors []CrawlError
	Paused       bool
	Stopping     bool          // true = stop crawling but still run analysis
	finished     bool          // crawl loop returned: no more pauses (under CrawlManager.mu)
	pauseCh      chan struct{} // closed when unpaused; recreated on each pause
	pauseMu      sync.Mutex
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
	store  *UiStore
	active *ActiveCrawl
	// tuneMu serialises TuneCrawl (apply, config copy, save), so two tunes
	// cannot save their configs in the opposite order of their changes.
	tuneMu     sync.Mutex
	results    map[string]*CrawlResults
	schedulers map[string]*scheduler.Scheduler
	// scheduleReserved tracks a schedule being created and any stop/delete received meanwhile.
	scheduleReserved map[string]string
	// schedulersClosed prevents pending creations from starting during shutdown.
	schedulersClosed bool
	onProgress       ProgressCallback
	resultOrder      []string
	resultSize       map[string]int64 // estimated bytes per cached result
	loads            *resultLoads
	logBotIPs        map[string]map[string]map[string]struct{} // jobID -> bot -> IPs
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.RWMutex
	background       sync.WaitGroup // post-crawl work that outlives the "complete" event
	// SkipSSRF lets crawls reach loopback and private addresses. Tests
	// only: no API, flag or setting can turn it on.
	SkipSSRF bool
	// TrustedProxy is the only proxy dashboard and API crawl configs may
	// use (micelio ui --trusted-proxy, MICELIO_TRUSTED_PROXY); "" = none.
	// Set by the local user at start, so the crawler reaches it even on
	// loopback or a private address (Tor, a local forward proxy).
	TrustedProxy string
	// testBeforeCache runs just before a crawl's results are cached.
	testBeforeCache func(crawlID string)
	// analyzing holds crawls marked completed whose results are not cached
	// yet (core analysis running). Guarded by mu.
	analyzing map[string]bool
	// deferred holds the cancel func of each crawl's background analysis
	// (external link check, link intelligence), so deleting or restarting
	// the crawl stops it. Guarded by mu.
	deferred map[string]*deferredRun
	// logQueue bounds concurrent and waiting log analyses (limits.go).
	logQueue logQueue
	// jobHolds counts work using each crawl's job or DB; deleting marks
	// crawls DeleteCrawl is removing (crawl_files.go). Guarded by mu.
	jobHolds map[string]int
	deleting map[string]bool
	// softHolds counts the jobHolds that DeleteCrawl may cancel (deferred
	// analysis) instead of refusing; pendingRemove holds the crawl DB of a
	// deleted crawl until its last hold ends. Guarded by mu.
	softHolds     map[string]int
	pendingRemove map[string]string
}

// NewCrawlManager creates a new crawl manager.
func NewCrawlManager(store *UiStore) *CrawlManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &CrawlManager{
		store:      store,
		results:    make(map[string]*CrawlResults),
		resultSize: make(map[string]int64),
		loads:      newResultLoads(),
		schedulers: make(map[string]*scheduler.Scheduler),
		deferred:   make(map[string]*deferredRun),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Close cancels background goroutines and releases resources.
func (m *CrawlManager) Close() {
	m.cancel()
}

// WaitBackground blocks until running crawls and their post-crawl work
// (deferred analysis, saving the link graph) have finished. Used by tests.
func (m *CrawlManager) WaitBackground() { m.background.Wait() }

// TrackScheduler registers a running scheduler for lifecycle management.
func (m *CrawlManager) TrackScheduler(s *scheduler.Scheduler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := s.GetState()
	m.schedulers[state.ID] = s
}

// StopScheduler stops and removes a tracked scheduler. A schedule still
// being created under this ID (startUISchedule) is not started.
func (m *CrawlManager) StopScheduler(id string) {
	m.stopScheduler(id, reserveStopped)
}

// DeleteScheduler stops a scheduler for a deletion: a schedule still being
// created under this ID is not started and its saved state is removed.
func (m *CrawlManager) DeleteScheduler(id string) {
	m.stopScheduler(id, reserveDeleted)
}

func (m *CrawlManager) stopScheduler(id string, mark string) {
	m.mu.Lock()
	s, ok := m.schedulers[id]
	if ok {
		delete(m.schedulers, id)
	}
	if _, creating := m.scheduleReserved[id]; creating && m.scheduleReserved[id] != reserveDeleted {
		m.scheduleReserved[id] = mark
	}
	m.mu.Unlock()
	if ok {
		s.Stop()
	}
}

// StopAllSchedulers stops all running schedulers (called on server shutdown).
// Schedules still being created are not started afterwards.
func (m *CrawlManager) StopAllSchedulers() {
	m.mu.Lock()
	m.schedulersClosed = true
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
	applyEnvFallbacks(settings)
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
	return m.startCrawl(crawlID, config, nil)
}

// startCrawl takes the single active-crawl slot, then runs prepare (if set)
// before the crawl starts. prepare is where a restart rewrites the job, so a
// refused start (another crawl active) changes nothing. If prepare fails the
// slot is given back and the crawl does not start.
func (m *CrawlManager) startCrawl(crawlID string, config map[string]interface{}, prepare func() error) error {
	m.mu.Lock()
	if m.active != nil {
		activeID := m.active.ID
		m.mu.Unlock()
		return fmt.Errorf("crawl already running: %s", activeID)
	}
	if m.deleting[crawlID] {
		m.mu.Unlock()
		return fmt.Errorf("%w: crawl %s is being deleted", ErrCrawlBusy, crawlID)
	}
	release := m.holdJobLocked(crawlID)

	// A resumed crawl reuses its ID: stop the old run's background analysis
	// so it cannot write over the new run.
	m.cancelDeferredLocked(crawlID)
	ctx, cancel := context.WithCancel(context.Background())
	m.active = &ActiveCrawl{
		ID:          crawlID,
		Config:      config,
		StartedAt:   time.Now(),
		Cancel:      cancel,
		StatusCodes: make(map[string]int),
	}
	reserved := m.active
	m.mu.Unlock()

	if prepare != nil {
		if err := prepare(); err != nil {
			m.mu.Lock()
			if m.active == reserved {
				m.active = nil
			}
			m.mu.Unlock()
			cancel()
			release()
			return err
		}
	}

	// Cancel or stop may have hit the slot while prepare ran (or since the
	// unlock above). Then the crawl must not start: saving "running" would
	// overwrite "cancelled" and runCrawl would run on a dead context. Saved
	// under m.mu, so a later cancel's "cancelled" lands after "running".
	m.mu.Lock()
	if m.active != reserved || ctx.Err() != nil {
		stopped := m.active == reserved // StopCrawl keeps the slot, cancel clears it
		if stopped {
			m.active = nil
			if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{"status": "cancelled"}); err != nil {
				log.Printf("store: update crawl %s status: %v", crawlID, err)
			}
		}
		m.mu.Unlock()
		cancel()
		release()
		if stopped {
			m.store.AddCrawlEvent(crawlID, "cancelled", "stopped before the crawl started")
			m.emit(ProgressEvent{CrawlID: crawlID, Type: "cancelled", Data: map[string]interface{}{"crawlId": crawlID}})
		}
		return fmt.Errorf("crawl %s was cancelled before it started", crawlID)
	}
	if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{"status": "running"}); err != nil {
		log.Printf("store: update crawl %s status: %v", crawlID, err)
	}
	m.mu.Unlock()
	// Add "started" event (only for new crawls — resumes add their own event)
	if _, isResume := config["resume"]; !isResume {
		m.store.AddCrawlEvent(crawlID, "started", "")
	}

	m.background.Add(1)
	go func() {
		defer m.background.Done()
		defer release()
		m.runCrawl(ctx, crawlID, config)
	}()
	return nil
}

// PauseCrawl pauses the active crawl.
func (m *CrawlManager) PauseCrawl(crawlID string) error {
	m.mu.Lock()
	if m.active == nil || m.active.ID != crawlID || m.active.finished {
		m.mu.Unlock()
		return fmt.Errorf("no active crawl with id %s", crawlID)
	}
	active := m.active
	active.pauseMu.Lock()
	if active.pauseCh != nil {
		// Already paused. A new channel would strand the workers waiting
		// on the old one: ResumeCrawl only closes the current channel.
		active.pauseMu.Unlock()
		m.mu.Unlock()
		return nil
	}
	active.Paused = true
	active.pauseCh = make(chan struct{})
	active.pauseMu.Unlock()
	pages := active.PageCount
	// Saved under m.mu: cancel clears m.active and completion sets finished
	// under it first, so "paused" never lands after "cancelled"/"completed".
	if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{"status": "paused"}); err != nil {
		log.Printf("store: update crawl %s status: %v", crawlID, err)
	}
	m.mu.Unlock()

	m.store.AddCrawlEvent(crawlID, "paused", fmt.Sprintf("%d pages", pages))
	m.emit(ProgressEvent{CrawlID: crawlID, Type: "paused", Data: map[string]interface{}{"crawlId": crawlID}})
	return nil
}

// ResumeCrawl resumes a paused crawl.
func (m *CrawlManager) ResumeCrawl(crawlID string) error {
	m.mu.Lock()
	if err := m.liveCrawlLocked(crawlID); err != nil {
		m.mu.Unlock()
		return err
	}
	m.active.pauseMu.Lock()
	m.active.Paused = false
	if m.active.pauseCh != nil {
		close(m.active.pauseCh)
		m.active.pauseCh = nil
	}
	m.active.pauseMu.Unlock()
	// Saved under m.mu, like pause: completion sets finished under it first,
	// so "running" never lands after "completed".
	if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{"status": "running"}); err != nil {
		log.Printf("store: update crawl %s status: %v", crawlID, err)
	}
	m.mu.Unlock()

	m.emit(ProgressEvent{CrawlID: crawlID, Type: "resumed", Data: map[string]interface{}{"crawlId": crawlID}})
	return nil
}

// RestartCrawl resumes a crawl that was interrupted (e.g. by app restart).
// It loads the original config, sets resume=true, and starts a new crawl job
// that reuses the existing crawl DB to skip already-crawled URLs.
// If configOverride is provided, it replaces the stored config (allowing
// the user to change settings before resuming).
func (m *CrawlManager) RestartCrawl(oldJobID string, configOverride map[string]interface{}) (string, error) {
	// Held until StartCrawl takes over, so a delete cannot remove the job
	// or its DB while the resume is being set up.
	release, err := m.holdJob(oldJobID)
	if err != nil {
		return "", err
	}
	defer release()
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
	if err := m.applyTrustedProxy(config); err != nil {
		return "", err
	}
	config["resume"] = true
	config["dbPath"] = oldJob.DBPath

	// The job is rewritten only once the crawl slot is ours: a restart
	// refused because another crawl runs (or this one is still active)
	// leaves the job, its config and its events untouched.
	prepare := func() error {
		// Clean up any duplicate jobs sharing the same DB (from previous resume attempts)
		if _, cleanErr := m.store.DeleteCrawlJobsByDBPath(oldJobID, oldJob.DBPath); cleanErr != nil {
			log.Printf("store: clean up duplicate jobs for %s: %v", oldJobID, cleanErr)
		}

		// Reuse the existing job — update its config and status, add "resumed" event.
		// The saved config leaves out resume/dbPath so a later Recrawl of this
		// job starts fresh instead of reopening its DB.
		saved := make(map[string]interface{}, len(config))
		for k, v := range config {
			if k != "resume" && k != "dbPath" {
				saved[k] = v
			}
		}
		if err := m.store.UpdateCrawlJobConfig(oldJobID, saved); err != nil {
			return fmt.Errorf("save config for resume: %w", err)
		}
		if err := m.store.UpdateCrawlJob(oldJobID, map[string]interface{}{
			"status":      "running",
			"completedAt": nil,
			"durationMs":  nil,
		}); err != nil {
			return fmt.Errorf("update job for resume: %w", err)
		}
		m.store.AddCrawlEvent(oldJobID, "resumed", fmt.Sprintf("from %d pages", oldJob.PageCount))
		return nil
	}

	if err := m.startCrawl(oldJobID, config, prepare); err != nil {
		return "", fmt.Errorf("start resumed crawl: %w", err)
	}

	return oldJobID, nil
}

// CancelCrawl cancels the active crawl. Once its pages are fetched it
// cancels the post-crawl work instead (cancelPostCrawlLocked).
func (m *CrawlManager) CancelCrawl(crawlID string) error {
	m.mu.Lock()
	if err := m.liveCrawlLocked(crawlID); err != nil {
		ok := m.cancelPostCrawlLocked(crawlID)
		m.mu.Unlock()
		if ok {
			return nil
		}
		return err
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
//
// Once the pages are fetched, Stop & Analyze stops the network work left
// (page weight, external link check) and lets the analysis finish with what
// it has.
func (m *CrawlManager) StopCrawl(crawlID string) error {
	m.mu.Lock()
	if err := m.liveCrawlLocked(crawlID); err != nil {
		run := m.deferred[crawlID]
		if run == nil || run.ctx.Err() != nil {
			m.mu.Unlock()
			return err
		}
		first := run.net.Err() == nil
		run.stopNet(errAnalysisStopped)
		m.mu.Unlock()
		if first {
			m.store.AddCrawlEvent(crawlID, "stopping", "network checks stopped; analysing what was fetched")
			m.emit(ProgressEvent{CrawlID: crawlID, Type: "stopping", Data: map[string]interface{}{"crawlId": crawlID, "postCrawl": true}})
		}
		return nil
	}
	m.active.Stopping = true
	m.active.stopAt = time.Now()
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

// errCrawlFinished: the crawl loop has returned and its post-crawl analysis
// runs. The job is already "completed"; resume, cancel, stop and tune would
// overwrite that status or cancel the analysis.
var errCrawlFinished = errors.New("crawl has finished; its analysis is running")

// liveCrawlLocked checks that crawlID is the active crawl and that its crawl
// loop is still running. Caller holds m.mu.
func (m *CrawlManager) liveCrawlLocked(crawlID string) error {
	if m.active == nil || m.active.ID != crawlID {
		return fmt.Errorf("no active crawl with id %s", crawlID)
	}
	if m.active.finished {
		return fmt.Errorf("crawl %s: %w", crawlID, errCrawlFinished)
	}
	return nil
}

// setAnalyzing marks a completed crawl whose results are not cached yet.
func (m *CrawlManager) setAnalyzing(crawlID string, on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !on {
		delete(m.analyzing, crawlID)
		return
	}
	if m.analyzing == nil {
		m.analyzing = map[string]bool{}
	}
	m.analyzing[crawlID] = true
}

// IsAnalyzing reports whether a completed crawl is still being analysed, so
// its results are not available yet.
func (m *CrawlManager) IsAnalyzing(crawlID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.analyzing[crawlID]
}

// activeCounters returns the queue counters of the active crawl, read under
// the lock the crawl updates them with (zero if crawlID is not active).
func (m *CrawlManager) activeCounters(crawlID string) (queued, totalSeen, excluded int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil || m.active.ID != crawlID {
		return 0, 0, 0
	}
	return m.active.Queued, m.active.TotalSeen, m.active.Excluded
}

// activePageCounts returns the live page and error counts of the active
// crawl. The DB copy is only written every 10 pages, so lists and status
// answers use these for the running crawl to match the Monitor.
func (m *CrawlManager) activePageCounts(crawlID string) (pages, errors int, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil || m.active.ID != crawlID {
		return 0, 0, false
	}
	return m.active.PageCount, m.active.ErrorCount, true
}

// withLiveCounts swaps the DB page and error counts of the active crawl's
// job for the live ones. Jobs must be copies (redactedJobs makes them).
func (m *CrawlManager) withLiveCounts(jobs []*CrawlJob) {
	for _, j := range jobs {
		if j == nil || (j.Status != "running" && j.Status != "paused") {
			continue
		}
		if pages, errs, ok := m.activePageCounts(j.ID); ok {
			// Never go backwards: a resumed crawl's DB count may be ahead
			// until its first progress event.
			if pages > j.PageCount {
				j.PageCount = pages
			}
			if errs > j.ErrorCount {
				j.ErrorCount = errs
			}
		}
	}
}

// CrawlError is one failed page shown in the Monitor's error list.
type CrawlError struct {
	URL        string `json:"url"`
	StatusCode int    `json:"statusCode,omitempty"`
	Error      string `json:"error"`
}

// maxRecentErrors caps the errors kept for the Monitor (the dashboard shows
// the same number).
const maxRecentErrors = 200

// crawlErrorFor describes a failed page, or returns false for a page that
// did not fail: a network error, or a 4xx/5xx answer.
func crawlErrorFor(url string, statusCode int, pageError string) (CrawlError, bool) {
	switch {
	case pageError != "":
		return CrawlError{URL: url, StatusCode: statusCode, Error: pageError}, true
	case statusCode >= 400:
		return CrawlError{URL: url, StatusCode: statusCode, Error: fmt.Sprintf("HTTP %d", statusCode)}, true
	}
	return CrawlError{}, false
}

// activeErrors returns a copy of the running crawl's latest errors, oldest
// first, with credentials in URLs masked.
func (m *CrawlManager) activeErrors(crawlID string) []CrawlError {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil || m.active.ID != crawlID {
		return nil
	}
	out := make([]CrawlError, len(m.active.RecentErrors))
	for i, e := range m.active.RecentErrors {
		e.URL = secrets.RedactURL(e.URL)
		out[i] = e
	}
	return out
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
	// Configs saved before the key/snippet folders existed (History,
	// schedules) lose those files here: say so in the crawl's events and
	// Monitor, else GSC/GA4/snippets just quietly do nothing.
	for _, w := range confineCrawlConfig(&crawlConfig) {
		log.Printf("crawl %s: %s", crawlID, w)
		m.store.AddCrawlEvent(crawlID, "warning", w)
		m.emit(ProgressEvent{CrawlID: crawlID, Type: "warning", Data: map[string]interface{}{"crawlId": crawlID, "message": w}})
	}
	crawlConfig.SkipSSRF = m.SkipSSRF
	// Configs stored before the trusted-proxy rule, or a dashboard started
	// without the proxy a schedule was saved with, must not reach a proxy
	// the local user did not set.
	if err := m.checkRunProxy(crawlConfig.Proxy); err != nil {
		log.Printf("crawl %s: %v", crawlID, err)
		if err2 := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{
			"status": "failed", "completedAt": time.Now().UTC().Format(time.RFC3339),
		}); err2 != nil {
			log.Printf("store: update crawl %s failure: %v", crawlID, err2)
		}
		m.store.AddCrawlEvent(crawlID, "failed", err.Error())
		m.emit(ProgressEvent{CrawlID: crawlID, Type: "error", Data: map[string]interface{}{"crawlId": crawlID, "error": err.Error()}})
		return
	}
	if crawlConfig.Proxy != "" {
		crawlConfig.Proxy = strings.TrimSpace(m.TrustedProxy) // never a masked form
	}

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
	var tuner *crawler.Tuner
	result, err := crawler.CrawlWithTuner(ctx, crawlConfig, func(p crawler.CrawlProgress) {
		completed := p.Crawled - p.Errors
		failed := p.Errors
		// URLs really waiting in the queue (not "limit minus crawled", which
		// reads as 999,683 pending on a 1M-limit crawl of a small site).
		pending := p.Queued
		total := maxPages
		if tuner != nil {
			total = tuner.Get().MaxPages // may change mid-crawl
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
			if ce, ok := crawlErrorFor(p.CurrentURL, sc, p.PageError); ok {
				if len(m.active.RecentErrors) >= maxRecentErrors {
					m.active.RecentErrors = m.active.RecentErrors[1:]
				}
				m.active.RecentErrors = append(m.active.RecentErrors, ce)
			}
		}
		m.mu.Unlock()

		// Update DB periodically (every 10 pages)
		if p.Crawled%10 == 0 {
			if p.Coverage != nil {
				if err := m.store.SaveCrawlCoverage(crawlID, p.Coverage); err != nil {
					log.Printf("[manager] save coverage: %v", err)
				}
			}
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
				"completed":     completed,
				"failed":        failed,
				"pending":       pending,
				"total":         total,
				"elapsedMs":     elapsed,
				"crawled":       p.Crawled,
				"queued":        p.Queued,
				"totalSeen":     p.TotalSeen,
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
		if phase == "analysis_loading" && loaded == 0 {
			// Make room before the crawler materializes its result, not only
			// when the final report is inserted into the cache.
			reserveCtx := ctx
			m.mu.RLock()
			if m.active != nil && m.active.ID == crawlID && m.active.Stopping {
				reserveCtx = m.ctx // graceful stop still analyzes the saved rows
			}
			m.mu.RUnlock()
			if evicted, _ := m.prepareResultLoad(reserveCtx, crawlID, total, nil); evicted {
				debug.FreeOSMemory()
			}
		}
		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: "progress",
			Data: map[string]interface{}{
				"phase":   phase,
				"loaded":  loaded,
				"total":   total,
				"crawled": 0,
			},
		})
	}, func(t *crawler.Tuner) {
		tuner = t
		m.mu.Lock()
		if m.active != nil && m.active.ID == crawlID {
			m.active.Tuner = t
		}
		m.mu.Unlock()
	})

	// Persist before cancellation handling: final decisions also matter when no
	// pages were fetched (robots/filters) or the user stopped the execution.
	if result != nil && result.Coverage != nil {
		if saveErr := m.store.SaveCrawlCoverage(crawlID, result.Coverage); saveErr != nil {
			log.Printf("[manager] save final coverage: %v", saveErr)
		}
	}
	elapsed := time.Since(startTime).Milliseconds()
	now := time.Now().UTC().Format(time.RFC3339)
	timer := newPhaseTimer(crawlID)

	// Check if the crawl was stopped (graceful) or cancelled (hard).
	// StopCrawl sets Stopping=true — we still run analysis on partial results.
	// CancelCrawl clears m.active — bail out entirely.
	m.mu.Lock()
	stopping := m.active != nil && m.active.Stopping
	if stopping {
		// Time for in-flight requests to finish and pages to load back.
		timer.parts = append(timer.parts, fmt.Sprintf("stop→crawl end %s", time.Since(m.active.stopAt).Round(time.Millisecond)))
	}
	// The post-crawl work (page weight, analysis, background checks) gets
	// its own context now, in the same lock as finished: from here cancel
	// and stop reach it instead of the crawl loop.
	var run *deferredRun
	runDone := func() {}
	if m.active != nil && m.active.ID == crawlID {
		m.active.finished = true // a later pause must not mark it "paused"
		if ctx.Err() == nil || stopping {
			run, runDone = m.startPostCrawlLocked(crawlID)
		}
	}
	m.mu.Unlock()
	handedOff := false
	defer func() {
		if !handedOff {
			runDone()
		}
	}()

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
		// "completed" comes before the analysis: say results are not ready.
		m.setAnalyzing(crawlID, true)
		defer m.setAnalyzing(crawlID, false)
		if err2 := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{
			"status": "completed", "completedAt": now,
			"pageCount": pageCount, "durationMs": elapsed,
		}); err2 != nil {
			log.Printf("store: update crawl %s completion: %v", crawlID, err2)
		}
		if result.StopReason != "" {
			// Stopped on its own with URLs still queued: say why, so a
			// short crawl does not look like a finished one.
			m.store.AddCrawlEvent(crawlID, "limit", "stopped early: "+result.StopReason+"; resume to continue")
		}
		m.store.AddCrawlEvent(crawlID, "completed", fmt.Sprintf("%d pages", pageCount))
	}

	// Cache results for successful crawls and graceful stops
	if success && run == nil {
		run, runDone = m.startPostCrawl(crawlID)
	}
	if success {
		// Run core analysis (fast: reporter + n-grams + field stripping)
		postCfg := analysis.PostProcessConfigFromCrawl(crawlConfig)
		postCfg.CrawlDurationMs = elapsed
		postCfg.NgramAnalyzer = result.NgramAnalyzer
		postCfg.AnchorAggregator = result.AnchorAggregator
		postCfg.InternalLinksIter = result.InternalLinksIter
		if len(result.DiscoveredSitemapURLs) > 0 {
			postCfg.SitemapEntries = result.DiscoveredSitemapURLs
		}
		postCfg.Soft404Probes = result.Soft404Probes
		postCfg.SitemapHreflang = result.SitemapHreflang
		postCfg.AgentReadiness = result.AgentReadiness
		intLinksClose := result.InternalLinksClose
		// Resource sizes (one HEAD per unique resource, often 100K+ on
		// image-heavy sites) are asked after the results are shown, in the
		// background with the deferred analysis (runDeferredAnalysis), on
		// the post-crawl network context: cancel and stop end them.
		var pageWeight *pageWeightJob
		if crawlConfig.PageWeight && len(result.ResourceRefs) > 0 {
			pageWeight = &pageWeightJob{
				refs:          result.ResourceRefs,
				transferSizes: result.TransferSizes,
				sizer:         result.ResourceSizer,
				concurrency:   crawlConfig.Concurrency,
			}
		} else if len(result.ResourceRefs) > 0 {
			// Even without full page weight mode, populate from available data
			analysis.AssignPageWeightFromRefs(pages, result.ResourceRefs, result.TransferSizes)
		}
		// External links are checked after the results are shown (background,
		// with the deferred analysis), so Stop & Analyze does not wait for
		// hundreds of HEAD requests to other sites.
		var externalLinks map[string][]string
		if crawlConfig.CheckExternal && len(result.ExternalLinks) > 0 {
			externalLinks = result.ExternalLinks
		}
		// Release CrawlResult (external links, resource refs, transfer sizes)
		// before post-crawl analysis to reduce peak RSS overlap.
		result = nil
		// The crawl context is cancelled by Stop & Analyze; the post-crawl
		// context is cancelled only by cancel, delete or restart.
		handedOff = true
		m.analyzeAndCache(analysisRun{
			ctx: run.ctx, crawlID: crawlID, job: job, config: crawlConfig,
			pages: pages, postCfg: postCfg, crawlStore: crawlStore, externalLinks: externalLinks,
			closeLinks: intLinksClose, elapsed: elapsed, beforeComplete: clearActive,
			timer: timer, run: run, runDone: runDone, pageWeight: pageWeight,
		})
		return
	}

	// Clear active before emitting complete so listeners see no active crawl.
	clearActive()

	m.emit(ProgressEvent{
		CrawlID: crawlID, Type: "complete",
		Data: map[string]interface{}{"crawlId": crawlID, "totalPages": pageCount, "durationMs": elapsed},
	})
}

// analysisRun is the input of the post-crawl analysis shared by finished
// crawls and imported crawl files.
type analysisRun struct {
	ctx            context.Context
	crawlID        string
	job            *CrawlJob
	config         types.CrawlConfig
	pages          []*types.PageData
	postCfg        analysis.PostProcessConfig
	crawlStore     *storage.CrawlStore // for the page index; may be nil
	externalLinks  map[string][]string // checked in the background after core analysis
	closeLinks     func()              // releases InternalLinksIter; may be nil
	elapsed        int64
	beforeComplete func()      // runs before the "complete" event; may be nil
	skipNotify     bool        // imports evaluate alerts but send no webhooks
	timer          *phaseTimer // may be nil
	// run is the post-crawl run cancel and stop reach; runDone ends it.
	// nil: analyzeAndCache starts one.
	run         *deferredRun
	runDone     func()
	interrupted string         // work already cut short by the user (stats note)
	pageWeight  *pageWeightJob // resource sizes asked in the background; may be nil
}

// analyzeAndCache runs core analysis, persists stats, caches the results and
// starts deferred analysis (link intelligence, embeddings) when configured.
//
// Cancel (CancelCrawl) before the results are cached ends the run with the
// job "cancelled" and nothing cached; after, the background work stops and
// keeps what it has done (runDeferredAnalysis).
func (m *CrawlManager) analyzeAndCache(r analysisRun) {
	run, runDone := r.run, r.runDone
	if run == nil {
		run, runDone = m.startPostCrawl(r.crawlID)
	}
	handedOff := false
	defer func() {
		if !handedOff {
			runDone()
		}
	}()
	// r.ctx (an import's request) or the run: either one cancels the analysis.
	analysisCtx, stopMerge := context.WithCancelCause(r.ctx)
	defer stopMerge(nil)
	defer context.AfterFunc(run.ctx, func() { stopMerge(context.Cause(run.ctx)) })()

	restoreGC := gctune.Hold(50, 0)
	defer restoreGC()
	if evicted, _ := m.prepareResultLoad(analysisCtx, r.crawlID, len(r.pages), nil); evicted {
		debug.FreeOSMemory()
	}
	crawlID, job, crawlConfig := r.crawlID, r.job, r.config
	pages, postCfg, crawlStore := r.pages, r.postCfg, r.crawlStore
	intLinksClose, elapsed, pageCount := r.closeLinks, r.elapsed, len(r.pages)
	clearActive := r.beforeComplete
	if clearActive == nil {
		clearActive = func() {}
	}
	timer := r.timer
	if timer == nil {
		timer = newPhaseTimer(crawlID)
	}
	// cancelled ends a run cancelled (user, delete, restart) before its
	// results were cached. At shutdown the analysis goes on as before, so
	// a "completed" job does not lose its stats.
	cancelled := func() bool {
		if run.ctx.Err() == nil || m.ctx.Err() != nil {
			return false
		}
		if intLinksClose != nil {
			intLinksClose()
		}
		m.endCancelledAnalysis(crawlID, run, pageCount)
		clearActive()
		return true
	}
	if cancelled() {
		return
	}
	crawlStats, coreGraph := analysis.RunCoreAnalysisWithGraph(analysisCtx, pages, postCfg)
	crawlStats.AnalysisInterrupted = r.interrupted
	timer.mark("report")
	if cancelled() {
		return // no alerts, emails or stats for a cancelled analysis
	}
	// Link intelligence reuses the reporter's graph instead of re-reading
	// every edge from disk.
	postCfg.Graph = coreGraph

	// The reporter already stored PageRank on each page (stats keep only
	// a histogram, plus the URL map for small crawls).

	// Inlink counts feed segment stats and the /pages API, so compute them
	// before stats are serialized. Pages no longer carry InternalLinks, so
	// read inlinks from the link graph.
	inlinkIdx := newInlinkIndex(coreGraph, pages)
	assignInlinks(pages, inlinkIdx)
	refreshAutoSegmentStats(pages, &crawlStats)
	timer.mark("inlinks+segments")

	// Evaluate alert rules against crawl stats
	crawlStats.AlertSummary = alerts.Evaluate(&crawlStats)
	if crawlStats.AlertSummary != nil {
		log.Printf("[alerts] %d alerts triggered (%d critical, %d warnings)",
			len(crawlStats.AlertSummary.Alerts), crawlStats.AlertSummary.Critical, crawlStats.AlertSummary.Warnings)
		// Send notifications if configured
		notifyCfg := m.getAlertNotifyConfig()
		notifyCfg.SlackURL = "" // Slack gets the full crawl report (notify.go)
		if !r.skipNotify && (notifyCfg.WebhookURL != "" || notifyCfg.SlackURL != "") {
			go alerts.Notify(notifyCfg, crawlStats.AlertSummary, crawlConfig.SeedURL)
		}
	}
	if !r.skipNotify {
		m.emailCrawlResult(job, crawlStats, pages)
	}

	// Convert CrawlStats to map for JSON serialization
	statsData, _ := json.Marshal(crawlStats)
	var statsMap map[string]interface{}
	json.Unmarshal(statsData, &statsMap)

	// Persist stats to UI DB (survives server restarts)
	if persistErr := m.store.SaveCrawlStats(crawlID, statsData); persistErr != nil {
		log.Printf("store: persist stats for crawl %s: %v", crawlID, persistErr)
	}
	timer.mark("alerts+save stats")

	// Determine DB path for lazy page loading
	dbPath := ""
	if job != nil {
		dbPath = job.DBPath
	}

	// Check if deferred analysis (link intelligence, embeddings) is needed
	needsDeferred := crawlConfig.LinkIntelligence || (crawlConfig.Embeddings && crawlConfig.EmbeddingKey != "") || len(r.externalLinks) > 0 || r.pageWeight != nil
	var checkExternal func(context.Context) *crawler.ExternalCheckSummary
	if len(r.externalLinks) > 0 {
		links := r.externalLinks
		checkExternal = func(ctx context.Context) *crawler.ExternalCheckSummary {
			// Network work: Stop & Analyze ends it too (netContext).
			return m.checkExternalLinks(netContext(ctx), crawlID, crawlConfig, links)
		}
	}

	// Link graph + PageRank go to the crawl DB so reloads skip rebuilding
	// them. Scores are copied now; the write happens in the background.
	var coreSnap *analysisSnapshot
	if dbPath != "" {
		coreSnap = snapshotAnalysis(pages, coreGraph, true)
	}

	// Pre-compute aggregations before stripping (Anchors, InternalLinks, etc.)
	anchorStats := anchorCloudToMap(crawlStats.AnchorCloud)
	if anchorStats == nil {
		anchorStats = buildAnchorStats(pages)
	}
	dirTree := buildDirectoryTree(pages)
	tmplTypes := collectTemplateTypes(pages)
	timer.mark("anchors+dir tree")

	// Build page_index for instant merge lookups (before stripping)
	if crawlStore != nil {
		if idxErr := crawlStore.BuildPageIndex(pages); idxErr != nil {
			log.Printf("crawl %s: build page index: %v", crawlID, idxErr)
		} else {
			fmt.Fprintf(os.Stderr, "  [page-index] Built index with %d entries for instant merge\n", len(pages))
		}
	}

	timer.mark("page index")

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
		Graph:           coreGraph,
		DBPath:          dbPath,
		PageCount:       pageCount,
		AnalysisPending: needsDeferred,
		PagesReady:      true,
		linkAnalysis:    crawlConfig.LinkIntelligence || (crawlConfig.Embeddings && crawlConfig.EmbeddingKey != ""),
	}
	newResult.loadingProgress.Store(100)

	if m.testBeforeCache != nil {
		m.testBeforeCache(crawlID)
	}
	m.mu.Lock()
	if run.ctx.Err() != nil && m.ctx.Err() == nil {
		m.mu.Unlock()
		cancelled()
		return
	}
	// Replaces (and cancels the background load of) any older entry.
	m.cacheResultLocked(crawlID, newResult, pageCount)
	delete(m.analyzing, crawlID)
	run.shown = true // from now a cancel keeps the results (same lock as CancelCrawl)
	m.mu.Unlock()

	// Clear active before emitting complete so listeners see no active crawl.
	clearActive()
	timer.mark("cache")
	timer.log()

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
		// Its own context: deleting or restarting the crawl cancels it. The
		// crawl context is not used, Stop & Analyze has already cancelled it.
		// A soft hold keeps the crawl DB until it ends, without making
		// DeleteCrawl wait (see DeleteCrawl).
		deferCtx, deferDone := run.ctx, runDone
		handedOff = true
		release := m.retainSoft(crawlID)
		m.background.Add(1)
		go func() {
			defer m.background.Done()
			defer release()
			defer deferDone()
			defer func() {
				if intLinksClose != nil {
					intLinksClose()
				}
			}()
			coreSnap.save(dbPath)
			m.runDeferredAnalysis(deferCtx, crawlID, newResult, postCfg, crawlStats, dbPath, checkExternal, r.pageWeight)
		}()
	} else {
		if intLinksClose != nil {
			intLinksClose()
		}
		if coreSnap != nil {
			release := m.retainJob(crawlID)
			m.background.Add(1)
			go func() {
				defer m.background.Done()
				defer release()
				coreSnap.save(dbPath)
			}()
		}
	}

}

// deferredRun is the post-crawl work of one crawl run, from the end of the
// crawl loop to the end of its background analysis: page weight, core
// analysis, external link check, link intelligence, embeddings. Cancel,
// Stop & Analyze, delete and restart reach it through m.deferred.
type deferredRun struct {
	// ctx ends all of it. Cause errAnalysisCancelled: the user cancelled,
	// the work done so far is kept. Other causes (delete, restart, end):
	// nothing more is published.
	ctx    context.Context
	cancel context.CancelCauseFunc
	// net is ctx for the network work (page weight, external link check);
	// Stop & Analyze ends only it, with errAnalysisStopped.
	net     context.Context
	stopNet context.CancelCauseFunc
	// shown is set when the results are cached. A cancel before that ends
	// the job "cancelled"; after, the job stays "completed". Guarded by m.mu.
	shown bool
}

var (
	errAnalysisCancelled = errors.New("cancelled by user")
	errAnalysisStopped   = errors.New("stopped by user")
)

type deferredRunKey struct{}

// netContext returns the network context of the run ctx belongs to, or ctx.
func netContext(ctx context.Context) context.Context {
	if run, ok := ctx.Value(deferredRunKey{}).(*deferredRun); ok {
		return run.net
	}
	return ctx
}

// startDeferred returns the context of a crawl's post-crawl work and a func
// to call when it ends. A previous run of the same crawl is cancelled.
func (m *CrawlManager) startDeferred(crawlID string) (context.Context, func()) {
	run, done := m.startPostCrawl(crawlID)
	return run.ctx, done
}

func (m *CrawlManager) startPostCrawl(crawlID string) (*deferredRun, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startPostCrawlLocked(crawlID)
}

// startPostCrawlLocked registers the post-crawl run of a crawl. Caller holds m.mu.
func (m *CrawlManager) startPostCrawlLocked(crawlID string) (*deferredRun, func()) {
	run := &deferredRun{}
	base, cancel := context.WithCancelCause(m.ctx)
	run.ctx, run.cancel = context.WithValue(base, deferredRunKey{}, run), cancel
	run.net, run.stopNet = context.WithCancelCause(run.ctx)
	m.cancelDeferredLocked(crawlID)
	if m.deferred == nil {
		m.deferred = make(map[string]*deferredRun)
	}
	m.deferred[crawlID] = run
	return run, func() {
		m.mu.Lock()
		if m.deferred[crawlID] == run {
			delete(m.deferred, crawlID)
		}
		m.mu.Unlock()
		cancel(nil)
	}
}

// cancelDeferredLocked stops the background analysis of a crawl that is
// deleted or started again. Caller holds m.mu.
func (m *CrawlManager) cancelDeferredLocked(crawlID string) {
	if run := m.deferred[crawlID]; run != nil {
		run.cancel(nil)
		delete(m.deferred, crawlID)
	}
}

// cancelPostCrawlLocked cancels the post-crawl work of a crawl whose pages
// are fetched; false if none is running. Its owner records the outcome:
// endCancelledAnalysis before the results are cached, runDeferredAnalysis
// after. Caller holds m.mu.
func (m *CrawlManager) cancelPostCrawlLocked(crawlID string) bool {
	run := m.deferred[crawlID]
	if run == nil {
		return false
	}
	if run.ctx.Err() != nil {
		// Already cancelled by the user: same answer. Ended by delete or
		// restart: nothing of this run is left to cancel.
		return errors.Is(context.Cause(run.ctx), errAnalysisCancelled)
	}
	run.cancel(errAnalysisCancelled)
	return true
}

// endCancelledAnalysis records a post-crawl analysis cancelled before its
// results were cached: the job ends "cancelled" (its pages stay in the
// crawl DB; Resume runs it again). A run ended by delete or restart leaves
// the job to them.
func (m *CrawlManager) endCancelledAnalysis(crawlID string, run *deferredRun, pageCount int) {
	m.mu.Lock()
	delete(m.analyzing, crawlID)
	m.mu.Unlock()
	if !errors.Is(context.Cause(run.ctx), errAnalysisCancelled) {
		return
	}
	log.Printf("crawl %s: analysis cancelled by user before results were ready", crawlID)
	if err := m.store.UpdateCrawlJob(crawlID, map[string]interface{}{"status": "cancelled"}); err != nil {
		log.Printf("store: update crawl %s status: %v", crawlID, err)
	}
	m.store.AddCrawlEvent(crawlID, "cancelled", fmt.Sprintf("%d pages fetched; analysis cancelled before results were ready", pageCount))
	m.emit(ProgressEvent{CrawlID: crawlID, Type: "cancelled", Data: map[string]interface{}{"crawlId": crawlID}})
}

// deferredWork returns the pages deferred analysis works on, without
// copying them. Embeddings only read pages. Link intelligence swaps each
// page for a small stub in its own slice and writes scores on the stubs; it
// never writes to the input pages once their links are released, and the
// structs stubs share with them (Topicality, AutoSegments) are copied only
// where a value changes. So the cached pages drop their links first, under
// the write lock (the graph is built before that), and handlers keep
// reading them. Without link intelligence nothing is copied; with it the
// extra is the slice plus the changed structs.
func deferredWork(own *CrawlResults, cfg *analysis.PostProcessConfig) []*types.PageData {
	pages := own.Pages
	if !cfg.LinkIntelligence {
		return pages
	}
	if cfg.Graph == nil || cfg.Graph.N != len(pages) {
		cfg.Graph = analysis.BuildLinkGraph(pages, cfg.InternalLinksIter)
	}
	own.mu.Lock()
	releaseLinkData(pages)
	own.mu.Unlock()
	return slices.Clone(pages)
}

// releaseLinkData frees what link intelligence would free on the cached
// pages: link slices (counts kept) and the fields stripped for the cache.
// The analysis copies keep what they need. Caller holds the result lock.
func releaseLinkData(pages []*types.PageData) {
	for _, p := range pages {
		if len(p.InternalLinks) > 0 {
			p.InternalLinkCount = len(p.InternalLinks)
		}
		if len(p.ExternalLinks) > 0 {
			p.ExternalLinkCount = len(p.ExternalLinks)
		}
		p.InternalLinks = nil
		p.ExternalLinks = nil
	}
	stripPagesForCache(pages)
}

// runDeferredAnalysis runs link intelligence and embeddings in the background.
// Updates cached results and persists stats when complete.
// Receives crawlStats directly from RunCoreAnalysis to avoid JSON round-trip.
// own is the cached result holding the pages; they change only under its
// write lock. ctx is cancelled when the crawl is deleted or restarted: the
// run then stops without publishing anything. Cancelled by the user
// (errAnalysisCancelled) it stops too, but publishes what it has done with a
// note (stats.analysisInterrupted, a "cancelled" crawl event); Stop & Analyze
// ends only the external check (netContext) and the analysis goes on.
func (m *CrawlManager) runDeferredAnalysis(ctx context.Context, crawlID string, own *CrawlResults, cfg analysis.PostProcessConfig, crawlStats types.CrawlStats, dbPath string, checkExternal func(context.Context) *crawler.ExternalCheckSummary, pageWeight *pageWeightJob) {
	pages := own.Pages
	// finish strips the cached pages and clears the pending flag.
	finish := func() {
		own.mu.Lock()
		stripPagesForCache(pages)
		own.AnalysisPending = false
		own.mu.Unlock()
	}
	// Panic recovery: if analysis panics, clear pending flag so UI doesn't hang.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("deferred analysis panic for crawl %s: %v", crawlID, r)
			m.mu.Lock()
			if res, ok := m.results[crawlID]; ok {
				res.AnalysisPending = false
			}
			m.mu.Unlock()
			finish()
			m.emit(ProgressEvent{
				CrawlID: crawlID, Type: "analysis_complete",
				Data: map[string]interface{}{"crawlId": crawlID, "error": fmt.Sprintf("%v", r)},
			})
		}
	}()
	userCancelled := func() bool { return errors.Is(context.Cause(ctx), errAnalysisCancelled) }
	stale := func() bool {
		if ctx.Err() == nil || userCancelled() {
			return false
		}
		log.Printf("deferred analysis for crawl %s cancelled (crawl deleted or restarted)", crawlID)
		finish()
		return true
	}
	// notes say what the user cut short, for the stats and the crawl events.
	var notes []string
	if crawlStats.AnalysisInterrupted != "" {
		notes = append(notes, crawlStats.AnalysisInterrupted)
	}

	// External links first: their results are short and users wait for them.
	// Stats are saved and the Results page refreshed as soon as they finish.
	if checkExternal != nil {
		sum := checkExternal(ctx)
		if stale() {
			return
		}
		if sum != nil {
			for _, ext := range sum.Broken {
				if ext.StatusCode >= 400 || ext.Error != "" {
					crawlStats.BrokenExternalLinks = append(crawlStats.BrokenExternalLinks, ext)
				}
			}
			applyExternalCheckSummary(&crawlStats, sum)
			if net := netContext(ctx); net.Err() != nil && sum.Skipped > 0 {
				notes = append(notes, fmt.Sprintf("external link check %v after %d of %d URLs (the rest left unchecked)",
					context.Cause(net), sum.Checked, sum.Total))
				crawlStats.AnalysisInterrupted = strings.Join(notes, "; ")
			}
			m.publishStats(crawlID, crawlStats)
		}
		m.mu.RLock()
		if res := m.results[crawlID]; res != nil {
			res.externalTotal.Store(0)
		}
		m.mu.RUnlock()
		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: "analysis_progress",
			Data: map[string]interface{}{"crawlId": crawlID, "phase": "external_done"},
		})
	}

	// Resource sizes for page weight: started after the external check so
	// both never add up on one host, then left running next to link
	// intelligence (network next to CPU). Applied when both are done.
	// Network work: Stop & Analyze ends it too (netContext); the sizes
	// known by then are kept.
	var pageWeightCh <-chan pageWeightResult
	if pageWeight != nil {
		pageWeightCh = m.startPageWeight(netContext(ctx), crawlID, pageWeight)
	}
	pageWeightApplied := false

	m.emit(ProgressEvent{
		CrawlID: crawlID, Type: "analysis_progress",
		Data: map[string]interface{}{"crawlId": crawlID, "phase": "link_intelligence"},
	})

	// Set LI phase progress callback for granular WebSocket updates
	cfg.OnLIPhase = func(phase string) {
		if ctx.Err() != nil {
			return
		}
		m.emit(ProgressEvent{
			CrawlID: crawlID, Type: "analysis_progress",
			Data: map[string]interface{}{"crawlId": crawlID, "phase": "li_" + phase},
		})
	}

	// Persist stats after each LI phase so completed phases survive a crash.
	cfg.OnPersist = func() {
		if ctx.Err() == nil {
			m.publishStats(crawlID, crawlStats)
		}
	}

	// The cached pages drop now what link intelligence would drop, so
	// memory peaks as before. Cancelled already: nothing more runs.
	work := pages
	if ctx.Err() == nil {
		work = deferredWork(own, &cfg)
	}

	// Run deferred analysis (link intelligence + embeddings)
	graph := analysis.RunDeferredAnalysis(ctx, work, cfg, &crawlStats)
	// Link intelligence adds graph in-degree; refresh segment inlink
	// averages on the stubs. Without it nothing changed since the core
	// analysis did the same.
	if cfg.LinkIntelligence && graph != nil {
		analysis.AssignAuthoritySegmentsShared(work, nil)
		crawlStats.AutoSegmentStats = analysis.BuildAutoSegmentStats(work)
	}
	if stale() {
		return
	}
	if ctx.Err() != nil && (cfg.LinkIntelligence || (cfg.Embeddings && cfg.EmbeddingKey != "")) {
		notes = append(notes, "link intelligence and embeddings cancelled by user (finished phases kept)")
	}
	// Sizes already known: publish them with the deferred results (one
	// save, one refresh). work holds the pages the cache gets below.
	if pageWeightCh != nil {
		select {
		case r := <-pageWeightCh:
			own.mu.Lock()
			crawlStats.PageWeightStats = pageWeight.apply(work, r)
			own.mu.Unlock()
			pageWeightApplied = true
			if note := pageWeightNote(netContext(ctx), r.sum); note != "" {
				notes = append(notes, note)
			}
		default:
		}
	}
	interrupted := strings.Join(notes, "; ")
	crawlStats.AnalysisInterrupted = interrupted
	// Store HITS scores with PageRank (the graph was stored after core analysis).
	if dbPath != "" && graph != nil {
		snapshotAnalysis(work, graph, false).save(dbPath)
	}

	// Convert updated stats to map for JSON API
	updatedData, err := json.Marshal(crawlStats)
	if err != nil {
		log.Printf("store: marshal deferred stats for crawl %s: %v", crawlID, err)
		finish()
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

	// Swap in the analysed pages and strip them under the write lock, so
	// HTTP handlers never read a page while it changes.
	m.mu.RLock()
	res := m.results[crawlID]
	m.mu.RUnlock()
	update := func(r *CrawlResults) {
		r.Stats = updatedMap
		if graph != nil {
			r.Graph = graph
		}
		r.AnalysisPending = false
	}
	own.mu.Lock()
	copy(pages, work)
	stripPagesForCache(pages)
	own.AnalysisPending = false
	if res == own {
		update(own)
	}
	own.mu.Unlock()
	if res != nil && res != own { // evicted and reloaded meanwhile
		res.mu.Lock()
		update(res)
		res.mu.Unlock()
	}

	// Release deferred analysis memory
	debug.FreeOSMemory()

	if interrupted != "" {
		event := "stopped"
		if userCancelled() {
			event = "cancelled"
		}
		m.store.AddCrawlEvent(crawlID, event, interrupted)
		log.Printf("crawl %s: background analysis %s: %s", crawlID, event, interrupted)
	}
	m.emit(ProgressEvent{
		CrawlID: crawlID, Type: "analysis_complete",
		Data: map[string]interface{}{"crawlId": crawlID, "interrupted": interrupted},
	})

	fmt.Fprintf(os.Stderr, "  [deferred] Analysis complete for crawl %s\n", crawlID)

	if pageWeightCh != nil {
		m.finishPageWeight(ctx, crawlID, own, pageWeight, pageWeightCh, pageWeightApplied, crawlStats)
	}
}

// publishStats saves stats mid-analysis and swaps them into the cached
// result, so finished parts survive a crash and show up on reload.
func (m *CrawlManager) publishStats(crawlID string, crawlStats types.CrawlStats) {
	data, err := json.Marshal(crawlStats)
	if err != nil {
		log.Printf("store: marshal stats for crawl %s: %v", crawlID, err)
		return
	}
	if err := m.store.SaveCrawlStats(crawlID, data); err != nil {
		log.Printf("store: persist stats for crawl %s: %v", crawlID, err)
	}
	m.mu.RLock()
	res := m.results[crawlID]
	m.mu.RUnlock()
	if res == nil {
		return // persistence is sufficient; a future reload reads the latest stats
	}
	var statsMap map[string]interface{}
	if err := json.Unmarshal(data, &statsMap); err != nil {
		return
	}
	res.mu.Lock()
	defer res.mu.Unlock()
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.results[crawlID] == res {
		res.Stats = statsMap
	}
}

// evictOldestResult removes the oldest cached result, cancelling any background loading.
// Must be called with m.mu held.
func (m *CrawlManager) evictOldestResult() {
	if len(m.resultOrder) == 0 {
		return
	}
	m.removeCachedResultLocked(m.resultOrder[0])
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

	// One load per crawl at a time: concurrent requests share it instead of
	// each decoding every page.
	return m.loads.do(crawlID, func() *CrawlResults { return m.loadResults(crawlID) })
}

// loadResults loads a completed crawl from its stats and crawl DB.
func (m *CrawlManager) loadResults(crawlID string) *CrawlResults {
	m.mu.RLock()
	if r, ok := m.results[crawlID]; ok {
		m.mu.RUnlock()
		return r // cached while this call waited
	}
	m.mu.RUnlock()

	// Held so a delete cannot run between reading the job and caching it,
	// which would cache a crawl that no longer exists.
	release, err := m.holdJob(crawlID)
	if err != nil {
		return nil
	}
	defer release()

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
	var inlinkIdx *inlinkIndex
	var graph *analysis.AdjacencyGraph
	if job.DBPath != "" {
		m.loads.acquire(context.Background())
		defer m.loads.release()
		// Quick-stats may have inserted a placeholder while this waited.
		if existing := m.GetCachedResults(crawlID); existing != nil {
			return existing
		}
		if evicted, _ := m.prepareResultLoad(context.Background(), crawlID, job.PageCount, nil); evicted {
			debug.FreeOSMemory()
		}
		cs, csErr := storage.NewCrawlStore(job.DBPath)
		if csErr == nil {
			var loadErr error
			pages, loadErr = cs.GetPagesForAnalysis(context.Background(), nil, nil, storedAnchorCloud(statsMap) != nil)
			cs.Close()
			if loadErr != nil {
				log.Printf("lazy load pages for crawl %s: %v", crawlID, loadErr)
			}
			agg := prepareLoadedPages(crawlID, pages, statsMap, job.DBPath, job.SeedURL, job.Config)
			applyStatsUpdates(statsMap, agg.statsUpdates)
			anchorStats, dirTree, tmplTypes = agg.anchorStats, agg.dirTree, agg.tmplTypes
			inlinkIdx, graph = agg.inlinkIdx, agg.graph
			stripPagesForCache(pages)
			if agg.persist {
				if updated, err := json.Marshal(statsMap); err == nil {
					if persistErr := m.store.SaveCrawlStats(crawlID, updated); persistErr != nil {
						log.Printf("backfill: persist stats for crawl %s: %v", crawlID, persistErr)
					}
				}
			}
		} else {
			log.Printf("lazy load: open crawl store for %s: %v", crawlID, csErr)
		}
	}

	result := &CrawlResults{
		Stats:         statsMap,
		Pages:         pages,
		Graph:         graph,
		AnchorStats:   anchorStats,
		DirTree:       dirTree,
		TemplateTypes: tmplTypes,
		InlinkIndex:   inlinkIdx,
		DBPath:        job.DBPath,
		PageCount:     job.PageCount,
		PagesReady:    true,
	}
	result.loadingProgress.Store(100)

	// Cache for future requests (double-check under write lock to avoid TOCTOU race)
	m.mu.Lock()
	if existing, ok := m.results[crawlID]; ok {
		m.mu.Unlock()
		return existing
	}
	m.cacheResultLocked(crawlID, result, max(job.PageCount, len(pages)))
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

	// Held until the entry is cached (see loadResults).
	release, err := m.holdJob(crawlID)
	if err != nil {
		return nil
	}
	defer release()

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
	m.cacheResultLocked(crawlID, result, job.PageCount)
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

	// Full loads hold every page in memory: run them one at a time.
	if !m.loads.acquire(ctx) {
		return // evicted while waiting
	}
	defer m.loads.release()
	evicted, allowed := m.prepareResultLoad(ctx, crawlID, result.PageCount, result)
	if !allowed {
		return
	}
	if evicted {
		debug.FreeOSMemory()
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
	onProgress := func(loaded int) {
		if total > 0 {
			progress := (loaded * 100) / total
			result.loadingProgress.Store(int32(progress))
		}
	}
	result.mu.RLock()
	skipAnchors := storedAnchorCloud(result.Stats) != nil
	result.mu.RUnlock()
	var pages []*types.PageData
	var loadErr error
	pages, loadErr = cs.GetPagesForAnalysis(ctx, onProgress, nil, skipAnchors)
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

	// Snapshot stats so graph backfills can run without holding the lock.
	var statsSnapshot map[string]interface{}
	result.mu.RLock()
	if result.Stats != nil {
		statsSnapshot = make(map[string]interface{}, len(result.Stats))
		for k, v := range result.Stats {
			statsSnapshot[k] = v
		}
	}
	result.mu.RUnlock()

	var jobConfig map[string]interface{}
	if job, err := m.store.GetCrawlJob(crawlID); err == nil && job != nil {
		jobConfig = job.Config
	}

	// Graph first, then inlinks and PageRank, then segments and the
	// directory tree, so every aggregate sees the final values.
	agg := prepareLoadedPages(crawlID, pages, statsSnapshot, result.DBPath, seedURL, jobConfig)
	anchorStats, dirTree, tmplTypes := agg.anchorStats, agg.dirTree, agg.tmplTypes
	inlinkIdx, graph := agg.inlinkIdx, agg.graph

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

	if len(agg.statsUpdates) > 0 {
		result.mu.Lock()
		if result.Stats != nil {
			applyStatsUpdates(result.Stats, agg.statsUpdates)
		}
		result.mu.Unlock()
	}
	if agg.persist {
		result.mu.RLock()
		if updated, err := json.Marshal(result.Stats); err == nil {
			if persistErr := m.store.SaveCrawlStats(crawlID, updated); persistErr != nil {
				log.Printf("backfill: persist stats for crawl %s: %v", crawlID, persistErr)
			}
		}
		result.mu.RUnlock()
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
		// Keep the summary (TotalBytes, HTMLBytes, ByType). Write only when
		// set: the struct can be shared with copies handlers are encoding.
		if p.PageWeight != nil && p.PageWeight.Resources != nil {
			p.PageWeight.Resources = nil
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
// ConfigFromMap converts a dashboard-format crawl config (the JSON the New
// Crawl page sends) to a CrawlConfig. Used by `micelio schedule --config`.
func ConfigFromMap(m map[string]interface{}) types.CrawlConfig { return configFromMap(m) }

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
	// The dashboard sends "delay" (ms) and "urlList" (one URL per line).
	// Without these two mappings every dashboard crawl ran with no delay
	// and list mode crawled nothing.
	if v, ok := m["delay"]; ok && m["delayMs"] == nil {
		m["delayMs"] = v
	}
	if v, ok := m["urlList"].(string); ok && m["urls"] == nil {
		var urls []string
		for _, line := range strings.Split(v, "\n") {
			if s := strings.TrimSpace(line); s != "" {
				urls = append(urls, s)
			}
		}
		m["urls"] = urls
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

	m.finishLogJob(jobID, stats, result.StreamURLs, func() map[string]interface{} {
		return map[string]interface{}{
			"completedAt": now, "durationMs": elapsed,
			"totalLines": stats.TotalLines, "processedLines": stats.TotalLines, "format": string(format),
		}
	}, map[string]interface{}{"totalLines": stats.TotalLines})
}

// finishLogJob saves a parsed log job (per-URL table, summary stats, final
// status) and announces it. Success is announced only when every write
// worked. On a failure the partial URL table is dropped, so a later merge
// does not read it as the full set (it falls back to the capped summary),
// and the job is marked failed with an error log_complete.
func (m *CrawlManager) finishLogJob(jobID string, stats *logs.LogStats, stream func(fn func(logs.URLStat) error) error, jobFields func() map[string]interface{}, completeData map[string]interface{}) {
	err := m.saveLogResult(jobID, stats, stream)
	fields := jobFields() // after the save: timings include it
	if err == nil {
		done := map[string]interface{}{"status": "completed"}
		for k, v := range fields {
			done[k] = v
		}
		if err = m.store.UpdateLogJob(jobID, done); err != nil {
			err = fmt.Errorf("save job status: %w", err)
		}
	}
	if err != nil {
		errMsg := "saving log analysis failed: " + err.Error()
		log.Printf("[logs] jobID=%s %s", jobID, errMsg)
		_ = m.store.SaveLogURLStats(jobID, nil) // drop a partial URL table
		failed := map[string]interface{}{"status": "failed", "errorMsg": errMsg}
		for k, v := range fields {
			if k != "format" && k != "totalLines" && k != "processedLines" {
				failed[k] = v
			}
		}
		if uerr := m.store.UpdateLogJob(jobID, failed); uerr != nil {
			log.Printf("[logs] jobID=%s mark failed: %v", jobID, uerr)
		}
		m.emit(ProgressEvent{
			Type: "log_complete",
			Data: map[string]interface{}{"jobId": jobID, "error": errMsg},
		})
		return
	}
	data := map[string]interface{}{"jobId": jobID}
	for k, v := range completeData {
		data[k] = v
	}
	m.emit(ProgressEvent{Type: "log_complete", Data: data})
}

// saveLogResult writes the per-URL table (if any) and the summary stats.
func (m *CrawlManager) saveLogResult(jobID string, stats *logs.LogStats, stream func(fn func(logs.URLStat) error) error) error {
	// Stream URL stats to per-job SQLite table (no intermediate slice).
	if stream != nil {
		if err := m.store.StreamLogURLStats(jobID, stream); err != nil {
			return fmt.Errorf("save URL stats: %w", err)
		}
		if len(stats.TopURLs) == 0 {
			if topURLs, err := m.store.QueryTopLogURLs(jobID, 500); err == nil && len(topURLs) > 0 {
				stats.TopURLs = topURLs
			}
		}
	}
	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("encode stats: %w", err)
	}
	if err := m.store.SaveLogStats(jobID, statsJSON); err != nil {
		return fmt.Errorf("save stats: %w", err)
	}
	return nil
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
	// Memory stays bounded: distinct URLs are capped (MICELIO_MAX_LOG_URLS)
	// and so is .gz expansion (MICELIO_MAX_LOG_DECOMPRESSED_GB).
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

	// URL stats stream to the per-job SQLite table (no intermediate slice —
	// saves ~2.5GB); analysisMs includes that save.
	m.finishLogJob(jobID, stats, result.StreamURLs, func() map[string]interface{} {
		return map[string]interface{}{
			"completedAt": now, "durationMs": elapsed,
			"parseMs": parseMs, "analysisMs": time.Since(analysisStart).Milliseconds(),
			"totalLines": stats.TotalLines, "processedLines": stats.TotalLines, "format": string(format),
			"files": perFile,
		}
	}, map[string]interface{}{"totalLines": stats.TotalLines, "filesTotal": len(filePaths)})
}

// runBotVerification performs FCrDNS verification for a log analysis job.
func (m *CrawlManager) runBotVerification(jobID string) (map[string]*logs.VerificationStats, []*logs.VerificationResult, error) {
	m.mu.RLock()
	botIPs := m.logBotIPs[jobID]
	m.mu.RUnlock()

	if botIPs == nil {
		return nil, nil, fmt.Errorf("no bot IP data for job %s (data available only in current session)", jobID)
	}

	// Shared verifier: one DNS cache and one run per job however many
	// requests arrive; IPs checked are capped (MICELIO_MAX_VERIFY_IPS).
	stats, spoofed := logs.SharedBotVerifier().VerifyJob(jobID, botIPs)
	return stats, spoofed, nil
}
