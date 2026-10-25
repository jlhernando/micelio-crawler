// Package scheduler runs crawls on a cron schedule with state persistence.
package scheduler

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/types"
	"github.com/micelio/micelio/internal/webhook"
)

// State tracks a schedule's run history. Schedules created from the
// dashboard (Source "ui") also store their crawl config, check and webhook
// here, so the server can resume them after a restart.
type State struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	Source         string `json:"source,omitempty"` // "ui" or "cli"
	URL            string `json:"url"`
	Cron           string `json:"cron"`
	CreatedAt      string `json:"createdAt"`
	LastRun        string `json:"lastRun,omitempty"`
	LastStatus     string `json:"lastStatus,omitempty"`
	NextRun        string `json:"nextRun"`
	OutputDir      string `json:"outputDir"`
	LastPages      int    `json:"lastPages"`
	LastDurationMs int64  `json:"lastDurationMs"`
	TotalRuns      int    `json:"totalRuns"`
	LastReason     string `json:"lastReason,omitempty"`  // why a run was skipped, paused or stopped
	LastCrawlID    string `json:"lastCrawlId,omitempty"` // dashboard crawl job of the last run
	Running        bool   `json:"running,omitempty"`
	Paused         bool   `json:"paused,omitempty"` // waiting for the check to pass again

	CrawlConfig map[string]interface{} `json:"crawlConfig,omitempty"`
	Precheck    *Precheck              `json:"precheck,omitempty"`
	LastCheck   *PrecheckResult        `json:"lastCheck,omitempty"`
	Webhook     string                 `json:"webhook,omitempty"`
}

// Config defines a scheduled crawl.
type Config struct {
	Webhook     *webhook.Options
	URL         string
	CronExpr    string
	OutputDir   string
	CrawlConfig types.CrawlConfig
	MaxRuns     int

	Name     string
	Source   string                 // "ui" (dashboard) or "cli"; default "cli"
	CrawlMap map[string]interface{} // dashboard-format config, persisted for "ui" schedules
	Precheck *Precheck
	// Runner executes one crawl. Nil runs crawler.Crawl directly and writes
	// the output file (CLI behaviour).
	Runner Runner
}

// RunResult is the outcome of one scheduled run.
type RunResult struct {
	Status  string // "success", "failed", "stopped", "skipped"
	Reason  string
	Pages   int
	Output  string
	CrawlID string
}

// Runner executes one scheduled crawl. It should honour ctl: pause the crawl
// while ctl.Paused() is true (or stop it when pausing is not supported) and
// return when ctx is cancelled.
type Runner interface {
	Run(ctx context.Context, cfg Config, ctl *RunControl) RunResult
}

// RunControl tells a running crawl to pause or resume when the check
// changes.
type RunControl struct {
	mu      sync.Mutex
	paused  bool
	reason  string
	changed chan struct{}
}

func newRunControl() *RunControl { return &RunControl{changed: make(chan struct{}, 1)} }

// Paused reports whether the crawl should be paused, and why.
func (c *RunControl) Paused() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused, c.reason
}

// Changed is signalled whenever Paused changes.
func (c *RunControl) Changed() <-chan struct{} { return c.changed }

func (c *RunControl) set(paused bool, reason string) bool {
	c.mu.Lock()
	changed := c.paused != paused
	c.paused, c.reason = paused, reason
	c.mu.Unlock()
	if changed {
		select {
		case c.changed <- struct{}{}:
		default:
		}
	}
	return changed
}

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// Scheduler manages scheduled crawl runs.
type Scheduler struct {
	ctx      context.Context
	cron     *cron.Cron
	cancel   context.CancelFunc
	state    State
	stateDir string
	config   Config
	mu       sync.Mutex
	crawling bool
}

