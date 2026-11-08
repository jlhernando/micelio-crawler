package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/scheduler"
	"github.com/micelio/micelio/internal/webhook"
)

// Scheduled crawls created from the dashboard run as normal crawl jobs, so
// every run appears in History and can be compared, alerted on and opened
// like any other crawl.

const (
	scheduleBusyWait  = 30 * time.Minute // wait this long for another crawl to finish
	scheduleMaxPaused = 6 * time.Hour    // stop & analyze after this long paused
)

// jobRunner runs one scheduled crawl through the CrawlManager.
type jobRunner struct {
	store   *UiStore
	manager *CrawlManager
}

func (j jobRunner) Run(ctx context.Context, sc scheduler.Config, ctl *scheduler.RunControl) scheduler.RunResult {
	config := map[string]interface{}{}
	for k, v := range sc.CrawlMap {
		config[k] = v
	}
	if config["seedUrl"] == nil && config["url"] == nil && config["mode"] != "list" && config["mode"] != "sitemap" {
		config["seedUrl"] = sc.URL
	}
	state := map[string]interface{}{"scheduleUrl": sc.URL, "scheduleCron": sc.CronExpr}
	if sc.Name != "" {
		state["scheduleName"] = sc.Name
	}
	config["schedule"] = state

	job, err := j.store.CreateCrawlJob(config)
	if err != nil {
		return scheduler.RunResult{Status: "failed", Reason: err.Error()}
	}
	deadline := time.Now().Add(scheduleBusyWait)
	for {
		err = j.manager.StartCrawl(job.ID, config)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "already running") || time.Now().After(deadline) {
			j.store.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "failed"})
			j.store.AddCrawlEvent(job.ID, "skipped", err.Error())
			return scheduler.RunResult{Status: "skipped", Reason: "another crawl was still running", CrawlID: job.ID}
		}
		select {
		case <-ctx.Done():
			j.store.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "cancelled"})
			return scheduler.RunResult{Status: "skipped", Reason: "schedule stopped", CrawlID: job.ID}
		case <-time.After(time.Minute):
		}
	}

	cur, stoppedForCheck, stopped := j.manager.superviseJob(ctx, job.ID, ctl)
	if stopped {
		// The schedule was stopped or deleted: leave the crawl running.
		return scheduler.RunResult{Status: "success", Reason: "schedule stopped while the crawl was running", CrawlID: job.ID}
	}
	if cur == nil {
		return scheduler.RunResult{Status: "failed", Reason: "crawl deleted", CrawlID: job.ID}
	}
	if cur.Status == "completed" {
		res := scheduler.RunResult{Status: "success", Pages: cur.PageCount, CrawlID: job.ID}
		if stoppedForCheck != "" {
			res.Status, res.Reason = "stopped", fmt.Sprintf("paused over %s: %s", scheduleMaxPaused, stoppedForCheck)
		}
		return res
	}
	return scheduler.RunResult{Status: "failed", Reason: "crawl " + cur.Status, Pages: cur.PageCount, CrawlID: job.ID}
}

// superviseJob pauses a running crawl job while ctl says so and resumes it
// when ctl clears, and stops & analyzes it after scheduleMaxPaused paused.
// It only resumes pauses it made itself: a crawl the user paused stays
// paused, and a user resuming a crawl it paused takes over (no auto stop).
// It returns the job once it is completed, failed or cancelled (nil if it
// was deleted), with the check failure that stopped it, if any. stopped is
// true when ctx ended first.
func (m *CrawlManager) superviseJob(ctx context.Context, jobID string, ctl *scheduler.RunControl) (cur *CrawlJob, stoppedForCheck string, stopped bool) {
	var pausedSince time.Time // set while this supervisor holds the crawl paused
	poll := time.NewTicker(3 * time.Second)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, "", true
		case <-ctl.Changed():
			if paused, reason := ctl.Paused(); paused {
				if pausedSince.IsZero() && m.isCrawling(jobID) && m.PauseCrawl(jobID) == nil {
					pausedSince = time.Now()
					m.store.AddCrawlEvent(jobID, "paused", "check failed: "+reason)
				}
			} else if !pausedSince.IsZero() {
				pausedSince = time.Time{}
				if err := m.ResumeCrawl(jobID); err == nil {
					m.store.AddCrawlEvent(jobID, "resumed", "check passes again")
				}
			}
		case <-poll.C:
		}
		job, err := m.store.GetCrawlJob(jobID)
		if err != nil {
			continue
		}
		if job == nil {
			return nil, stoppedForCheck, false // deleted
		}
		switch job.Status {
		case "completed", "failed", "cancelled":
			return job, stoppedForCheck, false
		case "running":
			if stoppedForCheck == "" {
				pausedSince = time.Time{} // resumed by the user: their call
			}
		}
		if !pausedSince.IsZero() && time.Since(pausedSince) > scheduleMaxPaused && stoppedForCheck == "" {
			_, reason := ctl.Paused()
			stoppedForCheck = reason
			m.ResumeCrawl(jobID)
			m.StopCrawl(jobID) // stop & analyze what was crawled
		}
	}
}

