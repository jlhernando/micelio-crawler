package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/crawler"
	"github.com/micelio/micelio/internal/scheduler"
	"github.com/micelio/micelio/internal/types"
)

// fakeHome points ~ at a temp dir with ~/.micelio/keys/sa.json and
// ~/.micelio/snippets/s.js.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, f := range []string{".micelio/keys/sa.json", ".micelio/snippets/s.js"} {
		p := filepath.Join(home, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestSanitizeCrawlConfigDropsServerPaths(t *testing.T) {
	fakeHome(t)
	m := map[string]interface{}{"seedUrl": "https://example.com/", "outputPath": "/Users/x/.zshrc", "outputFormat": "csv", "dbPath": "/tmp/x.db"}
	if err := sanitizeCrawlConfig(m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["outputPath"]; ok {
		t.Error("outputPath kept")
	}
	if _, ok := m["dbPath"]; ok {
		t.Error("dbPath kept")
	}
}

func TestSanitizeCrawlConfigKeyFiles(t *testing.T) {
	home := fakeHome(t)
	keys := filepath.Join(home, ".micelio", "keys")
	outside := filepath.Join(home, "secret.json")
	os.WriteFile(outside, []byte("{}"), 0o600)
	os.Symlink(outside, filepath.Join(keys, "link.json"))

	ok := []string{"sa.json", filepath.Join(keys, "sa.json")}
	for _, v := range ok {
		m := map[string]interface{}{"gsc": true, "gscKeyFile": v}
		if err := sanitizeCrawlConfig(m); err != nil {
			t.Errorf("%q refused: %v", v, err)
		} else if m["gscKeyFile"] != filepath.Join(keys, "sa.json") {
			t.Errorf("%q resolved to %v", v, m["gscKeyFile"])
		}
	}
	bad := []string{outside, "../secret.json", keys + "/../secret.json", "/etc/passwd", "link.json", "missing.json", "..", "sub/sa.json"}
	for _, v := range bad {
		m := map[string]interface{}{"ga4": true, "ga4KeyFile": v}
		if err := sanitizeCrawlConfig(m); err == nil {
			t.Errorf("%q accepted with ga4 on: %v", v, m["ga4KeyFile"])
		}
		// With the integration off the path is dropped, not an error.
		m = map[string]interface{}{"ga4KeyFile": v}
		if err := sanitizeCrawlConfig(m); err != nil {
			t.Errorf("%q with ga4 off: %v", v, err)
		}
		if _, ok := m["ga4KeyFile"]; ok {
			t.Errorf("%q kept with ga4 off", v)
		}
	}
}

func TestSanitizeCrawlConfigSnippets(t *testing.T) {
	home := fakeHome(t)
	m := map[string]interface{}{"jsRendering": true, "snippetPaths": []interface{}{"s.js"}}
	if err := sanitizeCrawlConfig(m); err != nil {
		t.Fatal(err)
	}
	if got := m["snippetPaths"].([]interface{}); len(got) != 1 || got[0] != filepath.Join(home, ".micelio", "snippets", "s.js") {
		t.Errorf("snippets = %v", got)
	}
	m = map[string]interface{}{"jsRendering": true, "snippetPaths": []interface{}{"/etc/hosts"}}
	if err := sanitizeCrawlConfig(m); err == nil {
		t.Error("snippet outside ~/.micelio/snippets accepted")
	}
}

func TestSanitizeCrawlConfigLimits(t *testing.T) {
	fakeHome(t)
	for _, m := range []map[string]interface{}{
		{"limit": 1e12},
		{"maxPages": float64(limits().MaxCrawlPages + 1)},
		{"concurrency": float64(5000)},
		{"depth": float64(1e9)},
		{"liMaxPages": 1e15},
	} {
		if err := sanitizeCrawlConfig(m); err == nil {
			t.Errorf("%v accepted", m)
		}
	}
	// The documented 250K-page stress test fits.
	if err := sanitizeCrawlConfig(map[string]interface{}{"limit": float64(250_000), "concurrency": float64(20), "depth": float64(20)}); err != nil {
		t.Errorf("stress test config refused: %v", err)
	}
}

func TestConfineCrawlConfig(t *testing.T) {
	home := fakeHome(t)
	c := types.CrawlConfig{
		OutputPath: "/tmp/out.csv", DBPath: "/tmp/x.db", GSCKeyFile: "/etc/passwd",
		GA4KeyFile:   filepath.Join(home, ".micelio", "keys", "sa.json"),
		SnippetPaths: []string{"/etc/hosts", "s.js"},
		MaxPages:     1 << 40, Concurrency: 1 << 20, MaxDepth: 1 << 20,
		GSC: true, GA4: true, JSRendering: true,
	}
	warnings := confineCrawlConfig(&c)
	if len(warnings) != 2 || !strings.Contains(warnings[0], "GSC key file not used") || !strings.Contains(warnings[1], "~/.micelio/snippets") {
		t.Errorf("warnings = %q", warnings)
	}
	if c.OutputPath != "" || c.DBPath != "" || c.GSCKeyFile != "" {
		t.Errorf("paths kept: %+v", c)
	}
	if c.GA4KeyFile == "" || len(c.SnippetPaths) != 1 {
		t.Errorf("confined paths dropped: %q %v", c.GA4KeyFile, c.SnippetPaths)
	}
	if c.MaxPages != limits().MaxCrawlPages || c.Concurrency != maxCrawlConcurrency || c.MaxDepth != maxCrawlDepth {
		t.Errorf("not clamped: pages %d conc %d depth %d", c.MaxPages, c.Concurrency, c.MaxDepth)
	}
}

func TestCrawlStartRejectsUnsafeConfig(t *testing.T) {
	fakeHome(t)
	api, s, _ := newTestAPI(t)
	for _, body := range []string{
		`{"seedUrl":"https://example.com/","limit":1000000000000}`,
		`{"seedUrl":"https://example.com/","concurrency":100000}`,
		`{"seedUrl":"https://example.com/","gsc":true,"gscKeyFile":"/etc/passwd"}`,
	} {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/start", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, rec.Code)
		}
	}
	if jobs, _ := s.ListCrawlJobs(); len(jobs) != 0 {
		t.Errorf("refused configs created %d jobs", len(jobs))
	}
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/x/restart", strings.NewReader(`{"outputPath":"/x","limit":1e13}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "page limit") {
		t.Errorf("restart override: %d %s", rec.Code, rec.Body.String())
	}
}

