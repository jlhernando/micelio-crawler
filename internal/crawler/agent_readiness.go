package crawler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/extract"
	"github.com/micelio/micelio/internal/types"
)

// AgentReadinessMetaKey is the crawl DB metadata key holding the agent
// readiness probes ([]types.AgentHostReport), so `micelio import` can use them.
const AgentReadinessMetaKey = "agentReadiness"

const (
	agentMaxRequests = 25               // per host, redirect hops included
	agentMaxHosts    = 5                // hosts probed per crawl
	agentReqTimeout  = 10 * time.Second // per request
	agentHostTimeout = 60 * time.Second // whole probe of one host
	agentMaxBody     = 512 * 1024
	agentMaxHops     = 3
)

// AIBot is an AI crawler or agent that robots.txt can address.
type AIBot struct {
	Token   string // robots.txt product token
	Owner   string
	Purpose string // training, search, user
	UA      string // full User-Agent, set for the bots tested live
}

// AIBots are the AI bots reported in the robots.txt table.
var AIBots = []AIBot{
	{Token: "GPTBot", Owner: "OpenAI", Purpose: "training", UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; GPTBot/1.2; +https://openai.com/gptbot"},
	{Token: "OAI-SearchBot", Owner: "OpenAI", Purpose: "search"},
	{Token: "ChatGPT-User", Owner: "OpenAI", Purpose: "user"},
	{Token: "ClaudeBot", Owner: "Anthropic", Purpose: "training", UA: "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)"},
	{Token: "Claude-SearchBot", Owner: "Anthropic", Purpose: "search"},
	{Token: "Claude-User", Owner: "Anthropic", Purpose: "user"},
	{Token: "PerplexityBot", Owner: "Perplexity", Purpose: "search"},
	{Token: "Perplexity-User", Owner: "Perplexity", Purpose: "user"},
	{Token: "Google-Extended", Owner: "Google", Purpose: "training"},
	{Token: "Applebot-Extended", Owner: "Apple", Purpose: "training"},
	{Token: "Amazonbot", Owner: "Amazon", Purpose: "search"},
	{Token: "DuckAssistBot", Owner: "DuckDuckGo", Purpose: "search"},
	{Token: "MistralAI-User", Owner: "Mistral", Purpose: "user"},
	{Token: "meta-externalagent", Owner: "Meta", Purpose: "training"},
	{Token: "meta-externalfetcher", Owner: "Meta", Purpose: "user"},
	{Token: "CCBot", Owner: "Common Crawl", Purpose: "training"},
	{Token: "Bytespider", Owner: "ByteDance", Purpose: "training"},
	{Token: "cohere-ai", Owner: "Cohere", Purpose: "training"},
}

// AgentCheckOptions configures CheckAgentReadiness.
type AgentCheckOptions struct {
	// Fetch carries the crawl's client (proxy, Tor, stealth, SSRF guard),
	// User-Agent, custom headers, cookies and per-host Pace.
	Fetch FetchOptions
	// Allowed reports whether robots.txt lets the crawler fetch a URL. It is
	// asked for every request except robots.txt itself (requests sent as an
	// AI bot follow that bot's rules instead). Nil allows everything.
	Allowed func(ctx context.Context, rawURL string) bool
	// MaxRequests caps the requests sent to the host (default 25).
	MaxRequests int
}

// agentResp is one probe response.
type agentResp struct {
	url      string
	finalURL string
	status   int
	ctype    string
	header   http.Header
	body     []byte
	err      string
}

// state classifies a response for a file that should not be HTML:
// ok, missing (4xx), soft404 (200 with an HTML page or a redirect to the
// home page), error (5xx, network) or skipped (robots, budget).
func (r agentResp) state(origin string, htmlOK bool) string {
	switch {
	case r.err != "" && r.status == 0:
		if strings.HasPrefix(r.err, "skipped") {
			return "skipped"
		}
		return "error"
	case r.status >= 200 && r.status < 300:
		if !htmlOK && looksHTML(r) {
			return "soft404"
		}
		if r.finalURL != r.url && strings.TrimRight(r.finalURL, "/") == strings.TrimRight(origin, "/") {
			return "soft404" // redirected to the home page
		}
		return "ok"
	case r.status == 429 || r.status >= 500:
		return "error"
	default:
		return "missing"
	}
}

func looksHTML(r agentResp) bool {
	ct := strings.ToLower(r.ctype)
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "xhtml") {
		return true
	}
	head := bytes.ToLower(bytes.TrimSpace(r.body[:min(len(r.body), 512)]))
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	return bytes.HasPrefix(head, []byte("<!doctype html")) || bytes.HasPrefix(head, []byte("<html")) ||
		(bytes.HasPrefix(head, []byte("<")) && bytes.Contains(head, []byte("<head")))
}

func (r agentResp) describe() string {
	if r.status == 0 {
		return r.err
	}
	s := fmt.Sprintf("%d", r.status)
	if r.ctype != "" {
		s += " " + strings.TrimSpace(strings.SplitN(r.ctype, ";", 2)[0])
	}
	if r.finalURL != "" && r.finalURL != r.url {
		s += " after redirect to " + r.finalURL
	}
	return s
}

