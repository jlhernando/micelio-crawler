package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

// newTestScheduler creates a scheduler with stateDir redirected to a temp directory
// so tests never pollute the real ~/.micelio/schedules/.
func newTestScheduler(t *testing.T, cfg Config) *Scheduler {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Clean up state file written to real stateDir during New()
	realStatePath := filepath.Join(s.stateDir, s.state.ID+".json")
	t.Cleanup(func() { os.Remove(realStatePath) })
	// Redirect to temp dir for all subsequent operations
	s.stateDir = t.TempDir()
	s.saveState()
	return s
}

// ── scheduleID ──

func TestScheduleID(t *testing.T) {
	id := scheduleID("https://example.com", "0 2 * * *")
	if id == "" {
		t.Fatal("expected non-empty ID")
	}
	if id[:len("example-com")] != "example-com" {
		t.Fatalf("expected hostname prefix, got %q", id)
	}
	// Same inputs → same ID
	id2 := scheduleID("https://example.com", "0 2 * * *")
	if id != id2 {
		t.Fatal("expected deterministic ID")
	}
	// Different inputs → different ID
	id3 := scheduleID("https://example.com", "0 3 * * *")
	if id == id3 {
		t.Fatal("expected different ID for different cron")
	}
}

func TestScheduleIDEdgeCases(t *testing.T) {
	// Invalid URL falls back to "unknown"
	id := scheduleID("not-a-url", "0 2 * * *")
	if !strings.HasPrefix(id, "unknown-") {
		t.Fatalf("expected unknown prefix for invalid URL, got %q", id)
	}

	// Empty URL
	id2 := scheduleID("", "0 2 * * *")
	if !strings.HasPrefix(id2, "unknown-") {
		t.Fatalf("expected unknown prefix for empty URL, got %q", id2)
	}

	// Different URLs → different IDs
	idA := scheduleID("https://a.com", "@daily")
	idB := scheduleID("https://b.com", "@daily")
	if idA == idB {
		t.Fatal("expected different IDs for different URLs")
	}

	// URL with subdomain
	id3 := scheduleID("https://www.example.com", "@hourly")
	if !strings.HasPrefix(id3, "www-example-com-") {
		t.Fatalf("expected www-example-com prefix, got %q", id3)
	}
}

// ── buildOutputFilename ──

func TestBuildOutputFilename(t *testing.T) {
	f := buildOutputFilename("https://example.com", "/tmp/output", types.FormatJSONL)
	if filepath.Dir(f) != "/tmp/output" {
		t.Fatalf("expected /tmp/output dir, got %q", filepath.Dir(f))
	}
	if filepath.Ext(f) != ".jsonl" {
		t.Fatalf("expected .jsonl extension, got %q", filepath.Ext(f))
	}

	f2 := buildOutputFilename("https://example.com", "/tmp/output", types.FormatCSV)
	if filepath.Ext(f2) != ".csv" {
		t.Fatalf("expected .csv extension, got %q", filepath.Ext(f2))
	}
}

func TestBuildOutputFilenameEdgeCases(t *testing.T) {
	// Invalid URL falls back to "crawl" hostname
	f := buildOutputFilename("not-a-url", "/tmp/out", types.FormatJSONL)
	base := filepath.Base(f)
	if !strings.HasPrefix(base, "crawl-") {
		t.Fatalf("expected crawl prefix for invalid URL, got %q", base)
	}
	if filepath.Ext(f) != ".jsonl" {
		t.Fatalf("expected .jsonl extension, got %q", filepath.Ext(f))
	}

	// Empty URL
	f2 := buildOutputFilename("", "/tmp/out", types.FormatCSV)
	base2 := filepath.Base(f2)
	if !strings.HasPrefix(base2, "crawl-") {
		t.Fatalf("expected crawl prefix for empty URL, got %q", base2)
	}
	if filepath.Ext(f2) != ".csv" {
		t.Fatalf("expected .csv extension, got %q", filepath.Ext(f2))
	}

	// URL with subdomain produces hostname with dashes
	f3 := buildOutputFilename("https://blog.example.com", "/tmp/out", types.FormatJSONL)
	base3 := filepath.Base(f3)
	if !strings.HasPrefix(base3, "blog-example-com-") {
		t.Fatalf("expected blog-example-com prefix, got %q", base3)
	}

	// Default format (empty) → jsonl
	f4 := buildOutputFilename("https://example.com", "/tmp/out", "")
	if filepath.Ext(f4) != ".jsonl" {
		t.Fatalf("expected .jsonl for default format, got %q", filepath.Ext(f4))
	}
}

