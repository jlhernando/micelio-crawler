package integration

import (
	"testing"
	"time"
)

func TestReportDateRange(t *testing.T) {
	now := time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		days, lag  int
		start, end string
	}{
		{28, 3, "2026-10-07", "2026-11-03"}, // exactly 28 days inclusive
		{90, 1, "2026-08-08", "2026-11-05"},
		{1, 3, "2026-11-03", "2026-11-03"},
		{0, 1, "2026-11-05", "2026-11-05"},
	}
	for _, c := range cases {
		s, e := reportDateRange(now, c.days, c.lag)
		if s != c.start || e != c.end {
			t.Errorf("reportDateRange(%d, %d) = %s..%s, want %s..%s", c.days, c.lag, s, e, c.start, c.end)
		}
	}
	// A local time zone does not shift the dates (just after midnight in Madrid).
	madrid := time.FixedZone("CET", 3600)
	if s, e := reportDateRange(time.Date(2027, 1, 2, 0, 30, 0, 0, madrid), 3, 3); s != "2026-12-27" || e != "2026-12-29" {
		t.Errorf("UTC range = %s..%s", s, e)
	}
}