// agentProbe runs the checks for one host.
type agentProbe struct {
	opts     AgentCheckOptions
	client   *http.Client
	origin   string
	site     *url.URL // checked site: only it gets the crawl's cookies and custom headers
	page     string   // page used for markdown/home checks (seed URL or origin "/")
	requests int
	max      int
	rep      types.AgentHostReport
}

// CheckAgentReadiness probes one site for AI agent readiness: robots.txt AI
// rules and Content-Signal, llms.txt, markdown negotiation, Link headers and
// the /.well-known agent files. It sends at most opts.MaxRequests requests.
// The returned report has no score yet (analysis.ScoreAgentHost).
func CheckAgentReadiness(ctx context.Context, siteURL string, opts AgentCheckOptions) types.AgentHostReport {
	u, err := url.Parse(siteURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return types.AgentHostReport{URL: siteURL, Error: "invalid URL"}
	}
	p := &agentProbe{opts: opts, origin: u.Scheme + "://" + u.Host, site: u, max: opts.MaxRequests}
	if p.max <= 0 {
		p.max = agentMaxRequests
	}
	p.page = siteURL
	if u.Path == "" {
		p.page = p.origin + "/"
	}
	p.client = opts.Fetch.Client
	if p.client == nil {
		switch {
		case opts.Fetch.Stealth:
			p.client, _ = StealthClient(agentReqTimeout, nil)
		case opts.Fetch.SkipSSRF:
			p.client = UnsafeClient(agentReqTimeout, nil)
		}
		if p.client == nil {
			p.client = DefaultClient(agentReqTimeout, nil)
		}
	}
	if p.opts.Fetch.UserAgent == "" {
		p.opts.Fetch.UserAgent = "Micelio/1.0"
	}
	p.rep = types.AgentHostReport{Host: u.Host, URL: p.origin, CheckedAt: time.Now().UTC().Format(time.RFC3339)}

	ctx, cancel := context.WithTimeout(ctx, agentHostTimeout)
	defer cancel()
	p.run(ctx)
	p.rep.Requests = p.requests
	return p.rep
}

// get fetches path (absolute or origin-relative) following up to 3
// redirects. accept "" keeps the crawler's normal Accept header.
func (p *agentProbe) get(ctx context.Context, target, accept, ua string, robots bool) agentResp {
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = p.origin + target
	}
	r := agentResp{url: target, finalURL: target}
	if robots && p.opts.Allowed != nil && !p.opts.Allowed(ctx, target) {
		r.err = "skipped: blocked by robots.txt"
		return r
	}
	fo := p.opts.Fetch
	if ua != "" {
		fo.UserAgent = ua
		fo.Stealth = false
	}
	current := target
	for hop := 0; ; hop++ {
		if p.requests >= p.max {
			r.err = "skipped: request budget reached"
			return r
		}
		if ctx.Err() != nil {
			r.err = "skipped: time limit reached"
			return r
		}
		if !fo.SkipSSRF && IsPrivateURL(current) {
			r.err = "blocked: private/loopback address"
			return r
		}
		rctx, cancel := context.WithTimeout(ctx, agentReqTimeout)
		req, err := newRequest(rctx, http.MethodGet, current)
		if err != nil {
			cancel()
			r.err = err.Error()
			return r
		}
		// Cookies and custom headers only go to the checked site, never to
		// a redirect target or an OpenAPI URL on another host.
		setHeadersFor(req, fo, credentialsAllowed(p.site, req.URL))
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		p.requests++
		resp, err := pacedDo(p.client, req, fo.Pace)
		if err != nil {
			cancel()
			r.err = FormatError(err)
			return r
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 && resp.Header.Get("Location") != "" && hop < agentMaxHops {
			drainAndClose(resp.Body)
			cancel()
			current = resolveURL(current, resp.Header.Get("Location"))
			r.finalURL = current
			continue
		}
		r.status = resp.StatusCode
		r.ctype = resp.Header.Get("Content-Type")
		r.header = resp.Header
		r.body, _ = io.ReadAll(io.LimitReader(resp.Body, agentMaxBody))
		resp.Body.Close()
		cancel()
		return r
	}
}

func (p *agentProbe) add(c types.AgentCheck) {
	p.rep.Checks = append(p.rep.Checks, c)
}

func (p *agentProbe) run(ctx context.Context) {
	// Most important first: the request budget may run out.
	robotsBody := p.checkRobots(ctx)
	home := p.checkHome(ctx)
	p.checkMarkdown(ctx, home)
	p.checkLLMs(ctx)
	p.checkBotsLive(ctx, home, robotsBody)
	openapiRefs := p.checkWellKnown(ctx)
	p.checkOpenAPI(ctx, openapiRefs)
	p.checkMarkdownVariant(ctx)
}

// adopt switches the probe to the origin a redirect led to (http to https,
// apex to www), so later requests do not spend the budget on redirects.
func (p *agentProbe) adopt(finalURL string) {
	fu, err := url.Parse(finalURL)
	if err != nil || fu.Host == "" {
		return
	}
	origin := fu.Scheme + "://" + fu.Host
	if origin == p.origin {
		return
	}
	if pu, err := url.Parse(p.page); err == nil {
		pu.Scheme, pu.Host = fu.Scheme, fu.Host
		p.page = pu.String()
	}
	p.origin = origin
	p.rep.URL, p.rep.Host = origin, fu.Host
}

