package analysis

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// agentCheckWeights is how much each check counts in the score. Checks not
// listed weigh 1. na checks are left out.
var agentCheckWeights = map[string]int{
	"robots-txt":           3,
	"ai-agents-access":     3,
	"llms-txt":             3,
	"markdown-negotiation": 3,
	"structured-data":      3,
	"js-content":           3,
	"robots-sitemap":       2,
	"ai-bot-rules":         2,
	"content-signals":      2,
	"ai-bot-firewall":      2,
	"mcp-server-card":      2,
}

// agentCategories is the display order of check categories.
var agentCategories = []string{"discovery", "content", "access", "protocols"}

// BuildAgentReadiness adds the checks that come from the crawled pages to
// each host probe and scores them. Returns nil without probes.
func BuildAgentReadiness(reports []types.AgentHostReport, pages []*types.PageData) *types.AgentReadinessStats {
	if len(reports) == 0 {
		return nil
	}
	out := &types.AgentReadinessStats{Hosts: make([]types.AgentHostReport, 0, len(reports))}
	for _, r := range reports {
		r.Checks = append([]types.AgentCheck(nil), r.Checks...)
		var hostPages []*types.PageData
		for _, p := range pages {
			// One host: every crawled page counts (the seed may redirect,
			// e.g. apex to www). Several: match the host, www or not.
			if h := pageHost(p); h != "" && (len(reports) == 1 || strings.TrimPrefix(h, "www.") == strings.TrimPrefix(r.Host, "www.")) {
				hostPages = append(hostPages, p)
			}
		}
		r.Checks = append(r.Checks, structuredDataCheck(r, hostPages), jsContentCheck(hostPages))
		ScoreAgentHost(&r)
		out.Hosts = append(out.Hosts, r)
	}
	out.Score, out.Level = out.Hosts[0].Score, out.Hosts[0].Level
	return out
}

func pageHost(p *types.PageData) string {
	if p.RobotsBlocked || p.StatusCode != 200 {
		return ""
	}
	u, err := url.Parse(p.URL)
	if err != nil {
		return ""
	}
	return u.Host
}

// structuredDataCheck: share of crawled 200 pages with JSON-LD, or the home
// page's JSON-LD when no pages were crawled (micelio agent-check).
func structuredDataCheck(r types.AgentHostReport, pages []*types.PageData) types.AgentCheck {
	c := types.AgentCheck{ID: "structured-data", Category: "content", Title: "Structured data (JSON-LD)"}
	fix := "Describe your main entities with JSON-LD (Organization, Product, Article, BreadcrumbList…) in the raw HTML so agents get facts without guessing."
	if len(pages) == 0 {
		switch {
		case len(r.HomeSchema) > 0:
			c.Status, c.Evidence = types.AgentPass, "Home page JSON-LD: "+strings.Join(r.HomeSchema, ", ")
		case r.Error == "" && hasCheck(r, "link-headers", types.AgentPass, types.AgentWarn):
			c.Status, c.Evidence, c.Fix = types.AgentWarn, "No JSON-LD in the home page raw HTML", fix
		default:
			c.Status, c.Evidence = types.AgentNA, "No pages to inspect"
		}
		return c
	}
	typeCounts := map[string]int{}
	with := 0
	for _, p := range pages {
		has := false
		for _, sd := range p.StructuredData {
			if sd.Format != types.FormatJSONLD {
				continue
			}
			has = true
			for _, t := range strings.Split(sd.Type, ",") {
				if t = strings.TrimSpace(t); t != "" {
					typeCounts[t]++
				}
			}
		}
		if has {
			with++
		}
	}
	pct := with * 100 / len(pages)
	c.Evidence = fmt.Sprintf("%d of %d pages (%d%%) have JSON-LD", with, len(pages), pct)
	if top := topTypes(typeCounts, 5); top != "" {
		c.Evidence += "; main types: " + top
	}
	switch {
	case pct >= 50:
		c.Status = types.AgentPass
	case with > 0:
		c.Status, c.Fix = types.AgentWarn, fix
	default:
		c.Status, c.Fix = types.AgentFail, fix
	}
	return c
}

