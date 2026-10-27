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

func TestTelegramTokenMasked(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	handler := CreateAPIHandler(s, m, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/alerts/telegram/test", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("test without settings: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings",
		strings.NewReader(`{"alertTelegramToken":"123:SECRET","alertTelegramChatId":"42","alertAlways":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatal("GET /api/settings exposes the Telegram token")
	}
	ch, always, _ := m.channelSettings()
	if ch.telegramToken != "123:SECRET" || ch.telegramChat != "42" || !always || !ch.any() {
		t.Errorf("channels = %+v always=%v", ch, always)
	}
}

// Channels can come from the environment (~/.micelio/micelio.env) so tokens
// and chat IDs never need to be saved in a file inside a repository. Saved
// dashboard values win; blank saved values fall back to the environment.
func TestChannelsFromEnvironment(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	t.Setenv("MICELIO_TELEGRAM_TOKEN", "999:ENVTOKEN")
	t.Setenv("MICELIO_TELEGRAM_CHAT", "777")
	t.Setenv("MICELIO_SMTP_HOST", "smtp.example.com")
	t.Setenv("MICELIO_SMTP_PORT", "465")
	t.Setenv("MICELIO_SMTP_FROM", "m@example.com")
	t.Setenv("MICELIO_EMAIL_TO", "a@example.com")
	s.UpdateSettings(map[string]interface{}{"alertTelegramChatId": "", "alertEmailFrom": "saved@example.com"})

	ch, _, _ := m.channelSettings()
	if ch.telegramToken != "999:ENVTOKEN" || ch.telegramChat != "777" {
		t.Errorf("telegram from env = %q/%q", ch.telegramToken, ch.telegramChat)
	}
	if ch.email.Host != "smtp.example.com" || ch.email.Port != 465 || ch.email.From != "saved@example.com" {
		t.Errorf("email = %+v (saved From should win)", ch.email)
	}

	handler := CreateAPIHandler(s, m, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if strings.Contains(rec.Body.String(), "ENVTOKEN") {
		t.Error("GET /api/settings exposes a token from the environment")
	}
}

// micelio schedule reads the channels saved in the dashboard.
func TestLoadAlertChannelsFromSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MICELIO_TELEGRAM_TOKEN", "")
	t.Setenv("MICELIO_TELEGRAM_CHAT", "")
	store, err := OpenUiStore()
	if err != nil {
		t.Fatal(err)
	}
	store.UpdateSettings(map[string]interface{}{"alertTelegramToken": "1:T", "alertTelegramChatId": "42", "alertAlways": true})
	store.Close()

	ch, err := LoadAlertChannels()
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Any() || strings.Join(ch.Names(), ",") != "telegram" || !ch.Always {
		t.Errorf("channels = %v always=%v", ch.Names(), ch.Always)
	}
	ch.Override(nil, "", "99", "")
	if ch.ch.telegramChat != "99" || ch.ch.telegramToken != "1:T" {
		t.Errorf("override = %+v", ch.ch)
	}
}
