package alerts

import (
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestEvaluateNilStats(t *testing.T) {
	result := Evaluate(nil)
	if result != nil {
		t.Error("expected nil for nil stats")
	}
}

func TestEvaluateNoAlerts(t *testing.T) {
	stats := &types.CrawlStats{
		TotalPages:  100,
		StatusCodes: map[int]int{200: 100},
		SeoFunnelStats: &types.SeoFunnelStats{
			Crawled:      100,
			Indexable:    95,
			PctIndexable: 95.0,
		},
	}
	result := Evaluate(stats)
	if result != nil {
		t.Errorf("expected nil (no alerts), got %d alerts", len(result.Alerts))
	}
}

func TestEvaluateHighErrorRate(t *testing.T) {
	stats := &types.CrawlStats{
		TotalPages: 100,
		StatusCodes: map[int]int{
			200: 80,
			404: 15,
			500: 5,
		},
	}
	result := Evaluate(stats)
	if result == nil {
		t.Fatal("expected alerts")
	}

	found := false
	for _, a := range result.Alerts {
		if a.ID == "high-error-rate" {
			found = true
			if a.Severity != "critical" {
				t.Errorf("expected critical severity, got %s", a.Severity)
			}
			if a.Value < 20 {
				t.Errorf("expected error rate ~20%%, got %.1f%%", a.Value)
			}
		}
	}
	if !found {
		t.Error("expected high-error-rate alert")
	}
}

func TestEvaluateHigh5xxRate(t *testing.T) {
	stats := &types.CrawlStats{
		TotalPages: 100,
		StatusCodes: map[int]int{
			200: 95,
			500: 3,
			503: 2,
		},
	}
	result := Evaluate(stats)
	if result == nil {
		t.Fatal("expected alerts")
	}

	found := false
	for _, a := range result.Alerts {
		if a.ID == "high-5xx-rate" {
			found = true
			if a.Value < 5 {
				t.Errorf("expected 5xx rate ~5%%, got %.1f%%", a.Value)
			}
		}
	}
	if !found {
		t.Error("expected high-5xx-rate alert")
	}
}

func TestEvaluateLowIndexability(t *testing.T) {
	stats := &types.CrawlStats{
		TotalPages: 100,
		SeoFunnelStats: &types.SeoFunnelStats{
			Crawled:      100,
			Indexable:    50,
			PctIndexable: 50.0,
		},
	}
	result := Evaluate(stats)
	if result == nil {
		t.Fatal("expected alerts")
	}

	found := false
	for _, a := range result.Alerts {
		if a.ID == "low-indexability" {
			found = true
			if a.Value != 50.0 {
				t.Errorf("expected 50.0, got %.1f", a.Value)
			}
		}
	}
	if !found {
		t.Error("expected low-indexability alert")
	}
}

func TestEvaluateSlowResponseTime(t *testing.T) {
	stats := &types.CrawlStats{
		TotalPages:              100,
		ResponseTimePercentiles: types.PercentileData{P50: 1000, P90: 4000, P99: 8000},
	}
	result := Evaluate(stats)
	if result == nil {
		t.Fatal("expected alerts")
	}

	found := false
	for _, a := range result.Alerts {
		if a.ID == "slow-response-time" {
			found = true
		}
	}
	if !found {
		t.Error("expected slow-response-time alert")
	}
}

func TestEvaluateOrphanPages(t *testing.T) {
	orphans := make([]string, 10)
	for i := range orphans {
		orphans[i] = "https://example.com/orphan"
	}
	stats := &types.CrawlStats{
		TotalPages:  100,
		OrphanPages: orphans,
	}
	result := Evaluate(stats)
	if result == nil {
		t.Fatal("expected alerts")
	}

	found := false
	for _, a := range result.Alerts {
		if a.ID == "orphan-pages" {
			found = true
			if a.Value < 10 {
				t.Errorf("expected orphan rate ~10%%, got %.1f%%", a.Value)
			}
		}
	}
	if !found {
		t.Error("expected orphan-pages alert")
	}
}

func TestEvaluateSeverityCounts(t *testing.T) {
	stats := &types.CrawlStats{
		TotalPages: 100,
		StatusCodes: map[int]int{
			200: 70,
			404: 20,
			500: 10,
		},
		SeoFunnelStats: &types.SeoFunnelStats{
			Crawled:      100,
			Indexable:    40,
			PctIndexable: 40.0,
		},
	}
	result := Evaluate(stats)
	if result == nil {
		t.Fatal("expected alerts")
	}
	if result.Critical == 0 {
		t.Error("expected at least one critical alert")
	}
	if result.Critical+result.Warnings+result.Info != len(result.Alerts) {
		t.Error("severity counts don't match alert count")
	}
}

func TestEvaluateLowActiveRateWithAnalytics(t *testing.T) {
	stats := &types.CrawlStats{
		TotalPages: 100,
		GscStats:   &types.GscStats{}, // Analytics connected
		SeoFunnelStats: &types.SeoFunnelStats{
			Crawled:  100,
			Active:   0,
			PctActive: 0,
		},
	}
	result := Evaluate(stats)
	if result == nil {
		t.Fatal("expected low-active-rate alert when analytics connected and active=0")
	}
	found := false
	for _, a := range result.Alerts {
		if a.ID == "low-active-rate" {
			found = true
		}
	}
	if !found {
		t.Error("expected low-active-rate alert")
	}
}

func TestEvaluateLowActiveRateNoAnalytics(t *testing.T) {
	stats := &types.CrawlStats{
		TotalPages: 100,
		// No GscStats, Ga4Stats, or PlausibleStats → analytics not connected
		SeoFunnelStats: &types.SeoFunnelStats{
			Crawled:  100,
			Active:   0,
			PctActive: 0,
		},
	}
	result := Evaluate(stats)
	// Should NOT fire when analytics aren't connected
	if result != nil {
		for _, a := range result.Alerts {
			if a.ID == "low-active-rate" {
				t.Error("should not fire low-active-rate when no analytics connected")
			}
		}
	}
}

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://example.com/webhook", false},
		{"http://example.com/webhook", false},
		{"ftp://example.com", true},
		{"http://localhost/webhook", true},
		{"http://127.0.0.1/webhook", true},
		{"http://192.168.1.1/webhook", true},
		{"http://10.0.0.1/webhook", true},
		{"http://169.254.169.254/latest", true},
		{"not-a-url", true},
	}
	for _, tt := range tests {
		err := validateWebhookURL(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateWebhookURL(%q) = %v, wantErr %v", tt.url, err, tt.wantErr)
		}
	}
}

func TestValidateSlackURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://hooks.slack.com/services/T00/B00/xxx", false},
		{"http://hooks.slack.com/services/T00/B00/xxx", true},  // must be HTTPS
		{"https://evil.com/services/T00/B00/xxx", true},        // not slack.com
		{"https://hooks.slack.com.evil.com/services", true},     // subdomain attack
	}
	for _, tt := range tests {
		err := validateSlackURL(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateSlackURL(%q) = %v, wantErr %v", tt.url, err, tt.wantErr)
		}
	}
}
