package report

import (
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/micelio/micelio/internal/csvsafe"
	"github.com/micelio/micelio/internal/types"
)

// The backlog turns crawl findings into tickets a product manager can hand
// to a team: priority, effort, owner, evidence, how to fix and when it is
// done. The PDF shows them as cards; BacklogCSV exports them for JIRA.

// Issue is one finding before it becomes a ticket.
type Issue struct {
	ID       string
	Severity string // critical, warning, notice
	Label    string
	Count    int
	Base     int // what Share is a share of (pages, images, ...)
	Share    string
	Example  string
}

// Ticket is one backlog item.
type Ticket struct {
	Key      string // SEO-1, GEO-1
	Track    string // SEO (search engines) or GEO (AI search and agents)
	ID       string // issue id
	Priority string // P0-P3
	Tone     string // bad, warn, info, ""
	Type     string // Bug or Task
	Effort   string // S, M, L
	Points   int
	Owner    string
	Title    string
	Problem  string
	Count    int
	Unit     string // "URLs", "images", ...; "" for a site-wide check
	Evidence string // what a site-wide check found (when Unit is "")
	Share    string
	Pct      float64  // bar width, 0-100
	Examples []string // shortened for the PDF
	// FullExamples are the same URLs in full (CSV), up to 10.
	FullExamples []string
	Fix          []string
	Accept       string
	View         string // Universe view that shows the problem, "" for none
	Map          *universeImage
	MapStory     string
	// Matrix position (percent of the chart), see sprintPlan.
	X, Y float64
}

// ticketSpec describes how one kind of issue reads as a ticket.
type ticketSpec struct {
	title   string // %s = count
	owner   string
	effort  string
	typ     string
	problem string
	fix     []string
	accept  string
	view    string
	unit    string
}