// isCrawling reports whether jobID is the active crawl and is neither
// paused nor stopping.
func (m *CrawlManager) isCrawling(jobID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active != nil && m.active.ID == jobID && !m.active.Paused && !m.active.Stopping
}

// scheduleRequest is the body of POST /api/schedules. config uses the same
// keys as POST /api/crawl/start (what the New Crawl page sends).
type scheduleRequest struct {
	URL       string                 `json:"url"`
	Name      string                 `json:"name"`
	Cron      string                 `json:"cron"`
	OutputDir string                 `json:"outputDir"`
	Webhook   string                 `json:"webhook"`
	Config    map[string]interface{} `json:"config"`
	Precheck  *scheduler.Precheck    `json:"precheck"`
	// Legacy flat fields (older dashboards).
	Depth, Limit, Concurrency, Delay *float64

	// resumed marks a saved schedule restarted at server start: a config the
	// newer checks refuse is fixed (bad key files, snippets and outputDir
	// dropped, numbers clamped) and noted in warning instead of refused, so
	// the schedule keeps running.
	resumed bool
	warning string
}

const defaultScheduleOutputDir = "scheduled-crawls"

// startUISchedule validates a dashboard schedule, saves it and starts it.
// The schedule is tracked before its cron starts, so it can always be
// stopped and deleted.
func (m *CrawlManager) startUISchedule(store *UiStore, req scheduleRequest) (*scheduler.Scheduler, error) {
	if req.Cron == "" {
		return nil, fmt.Errorf("cron is required")
	}
	config := req.Config
	if config == nil {
		config = map[string]interface{}{}
		for k, v := range map[string]*float64{"depth": req.Depth, "limit": req.Limit, "concurrency": req.Concurrency, "delay": req.Delay} {
			if v != nil {
				config[k] = *v
			}
		}
	}
	// The same checks as POST /api/crawl/start: a schedule must not keep
	// (and re-run) a config the start endpoint would refuse.
	var warnings []string
	if req.resumed {
		// A saved proxy that is no longer trusted fails each run
		// (runCrawl), with a clear event, instead of dropping the schedule.
		warnings = relaxCrawlConfig(config)
	} else if err := m.applyTrustedProxy(config); err != nil {
		return nil, err
	} else if err := sanitizeCrawlConfig(config); err != nil {
		return nil, err
	}
	siteURL := req.URL
	if siteURL == "" {
		for _, k := range []string{"seedUrl", "url"} {
			if s, ok := config[k].(string); ok && s != "" {
				siteURL = s
				break
			}
		}
	}
	if siteURL == "" {
		if s, ok := config["sitemapUrls"].([]interface{}); ok && len(s) > 0 {
			siteURL, _ = s[0].(string)
		}
	}
	if siteURL == "" {
		return nil, fmt.Errorf("url is required")
	}
	if crawler.IsPrivateURL(siteURL) {
		return nil, fmt.Errorf("seed URL targets a private/loopback address")
	}
	if req.Precheck != nil && req.Precheck.Command != "" {
		return nil, fmt.Errorf("precheck commands can only be set from the CLI")
	}
	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = defaultScheduleOutputDir
	}
	if !filepath.IsLocal(outputDir) {
		if !req.resumed {
			return nil, fmt.Errorf("outputDir must be a relative path inside the working directory")
		}
		warnings = append(warnings, fmt.Sprintf("outputDir %s is outside the working directory; using %s", outputDir, defaultScheduleOutputDir))
		outputDir = defaultScheduleOutputDir
	}
	warning := strings.Join(warnings, ". ")
	if warning == "" {
		// The first resume saved the fixed config, so later resumes find
		// nothing to fix: keep the note until the schedule is recreated.
		warning = req.warning
	}
	cfg := scheduler.Config{
		URL:       siteURL,
		Name:      strings.TrimSpace(req.Name),
		CronExpr:  req.Cron,
		OutputDir: outputDir,
		Source:    "ui",
		CrawlMap:  config,
		Precheck:  req.Precheck,
		Warning:   warning,
		Runner:    jobRunner{store: store, manager: m},
		OnRunDone: m.emailScheduleRun,
	}
	if req.Webhook != "" {
		if err := webhook.ValidateURL(req.Webhook); err != nil {
			return nil, err
		}
		cfg.Webhook = &webhook.Options{URL: req.Webhook}
	}
	// scheduler.New saves its state: reserve the ID first, so a refused
	// request does not overwrite the active schedule's config, and two
	// concurrent identical requests cannot both start a scheduler.
	id := scheduler.ID(cfg)
	if err := m.reserveSchedule(id); err != nil {
		return nil, err
	}
	if afterScheduleReserved != nil {
		afterScheduleReserved(id)
	}
	s, err := scheduler.New(cfg)
	if err != nil {
		m.releaseSchedule(id)
		return nil, err
	}
	// A delete, stop or shutdown that came in while scheduler.New ran found
	// nothing to stop: honour it here instead of starting the scheduler.
	m.mu.Lock()
	mark := m.scheduleReserved[id]
	closed := m.schedulersClosed
	delete(m.scheduleReserved, id)
	start := mark == "" && !closed
	if start {
		m.schedulers[id] = s
	}
	m.mu.Unlock()
	if !start {
		s.Stop()
		if mark == reserveDeleted {
			// scheduler.New may have saved the state after the delete removed it.
			if err := scheduler.DeleteSchedule(id); err != nil {
				log.Printf("[scheduler] remove deleted schedule %s: %v", id, err)
			}
		}
		return nil, errScheduleCancelled
	}
	go s.Run()
	return s, nil
}

