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

	"github.com/micelio/micelio/internal/scheduler"
)

// newCrawlTarget follows crawler.credentialsAllowed: the original hosts
// (seed, list, sitemaps, allowed domains) and their www twins, same port,
// https never moved to http; no other subdomain.
func TestNewCrawlTargetHostRule(t *testing.T) {
	orig := map[string]any{
		"seedUrl":        "http://example.com/",
		"sitemapUrls":    []any{"https://maps.example.net/sitemap.xml"},
		"allowedDomains": []any{"blog.example.org"},
	}
	for seed, want := range map[string]string{
		"http://example.com/x":               "",
		"https://example.com/":               "", // upgrade, default ports
		"http://www.example.com/":            "",
		"https://maps.example.net/":          "",
		"https://www.maps.example.net/":      "",
		"http://maps.example.net/":           "http://maps.example.net",
		"https://blog.example.org/":          "",
		"https://www.blog.example.org/":      "",
		"https://blog.example.org:8443/":     "https://blog.example.org",
		"https://shop.example.com/":          "https://shop.example.com",
		"https://attacker.example.com/":      "https://attacker.example.com",
		"https://evil.blog.example.org/":     "https://evil.blog.example.org",
		"http://example.com:8080/":           "http://example.com",
		"https://example.com.attacker.test/": "https://example.com.attacker.test",
	} {
		if got := newCrawlTarget(map[string]any{"seedUrl": seed}, orig); got != want {
			t.Errorf("seed %s: newCrawlTarget = %q, want %q", seed, got, want)
		}
	}
	for d, want := range map[string]string{
		"example.com": "", "www.example.com": "", "maps.example.net": "", "blog.example.org": "",
		"shop.example.com": "shop.example.com", "attacker.example.com": "attacker.example.com",
	} {
		in := map[string]any{"seedUrl": "http://example.com/", "allowedDomains": []any{d}}
		if got := newCrawlTarget(in, orig); got != want {
			t.Errorf("allowed domain %s: newCrawlTarget = %q, want %q", d, got, want)
		}
	}
}

