package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/scheduler"
	"github.com/micelio/micelio/internal/types"
)

// secretConfig is a dashboard crawl config carrying every kind of secret.
func secretConfig() map[string]any {
	return map[string]any{
		"seedUrl": "https://example.invalid/",
		"mode":    "spider",
		"limit":   float64(5),
		"cookies": "session=COOKIE-SECRET",
		"customHeaders": map[string]any{
			"Authorization":   "Bearer AUTH-SECRET",
			"X-Api-Key":       "APIKEY-SECRET",
			"Accept-Language": "es-ES",
		},
		"proxy":       "http://bob:PROXY-SECRET@proxy.example:8080",
		"psiKey":      "PSI-SECRET",
		"aiKey":       "AI-SECRET",
		"gscKeyFile":  "/home/u/gsc-SECRET.json",
		"torPassword": "TOR-SECRET",
		"userAgent":   "Micelio/1.0",
		"segmentRules": []any{
			map[string]any{"name": "blog", "pattern": "^/blog/"},
		},
	}
}

// jsonCopy deep-copies a config the way it travels through the API.
func jsonCopy(t *testing.T, v map[string]any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	json.Unmarshal(data, &out)
	return out
}

func assertNoSecrets(t *testing.T, where, body string) {
	t.Helper()
	if strings.Contains(body, "SECRET") {
		t.Errorf("%s leaks a secret: %s", where, body)
	}
}

func TestRedactConfig(t *testing.T) {
	cfg := secretConfig()
	red := redactConfig(cfg)
	data, _ := json.Marshal(red)
	assertNoSecrets(t, "redactConfig", string(data))
	if red["cookies"] != secretMask || red["psiKey"] != secretMask {
		t.Errorf("secrets not masked: %v", red)
	}
	h := red["customHeaders"].(map[string]any)
	if h["Authorization"] != secretMask || h["X-Api-Key"] != secretMask || h["Accept-Language"] != "es-ES" {
		t.Errorf("headers = %v", h)
	}
	if red["proxy"] != "http://"+secretMask+":"+secretMask+"@proxy.example:8080" {
		t.Errorf("proxy = %v", red["proxy"])
	}
	if red["userAgent"] != "Micelio/1.0" || red["limit"] != float64(5) {
		t.Errorf("plain values changed: %v", red)
	}
	// The stored config is untouched.
	if cfg["cookies"] != "session=COOKIE-SECRET" || cfg["customHeaders"].(map[string]any)["Authorization"] != "Bearer AUTH-SECRET" {
		t.Error("redactConfig changed its input")
	}
}

func TestRestoreConfigSecretsRoundTrip(t *testing.T) {
	orig := secretConfig()
	// The client reads the masked config and sends it back unchanged.
	in := jsonCopy(t, redactConfig(orig))
	if err := restoreConfigSecrets(in, orig); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, jsonCopy(t, orig)) {
		t.Fatalf("round trip lost secrets:\n got %v\nwant %v", in, orig)
	}

	// Edited values win; header names may change casing.
	in = jsonCopy(t, redactConfig(orig))
	in["psiKey"] = "NEW-KEY"
	in["customHeaders"].(map[string]any)["authorization"] = in["customHeaders"].(map[string]any)["Authorization"]
	delete(in["customHeaders"].(map[string]any), "Authorization")
	if err := restoreConfigSecrets(in, orig); err != nil {
		t.Fatal(err)
	}
	h := in["customHeaders"].(map[string]any)
	if in["psiKey"] != "NEW-KEY" || h["authorization"] != "Bearer AUTH-SECRET" {
		t.Errorf("restore = %v", in)
	}
}

// A secret edited around its mask is restored in place where that is safe.
func TestRestoreConfigSecretsEdited(t *testing.T) {
	orig := map[string]any{
		"proxy":         "http://bob:PROXY-SECRET@proxy-a:8080",
		"cookies":       "session=COOKIE-SECRET; csrf=CSRF-SECRET",
		"customHeaders": map[string]any{"Cookie": "a=HDR-SECRET"},
		"seedUrl":       "https://u:SEED-SECRET@example.com/",
	}
	cases := []struct {
		key  string
		sent any
		want any
	}{
		{"proxy", "http://bob:" + secretMask + "@proxy-a:8080/", "http://bob:PROXY-SECRET@proxy-a:8080/"},
		{"cookies", secretMask + "; lang=en", "session=COOKIE-SECRET; csrf=CSRF-SECRET; lang=en"},
		{"cookies", "csrf=" + secretMask + "; lang=en", "csrf=CSRF-SECRET; lang=en"},
		{"customHeaders", map[string]any{"Cookie": secretMask + "; b=2"}, map[string]any{"Cookie": "a=HDR-SECRET; b=2"}},
		{"seedUrl", "https://u:" + secretMask + "@example.com/blog/", "https://u:SEED-SECRET@example.com/blog/"},
	}
	for _, c := range cases {
		in := map[string]any{c.key: c.sent}
		if err := restoreConfigSecrets(in, jsonCopy(t, orig)); err != nil {
			t.Errorf("%s %v: %v", c.key, c.sent, err)
			continue
		}
		if !reflect.DeepEqual(in[c.key], c.want) {
			t.Errorf("%s: got %v, want %v", c.key, in[c.key], c.want)
		}
	}
}

