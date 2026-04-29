package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestCruxFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["url"] == nil {
			t.Fatal("expected url in body")
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"record": map[string]interface{}{
				"metrics": map[string]interface{}{
					"largest_contentful_paint": map[string]interface{}{
						"percentiles": map[string]interface{}{"p75": 2200.0},
					},
					"interaction_to_next_paint": map[string]interface{}{
						"percentiles": map[string]interface{}{"p75": 180.0},
					},
					"cumulative_layout_shift": map[string]interface{}{
						"percentiles": map[string]interface{}{"p75": 0.08},
					},
					"experimental_time_to_first_byte": map[string]interface{}{
						"percentiles": map[string]interface{}{"p75": 350.0},
					},
					"first_contentful_paint": map[string]interface{}{
						"percentiles": map[string]interface{}{"p75": 1500.0},
					},
				},
			},
		})
	}))
	defer server.Close()

	origEndpoint := cruxEndpoint
	defer func() { setCruxEndpoint(origEndpoint) }()
	setCruxEndpoint(server.URL)

	client := NewCruxClient("test-key", types.FormFactorPhone, 5)
	results := client.FetchBatch(context.Background(), []string{"https://example.com"})

	data, ok := results["https://example.com"]
	if !ok {
		t.Fatal("expected data for example.com")
	}
	if data.LCPMs == nil || *data.LCPMs != 2200 {
		t.Fatalf("expected LCP 2200, got %v", data.LCPMs)
	}
	if data.INPMs == nil || *data.INPMs != 180 {
		t.Fatalf("expected INP 180, got %v", data.INPMs)
	}
	if data.CLS == nil || *data.CLS != 0.08 {
		t.Fatalf("expected CLS 0.08, got %v", data.CLS)
	}
	if data.FormFactor != types.FormFactorPhone {
		t.Fatalf("expected PHONE form factor, got %s", data.FormFactor)
	}
}

func TestCruxNoData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	origEndpoint := cruxEndpoint
	defer func() { setCruxEndpoint(origEndpoint) }()
	setCruxEndpoint(server.URL)

	client := NewCruxClient("test-key", types.FormFactorAll, 5)
	results := client.FetchBatch(context.Background(), []string{"https://no-data.com"})

	if len(results) != 0 {
		t.Fatalf("expected no results for 404, got %d", len(results))
	}
}

func TestCrUXAssessment(t *testing.T) {
	tests := []struct {
		metric string
		want   CrUXAssessment
		value  float64
	}{
		{metric: "lcp", value: 2000, want: CrUXGood},
		{metric: "lcp", value: 3000, want: CrUXNeedsImprovement},
		{metric: "lcp", value: 5000, want: CrUXPoor},
		{metric: "inp", value: 150, want: CrUXGood},
		{metric: "inp", value: 300, want: CrUXNeedsImprovement},
		{metric: "inp", value: 600, want: CrUXPoor},
		{metric: "cls", value: 0.05, want: CrUXGood},
		{metric: "cls", value: 0.2, want: CrUXNeedsImprovement},
		{metric: "cls", value: 0.3, want: CrUXPoor},
	}

	for _, tc := range tests {
		got := GetCrUXAssessment(tc.metric, tc.value)
		if got != tc.want {
			t.Errorf("%s=%.2f: expected %s, got %s", tc.metric, tc.value, tc.want, got)
		}
	}
}
