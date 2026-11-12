package report

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func backlogFixture() (*types.CrawlStats, []*types.PageData) {
	depth := func(n int) *types.LinkIntelligenceData { return &types.LinkIntelligenceData{ClickDepth: &n} }
	s := &types.CrawlStats{
		TotalPages:        200,
		StatusCodes:       map[int]int{200: 170, 404: 30},
		IndexabilityStats: types.IndexabilityStats{Indexable: 170},
		PagesWithoutTitle: 1,
		TotalImages:       1000,
		ImagesMissingAlt:  300,
		RedirectStats:     &types.RedirectStats{ByStatusCode: map[int]int{301: 30, 308: 10}},
		LinkIntelligenceStats: &types.LinkIntelligenceStats{
			ClickDepthDistribution: map[int]int{0: 1, 1: 20, 2: 40, 3: 39, 4: 60, 5: 40},
		},
	}
	var pages []*types.PageData
	for i := 0; i < 30; i++ {
		pages = append(pages, &types.PageData{URL: "https://ex.com/shop/gone-" + string(rune('a'+i)), StatusCode: 404})
	}
	pages = append(pages,
		&types.PageData{URL: "https://ex.com/blog/x", StatusCode: 200, LinkIntelligence: depth(5), Title: &types.TextLength{Text: "X"}},
		&types.PageData{URL: "https://ex.com/no-title", StatusCode: 200, LinkIntelligence: depth(1)},
	)
	return s, pages
}

func TestBacklogTickets(t *testing.T) {
	s, pages := backlogFixture()
	d := BuildPDFData("https://ex.com/", s, pages, PDFOptions{})

	byID := map[string]Ticket{}
	for i, tk := range d.Tickets {
		byID[tk.ID] = tk
		if want := "SEO-" + string(rune('1'+i)); i < 9 && tk.Key != want {
			t.Errorf("ticket %d key %q, want %q", i, tk.Key, want)
		}
		if tk.Points == 0 || tk.Owner == "" || tk.Accept == "" || len(tk.Fix) == 0 {
			t.Errorf("%s incomplete: %+v", tk.ID, tk)
		}
		if tk.X < 0 || tk.X > 100 || tk.Y < 0 || tk.Y > 100 {
			t.Errorf("%s off the matrix: %.0f,%.0f", tk.ID, tk.X, tk.Y)
		}
	}
	// 4xx pages and broken links are one ticket, P0, with real examples and
	// a story: every error sits under /shop/.
	b, ok := byID["broken-links"]
	if !ok || b.Priority != "P0" || b.Count != 30 || len(b.Examples) != 3 || len(b.FullExamples) != 10 {
		t.Fatalf("broken links ticket %+v", b)
	}
	if !strings.Contains(b.MapStory, "/shop/") {
		t.Errorf("story %q", b.MapStory)
	}
	if d.Tickets[0].ID != "broken-links" {
		t.Errorf("first ticket %s", d.Tickets[0].ID)
	}
	// Alt text is a share of images, not pages.
	if a := byID["img-alt"]; a.Share != "30%" || a.Unit != "images" {
		t.Errorf("alt ticket share %q unit %q", a.Share, a.Unit)
	}
	// Redirects come from redirect stats (status codes hold final pages).
	if r := byID["redirect-links"]; r.Count != 40 {
		t.Errorf("redirect ticket %+v", r)
	}
	// Deep pages use click depth: 100 of 200 pages are 4+ clicks deep.
	if dp := byID["deep-pages"]; dp.Count != 100 || dp.View != "depth" || dp.Examples[0] != "/blog/x" {
		t.Errorf("deep ticket %+v", dp)
	}
	if mt := byID["missing-title"]; len(mt.Examples) != 1 || mt.Examples[0] != "/no-title" {
		t.Errorf("missing title examples %v", mt.Examples)
	}
	if d.Depth[0].Count != 1 || d.Depth[4].Count != 60 {
		t.Errorf("depth chart not from click depth: %+v", d.Depth)
	}
	// One map per view, and views in ticket order.
	if v := BacklogViews("https://ex.com/", s, pages, PDFOptions{}); strings.Join(v, ",") != "errors,depth,redirects" {
		t.Errorf("views %v", v)
	}
	if d.Plan == nil || len(d.Plan.Now) == 0 || d.Plan.Now[0].ID != "broken-links" {
		t.Errorf("plan %+v", d.Plan)
	}
}

