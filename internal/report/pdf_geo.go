package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// GEO (generative engine optimisation): can AI search and AI agents reach,
// read and quote the site? Tickets come from the agent-readiness checks
// (per host) and from page-level answer signals. Like every Micelio signal
// these are crawl-time proxies: they show access and readability, not
// whether an AI system cites the site.

type geoSpec struct {
	priority int // for a failed check; a warning is one level lower
	title    string
	owner    string
	effort   string
	problem  string
	fix      []string
	accept   string
}

var geoChecks = map[string]geoSpec{
	"ai-agents-access": {0, "Let AI search and assistant bots read the site", "SEO + Dev", "S",
		"robots.txt blocks bots that AI search engines and assistants use to read pages for answers (search and user bots, not only training bots). Blocked pages cannot be quoted or linked in AI answers.",
		[]string{"Allow the search and user bots (OAI-SearchBot, ChatGPT-User, Claude-SearchBot, PerplexityBot…) in robots.txt.", "Block only the training bots you do not want, by name."},
		"Every AI search and user bot may fetch the home page per robots.txt."},
	"ai-bot-firewall": {0, "Verify that the CDN lets verified AI bots through", "SEO + Dev", "S",
		"Our test asked for the start page with AI bot user agents and was blocked, although robots.txt allows these bots. Many CDNs block requests that claim to be a bot but do not come from that bot's published IP ranges, which is right. Some also block the real bots (for example Cloudflare's 'Block AI bots' setting). From outside the two look the same, so this needs a check.",
		[]string{"In the CDN, look at bot settings and firewall events for GPTBot, ClaudeBot and OAI-SearchBot (Cloudflare: Security → Bots, and AI Crawl Control).", "Or check server/CDN logs: hits from verified AI bots should get 200 (Micelio's log analysis shows status codes per AI bot).", "If the real bots are blocked on purpose, disallow them in robots.txt so the policy is clear."},
		"Verified AI search bots get 200 in CDN logs or firewall events, or robots.txt matches the block."},
	"js-content": {0, "Put key content in the HTML, not only in JavaScript", "Dev", "L",
		"Main content appears only after JavaScript runs. Most AI crawlers do not run JavaScript, so they see an empty or partial page.",
		[]string{"Render the main text, headings and links on the server (SSR or pre-rendering).", "Keep JavaScript for interaction, not for the content itself."},
		"The raw HTML of key templates contains their main text and links."},
	"structured-data": {1, "Describe the main entities with JSON-LD", "Dev", "M",
		"Structured data tells machines what a page is about: the organisation, products, articles, breadcrumbs. AI systems use it to understand and attribute content.",
		[]string{"Add JSON-LD in the raw HTML for Organization, BreadcrumbList and the main type of each template (Product, Article, LocalBusiness…).", "Validate with the Rich Results Test or Schema.org validator."},
		"Each main template outputs valid JSON-LD in the raw HTML."},
	"robots-txt": {1, "Publish a robots.txt with rules for AI bots", "SEO + Dev", "S",
		"Without a robots.txt, every bot guesses. You cannot say which AI bots may read or train on the site.",
		[]string{"Publish /robots.txt with a Sitemap line and User-agent groups for the AI bots you allow or block."},
		"/robots.txt answers 200 with AI bot groups and a Sitemap line."},
	"llms-txt": {1, "Publish an llms.txt index of key pages", "Content + Dev", "S",
		"llms.txt is a short markdown map of a site's most useful pages for AI agents (llmstxt.org). It is cheap and helps agents find the right pages first.",
		[]string{"Publish /llms.txt: site name, one-line summary, then sections with links to key pages and one line each.", "Keep it in sync with the main navigation."},
		"/llms.txt answers 200 with markdown links to the key pages."},
	"ai-bot-rules": {1, "Set explicit robots.txt rules for AI bots", "SEO", "S",
		"AI bots only follow the generic * rules. Explicit groups make the decision visible and stop surprises when generic rules change.",
		[]string{"Add User-agent groups for GPTBot, OAI-SearchBot, ClaudeBot, Claude-SearchBot, PerplexityBot and Google-Extended with the access you want."},
		"robots.txt names the main AI bots explicitly."},
	"robots-sitemap": {2, "List the sitemaps in robots.txt", "SEO", "S",
		"Crawlers, AI bots included, look for sitemaps in robots.txt first.",
		[]string{"Add a Sitemap: line for each XML sitemap to robots.txt."},
		"robots.txt lists every XML sitemap."},
	"content-signals": {2, "Declare how AI may use the content (Content-Signal)", "SEO + Legal", "S",
		"Content-Signal in robots.txt says whether content may be used for search, AI answers and AI training. Without it, the choice is left to each bot.",
		[]string{"Add e.g. `Content-Signal: search=yes, ai-input=yes, ai-train=no` to robots.txt, after a decision with legal/marketing."},
		"robots.txt carries a Content-Signal line agreed with legal."},
	"markdown-negotiation": {2, "Serve a markdown version of pages to AI agents", "Dev", "M",
		"Agents that ask for text/markdown get full HTML, which wastes their context on menus and scripts.",
		[]string{"Return markdown when the request has `Accept: text/markdown` (e.g. Cloudflare Markdown for Agents or a middleware)."},
		"A request with Accept: text/markdown gets a markdown body."},
	"link-headers": {2, "Advertise agent resources with Link headers", "Dev", "S",
		"Link headers or <link> tags tell agents where llms.txt, APIs or feeds live, so they do not have to guess.",
		[]string{"Add e.g. `Link: </llms.txt>; rel=\"describedby\"` or the matching <link> tags."},
		"The home page advertises its agent resources."},
	"llms-full-txt": {3, "Publish llms-full.txt with the full text of key pages", "Content + Dev", "M",
		"llms-full.txt gives agents the content itself in one file.",
		[]string{"Generate /llms-full.txt from the pages listed in llms.txt."}, "/llms-full.txt answers 200 with markdown."},
}