var ticketSpecs = map[string]ticketSpec{
	"server-errors": {"Fix %s pages that fail with a server error (5xx)", "Dev", "M", "Bug",
		"These pages crash on the server. Search engines and visitors get an error instead of content, and repeated errors make crawlers slow down.",
		[]string{"Check the server logs for the listed URLs and fix the failing code, timeout or upstream service.", "Re-crawl the URLs and confirm they answer 200."},
		"No URL answers 5xx in the next crawl.", "errors", "URLs"},
	"broken-links": {"Fix %s broken internal links", "Dev + Content", "M", "Bug",
		"Links inside the site point to pages that no longer exist. Visitors hit dead ends and crawlers spend their visits on errors.",
		[]string{"Update each link to the live page, or remove it. If the old URL has a clear successor, redirect it (301).", "Fix shared templates first (menus, footers, related items): one template link can break thousands of pages."},
		"No internal link points to a 4xx or 5xx URL in the next crawl.", "errors", "URLs"},
	"missing-title": {"Add a title to %s pages", "Content", "S", "Bug",
		"Pages without a <title> show a made-up headline in search results, or none at all.",
		[]string{"Write a unique title of 30-60 characters for each page, with its main topic first.", "If a template prints no title, fix the template once."},
		"Every indexable page has a non-empty title in the next crawl.", "", "pages"},
	"soft-404": {"Return a real 404 on %s empty pages", "Dev", "M", "Bug",
		"These pages answer 200 OK but look like 'not found' or empty listings. Search engines may index them as thin pages. These are candidates: confirm them in Search Console.",
		[]string{"Return 404 (or 410) when a page has no content, or add real content to it.", "Check the 'Page indexing' report in Search Console for 'Soft 404'."},
		"The candidates return 404/410 or have content; the soft-404 count drops in the next crawl.", "", "pages"},
	"html-over-2mb": {"Cut the HTML of %s pages below 2 MB", "Dev", "M", "Bug",
		"These HTML files are larger than 2 MB. Googlebot reads only the first 2 MB (uncompressed, headers included) and indexes that part as if it were the whole page: text, links and structured data after the cutoff are not seen.",
		[]string{"Move inline scripts, styles, SVGs and base64 images into separate files (each file has its own limit).", "Trim big inline JSON (app state, hydration data) and long repeated lists; paginate them.", "Put the main content, links and structured data near the top of the HTML."},
		"No HTML page is over 2 MB in the next crawl.", "", "pages"},
	"unreachable": {"Link to %s pages no one can reach from the home page", "SEO + Dev", "M", "Task",
		"No chain of internal links leads to these pages from the start page. Crawlers only find them through sitemaps, if at all.",
		[]string{"Decide per page: link it from a relevant hub or category, or remove it from the sitemap if it should not rank.", "Add the important ones to navigation, breadcrumbs or related-content blocks."},
		"Every page meant to rank is reachable by links from the start page in the next crawl.", "", "pages"},
	"orphans": {"Link to %s orphan pages", "SEO + Content", "M", "Task",
		"No other page on the site links to these pages. Crawlers find them only through sitemaps and they get no internal link weight.",
		[]string{"Link each page from at least one relevant page, or remove it if it is obsolete."},
		"0 orphan pages meant to rank in the next crawl.", "", "pages"},
	"deep-pages": {"Bring %s pages within 3 clicks of the home page", "SEO + Dev", "L", "Task",
		"These pages need 4 or more clicks from the start page. Deep pages get crawled less often and receive less internal link weight.",
		[]string{"Add hub pages or category links so key sections are 1-2 clicks from the home page.", "Use pagination with direct page links, breadcrumbs and related links instead of long chains."},
		"Fewer than 20% of indexable pages sit 4+ clicks deep in the next crawl.", "depth", "pages"},
	"near-orphans": {"Strengthen internal links to %s weakly linked pages", "SEO + Content", "M", "Task",
		"These pages get only one or two internal links, usually from deep pages. They receive little internal PageRank.",
		[]string{"Link them from strong pages of the same topic (bright dots on the PageRank map).", "Add related-content blocks to the template so each page gets several contextual links."},
		"Each listed page has at least 3 internal links from other pages in the next crawl.", "pagerank", "pages"},
	"redirect-links": {"Point %s internal links straight to their final URL", "Dev", "M", "Task",
		"Internal links go to URLs that redirect. Each hop costs crawl time and slows visitors down.",
		[]string{"Replace links to redirecting URLs with the final URL, starting with templates.", "Keep the redirects for outside links and bookmarks."},
		"Internal links to redirecting URLs drop by 90% in the next crawl.", "redirects", "URLs"},
	"redirect-chains": {"Collapse %s redirect chains into one hop", "Dev", "S", "Task",
		"These URLs go through several redirects before reaching a page. Crawlers may give up and link weight leaks at each hop.",
		[]string{"Redirect each first URL straight to the final one.", "Update internal links to the final URL."},
		"No redirect chain longer than 1 hop in the next crawl.", "redirects", "URLs"},
	"missing-desc": {"Write meta descriptions for %s pages", "Content", "M", "Task",
		"Without a description, search engines pick a snippet from the page, which often reads badly and lowers clicks.",
		[]string{"Write a 120-155 character description that says what the page offers and why to click.", "For large page sets, generate them from a template with the page's own data."},
		"Every indexable page has a meta description in the next crawl.", "", "pages"},
	"missing-h1": {"Add a main heading (H1) to %s pages", "Content + Dev", "S", "Task",
		"Pages without an H1 lack a clear main heading for readers and for search engines.",
		[]string{"Add one H1 per page that matches the title's topic; fix the template if it lacks one."},
		"Every indexable page has exactly one H1 in the next crawl.", "", "pages"},
	"dup-titles": {"Make %s duplicate titles unique", "Content", "L", "Task",
		"Several pages share the same title, so search results cannot tell them apart and the pages compete with each other.",
		[]string{"Add the distinguishing detail (place, product, page number) to each title.", "If pages are true duplicates, keep one and canonicalise or redirect the rest."},
		"No two indexable pages share a title in the next crawl.", "", "pages"},
	"thin": {"Expand or merge %s thin pages", "Content", "L", "Task",
		"These pages have very little text. Thin pages rarely rank and can drag down how a site is judged overall.",
		[]string{"Add useful content where the page should rank, merge near-empty pages, or noindex them."},
		"The thin-content count drops by half in the next crawl.", "", "pages"},
	"non-https": {"Move %s pages to HTTPS", "Dev", "S", "Bug",
		"These pages load without encryption. Browsers mark them 'Not secure'.",
		[]string{"Redirect HTTP to HTTPS (301) and update internal links."}, "No page is served over HTTP in the next crawl.", "", "pages"},
	"mixed": {"Fix mixed content on %s pages", "Dev", "S", "Bug",
		"Secure pages load some files over plain HTTP. Browsers block or warn about them.",
		[]string{"Load every image, script and style over HTTPS."}, "0 pages with mixed content in the next crawl.", "", "pages"},
	"canonical-non200": {"Fix %s canonicals that point to broken or redirected URLs", "Dev", "S", "Bug",
		"The canonical tag tells search engines which URL to index. Here it points to a URL that does not answer 200, so the signal is ignored.",
		[]string{"Point each canonical to the live, final URL of the page."}, "Every canonical points to a URL that answers 200.", "", "pages"},
	"broken-external": {"Fix %s broken outbound links", "Content", "S", "Task",
		"Links to other sites lead to errors, which hurts trust for readers.",
		[]string{"Update or remove each broken outbound link."}, "0 broken external links in the next crawl.", "", "links"},
	"dup-desc": {"Make %s duplicate meta descriptions unique", "Content", "M", "Task",
		"Many pages share the same description, so their search snippets look identical.",
		[]string{"Generate descriptions from each page's own data, or write them for key pages."}, "Duplicate descriptions drop by 80% in the next crawl.", "", "pages"},
	"multi-h1": {"Keep one H1 on %s pages", "Dev", "S", "Task",
		"Several H1 headings blur what the page is mainly about.",
		[]string{"Keep one H1 and turn the others into H2."}, "Every page has exactly one H1.", "", "pages"},
	"title-long": {"Shorten %s titles that get cut in search results", "Content", "M", "Task",
		"Long titles are cut off in search results, often losing the part that makes people click.",
		[]string{"Keep titles under about 60 characters with the key words first."}, "Fewer than 5% of titles exceed 60 characters.", "", "pages"},
	"img-alt": {"Add alt text to %s images", "Content", "M", "Task",
		"Images without alt text are invisible to screen readers and to image search.",
		[]string{"Describe each meaningful image in a short alt text; use alt=\"\" for decorative ones.", "Fix the image component so it always prints an alt attribute."},
		"Fewer than 5% of images lack alt text in the next crawl.", "", "images"},
	"no-og": {"Add social sharing tags to %s pages", "Dev", "S", "Task",
		"Without Open Graph tags, links shared on social networks show no proper title or image.",
		[]string{"Add og:title, og:description and og:image to the page templates."}, "Every indexable page has og:title and og:image.", "", "pages"},
}

