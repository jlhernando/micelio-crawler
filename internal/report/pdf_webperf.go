package report

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// Core Web Vitals and CDN sheets, and the tickets they raise. Both come from
// checks made at export time (PDFOptions.CWV / CDN), so older crawls get
// them too.

// cwvThresholds are Google's good / poor limits (ms, CLS unitless).
var cwvThresholds = map[string][2]float64{
	"lcp": {2500, 4000}, "inp": {200, 500}, "cls": {0.1, 0.25}, "fcp": {1800, 3000}, "ttfb": {800, 1800},
}

var cwvNames = map[string]string{
	"lcp": "Largest Contentful Paint", "inp": "Interaction to Next Paint", "cls": "Cumulative Layout Shift",
	"fcp": "First Contentful Paint", "ttfb": "Time to First Byte",
}

func cwvTone(id string, v float64) string {
	t, ok := cwvThresholds[id]
	switch {
	case !ok:
		return ""
	case v <= t[0]:
		return "good"
	case v <= t[1]:
		return "warn"
	}
	return "bad"
}

func cwvValue(id string, v float64) string {
	if id == "cls" {
		return fmt.Sprintf("%.2f", v)
	}
	return ms(v)
}

func cwvLimit(id string) string {
	t := cwvThresholds[id]
	return "good ≤ " + cwvValue(id, t[0])
}

// CWVView feeds the Core Web Vitals sheet.
type CWVView struct {
	Measured bool
	Source   string // label of where the numbers come from
	Missing  string // why there are no numbers
	Columns  []CWVColumn
	Lab      *CWVLabView
	Pages    []Metric // page-level CrUX from the crawl, if any
	Proxy    []Metric // crawl response times, shown when nothing was measured
	Verdict  string
	Tone     string
}

// CWVColumn is one form factor.
type CWVColumn struct {
	Label string
	Rows  []CWVRow
}

// CWVRow is one metric.
type CWVRow struct {
	ID, Name, Value, Limit, Tone string
	Core                         bool
	Good, NI, Poor               float64 // percent
}

// CWVLabView is the Lighthouse run.
type CWVLabView struct {
	URL   string
	Score int
	Tone  string
	Dash  float64
	Items []Metric
}