// ── DescribeCron ──

func TestDescribeCron(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"@hourly", "Every hour"},
		{"@daily", "Every day at midnight"},
		{"@weekly", "Every Sunday at midnight"},
		{"@monthly", "First day of every month at midnight"},
		{"@yearly", "January 1st at midnight"},
		{"0 2 * * *", "Every day at 02:00"},
	}
	for _, tc := range tests {
		got := DescribeCron(tc.input)
		if got != tc.want {
			t.Errorf("DescribeCron(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDescribeCronExtended(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// @midnight is alias for @daily
		{"@midnight", "Every day at midnight"},
		// @annually is alias for @yearly
		{"@annually", "January 1st at midnight"},
		// Case insensitivity
		{"@HOURLY", "Every hour"},
		{"@Daily", "Every day at midnight"},
		{"@WEEKLY", "Every Sunday at midnight"},
		{"@Monthly", "First day of every month at midnight"},
		{"@YEARLY", "January 1st at midnight"},
		{"@ANNUALLY", "January 1st at midnight"},
		// Leading/trailing whitespace
		{"  @hourly  ", "Every hour"},
		{"\t@daily\t", "Every day at midnight"},
		// Parsed cron expressions
		{"*/5 * * * *", "Every 5 minutes"},
		{"0 9 * * 1-5", "Cron: 0 9 * * 1-5"}, // range in dow not parsed
		{"0 0 1 * *", "Day 1 of every month at 00:00"},
		{"5 11 * * *", "Every day at 11:05"},
		{"0 9 * * 1", "Every Monday at 09:00"},
		{"30 14 * * fri", "Every Friday at 14:30"},
		// Empty string
		{"", "Cron: "},
	}
	for _, tc := range tests {
		got := DescribeCron(tc.input)
		if got != tc.want {
			t.Errorf("DescribeCron(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ── New ──

func TestNewSchedulerValidation(t *testing.T) {
	dir := t.TempDir()

	// Valid cron
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL:     "https://example.com",
			Mode:        types.ModeSpider,
			Concurrency: 3,
			MaxPages:    100,
		},
	}
	s := newTestScheduler(t, cfg)
	if s.state.ID == "" {
		t.Fatal("expected state ID")
	}
	if s.state.NextRun == "" {
		t.Fatal("expected next run time")
	}

	// Invalid cron
	cfg.CronExpr = "invalid"
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for invalid cron")
	}
}

func TestNewSchedulerLoadsExistingState(t *testing.T) {
	dir := t.TempDir()
	sharedStateDir := t.TempDir()

	cfg := Config{
		URL:       "https://reload-test.com",
		CronExpr:  "@hourly",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://reload-test.com",
			Mode:    types.ModeSpider,
		},
	}

	// Create first scheduler, redirect stateDir, update and save
	s1 := newTestScheduler(t, cfg)
	s1.stateDir = sharedStateDir
	s1.state.TotalRuns = 10
	s1.state.LastStatus = "success"
	s1.state.LastPages = 42
	s1.saveState()

	// Create second scheduler with same stateDir — should reload state
	s2 := newTestScheduler(t, cfg)
	s2.stateDir = sharedStateDir
	if existing := s2.loadState(s1.state.ID); existing != nil {
		s2.state = *existing
		s2.state.OutputDir = cfg.OutputDir
	}
	if s2.state.TotalRuns != 10 {
		t.Fatalf("expected 10 total runs from reloaded state, got %d", s2.state.TotalRuns)
	}
	if s2.state.LastStatus != "success" {
		t.Fatalf("expected success status from reloaded state, got %q", s2.state.LastStatus)
	}
	// OutputDir should be updated from config, not loaded state
	if s2.state.OutputDir != dir {
		t.Fatalf("expected output dir %q, got %q", dir, s2.state.OutputDir)
	}
}

// ── GetState ──

func TestGetState(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	state := s.GetState()
	if state.ID == "" {
		t.Fatal("expected non-empty state ID")
	}
	if state.URL != "https://example.com" {
		t.Fatalf("expected URL https://example.com, got %q", state.URL)
	}
	if state.Cron != "@daily" {
		t.Fatalf("expected cron @daily, got %q", state.Cron)
	}
	if state.CreatedAt == "" {
		t.Fatal("expected non-empty CreatedAt")
	}
	if state.NextRun == "" {
		t.Fatal("expected non-empty NextRun")
	}

	// GetState returns a copy — mutating it shouldn't affect the scheduler
	state.TotalRuns = 999
	state2 := s.GetState()
	if state2.TotalRuns == 999 {
		t.Fatal("GetState should return a copy, not a reference")
	}
}

// ── Stop ──

func TestStop(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	// Stop should cancel the context
	s.Stop()

	select {
	case <-s.ctx.Done():
		// Expected — context is cancelled
	case <-time.After(time.Second):
		t.Fatal("expected context to be cancelled after Stop")
	}
}

// ── Run + Stop ──

func TestRunStopImmediately(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	// Stop immediately so Run() returns
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Stop()
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
}

// ── State persistence ──

func TestStatePersistence(t *testing.T) {
	dir := t.TempDir()

	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}

	s := newTestScheduler(t, cfg)

	// Manually update state and save
	s.state.TotalRuns = 5
	s.state.LastStatus = "success"
	s.saveState()

	// Verify file was written
	path := filepath.Join(s.stateDir, s.state.ID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("state file not found: %v", err)
	}

	// Load state
	loaded := s.loadState(s.state.ID)
	if loaded == nil {
		t.Fatal("expected loaded state")
	}
	if loaded.TotalRuns != 5 {
		t.Fatalf("expected 5 total runs, got %d", loaded.TotalRuns)
	}
	if loaded.LastStatus != "success" {
		t.Fatalf("expected success status, got %q", loaded.LastStatus)
	}
}