// --- robots.txt ---

func (p *agentProbe) checkRobots(ctx context.Context) string {
	r := p.get(ctx, "/robots.txt", "text/plain,*/*;q=0.8", "", false)
	if r.status != 0 {
		p.adopt(r.finalURL)
	}
	c := types.AgentCheck{ID: "robots-txt", Category: "discovery", Title: "robots.txt", URL: r.url}
	body := ""
	unavailable := false
	switch r.state(p.origin, false) {
	case "ok":
		body = string(r.body)
		c.Status, c.Evidence = types.AgentPass, r.describe()
	case "soft404":
		c.Status = types.AgentFail
		c.Evidence = r.describe() + ": an HTML page instead of robots.txt rules"
		c.Fix = "Serve a plain-text robots.txt (or a real 404) at /robots.txt."
	case "missing":
		c.Status = types.AgentWarn
		c.Evidence = r.describe() + ": every bot may crawl everything, and there is no place to state an AI policy"
		c.Fix = "Publish /robots.txt with your rules for AI bots and a Sitemap line."
	default:
		unavailable = true
		c.Status = types.AgentFail
		c.Evidence = r.describe() + ": crawlers treat the whole site as blocked while robots.txt fails"
		c.Fix = "Make /robots.txt answer 200 (or 404) quickly; 5xx and 429 block crawlers."
	}
	p.add(c)

	rules := parseRobots(body)
	sm := types.AgentCheck{ID: "robots-sitemap", Category: "discovery", Title: "Sitemap in robots.txt", URL: r.url}
	if len(rules.sitemaps) > 0 {
		sm.Status = types.AgentPass
		sm.Evidence = fmt.Sprintf("%d declared: %s", len(rules.sitemaps), strings.Join(rules.sitemaps[:min(3, len(rules.sitemaps))], ", "))
	} else {
		sm.Status = types.AgentWarn
		sm.Evidence = "No Sitemap line"
		sm.Fix = "Add `Sitemap: https://…/sitemap.xml` to robots.txt so crawlers find every page."
	}
	p.add(sm)

	// Per-bot access to the page.
	var explicit, blockedAgents, agents []string
	for _, b := range AIBots {
		ba := types.AgentBotAccess{Bot: b.Token, Owner: b.Owner, Purpose: b.Purpose}
		if !unavailable {
			allowed := body == "" || rules.selectGroups(b.Token).allowed(p.page)
			ba.Allowed = &allowed
			ba.Explicit = rules.namesAgent(b.Token)
			if ba.Explicit {
				explicit = append(explicit, b.Token)
			}
			if b.Purpose != "training" {
				agents = append(agents, b.Token)
				if !allowed {
					blockedAgents = append(blockedAgents, b.Token)
				}
			}
		}
		p.rep.Bots = append(p.rep.Bots, ba)
	}

	rc := types.AgentCheck{ID: "ai-bot-rules", Category: "access", Title: "robots.txt rules for AI bots", URL: r.url}
	switch {
	case unavailable:
		rc.Status, rc.Evidence = types.AgentNA, "robots.txt could not be read"
	case len(explicit) > 0:
		rc.Status = types.AgentPass
		rc.Evidence = fmt.Sprintf("Rules for %d of %d AI bots: %s", len(explicit), len(AIBots), strings.Join(explicit, ", "))
	default:
		rc.Status = types.AgentWarn
		rc.Evidence = "No group names an AI bot: they all follow the * group"
		rc.Fix = "Add User-agent groups for the AI bots you want to allow or block (GPTBot, OAI-SearchBot, ClaudeBot, PerplexityBot, Google-Extended…)."
	}
	p.add(rc)

	ac := types.AgentCheck{ID: "ai-agents-access", Category: "access", Title: "AI search and user agents may read the site", URL: p.page}
	switch {
	case unavailable:
		ac.Status = types.AgentFail
		ac.Evidence = "robots.txt fails, so every AI agent treats the site as blocked"
		ac.Fix = "Fix robots.txt first."
	case len(blockedAgents) == 0:
		ac.Status = types.AgentPass
		ac.Evidence = fmt.Sprintf("All %d search/user agents allowed on %s", len(agents), pathOf(p.page))
	case len(blockedAgents) == len(agents):
		ac.Status = types.AgentFail
		ac.Evidence = "Blocked: " + strings.Join(blockedAgents, ", ")
		ac.Fix = "AI answers cannot cite or open your pages. Allow the search/user agents (OAI-SearchBot, ChatGPT-User, Claude-SearchBot, Claude-User, PerplexityBot) even if you block training bots."
	default:
		ac.Status = types.AgentWarn
		ac.Evidence = "Blocked: " + strings.Join(blockedAgents, ", ")
		ac.Fix = "Blocking search/user agents hides the site from those assistants' answers. Block training bots instead if that is the goal."
	}
	p.add(ac)

	signals := contentSignals(body)
	p.rep.ContentSignals = signals
	cs := types.AgentCheck{ID: "content-signals", Category: "access", Title: "Content signals (search, ai-input, ai-train)", URL: r.url}
	switch {
	case len(signals) == 0:
		cs.Status = types.AgentWarn
		cs.Evidence = "No Content-Signal line in robots.txt"
		cs.Fix = "Say how your content may be used, e.g. `Content-Signal: search=yes, ai-input=yes, ai-train=no` (contentsignals.org)."
	case !validContentSignals(signals):
		cs.Status = types.AgentWarn
		cs.Evidence = strings.Join(signals, " | ")
		cs.Fix = "Use only search, ai-input and ai-train with yes or no."
	default:
		cs.Status, cs.Evidence = types.AgentPass, strings.Join(signals, " | ")
	}
	p.add(cs)
	if unavailable {
		return ""
	}
	return body
}