// geoBacklog builds the GEO tickets, most urgent first.
func geoBacklog(s *types.CrawlStats, total int, cdn string) []Ticket {
	var out []Ticket
	if a := s.AgentReadiness; a != nil && len(a.Hosts) > 0 {
		h := a.Hosts[0]
		for _, c := range h.Checks {
			spec, ok := geoChecks[c.ID]
			if !ok || (c.Status != types.AgentFail && c.Status != types.AgentWarn) {
				continue
			}
			p := spec.priority
			if c.Status == types.AgentWarn {
				p = min(p+1, 3)
			}
			ev := c.Evidence
			if c.ID == "ai-agents-access" {
				if b := blockedBots(h.Bots); b != "" {
					ev = "Blocked: " + b
				}
			}
			if c.ID == "ai-bot-firewall" && cdn != "" {
				spec.title = "Verify that " + cdn + " lets verified AI bots through"
				ev = cdn + " block page for our test with AI bot user agents (not from the bots' own IPs). " + ev
			}
			if ev == "" {
				ev = c.Title + ": " + map[string]string{types.AgentFail: "fails", types.AgentWarn: "needs work"}[c.Status]
			}
			out = append(out, geoTicket(p, spec, 0, "", 0, ev))
		}
	}
	if total > 0 {
		if ai := s.AIReadinessStats; ai != nil {
			if n := total - ai.PagesWithStructuredAnswer; n*100 >= total*80 {
				out = append(out, geoTicket(2, geoSpec{0, "Add answer-ready blocks (lists, tables, steps) to %s pages", "Content", "L",
					"AI answers quote short, self-contained passages. Pages without lists, tables or step-by-step blocks are harder to quote. This is a crawl-time proxy, not a measure of AI citations.",
					[]string{"On key templates, add a short block that answers the page's main question: a list, a comparison table or numbered steps.", "Put it high on the page, in the HTML, under a clear heading."},
					"At least 40% of key pages carry a structured answer block in the next crawl."}, n, "pages", total, ""))
			}
			if n := total - ai.PagesWithConciseDefinition; n*100 >= total*50 {
				out = append(out, geoTicket(2, geoSpec{0, "Open %s pages with a one-paragraph answer", "Content", "M",
					"These pages do not start with a short paragraph that says what the page is about. AI summaries and featured answers usually draw on that first paragraph.",
					[]string{"Start each key page with 1-2 sentences that answer its main question directly."},
					"Most key pages open with a concise answer paragraph in the next crawl."}, n, "pages", total, ""))
			}
		}
		if s.PagesWithStructuredData*2 < total && !hasCheck(out, "Describe the main entities with JSON-LD") {
			n := total - s.PagesWithStructuredData
			out = append(out, geoTicket(1, geoSpec{0, "Add JSON-LD to %s pages", "Dev", "M", geoChecks["structured-data"].problem, geoChecks["structured-data"].fix,
				"More than half of indexable pages output valid JSON-LD in the next crawl."}, n, "pages", total, ""))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	for i := range out {
		out[i].Key = fmt.Sprintf("GEO-%d", i+1)
	}
	return out
}

func geoTicket(p int, spec geoSpec, count int, unit string, base int, evidence string) Ticket {
	title := spec.title
	if strings.Contains(title, "%s") {
		title = countTitle(title, count)
	}
	t := Ticket{
		Track: "GEO", Priority: fmt.Sprintf("P%d", p), Tone: []string{"bad", "warn", "info", ""}[p],
		Type: "Task", Effort: spec.effort, Points: map[string]int{"S": 2, "M": 3, "L": 5}[spec.effort],
		Owner: spec.owner, Title: title, Problem: spec.problem, Fix: spec.fix, Accept: spec.accept,
		Count: count, Unit: unit, Evidence: evidence,
	}
	if p == 0 {
		t.Type = "Bug"
	}
	if unit != "" {
		t.Share = share(count, base)
		t.Pct = min(100, 100*float64(count)/float64(max(1, base)))
	}
	return t
}

func hasCheck(ts []Ticket, title string) bool {
	for _, t := range ts {
		if t.Title == title {
			return true
		}
	}
	return false
}

// blockedBots names the AI search and user bots robots.txt blocks.
func blockedBots(bots []types.AgentBotAccess) string {
	var names []string
	for _, b := range bots {
		if b.Allowed != nil && !*b.Allowed && b.Purpose != "training" {
			names = append(names, b.Bot)
		}
	}
	return strings.Join(names, ", ")
}

// GEOView feeds the "Ready for AI search?" sheet.
type GEOView struct {
	Score      int
	Level      string
	Tone       string
	Dash       float64
	Categories []Bar
	Bots       []GEOBot
	Signals    []Metric
	Host       string
}

// GEOBot is one AI bot on the access grid.
type GEOBot struct {
	Bot, Owner, Purpose string
	State               string // allowed, blocked, refused (robots.txt allows it, the server does not), unknown
	Note                string
}

func geoView(s *types.CrawlStats, total int) *GEOView {
	a := s.AgentReadiness
	if a == nil || len(a.Hosts) == 0 {
		return nil
	}
	h := a.Hosts[0]
	v := &GEOView{Score: a.Score, Level: a.Level, Host: h.Host}
	switch {
	case a.Score >= 70:
		v.Tone = "good"
	case a.Score >= 40:
		v.Tone = "warn"
	default:
		v.Tone = "bad"
	}
	v.Dash = 251.327 - float64(a.Score)/100*251.327
	for _, c := range h.Categories {
		if c.Score < 0 {
			continue
		}
		tone := "bad"
		if c.Score >= 70 {
			tone = "good"
		} else if c.Score >= 40 {
			tone = "warn"
		}
		label := strings.ToUpper(c.Category[:1]) + c.Category[1:]
		v.Categories = append(v.Categories, Bar{Label: label, Count: c.Score, Pct: float64(c.Score), Share: fmt.Sprintf("%d pass · %d warn · %d fail", c.Pass, c.Warn, c.Fail), Tone: tone})
	}
	for _, b := range h.Bots {
		st := "unknown"
		if b.Allowed != nil {
			st = map[bool]string{true: "allowed", false: "blocked"}[*b.Allowed]
		}
		bot := GEOBot{Bot: b.Bot, Owner: b.Owner, Purpose: b.Purpose, State: st}
		if st == "allowed" && b.LiveStatus >= 400 {
			bot.State, bot.Note = "refused", fmt.Sprintf("%d to our test", b.LiveStatus)
		}
		v.Bots = append(v.Bots, bot)
	}
	if total > 0 {
		v.Signals = append(v.Signals, Metric{Label: "Pages with JSON-LD", Value: share(s.PagesWithStructuredData, total), Note: commaFormat(s.PagesWithStructuredData) + " pages", Tone: toneRatio(s.PagesWithStructuredData, total, .8, .5)})
		if ai := s.AIReadinessStats; ai != nil {
			v.Signals = append(v.Signals,
				Metric{Label: "Open with a concise answer", Value: share(ai.PagesWithConciseDefinition, total), Note: commaFormat(ai.PagesWithConciseDefinition) + " pages", Tone: toneRatio(ai.PagesWithConciseDefinition, total, .5, .25)},
				Metric{Label: "Structured answer blocks", Value: share(ai.PagesWithStructuredAnswer, total), Note: "lists, tables, steps", Tone: toneRatio(ai.PagesWithStructuredAnswer, total, .4, .2)},
				Metric{Label: "FAQ / HowTo schema", Value: commaFormat(ai.PagesWithFAQSchema + ai.PagesWithHowToSchema), Note: "pages"})
		}
	}
	return v
}