func TestBacklogMapsAndCards(t *testing.T) {
	s, pages := backlogFixture()
	shots := []UniverseShot{
		{Key: "structure", Title: "Link structure", JPEG: []byte{1}, Cover: []byte{2}, Meta: &UniverseMeta{Pages: 100, Clusters: 4, Top: []UniverseCluster{{Label: "/shop/", Count: 60}}}},
		{Key: "errors", Title: "Errors on the map", JPEG: []byte{3}},
	}
	d := BuildPDFData("https://ex.com/", s, pages, PDFOptions{Universe: shots})
	if d.Hero == nil || !strings.Contains(d.HeroStory, "/shop/, holds 60%") {
		t.Errorf("hero story %q", d.HeroStory)
	}
	if d.Tickets[0].Map == nil {
		t.Error("errors map not on the broken links ticket")
	}
	// A ticket with a map gets its own sheet.
	if len(d.Cards) == 0 || len(d.Cards[0]) != 1 || d.Cards[0][0].ID != "broken-links" {
		t.Errorf("cards %v", d.Cards)
	}
	var buf bytes.Buffer
	if err := GeneratePrintHTML(&buf, "https://ex.com/", s, pages, PDFOptions{Universe: shots}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"What to fix, as tickets", "Where to start", "Quick wins", "Done when", "Errors on the map", "SEO-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(out, `<section class="sheet`); n != d.Sheets {
		t.Errorf("rendered %d sheets, data says %d", n, d.Sheets)
	}
}

func TestBacklogCSV(t *testing.T) {
	s, pages := backlogFixture()
	var buf bytes.Buffer
	if err := BacklogCSV(&buf, "https://ex.com/", s, pages, PDFOptions{}); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rows[0], "|") != "Issue key|Summary|Issue Type|Priority|Story Points|Component|Labels|Description" {
		t.Fatalf("header %v", rows[0])
	}
	first := rows[1]
	if first[0] != "SEO-1" || first[1] != "Fix 30 broken internal links" || first[2] != "Bug" || first[3] != "Highest" {
		t.Errorf("first row %v", first[:4])
	}
	if first[5] != "SEO" || !strings.Contains(first[6], "seo") || !strings.Contains(first[6], "owner-dev-content") {
		t.Errorf("component %q labels %q", first[5], first[6])
	}
	for _, want := range []string{"h3. Problem", "h3. How to fix", "h3. Acceptance criteria", "https://ex.com/shop/gone-a"} {
		if !strings.Contains(first[7], want) {
			t.Errorf("description misses %q", want)
		}
	}
}

func TestShareRounding(t *testing.T) {
	if got := share(9996, 10000); got != ">99%" {
		t.Errorf("share = %q", got)
	}
	if got := share(1, 1000); got != "<1%" {
		t.Errorf("share = %q", got)
	}
}

func geoFixture() *types.CrawlStats {
	no, yes := false, true
	s, _ := backlogFixture()
	s.PagesWithStructuredData = 20
	s.AIReadinessStats = &types.AIReadinessStats{PagesWithConciseDefinition: 150, PagesWithStructuredAnswer: 5}
	s.AgentReadiness = &types.AgentReadinessStats{Score: 38, Level: "Basic", Hosts: []types.AgentHostReport{{
		Host: "ex.com",
		Checks: []types.AgentCheck{
			{ID: "ai-agents-access", Status: types.AgentFail, Title: "AI search and user agents may read the site"},
			{ID: "llms-txt", Status: types.AgentWarn, Title: "llms.txt", Evidence: "/llms.txt answers 404"},
			{ID: "security-txt", Status: types.AgentWarn},    // not a GEO ticket
			{ID: "structured-data", Status: types.AgentPass}, // passes: no ticket
			{ID: "mcp-server-card", Status: types.AgentNA},
		},
		Bots: []types.AgentBotAccess{
			{Bot: "GPTBot", Owner: "OpenAI", Purpose: "training", Allowed: &no},
			{Bot: "OAI-SearchBot", Owner: "OpenAI", Purpose: "search", Allowed: &no},
			{Bot: "ClaudeBot", Owner: "Anthropic", Purpose: "training", Allowed: &yes, LiveStatus: 403},
		},
		Categories: []types.AgentCategoryScore{{Category: "access", Score: 20, Fail: 1}, {Category: "protocols", Score: -1}},
	}}}
	return s
}

func TestGEOBacklog(t *testing.T) {
	s := geoFixture()
	_, pages := backlogFixture()
	d := BuildPDFData("https://ex.com/", s, pages, PDFOptions{})
	var titles []string
	for i, g := range d.GEO {
		titles = append(titles, g.Title)
		if g.Track != "GEO" || g.Key != "GEO-"+string(rune('1'+i)) {
			t.Errorf("ticket %d: track %q key %q", i, g.Track, g.Key)
		}
	}
	got := strings.Join(titles, " | ")
	for _, want := range []string{"Let AI search and assistant bots read the site", "Publish an llms.txt index", "Add JSON-LD to 180 pages", "Add answer-ready blocks (lists, tables, steps) to 195 pages"} {
		if !strings.Contains(got, want) {
			t.Errorf("GEO tickets miss %q: %s", want, got)
		}
	}
	if strings.Contains(got, "security") || strings.Contains(got, "one-paragraph") {
		t.Errorf("unexpected GEO ticket: %s", got)
	}
	first := d.GEO[0]
	if first.Priority != "P0" || first.Unit != "" || first.Evidence != "Blocked: OAI-SearchBot" {
		t.Errorf("first GEO ticket %+v", first)
	}
	// A warning ranks one level below a failure: llms.txt P1 → P2.
	for _, g := range d.GEO {
		if strings.HasPrefix(g.Title, "Publish an llms.txt") && g.Priority != "P2" {
			t.Errorf("llms.txt priority %s", g.Priority)
		}
	}
	if v := d.GEOView; v == nil || v.Score != 38 || len(v.Bots) != 3 || v.Bots[1].State != "blocked" || len(v.Categories) != 1 {
		t.Errorf("GEO view %+v", v)
	}
	if b := d.GEOView.Bots[2]; b.State != "refused" || b.Note != "403 to our test" {
		t.Errorf("robots.txt allows ClaudeBot but the server refuses it: %+v", b)
	}
	for _, c := range d.Checks {
		if strings.Contains(c.Name, "agent") {
			t.Error("agent readiness still in Beyond the basics")
		}
	}

	var buf bytes.Buffer
	if err := GeneratePrintHTML(&buf, "https://ex.com/", s, pages, PDFOptions{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"Ready for AI search?", "OAI-SearchBot", "AI search and agents", "GEO-1", "Site-wide check", "crawl-time proxies"} {
		if !strings.Contains(out, want) {
			t.Errorf("PDF misses %q", want)
		}
	}
	if n := strings.Count(out, `<section class="sheet`); n != d.Sheets {
		t.Errorf("rendered %d sheets, data says %d", n, d.Sheets)
	}

	buf.Reset()
	if err := BacklogCSV(&buf, "https://ex.com/", s, pages, PDFOptions{}); err != nil {
		t.Fatal(err)
	}
	rows, _ := csv.NewReader(&buf).ReadAll()
	var geoRow []string
	for _, r := range rows {
		if r[0] == "GEO-1" {
			geoRow = r
		}
	}
	if geoRow == nil || geoRow[5] != "GEO" || !strings.Contains(geoRow[6], "geo") || !strings.Contains(geoRow[7], "Blocked: OAI-SearchBot") || !strings.Contains(geoRow[7], "crawl-time proxies") {
		t.Errorf("GEO csv row %v", geoRow)
	}
}