// namesAgent reports whether a group names the bot's product token.
func (r *robotsRules) namesAgent(token string) bool {
	tok := strings.ToLower(token)
	for _, g := range r.groups {
		for _, a := range g.agents {
			if a == tok {
				return true
			}
		}
	}
	return false
}

// contentSignals returns the Content-Signal values in a robots.txt body.
func contentSignals(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if v := MatchDirective(strings.TrimSpace(line), "Content-Signal"); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func validContentSignals(values []string) bool {
	for _, v := range values {
		for _, kv := range strings.Split(v, ",") {
			k, val, ok := strings.Cut(strings.TrimSpace(kv), "=")
			if !ok {
				return false
			}
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "search", "ai-input", "ai-train":
			default:
				return false
			}
			if s := strings.ToLower(strings.TrimSpace(val)); s != "yes" && s != "no" {
				return false
			}
		}
	}
	return true
}

// --- home page: Link headers, feeds, WebMCP, JSON-LD ---

var (
	linkTagRe  = regexp.MustCompile(`(?is)<link\b[^>]*>`)
	attrRe     = regexp.MustCompile(`(?is)\b(rel|type|href)\s*=\s*("([^"]*)"|'([^']*)'|([^\s>]+))`)
	mdLinkRe   = regexp.MustCompile(`\[[^\]\n]+\]\([^)\s]+[^)]*\)`)
	webMCPRe   = regexp.MustCompile(`(?i)navigator\.modelContext|\btoolname\s*=`)
	noindexRe  = regexp.MustCompile(`(?i)<meta[^>]+name\s*=\s*["']?robots["']?[^>]+noindex`)
	agentRels  = map[string]bool{"api-catalog": true, "service-desc": true, "service-doc": true, "service-meta": true, "describedby": true, "llms-txt": true, "llms": true, "mcp": true, "agent-card": true}
	feedTypeRe = regexp.MustCompile(`(?i)application/(rss|atom|feed)\+xml|application/feed\+json`)
)

type homePage struct {
	resp agentResp
	ok   bool
}

func (p *agentProbe) checkHome(ctx context.Context) homePage {
	r := p.get(ctx, p.page, "", "", true)
	h := homePage{resp: r, ok: r.status >= 200 && r.status < 300 && len(r.body) > 0}
	if h.ok {
		// Later page requests go straight to where the page lives.
		p.adopt(r.finalURL)
		p.page = r.finalURL
	}
	html := ""
	if h.ok && looksHTML(r) {
		html = string(r.body)
	}

	// Link response headers and <link> tags.
	var found []string
	for _, lv := range r.header.Values("Link") {
		for _, part := range splitLinkHeader(lv) {
			rel, typ := linkParam(part, "rel"), linkParam(part, "type")
			for _, one := range strings.Fields(strings.ToLower(rel)) {
				if agentRels[one] || (one == "alternate" && strings.Contains(typ, "markdown")) {
					found = append(found, "Link: rel="+one)
				}
			}
		}
	}
	var feeds []string
	for _, tag := range linkTagRe.FindAllString(html, 200) {
		attrs := map[string]string{}
		for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
			attrs[strings.ToLower(m[1])] = m[3] + m[4] + m[5]
		}
		rel, typ := strings.ToLower(attrs["rel"]), strings.ToLower(attrs["type"])
		for _, one := range strings.Fields(rel) {
			if agentRels[one] || (one == "alternate" && strings.Contains(typ, "markdown")) {
				found = append(found, "<link rel="+one+">")
			}
			if one == "alternate" && feedTypeRe.MatchString(typ) {
				feeds = append(feeds, attrs["href"])
			}
		}
	}
	lc := types.AgentCheck{ID: "link-headers", Category: "discovery", Title: "Link headers / <link> tags for agents", URL: r.url}
	switch {
	case !h.ok:
		lc.Status, lc.Evidence = types.AgentNA, "Page not fetched: "+r.describe()
	case len(found) > 0:
		lc.Status, lc.Evidence = types.AgentPass, strings.Join(dedupe(found), ", ")
	default:
		lc.Status = types.AgentWarn
		lc.Evidence = "No Link header or <link> pointing agents to an API catalog, OpenAPI spec, llms.txt or markdown version"
		lc.Fix = "Advertise agent resources, e.g. `Link: </.well-known/api-catalog>; rel=\"api-catalog\"` or `<link rel=\"alternate\" type=\"text/markdown\" href=\"…\">`."
	}
	p.add(lc)

	fc := types.AgentCheck{ID: "feeds", Category: "discovery", Title: "RSS/Atom feed autodiscovery", URL: r.url}
	switch {
	case html == "":
		fc.Status, fc.Evidence = types.AgentNA, "No HTML to inspect"
	case len(feeds) > 0:
		fc.Status, fc.Evidence = types.AgentPass, strings.Join(dedupe(feeds)[:min(3, len(dedupe(feeds)))], ", ")
	default:
		fc.Status, fc.Evidence = types.AgentNA, "No feed linked (only useful for sites that publish news or posts)"
	}
	p.add(fc)

	wc := types.AgentCheck{ID: "webmcp", Category: "protocols", Title: "WebMCP tools in the page", URL: r.url}
	if html != "" && webMCPRe.MatchString(html) {
		wc.Status, wc.Evidence = types.AgentPass, "navigator.modelContext or toolname= found in the raw HTML"
	} else {
		wc.Status, wc.Evidence = types.AgentNA, "No WebMCP hints in the raw HTML (optional, experimental)"
	}
	p.add(wc)

	if html != "" {
		pd := extract.ExtractPageData(html, extract.ExtractionOptions{PageURL: r.url, FinalURL: r.finalURL, StatusCode: r.status})
		for _, sd := range pd.StructuredData {
			if sd.Format == types.FormatJSONLD && sd.Type != "" {
				p.rep.HomeSchema = append(p.rep.HomeSchema, sd.Type)
			}
		}
		p.rep.HomeSchema = dedupe(p.rep.HomeSchema)
	}
	return h
}

