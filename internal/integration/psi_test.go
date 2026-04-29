package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPSIFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("url") == "" {
			t.Fatal("expected url parameter")
		}
		if r.URL.Query().Get("strategy") != "mobile" {
			t.Fatal("expected mobile strategy")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"lighthouseResult": map[string]interface{}{
				"categories": map[string]interface{}{
					"performance": map[string]interface{}{
						"score": 0.85,
					},
				},
				"audits": map[string]interface{}{
					"largest-contentful-paint":  map[string]interface{}{"numericValue": 2500.0},
					"interaction-to-next-paint": map[string]interface{}{"numericValue": 150.0},
					"cumulative-layout-shift":   map[string]interface{}{"numericValue": 0.05},
					"server-response-time":      map[string]interface{}{"numericValue": 200.0},
					"speed-index":               map[string]interface{}{"numericValue": 3000.0},
					"total-blocking-time":       map[string]interface{}{"numericValue": 100.0},
				},
			},
		})
	}))
	defer server.Close()

	client := NewPSIClient("test-key")
	// Override endpoint for testing
	origEndpoint := psiEndpoint
	defer func() { setPSIEndpoint(origEndpoint) }()
	setPSIEndpoint(server.URL)

	data, err := client.Fetch(context.Background(), "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if data.PerformanceScore != 85 {
		t.Fatalf("expected score 85, got %f", data.PerformanceScore)
	}
	if data.LCP != 2500 {
		t.Fatalf("expected LCP 2500, got %f", data.LCP)
	}
	if data.INP != 150 {
		t.Fatalf("expected INP 150, got %f", data.INP)
	}
	if data.CLS != 0.05 {
		t.Fatalf("expected CLS 0.05, got %f", data.CLS)
	}
}

func TestPSIFetchError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	client := NewPSIClient("")
	origEndpoint := psiEndpoint
	defer func() { setPSIEndpoint(origEndpoint) }()
	setPSIEndpoint(server.URL)

	data, err := client.Fetch(context.Background(), "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if data.Error == "" {
		t.Fatal("expected error in data")
	}
}

func TestPSIBatchCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"lighthouseResult": map[string]interface{}{
				"categories": map[string]interface{}{
					"performance": map[string]interface{}{"score": 0.5},
				},
				"audits": map[string]interface{}{},
			},
		})
	}))
	defer server.Close()

	client := NewPSIClient("")
	origEndpoint := psiEndpoint
	defer func() { setPSIEndpoint(origEndpoint) }()
	setPSIEndpoint(server.URL)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	results := client.FetchBatch(ctx, []string{"https://a.com", "https://b.com"})
	// Should get 0 results since context is cancelled
	if len(results) != 0 {
		t.Fatalf("expected 0 results with cancelled context, got %d", len(results))
	}
}
