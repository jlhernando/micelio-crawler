package report

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/micelio/micelio/internal/browser"
	"github.com/micelio/micelio/internal/types"
)

// The PDF report is a short, client-ready audit: a dark cover in the
// dashboard's style, then light A4 sheets that print well. Every sheet has a
// fixed size, so lists are capped to what fits on one page.

//go:embed pdf.tmpl
var pdfHTML string

//go:embed pdf-mark.jpg
var pdfMark []byte

// PDFOptions carries what the stats alone do not hold.
type PDFOptions struct {
	// Totals of lists the dashboard caps in stats (totalBrokenLinks, ...).
	Totals      map[string]int
	GeneratedAt time.Time
	// Screenshots of the dashboard's Universe map, the first one the main
	// view. Only the dashboard can take them; the CLI leaves this empty.
	Universe []UniverseShot
	// Checked when the report is exported: real-user Core Web Vitals
	// (needs a Google API key) and the CDN / caching probe.
	CWV *types.CWVReport
	CDN *types.CDNReport
}

// UniverseShot is one screenshot of the Universe map.
type UniverseShot struct {
	// Key is the view: "structure" (main map) or a ticket view ("errors",
	// "redirects", "depth", "pagerank").
	Key     string
	Title   string
	Caption string
	JPEG    []byte
	Cover   []byte // optional label-free view for the cover backdrop
	Meta    *UniverseMeta
}

// UniverseMeta is what the capture page reports about the map it drew.
type UniverseMeta struct {
	Pages     int               `json:"pages"`
	Clusters  int               `json:"clusters"`
	Links     int               `json:"links"`
	Unreached int               `json:"unreached"`
	Dim       string            `json:"dim"`
	Top       []UniverseCluster `json:"top"`
}

// UniverseCluster is one legend entry of the map.
type UniverseCluster struct {
	Label string `json:"label"`
	Count int    `json:"count"`
	Color string `json:"color"`
}

// universeImage is a shot ready for the template.
type universeImage struct {
	Title   string
	Caption string
	Src     template.URL
}

// PDFData feeds the print template.
type PDFData struct {
	Domain      string
	SeedURL     string
	Date        string
	Duration    string
	Score       int
	ScoreLabel  string
	ScoreTone   string
	ScoreDash   float64 // stroke-dashoffset of the score ring (r=40)
	Cover       []Metric
	KPIs        []Metric
	Insights    []string
	Meaning     string
	Areas       []Area
	Status      []Bar
	Reasons     []Bar
	Issues      []Issue
	Depth       []Bar
	Speed       []Bar
	Percentiles []Metric
	LinkFacts   []Metric
	Checks      []Check
	TopPages    []TopPage
	HasInlinks  bool           // CLI crawls free links before the report: hide the column
	Hero        *universeImage // main Universe view (cover backdrop and its own sheet)
	HeroMeta    *UniverseMeta
	HeroFacts   []Metric
	HeroStory   string
	CoverSrc    template.URL
	Tickets     []Ticket // SEO backlog
	GEO         []Ticket // GEO backlog (AI search and agents)
	GEOView     *GEOView // "Ready for AI search?" sheet
	Overview    []Ticket // SEO rows of the backlog table
	GEOOverview []Ticket // GEO rows of the backlog table
	Matrix      []Ticket // tickets on the impact × effort chart
	Plan        *SprintPlan
	Cards       [][]Ticket // SEO ticket cards, one slice per sheet
	GEOCards    [][]Ticket // GEO ticket cards
	CWV         *CWVView
	CDN         *CDNView
	Sheets      int
}

// Metric is one labelled number.
type Metric struct {
	Label string
	Value string
	Note  string
	Tone  string // good, warn, bad, info or ""
}

// Area is one tile of the health scorecard.
type Area struct {
	Name   string
	Score  int
	Tone   string
	Detail string
}

// Bar is one row of a bar chart; Pct is its width (0-100).
type Bar struct {
	Label string
	Count int
	Pct   float64
	Share string
	Tone  string
}