// singularRe finds the noun after "1 " (up to two words in between:
// "1 broken internal links").
var singularRe = regexp.MustCompile(`\b1 ((?:[a-z-]+ ){0,2}?)(pages|URLs|links|images|titles|descriptions|chains|canonicals)\b`)

// countTitle fills a ticket title with its count and reads right for one:
// "Add a title to 1 page", "Fix 1 page that fails with a server error".
func countTitle(format string, n int) string {
	t := fmt.Sprintf(format, commaFormat(n))
	if n != 1 {
		return t
	}
	for _, r := range [][2]string{{"that fail ", "that fails "}, {"that get cut", "that gets cut"}, {"that point ", "that points "}} {
		t = strings.Replace(t, r[0], r[1], 1)
	}
	return singularRe.ReplaceAllStringFunc(t, func(m string) string {
		return strings.TrimSuffix(m, "s")
	})
}

// plural writes "1 ticket" / "3 tickets".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return commaFormat(n) + " " + word + "s"
}

// unitOf is the unit of a count, singular for one ("1 page").
func unitOf(n int, unit string) string {
	if n == 1 {
		return strings.TrimSuffix(unit, "s")
	}
	return unit
}

// buildIssues lists the crawl's findings, worst first.
func buildIssues(s *types.CrawlStats, total int, t totals) []Issue {
	var out []Issue
	add := func(id, sev, label string, n, base int, example string) {
		if n <= 0 {
			return
		}
		if base <= 0 {
			base = total
		}
		out = append(out, Issue{ID: id, Severity: sev, Label: label, Count: n, Base: base, Share: share(n, base), Example: shortURL(example)})
	}
	first := func(list []string) string {
		if len(list) > 0 {
			return list[0]
		}
		return ""
	}
	var brokenEx string
	if len(s.BrokenLinks) > 0 {
		brokenEx = s.BrokenLinks[0].URL
	}
	add("server-errors", "critical", "Server errors (5xx)", classCount(s.StatusCodes, 5), 0, "")
	// A 4xx page is reported once: as a broken link when the site links to
	// it (it almost always does), else as a client error.
	broken := max(t.broken(), classCount(s.StatusCodes, 4))
	add("broken-links", "critical", "Broken internal links (4xx)", broken, 0, brokenEx)
	add("missing-title", "critical", "Pages without a title", s.PagesWithoutTitle, 0, "")
	var bigHTMLEx string
	if len(s.HTMLOver2MBPages) > 0 {
		bigHTMLEx = s.HTMLOver2MBPages[0].URL
	}
	add("html-over-2mb", "critical", "HTML over 2 MB (Googlebot stops reading)", max(s.TotalHTMLOver2MBPages, len(s.HTMLOver2MBPages)), 0, bigHTMLEx)
	if s.Soft404Stats != nil {
		add("soft-404", "warning", "Soft 404 candidates", s.Soft404Stats.ByConfidence["high"]+s.Soft404Stats.ByConfidence["medium"], 0, first(s.Soft404Pages))
	}
	if li := s.LinkIntelligenceStats; li != nil {
		add("unreachable", "warning", "Pages not reachable from the start page", max(li.UnreachablePagesCount, len(li.UnreachablePages)), 0, first(li.UnreachablePages))
		if deep, all := deepPages(li.ClickDepthDistribution); all > 0 && deep*20 >= all {
			add("deep-pages", "warning", "Pages 4+ clicks deep", deep, all, "")
		}
		var nearEx string
		if len(li.NearOrphans) > 0 {
			nearEx = li.NearOrphans[0].URL
		}
		add("near-orphans", "warning", "Weakly linked pages", max(li.NearOrphansCount, len(li.NearOrphans)), 0, nearEx)
	}
	add("orphans", "warning", "Orphan pages (no internal links in)", t.orphans(), 0, first(s.OrphanPages))
	if r := redirectCount(s); r > 0 && r*50 >= total {
		add("redirect-links", "warning", "Internal links to redirects", r, total+r, "")
	}
	var chainEx string
	if len(s.LongRedirectChains) > 0 {
		chainEx = s.LongRedirectChains[0].URL
	}
	add("redirect-chains", "warning", "Long redirect chains", t.get("totalLongRedirectChains", len(s.LongRedirectChains)), 0, chainEx)
	add("missing-desc", "warning", "Missing meta description", s.PagesWithoutDescription, 0, "")
	add("missing-h1", "warning", "Missing H1", s.PagesWithoutH1, 0, "")
	add("dup-titles", "warning", "Duplicate titles", s.DuplicateTitleCount, 0, "")
	add("thin", "warning", "Thin content", t.thin(), 0, firstWord(s.ThinContentPages))
	add("non-https", "warning", "Non-HTTPS pages", t.get("totalNonHttpsPages", len(s.NonHTTPSPages)), 0, first(s.NonHTTPSPages))
	add("mixed", "warning", "Mixed content", len(s.MixedContentPages), 0, first(s.MixedContentPages))
	if cs := s.CanonicalStats; cs != nil {
		add("canonical-non200", "warning", "Canonical points to a non-200 URL", len(cs.CanonicalToNon200), 0, "")
	}
	add("broken-external", "notice", "Broken external links", len(s.BrokenExternalLinks), max(s.ExternalLinksChecked, len(s.BrokenExternalLinks)), "")
	add("dup-desc", "notice", "Duplicate meta descriptions", s.DuplicateDescriptionCount, 0, "")
	add("multi-h1", "notice", "Multiple H1", s.MultipleH1Count, 0, "")
	add("title-long", "notice", "Title too long", s.TitleTooLongCount, 0, "")
	add("img-alt", "notice", "Images without alt text", s.ImagesMissingAlt, max(s.TotalImages, s.ImagesMissingAlt), "")
	add("no-og", "notice", "Pages without Open Graph", s.PagesWithoutOG, 0, "")

	rank := map[string]int{"critical": 0, "warning": 1, "notice": 2}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := priorityOf(out[i]), priorityOf(out[j])
		if pi != pj {
			return pi < pj
		}
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		return out[i].Count > out[j].Count
	})
	return out
}