// New creates a new scheduler. Returns error if the cron expression is invalid.
func New(cfg Config) (*Scheduler, error) {
	// Validate cron expression
	schedule, err := cronParser.Parse(cfg.CronExpr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", cfg.CronExpr, err)
	}

	// Ensure output dir
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	// State directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	stateDir := filepath.Join(homeDir, ".micelio", "schedules")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}

	if cfg.Source == "" {
		cfg.Source = "cli"
	}
	if err := cfg.Precheck.Validate(); err != nil {
		return nil, err
	}
	id := scheduleID(cfg.URL, cfg.CronExpr+cfg.Name)
	ctx, cancel := context.WithCancel(context.Background())

	s := &Scheduler{
		config:   cfg,
		stateDir: stateDir,
		ctx:      ctx,
		cancel:   cancel,
	}

	// Load or initialize state
	if existing := s.loadState(id); existing != nil {
		s.state = *existing
		s.state.OutputDir = cfg.OutputDir
	} else {
		s.state = State{
			ID:        id,
			URL:       cfg.URL,
			Cron:      cfg.CronExpr,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			OutputDir: cfg.OutputDir,
		}
	}
	s.state.Name, s.state.Source = cfg.Name, cfg.Source
	s.state.CrawlConfig, s.state.Precheck = cfg.CrawlMap, cfg.Precheck
	s.state.Running, s.state.Paused = false, false
	if cfg.Webhook != nil {
		s.state.Webhook = cfg.Webhook.URL
	}

	// Calculate next run
	nextRun := schedule.Next(time.Now())
	s.state.NextRun = nextRun.UTC().Format(time.RFC3339)
	s.saveState()

	return s, nil
}

// Run starts the scheduler. Blocks until stopped, max runs reached, or context cancelled.
func (s *Scheduler) Run() error {
	fmt.Fprintf(os.Stderr, "\nSchedule: %s\n", DescribeCron(s.config.CronExpr))
	fmt.Fprintf(os.Stderr, "  URL:      %s\n", s.config.URL)
	fmt.Fprintf(os.Stderr, "  Cron:     %s\n", s.config.CronExpr)
	fmt.Fprintf(os.Stderr, "  Output:   %s/\n", s.config.OutputDir)
	if s.config.Webhook != nil {
		fmt.Fprintf(os.Stderr, "  Webhook:  %s\n", webhook.RedactURL(s.config.Webhook.URL))
	}
	if s.config.MaxRuns > 0 {
		fmt.Fprintf(os.Stderr, "  Max runs: %d\n", s.config.MaxRuns)
	}
	fmt.Fprintf(os.Stderr, "  Next run: %s\n", s.state.NextRun)
	fmt.Fprintf(os.Stderr, "\nWaiting for next scheduled run... (Ctrl+C to stop)\n\n")

	s.cron = cron.New(cron.WithParser(cronParser))
	s.cron.AddFunc(s.config.CronExpr, func() {
		s.runOnce()
	})
	s.cron.Start()

	// Wait for context cancellation
	<-s.ctx.Done()

	// Wait for current crawl to finish
	s.mu.Lock()
	wasCrawling := s.crawling
	s.mu.Unlock()

	if wasCrawling {
		fmt.Fprintf(os.Stderr, "\nShutdown requested — waiting for current crawl to finish...\n")
		// Poll until crawl finishes
		for {
			time.Sleep(500 * time.Millisecond)
			s.mu.Lock()
			done := !s.crawling
			s.mu.Unlock()
			if done {
				break
			}
		}
	}

	s.cron.Stop()
	fmt.Fprintf(os.Stderr, "Scheduler stopped.\n")
	return nil
}

// Stop signals the scheduler to shut down gracefully.
func (s *Scheduler) Stop() {
	s.cancel()
}

// State returns a copy of the current schedule state.
func (s *Scheduler) GetState() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// RunNow starts a run immediately (in the background) unless one is running.
func (s *Scheduler) RunNow() bool {
	s.mu.Lock()
	busy := s.crawling
	s.mu.Unlock()
	if busy {
		return false
	}
	go s.runOnce()
	return true
}