func cwvView(c *types.CWVReport, s *types.CrawlStats) *CWVView {
	v := &CWVView{}
	if c != nil {
		for _, f := range c.Field {
			col := CWVColumn{Label: map[string]string{"phone": "Phones", "desktop": "Desktop", "all": "All devices"}[f.FormFactor]}
			if f.Scope == "url" {
				col.Label += " · start page"
			}
			for _, id := range []string{"lcp", "inp", "cls", "fcp", "ttfb"} {
				for _, m := range f.Metrics {
					if m.ID != id {
						continue
					}
					col.Rows = append(col.Rows, CWVRow{ID: id, Name: cwvNames[id], Value: cwvValue(id, m.P75), Limit: cwvLimit(id),
						Tone: cwvTone(id, m.P75), Core: id == "lcp" || id == "inp" || id == "cls",
						Good: m.Good * 100, NI: m.NI * 100, Poor: m.Poor * 100})
				}
			}
			if len(col.Rows) > 0 {
				v.Columns = append(v.Columns, col)
			}
		}
		if l := c.Lab; l != nil {
			lv := &CWVLabView{URL: shortURL(l.URL), Score: int(math.Round(l.Score))}
			lv.Tone = map[bool]string{true: "good", false: "warn"}[lv.Score >= 90]
			if lv.Score < 50 {
				lv.Tone = "bad"
			}
			lv.Dash = 251.327 - float64(lv.Score)/100*251.327
			lv.Items = []Metric{
				{Label: "LCP", Value: ms(l.LCP), Tone: cwvTone("lcp", l.LCP)},
				{Label: "Total Blocking Time", Value: ms(l.TBT), Tone: map[bool]string{true: "good", false: "bad"}[l.TBT <= 200]},
				{Label: "CLS", Value: fmt.Sprintf("%.2f", l.CLS), Tone: cwvTone("cls", l.CLS)},
				{Label: "Speed Index", Value: ms(l.SI)},
			}
			v.Lab = lv
		}
		v.Source = map[string]string{"crux": "Chrome UX Report · real users · p75, last 28 days", "psi": "PageSpeed Insights · real users (CrUX) and a lab run"}[c.Source]
		switch c.Missing {
		case "no-key":
			v.Missing = "No Google API key is set, so Micelio could not ask Chrome UX Report or PageSpeed Insights."
		case "no-data":
			v.Missing = "Chrome UX Report has no data for this site: it needs enough Chrome visitors over 28 days."
		case "":
		default:
			v.Missing = "Google's API did not answer: " + c.Missing
		}
	} else {
		v.Missing = "Core Web Vitals were not fetched for this report."
	}
	if cs := s.CruxStats; cs != nil && cs.PagesWithData > 0 {
		n := cs.PagesWithData
		v.Pages = []Metric{
			{Label: "Pages with CrUX data", Value: commaFormat(n)},
			{Label: "Good LCP", Value: share(cs.GoodLCP, n), Tone: toneRatio(cs.GoodLCP, n, .75, .5)},
			{Label: "Good INP", Value: share(cs.GoodINP, n), Tone: toneRatio(cs.GoodINP, n, .75, .5)},
			{Label: "Good CLS", Value: share(cs.GoodCLS, n), Tone: toneRatio(cs.GoodCLS, n, .75, .5)},
		}
	}
	v.Measured = len(v.Columns) > 0 || v.Lab != nil || len(v.Pages) > 0
	p := s.ResponseTimePercentiles
	if p.P50 > 0 {
		v.Proxy = []Metric{
			{Label: "Crawl response p50", Value: ms(p.P50), Tone: toneMs(p.P50)},
			{Label: "Crawl response p90", Value: ms(p.P90), Tone: toneMs(p.P90)},
		}
	}
	// Verdict: the phone column decides (Google assesses mobile first).
	if len(v.Columns) > 0 {
		bad, warn := 0, 0
		for _, r := range v.Columns[0].Rows {
			if !r.Core {
				continue
			}
			switch r.Tone {
			case "bad":
				bad++
			case "warn":
				warn++
			}
		}
		switch {
		case bad > 0:
			v.Verdict, v.Tone = "Fails Core Web Vitals", "bad"
		case warn > 0:
			v.Verdict, v.Tone = "Needs improvement", "warn"
		default:
			v.Verdict, v.Tone = "Passes Core Web Vitals", "good"
		}
	}
	return v
}

