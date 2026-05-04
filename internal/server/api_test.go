package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestAPI(t *testing.T) (http.Handler, *UiStore, *CrawlManager) {
	t.Helper()
	s := newTestStore(t)
	m := NewCrawlManager(s)
	return CreateAPIHandler(s, m, nil), s, m
}

func TestGetPresets(t *testing.T) {
	api, _, _ := newTestAPI(t)

	req := httptest.NewRequest("GET", "/api/presets", nil)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var presets []SavedPreset
	json.NewDecoder(rec.Body).Decode(&presets)
	if len(presets) != 5 {
		t.Fatalf("expected 5 presets, got %d", len(presets))
	}
}

func TestCrawlLifecycle(t *testing.T) {
	api, _, _ := newTestAPI(t)

	// Start crawl
	body, _ := json.Marshal(map[string]interface{}{"url": "https://example.com", "mode": "spider"})
	req := httptest.NewRequest("POST", "/api/crawl/start", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("start: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var startResp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&startResp)
	crawlID, _ := startResp["id"].(string)
	if crawlID == "" {
		t.Fatal("expected crawl ID")
	}

	// Get status
	req = httptest.NewRequest("GET", "/api/crawl/"+crawlID+"/status", nil)
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: expected 200, got %d", rec.Code)
	}

	// List crawls
	req = httptest.NewRequest("GET", "/api/crawls", nil)
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rec.Code)
	}

	var listResp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&listResp)
	total := int(listResp["total"].(float64))
	if total != 1 {
		t.Fatalf("expected 1 crawl, got %d", total)
	}

	// Cancel running crawl before cleanup to avoid goroutine racing with DB close
	req = httptest.NewRequest("POST", "/api/crawl/"+crawlID+"/cancel", nil)
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	time.Sleep(100 * time.Millisecond)

	// Delete crawl
	req = httptest.NewRequest("DELETE", "/api/crawl/"+crawlID, nil)
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", rec.Code)
	}

	// Verify deleted
	req = httptest.NewRequest("GET", "/api/crawl/"+crawlID+"/status", nil)
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", rec.Code)
	}
}

func TestSettingsAPI(t *testing.T) {
	api, _, _ := newTestAPI(t)

	// PUT settings
	body, _ := json.Marshal(map[string]interface{}{
		"defaultDepth":   3,
		"psiKey":         "test-key",
		"invalidSetting": "should be rejected",
	})
	req := httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	rejected := resp["rejected"].([]interface{})
	if len(rejected) != 1 || rejected[0].(string) != "invalidSetting" {
		t.Fatalf("expected invalidSetting rejected, got %v", rejected)
	}

	// GET settings
	req = httptest.NewRequest("GET", "/api/settings", nil)
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var settings map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&settings)
	if settings["psiKey"] != "••••••••" {
		t.Fatalf("expected masked key, got %v", settings["psiKey"])
	}
}

func TestCreatePresetAPI(t *testing.T) {
	api, _, _ := newTestAPI(t)

	body, _ := json.Marshal(map[string]interface{}{
		"name":   "Custom Preset",
		"config": map[string]interface{}{"depth": 5},
	})
	req := httptest.NewRequest("POST", "/api/presets", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var preset SavedPreset
	json.NewDecoder(rec.Body).Decode(&preset)
	if preset.Name != "Custom Preset" {
		t.Fatalf("expected Custom Preset, got %s", preset.Name)
	}
	if preset.BuiltIn {
		t.Fatal("should not be built-in")
	}
}

func TestNotFoundCrawl(t *testing.T) {
	api, _, _ := newTestAPI(t)

	req := httptest.NewRequest("GET", "/api/crawl/nonexistent/status", nil)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestValidateSettings(t *testing.T) {
	valid, rejected := validateSettings(map[string]interface{}{
		"defaultDepth":  float64(3), // JSON numbers are float64
		"aiProvider":    "openai",
		"psiKey":        "key",
		"gscKeyFile":    "/absolute/path.json",
		"badKey":        "rejected",
		"defaultNgrams": true,
	})

	if len(valid) != 5 {
		t.Fatalf("expected 5 valid, got %d: %v", len(valid), valid)
	}
	if len(rejected) != 1 || rejected[0] != "badKey" {
		t.Fatalf("expected [badKey] rejected, got %v", rejected)
	}
}

func TestValidateSettingsPathTraversal(t *testing.T) {
	cases := []struct {
		val  interface{}
		name string
	}{
		{name: "relative path", val: "../etc/passwd"},
		{name: "embedded dotdot", val: "/home/../etc/passwd"},
		{name: "non-string type", val: 123},
	}
	for _, tc := range cases {
		valid, rejected := validateSettings(map[string]interface{}{
			"gscKeyFile": tc.val,
		})
		if len(valid) != 0 {
			t.Fatalf("%s: path traversal should be rejected", tc.name)
		}
		if len(rejected) != 1 {
			t.Fatalf("%s: expected 1 rejected, got %d", tc.name, len(rejected))
		}
	}
}