func (s *Scheduler) runOnce() {
	s.mu.Lock()
	if s.crawling {
		s.mu.Unlock()
		log.Printf("[scheduler] skipping run — previous crawl still in progress")
		return
	}
	if s.config.MaxRuns > 0 && s.state.TotalRuns >= s.config.MaxRuns {
		s.mu.Unlock()
		fmt.Fprintf(os.Stderr, "Reached max runs (%d). Scheduler stopping.\n", s.config.MaxRuns)
		s.cancel()
		return
	}
	s.crawling = true
	s.state.Running = true
	runNumber := s.state.TotalRuns + 1
	s.mu.Unlock()
	s.saveState()

	startTime := time.Now()
	fmt.Fprintf(os.Stderr, "\n%s\n", strings.Repeat("=", 60))
	fmt.Fprintf(os.Stderr, "Scheduled run #%d starting at %s\n", runNumber, startTime.Format(time.RFC3339))
	fmt.Fprintf(os.Stderr, "%s\n\n", strings.Repeat("=", 60))

	var res RunResult
	if pc := s.config.Precheck; pc.Enabled() {
		check := pc.Run(s.ctx)
		s.recordCheck(check)
		if !check.OK {
			res = RunResult{Status: "skipped", Reason: check.Reason}
			fmt.Fprintf(os.Stderr, "Skipped: %s\n", check.Reason)
		}
	}
	if res.Status == "" {
		ctl := newRunControl()
		runCtx, stopMonitor := context.WithCancel(s.ctx)
		monitorDone := make(chan struct{})
		go func() {
			defer close(monitorDone)
			s.monitorPrecheck(runCtx, ctl)
		}()
		runner := s.config.Runner
		if runner == nil {
			runner = directRunner{}
		}
		res = runner.Run(s.ctx, s.config, ctl)
		stopMonitor()
		// Wait for the monitor so it cannot save a stale paused state
		// after the run has finished.
		<-monitorDone
	}

	durationMs := time.Since(startTime).Milliseconds()
	fmt.Fprintf(os.Stderr, "\nRun #%d %s (%d pages, %.1fs)\n", runNumber, res.Status, res.Pages, float64(durationMs)/1000)
	if res.Output != "" {
		fmt.Fprintf(os.Stderr, "Output: %s\n", res.Output)
	}

	// Update state
	schedule, _ := cronParser.Parse(s.config.CronExpr)
	nextRun := schedule.Next(time.Now())

	s.mu.Lock()
	s.state.LastRun = startTime.UTC().Format(time.RFC3339)
	s.state.LastStatus = res.Status
	s.state.LastReason = res.Reason
	s.state.LastPages = res.Pages
	s.state.LastDurationMs = durationMs
	if res.CrawlID != "" {
		s.state.LastCrawlID = res.CrawlID
	}
	s.state.NextRun = nextRun.UTC().Format(time.RFC3339)
	if res.Status != "skipped" {
		s.state.TotalRuns++
	}
	s.state.Running, s.state.Paused = false, false
	s.crawling = false
	totalRuns := s.state.TotalRuns
	s.mu.Unlock()

	s.saveState()

	// Send webhook
	if s.config.Webhook != nil {
		fmt.Fprintf(os.Stderr, "Sending webhook notification...\n")
		event := map[string]string{"failed": "crawl_failed", "skipped": "crawl_skipped", "stopped": "crawl_stopped"}[res.Status]
		if event == "" {
			event = "crawl_complete"
		}
		webhook.Send(*s.config.Webhook, webhook.Payload{
			Event:      event,
			URL:        s.config.URL,
			Timestamp:  startTime.UTC().Format(time.RFC3339),
			Duration:   fmt.Sprintf("%.1fs", float64(durationMs)/1000),
			Pages:      res.Pages,
			OutputFile: res.Output,
			Reason:     res.Reason,
			Schedule: &webhook.Schedule{
				Cron:      s.config.CronExpr,
				RunNumber: totalRuns,
				NextRun:   nextRun.UTC().Format(time.RFC3339),
			},
		})
	}

	// Check max runs after completion
	if s.config.MaxRuns > 0 && totalRuns >= s.config.MaxRuns {
		fmt.Fprintf(os.Stderr, "\nReached max runs (%d). Scheduler stopping.\n", s.config.MaxRuns)
		s.cancel()
		return
	}

	fmt.Fprintf(os.Stderr, "\nNext run: %s\nWaiting... (Ctrl+C to stop)\n", nextRun.Format(time.RFC3339))
}