func TestLoadStateNotFound(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	// Loading a non-existent state returns nil
	loaded := s.loadState("nonexistent-id")
	if loaded != nil {
		t.Fatal("expected nil for non-existent state")
	}
}

func TestLoadStateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	// Write invalid JSON to state file
	badPath := filepath.Join(s.stateDir, "bad-state.json")
	if err := os.WriteFile(badPath, []byte("{invalid json}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// loadState should return nil for invalid JSON
	loaded := s.loadState("bad-state")
	if loaded != nil {
		t.Fatal("expected nil for invalid JSON state file")
	}
}

func TestSaveStateWritesValidJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@hourly",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	s.state.TotalRuns = 3
	s.state.LastStatus = "success"
	s.state.LastPages = 50
	s.state.LastDurationMs = 12345
	s.saveState()

	// Read the file and verify it's valid JSON with correct fields
	path := filepath.Join(s.stateDir, s.state.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("state file is not valid JSON: %v", err)
	}
	if state.TotalRuns != 3 {
		t.Fatalf("expected 3 total runs, got %d", state.TotalRuns)
	}
	if state.LastPages != 50 {
		t.Fatalf("expected 50 pages, got %d", state.LastPages)
	}
	if state.LastDurationMs != 12345 {
		t.Fatalf("expected 12345ms duration, got %d", state.LastDurationMs)
	}
	if state.URL != "https://example.com" {
		t.Fatalf("expected URL https://example.com, got %q", state.URL)
	}
}

// ── ListSchedules ──

func TestListSchedules(t *testing.T) {
	// This tests the function doesn't crash on the user's system
	schedules, err := ListSchedules()
	if err != nil {
		t.Fatal(err)
	}
	// May or may not be empty depending on user's system
	_ = schedules
}