func splitLinkHeader(v string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, v[start:i])
				start = i + 1
			}
		}
	}
	return append(out, v[start:])
}

func linkParam(part, name string) string {
	for _, seg := range strings.Split(part, ";")[1:] {
		k, v, ok := strings.Cut(strings.TrimSpace(seg), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

// --- markdown ---

func (p *agentProbe) checkMarkdown(ctx context.Context, home homePage) {
	c := types.AgentCheck{ID: "markdown-negotiation", Category: "content", Title: "Markdown for Accept: text/markdown", URL: p.page}
	if !home.ok {
		c.Status, c.Evidence = types.AgentNA, "Page not fetched: "+home.resp.describe()
		p.add(c)
		return
	}
	r := p.get(ctx, p.page, "text/markdown", "", true)
	ct := strings.ToLower(r.ctype)
	switch {
	case r.status == 0:
		c.Status, c.Evidence = types.AgentNA, r.err
	case r.status >= 200 && r.status < 300 && strings.Contains(ct, "text/markdown") && !looksHTML(agentResp{body: r.body}):
		c.Status, c.Evidence = types.AgentPass, r.describe()
		if !strings.Contains(strings.ToLower(strings.Join(r.header.Values("Vary"), ",")), "accept") {
			c.Evidence += " (no Vary: Accept; caches may mix HTML and markdown)"
		}
	default:
		c.Status = types.AgentWarn
		c.Evidence = "Answered " + r.describe()
		c.Fix = "Return a markdown version when agents send `Accept: text/markdown` (Cloudflare Markdown for Agents or your framework), with `Vary: Accept`."
	}
	p.add(c)
}

func (p *agentProbe) checkMarkdownVariant(ctx context.Context) {
	u, _ := url.Parse(p.page)
	path := strings.TrimSuffix(u.Path, "/")
	if path == "" {
		path = "/index"
	}
	target := p.origin + path + ".md"
	c := types.AgentCheck{ID: "markdown-variant", Category: "content", Title: ".md version of the page", URL: target}
	r := p.get(ctx, target, "text/markdown,text/plain;q=0.9,*/*;q=0.5", "", true)
	switch r.state(p.origin, false) {
	case "ok":
		c.Status, c.Evidence = types.AgentPass, r.describe()
	case "soft404":
		c.Status = types.AgentWarn
		c.Evidence = r.describe() + ": an HTML page with 200 (soft 404)"
		c.Fix = "Answer 404 for files that do not exist, or serve real markdown at this URL."
	case "skipped":
		c.Status, c.Evidence = types.AgentNA, r.err
	default:
		c.Status, c.Evidence = types.AgentNA, r.describe()+" (optional: a markdown twin helps agents read pages)"
	}
	p.add(c)
}

// --- llms.txt ---

func (p *agentProbe) checkLLMs(ctx context.Context) {
	for _, f := range []struct{ id, path, title string }{
		{"llms-txt", "/llms.txt", "llms.txt"},
		{"llms-full-txt", "/llms-full.txt", "llms-full.txt"},
	} {
		r := p.get(ctx, f.path, "text/markdown,text/plain;q=0.9,*/*;q=0.5", "", true)
		c := types.AgentCheck{ID: f.id, Category: "discovery", Title: f.title, URL: r.url}
		switch r.state(p.origin, false) {
		case "ok":
			text := string(r.body)
			if f.id == "llms-txt" {
				h1, links := llmsFormat(text)
				switch {
				case h1 != "" && links > 0:
					c.Status = types.AgentPass
					c.Evidence = fmt.Sprintf("%s · H1 %q · %d links", r.describe(), h1, links)
				default:
					c.Status = types.AgentWarn
					c.Evidence = fmt.Sprintf("%s · H1 %q · %d links", r.describe(), h1, links)
					c.Fix = "Follow the llms.txt format: a `# Site name` H1, a `>` summary, then `## Sections` with `- [Title](url): note` links."
				}
			} else {
				c.Status = types.AgentPass
				c.Evidence = fmt.Sprintf("%s · %d KB read", r.describe(), len(r.body)/1024)
			}
		case "soft404":
			c.Status = types.AgentFail
			c.Evidence = r.describe() + ": an HTML page with 200 instead of a text file (soft 404)"
			if noindexRe.Match(r.body) || strings.Contains(strings.ToLower(r.header.Get("X-Robots-Tag")), "noindex") {
				c.Evidence += ", marked noindex"
			}
			c.Fix = "Serve " + f.path + " as text/markdown or text/plain, or answer 404 until you publish one."
		case "missing":
			if f.id == "llms-txt" {
				c.Status = types.AgentWarn
				c.Fix = "Publish /llms.txt: a markdown index of your key pages for AI agents (llmstxt.org)."
			} else {
				c.Status = types.AgentNA
			}
			c.Evidence = r.describe()
		case "skipped":
			c.Status, c.Evidence = types.AgentNA, r.err
		default:
			c.Status, c.Evidence = types.AgentWarn, r.describe()
			c.Fix = "The file did not load; check the server."
		}
		p.add(c)
	}
}

// llmsFormat returns the H1 (first non-empty line starting with "# ") and
// the number of markdown links.
func llmsFormat(text string) (h1 string, links int) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			h1 = strings.TrimSpace(line[2:])
		}
		break
	}
	return h1, len(mdLinkRe.FindAllStringIndex(text, -1))
}

