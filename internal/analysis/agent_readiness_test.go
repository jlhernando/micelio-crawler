package analysis

import (
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func findAgentCheck(r types.AgentHostReport, id string) types.AgentCheck {
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	return types.AgentCheck{}
}

func TestBuildAgentReadinessCrawlChecks(t *testing.T) {
	jsonld := []types.StructuredDataEntry{{Type: "Product", Format: types.FormatJSONLD}}
	pages := []*types.PageData{
		{URL: "https://www.shop.example/a", StatusCode: 200, StructuredData: jsonld, JSRendered: true},
		{URL: "https://www.shop.example/b", StatusCode: 200, StructuredData: jsonld, JSRendered: true,
			RenderDiffs: []types.RenderDiff{{Field: "word_count", Original: "0", Rendered: "400"}}},
		{URL: "https://www.shop.example/c", StatusCode: 200, JSRendered: true,
			RenderDiffs: []types.RenderDiff{{Field: "h1", Original: "", Rendered: "Shoes"}}},
		{URL: "https://www.shop.example/d", StatusCode: 200, JSRendered: true,
			RenderDiffs: []types.RenderDiff{{Field: "word_count", Original: "300", Rendered: "380"}}}, // small change: fine
		{URL: "https://www.shop.example/gone", StatusCode: 404}, // not counted
	}
	probe := types.AgentHostReport{Host: "shop.example", URL: "https://shop.example", Checks: []types.AgentCheck{
		{ID: "robots-txt", Category: "discovery", Status: types.AgentPass},
		{ID: "llms-txt", Category: "discovery", Status: types.AgentFail},
		{ID: "mcp-server-card", Category: "protocols", Status: types.AgentNA},
	}}
	st := BuildAgentReadiness([]types.AgentHostReport{probe}, pages)
	if st == nil || len(st.Hosts) != 1 {
		t.Fatalf("stats = %+v", st)
	}
	h := st.Hosts[0]
	sd := findAgentCheck(h, "structured-data")
	if sd.Status != types.AgentPass || !strings.Contains(sd.Evidence, "2 of 4 pages") || !strings.Contains(sd.Evidence, "Product (2)") {
		t.Errorf("structured-data = %+v", sd)
	}
	js := findAgentCheck(h, "js-content")
	if js.Status != types.AgentFail || !strings.HasPrefix(js.Evidence, "2 of 4 rendered pages (50%)") {
		t.Errorf("js-content = %+v", js)
	}
	// robots 3 pass, llms 3 fail, structured 3 pass, js 3 fail: (6+0+6+0)/24.
	if h.Score != 50 || h.Level != "Basic" || st.Score != h.Score {
		t.Errorf("score = %d %q", h.Score, h.Level)
	}
	if len(probe.Checks) != 3 {
		t.Error("probe checks were modified")
	}
	for _, c := range h.Categories {
		if c.Category == "protocols" && (c.Score != -1 || c.NA != 1) {
			t.Errorf("protocols with only na = %+v", c)
		}
	}
}

func TestBuildAgentReadinessWithoutPages(t *testing.T) {
	if BuildAgentReadiness(nil, nil) != nil {
		t.Fatal("no probes should give nil")
	}
	probe := types.AgentHostReport{Host: "a.example", HomeSchema: []string{"Organization"}}
	h := BuildAgentReadiness([]types.AgentHostReport{probe}, nil).Hosts[0]
	if c := findAgentCheck(h, "structured-data"); c.Status != types.AgentPass {
		t.Errorf("home JSON-LD = %+v", c)
	}
	if c := findAgentCheck(h, "js-content"); c.Status != types.AgentNA {
		t.Errorf("js-content without rendering = %+v", c)
	}

	pages := []*types.PageData{{URL: "https://a.example/", StatusCode: 200}}
	h = BuildAgentReadiness([]types.AgentHostReport{probe}, pages).Hosts[0]
	if c := findAgentCheck(h, "structured-data"); c.Status != types.AgentFail {
		t.Errorf("no JSON-LD on crawled pages = %+v", c)
	}
}

func TestAgentLevel(t *testing.T) {
	for score, want := range map[int]string{100: "Agent-ready", 80: "Agent-ready", 79: "Good", 40: "Basic", 0: "Not ready"} {
		if got := AgentLevel(score); got != want {
			t.Errorf("AgentLevel(%d) = %q, want %q", score, got, want)
		}
	}
}