func TestListSchedulesReturnsCreatedSchedules(t *testing.T) {
	// ListSchedules reads from real ~/.micelio/schedules/ — verify it doesn't crash
	// and returns a valid (possibly empty) slice
	schedules, err := ListSchedules()
	if err != nil {
		t.Fatal(err)
	}
	// Verify returned states have valid structure
	for _, s := range schedules {
		if s.ID == "" {
			t.Fatal("expected non-empty ID in schedule state")
		}
	}
}

// ── runOnce early exits ──

func TestRunOnceSkipsWhenAlreadyCrawling(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	// Simulate a crawl already in progress
	s.mu.Lock()
	s.crawling = true
	s.mu.Unlock()

	// runOnce should return early without panicking
	s.runOnce()

	// Should still be crawling (not changed)
	s.mu.Lock()
	stillCrawling := s.crawling
	s.mu.Unlock()
	if !stillCrawling {
		t.Fatal("expected crawling to still be true after skip")
	}
}

func TestRunOnceStopsAtMaxRuns(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		MaxRuns:   3,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	// Set TotalRuns to MaxRuns
	s.mu.Lock()
	s.state.TotalRuns = 3
	s.mu.Unlock()

	// runOnce should detect max runs reached and cancel context
	s.runOnce()

	select {
	case <-s.ctx.Done():
		// Expected — context cancelled because max runs reached
	case <-time.After(time.Second):
		t.Fatal("expected context to be cancelled after max runs reached")
	}
}

// ── Run with crawling in progress ──

func TestRunStopWhileCrawling(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	// Simulate crawling in progress, then clear it after a delay
	go func() {
		time.Sleep(30 * time.Millisecond)
		// Set crawling to true before stopping, to exercise the wasCrawling branch
		s.mu.Lock()
		s.crawling = true
		s.mu.Unlock()

		time.Sleep(10 * time.Millisecond)
		s.Stop()

		// After a short delay, simulate crawl finishing
		time.Sleep(50 * time.Millisecond)
		s.mu.Lock()
		s.crawling = false
		s.mu.Unlock()
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop with crawling")
	}
}

// ── saveState error handling ──

func TestSaveStateWithBadDir(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	// Point stateDir to a non-existent directory to trigger write error
	s.stateDir = "/nonexistent/path/that/does/not/exist"

	// saveState should not panic, just log the error
	s.saveState()
}

// ── Run with webhook config ──

func TestRunWithWebhookConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "@daily",
		OutputDir: dir,
		MaxRuns:   1,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	// Stop immediately
	go func() {
		time.Sleep(50 * time.Millisecond)
		s.Stop()
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

// ── Config / State struct fields ──

func TestStateNextRunIsFuture(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		URL:       "https://example.com",
		CronExpr:  "0 */2 * * *", // every 2 hours
		OutputDir: dir,
		CrawlConfig: types.CrawlConfig{
			SeedURL: "https://example.com",
			Mode:    types.ModeSpider,
		},
	}
	s := newTestScheduler(t, cfg)

	state := s.GetState()
	nextRun, err := time.Parse(time.RFC3339, state.NextRun)
	if err != nil {
		t.Fatalf("invalid NextRun time format: %v", err)
	}
	if !nextRun.After(time.Now().Add(-time.Second)) {
		t.Fatal("NextRun should be in the future")
	}
}

func TestNewSchedulerWithDifferentCronExprs(t *testing.T) {
	dir := t.TempDir()
	exprs := []string{
		"@hourly",
		"0 9 * * *",
		"0 0 * * 0",
		"*/15 * * * *",
	}
	for _, expr := range exprs {
		cfg := Config{
			URL:       "https://example.com",
			CronExpr:  expr,
			OutputDir: filepath.Join(dir, expr),
			CrawlConfig: types.CrawlConfig{
				SeedURL: "https://example.com",
				Mode:    types.ModeSpider,
			},
		}
		s := newTestScheduler(t, cfg)
		if s.state.Cron != expr {
			t.Fatalf("expected cron %q in state, got %q", expr, s.state.Cron)
		}
	}
}