// cwvTickets raises one SEO ticket per Core Web Vital that is not good on
// phones (or the first form factor measured).
func cwvTickets(c *types.CWVReport, s *types.CrawlStats) []Ticket {
	var out []Ticket
	fixes := map[string][]string{
		"lcp":  {"Find the LCP element of key templates (usually the hero image or heading) in PageSpeed Insights.", "Serve that image small and early: modern format, right size, fetchpriority=\"high\", no lazy-loading above the fold.", "Cut server time for HTML (edge caching) and render-blocking CSS/JS."},
		"inp":  {"Find slow interactions with the Chrome DevTools Performance panel or RUM data.", "Break up long JavaScript tasks, defer third-party scripts and keep event handlers light."},
		"cls":  {"Reserve space for images, ads and embeds (width/height or aspect-ratio).", "Do not insert banners or consent bars above content after load; use font-display: optional/swap with matched fallbacks."},
		"ttfb": {"Cache HTML at the CDN edge, or speed up the origin (database, rendering).", "Avoid redirects before the page."},
	}
	if c != nil && len(c.Field) > 0 {
		f := c.Field[0]
		for _, m := range f.Metrics {
			if m.ID == "fcp" {
				continue
			}
			tone := cwvTone(m.ID, m.P75)
			if tone == "good" || tone == "" {
				continue
			}
			p := 1
			if tone == "bad" && m.ID != "ttfb" {
				p = 0
			}
			if m.ID == "ttfb" {
				p = 2
				if tone == "bad" {
					p = 1
				}
			}
			device := map[string]string{"phone": "phones", "desktop": "desktop", "all": "all devices"}[f.FormFactor]
			t := webTicket(p, "Dev", "L", fmt.Sprintf("Bring %s under %s on %s (now %s)", strings.ToUpper(m.ID), cwvValue(m.ID, cwvThresholds[m.ID][0]), device, cwvValue(m.ID, m.P75)),
				fmt.Sprintf("%s measures %s. Real Chrome users see %s at the 75th percentile; %s of visits are good. Google uses Core Web Vitals as a page experience signal.",
					strings.ToUpper(m.ID), map[string]string{"lcp": "how fast the main content shows", "inp": "how fast the page reacts to taps and clicks", "cls": "how much the layout jumps while loading", "ttfb": "how fast the server starts answering"}[m.ID],
					cwvValue(m.ID, m.P75), fmt.Sprintf("%.0f%%", m.Good*100)),
				fixes[m.ID], fmt.Sprintf("CrUX p75 %s ≤ %s on %s (28-day window, so allow a month).", strings.ToUpper(m.ID), cwvValue(m.ID, cwvThresholds[m.ID][0]), device),
				fmt.Sprintf("p75 %s %s on %s (good ≤ %s): %.0f%% good, %.0f%% poor visits.", strings.ToUpper(m.ID), cwvValue(m.ID, m.P75), device, cwvValue(m.ID, cwvThresholds[m.ID][0]), m.Good*100, m.Poor*100))
			if m.ID == "ttfb" {
				t.Effort, t.Points = "M", 3
			}
			out = append(out, t)
		}
	}
	if cs := s.CruxStats; cs != nil && cs.PoorLCP > 0 {
		t := webTicket(1, "Dev", "M", countTitle("Fix slow LCP on %s pages", cs.PoorLCP),
			"These pages have a poor Largest Contentful Paint (over 4 s) for real users.", fixes["lcp"],
			"No page in the crawl has a poor CrUX LCP.", "")
		t.Count, t.Unit = cs.PoorLCP, "pages"
		t.Share = share(cs.PoorLCP, max(1, cs.PagesWithData))
		t.Pct = 100 * float64(cs.PoorLCP) / float64(max(1, cs.PagesWithData))
		for _, w := range cs.WorstLCP {
			if len(t.Examples) == 3 {
				break
			}
			t.Examples = append(t.Examples, shortURL(w.URL))
			t.FullExamples = append(t.FullExamples, w.URL)
		}
		out = append(out, t)
	}
	return out
}

func webTicket(p int, owner, effort, title, problem string, fix []string, accept, evidence string) Ticket {
	return Ticket{Track: "SEO", Priority: fmt.Sprintf("P%d", p), Tone: []string{"bad", "warn", "info", ""}[p],
		Type: map[bool]string{true: "Bug", false: "Task"}[p == 0], Effort: effort, Points: map[string]int{"S": 2, "M": 3, "L": 5}[effort],
		Owner: owner, Title: title, Problem: problem, Fix: fix, Accept: accept, Evidence: evidence}
}

// CDNView feeds the CDN & caching sheet.
type CDNView struct {
	Provider, Evidence string
	Rows               []CDNRow
	Opportunities      []string
}

// CDNRow is one line of the delivery checklist.
type CDNRow struct {
	Label, Value, Note, Tone string
}

// htmlCached says whether the edge kept the page.
func htmlCached(r *types.CDNReport) bool {
	for _, h := range r.HTML {
		if h.Status == "HIT" || h.Status == "STALE" || h.Status == "REVALIDATED" || h.Age > 0 {
			return true
		}
	}
	return false
}