func topTypes(counts map[string]int, n int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, 0, n)
	for _, k := range keys[:min(n, len(keys))] {
		parts = append(parts, fmt.Sprintf("%s (%d)", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}

// jsContentCheck: with JS rendering, share of pages whose title, h1, text,
// links or JSON-LD only appear after JavaScript. Most AI crawlers do not
// run JavaScript.
func jsContentCheck(pages []*types.PageData) types.AgentCheck {
	c := types.AgentCheck{ID: "js-content", Category: "content", Title: "Key content in the raw HTML (no JavaScript needed)"}
	rendered, needJS := 0, 0
	fields := map[string]int{}
	for _, p := range pages {
		if !p.JSRendered {
			continue
		}
		rendered++
		need := false
		for _, d := range p.RenderDiffs {
			if renderDiffNeedsJS(d) {
				need = true
				fields[d.Field]++
			}
		}
		if need {
			needJS++
		}
	}
	if rendered == 0 {
		c.Status, c.Evidence = types.AgentNA, "JavaScript rendering was off; turn it on to see which content needs JavaScript"
		return c
	}
	pct := needJS * 100 / rendered
	c.Evidence = fmt.Sprintf("%d of %d rendered pages (%d%%) show key content only after JavaScript", needJS, rendered, pct)
	if top := topTypes(fields, 5); top != "" {
		c.Evidence += ": " + strings.ReplaceAll(top, "_", " ")
	}
	switch {
	case pct < 10:
		c.Status = types.AgentPass
	case pct < 50:
		c.Status = types.AgentWarn
	default:
		c.Status = types.AgentFail
	}
	if c.Status != types.AgentPass {
		c.Fix = "Server-render titles, headings, main text, links and JSON-LD. GPTBot, ClaudeBot and PerplexityBot read the raw HTML only."
	}
	return c
}

func renderDiffNeedsJS(d types.RenderDiff) bool {
	switch d.Field {
	case "title", "h1":
		return strings.TrimSpace(d.Original) == "" && strings.TrimSpace(d.Rendered) != ""
	case "word_count", "internal_links_count", "json_ld_count":
		o, err1 := strconv.Atoi(d.Original)
		r, err2 := strconv.Atoi(d.Rendered)
		if err1 != nil || err2 != nil {
			return false
		}
		if d.Field == "json_ld_count" {
			return r > o
		}
		return r > 2*o && r-o >= 10
	}
	return false
}

func hasCheck(r types.AgentHostReport, id string, statuses ...string) bool {
	for _, c := range r.Checks {
		if c.ID == id {
			for _, s := range statuses {
				if c.Status == s {
					return true
				}
			}
		}
	}
	return false
}

// ScoreAgentHost sets the score (0-100), level and category scores of a
// host report from its checks: pass counts fully, warn half, fail nothing;
// na checks are left out.
func ScoreAgentHost(r *types.AgentHostReport) {
	r.Categories = r.Categories[:0]
	total, got := 0, 0
	for _, cat := range agentCategories {
		cs := types.AgentCategoryScore{Category: cat}
		ct, cg := 0, 0
		for _, c := range r.Checks {
			if c.Category != cat {
				continue
			}
			w := agentCheckWeights[c.ID]
			if w == 0 {
				w = 1
			}
			switch c.Status {
			case types.AgentPass:
				cs.Pass++
				ct += 2 * w
				cg += 2 * w
			case types.AgentWarn:
				cs.Warn++
				ct += 2 * w
				cg += w
			case types.AgentFail:
				cs.Fail++
				ct += 2 * w
			default:
				cs.NA++
			}
		}
		cs.Score = -1
		if ct > 0 {
			cs.Score = cg * 100 / ct
		}
		total += ct
		got += cg
		r.Categories = append(r.Categories, cs)
	}
	r.Score = 0
	if total > 0 {
		r.Score = got * 100 / total
	}
	r.Level = AgentLevel(r.Score)
}

// AgentLevel names a score band.
func AgentLevel(score int) string {
	switch {
	case score >= 80:
		return "Agent-ready"
	case score >= 60:
		return "Good"
	case score >= 40:
		return "Basic"
	default:
		return "Not ready"
	}
}
