package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestApplyTrustedProxy(t *testing.T) {
	m := &CrawlManager{TrustedProxy: "http://u:pw@127.0.0.1:18888"}
	for _, tc := range []struct {
		in   any
		want any // nil: key removed; "error": refused
	}{
		{"", nil},
		{"   ", nil},
		{"http://u:pw@127.0.0.1:18888", m.TrustedProxy},
		{"HTTP://u:pw@127.0.0.1:18888/", m.TrustedProxy},
		{"http://u:" + secretMask + "@127.0.0.1:18888", m.TrustedProxy},
		{"http://u:pw@127.0.0.1:6379", "error"},
		{"http://u:" + secretMask + "@127.0.0.1:6379", "error"},
		{"http://127.0.0.1:18888", "error"}, // credentials differ
		{"socks5://10.0.0.1:1080", "error"},
		{"tor", "error"},
		{42.0, "error"},
	} {
		cfg := map[string]any{"proxy": tc.in}
		err := m.applyTrustedProxy(cfg)
		got, has := cfg["proxy"]
		switch {
		case tc.want == "error":
			if err == nil || !strings.Contains(err.Error(), "--trusted-proxy") && tc.in != 42.0 {
				t.Errorf("%v: err = %v, want refusal naming --trusted-proxy", tc.in, err)
			}
		case tc.want == nil:
			if err != nil || has {
				t.Errorf("%v: err = %v, proxy kept = %v", tc.in, err, got)
			}
		default:
			if err != nil || got != tc.want {
				t.Errorf("%v: err = %v, proxy = %v, want %v", tc.in, err, got, tc.want)
			}
		}
	}

	none := &CrawlManager{}
	err := none.applyTrustedProxy(map[string]any{"proxy": "http://127.0.0.1:18888"})
	if err == nil || !strings.Contains(err.Error(), "no trusted proxy") {
		t.Errorf("no trusted proxy: err = %v", err)
	}
	if err := none.applyTrustedProxy(map[string]any{"proxy": "tor"}); err == nil {
		t.Error("tor accepted without --trusted-proxy tor")
	}
	tor := &CrawlManager{TrustedProxy: "tor"}
	if cfg := (map[string]any{"proxy": "TOR"}); tor.applyTrustedProxy(cfg) != nil || cfg["proxy"] != "tor" {
		t.Errorf("trusted tor refused: %v", cfg)
	}
}

func TestValidateTrustedProxy(t *testing.T) {
	for _, ok := range []string{"", "tor", "http://127.0.0.1:8080", "socks5://u:p@10.0.0.2:1080", "https://proxy.example"} {
		if err := ValidateTrustedProxy(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"127.0.0.1:8080", "ftp://proxy:21", "file:///etc/passwd"} {
		if err := ValidateTrustedProxy(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// The API refuses a proxy that is not the trusted one (400), accepts the
// trusted one (200) and turns its masked form back into the real URL.
func TestCrawlStartTrustedProxyAPI(t *testing.T) {
	api, s, m := newTestAPI(t)
	start := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/start", strings.NewReader(body)))
		return rec
	}

	// No trusted proxy: any proxy is refused, loopback or not.
	for _, p := range []string{"http://127.0.0.1:18888", "http://proxy.example:8080", "tor"} {
		rec := start(`{"seedUrl":"https://example.invalid/","mode":"spider","proxy":"` + p + `"}`)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "--trusted-proxy") {
			t.Errorf("%s without trusted proxy: %d %s", p, rec.Code, rec.Body.String())
		}
	}

	m.TrustedProxy = "http://bob:PROXY-SECRET@127.0.0.1:1"
	rec := start(`{"seedUrl":"https://example.invalid/","mode":"spider","proxy":"http://bob:PROXY-SECRET@127.0.0.1:2"}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "not the trusted proxy") {
		t.Errorf("other proxy: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "PROXY-SECRET") {
		t.Error("error echoes the proxy password")
	}

	// The masked form (a config read back from the API) is the trusted proxy.
	rec = start(`{"seedUrl":"https://example.invalid/","mode":"spider","proxy":"http://bob:` + secretMask + `@127.0.0.1:1"}`)
	if rec.Code != 200 {
		t.Fatalf("masked trusted proxy: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct{ ID string }
	json.Unmarshal(rec.Body.Bytes(), &resp)
	job, _ := s.GetCrawlJob(resp.ID)
	if job == nil || job.Config["proxy"] != m.TrustedProxy {
		t.Fatalf("stored proxy = %v, want the real trusted URL", job)
	}
	stopRunning(t, m)

	rec = start(`{"seedUrl":"https://example.invalid/","mode":"spider","proxy":"` + m.TrustedProxy + `"}`)
	if rec.Code != 200 {
		t.Fatalf("trusted proxy: %d %s", rec.Code, rec.Body.String())
	}
	stopRunning(t, m)

	// Resume with edits: same rule.
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/crawl/"+resp.ID+"/restart",
		strings.NewReader(`{"seedUrl":"https://example.invalid/","proxy":"socks5://10.0.0.1:1080"}`)))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "--trusted-proxy") {
		t.Errorf("restart with other proxy: %d %s", rec.Code, rec.Body.String())
	}

	// Schedules: same rule.
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest("POST", "/api/schedules", strings.NewReader(
		`{"cron":"0 3 * * *","config":{"seedUrl":"https://example.invalid/","proxy":"http://10.0.0.1:3128"}}`)))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "--trusted-proxy") {
		t.Errorf("schedule with other proxy: %d %s", rec.Code, rec.Body.String())
	}
}

// A stored config with a proxy that is not trusted (saved before the rule,
// or the dashboard restarted without --trusted-proxy) fails at run time.
func TestRunRefusesUntrustedStoredProxy(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	cfg := map[string]any{"seedUrl": "https://example.invalid/", "mode": "spider", "proxy": "http://127.0.0.1:6379"}
	job, err := s.CreateCrawlJob(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartCrawl(job.ID, cfg); err != nil {
		t.Fatal(err)
	}
	waitNotRunning(t, m)
	got, _ := s.GetCrawlJob(job.ID)
	if got.Status != "failed" {
		t.Fatalf("status = %s, want failed", got.Status)
	}
	found := false
	for _, e := range got.Events {
		if e.Type == "failed" && strings.Contains(e.Note, "--trusted-proxy") {
			found = true
		}
	}
	if !found {
		t.Errorf("no failed event naming --trusted-proxy: %+v", got.Events)
	}
}

// stopRunning cancels the active crawl (the tests only need it started).
func stopRunning(t *testing.T, m *CrawlManager) {
	t.Helper()
	if a := m.GetStatus(); a != nil {
		m.CancelCrawl(a.ID)
	}
	waitNotRunning(t, m)
}

func waitNotRunning(t *testing.T, m *CrawlManager) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for m.GetStatus() != nil {
		if time.Now().After(deadline) {
			if a := m.GetStatus(); a != nil {
				m.CancelCrawl(a.ID)
			}
			t.Fatal("crawl still running")
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.WaitBackground()
}