func cdnView(r *types.CDNReport) *CDNView {
	if r == nil || r.Error != "" || len(r.HTML) == 0 {
		return nil
	}
	v := &CDNView{Provider: r.Provider, Evidence: r.Evidence}
	if v.Provider == "" {
		v.Provider, v.Evidence = "No CDN detected", "no CDN headers in the answer"
	}
	h := r.HTML[0]
	statuses := []string{}
	for _, x := range r.HTML {
		st := x.Status
		if st == "" {
			st = "—"
		}
		statuses = append(statuses, st)
	}
	cached := htmlCached(r)
	row := CDNRow{Label: "HTML at the edge", Value: strings.Join(statuses, " → "), Note: cacheNote(h.CacheControl)}
	switch {
	case cached:
		row.Tone = "good"
	case r.Provider != "":
		row.Tone = "bad"
		row.Note += " · every visit waits for the origin server"
	default:
		row.Tone = "warn"
	}
	v.Rows = append(v.Rows, row)
	if a := r.Asset; a != nil {
		ar := CDNRow{Label: "Static files (JS/CSS)", Value: orDash(a.Status), Note: cacheNote(a.CacheControl)}
		ar.Tone = map[bool]string{true: "good", false: "warn"}[longCache(a.CacheControl)]
		v.Rows = append(v.Rows, ar)
	}
	comp := CDNRow{Label: "Compression", Value: orDash(r.Compression), Tone: "good"}
	switch r.Compression {
	case "br", "zstd":
		comp.Note = "modern compression"
	case "gzip":
		comp.Note, comp.Tone = "Brotli or zstd would be ~15-20% smaller", "warn"
	default:
		comp.Note, comp.Tone = "HTML is sent uncompressed", "bad"
	}
	v.Rows = append(v.Rows, comp)
	proto := CDNRow{Label: "Protocol", Value: strings.TrimPrefix(r.Protocol, "HTTP/"), Tone: "good"}
	if r.HTTP3 {
		proto.Value += " + HTTP/3"
	} else if strings.HasPrefix(r.Protocol, "HTTP/1") {
		proto.Tone, proto.Note = "bad", "HTTP/1.1 loads files one after another"
	} else {
		proto.Tone, proto.Note = "warn", "HTTP/3 not advertised"
	}
	proto.Value = "HTTP/" + proto.Value
	v.Rows = append(v.Rows, proto)
	v.Rows = append(v.Rows, CDNRow{Label: "Early Hints (103)", Value: yesNo(r.EarlyHints), Tone: map[bool]string{true: "good", false: ""}[r.EarlyHints], Note: "lets the browser fetch CSS/fonts while the page is built"})
	v.Rows = append(v.Rows, CDNRow{Label: "HSTS", Value: yesNo(r.HSTS), Tone: map[bool]string{true: "good", false: "warn"}[r.HSTS]})
	ttfb := []string{}
	for _, x := range r.HTML {
		ttfb = append(ttfb, fmt.Sprintf("%d ms", x.TTFBMs))
	}
	v.Rows = append(v.Rows, CDNRow{Label: "Time to first byte (start page)", Value: strings.Join(ttfb, " → "), Note: "from this computer; first request includes the connection"})

	if r.Provider == "Cloudflare" {
		v.Opportunities = []string{
			"Crawler Hints: Cloudflare tells search engines (via IndexNow) when pages change, so they recrawl less blindly.",
			"AI Crawl Control: see which AI bots visit, allow or block each one, and check the 'Block AI bots' setting against robots.txt.",
			"Markdown for Agents: serves a markdown version of pages to AI agents that ask for it.",
			"Tiered Cache and Cache Reserve: fewer trips to the origin once HTML is cacheable.",
		}
		if !r.EarlyHints {
			v.Opportunities = append(v.Opportunities, "Early Hints: one switch in Speed → Optimization.")
		}
	} else if r.Provider == "" {
		v.Opportunities = []string{"A CDN serves pages and files from a location near each visitor and absorbs crawler traffic; most offer a free tier."}
	}
	return v
}

