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

// State tracks a schedule's run history.
type State struct {
	ID             string `json:"id"`
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
}

// Config defines a scheduled crawl.
type Config struct {
	Webhook     *webhook.Options
	URL         string
	CronExpr    string
	OutputDir   string
	CrawlConfig types.CrawlConfig
	MaxRuns     int
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

	id := scheduleID(cfg.URL, cfg.CronExpr)
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
	runNumber := s.state.TotalRuns + 1
	s.mu.Unlock()

	startTime := time.Now()
	fmt.Fprintf(os.Stderr, "\n%s\n", strings.Repeat("=", 60))
	fmt.Fprintf(os.Stderr, "Scheduled run #%d starting at %s\n", runNumber, startTime.Format(time.RFC3339))
	fmt.Fprintf(os.Stderr, "%s\n\n", strings.Repeat("=", 60))

	// Build output filename
	outputFile := buildOutputFilename(s.config.URL, s.config.OutputDir, s.config.CrawlConfig.OutputFormat)

	// Configure crawl
	cfg := s.config.CrawlConfig
	cfg.OutputPath = outputFile
	if cfg.SeedURL == "" {
		cfg.SeedURL = s.config.URL
	}

	// Run crawl
	result, err := crawler.Crawl(s.ctx, cfg, func(p crawler.CrawlProgress) {
		fmt.Fprintf(os.Stderr, "\r  [%d/%d] %.1f p/s — %s",
			p.Crawled, p.TotalSeen, p.Rate, p.CurrentURL)
	})

	durationMs := time.Since(startTime).Milliseconds()
	status := "success"
	pages := 0
	if err != nil {
		status = "failed"
		log.Printf("scheduled crawl error: %v", err)
	} else {
		pages = len(result.Pages)
	}

	fmt.Fprintf(os.Stderr, "\nRun #%d %s (%d pages, %.1fs)\n", runNumber, status, pages, float64(durationMs)/1000)
	fmt.Fprintf(os.Stderr, "Output: %s\n", outputFile)

	// Update state
	schedule, _ := cronParser.Parse(s.config.CronExpr)
	nextRun := schedule.Next(time.Now())

	s.mu.Lock()
	s.state.LastRun = startTime.UTC().Format(time.RFC3339)
	s.state.LastStatus = status
	s.state.LastPages = pages
	s.state.LastDurationMs = durationMs
	s.state.NextRun = nextRun.UTC().Format(time.RFC3339)
	s.state.TotalRuns++
	s.crawling = false
	totalRuns := s.state.TotalRuns
	s.mu.Unlock()

	s.saveState()

	// Send webhook
	if s.config.Webhook != nil {
		fmt.Fprintf(os.Stderr, "Sending webhook notification...\n")
		event := "crawl_complete"
		if status == "failed" {
			event = "crawl_failed"
		}
		webhook.Send(*s.config.Webhook, webhook.Payload{
			Event:      event,
			URL:        s.config.URL,
			Timestamp:  startTime.UTC().Format(time.RFC3339),
			Duration:   fmt.Sprintf("%.1fs", float64(durationMs)/1000),
			Pages:      pages,
			OutputFile: outputFile,
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