// A restored secret stays bound to where it was stored for: changing the
// crawl's site or a service's host means typing it again.
func TestRestoreSecretsBoundToDestination(t *testing.T) {
	orig := map[string]any{
		"seedUrl":         "https://example.com/",
		"cookies":         "session=COOKIE-SECRET",
		"customHeaders":   map[string]any{"Authorization": "Bearer AUTH-SECRET", "X-JFrog-Art-Api": "JFROG-SECRET"},
		"plausibleApiKey": "PLAUSIBLE-SECRET",
		"plausibleHost":   "https://plausible.example.com",
		"aiKey":           "AI-SECRET",
		"aiProvider":      "openai",
		"psiKey":          "PSI-SECRET",
	}
	masked := redactConfig(orig)
	refused := []struct {
		name  string
		edit  func(map[string]any)
		field string
	}{
		{"seed moved to attacker", func(c map[string]any) { c["seedUrl"] = "https://attacker.example/" }, ""},
		{"seed downgraded to http", func(c map[string]any) { c["seedUrl"] = "http://example.com/" }, ""},
		{"seed on another port", func(c map[string]any) { c["seedUrl"] = "https://example.com:8443/" }, ""},
		{"list url added", func(c map[string]any) { c["urlList"] = "https://example.com/a\nhttps://attacker.example/b" }, ""},
		{"sitemap on another host", func(c map[string]any) { c["sitemapUrls"] = []any{"https://attacker.example/sitemap.xml"} }, ""},
		{"allowed domain added", func(c map[string]any) { c["allowedDomains"] = []any{"attacker.example"} }, ""},
		// A subdomain may belong to someone else (shared hosting, SaaS
		// tenants): the crawler does not send it the credentials either.
		{"subdomain", func(c map[string]any) { c["seedUrl"] = "https://shop.example.com/" }, ""},
		{"attacker subdomain", func(c map[string]any) { c["seedUrl"] = "https://attacker.example.com/" }, ""},
		{"www of a subdomain", func(c map[string]any) { c["seedUrl"] = "https://www.attacker.example.com/" }, ""},
		{"list url on a subdomain", func(c map[string]any) { c["urlList"] = "https://example.com/a\nhttps://attacker.example.com/b" }, ""},
		{"subdomain as allowed domain", func(c map[string]any) { c["allowedDomains"] = []any{"attacker.example.com"} }, ""},
		{"parent domain", func(c map[string]any) { c["seedUrl"] = "https://com/" }, ""},
		{"plausible host changed", func(c map[string]any) { c["plausibleHost"] = "https://attacker.example" }, "plausibleApiKey"},
		{"ai provider changed", func(c map[string]any) { c["aiProvider"] = "ollama" }, "aiKey"},
	}
	for _, c := range refused {
		in := jsonCopy(t, masked)
		c.edit(in)
		if c.field != "" {
			// Only the service secret is sent masked.
			for _, k := range []string{"cookies", "customHeaders"} {
				delete(in, k)
			}
		}
		err := restoreConfigSecrets(in, jsonCopy(t, orig))
		if err == nil || !strings.Contains(err.Error(), "re-enter") || (c.field != "" && !strings.Contains(err.Error(), c.field)) {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}

	allowed := []struct {
		name string
		edit func(map[string]any)
	}{
		{"unchanged", func(map[string]any) {}},
		{"new path", func(c map[string]any) { c["seedUrl"] = "https://example.com/blog/" }},
		{"www", func(c map[string]any) { c["seedUrl"] = "https://www.example.com/" }},
		{"www with explicit default port", func(c map[string]any) { c["seedUrl"] = "https://www.example.com:443/" }},
		{"allowed domain www twin", func(c map[string]any) { c["allowedDomains"] = []any{"www.example.com"} }},
		{"secrets re-entered for a new site", func(c map[string]any) {
			c["seedUrl"] = "https://other.example/"
			c["cookies"] = "session=NEW"
			c["customHeaders"] = map[string]any{"Authorization": "Bearer NEW"}
		}},
		{"plausible key re-entered for a new host", func(c map[string]any) {
			c["plausibleHost"] = "https://p2.example"
			c["plausibleApiKey"] = "NEW"
		}},
		{"fixed endpoint key with a new site", func(c map[string]any) {
			c["seedUrl"] = "https://other.example/"
			delete(c, "cookies")
			delete(c, "customHeaders")
		}},
	}
	for _, c := range allowed {
		in := jsonCopy(t, masked)
		c.edit(in)
		if err := restoreConfigSecrets(in, jsonCopy(t, orig)); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		data, _ := json.Marshal(in)
		if strings.Contains(string(data), secretMask) {
			t.Errorf("%s: mask left: %s", c.name, data)
		}
	}
}

// The finding's scenario through the API: Recrawl and Resume with edits
// with the masked cookies and headers but a new start URL are refused.
func TestStartRefusesSecretsForNewSite(t *testing.T) {
	api, s, m := newTestAPI(t)
	m.SkipSSRF = true
	old, _ := s.CreateCrawlJob(map[string]any{
		"seedUrl": "https://example.invalid/", "mode": "spider",
		"cookies": "sid=COOKIE-SECRET", "customHeaders": map[string]any{"Authorization": "Bearer AUTH-SECRET"},
	})
	s.UpdateCrawlJob(old.ID, map[string]interface{}{"status": "failed"})
	in := redactConfig(old.Config)
	in["seedUrl"] = "https://attacker.invalid/"
	in["fromCrawl"] = old.ID
	body, _ := json.Marshal(in)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/start", bytes.NewReader(body)))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "re-enter") {
		t.Errorf("start: %d %s", rec.Code, rec.Body.String())
	}
	delete(in, "fromCrawl")
	body, _ = json.Marshal(in)
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/"+old.ID+"/restart", bytes.NewReader(body)))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "re-enter") {
		t.Errorf("restart: %d %s", rec.Code, rec.Body.String())
	}
}

// Custom headers whose names match no credential word are masked too,
// and still come back on Recrawl.
func TestCustomHeadersMaskedByDefault(t *testing.T) {
	orig := map[string]any{
		"seedUrl": "https://example.com/",
		"customHeaders": map[string]any{
			"X-JFrog-Art-Api":          "JFROG-SECRET",
			"X-Goog-IAP-JWT-Assertion": "IAP-SECRET",
			"Accept-Language":          "es-ES",
		},
	}
	red := redactConfig(orig)
	data, _ := json.Marshal(red)
	assertNoSecrets(t, "redactConfig", string(data))
	if red["customHeaders"].(map[string]any)["Accept-Language"] != "es-ES" {
		t.Errorf("safe header masked: %s", data)
	}
	in := jsonCopy(t, red)
	if err := restoreConfigSecrets(in, jsonCopy(t, orig)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, jsonCopy(t, orig)) {
		t.Errorf("round trip: %v", in)
	}

	api, s, _ := newTestAPI(t)
	job, _ := s.CreateCrawlJob(orig)
	s.UpdateCrawlJob(job.ID, map[string]interface{}{"status": "failed"})
	for _, p := range []string{"/api/crawl/" + job.ID + "/status", "/api/crawls"} {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		assertNoSecrets(t, p, rec.Body.String())
	}
}