// Marks for a schedule stopped or deleted while it was being created.
const (
	reserveStopped = "stopped"
	reserveDeleted = "deleted"
)

var errScheduleCancelled = errors.New("the schedule was stopped or deleted while it was being created")

// afterScheduleReserved, when set (tests only), runs between reserving a
// schedule ID and creating the scheduler.
var afterScheduleReserved func(id string)

// reserveSchedule claims id for a schedule being created. It fails when a
// scheduler with that ID runs, another request is creating it, or the
// server is shutting down.
func (m *CrawlManager) reserveSchedule(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.schedulersClosed {
		return errScheduleCancelled
	}
	if _, ok := m.schedulers[id]; ok {
		return errScheduleExists
	}
	if _, ok := m.scheduleReserved[id]; ok {
		return errScheduleExists
	}
	if m.scheduleReserved == nil {
		m.scheduleReserved = make(map[string]string)
	}
	m.scheduleReserved[id] = ""
	return nil
}

func (m *CrawlManager) releaseSchedule(id string) {
	m.mu.Lock()
	delete(m.scheduleReserved, id)
	m.mu.Unlock()
}

var errScheduleExists = errors.New("a schedule with this URL, cron and name already exists")

// ResumeUISchedules restarts dashboard schedules saved before a restart.
func (m *CrawlManager) ResumeUISchedules(store *UiStore) {
	states, err := scheduler.ListSchedules()
	if err != nil {
		return
	}
	for _, st := range states {
		if st.Source != "ui" || m.IsSchedulerActive(st.ID) {
			continue
		}
		req := scheduleRequest{URL: st.URL, Name: st.Name, Cron: st.Cron, OutputDir: st.OutputDir, Webhook: st.Webhook, Config: st.CrawlConfig, Precheck: st.Precheck,
			resumed: true, warning: st.Warning}
		if req.Precheck != nil {
			req.Precheck.Command = "" // never from saved UI state
		}
		s, err := m.startUISchedule(store, req)
		if err != nil {
			log.Printf("[scheduler] resume %s: %v", st.ID, err)
			continue
		}
		log.Printf("[scheduler] resumed %s (%s)", st.ID, scheduler.DescribeCron(st.Cron))
		if w := s.GetState().Warning; w != "" && w != st.Warning {
			log.Printf("[scheduler] %s: %s", st.ID, w)
		}
	}
}

func (m *CrawlManager) trackedScheduler(id string) *scheduler.Scheduler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.schedulers[id]
}

func registerScheduleRoutes(mux *http.ServeMux, store *UiStore, manager *CrawlManager) {
	mux.HandleFunc("POST /api/schedules", func(w http.ResponseWriter, r *http.Request) {
		var req scheduleRequest
		if err := decodeJSONBody(r, &req); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		s, err := manager.startUISchedule(store, req)
		if errors.Is(err, errScheduleExists) || errors.Is(err, errScheduleCancelled) {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		state := s.GetState()
		jsonOK(w, map[string]interface{}{
			"id": state.ID, "url": redactURL(state.URL), "cron": state.Cron, "name": state.Name,
			"description": scheduler.DescribeCron(state.Cron), "nextRun": state.NextRun,
		})
	})

	mux.HandleFunc("POST /api/schedules/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		s := manager.trackedScheduler(r.PathValue("id"))
		if s == nil {
			jsonError(w, "schedule is not active", http.StatusNotFound)
			return
		}
		if !s.RunNow() {
			jsonError(w, "a run is already in progress", http.StatusConflict)
			return
		}
		jsonOK(w, map[string]bool{"started": true})
	})

	mux.HandleFunc("POST /api/schedules/{id}/check", func(w http.ResponseWriter, r *http.Request) {
		s := manager.trackedScheduler(r.PathValue("id"))
		if s == nil {
			jsonError(w, "schedule is not active", http.StatusNotFound)
			return
		}
		jsonOK(w, s.CheckNow(r.Context()))
	})

	// Try a check before saving a schedule.
	mux.HandleFunc("POST /api/schedules/check", func(w http.ResponseWriter, r *http.Request) {
		var pc scheduler.Precheck
		if err := decodeJSONBody(r, &pc); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		if pc.Command != "" {
			jsonError(w, "precheck commands can only be set from the CLI", http.StatusBadRequest)
			return
		}
		if err := pc.Validate(); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, pc.Run(r.Context()))
	})
}