// monitorPrecheck re-runs the check during a crawl and pauses or resumes it.
func (s *Scheduler) monitorPrecheck(ctx context.Context, ctl *RunControl) {
	pc := s.config.Precheck
	if !pc.Enabled() {
		return
	}
	t := time.NewTicker(pc.Interval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		check := pc.Run(ctx)
		if ctx.Err() != nil {
			return
		}
		s.recordCheck(check)
		if ctl.set(!check.OK, check.Reason) {
			s.mu.Lock()
			s.state.Paused = !check.OK
			if !check.OK {
				s.state.LastReason = "paused: " + check.Reason
			}
			s.mu.Unlock()
			s.saveState()
			if check.OK {
				fmt.Fprintf(os.Stderr, "\nCheck passes again, resuming crawl\n")
			} else {
				fmt.Fprintf(os.Stderr, "\nCheck failed (%s), pausing crawl\n", check.Reason)
			}
		}
	}
}

func (s *Scheduler) recordCheck(r PrecheckResult) {
	s.mu.Lock()
	s.state.LastCheck = &r
	s.mu.Unlock()
}

// CheckNow runs the configured check once and records the result.
func (s *Scheduler) CheckNow(ctx context.Context) PrecheckResult {
	r := s.config.Precheck.Run(ctx)
	s.recordCheck(r)
	s.saveState()
	return r
}

// directRunner runs crawler.Crawl in-process and writes the output file. It
// cannot pause a crawl, so a failing check stops the run (partial output is
// kept).
type directRunner struct{}

