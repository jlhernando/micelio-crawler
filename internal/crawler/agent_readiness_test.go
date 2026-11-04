package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func agentOpts() AgentCheckOptions {
	return AgentCheckOptions{Fetch: FetchOptions{SkipSSRF: true, UserAgent: "Micelio/1.0"}}
}

func checkStatus(t *testing.T, rep types.AgentHostReport, want map[string]string) {
	t.Helper()
	got := map[string]types.AgentCheck{}
	for _, c := range rep.Checks {
		got[c.ID] = c
	}
	for id, st := range want {
		c, ok := got[id]
		if !ok {
			t.Errorf("check %s missing", id)
			continue
		}
		if c.Status != st {
			t.Errorf("check %s = %s, want %s (evidence %q)", id, c.Status, st, c.Evidence)
		}
	}
}

const htmlNotFound = `<!DOCTYPE html><html><head><title>Not found</title></head><body>404</body></html>`

// openRobotsServer models petsitters.example: robots.txt allows everything with no AI
// rules, llms.txt and llms-full.txt answer 200 with the HTML home page
// (noindex), ai-plugin.json and openapi.yaml exist, every other file is a
// 404 HTML page, and the firewall blocks GPTBot and ClaudeBot.
func openRobotsServer(t *testing.T, hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		ua := r.UserAgent()
		if strings.Contains(ua, "GPTBot") || strings.Contains(ua, "ClaudeBot") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nAllow: /\nSitemap: https://www.petsitters.example/sitemap.xml\n")
		case "/", "/llms.txt", "/llms-full.txt":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Example</title><meta name="robots" content="noindex">`+
				`<script type="application/ld+json">{"@context":"https://schema.org","@type":"Organization","name":"Example"}</script>`+
				`</head><body><h1>Pet sitters</h1></body></html>`)
		case "/.well-known/ai-plugin.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"schema_version":"v1","name_for_human":"Example PetSitter Finder","name_for_model":"example","api":{"type":"openapi","url":"/openapi.yaml"}}`)
		case "/openapi.yaml":
			w.Header().Set("Content-Type", "text/yaml")
			fmt.Fprint(w, "openapi: 3.0.1\ninfo:\n  title: Example\n")
		default:
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, htmlNotFound)
		}
	}))
}

func TestAgentReadinessExample(t *testing.T) {
	var hits atomic.Int32
	srv := openRobotsServer(t, &hits)
	defer srv.Close()

	rep := CheckAgentReadiness(context.Background(), srv.URL, agentOpts())
	checkStatus(t, rep, map[string]string{
		"robots-txt":           types.AgentPass,
		"robots-sitemap":       types.AgentPass,
		"ai-bot-rules":         types.AgentWarn,
		"ai-agents-access":     types.AgentPass,
		"content-signals":      types.AgentWarn,
		"llms-txt":             types.AgentFail,
		"llms-full-txt":        types.AgentFail,
		"markdown-negotiation": types.AgentWarn,
		"markdown-variant":     types.AgentNA,
		"ai-plugin":            types.AgentWarn,
		"openapi":              types.AgentPass,
		"mcp-server-card":      types.AgentNA,
		"a2a-agent-card":       types.AgentNA,
		"api-catalog":          types.AgentNA,
		"web-bot-auth":         types.AgentNA,
		"security-txt":         types.AgentWarn,
		"link-headers":         types.AgentWarn,
		"ai-bot-firewall":      types.AgentWarn,
		"webmcp":               types.AgentNA,
	})
	for _, c := range rep.Checks {
		if c.ID == "llms-txt" && !strings.Contains(c.Evidence, "noindex") {
			t.Errorf("llms.txt evidence should mention noindex: %q", c.Evidence)
		}
	}
	if rep.Requests > agentMaxRequests || int(hits.Load()) != rep.Requests {
		t.Errorf("requests = %d (server saw %d), max %d", rep.Requests, hits.Load(), agentMaxRequests)
	}
	if len(rep.HomeSchema) != 1 || rep.HomeSchema[0] != "Organization" {
		t.Errorf("home schema = %v", rep.HomeSchema)
	}
	for _, b := range rep.Bots {
		if b.Allowed == nil || !*b.Allowed || b.Explicit {
			t.Errorf("bot %s: allowed=%v explicit=%v, want allowed by *", b.Bot, b.Allowed, b.Explicit)
		}
		if (b.Bot == "GPTBot" || b.Bot == "ClaudeBot") && b.LiveStatus != 403 {
			t.Errorf("bot %s live status = %d, want 403", b.Bot, b.LiveStatus)
		}
	}
}

