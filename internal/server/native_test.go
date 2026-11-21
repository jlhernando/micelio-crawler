package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeDashboardConfigAndValidation(t *testing.T) {
	m := map[string]interface{}{"seedUrl": "https://example.com/", "mode": "spider", "nativeChrome": true, "respectRobots": true, "concurrency": float64(8), "delay": float64(100)}
	if err := sanitizeCrawlConfig(m); err != nil {
		t.Fatal(err)
	}
	c := ConfigFromMap(m)
	if !c.NativeChrome || c.Concurrency != 1 || c.DelayMs != 1000 || !c.NoAgentCheck || !c.NoSoft404Probe {
		t.Fatalf("native config not normalized: %+v", c)
	}
	api, _, _ := newTestAPI(t)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest("POST", "/api/crawl/start", strings.NewReader(`{"seedUrl":"https://example.com/","mode":"spider","nativeChrome":true,"respectRobots":true,"cookies":"session=secret"}`)))
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "session=secret") {
		t.Fatalf("invalid native config must be refused without disclosing values: %d %s", w.Code, w.Body.String())
	}
}
