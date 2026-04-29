package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

	client := NewPlausibleClient("test-key", "example.com", 30, server.URL)
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

	client := NewPlausibleClient("key", "example.com", 7, server.URL)
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