// Check is one optional audit tile (hreflang, agents, devices, ...).
type Check struct {
	Name    string
	Value   string
	Tone    string
	Summary string
	Rows    []Bar
}

// TopPage is one row of the strongest pages table.
type TopPage struct {
	Rank     int
	Path     string
	Title    string
	PageRank string
	Inlinks  int
	Status   int
}

var pdfTemplate = template.Must(template.New("pdf").Funcs(template.FuncMap{
	"comma": func(n int) string {
		if n < 0 {
			return "-" + commaFormat(-n)
		}
		return commaFormat(n)
	},
	"pad":    func(n int) string { return fmt.Sprintf("%02d", n) },
	"inc":    func(n int) int { return n + 1 },
	"upper":  strings.ToUpper,
	"plural": plural,
	"unit":   unitOf,
}).Parse(pdfHTML))

// GeneratePrintHTML writes the print-ready HTML that GeneratePDF turns into
// a PDF. Useful on its own to preview the layout in a browser.
func GeneratePrintHTML(w io.Writer, seedURL string, stats *types.CrawlStats, pages []*types.PageData, opts PDFOptions) error {
	return pdfTemplate.Execute(w, struct {
		D    *PDFData
		Mark template.URL
	}{BuildPDFData(seedURL, stats, pages, opts), template.URL("data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(pdfMark))})
}

// GeneratePDF renders the report with headless Chrome.
func GeneratePDF(ctx context.Context, seedURL string, stats *types.CrawlStats, pages []*types.PageData, opts PDFOptions) ([]byte, error) {
	var buf bytes.Buffer
	if err := GeneratePrintHTML(&buf, seedURL, stats, pages, opts); err != nil {
		return nil, err
	}
	return browser.PrintPDF(ctx, buf.String())
}

// BuildPDFData turns crawl stats into the report's content.
func BuildPDFData(seedURL string, stats *types.CrawlStats, pages []*types.PageData, opts PDFOptions) *PDFData {
	if stats == nil {
		stats = &types.CrawlStats{}
	}
	at := opts.GeneratedAt
	if at.IsZero() {
		at = time.Now()
	}
	total := stats.TotalPages
	if total == 0 {
		total = len(pages)
	}
	t := totals{stats: stats, extra: opts.Totals}
	d := &PDFData{
		Domain:   siteLabel(seedURL),
		SeedURL:  seedURL,
		Date:     at.Format("2 January 2006"),
		Duration: humanDuration(stats.CrawlDurationMs),
	}

	d.Score = healthScore(stats, total, t)
	switch {
	case total == 0:
		d.ScoreLabel, d.ScoreTone = "No data", ""
	case d.Score >= 80:
		d.ScoreLabel, d.ScoreTone = "Good", "good"
	case d.Score >= 60:
		d.ScoreLabel, d.ScoreTone = "Needs work", "warn"
	default:
		d.ScoreLabel, d.ScoreTone = "Poor", "bad"
	}
	circ := 2 * math.Pi * 40
	d.ScoreDash = circ - float64(d.Score)/100*circ

	idx := stats.IndexabilityStats.Indexable
	errs := classCount(stats.StatusCodes, 4) + classCount(stats.StatusCodes, 5)
	d.Cover = []Metric{
		{Label: "Pages crawled", Value: commaFormat(total)},
		{Label: "Indexable", Value: share(idx, total), Tone: toneRatio(idx, total, .9, .7)},
		{Label: "Errors (4xx/5xx)", Value: commaFormat(errs), Tone: toneCount(errs, total)},
		{Label: "Median response", Value: ms(stats.ResponseTimePercentiles.P50), Tone: toneMs(stats.ResponseTimePercentiles.P50)},
	}

	d.Issues = buildIssues(stats, total, t)
	d.Areas = buildAreas(stats, total, t)
	d.Status = statusBars(stats.StatusCodes)
	d.Reasons = reasonBars(stats.IndexabilityStats.Reasons, total)
	depth := stats.DepthDistribution
	if li := stats.LinkIntelligenceStats; li != nil && len(li.ClickDepthDistribution) > 0 {
		depth = li.ClickDepthDistribution // real clicks; crawl depth counts sitemap URLs as 0
	}
	d.Depth = depthBars(depth)
	d.Speed = speedBars(stats.ResponseTimeBuckets)
	d.Percentiles = []Metric{
		{Label: "p50", Value: ms(stats.ResponseTimePercentiles.P50), Tone: toneMs(stats.ResponseTimePercentiles.P50)},
		{Label: "p90", Value: ms(stats.ResponseTimePercentiles.P90), Tone: toneMs(stats.ResponseTimePercentiles.P90)},
		{Label: "p99", Value: ms(stats.ResponseTimePercentiles.P99), Tone: toneMs(stats.ResponseTimePercentiles.P99)},
	}
	d.LinkFacts = linkFacts(stats, total, t)
	d.KPIs = kpis(stats, total, t)
	d.Tickets = buildBacklog(d.Issues, stats, pages, seedURL)
	d.Tickets = append(d.Tickets, cwvTickets(opts.CWV, stats)...)
	d.Tickets = append(d.Tickets, cdnTickets(opts.CDN, stats)...)
	rekey(d.Tickets)
	if total > 0 {
		d.CWV = cwvView(opts.CWV, stats)
	}
	d.CDN = cdnView(opts.CDN)
	provider := ""
	if opts.CDN != nil {
		provider = opts.CDN.Provider
	}
	d.GEO = geoBacklog(stats, total, provider)
	d.GEOView = geoView(stats, total)
	d.Insights = insights(stats, total, d.Tickets, d.GEO, d.Reasons)
	d.Meaning = meaning(d.Score, total, append(append([]Ticket{}, d.Tickets...), d.GEO...))
	d.Checks = checks(stats)
	d.TopPages = topPages(stats, pages)
	for _, p := range d.TopPages {
		d.HasInlinks = d.HasInlinks || p.Inlinks > 0
	}

	img := func(u UniverseShot) *universeImage {
		return &universeImage{Title: u.Title, Caption: u.Caption, Src: template.URL("data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(u.JPEG))}
	}
	for _, u := range opts.Universe {
		if u.Key == "structure" && d.Hero == nil {
			d.Hero = img(u)
			d.CoverSrc = d.Hero.Src
			if len(u.Cover) > 0 {
				d.CoverSrc = template.URL("data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(u.Cover))
			}
			d.HeroMeta = u.Meta
			continue
		}
		for i := range d.Tickets {
			if d.Tickets[i].View == u.Key && d.Tickets[i].Map == nil {
				d.Tickets[i].Map = img(u)
			}
		}
	}
	if m := d.HeroMeta; m != nil {
		d.HeroFacts = []Metric{
			{Label: "Pages on the map", Value: commaFormat(m.Pages)},
			{Label: "Sections", Value: commaFormat(m.Clusters)},
			{Label: "Internal links", Value: commaFormat(m.Links)},
			{Label: "Not reachable from start", Value: commaFormat(m.Unreached), Tone: toneCount(m.Unreached, max(1, m.Pages))},
		}
		if len(m.Top) > 0 && m.Pages > 0 {
			big := m.Top[0]
			d.HeroStory = fmt.Sprintf("The map splits into %d sections. The biggest, %s, holds %s of the pages.", m.Clusters, big.Label, share(big.Count, m.Pages))
			if m.Unreached > 0 {
				d.HeroStory += fmt.Sprintf(" %s pages hang outside the structure: no link path reaches them from the start page.", commaFormat(m.Unreached))
			}
		}
	}

	// Backlog: a table, a sprint plan, then cards. A card with a map gets a
	// whole sheet; the others go two per sheet. Cards are for urgent tickets
	// (P0-P1) and for tickets with a map; the rest stay in the table and CSV.
	d.Overview = d.Tickets[:min(len(d.Tickets), 11)]
	d.GEOOverview = d.GEO[:min(len(d.GEO), 16-len(d.Overview))]
	d.Matrix = append(append([]Ticket{}, d.Overview...), d.GEOOverview...)
	placeOnMatrix(d.Matrix)
	d.Plan = sprintPlan(append(append([]Ticket{}, d.Tickets...), d.GEO...))
	d.Cards = cardSheets(d.Tickets, 6)
	d.GEOCards = cardSheets(d.GEO, 2)

	d.Sheets = 5 + len(d.Cards) + len(d.GEOCards) // cover, summary, scorecard, structure, top pages
	if d.GEOView != nil {
		d.Sheets++
	}
	if d.CWV != nil {
		d.Sheets++
	}
	if d.CDN != nil {
		d.Sheets++
	}
	if d.Hero != nil {
		d.Sheets++
	}
	if len(d.Tickets)+len(d.GEO) > 0 {
		d.Sheets += 2 // backlog table and sprint plan
	}
	if len(d.Checks) > 0 {
		d.Sheets++
	}
	return d
}

// cardSheets lays tickets out as cards: one with a map fills a sheet, the
// others go two per sheet. Cards are for urgent tickets (P0-P1) and tickets
// with a map; the rest stay in the table and the CSV.
func cardSheets(tickets []Ticket, maxSheets int) [][]Ticket {
	var sheets [][]Ticket
	var pending []Ticket
	for _, tk := range tickets {
		if (tk.Map == nil && tk.Priority != "P0" && tk.Priority != "P1") || len(sheets) >= maxSheets {
			continue
		}
		if tk.Map != nil {
			sheets = append(sheets, []Ticket{tk})
			continue
		}
		pending = append(pending, tk)
		if len(pending) == 2 {
			sheets = append(sheets, pending)
			pending = nil
		}
	}
	if len(pending) > 0 && len(sheets) < maxSheets {
		sheets = append(sheets, pending)
	}
	return sheets
}

// totals returns the real size of a list stats may cap.
type totals struct {
	stats *types.CrawlStats
	extra map[string]int
}

func (t totals) get(key string, listLen int) int {
	if n := t.extra[key]; n > listLen {
		return n
	}
	return listLen
}

func (t totals) orphans() int {
	return t.get("totalOrphanPages", max(t.stats.TotalOrphanPages, len(t.stats.OrphanPages)))
}

func (t totals) broken() int { return t.get("totalBrokenLinks", len(t.stats.BrokenLinks)) }

func (t totals) thin() int { return t.get("totalThinContentPages", len(t.stats.ThinContentPages)) }

// healthScore is the dashboard's 0-100 health score (CrawlHealthScore.svelte).
func healthScore(s *types.CrawlStats, total int, t totals) int {
	if total == 0 {
		return 0
	}
	ratio := func(n int) float64 { return math.Min(1, float64(n)/float64(total)) }
	score := ratio(s.IndexabilityStats.Indexable) * 25
	score += ratio(total-s.PagesWithoutTitle) * 15
	score += ratio(total-s.PagesWithoutDescription) * 15
	score += ratio(total-s.PagesWithoutH1) * 10
	b := s.ResponseTimeBuckets
	if all := b.Fast + b.Medium + b.Slow + b.Slowest; all > 0 {
		score += float64(b.Fast) / float64(all) * 10
	} else {
		score += 5
	}
	score += math.Max(0, 10-float64(t.broken()+len(s.BrokenExternalLinks))*0.5)
	if cs := s.CanonicalStats; cs != nil {
		with := cs.SelfReferencing + cs.Canonicalized
		score += float64(min(with, total)) / float64(total) * 10
	} else {
		score += 5
	}
	score += math.Max(0, 5-float64(t.orphans())*0.25)
	return int(math.Round(math.Min(100, score)))
}

func buildAreas(s *types.CrawlStats, total int, t totals) []Area {
	if total == 0 {
		return nil
	}
	pct := func(f float64) int { return int(math.Round(math.Max(0, math.Min(1, f)) * 100)) }
	ok := func(missing int) float64 { return 1 - float64(missing)/float64(total) }
	tone := func(n int) string {
		switch {
		case n >= 80:
			return "good"
		case n >= 60:
			return "warn"
		}
		return "bad"
	}
	var areas []Area
	add := func(name string, score int, detail string) {
		areas = append(areas, Area{Name: name, Score: score, Tone: tone(score), Detail: detail})
	}
	idx := s.IndexabilityStats.Indexable
	add("Indexability", pct(float64(idx)/float64(total)), fmt.Sprintf("%s of %s pages can be indexed", commaFormat(idx), commaFormat(total)))
	tags := (ok(s.PagesWithoutTitle) + ok(s.PagesWithoutDescription) + ok(s.PagesWithoutH1)) / 3
	add("On-page tags", pct(tags), fmt.Sprintf("%s missing titles, %s descriptions, %s H1", commaFormat(s.PagesWithoutTitle), commaFormat(s.PagesWithoutDescription), commaFormat(s.PagesWithoutH1)))
	add("Internal links", pct(ok(t.broken()+t.orphans())), fmt.Sprintf("%s broken links, %s orphan pages", commaFormat(t.broken()), commaFormat(t.orphans())))
	add("Content", pct(ok(t.thin()+s.DuplicateTitleCount)), fmt.Sprintf("%s thin pages, %s duplicate titles", commaFormat(t.thin()), commaFormat(s.DuplicateTitleCount)))
	b := s.ResponseTimeBuckets
	if all := b.Fast + b.Medium + b.Slow + b.Slowest; all > 0 {
		add("Speed", pct((float64(b.Fast)+float64(b.Medium)*.5)/float64(all)), fmt.Sprintf("%s of pages answer in under 500 ms", share(b.Fast, all)))
	}
	return areas
}

func statusBars(codes map[int]int) []Bar {
	all := 0
	for _, n := range codes {
		all += n
	}
	if all == 0 {
		return nil
	}
	classes := []struct {
		c     int
		label string
		tone  string
	}{{2, "2xx OK", "good"}, {3, "3xx Redirect", "info"}, {4, "4xx Client error", "warn"}, {5, "5xx Server error", "bad"}}
	var out []Bar
	for _, cl := range classes {
		if n := classCount(codes, cl.c); n > 0 {
			out = append(out, Bar{Label: cl.label, Count: n, Pct: float64(n) / float64(all) * 100, Share: share(n, all), Tone: cl.tone})
		}
	}
	if other := all - sumBars(out); other > 0 {
		out = append(out, Bar{Label: "No response", Count: other, Pct: float64(other) / float64(all) * 100, Share: share(other, all)})
	}
	return out
}

func reasonBars(reasons map[string]int, total int) []Bar {
	var out []Bar
	top := 0
	for label, n := range reasons {
		if n > 0 {
			out = append(out, Bar{Label: label, Count: n, Share: share(n, total), Tone: "warn"})
			top = max(top, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > 6 {
		out = out[:6]
	}
	for i := range out {
		out[i].Pct = float64(out[i].Count) / float64(top) * 100
	}
	return out
}

func depthBars(dist map[int]int) []Bar {
	if len(dist) == 0 {
		return nil
	}
	buckets := make([]int, 7) // 0..5, 6+
	all, top := 0, 0
	for depth, n := range dist {
		buckets[min(max(depth, 0), 6)] += n
		all += n
	}
	for _, n := range buckets {
		top = max(top, n)
	}
	out := make([]Bar, 0, 7)
	for i, n := range buckets {
		label := fmt.Sprint(i)
		if i == 6 {
			label = "6+"
		}
		tone := "info"
		if i >= 4 {
			tone = "warn"
		}
		pct := 0.0
		if top > 0 {
			pct = float64(n) / float64(top) * 100
		}
		out = append(out, Bar{Label: label, Count: n, Pct: pct, Share: share(n, all), Tone: tone})
	}
	return out
}

func speedBars(b types.ResponseTimeBuckets) []Bar {
	all := b.Fast + b.Medium + b.Slow + b.Slowest
	if all == 0 {
		return nil
	}
	row := func(label string, n int, tone string) Bar {
		return Bar{Label: label, Count: n, Pct: float64(n) / float64(all) * 100, Share: share(n, all), Tone: tone}
	}
	return []Bar{
		row("Under 500 ms", b.Fast, "good"),
		row("500 ms – 1 s", b.Medium, "info"),
		row("1 – 2 s", b.Slow, "warn"),
		row("Over 2 s", b.Slowest, "bad"),
	}
}

func linkFacts(s *types.CrawlStats, total int, t totals) []Metric {
	out := []Metric{
		{Label: "Internal links", Value: commaFormat(s.TotalInternalLinks)},
		{Label: "External links", Value: commaFormat(s.TotalExternalLinks)},
		{Label: "Orphan pages", Value: commaFormat(t.orphans()), Tone: toneCount(t.orphans(), total)},
		{Label: "Broken internal links", Value: commaFormat(t.broken()), Tone: toneCount(t.broken(), total)},
	}
	if li := s.LinkIntelligenceStats; li != nil && li.AvgClickDepth > 0 {
		out = append(out, Metric{Label: "Avg. click depth", Value: fmt.Sprintf("%.1f", li.AvgClickDepth), Note: fmt.Sprintf("max %d", li.MaxClickDepth)})
	}
	return out
}

func kpis(s *types.CrawlStats, total int, t totals) []Metric {
	idx := s.IndexabilityStats.Indexable
	redirects := redirectCount(s)
	out := []Metric{
		{Label: "Pages crawled", Value: commaFormat(total), Note: durationNote(s.CrawlDurationMs)},
		{Label: "Indexable pages", Value: commaFormat(idx), Note: share(idx, total) + " of crawl", Tone: toneRatio(idx, total, .9, .7)},
		{Label: "Redirects", Value: commaFormat(redirects), Note: share(redirects, total) + " of crawl", Tone: toneCount(redirects, total)},
		{Label: "Errors", Value: commaFormat(classCount(s.StatusCodes, 4) + classCount(s.StatusCodes, 5)), Note: "4xx and 5xx", Tone: toneCount(classCount(s.StatusCodes, 4)+classCount(s.StatusCodes, 5), total)},
		{Label: "Robots-blocked", Value: commaFormat(s.RobotsBlockedCount), Note: "internal URLs"},
		{Label: "Structured data", Value: share(s.PagesWithStructuredData, total), Note: commaFormat(s.PagesWithStructuredData) + " pages"},
		{Label: "Median response", Value: ms(s.ResponseTimePercentiles.P50), Note: "p90 " + ms(s.ResponseTimePercentiles.P90), Tone: toneMs(s.ResponseTimePercentiles.P50)},
		{Label: "Orphan pages", Value: commaFormat(t.orphans()), Note: "no internal links in", Tone: toneCount(t.orphans(), total)},
	}
	return out
}

func insights(s *types.CrawlStats, total int, tickets, geo []Ticket, reasons []Bar) []string {
	if total == 0 {
		return []string{"The crawl has no pages yet, so there is nothing to report."}
	}
	var out []string
	idx := s.IndexabilityStats.Indexable
	line := fmt.Sprintf("%s of %s crawled pages (%s) can appear in search results.", commaFormat(idx), commaFormat(total), share(idx, total))
	if len(reasons) > 0 {
		line += fmt.Sprintf(" Most of the rest are excluded by %s (%s pages).", reasons[0].Label, commaFormat(reasons[0].Count))
	}
	out = append(out, line)
	urgent := func(ts []Ticket) (n, pts int) {
		for _, t := range ts {
			if t.Priority == "P0" || t.Priority == "P1" {
				n++
				pts += t.Points
			}
		}
		return n, pts
	}
	if len(tickets)+len(geo) > 0 {
		su, sp := urgent(tickets)
		gu, gp := urgent(geo)
		line := fmt.Sprintf("SEO backlog: %s, %d urgent (%d pt).", plural(len(tickets), "ticket"), su, sp)
		if s.AgentReadiness != nil || len(geo) > 0 {
			line += fmt.Sprintf(" GEO backlog (AI search): %s, %d urgent (%d pt).", plural(len(geo), "ticket"), gu, gp)
		}
		out = append(out, line)
	}
	for _, t := range tickets {
		if len(out) >= 4 {
			break
		}
		if t.Priority == "P0" || t.Priority == "P1" {
			out = append(out, fmt.Sprintf("%s · %s (%s of %s).", t.Key, t.Title, t.Share, t.Unit))
		}
	}
	for _, t := range geo {
		if len(out) >= 5 {
			break
		}
		if t.Priority == "P0" || t.Priority == "P1" {
			out = append(out, fmt.Sprintf("%s · %s.", t.Key, t.Title))
			break
		}
	}
	if p := s.ResponseTimePercentiles; p.P50 > 0 {
		b := s.ResponseTimeBuckets
		slow := b.Slow + b.Slowest
		out = append(out, fmt.Sprintf("Half the pages answer in under %s; %s take more than one second.", ms(p.P50), share(slow, max(1, b.Fast+b.Medium+slow))))
	}
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func meaning(score, total int, tickets []Ticket) string {
	if total == 0 {
		return "Run a crawl first, then export this report again."
	}
	p0 := 0
	for _, t := range tickets {
		if t.Priority == "P0" {
			p0++
		}
	}
	switch {
	case score >= 80 && p0 == 0:
		return "The site's technical base is in good shape. The backlog is polish: work through it to protect what already works."
	case score >= 80:
		return fmt.Sprintf("The base is solid, but %d ticket(s) are P0. Start there: they affect pages search engines can see today.", p0)
	case score >= 60:
		return "Search engines can read most of the site, but gaps in indexing, tags or links hold it back. Start with the 'Now' column of the sprint plan."
	}
	return "Large parts of the site are hard for search engines to crawl or index. The P0 tickets will have the biggest effect."
}

func checks(s *types.CrawlStats) []Check {
	var out []Check
	if h := s.Hreflang; h != nil && h.PagesWithHreflang > 0 {
		c := Check{Name: "Hreflang", Value: commaFormat(h.PagesWithHreflang), Summary: fmt.Sprintf("pages with hreflang, %d language codes", len(h.Codes)), Tone: "good"}
		top := 0
		for _, is := range h.Issues {
			top = max(top, is.Pages)
		}
		for _, is := range h.Issues {
			if len(c.Rows) == 4 {
				break
			}
			tone := map[string]string{"error": "bad", "warning": "warn"}[is.Severity]
			if tone == "bad" || (tone == "warn" && c.Tone == "good") {
				c.Tone = tone
			}
			c.Rows = append(c.Rows, Bar{Label: is.Label, Count: is.Pages, Pct: float64(is.Pages) / float64(max(1, top)) * 100, Tone: tone})
		}
		out = append(out, c)
	}
	if sp := s.Soft404Stats; sp != nil && sp.Total > 0 {
		likely := sp.ByConfidence["high"] + sp.ByConfidence["medium"]
		tone := "warn"
		if likely == 0 {
			tone = "good"
		}
		out = append(out, Check{Name: "Soft 404 candidates", Value: commaFormat(likely), Tone: tone, Summary: fmt.Sprintf("high or medium confidence, %s low. Confirm in Search Console.", commaFormat(sp.ByConfidence["low"]))})
	}
	if dp := s.DeviceParity; dp != nil && dp.Compared > 0 {
		tone := "good"
		if dp.Identical < dp.Compared*9/10 {
			tone = "warn"
		}
		out = append(out, Check{Name: "Device parity", Value: share(dp.Identical, dp.Compared), Tone: tone, Summary: fmt.Sprintf("of %s pages identical on both devices", commaFormat(dp.Compared))})
	}
	if c := s.CruxStats; c != nil && c.AvgLCPMs != nil {
		tone := "good"
		if *c.AvgLCPMs > 4000 {
			tone = "bad"
		} else if *c.AvgLCPMs > 2500 {
			tone = "warn"
		}
		sum := "average LCP from Chrome UX Report (real users)"
		if c.AvgCLS != nil {
			sum += fmt.Sprintf(", CLS %.2f", *c.AvgCLS)
		}
		out = append(out, Check{Name: "Core Web Vitals", Value: ms(*c.AvgLCPMs), Tone: tone, Summary: sum})
	}
	if sm := s.SitemapStats; sm != nil && sm.TotalSitemapURLs > 0 {
		out = append(out, Check{Name: "Sitemaps", Value: commaFormat(sm.TotalSitemapURLs), Tone: "info", Summary: "URLs listed in the XML sitemaps"})
	}
	return out
}

func topPages(s *types.CrawlStats, pages []*types.PageData) []TopPage {
	type pr struct {
		url     string
		title   string
		score   float64
		inlinks int
		status  int
	}
	var list []pr
	for _, p := range pages {
		if p == nil || p.PageRank <= 0 {
			continue
		}
		title := ""
		if p.Title != nil {
			title = p.Title.Text
		}
		list = append(list, pr{p.URL, title, p.PageRank, p.Inlinks, p.StatusCode})
	}
	if len(list) == 0 {
		for u, sc := range s.PageRankScores {
			list = append(list, pr{url: u, score: sc})
		}
	}
	if len(list) == 0 && s.LinkIntelligenceStats != nil {
		for _, e := range s.LinkIntelligenceStats.TopAuthorities {
			list = append(list, pr{url: e.URL, score: e.Score})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].url < list[j].url
	})
	if len(list) > 12 {
		list = list[:12]
	}
	out := make([]TopPage, len(list))
	for i, p := range list {
		out[i] = TopPage{Rank: i + 1, Path: shortURL(p.url), Title: p.title, PageRank: fmt.Sprintf("%.2f", p.score), Inlinks: p.inlinks, Status: p.status}
	}
	return out
}

// ── small helpers ──

func classCount(codes map[int]int, class int) int {
	n := 0
	for code, c := range codes {
		if code/100 == class {
			n += c
		}
	}
	return n
}

func sumBars(bars []Bar) int {
	n := 0
	for _, b := range bars {
		n += b.Count
	}
	return n
}

func share(n, total int) string {
	if total <= 0 {
		return "0%"
	}
	p := float64(n) / float64(total) * 100
	if p > 0 && p < 1 {
		return "<1%"
	}
	if p > 99 && p < 100 {
		return ">99%"
	}
	return fmt.Sprintf("%.0f%%", p)
}

func ms(v float64) string {
	if v <= 0 {
		return "—"
	}
	if v >= 1000 {
		return fmt.Sprintf("%.1f s", v/1000)
	}
	return fmt.Sprintf("%.0f ms", v)
}

func toneMs(v float64) string {
	switch {
	case v <= 0:
		return ""
	case v < 500:
		return "good"
	case v < 1000:
		return "warn"
	}
	return "bad"
}

func toneRatio(n, total int, good, warn float64) string {
	if total == 0 {
		return ""
	}
	r := float64(n) / float64(total)
	switch {
	case r >= good:
		return "good"
	case r >= warn:
		return "warn"
	}
	return "bad"
}

func toneCount(n, total int) string {
	switch {
	case n == 0:
		return "good"
	case total > 0 && float64(n)/float64(total) > .05:
		return "bad"
	}
	return "warn"
}

func humanDuration(msec int64) string {
	if msec <= 0 {
		return ""
	}
	d := time.Duration(msec) * time.Millisecond
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.0f s", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
}

func durationNote(msec int64) string {
	if h := humanDuration(msec); h != "" {
		return "in " + h
	}
	return ""
}

func siteLabel(seedURL string) string {
	u, err := url.Parse(seedURL)
	if err != nil || u.Hostname() == "" {
		return seedURL
	}
	return strings.TrimPrefix(u.Hostname(), "www.") + strings.TrimRight(u.Path, "/")
}

// shortURL drops the scheme and cuts long URLs so they fit a table cell.
func shortURL(raw string) string {
	s := raw
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		s = u.Path
		if u.RawQuery != "" {
			s += "?" + u.RawQuery
		}
		if s == "" {
			s = "/"
		}
	}
	if r := []rune(s); len(r) > 64 {
		s = string(r[:61]) + "…"
	}
	return s
}

func firstWord(list []types.URLWordCount) string {
	if len(list) > 0 {
		return list[0].URL
	}
	return ""
}