// --- /.well-known ---

type wellKnown struct {
	id, category, title string
	paths               []string // tried in order until one is not missing
	keys                []string // any of these keys makes the JSON valid for its kind
	text                bool     // not JSON (security.txt)
	absent              string   // status when missing
	absentFix           string
}

var wellKnownFiles = []wellKnown{
	{id: "ai-plugin", category: "protocols", title: "ai-plugin.json (deprecated)", paths: []string{"/.well-known/ai-plugin.json"}, keys: []string{"api", "name_for_model"}, absent: types.AgentNA},
	{id: "mcp-server-card", category: "protocols", title: "MCP server card", paths: []string{"/.well-known/mcp/server-card.json", "/.well-known/mcp.json"}, keys: []string{"name", "serverInfo", "remotes", "endpoint", "url", "transport"}, absent: types.AgentNA},
	{id: "a2a-agent-card", category: "protocols", title: "A2A agent card", paths: []string{"/.well-known/agent-card.json", "/.well-known/agent.json"}, keys: []string{"skills", "url", "capabilities"}, absent: types.AgentNA},
	{id: "api-catalog", category: "protocols", title: "API catalog (RFC 9727)", paths: []string{"/.well-known/api-catalog"}, keys: []string{"linkset"}, absent: types.AgentNA},
	{id: "oauth-authorization-server", category: "protocols", title: "OAuth authorization server metadata", paths: []string{"/.well-known/oauth-authorization-server"}, keys: []string{"issuer"}, absent: types.AgentNA},
	{id: "oauth-protected-resource", category: "protocols", title: "OAuth protected resource (RFC 9728)", paths: []string{"/.well-known/oauth-protected-resource"}, keys: []string{"resource"}, absent: types.AgentNA},
	{id: "agent-skills", category: "protocols", title: "Agent skills index", paths: []string{"/.well-known/agent-skills/index.json"}, keys: []string{"skills"}, absent: types.AgentNA},
	{id: "web-bot-auth", category: "access", title: "Web Bot Auth key directory", paths: []string{"/.well-known/http-message-signatures-directory"}, keys: []string{"keys"}, absent: types.AgentNA},
	{id: "security-txt", category: "discovery", title: "security.txt", paths: []string{"/.well-known/security.txt"}, text: true, absent: types.AgentWarn,
		absentFix: "Publish /.well-known/security.txt with Contact and Expires (RFC 9116) so people and agents can report problems."},
}