// cdnTickets turns delivery findings into SEO tickets.
func cdnTickets(r *types.CDNReport, s *types.CrawlStats) []Ticket {
	if r == nil || r.Error != "" || len(r.HTML) == 0 {
		return nil
	}
	var out []Ticket
	slow := s.ResponseTimePercentiles.P50 > 800
	if r.Provider == "" {
		out = append(out, webTicket(2, "Dev", "M", "Serve the site through a CDN",
			"No CDN answered for the start page. Every visitor and crawler goes to the origin server, wherever it is.",
			[]string{"Put the site behind a CDN (Cloudflare, CloudFront, Fastly, Bunny…), starting with static files.", "Then cache HTML with a short edge TTL."},
			"Responses carry a CDN header and static files come from the edge.", "No CDN headers on the start page."))
	} else if !htmlCached(r) {
		p := 2
		if slow {
			p = 1
		}
		steps := map[string][]string{
			"Cloudflare":        {"Add a Cache Rule for HTML (eligible for cache, edge TTL 5-60 min) that bypasses when a session or cart cookie is present.", "Or send `Cache-Control: public, s-maxage=300, stale-while-revalidate=600` from the app (Next.js: ISR / revalidate) so Cloudflare keeps pages."},
			"Vercel":            {"Use ISR / revalidate, or send `Cache-Control: s-maxage=300, stale-while-revalidate`, so Vercel's edge serves pages."},
			"Amazon CloudFront": {"Give HTML a cache policy with a short TTL and forward only the cookies and headers the page needs."},
			"Fastly":            {"Set a Surrogate-Control TTL for HTML and purge on publish."},
			"Akamai":            {"Give HTML a short edge TTL in the property and purge on publish."},
		}[r.Provider]
		if steps == nil {
			steps = []string{"Let the CDN cache HTML with a short TTL (s-maxage) and stale-while-revalidate; bypass for logged-in users."}
		}
		steps = append(steps, "Purge or revalidate on publish so pages stay fresh.")
		ev := fmt.Sprintf("%s answers HTML with %s (%s).", r.Provider, strings.Join(statusesOf(r), " then "), r.HTML[0].CacheControl)
		if slow {
			ev += fmt.Sprintf(" Crawl median response: %s.", ms(s.ResponseTimePercentiles.P50))
		}
		out = append(out, webTicket(p, "Dev", "M", "Cache HTML at the "+r.Provider+" edge",
			"The CDN passes every page request to the origin server, which builds the page each time. Caching HTML at the edge cuts server time for visitors and crawlers, and helps LCP.",
			steps, "A second request for a public page is a cache HIT (or has Age > 0).", ev))
	}
	if r.Asset != nil && !longCache(r.Asset.CacheControl) {
		out = append(out, webTicket(2, "Dev", "S", "Cache static files for a long time",
			"Scripts and stylesheets are not cached for long, so repeat visits download them again.",
			[]string{"Serve fingerprinted files with `Cache-Control: public, max-age=31536000, immutable`."},
			"Static JS/CSS carry a max-age of at least 30 days.", fmt.Sprintf("%s: %s", shortURL(r.Asset.URL), orDash(r.Asset.CacheControl))))
	}
	if r.Compression == "" {
		out = append(out, webTicket(1, "Dev", "S", "Compress HTML (Brotli or gzip)",
			"The page is sent uncompressed, several times larger than needed.",
			[]string{"Turn on Brotli (or gzip) for text responses at the CDN or server."}, "HTML responses carry Content-Encoding br or gzip.", "No Content-Encoding on the start page."))
	}
	if strings.HasPrefix(r.Protocol, "HTTP/1") {
		out = append(out, webTicket(2, "Dev", "S", "Serve the site over HTTP/2 or HTTP/3",
			"HTTP/1.1 loads files one after another on few connections.", []string{"Enable HTTP/2 and HTTP/3 at the CDN or server."},
			"The start page answers over HTTP/2 or newer.", "Protocol: "+r.Protocol))
	}
	return out
}

func statusesOf(r *types.CDNReport) []string {
	var out []string
	for _, h := range r.HTML {
		out = append(out, orDash(h.Status))
	}
	return out
}

func cacheNote(cc string) string {
	if cc == "" {
		return "no Cache-Control"
	}
	return cc
}

// longCache reports a max-age of at least 30 days.
func longCache(cc string) bool {
	for _, part := range strings.Split(strings.ToLower(cc), ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "max-age=") || strings.HasPrefix(part, "s-maxage=") {
			var n int
			fmt.Sscanf(part[strings.Index(part, "=")+1:], "%d", &n)
			if n >= 30*86400 {
				return true
			}
		}
	}
	return false
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// rekey sorts SEO tickets by priority (stable) and numbers them again.
func rekey(ts []Ticket) {
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].Priority < ts[j].Priority })
	for i := range ts {
		ts[i].Key = fmt.Sprintf("%s-%d", ts[i].Track, i+1)
	}
}