// URL credentials (user info and token parameters) are masked in every URL
// field, in the job's start URL and in schedules, and restored on Recrawl.
func TestURLCredentialsMaskedEverywhere(t *testing.T) {
	orig := map[string]any{
		"seedUrl":     "https://example.com/?access_token=SEED-SECRET",
		"urlList":     "https://example.com/a?api_key=LIST-SECRET\nhttps://u:LISTPW-SECRET@example.com/b",
		"sitemapUrls": []any{"https://example.com/sitemap.xml?X-Amz-Signature=SM-SECRET"},
		"proxy":       "http://bob-zone-SECRET:PROXY-SECRET@proxy.example:8080",
	}
	red := redactConfig(orig)
	data, _ := json.Marshal(red)
	assertNoSecrets(t, "redactConfig", string(data))

	in := jsonCopy(t, red)
	if err := restoreConfigSecrets(in, jsonCopy(t, orig)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, jsonCopy(t, orig)) {
		t.Errorf("round trip:\n got %v\nwant %v", in, orig)
	}
	// Edited around the masks: a new path, an extra parameter, a list
	// entry removed before the masked one.
	in = jsonCopy(t, red)
	in["seedUrl"] = strings.Replace(in["seedUrl"].(string), "/?", "/blog/?page=2&", 1)
	in["urlList"] = strings.SplitN(in["urlList"].(string), "\n", 2)[1]
	if err := restoreConfigSecrets(in, jsonCopy(t, orig)); err != nil {
		t.Fatal(err)
	}
	if in["seedUrl"] != "https://example.com/blog/?page=2&access_token=SEED-SECRET" || in["urlList"] != "https://u:LISTPW-SECRET@example.com/b" {
		t.Errorf("edited restore: %v", in)
	}
	// The masked token must not follow the URL to another host.
	in = map[string]any{"seedUrl": "https://attacker.example/?access_token=" + secretMask}
	if err := restoreConfigSecrets(in, jsonCopy(t, orig)); err == nil {
		t.Error("token moved to another host")
	}

	job := redactedJob(&CrawlJob{SeedURL: "https://u:JOBPW-SECRET@example.com/?token=JOB-SECRET", Config: orig})
	data, _ = json.Marshal(job)
	assertNoSecrets(t, "redactedJob", string(data))

	st := redactedSchedule(scheduler.State{ID: "x", URL: "https://u:SCHED-SECRET@example.com/?sig=SIG-SECRET", CrawlConfig: orig})
	data, _ = json.Marshal(st)
	assertNoSecrets(t, "redactedSchedule", string(data))
	if !strings.HasPrefix(st.URL, "https://") || !strings.Contains(st.URL, "@example.com/?sig=") {
		t.Errorf("schedule URL = %q", st.URL)
	}
}

// A failed CLI check command's output never reaches GET /api/schedules,
// neither through lastCheck nor lastReason (also in state files saved by
// older versions, which kept the output in the reason).
func TestScheduleHidesCommandOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".micelio", "schedules")
	os.MkdirAll(dir, 0o700)
	for _, reason := range []string{
		"check command failed: exit status 1 token=CMD-SECRET",
		"paused: check command failed: exit status 1 token=CMD-SECRET",
		"stopped: check failed during crawl: check command failed: exit status 1 CMD-SECRET",
	} {
		st := scheduler.State{
			ID: "cli1", Source: "cli", URL: "https://example.com/", Cron: "@daily",
			Precheck:   &scheduler.Precheck{Command: "check --password CMD-SECRET"},
			LastStatus: "skipped", LastReason: reason,
			LastCheck: &scheduler.PrecheckResult{Reason: reason},
		}
		data, _ := json.Marshal(st)
		os.WriteFile(filepath.Join(dir, "cli1.json"), data, 0o600)

		api, _, _ := newTestAPI(t)
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("GET", "/api/schedules", nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "cli1") {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		assertNoSecrets(t, "GET /api/schedules", rec.Body.String())
		if !strings.Contains(rec.Body.String(), "check command failed") {
			t.Errorf("reason lost: %s", rec.Body.String())
		}
	}
}