// checkWellKnown checks the /.well-known agent files and returns OpenAPI
// URLs they reference.
func (p *agentProbe) checkWellKnown(ctx context.Context) (openapi []string) {
	for _, wk := range wellKnownFiles {
		var r agentResp
		st := ""
		for _, path := range wk.paths {
			r = p.get(ctx, path, "application/json,*/*;q=0.5", "", true)
			if st = r.state(p.origin, false); st != "missing" {
				break
			}
		}
		c := types.AgentCheck{ID: wk.id, Category: wk.category, Title: wk.title, URL: r.url}
		switch st {
		case "missing":
			c.Status, c.Evidence, c.Fix = wk.absent, r.describe(), wk.absentFix
			if wk.absent == types.AgentNA {
				c.Evidence += " (optional)"
			}
		case "skipped":
			c.Status, c.Evidence = types.AgentNA, r.err
		case "error":
			c.Status, c.Evidence = types.AgentWarn, r.describe()
			c.Fix = "The file did not load; check the server."
		case "soft404":
			c.Status = types.AgentFail
			c.Evidence = r.describe() + ": an HTML page with 200 (soft 404)"
			c.Fix = "Answer 404 for /.well-known files you do not publish; agents read this HTML as a broken file."
		case "ok":
			if wk.text {
				p.securityTxt(&c, r)
				break
			}
			var doc map[string]any
			if err := json.Unmarshal(r.body, &doc); err != nil {
				c.Status = types.AgentFail
				c.Evidence = r.describe() + ": not valid JSON"
				c.Fix = "Fix the JSON syntax; agents cannot read this file."
				break
			}
			found := ""
			for _, k := range wk.keys {
				if _, ok := doc[k]; ok {
					found = k
					break
				}
			}
			if found == "" {
				c.Status = types.AgentWarn
				c.Evidence = r.describe() + ": valid JSON without " + strings.Join(wk.keys, "/")
				c.Fix = "Check the file against its specification."
				break
			}
			c.Status = types.AgentPass
			c.Evidence = r.describe() + jsonSummary(doc)
			switch wk.id {
			case "ai-plugin":
				c.Status = types.AgentWarn
				c.Fix = "ChatGPT plugins were retired in 2024. Keep the OpenAPI spec and publish an MCP server card or API catalog instead."
				if api, ok := doc["api"].(map[string]any); ok {
					if s, ok := api["url"].(string); ok && s != "" {
						openapi = append(openapi, resolveURL(r.finalURL, s))
					}
				}
			case "api-catalog":
				openapi = append(openapi, linksetServiceDescs(doc, r.finalURL)...)
				if !strings.Contains(strings.ToLower(r.ctype), "linkset+json") {
					c.Evidence += " (content type should be application/linkset+json)"
				}
			}
		}
		p.add(c)
	}
	return openapi
}

func (p *agentProbe) securityTxt(c *types.AgentCheck, r agentResp) {
	text := strings.ToLower(string(r.body))
	hasContact := strings.Contains(text, "contact:")
	hasExpires := strings.Contains(text, "expires:")
	switch {
	case hasContact && hasExpires:
		c.Status, c.Evidence = types.AgentPass, r.describe()+" · Contact and Expires"
	case hasContact:
		c.Status, c.Evidence = types.AgentWarn, r.describe()+" · no Expires field"
		c.Fix = "Add an Expires field (required by RFC 9116)."
	default:
		c.Status, c.Evidence = types.AgentWarn, r.describe()+" · no Contact field"
		c.Fix = "Add a Contact field (required by RFC 9116)."
	}
}

func jsonSummary(doc map[string]any) string {
	for _, k := range []string{"name", "name_for_human", "issuer", "resource"} {
		if s, ok := doc[k].(string); ok && s != "" {
			return fmt.Sprintf(" · %s %q", k, truncate(s, 60))
		}
	}
	if si, ok := doc["serverInfo"].(map[string]any); ok {
		if s, ok := si["name"].(string); ok {
			return fmt.Sprintf(" · server %q", truncate(s, 60))
		}
	}
	if ks, ok := doc["keys"].([]any); ok {
		return fmt.Sprintf(" · %d keys", len(ks))
	}
	if ls, ok := doc["linkset"].([]any); ok {
		return fmt.Sprintf(" · %d APIs", len(ls))
	}
	return ""
}

func linksetServiceDescs(doc map[string]any, base string) []string {
	var out []string
	ls, _ := doc["linkset"].([]any)
	for _, e := range ls {
		m, _ := e.(map[string]any)
		descs, _ := m["service-desc"].([]any)
		for _, d := range descs {
			if dm, ok := d.(map[string]any); ok {
				if h, ok := dm["href"].(string); ok && h != "" {
					out = append(out, resolveURL(base, h))
				}
			}
		}
	}
	return out
}

// --- OpenAPI ---

func (p *agentProbe) checkOpenAPI(ctx context.Context, refs []string) {
	c := types.AgentCheck{ID: "openapi", Category: "protocols", Title: "OpenAPI description"}
	referenced := len(refs) > 0
	candidates := dedupe(refs)
	if !referenced {
		candidates = []string{p.origin + "/openapi.json", p.origin + "/openapi.yaml"}
	}
	var last agentResp
	for _, cand := range candidates[:min(2, len(candidates))] {
		r := p.get(ctx, cand, "application/json,application/yaml,text/yaml,*/*;q=0.5", "", true)
		last = r
		st := r.state(p.origin, false)
		if st == "ok" && looksOpenAPI(r.body) {
			c.Status, c.URL = types.AgentPass, r.url
			c.Evidence = r.describe()
			if referenced {
				c.Evidence += " (referenced by ai-plugin.json or the API catalog)"
			}
			p.add(c)
			return
		}
		if st == "skipped" {
			break
		}
	}
	c.URL = last.url
	switch {
	case last.status == 0 && last.err != "":
		c.Status, c.Evidence = types.AgentNA, last.err
	case referenced:
		c.Status = types.AgentFail
		c.Evidence = "Referenced spec answered " + last.describe()
		if last.state(p.origin, false) == "ok" {
			c.Evidence += " and is not an OpenAPI document"
		}
		c.Fix = "Fix the spec URL referenced by ai-plugin.json or the API catalog."
	default:
		c.Status, c.Evidence = types.AgentNA, "No spec at /openapi.json or /openapi.yaml (optional, for sites with an API)"
	}
	p.add(c)
}

