package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmailSettingsMaskedAndTestEndpoint(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	handler := CreateAPIHandler(s, m, nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/alerts/email/test", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("test without settings: %d %s", rec.Code, rec.Body)
	}

	body := `{"alertEmailHost":"127.0.0.1","alertEmailPort":1,"alertEmailFrom":"m@example.com","alertEmailTo":"a@example.com","alertEmailPassword":"s3cret","alertEmailSecurity":"none"}`
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save settings: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatal("GET /api/settings exposes the SMTP password")
	}
	var got map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got["alertEmailPassword"] != "••••••••" {
		t.Errorf("password = %v, want masked", got["alertEmailPassword"])
	}

	// Port 1 refuses: the endpoint reports the SMTP error.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/alerts/email/test", nil))
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "s3cret") {
		t.Errorf("test with bad server: %d %s", rec.Code, rec.Body)
	}
}

func TestPreviousCrawlBySchedule(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	mk := func(seed, schedule string) *CrawlJob {
		cfg := map[string]interface{}{"seedUrl": seed}
		if schedule != "" {
			cfg["schedule"] = map[string]interface{}{"scheduleName": schedule}
		}
		j, err := s.CreateCrawlJob(cfg)
		if err != nil {
			t.Fatal(err)
		}
		s.UpdateCrawlJob(j.ID, map[string]interface{}{"status": "completed"})
		j.Status = "completed"
		return j
	}
	a1 := mk("https://shop.example/", "daily")
	mk("https://shop.example/", "")       // manual crawl, same site
	mk("https://other.example/", "daily2") // other schedule
	a2 := mk("https://shop.example/", "daily")
	// Started-at has one-second resolution; order them explicitly.
	s.UpdateCrawlJob(a1.ID, map[string]interface{}{"startedAt": "2026-10-01T00:00:00Z"})
	s.UpdateCrawlJob(a2.ID, map[string]interface{}{"startedAt": "2026-10-02T00:00:00Z"})
	a2, _ = s.GetCrawlJob(a2.ID)
	if prev := m.previousCrawl(a2); prev == nil || prev.ID != a1.ID {
		t.Errorf("previous of scheduled crawl = %+v, want %s", prev, a1.ID)
	}
}