// deepPages counts pages 4+ clicks from the start page.
func deepPages(dist map[int]int) (deep, all int) {
	for d, n := range dist {
		all += n
		if d >= 4 {
			deep += n
		}
	}
	return deep, all
}

// redirectCount is the number of crawled URLs that redirect. Stored status
// codes are those of the final page, so redirect stats are the real count.
func redirectCount(s *types.CrawlStats) int {
	n := classCount(s.StatusCodes, 3)
	if r := s.RedirectStats; r != nil {
		m := 0
		for _, c := range r.ByStatusCode {
			m += c
		}
		n = max(n, m)
	}
	return n
}

// priorityOf ranks an issue 0 (P0) to 3 (P3) by severity and reach.
func priorityOf(is Issue) int {
	f := float64(is.Count) / float64(max(1, is.Base))
	switch is.Severity {
	case "critical":
		if f >= .01 || is.Count >= 10 {
			return 0
		}
		return 1
	case "warning":
		if f >= .10 {
			return 1
		}
		return 2
	}
	if f >= .25 {
		return 2
	}
	return 3
}

// buildBacklog turns issues into tickets, most urgent first.
func buildBacklog(issues []Issue, s *types.CrawlStats, pages []*types.PageData, seedURL string) []Ticket {
	var out []Ticket
	for _, is := range issues {
		spec, ok := ticketSpecs[is.ID]
		if !ok {
			continue
		}
		p := priorityOf(is)
		effort := spec.effort
		if is.Count > 2000 && effort == "S" {
			effort = "M"
		}
		tk := Ticket{
			Key:      fmt.Sprintf("SEO-%d", len(out)+1),
			Track:    "SEO",
			ID:       is.ID,
			Priority: fmt.Sprintf("P%d", p),
			Tone:     []string{"bad", "warn", "info", ""}[p],
			Type:     spec.typ,
			Effort:   effort,
			Points:   map[string]int{"S": 2, "M": 3, "L": 5}[effort],
			Owner:    spec.owner,
			Title:    countTitle(spec.title, is.Count),
			Problem:  spec.problem,
			Count:    is.Count,
			Unit:     spec.unit,
			Share:    is.Share,
			Pct:      100 * float64(is.Count) / float64(max(1, is.Base)),
			Fix:      spec.fix,
			Accept:   spec.accept,
			View:     spec.view,
		}
		tk.Pct = min(tk.Pct, 100)
		affected := affectedURLs(is.ID, s, pages)
		for _, u := range affected {
			if len(tk.FullExamples) == 10 {
				break
			}
			if len(tk.Examples) < 3 {
				tk.Examples = append(tk.Examples, shortURL(u))
			}
			tk.FullExamples = append(tk.FullExamples, u)
		}
		if len(tk.Examples) == 0 && is.Example != "" {
			tk.Examples = []string{is.Example}
		}
		tk.MapStory = sectionStory(is.ID, affected, len(affected), seedURL)
		out = append(out, tk)
	}
	// Only the first ticket per view gets the map: one map, one point. A
	// handful of URLs would not show on a map of the whole site.
	seen := map[string]bool{}
	for i := range out {
		if out[i].Count < 25 {
			out[i].View = ""
		}
		if v := out[i].View; v != "" {
			if seen[v] {
				out[i].View = ""
			} else {
				seen[v] = true
			}
		}
	}
	return out
}