// agentReadyServer publishes everything an agent-ready site should.
func agentReadyServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		js := func(body string) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, body)
		}
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: GPTBot\nUser-agent: CCBot\nDisallow: /\n\nUser-agent: OAI-SearchBot\nAllow: /\n\n"+
				"User-agent: *\nContent-Signal: search=yes, ai-input=yes, ai-train=no\nAllow: /\n\nSitemap: /sitemap.xml\n")
		case "/":
			if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
				w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
				w.Header().Set("Vary", "Accept")
				fmt.Fprint(w, "# Shop\n\nWelcome.")
				return
			}
			w.Header().Set("Link", `</.well-known/api-catalog>; rel="api-catalog", </llms.txt>; rel="llms-txt"`)
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Shop</title><link rel="alternate" type="application/rss+xml" href="/feed.xml">`+
				`<script>navigator.modelContext.registerTool({name:"search"})</script></head><body><h1>Shop</h1></body></html>`)
		case "/index.md":
			w.Header().Set("Content-Type", "text/markdown")
			fmt.Fprint(w, "# Shop\n")
		case "/llms.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "# Shop\n\n> Things we sell.\n\n## Docs\n\n- [Products](https://shop.example/products.md): catalogue\n")
		case "/llms-full.txt":
			w.Header().Set("Content-Type", "text/markdown")
			fmt.Fprint(w, "# Shop\n\nEverything.\n")
		case "/.well-known/mcp/server-card.json":
			js(`{"serverInfo":{"name":"shop-mcp","version":"1.0"},"transport":{"type":"streamable-http","endpoint":"/mcp"}}`)
		case "/.well-known/agent-card.json":
			js(`{"name":"Shop agent","url":"https://shop.example/a2a","skills":[{"id":"search"}]}`)
		case "/.well-known/api-catalog":
			w.Header().Set("Content-Type", "application/linkset+json")
			fmt.Fprint(w, `{"linkset":[{"anchor":"https://shop.example/api","service-desc":[{"href":"/openapi.json","type":"application/json"}]}]}`)
		case "/openapi.json":
			js(`{"openapi":"3.1.0","info":{"title":"Shop"}}`)
		case "/.well-known/oauth-authorization-server":
			js(`{"issuer":"https://shop.example","token_endpoint":"https://shop.example/token"}`)
		case "/.well-known/oauth-protected-resource":
			js(`{"resource":"https://shop.example/api"}`)
		case "/.well-known/http-message-signatures-directory":
			w.Header().Set("Content-Type", "application/http-message-signatures-directory+json")
			fmt.Fprint(w, `{"keys":[{"kty":"OKP","crv":"Ed25519","x":"abc"}]}`)
		case "/.well-known/agent-skills/index.json":
			js(`{"skills": [`) // broken JSON
		case "/.well-known/security.txt":
			fmt.Fprint(w, "Contact: mailto:security@shop.example\nExpires: 2030-01-01T00:00:00Z\n")
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestAgentReadinessAgentReadySite(t *testing.T) {
	srv := agentReadyServer()
	defer srv.Close()
	rep := CheckAgentReadiness(context.Background(), srv.URL, agentOpts())
	checkStatus(t, rep, map[string]string{
		"robots-txt":                 types.AgentPass,
		"robots-sitemap":             types.AgentPass,
		"ai-bot-rules":               types.AgentPass,
		"ai-agents-access":           types.AgentPass,
		"content-signals":            types.AgentPass,
		"llms-txt":                   types.AgentPass,
		"llms-full-txt":              types.AgentPass,
		"markdown-negotiation":       types.AgentPass,
		"markdown-variant":           types.AgentPass,
		"link-headers":               types.AgentPass,
		"feeds":                      types.AgentPass,
		"webmcp":                     types.AgentPass,
		"mcp-server-card":            types.AgentPass,
		"a2a-agent-card":             types.AgentPass,
		"api-catalog":                types.AgentPass,
		"openapi":                    types.AgentPass,
		"oauth-authorization-server": types.AgentPass,
		"oauth-protected-resource":   types.AgentPass,
		"web-bot-auth":               types.AgentPass,
		"agent-skills":               types.AgentFail, // invalid JSON
		"security-txt":               types.AgentPass,
		"ai-plugin":                  types.AgentNA,
		"ai-bot-firewall":            types.AgentPass, // ClaudeBot tested, GPTBot disallowed so not sent
	})
	bots := map[string]types.AgentBotAccess{}
	for _, b := range rep.Bots {
		bots[b.Bot] = b
	}
	if b := bots["GPTBot"]; b.Allowed == nil || *b.Allowed || !b.Explicit || b.LiveStatus != 0 {
		t.Errorf("GPTBot = %+v, want blocked, explicit, not fetched live", b)
	}
	if b := bots["OAI-SearchBot"]; b.Allowed == nil || !*b.Allowed || !b.Explicit {
		t.Errorf("OAI-SearchBot = %+v, want allowed explicitly", b)
	}
	if b := bots["ClaudeBot"]; b.Allowed == nil || !*b.Allowed || b.Explicit || b.LiveStatus != 200 {
		t.Errorf("ClaudeBot = %+v, want allowed by *, live 200", b)
	}
	if len(rep.ContentSignals) != 1 {
		t.Errorf("content signals = %v", rep.ContentSignals)
	}
}

func TestAgentReadinessRobotsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	rep := CheckAgentReadiness(context.Background(), srv.URL, agentOpts())
	checkStatus(t, rep, map[string]string{
		"robots-txt":       types.AgentFail,
		"ai-agents-access": types.AgentFail,
		"ai-bot-rules":     types.AgentNA,
		"llms-txt":         types.AgentWarn, // plain 404
	})
	for _, b := range rep.Bots {
		if b.Allowed != nil {
			t.Fatalf("bot %s allowed should be unknown", b.Bot)
		}
	}
}

func TestAgentReadinessBudgetAndRobots(t *testing.T) {
	var hits atomic.Int32
	srv := openRobotsServer(t, &hits)
	defer srv.Close()
	opts := agentOpts()
	opts.MaxRequests = 5
	rep := CheckAgentReadiness(context.Background(), srv.URL, opts)
	if rep.Requests != 5 || hits.Load() != 5 {
		t.Fatalf("requests = %d, server saw %d, want 5", rep.Requests, hits.Load())
	}

	// The crawler's robots.txt rules apply to the check's own requests.
	hits.Store(0)
	opts = agentOpts()
	opts.Allowed = func(_ context.Context, u string) bool { return !strings.Contains(u, "/.well-known/") }
	rep = CheckAgentReadiness(context.Background(), srv.URL, opts)
	for _, c := range rep.Checks {
		if strings.Contains(c.URL, "/.well-known/") && c.Status != types.AgentNA {
			t.Errorf("%s: %s, want na (blocked by robots.txt)", c.ID, c.Status)
		}
	}
}

func TestCrawlRunsAgentCheck(t *testing.T) {
	srv := agentReadyServer()
	defer srv.Close()
	cfg := types.CrawlConfig{SeedURL: srv.URL + "/", Mode: types.ModeSpider, MaxDepth: 1, MaxPages: 2, Concurrency: 1, SkipSSRF: true, NoSoft404Probe: true}
	res, err := Crawl(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.AgentReadiness) != 1 || len(res.AgentReadiness[0].Checks) == 0 {
		t.Fatalf("agent readiness = %+v", res.AgentReadiness)
	}

	cfg.NoAgentCheck = true
	res, err = Crawl(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.AgentReadiness) != 0 {
		t.Fatalf("NoAgentCheck still ran: %+v", res.AgentReadiness)
	}
}

func TestLLMsFormatAndContentSignals(t *testing.T) {
	h1, links := llmsFormat("\xef\xbb\xbf# Site\n\n> x\n\n- [A](https://a.example/a.md): a\n- [B](/b)\n")
	if h1 != "Site" || links != 2 {
		t.Fatalf("llmsFormat = %q, %d", h1, links)
	}
	if h1, _ := llmsFormat("Site\n# Later"); h1 != "" {
		t.Fatalf("H1 must be the first line, got %q", h1)
	}
	sig := contentSignals("User-agent: *\ncontent-signal: search=yes,ai-train=no # comment\nAllow: /\n")
	if len(sig) != 1 || !validContentSignals(sig) {
		t.Fatalf("signals = %v", sig)
	}
	if validContentSignals([]string{"search=maybe"}) || validContentSignals([]string{"foo=yes"}) {
		t.Fatal("invalid signals accepted")
	}
}
