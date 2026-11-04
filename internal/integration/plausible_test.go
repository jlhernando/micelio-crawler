package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPlausibleFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Fatalf("expected Bearer test-key, got %s", auth)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{
					"dimensions": []string{"/blog/post-1"},
					"metrics":    []float64{100, 120, 150, 45.5, 180.0, 90.0, 75.0},
				},
				{
					"dimensions": []string{"/about"},
					"metrics":    []float64{50, 60, 70, 30.0, 120.0, 60.0, 50.0},
				},
			},
		})
	}))
	defer server.Close()

	client := NewPlausibleClient("test-key", "example.com", 30, server.URL).AllowPrivateHost()
	results, err := client.FetchBatch(context.Background(), []string{
		"https://example.com/blog/post-1",
		"https://example.com/about",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	data := results["https://example.com/blog/post-1"]
	if data == nil {
		t.Fatal("expected data for /blog/post-1")
	}
	if data.Visitors != 100 {
		t.Fatalf("expected 100 visitors, got %d", data.Visitors)
	}
	if data.Pageviews != 150 {
		t.Fatalf("expected 150 pageviews, got %d", data.Pageviews)
	}
	if data.TimeOnPage == nil || *data.TimeOnPage != 90.0 {
		t.Fatalf("expected time on page 90, got %v", data.TimeOnPage)
	}
}

func TestPlausibleRetryOn429(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []map[string]interface{}{
				{
					"dimensions": []string{"/"},
					"metrics":    []float64{10, 10, 10, 50.0, 60.0},
				},
			},
		})
	}))
	defer server.Close()

	client := NewPlausibleClient("key", "example.com", 7, server.URL).AllowPrivateHost()
	results, err := client.FetchBatch(context.Background(), []string{"https://example.com/"})
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result after retry, got %d", len(results))
	}
	if attempts < 2 {
		t.Fatal("expected at least 2 attempts")
	}
}

func TestNormalizePlausiblePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com/Blog/Post-1/", "/blog/post-1"},
		{"https://example.com/", "/"},
		{"https://example.com", "/"},
		{"https://example.com/path%20with%20spaces", "/path with spaces"},
	}

	for _, tc := range tests {
		got := normalizePlausiblePath(tc.input)
		if got != tc.want {
			t.Errorf("normalizePlausiblePath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestValidatePlausibleHost(t *testing.T) {
	cases := []struct {
		host    string
		want    string // "" means rejected
		trusted bool
	}{
		{"", "https://plausible.io", false},
		{"https://plausible.example.com/", "https://plausible.example.com", false},
		{"https://stats.example.com/plausible", "https://stats.example.com/plausible", false},
		{"http://plausible.example.com", "", false},
		{"http://plausible.example.com", "http://plausible.example.com", true},
		{"http://plausible.lan", "http://plausible.lan", true},
		{"http://192.168.1.20:8000", "http://192.168.1.20:8000", true},
		{"https://127.0.0.1", "", false},
		{"https://localhost:8000", "", false},
		{"https://10.0.0.8", "", false},
		{"https://169.254.169.254", "", false},
		{"https://[::1]", "", false},
		{"http://127.0.0.1:8000", "", false},
		{"http://127.0.0.1:8000", "http://127.0.0.1:8000", true},
		{"http://localhost:8000/", "http://localhost:8000", true},
		{"https://10.0.0.8", "https://10.0.0.8", true},
		{"http://10.0.0.8", "http://10.0.0.8", true},
		{"https://plausible.example.com/admin?", "", false},
		{"https://plausible.example.com/x?a=1", "", false},
		{"https://plausible.example.com/#frag", "", false},
		{"https://user:pw@plausible.example.com", "", false},
		{"file:///etc/passwd", "", true},
		{"gopher://plausible.example.com", "", false},
	}
	for _, c := range cases {
		got, err := ValidatePlausibleHost(c.host, c.trusted)
		if c.want == "" {
			if err == nil {
				t.Errorf("ValidatePlausibleHost(%q, %v) = %q, want error", c.host, c.trusted, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("ValidatePlausibleHost(%q, %v) = %q, %v; want %q", c.host, c.trusted, got, err, c.want)
		}
	}
}

// Without AllowPrivateHost the client refuses to connect to a private
// address (checked at dial time, so host names that resolve to one and
// redirects are covered too).
func TestPlausibleClientBlocksPrivateAddress(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()

	client := NewPlausibleClient("key", "example.com", 7, server.URL)
	if _, err := client.fetchChunk(context.Background(), []string{"/"}); err == nil {
		t.Fatal("request to a loopback Plausible host succeeded")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("loopback server got %d requests", n)
	}
}