func (directRunner) Run(ctx context.Context, sc Config, ctl *RunControl) RunResult {
	outputFile := buildOutputFilename(sc.URL, sc.OutputDir, sc.CrawlConfig.OutputFormat)
	cfg := sc.CrawlConfig
	cfg.OutputPath = outputFile
	if cfg.SeedURL == "" {
		cfg.SeedURL = sc.URL
	}
	crawlCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	stopReason := ""
	go func() {
		for {
			select {
			case <-crawlCtx.Done():
				return
			case <-ctl.Changed():
				if paused, reason := ctl.Paused(); paused {
					mu.Lock()
					stopReason = reason
					mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	result, err := crawler.Crawl(crawlCtx, cfg, func(p crawler.CrawlProgress) {
		fmt.Fprintf(os.Stderr, "\r  [%d/%d] %.1f p/s — %s", p.Crawled, p.TotalSeen, p.Rate, p.CurrentURL)
	})
	res := RunResult{Status: "success", Output: outputFile}
	mu.Lock()
	defer mu.Unlock()
	if result != nil {
		res.Pages = len(result.Pages)
	}
	switch {
	case stopReason != "":
		res.Status, res.Reason = "stopped", "check failed during crawl: "+stopReason
	case err != nil:
		res.Status, res.Reason = "failed", err.Error()
		log.Printf("scheduled crawl error: %v", err)
	}
	return res
}

// ── State persistence ──

func (s *Scheduler) saveState() {
	s.mu.Lock()
	stateCopy := s.state
	s.mu.Unlock()

	data, err := json.MarshalIndent(stateCopy, "", "  ")
	if err != nil {
		log.Printf("[scheduler] save state error: %v", err)
		return
	}
	path := filepath.Join(s.stateDir, stateCopy.ID+".json")
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		log.Printf("[scheduler] write state error: %v", err)
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		log.Printf("[scheduler] rename state file error: %v", err)
	}
}

func (s *Scheduler) loadState(id string) *State {
	path := filepath.Join(s.stateDir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return &state
}

// ListSchedules returns all saved schedule states.
func ListSchedules() ([]State, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	stateDir := filepath.Join(homeDir, ".micelio", "schedules")
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var schedules []State
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(stateDir, e.Name()))
		if err != nil {
			continue
		}
		var s State
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		schedules = append(schedules, s)
	}
	return schedules, nil
}

// DeleteSchedule removes a schedule's state file.
func DeleteSchedule(id string) error {
	// Validate ID to prevent path traversal
	if id != filepath.Base(id) || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid schedule id")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	path := filepath.Join(homeDir, ".micelio", "schedules", id+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ── Helpers ──

func scheduleID(siteURL, cronExpr string) string {
	hostname := "unknown"
	if parsed, err := url.Parse(siteURL); err == nil && parsed.Host != "" {
		hostname = strings.ReplaceAll(parsed.Hostname(), ".", "-")
	}
	hash := fmt.Sprintf("%x", md5.Sum([]byte(siteURL+":"+cronExpr)))[:6]
	return hostname + "-" + hash
}

func buildOutputFilename(siteURL, outputDir string, format types.OutputFormat) string {
	hostname := "crawl"
	if parsed, err := url.Parse(siteURL); err == nil && parsed.Host != "" {
		hostname = strings.ReplaceAll(parsed.Hostname(), ".", "-")
	}
	ts := time.Now().UTC().Format("2006-01-02T15-04")
	ext := "jsonl"
	if format == types.FormatCSV {
		ext = "csv"
	}
	return filepath.Join(outputDir, fmt.Sprintf("%s-%s.%s", hostname, ts, ext))
}

// DescribeCron returns a human-readable description of a cron expression.
func DescribeCron(expr string) string {
	lower := strings.TrimSpace(strings.ToLower(expr))
	switch lower {
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
	}

	// Parse standard 5-field cron: minute hour dom month dow
	fields := strings.Fields(lower)
	if len(fields) != 5 {
		return "Cron: " + expr
	}
	min, hour, dom, mon, dow := fields[0], fields[1], fields[2], fields[3], fields[4]
	if min == "*" && hour == "*" && dom == "*" && mon == "*" && dow == "*" {
		return "Every minute"
	}
	if strings.HasPrefix(min, "*/") && hour == "*" && dom == "*" && mon == "*" && dow == "*" {
		return "Every " + strings.TrimPrefix(min, "*/") + " minutes"
	}

	timeStr := describeCronTime(min, hour)

	dayNames := map[string]string{
		"0": "Sunday", "1": "Monday", "2": "Tuesday", "3": "Wednesday",
		"4": "Thursday", "5": "Friday", "6": "Saturday", "7": "Sunday",
		"sun": "Sunday", "mon": "Monday", "tue": "Tuesday", "wed": "Wednesday",
		"thu": "Thursday", "fri": "Friday", "sat": "Saturday",
	}
	monthNames := map[string]string{
		"1": "January", "2": "February", "3": "March", "4": "April",
		"5": "May", "6": "June", "7": "July", "8": "August",
		"9": "September", "10": "October", "11": "November", "12": "December",
		"jan": "January", "feb": "February", "mar": "March", "apr": "April",
		"may": "May", "jun": "June", "jul": "July", "aug": "August",
		"sep": "September", "oct": "October", "nov": "November", "dec": "December",
	}

	// Every N minutes pattern: */N * * * *
	if strings.HasPrefix(min, "*/") && hour == "*" && dom == "*" && mon == "*" && dow == "*" {
		return "Every " + min[2:] + " minutes"
	}

	// Every day at specific time: M H * * *
	if dom == "*" && mon == "*" && dow == "*" {
		return "Every day" + timeStr
	}

	// Specific day of week: M H * * D
	if dom == "*" && mon == "*" && dow != "*" {
		if name, ok := dayNames[dow]; ok {
			return "Every " + name + timeStr
		}
		return "Cron: " + expr
	}

	// Specific day of month: M H D * *
	if dow == "*" && mon == "*" && dom != "*" {
		return "Day " + dom + " of every month" + timeStr
	}

	// Specific month and day: M H D Mo *
	if dow == "*" && mon != "*" && dom != "*" {
		mName := monthNames[mon]
		if mName == "" {
			mName = mon
		}
		return mName + " " + dom + timeStr
	}

	return "Cron: " + expr
}

// describeCronTime formats minute and hour fields into a readable time suffix.
func describeCronTime(min, hour string) string {
	if min == "*" || hour == "*" {
		return ""
	}
	if len(min) == 1 {
		min = "0" + min
	}
	if len(hour) == 1 {
		hour = "0" + hour
	}
	return " at " + hour + ":" + min
}
