package alerts

import (
	"fmt"
	"html"
	"net/url"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// A crawl email: the run's alerts plus what changed since the previous crawl
// of the same site or schedule. Only runs worth reading are sent (see
// ShouldEmail), so a stable site stays quiet.

// maxExamples is the number of example URLs listed per change.
const maxExamples = 10

// Change is one difference between two crawls.
type Change struct {
	Label     string
	Before    string
	After     string
	Important bool     // worth an email on its own
	Examples  []string // up to maxExamples URLs
	More      int      // URLs not listed
}

// CrawlSnapshot is what CompareCrawls needs from one crawl.
type CrawlSnapshot struct {
	Stats *types.CrawlStats
	URLs  []string // crawled URLs; optional (enables "URLs no longer found")
}

// RunInfo describes one finished run for the email.
type RunInfo struct {
	Name        string // schedule name, else the host
	SeedURL     string
	Status      string // completed, skipped, failed, stopped
	Reason      string // why skipped/failed/stopped
	Pages       int
	Errors      int
	DurationMs  int64
	Link        string // the crawl in the dashboard
	Alerts      *types.AlertSummary
	PrevAlerts  map[string]bool // alert IDs of the previous crawl
	HasPrevious bool
	Changes     []Change
}

// CompareCrawls lists the differences between the previous and current
// crawl that an SEO health check cares about.
func CompareCrawls(prev, cur CrawlSnapshot) []Change {
	if prev.Stats == nil || cur.Stats == nil {
		return nil
	}
	p, c := prev.Stats, cur.Stats
	var out []Change

	// Pages crawled.
	if d := c.TotalPages - p.TotalPages; d != 0 {
		out = append(out, Change{
			Label: "Pages crawled", Before: fmt.Sprint(p.TotalPages), After: fmt.Sprint(c.TotalPages),
			Important: abs(d) >= 20 && pct(abs(d), p.TotalPages) >= 10,
		})
	}

	// 4xx/5xx responses.
	pe, ce := countCodes(p.StatusCodes, 400, 600), countCodes(c.StatusCodes, 400, 600)
	if ce != pe {
		out = append(out, Change{
			Label: "Error responses (4xx/5xx)", Before: fmt.Sprint(pe), After: fmt.Sprint(ce),
			Important: ce-pe >= 10 && pct(ce-pe, max(pe, 1)) >= 20,
		})
	}
	p5, c5 := countCodes(p.StatusCodes, 500, 600), countCodes(c.StatusCodes, 500, 600)
	if c5 != p5 {
		out = append(out, Change{
			Label: "Server errors (5xx)", Before: fmt.Sprint(p5), After: fmt.Sprint(c5),
			Important: c5-p5 >= 5,
		})
	}

	// Indexable share.
	pi, ci := indexablePct(p), indexablePct(c)
	if pi >= 0 && ci >= 0 && fmt.Sprintf("%.1f", pi) != fmt.Sprintf("%.1f", ci) {
		out = append(out, Change{
			Label: "Indexable pages", Before: fmt.Sprintf("%.1f%%", pi), After: fmt.Sprintf("%.1f%%", ci),
			Important: pi-ci >= 2,
		})
	}

	// Soft-404 candidates (high and medium confidence).
	ps, cs := soft404Set(p), soft404Set(c)
	if ps != nil || cs != nil {
		added, resolved := diffSets(ps, cs), diffSets(cs, ps)
		if len(added) > 0 {
			ch := Change{Label: "New soft-404 candidates", Before: fmt.Sprint(len(ps)), After: fmt.Sprint(len(cs)), Important: true}
			ch.Examples, ch.More = examples(added)
			out = append(out, ch)
		}
		if len(resolved) > 0 {
			ch := Change{Label: "Soft-404 candidates no longer flagged", Before: fmt.Sprint(len(ps)), After: fmt.Sprint(len(cs))}
			ch.Examples, ch.More = examples(resolved)
			out = append(out, ch)
		}
	}

	// URLs no longer found (only when both URL lists are known).
	if len(prev.URLs) > 0 && len(cur.URLs) > 0 {
		gone := diffSets(toSet(cur.URLs), toSet(prev.URLs))
		if len(gone) > 0 {
			ch := Change{
				Label: "URLs crawled last time but not this time", After: fmt.Sprintf("%d of %d", len(gone), len(prev.URLs)),
				Important: len(gone) >= 10 && pct(len(gone), len(prev.URLs)) >= 5,
			}
			ch.Examples, ch.More = examples(gone)
			out = append(out, ch)
		}
	}
	return out
}

// ShouldEmail reports whether a run is worth an email: always when asked,
// for runs that did not complete, for critical or new alerts, and for
// important changes.
func ShouldEmail(r RunInfo, always bool) bool {
	if always || r.Status != "completed" {
		return true
	}
	if r.Alerts != nil {
		for _, a := range r.Alerts.Alerts {
			if a.Severity == "critical" || (r.HasPrevious && !r.PrevAlerts[a.ID]) {
				return true
			}
		}
		if !r.HasPrevious && len(r.Alerts.Alerts) > 0 {
			return true // first crawl: report what it found
		}
	}
	for _, c := range r.Changes {
		if c.Important {
			return true
		}
	}
	return false
}

// RenderEmail builds the subject and the text and HTML bodies.
func RenderEmail(r RunInfo) (subject, text, htmlBody string) {
	name := r.Name
	if name == "" {
		name = hostOf(r.SeedURL)
	}
	var tags []string
	switch r.Status {
	case "skipped":
		tags = append(tags, "skipped")
	case "failed":
		tags = append(tags, "failed")
	case "stopped":
		tags = append(tags, "stopped")
	}
	if n := alertCount(r.Alerts); n > 0 {
		tags = append(tags, plural(n, "alert", "alerts"))
	}
	for _, c := range r.Changes {
		if c.Label == "New soft-404 candidates" {
			tags = append(tags, fmt.Sprintf("+%d soft-404", len(c.Examples)+c.More))
		}
	}
	if len(tags) == 0 {
		tags = append(tags, "no issues")
	}
	subject = fmt.Sprintf("[Micelio] %s: %s", name, strings.Join(tags, ", "))

	var t strings.Builder
	var h strings.Builder
	esc := html.EscapeString
	h.WriteString(`<div style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;font-size:15px;line-height:1.45;color:#1f2328;max-width:640px">`)
	fmt.Fprintf(&h, `<h2 style="font-size:18px;margin:0 0 4px">%s</h2><div style="color:#57606a;margin-bottom:16px">%s</div>`, esc(name), esc(r.SeedURL))
	fmt.Fprintf(&t, "%s\n%s\n\n", name, r.SeedURL)

	switch r.Status {
	case "completed":
		line := fmt.Sprintf("%d pages crawled, %d errors", r.Pages, r.Errors)
		if r.DurationMs > 0 {
			line += fmt.Sprintf(", %s", fmtDuration(r.DurationMs))
		}
		fmt.Fprintf(&t, "%s\n", line)
		fmt.Fprintf(&h, "<p>%s</p>", esc(line))
	default:
		line := fmt.Sprintf("The run was %s: %s", r.Status, r.Reason)
		fmt.Fprintf(&t, "%s\n", line)
		fmt.Fprintf(&h, `<p style="color:#9a6700"><b>%s</b></p>`, esc(line))
	}

	if r.Alerts != nil && len(r.Alerts.Alerts) > 0 {
		t.WriteString("\nAlerts\n")
		h.WriteString(`<h3 style="font-size:16px;margin:20px 0 8px">Alerts</h3><ul style="padding-left:18px;margin:0">`)
		for _, a := range r.Alerts.Alerts {
			isNew := r.HasPrevious && !r.PrevAlerts[a.ID]
			mark := ""
			if isNew {
				mark = " (new)"
			}
			fmt.Fprintf(&t, "- [%s] %s%s: %s\n", a.Severity, a.Name, mark, a.Message)
			fmt.Fprintf(&h, `<li><span style="color:%s;font-weight:600">%s</span> %s%s: %s</li>`,
				severityColor(a.Severity), esc(a.Severity), esc(a.Name), esc(mark), esc(a.Message))
		}
		h.WriteString("</ul>")
	}

	if len(r.Changes) > 0 {
		t.WriteString("\nChanges since the previous crawl\n")
		h.WriteString(`<h3 style="font-size:16px;margin:20px 0 8px">Changes since the previous crawl</h3>`)
		for _, c := range r.Changes {
			if c.Before == "" {
				fmt.Fprintf(&t, "- %s: %s\n", c.Label, c.After)
			} else {
				fmt.Fprintf(&t, "- %s: %s -> %s\n", c.Label, c.Before, c.After)
			}
			weight := "400"
			if c.Important {
				weight = "600"
			}
			if c.Before == "" {
				fmt.Fprintf(&h, `<div style="margin:10px 0 4px;font-weight:%s">%s: %s</div>`, weight, esc(c.Label), esc(c.After))
			} else {
				fmt.Fprintf(&h, `<div style="margin:10px 0 4px;font-weight:%s">%s: %s &rarr; %s</div>`, weight, esc(c.Label), esc(c.Before), esc(c.After))
			}
			if len(c.Examples) > 0 {
				h.WriteString(`<ul style="padding-left:18px;margin:0;font-size:13px;color:#57606a">`)
				for _, u := range c.Examples {
					fmt.Fprintf(&t, "    %s\n", u)
					fmt.Fprintf(&h, `<li style="word-break:break-all">%s</li>`, esc(u))
				}
				if c.More > 0 {
					fmt.Fprintf(&t, "    … and %d more\n", c.More)
					fmt.Fprintf(&h, "<li>… and %d more</li>", c.More)
				}
				h.WriteString("</ul>")
			}
		}
	} else if r.Status == "completed" && !r.HasPrevious {
		t.WriteString("\nFirst crawl of this site: changes will show from the next one.\n")
		h.WriteString(`<p style="color:#57606a">First crawl of this site: changes will show from the next one.</p>`)
	}

	if r.Link != "" {
		fmt.Fprintf(&t, "\nOpen the crawl: %s\n", r.Link)
		fmt.Fprintf(&h, `<p style="margin-top:20px"><a href="%s" style="color:#0969da">Open the crawl in Micelio</a></p>`, esc(r.Link))
	}
	t.WriteString("\nSoft-404 and other findings are crawl-time candidates; confirm them in Search Console.\n")
	h.WriteString(`<p style="color:#57606a;font-size:12px;margin-top:24px">Soft-404 and other findings are crawl-time candidates; confirm them in Search Console.</p></div>`)
	return subject, t.String(), h.String()
}

func countCodes(codes map[int]int, from, to int) int {
	n := 0
	for code, c := range codes {
		if code >= from && code < to {
			n += c
		}
	}
	return n
}

func indexablePct(s *types.CrawlStats) float64 {
	total := s.IndexabilityStats.Indexable + s.IndexabilityStats.NonIndexable
	if total == 0 {
		return -1
	}
	return float64(s.IndexabilityStats.Indexable) / float64(total) * 100
}

func soft404Set(s *types.CrawlStats) map[string]bool {
	if s.Soft404Stats == nil {
		return nil
	}
	set := map[string]bool{}
	for _, c := range s.Soft404Stats.Candidates {
		if c.Confidence != types.Soft404Low {
			set[c.URL] = true
		}
	}
	return set
}

func toSet(urls []string) map[string]bool {
	set := make(map[string]bool, len(urls))
	for _, u := range urls {
		set[u] = true
	}
	return set
}

// diffSets returns the keys of b missing from a, sorted.
func diffSets(a, b map[string]bool) []string {
	var out []string
	for k := range b {
		if !a[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func examples(urls []string) ([]string, int) {
	if len(urls) <= maxExamples {
		return urls, 0
	}
	return urls[:maxExamples], len(urls) - maxExamples
}

func alertCount(s *types.AlertSummary) int {
	if s == nil {
		return 0
	}
	return len(s.Alerts)
}

func severityColor(s string) string {
	switch s {
	case "critical":
		return "#cf222e"
	case "warning":
		return "#9a6700"
	}
	return "#57606a"
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func fmtDuration(ms int64) string {
	m := ms / 60000
	if m >= 60 {
		return fmt.Sprintf("%dh %dm", m/60, m%60)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", ms/1000)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func pct(part, whole int) float64 {
	if whole == 0 {
		return 100
	}
	return float64(part) / float64(whole) * 100
}