func looksOpenAPI(body []byte) bool {
	var doc map[string]any
	if json.Unmarshal(body, &doc) == nil {
		_, a := doc["openapi"]
		_, b := doc["swagger"]
		return a || b
	}
	for _, line := range strings.Split(string(body[:min(len(body), 4096)]), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "openapi:") || strings.HasPrefix(l, "swagger:") {
			return true
		}
	}
	return false
}

// --- live fetch as AI bots ---

var challengeRe = regexp.MustCompile(`(?i)cf-chl-|just a moment\.\.\.|captcha|attention required|access denied`)

func (p *agentProbe) checkBotsLive(ctx context.Context, home homePage, robotsBody string) {
	c := types.AgentCheck{ID: "ai-bot-firewall", Category: "access", Title: "AI bots get the page (no firewall block)", URL: p.page}
	if !home.ok {
		c.Status, c.Evidence = types.AgentNA, "Page not fetched: "+home.resp.describe()
		p.add(c)
		return
	}
	rules := parseRobots(robotsBody)
	var blocked, okBots []string
	for i, b := range AIBots {
		if b.UA == "" {
			continue
		}
		if robotsBody != "" && !rules.selectGroups(b.Token).allowed(p.page) {
			continue // the site asks this bot to stay away: do not send it
		}
		r := p.get(ctx, p.page, "", b.UA, false)
		if r.status == 0 {
			continue
		}
		p.rep.Bots[i].LiveStatus = r.status
		challenged := r.status >= 200 && r.status < 300 && challengeRe.Match(r.body[:min(len(r.body), 8192)]) &&
			!challengeRe.Match(home.resp.body[:min(len(home.resp.body), 8192)])
		if r.status == 401 || r.status == 403 || r.status == 429 || r.status == 503 || challenged {
			blocked = append(blocked, fmt.Sprintf("%s (%d%s)", b.Token, r.status, map[bool]string{true: ", challenge page"}[challenged]))
		} else {
			okBots = append(okBots, fmt.Sprintf("%s (%d)", b.Token, r.status))
		}
	}
	switch {
	case len(blocked) > 0:
		c.Status = types.AgentWarn
		c.Evidence = "Blocked by the server although robots.txt allows them: " + strings.Join(blocked, ", ")
		c.Fix = "A firewall or bot manager blocks these AI bots. If you want them, allow their verified IPs or signed requests (Web Bot Auth); else disallow them in robots.txt so the policy is clear."
	case len(okBots) > 0:
		c.Status, c.Evidence = types.AgentPass, "Same page served to "+strings.Join(okBots, ", ")
	default:
		c.Status, c.Evidence = types.AgentNA, "Not tested (bots disallowed in robots.txt, or request limit reached)"
	}
	p.add(c)
}

// --- crawl integration ---

// setupAgentCheck probes each crawled host (seed host and list-mode hosts,
// at most 5) and stores the reports in the crawl DB meta.
func (s *crawlSession) setupAgentCheck(ctx context.Context) {
	if s.config.NoAgentCheck || s.config.HeadOnly {
		return
	}
	targets := agentCheckTargets(s.seedURL, s.config.URLs)
	if len(targets) == 0 {
		return
	}
	start := time.Now()
	opts := AgentCheckOptions{Fetch: s.fetchOpts}
	opts.Fetch.Timeout = agentReqTimeout
	if s.robots != nil {
		opts.Allowed = s.robots.IsAllowed
	}
	var reports []types.AgentHostReport
	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		reports = append(reports, CheckAgentReadiness(ctx, t, opts))
	}
	s.agentReadiness = reports
	if saver, ok := s.crawlDB.(interface{ SaveMeta(key, value string) error }); ok {
		if data, err := json.Marshal(reports); err == nil {
			saver.SaveMeta(AgentReadinessMetaKey, string(data))
		}
	}
	reqs := 0
	for _, r := range reports {
		reqs += r.Requests
	}
	fmt.Fprintf(os.Stderr, "  [agent-check] %d host(s), %d requests, %s\n", len(reports), reqs, time.Since(start).Round(time.Millisecond))
}

// agentCheckTargets returns the seed URL plus one URL per other host in
// list mode, at most agentMaxHosts.
func agentCheckTargets(seed string, urls []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range append([]string{seed}, urls...) {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || seen[u.Host] {
			continue
		}
		seen[u.Host] = true
		out = append(out, raw)
		if len(out) == agentMaxHosts {
			break
		}
	}
	return out
}

// --- helpers ---

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0:0]
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func pathOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		return u.Path
	}
	return "/"
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