// A mask that cannot be restored is refused with the field named, never
// dropped (the crawl would run unauthenticated or from the real IP) nor
// used as a credential.
func TestRestoreConfigSecretsRefusesUnrestorable(t *testing.T) {
	orig := secretConfig()
	cases := []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"proxy": "http://alice:" + secretMask + "@proxy.example:8080"}, "re-enter the proxy password"},
		// Same user, other host: the stored password must not follow it.
		{map[string]any{"proxy": "http://bob:" + secretMask + "@evil.example:8080"}, "re-enter the proxy password"},
		{map[string]any{"seedUrl": "https://u:" + secretMask + "@evil.example/"}, "type it again"},
		{map[string]any{"cookies": "nope=" + secretMask}, "cookies"},
		{map[string]any{"customHeaders": map[string]any{"X-Other-Token": secretMask}}, "customHeaders.X-Other-Token"},
		{map[string]any{"customHeaders": map[string]any{"Authorization": "Bearer " + secretMask}}, "customHeaders.Authorization"},
		{map[string]any{"seedUrl": "https://u:" + secretMask + "@example.com/"}, "type it again"},
	}
	for _, c := range cases {
		err := restoreConfigSecrets(c.in, orig)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.in, err, c.want)
		}
	}
	// No stored config: any mask is refused.
	if err := restoreConfigSecrets(jsonCopy(t, redactConfig(orig)), nil); err == nil {
		t.Error("masks without a stored config accepted")
	}
}

func TestStartRefusesMaskedSeedURL(t *testing.T) {
	api, s, m := newTestAPI(t)
	m.SkipSSRF = true
	// An imported crawl stores the start URL already masked.
	old, _ := s.CreateCrawlJob(map[string]any{"seedUrl": "https://u:" + secretMask + "@example.invalid/"})
	s.UpdateCrawlJob(old.ID, map[string]interface{}{"status": "failed"})
	for _, body := range []string{
		`{"seedUrl":"https://u:` + secretMask + `@example.invalid/","mode":"spider"}`,
		`{"seedUrl":"https://u:` + secretMask + `@example.invalid/","mode":"spider","fromCrawl":"` + old.ID + `"}`,
	} {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/start", strings.NewReader(body)))
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "type it again") {
			t.Errorf("start: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/"+old.ID+"/restart",
		strings.NewReader(`{"seedUrl":"https://example.invalid/","proxy":"http://x:`+secretMask+`@p:1"}`)))
	// A masked proxy that is not the trusted proxy is refused before any
	// secret is restored.
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "--trusted-proxy") {
		t.Errorf("restart: %d %s", rec.Code, rec.Body.String())
	}
}

func TestCrawlAndPresetAPIRedactSecrets(t *testing.T) {
	api, s, _ := newTestAPI(t)
	job, err := s.CreateCrawlJob(secretConfig())
	if err != nil {
		t.Fatal(err)
	}
	s.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "failed"})
	get := func(path string) string {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	for _, p := range []string{"/api/crawl/" + job.ID + "/status", "/api/crawls"} {
		body := get(p)
		assertNoSecrets(t, p, body)
		if !strings.Contains(body, "Accept-Language") || !strings.Contains(body, secretMask) {
			t.Errorf("%s: config missing: %s", p, body)
		}
	}
	// The stored config keeps the secrets (resume needs them).
	if stored, _ := s.GetCrawlJob(job.ID); stored.Config["cookies"] != "session=COOKIE-SECRET" {
		t.Errorf("stored config changed: %v", stored.Config)
	}

	body, _ := json.Marshal(map[string]any{"name": "auth", "config": secretConfig()})
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/presets", bytes.NewReader(body)))
	assertNoSecrets(t, "POST /api/presets", rec.Body.String())
	assertNoSecrets(t, "GET /api/presets", get("/api/presets"))
}

func TestSettingsMaskWebhookURLs(t *testing.T) {
	api, s, _ := newTestAPI(t)
	s.UpdateSettings(map[string]interface{}{
		"alertSlackUrl":   "https://hooks.slack.com/services/T/B/SLACK-SECRET",
		"alertWebhookUrl": "https://example.com/hook?token=HOOK-SECRET",
	})
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/settings", nil))
	assertNoSecrets(t, "GET /api/settings", rec.Body.String())

	// Saving the settings page back keeps the stored URLs.
	put, _ := json.Marshal(map[string]any{"alertSlackUrl": secretMask, "alertWebhookUrl": secretMask})
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(put)))
	st, _ := s.GetSettings()
	if st["alertSlackUrl"] != "https://hooks.slack.com/services/T/B/SLACK-SECRET" {
		t.Errorf("slack URL overwritten: %v", st["alertSlackUrl"])
	}
}

