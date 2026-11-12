package mcpserver

import (
	"encoding/json"
	"testing"
)

// The stats JSON keys written by the reporter reach the html-over-2mb issue.
func TestIssueDefsHTMLOver2MB(t *testing.T) {
	var s summaryStats
	raw := `{"htmlOver2mbPages":[{"url":"https://ex.com/big","bytes":3145728}],"totalHtmlOver2mbPages":7}`
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	for _, d := range issueDefs(s) {
		if d.id == "html-over-2mb" {
			if d.count != 7 || len(d.samples) != 1 || d.samples[0] != "https://ex.com/big" || d.filter["issue"] != "html-over-2mb" {
				t.Errorf("issue %+v", d)
			}
			return
		}
	}
	t.Fatal("html-over-2mb issue missing")
}