// A crawl started with a saved config holding outputPath (configs saved
// before the sanitiser) must not create or truncate that file.
func TestRunCrawlIgnoresOutputPath(t *testing.T) {
	fakeHome(t)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>$(touch /tmp/pwned)</title></head><body>hi</body></html>`)
	}))
	defer site.Close()
	_, s, m := newTestAPI(t)
	m.SkipSSRF = true
	victim := filepath.Join(t.TempDir(), ".zshrc")
	os.WriteFile(victim, []byte("keep me"), 0o600)

	cfg := map[string]interface{}{"seedUrl": site.URL + "/", "limit": 2, "concurrency": 1, "delay": 0,
		"outputPath": victim, "outputFormat": "csv"}
	job, err := s.CreateCrawlJob(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartCrawl(job.ID, cfg); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if j, _ := s.GetCrawlJob(job.ID); j != nil && j.Status != "running" && j.Status != "pending" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	m.WaitBackground()
	if b, _ := os.ReadFile(victim); string(b) != "keep me" {
		t.Errorf("outputPath file was written: %q", b)
	}
}

func TestScheduleSanitizesConfig(t *testing.T) {
	fakeHome(t)
	t.Chdir(t.TempDir()) // the schedule creates its output dir
	_, s, m := newTestAPI(t)
	cfg := map[string]interface{}{"seedUrl": "https://example.com/", "outputPath": "/tmp/x", "dbPath": "/tmp/y.db"}
	if _, err := m.startUISchedule(s, scheduleRequest{Cron: "@daily", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if cfg["outputPath"] != nil || cfg["dbPath"] != nil {
		t.Errorf("schedule kept server paths: %v", cfg)
	}
	for _, req := range []scheduleRequest{
		{Cron: "@daily", Config: map[string]interface{}{"seedUrl": "https://example.com/", "limit": 1e12}},
		{Cron: "@daily", URL: "https://example.com/", OutputDir: "../../etc"},
	} {
		if _, err := m.startUISchedule(s, req); err == nil {
			t.Errorf("schedule accepted: %+v", req)
		}
	}
}

// Recrawling an old History config with a key file outside ~/.micelio/keys
// gets a 400 that says how to fix it.
func TestCrawlStartKeyFileErrorIsActionable(t *testing.T) {
	fakeHome(t)
	api, _, _ := newTestAPI(t)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/start",
		strings.NewReader(`{"seedUrl":"https://example.com/","gsc":true,"gscKeyFile":"/old/place/sa.json"}`)))
	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest ||
		!strings.Contains(body, "gscKeyFile: /old/place/sa.json is outside ~/.micelio/keys") ||
		!strings.Contains(body, "move the key file into ~/.micelio/keys and select it again") {
		t.Errorf("%d %s", rec.Code, body)
	}
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/start",
		strings.NewReader(`{"seedUrl":"https://example.com/","jsRendering":true,"snippetPaths":["/old/s.js"]}`)))
	if body := rec.Body.String(); rec.Code != http.StatusBadRequest || !strings.Contains(body, "move the snippet into ~/.micelio/snippets") {
		t.Errorf("%d %s", rec.Code, body)
	}
}

// A dashboard schedule saved before the key/snippet/outputDir checks keeps
// running after an upgrade: the bad parts are dropped and the schedule
// carries a warning that GET /api/schedules returns.
func TestResumeUIScheduleWithOldConfig(t *testing.T) {
	fakeHome(t)
	wd := filepath.Join(t.TempDir(), "wd")
	os.MkdirAll(wd, 0o755)
	t.Chdir(wd)
	api, s, m := newTestAPI(t)
	defer m.StopAllSchedulers()

	old, err := scheduler.New(scheduler.Config{
		URL: "https://example.com/", CronExpr: "@daily", Name: "old", Source: "ui", OutputDir: "../crawls",
		CrawlMap: map[string]interface{}{"seedUrl": "https://example.com/", "gsc": true, "gscKeyFile": "/old/sa.json",
			"jsRendering": true, "snippetPaths": []interface{}{"/old/s.js", "s.js"}, "limit": 1e12},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := old.GetState().ID

	m.ResumeUISchedules(s)
	if !m.IsSchedulerActive(id) {
		t.Fatal("old schedule not resumed")
	}
	st := m.trackedScheduler(id).GetState()
	for _, want := range []string{"gscKeyFile dropped", "~/.micelio/keys", "snippet dropped", "page limit lowered", "outputDir ../crawls"} {
		if !strings.Contains(st.Warning, want) {
			t.Errorf("warning %q lacks %q", st.Warning, want)
		}
	}
	if st.OutputDir != defaultScheduleOutputDir || st.CrawlConfig["gscKeyFile"] != nil || len(st.CrawlConfig["snippetPaths"].([]interface{})) != 1 {
		t.Errorf("config not fixed: dir %q config %v", st.OutputDir, st.CrawlConfig)
	}

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/schedules", nil))
	if !strings.Contains(rec.Body.String(), `"warning":"gscKeyFile dropped`) {
		t.Errorf("listing lacks the warning: %s", rec.Body.String())
	}

	// The next restart finds a clean config but keeps the note.
	m.StopScheduler(id)
	m.ResumeUISchedules(s)
	if w := m.trackedScheduler(id).GetState().Warning; w != st.Warning {
		t.Errorf("warning after second restart = %q", w)
	}

	// Creating a schedule with the same config is still refused.
	if _, err := m.startUISchedule(s, scheduleRequest{Cron: "@daily", OutputDir: "../crawls",
		Config: map[string]interface{}{"seedUrl": "https://example.com/"}}); err == nil {
		t.Error("new schedule with outputDir outside the working directory accepted")
	}
}

