package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSendSuccess(t *testing.T) {
	var received Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected JSON content type")
		}
		if r.Header.Get("User-Agent") != "Micelio/1.0 Scheduler" {
			t.Errorf("expected Micelio user agent")
		}
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := Payload{
		Event:      "crawl_complete",
		URL:        "https://example.com",
		Timestamp:  "2026-01-01T00:00:00Z",
		Duration:   "5m30s",
		Pages:      1000,
		Errors:     2,
		OutputFile: "/tmp/crawl.jsonl",
	}

	ok := Send(Options{URL: srv.URL, SkipValidation: true}, payload)
	if !ok {
		t.Fatal("expected success")
	}
	if received.Event != "crawl_complete" {
		t.Fatalf("expected crawl_complete, got %q", received.Event)
	}
	if received.Pages != 1000 {
		t.Fatalf("expected 1000 pages, got %d", received.Pages)
	}
}

func TestSendRetryThenSuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ok := Send(Options{URL: srv.URL, SkipValidation: true}, Payload{Event: "crawl_complete"})
	if !ok {
		t.Fatal("expected success after retry")
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestSendCustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("expected auth header")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := Options{
		URL:            srv.URL,
		Headers:        map[string]string{"Authorization": "Bearer secret"},
		SkipValidation: true,
	}
	ok := Send(opts, Payload{Event: "crawl_complete"})
	if !ok {
		t.Fatal("expected success")
	}
}

func TestSendFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	ok := Send(Options{URL: srv.URL, SkipValidation: true}, Payload{Event: "crawl_failed"})
	if ok {
		t.Fatal("expected failure")
	}
}

func TestRedactURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://hooks.slack.com/services/T00/B00/xxxx", "https://hooks.slack.com/..."},
		{"http://localhost:8080/webhook?token=secret", "http://localhost:8080/..."},
		{"not-a-url", "not-a-url"},
	}
	for _, tc := range tests {
		got := RedactURL(tc.input)
		if got != tc.want {
			t.Errorf("RedactURL(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestPayloadWithSchedule(t *testing.T) {
	var received Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	payload := Payload{
		Event: "crawl_complete",
		Schedule: &Schedule{
			Cron:      "0 2 * * *",
			RunNumber: 5,
			NextRun:   "2026-01-02T02:00:00Z",
		},
	}

	Send(Options{URL: srv.URL, SkipValidation: true}, payload)
	if received.Schedule == nil {
		t.Fatal("expected schedule in payload")
	}
	if received.Schedule.RunNumber != 5 {
		t.Fatalf("expected run number 5, got %d", received.Schedule.RunNumber)
	}
}