// affectedURLs lists the URLs behind an issue, from stats lists or pages.
func affectedURLs(id string, s *types.CrawlStats, pages []*types.PageData) []string {
	var out []string
	fromPages := func(match func(p *types.PageData) bool) []string {
		var list []string
		for _, p := range pages {
			if p != nil && match(p) {
				list = append(list, p.URL)
			}
		}
		return list
	}
	ok := func(p *types.PageData) bool { return p.IsHTMLPage() } // 2xx HTML, not blocked
	switch id {
	case "server-errors":
		out = fromPages(func(p *types.PageData) bool { return p.StatusCode >= 500 })
	case "broken-links":
		out = fromPages(func(p *types.PageData) bool { return p.StatusCode >= 400 && p.StatusCode < 500 })
		if len(out) == 0 {
			for _, b := range s.BrokenLinks {
				out = append(out, b.URL)
			}
		}
	case "missing-title":
		out = fromPages(func(p *types.PageData) bool {
			return ok(p) && (p.Title == nil || strings.TrimSpace(p.Title.Text) == "")
		})
	case "missing-desc":
		out = fromPages(func(p *types.PageData) bool {
			return ok(p) && (p.MetaDescription == nil || strings.TrimSpace(p.MetaDescription.Text) == "")
		})
	case "missing-h1":
		out = fromPages(func(p *types.PageData) bool { return ok(p) && len(p.Headings.H1) == 0 })
	case "multi-h1":
		out = fromPages(func(p *types.PageData) bool { return ok(p) && len(p.Headings.H1) > 1 })
	case "title-long":
		out = fromPages(func(p *types.PageData) bool { return ok(p) && p.Title != nil && p.Title.Length > 60 })
	case "dup-titles":
		groups := map[string][]string{}
		for _, p := range pages {
			if p != nil && ok(p) && p.Title != nil && p.Title.Text != "" {
				groups[p.Title.Text] = append(groups[p.Title.Text], p.URL)
			}
		}
		var best []string
		for _, g := range groups {
			if len(g) > len(best) {
				best = g
			}
		}
		if len(best) > 1 {
			out = best
		}
	case "soft-404":
		out = s.Soft404Pages
	case "unreachable":
		if s.LinkIntelligenceStats != nil {
			out = s.LinkIntelligenceStats.UnreachablePages
		}
	case "orphans":
		out = s.OrphanPages
	case "near-orphans":
		if s.LinkIntelligenceStats != nil {
			for _, n := range s.LinkIntelligenceStats.NearOrphans {
				out = append(out, n.URL)
			}
		}
	case "deep-pages":
		type dp struct {
			url   string
			depth int
		}
		var list []dp
		for _, p := range pages {
			if p != nil && p.LinkIntelligence != nil && p.LinkIntelligence.ClickDepth != nil && *p.LinkIntelligence.ClickDepth >= 4 {
				list = append(list, dp{p.URL, *p.LinkIntelligence.ClickDepth})
			}
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].depth > list[j].depth })
		for _, d := range list {
			out = append(out, d.url)
		}
	case "redirect-links":
		out = fromPages(func(p *types.PageData) bool { return len(p.RedirectChain) > 0 })
		if len(out) == 0 && s.RedirectStats != nil {
			for _, r := range s.RedirectStats.TemporaryRedirects {
				out = append(out, r.URL)
			}
		}
	case "redirect-chains":
		for _, r := range s.LongRedirectChains {
			out = append(out, r.URL)
		}
	case "html-over-2mb":
		for _, h := range s.HTMLOver2MBPages {
			out = append(out, h.URL)
		}
	case "thin":
		for _, w := range s.ThinContentPages {
			out = append(out, w.URL)
		}
	case "non-https":
		out = s.NonHTTPSPages
	case "mixed":
		out = s.MixedContentPages
	case "canonical-non200":
		if s.CanonicalStats != nil {
			for _, c := range s.CanonicalStats.CanonicalToNon200 {
				out = append(out, c.URL)
			}
		}
	case "broken-external":
		for _, b := range s.BrokenExternalLinks {
			out = append(out, b.URL)
		}
	}
	return out
}