func TestBacklogHTMLOver2MB(t *testing.T) {
	s, pages := backlogFixture()
	s.HTMLOver2MBPages = []types.HTMLSizeEntry{{URL: "https://ex.com/shop/big", Bytes: 3 << 20}}
	s.TotalHTMLOver2MBPages = 4
	d := BuildPDFData("https://ex.com/", s, pages, PDFOptions{})
	for _, tk := range d.Tickets {
		if tk.ID == "html-over-2mb" {
			if tk.Count != 4 || tk.Type != "Bug" || len(tk.Examples) == 0 || tk.Examples[0] != "/shop/big" {
				t.Errorf("ticket %+v", tk)
			}
			return
		}
	}
	t.Fatal("no html-over-2mb ticket")
}

func TestCountTitleSingular(t *testing.T) {
	for _, c := range []struct {
		format string
		n      int
		want   string
	}{
		{"Add a title to %s pages", 1, "Add a title to 1 page"},
		{"Add a title to %s pages", 2, "Add a title to 2 pages"},
		{"Fix %s broken internal links", 1, "Fix 1 broken internal link"},
		{"Fix %s pages that fail with a server error (5xx)", 1, "Fix 1 page that fails with a server error (5xx)"},
		{"Shorten %s titles that get cut in search results", 1, "Shorten 1 title that gets cut in search results"},
		{"Fix %s canonicals that point to broken or redirected URLs", 1, "Fix 1 canonical that points to broken or redirected URLs"},
		{"Make %s duplicate meta descriptions unique", 1, "Make 1 duplicate meta description unique"},
		{"Strengthen internal links to %s weakly linked pages", 1, "Strengthen internal links to 1 weakly linked page"},
		{"Collapse %s redirect chains into one hop", 1, "Collapse 1 redirect chain into one hop"},
		{"Add alt text to %s images", 11, "Add alt text to 11 images"},
		{"Fix %s broken internal links", 1001, "Fix 1,001 broken internal links"},
	} {
		if got := countTitle(c.format, c.n); got != c.want {
			t.Errorf("%q, %d: got %q, want %q", c.format, c.n, got, c.want)
		}
	}
	// Every spec reads right for one.
	for id, spec := range ticketSpecs {
		if got := countTitle(spec.title, 1); strings.Contains(got, " 1 pages") || strings.Contains(got, " 1 URLs") || strings.Contains(got, " 1 images") {
			t.Errorf("%s: %q", id, got)
		}
	}
	if plural(1, "ticket") != "1 ticket" || plural(3, "ticket") != "3 tickets" || unitOf(1, "pages") != "page" {
		t.Error("plural helpers")
	}
}
