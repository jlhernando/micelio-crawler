package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGA4Fetch(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "ga4-mock-token",
			"expires_in":   3600,
		})
	}))
	defer tokenServer.Close()

	ga4Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer ga4-mock-token" {
			t.Fatalf("expected Bearer ga4-mock-token, got %s", auth)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"rows": []map[string]interface{}{
				{
					"dimensionValues": []map[string]string{{"value": "/blog/post-1"}},
					"metricValues": []map[string]string{
						{"value": "500"}, {"value": "750"}, {"value": "0.45"},
						{"value": "10"}, {"value": "400"}, {"value": "0.72"},
						{"value": "120.5"},
					},
				},
				{
					"dimensionValues": []map[string]string{{"value": "/about"}},
					"metricValues": []map[string]string{
						{"value": "200"}, {"value": "220"}, {"value": "0.6"},
						{"value": "2"}, {"value": "180"}, {"value": "0.55"},
						{"value": "60.0"},
					},
				},
			},
			"rowCount": 2,
		})
	}))
	defer ga4Server.Close()

	origEndpoint := ga4Endpoint
	defer func() { setGa4Endpoint(origEndpoint) }()
	setGa4Endpoint(ga4Server.URL)

	keyJSON := generateTestKeyJSON(t, tokenServer.URL)
	client, err := NewGA4Client(keyJSON, "123456789", 90)
	if err != nil {
		t.Fatal(err)
	}

	results, err := client.FetchBatch(context.Background(), []string{
		"https://example.com/blog/post-1",
		"https://example.com/about",
		"https://example.com/missing",
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
	if data.Sessions != 500 {
		t.Fatalf("expected 500 sessions, got %d", data.Sessions)
	}
	if data.Pageviews != 750 {
		t.Fatalf("expected 750 pageviews, got %d", data.Pageviews)
	}
	if data.ActiveUsers != 400 {
		t.Fatalf("expected 400 active users, got %d", data.ActiveUsers)
	}
}

func TestGA4NormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://example.com/Blog/Post-1/", "/blog/post-1"},
		{"https://example.com/", "/"},
		{"https://example.com", "/"},
	}

	for _, tc := range tests {
		got := normalizeGA4Path(tc.input)
		if got != tc.want {
			t.Errorf("normalizeGA4Path(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