// sectionStory says where an issue concentrates, e.g. "61% of the 1,028
// affected URLs sit under /es/petsitter/." Empty when it is spread out.
func sectionStory(id string, urls []string, n int, seedURL string) string {
	if n < 10 {
		return ""
	}
	folder, count := topSection(urls, seedURL)
	if folder == "" || count*100 < n*25 {
		return ""
	}
	return fmt.Sprintf("%s of the %s affected URLs sit under %s.", share(count, n), commaFormat(n), folder)
}

// topSection is the folder (one level below the start URL's folder) with
// the most of the given URLs.
func topSection(urls []string, seedURL string) (string, int) {
	baseDepth := 0
	if u, err := url.Parse(seedURL); err == nil {
		baseDepth = len(splitPath(u.Path))
	}
	counts := map[string]int{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		parts := splitPath(u.Path)
		if len(parts) <= baseDepth+1 {
			continue // a page directly in the start folder, not a section
		}
		counts["/"+strings.Join(parts[:baseDepth+1], "/")+"/"]++
	}
	best, bestN := "", 0
	for f, c := range counts {
		if c > bestN || (c == bestN && f < best) {
			best, bestN = f, c
		}
	}
	return best, bestN
}

func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SprintPlan groups tickets on a board and places them on the
// impact × effort chart.
type SprintPlan struct {
	Now, Next, Later []Ticket
	Points           [3]int
}