// A crawl whose saved config names a key file outside ~/.micelio/keys
// records why GSC did not run.
func TestRunCrawlWarnsAboutDroppedKeyFile(t *testing.T) {
	fakeHome(t)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>hi</body></html>`)
	}))
	defer site.Close()
	_, s, m := newTestAPI(t)
	m.SkipSSRF = true
	var got []ProgressEvent
	var mu sync.Mutex
	m.SetProgressCallback(func(e ProgressEvent) {
		if e.Type == "warning" {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		}
	})
	cfg := map[string]interface{}{"seedUrl": site.URL + "/", "limit": 1, "concurrency": 1, "delay": 0,
		"ga4": true, "ga4KeyFile": "/old/sa.json"}
	job, err := s.CreateCrawlJob(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartCrawl(job.ID, cfg); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if j, _ := s.GetCrawlJob(job.ID); j != nil && j.Status != "running" && j.Status != "pending" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	m.WaitBackground()
	j, _ := s.GetCrawlJob(job.ID)
	var note string
	for _, e := range j.Events {
		if e.Type == "warning" {
			note = e.Note
		}
	}
	if !strings.Contains(note, "GA4 key file not used") || !strings.Contains(note, "~/.micelio/keys") {
		t.Errorf("events = %+v", j.Events)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Data["message"] != note {
		t.Errorf("emitted = %+v", got)
	}
}

func TestTuneRejectsHugeLimit(t *testing.T) {
	_, _, m := newTestAPI(t)
	_, err := m.TuneCrawl("any", crawler.Tuning{MaxPages: limits().MaxCrawlPages + 1})
	if err == nil || !strings.Contains(err.Error(), "page limit") {
		t.Errorf("err = %v, want page limit error", err)
	}
}
