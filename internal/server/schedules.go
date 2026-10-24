package server

import (
	"context"
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

	var pausedSince time.Time
	stoppedForCheck := ""
	poll := time.NewTicker(3 * time.Second)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			// The schedule was stopped or deleted: leave the crawl running.
			return scheduler.RunResult{Status: "success", Reason: "schedule stopped while the crawl was running", CrawlID: job.ID}
		case <-ctl.Changed():
			if paused, reason := ctl.Paused(); paused {
				if err := j.manager.PauseCrawl(job.ID); err == nil {
					pausedSince = time.Now()
					j.store.AddCrawlEvent(job.ID, "paused", "check failed: "+reason)
				}
			} else if !pausedSince.IsZero() {
				if err := j.manager.ResumeCrawl(job.ID); err == nil {
					pausedSince = time.Time{}
					j.store.AddCrawlEvent(job.ID, "resumed", "check passes again")
				}
			}
		case <-poll.C:
		}
		if !pausedSince.IsZero() && time.Since(pausedSince) > scheduleMaxPaused && stoppedForCheck == "" {
			_, reason := ctl.Paused()
			stoppedForCheck = reason
			j.manager.ResumeCrawl(job.ID)
			j.manager.StopCrawl(job.ID) // stop & analyze what was crawled
		}
		cur, err := j.store.GetCrawlJob(job.ID)
		if err != nil || cur == nil {
			continue
		}
		switch cur.Status {
		case "completed":
			res := scheduler.RunResult{Status: "success", Pages: cur.PageCount, CrawlID: job.ID}
			if stoppedForCheck != "" {
				res.Status, res.Reason = "stopped", fmt.Sprintf("paused over %s: %s", scheduleMaxPaused, stoppedForCheck)
			}
			return res
		case "failed", "cancelled":
			return scheduler.RunResult{Status: "failed", Reason: "crawl " + cur.Status, Pages: cur.PageCount, CrawlID: job.ID}
		}
	}
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
}

func (m *CrawlManager) newUISchedule(store *UiStore, req scheduleRequest) (*scheduler.Scheduler, error) {
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
		outputDir = "scheduled-crawls"
	}
	if filepath.IsAbs(outputDir) {
		return nil, fmt.Errorf("outputDir must be a relative path")
	}
	cfg := scheduler.Config{
		URL:       siteURL,
		Name:      strings.TrimSpace(req.Name),
		CronExpr:  req.Cron,
		OutputDir: outputDir,
		Source:    "ui",
		CrawlMap:  config,
		Precheck:  req.Precheck,
		Runner:    jobRunner{store: store, manager: m},
	}
	if req.Webhook != "" {
		if err := webhook.ValidateURL(req.Webhook); err != nil {
			return nil, err
		}
		cfg.Webhook = &webhook.Options{URL: req.Webhook}
	}
	return scheduler.New(cfg)
}

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
		req := scheduleRequest{URL: st.URL, Name: st.Name, Cron: st.Cron, OutputDir: st.OutputDir, Webhook: st.Webhook, Config: st.CrawlConfig, Precheck: st.Precheck}
		if req.Precheck != nil {
			req.Precheck.Command = "" // never from saved UI state
		}
		s, err := m.newUISchedule(store, req)
		if err != nil {
			log.Printf("[scheduler] resume %s: %v", st.ID, err)
			continue
		}
		go s.Run()
		m.TrackScheduler(s)
		log.Printf("[scheduler] resumed %s (%s)", st.ID, scheduler.DescribeCron(st.Cron))
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
		s, err := manager.newUISchedule(store, req)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		state := s.GetState()
		if manager.IsSchedulerActive(state.ID) {
			jsonError(w, "a schedule with this URL, cron and name already exists", http.StatusConflict)
			return
		}
		go s.Run()
		manager.TrackScheduler(s)
		jsonOK(w, map[string]interface{}{
			"id": state.ID, "url": state.URL, "cron": state.Cron, "name": state.Name,
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