func sprintPlan(tickets []Ticket) *SprintPlan {
	if len(tickets) == 0 {
		return nil
	}
	sp := &SprintPlan{}
	for _, t := range tickets {
		switch {
		case t.Priority == "P0" || (t.Priority == "P1" && t.Effort == "S"):
			sp.Now = append(sp.Now, t)
			sp.Points[0] += t.Points
		case t.Priority == "P1":
			sp.Next = append(sp.Next, t)
			sp.Points[1] += t.Points
		default:
			sp.Later = append(sp.Later, t)
			sp.Points[2] += t.Points
		}
	}
	return sp
}

// placeOnMatrix sets each ticket's position on the 2×2 chart: effort left
// to right (S in the left half), impact top to bottom (P0-P1 in the top
// half). Tickets in the same band are spread so labels do not overlap.
func placeOnMatrix(tickets []Ticket) {
	xs := map[string][2]float64{"S": {4, 44}, "M": {54, 74}, "L": {76, 92}}
	ys := [4][2]float64{{9, 20}, {27, 42}, {58, 70}, {78, 90}}
	cell := map[string]int{}
	for i := range tickets {
		t := &tickets[i]
		row := int(t.Priority[1] - '0')
		key := t.Effort + t.Priority
		k := cell[key]
		cell[key]++
		x, y := xs[t.Effort], ys[row]
		perRow := max(1, int((x[1]-x[0])/11))
		t.X = x[0] + float64(k%perRow)*11
		t.Y = y[0] + float64(k/perRow)*7
		t.Y = min(t.Y, y[1])
	}
}

// BacklogCSV writes the backlog in a shape JIRA's CSV import maps directly
// (Summary, Issue Type, Priority, Story Points, Labels, Description).
func BacklogCSV(w io.Writer, seedURL string, stats *types.CrawlStats, pages []*types.PageData, opts PDFOptions) error {
	d := BuildPDFData(seedURL, stats, pages, opts)
	cw := csvsafe.NewWriter(w)
	cw.Write([]string{"Issue key", "Summary", "Issue Type", "Priority", "Story Points", "Component", "Labels", "Description"})
	jira := map[string]string{"P0": "Highest", "P1": "High", "P2": "Medium", "P3": "Low"}
	for _, t := range append(append([]Ticket{}, d.Tickets...), d.GEO...) {
		var b strings.Builder
		fmt.Fprintf(&b, "h3. Problem\n%s\n\n", t.Problem)
		if t.Unit != "" {
			fmt.Fprintf(&b, "h3. Evidence\n%s %s (%s) on %s, crawl of %s.\n", commaFormat(t.Count), unitOf(t.Count, t.Unit), t.Share, d.Domain, d.Date)
		} else {
			fmt.Fprintf(&b, "h3. Evidence\n%s (%s, crawl of %s).\n", t.Evidence, d.Domain, d.Date)
		}
		if t.MapStory != "" {
			fmt.Fprintf(&b, "%s\n", t.MapStory)
		}
		for _, e := range t.FullExamples {
			fmt.Fprintf(&b, "* %s\n", e)
		}
		b.WriteString("\nh3. How to fix\n")
		for _, f := range t.Fix {
			fmt.Fprintf(&b, "# %s\n", f)
		}
		fmt.Fprintf(&b, "\nh3. Acceptance criteria\n* %s\n\nOwner: %s · Effort: %s", t.Accept, t.Owner, t.Effort)
		if t.Track == "GEO" {
			b.WriteString("\n\nGEO signals are crawl-time proxies: they show whether AI systems can reach and read the site, not whether they cite it.")
		}
		labels := []string{strings.ToLower(t.Track), "micelio", "owner-" + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(t.Owner, " + ", "-"), " ", "-"))}
		cw.Write([]string{t.Key, t.Title, t.Type, jira[t.Priority], fmt.Sprint(t.Points), t.Track, strings.Join(labels, " "), b.String()})
	}
	cw.Flush()
	return cw.Error()
}

// BacklogViews lists the Universe views the backlog's tickets need, in
// ticket order: "errors", "redirects", "depth", "pagerank".
func BacklogViews(seedURL string, stats *types.CrawlStats, pages []*types.PageData, opts PDFOptions) []string {
	opts.Universe = nil
	var out []string
	for _, t := range BuildPDFData(seedURL, stats, pages, opts).Tickets {
		if t.View != "" && len(out) < 4 {
			out = append(out, t.View)
		}
	}
	return out
}