// Recrawl (start with fromCrawl) and Resume with edits (restart) send back
// the masked config they read; the crawl must run with the real secrets.
func TestMaskedConfigRoundTripKeepsSecrets(t *testing.T) {
	api, s, m := newTestAPI(t)
	m.SkipSSRF = true
	// API configs may only name the trusted proxy (--trusted-proxy).
	m.TrustedProxy = secretConfig()["proxy"].(string)
	// Key file paths are confined to ~/.micelio/keys and dropped otherwise,
	// so this round trip covers the inline secrets only.
	cfg := func() map[string]any {
		c := secretConfig()
		delete(c, "gscKeyFile")
		return c
	}
	old, err := s.CreateCrawlJob(cfg())
	if err != nil {
		t.Fatal(err)
	}
	s.UpdateCrawlJob(old.ID, map[string]interface{}{"status": "failed"})
	masked := redactConfig(cfg())

	// Recrawl.
	start := jsonCopy(t, masked)
	start["fromCrawl"] = old.ID
	body, _ := json.Marshal(start)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/start", bytes.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rec.Body.Bytes(), &resp)
	id, _ := resp["id"].(string)
	m.CancelCrawl(id)
	waitIdle(t, m)
	job, _ := s.GetCrawlJob(id)
	want := jsonCopy(t, cfg())
	if !reflect.DeepEqual(jsonCopy(t, job.Config), want) {
		t.Errorf("recrawl config:\n got %v\nwant %v", job.Config, want)
	}

	// Resume with edits.
	dbPath := filepath.Join(t.TempDir(), "crawl.db")
	os.WriteFile(dbPath, nil, 0o600)
	resumed, _ := s.CreateCrawlJobWithDB(cfg(), dbPath)
	s.UpdateCrawlJob(resumed.ID, map[string]interface{}{"status": "failed"})
	edit := jsonCopy(t, masked)
	edit["limit"] = float64(7)
	body, _ = json.Marshal(edit)
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/"+resumed.ID+"/restart", bytes.NewReader(body)))
	m.CancelCrawl(resumed.ID)
	waitIdle(t, m)
	job, _ = s.GetCrawlJob(resumed.ID)
	want["limit"] = float64(7)
	if !reflect.DeepEqual(jsonCopy(t, job.Config), want) {
		t.Errorf("resume config (%d %s):\n got %v\nwant %v", rec.Code, rec.Body.String(), job.Config, want)
	}
}

func waitIdle(t *testing.T, m *CrawlManager) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for m.GetStatus() != nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

func TestImportedConfigDropsNestedSecrets(t *testing.T) {
	cfg := &types.CrawlConfig{
		SeedURL:       "https://example.com/",
		Cookies:       "session=COOKIE-SECRET",
		CustomHeaders: map[string]string{"Authorization": "Bearer AUTH-SECRET", "X-Api-Key": "APIKEY-SECRET"},
		Proxy:         "http://bob:PROXY-SECRET@proxy.example:8080",
		PSIKey:        "PSI-SECRET",
		TorPassword:   "TOR-SECRET",
	}
	m := importedConfigMap(cfg, "imported")
	data, _ := json.Marshal(m)
	assertNoSecrets(t, "importedConfigMap", string(data))
	if m["seedUrl"] != "https://example.com/" {
		t.Errorf("seedUrl = %v", m["seedUrl"])
	}
}

func TestScheduleListRedactsSecrets(t *testing.T) {
	st := scheduler.State{
		ID: "x", URL: "https://example.com/",
		CrawlConfig: secretConfig(),
		Webhook:     "https://hooks.slack.com/services/T/B/HOOK-SECRET",
		Precheck:    &scheduler.Precheck{Command: "check --password CMD-SECRET"},
		LastCheck:   &scheduler.PrecheckResult{Reason: "exit 1: CMD-SECRET"},
	}
	red := redactedSchedule(st)
	data, _ := json.Marshal(red)
	assertNoSecrets(t, "redactedSchedule", string(data))
	if !strings.HasPrefix(red.Webhook, "https://hooks.slack.com/") {
		t.Errorf("webhook = %q", red.Webhook)
	}
	if st.Precheck.Command != "check --password CMD-SECRET" || st.CrawlConfig["cookies"] != "session=COOKIE-SECRET" {
		t.Error("redactedSchedule changed the saved state")
	}

	t.Setenv("HOME", t.TempDir())
	api, _, m := newTestAPI(t)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/schedules", strings.NewReader(
		`{"url":"https://example.com/","cron":"@daily","webhook":"https://hooks.slack.com/services/T/B/HOOK-SECRET","config":{"seedUrl":"https://example.com/","cookies":"COOKIE-SECRET"}}`)))
	if rec.Code != 200 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	json.Unmarshal(rec.Body.Bytes(), &created)
	t.Cleanup(func() { m.StopScheduler(created["id"].(string)) })
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/schedules", nil))
	assertNoSecrets(t, "GET /api/schedules", rec.Body.String())
	if !strings.Contains(rec.Body.String(), "example.com") {
		t.Errorf("schedule missing: %s", rec.Body.String())
	}
}
