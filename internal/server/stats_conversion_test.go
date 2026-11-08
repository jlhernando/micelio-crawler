package server

import (
	"reflect"
	"testing"
)

func TestStatsConversionKeepsMetricsWithLegacyIssueDetails(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  interface{}
		want map[string][]string
	}{
		{"current", map[string]interface{}{"missingTitle": []interface{}{"https://example.com/a"}}, map[string][]string{"missingTitle": {"https://example.com/a"}}},
		{"legacy", map[string]interface{}{"missingTitle": map[string]interface{}{"count": 2}}, nil},
		{"null", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]interface{}{"totalPages": float64(12), "urlIssueStats": tc.raw}
			got := statsMapToCrawlStats(input)
			if got.TotalPages != 12 || !reflect.DeepEqual(got.URLIssueStats, tc.want) {
				t.Fatalf("conversion lost totals or valid issues: %+v", got)
			}
			if !reflect.DeepEqual(input["urlIssueStats"], tc.raw) {
				t.Fatal("conversion mutated stored issue details")
			}
		})
	}
}
