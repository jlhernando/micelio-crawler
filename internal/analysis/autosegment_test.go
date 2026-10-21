package analysis

import (
	"fmt"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/micelio/micelio/internal/types"
)

func TestDirLevels(t *testing.T) {
	cases := []struct {
		url  string
		want [3]string
	}{
		{"https://ex.com/", [3]string{"/", "/", "/"}},
		{"https://ex.com/about", [3]string{"/", "/", "/"}},
		{"https://ex.com/blog/", [3]string{"/blog/", "/blog/", "/blog/"}},
		{"https://ex.com/blog/post-1", [3]string{"/blog/", "/blog/", "/blog/"}},
		{"https://ex.com/shop/men/shoes/runner-x", [3]string{"/shop/", "/shop/men/", "/shop/men/shoes/"}},
		{"https://ex.com/a/b/c/d/e/", [3]string{"/a/", "/a/b/", "/a/b/c/"}},
	}
	for _, c := range cases {
		if got := dirLevels(c.url); got != c.want {
			t.Errorf("dirLevels(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestURLExtension(t *testing.T) {
	cases := map[string]string{
		"https://ex.com/":              "/",
		"https://ex.com/page.html":     ".html",
		"https://ex.com/Default.ASPX":  ".aspx",
		"https://ex.com/index.php?x=1": ".php",
		"https://ex.com/about":         "none",
		"https://ex.com/v1.2":          "none",
		"https://ex.com/release.2024":  "none",
	}
	for u, want := range cases {
		if got := urlExtension(u); got != want {
			t.Errorf("urlExtension(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestURLParamType(t *testing.T) {
	cases := map[string]string{
		"https://ex.com/shoes":                         "clean",
		"https://ex.com/shoes/page/3":                  "pagination",
		"https://ex.com/shoes?page=2":                  "pagination",
		"https://ex.com/shoes?color=red&page=2":        "facets",
		"https://ex.com/search?q=boots":                "search",
		"https://ex.com/shoes?utm_source=x&gclid=1":    "tracking",
		"https://ex.com/shoes?sessionid=abc":           "other parameters",
		"https://ex.com/shoes?utm_source=x&sessid=abc": "other parameters",
	}
	for u, want := range cases {
		if got := urlParamType(u); got != want {
			t.Errorf("urlParamType(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestTemplateSegment(t *testing.T) {
	cases := []struct {
		page  types.PageData
		group string
		want  string
	}{
		{types.PageData{URL: "https://ex.com/blog/post", TemplateType: TemplateArticle}, "", "blog_post"},
		{types.PageData{URL: "https://ex.com/guides/x", TemplateType: TemplateArticle}, "", "article"},
		{types.PageData{URL: "https://ex.com/guides/x", TemplateType: TemplateArticle,
			StructuredData: []types.StructuredDataEntry{{Type: "BlogPosting"}}}, "", "blog_post"},
		{types.PageData{URL: "https://ex.com/blog/", TemplateType: TemplateListing}, "", "blog_index"},
		{types.PageData{URL: "https://ex.com/category/shoes/", TemplateType: TemplateListing}, "", "category"},
		{types.PageData{URL: "https://ex.com/category/shoes/", TemplateType: TemplateListing, ListingSignals: 3}, "", "plp"},
		{types.PageData{URL: "https://ex.com/category/shoes/?color=red", TemplateType: TemplateListing}, "", "plp"},
		{types.PageData{URL: "https://ex.com/p/123", TemplateType: TemplateProduct}, "", "pdp"},
		{types.PageData{URL: "https://ex.com/x", TemplateType: TemplateOther}, "group 1 · /x/", "group 1 · /x/"},
		{types.PageData{URL: "https://ex.com/x", TemplateType: TemplateOther}, "", "other"},
		{types.PageData{URL: "https://ex.com/x"}, "", ""},
	}
	for i, c := range cases {
		p := c.page
		if got := templateSegment(&p, c.group, true); got != c.want {
			t.Errorf("case %d (%s): got %q, want %q", i, c.page.URL, got, c.want)
		}
	}
	listing := &types.PageData{URL: "https://ex.com/forum/", TemplateType: TemplateListing, ListingSignals: 2}
	if got := templateSegment(listing, "", false); got != TemplateListing {
		t.Errorf("listing on a non-commerce site = %q, want listing", got)
	}
}

func TestTextAmountUsesSiteTerciles(t *testing.T) {
	var pages []*types.PageData
	for i := 1; i <= 9; i++ {
		pages = append(pages, &types.PageData{URL: fmt.Sprintf("https://ex.com/%d", i), StatusCode: 200, WordCount: i * 100})
	}
	pages = append(pages, &types.PageData{URL: "https://ex.com/empty", StatusCode: 200})
	pages = append(pages, &types.PageData{URL: "https://ex.com/404", StatusCode: 404, WordCount: 50})
	AssignAutoSegments(pages, nil, AutoSegmentOptions{})

	want := map[string]string{
		"https://ex.com/1": "low", "https://ex.com/3": "low",
		"https://ex.com/4": "medium", "https://ex.com/6": "medium",
		"https://ex.com/7": "high", "https://ex.com/9": "high",
		"https://ex.com/empty": "empty", "https://ex.com/404": "",
	}
	for _, p := range pages {
		if w, ok := want[p.URL]; ok && p.AutoSegments.TextAmount != w {
			t.Errorf("%s: textAmount = %q, want %q", p.URL, p.AutoSegments.TextAmount, w)
		}
	}
}

func TestIndexStateAndLocale(t *testing.T) {
	cases := []struct {
		p          types.PageData
		wantState  string
		wantLocale string
	}{
		{types.PageData{URL: "https://ex.com/es/x", StatusCode: 200, Indexability: types.IndexabilityData{Indexable: true}}, "indexable", "es"},
		{types.PageData{URL: "https://ex.com/x", StatusCode: 200, HTMLLang: "en_US", Indexability: types.IndexabilityData{Reason: "meta robots noindex"}}, "noindex", "en-us"},
		{types.PageData{URL: "https://ex.com/x", StatusCode: 200, Indexability: types.IndexabilityData{Reason: "canonicalized"},
			Hreflang: []types.HreflangEntry{{Lang: "fr-FR", Href: "https://ex.com/x"}}}, "canonicalised", "fr-fr"},
		{types.PageData{URL: "https://ex.com/x", StatusCode: 200}, "non-indexable", "unknown"},
		{types.PageData{URL: "https://ex.com/x", StatusCode: 301}, "redirect", ""},
		{types.PageData{URL: "https://ex.com/x", StatusCode: 404}, "4xx", ""},
		{types.PageData{URL: "https://ex.com/x", StatusCode: 503}, "5xx", ""},
		{types.PageData{URL: "https://ex.com/x", RobotsBlocked: true}, "robots blocked", ""},
	}
	for i, c := range cases {
		p := c.p
		if got := indexState(&p); got != c.wantState {
			t.Errorf("case %d: indexState = %q, want %q", i, got, c.wantState)
		}
		u, _ := url.Parse(p.URL)
		if got := pageLocale(&p, p.URL, u); got != c.wantLocale {
			t.Errorf("case %d: locale = %q, want %q", i, got, c.wantLocale)
		}
	}
}

func TestPrimarySchemaTypeSkipsGeneric(t *testing.T) {
	p := &types.PageData{StatusCode: 200, StructuredData: []types.StructuredDataEntry{{Type: "BreadcrumbList"}, {Type: "WebSite"}, {Type: "Product"}}}
	if got := primarySchemaType(p); got != "Product" {
		t.Errorf("got %q, want Product", got)
	}
	p.StructuredData = []types.StructuredDataEntry{{Type: "WebPage"}}
	if got := primarySchemaType(p); got != "WebPage" {
		t.Errorf("got %q, want WebPage", got)
	}
	p.StructuredData = nil
	if got := primarySchemaType(p); got != "none" {
		t.Errorf("got %q, want none", got)
	}
}

func TestFreshnessAndLinkProfile(t *testing.T) {
	now := time.Now()
	p := &types.PageData{StatusCode: 200, Freshness: &types.FreshnessData{BylineDate: &now, ContentAgeDays: 30}}
	if got := freshnessBand(p); got != "< 6 months" {
		t.Errorf("freshness = %q", got)
	}
	p.Freshness.ContentAgeDays = 400
	if got := freshnessBand(p); got != "6-24 months" {
		t.Errorf("freshness = %q", got)
	}
	p.Freshness = nil
	if got := freshnessBand(p); got != "no date" {
		t.Errorf("freshness = %q", got)
	}

	p.LinkZones = &types.LinkZoneCounts{Content: 8, Navigation: 2}
	if got := linkProfile(p); got != "content-led" {
		t.Errorf("linkProfile = %q", got)
	}
	p.LinkZones = &types.LinkZoneCounts{Content: 3, Footer: 7}
	if got := linkProfile(p); got != "mixed" {
		t.Errorf("linkProfile = %q", got)
	}
	p.LinkZones = &types.LinkZoneCounts{Content: 1, Navigation: 30, Footer: 20}
	if got := linkProfile(p); got != "boilerplate-heavy" {
		t.Errorf("linkProfile = %q", got)
	}
	p.LinkZones = &types.LinkZoneCounts{}
	if got := linkProfile(p); got != "no internal links" {
		t.Errorf("linkProfile = %q", got)
	}
	p.LinkZones = nil
	if got := linkProfile(p); got != "" {
		t.Errorf("linkProfile without zone data = %q, want empty", got)
	}
}

func TestAuthorityFallsBackToInlinks(t *testing.T) {
	var pages []*types.PageData
	for i := 0; i < 9; i++ {
		pages = append(pages, &types.PageData{URL: fmt.Sprintf("https://ex.com/%d", i), StatusCode: 200, Inlinks: i, AutoSegments: &types.PageSegments{}})
	}
	AssignAuthoritySegments(pages, nil)
	if got := pages[0].AutoSegments.Authority; got != "none" {
		t.Errorf("0 inlinks: %q, want none", got)
	}
	if got := pages[8].AutoSegments.Authority; got != "high" {
		t.Errorf("8 inlinks: %q, want high", got)
	}
	if got := pages[1].AutoSegments.Authority; got != "low" {
		t.Errorf("1 inlink: %q, want low", got)
	}

	uniform := map[string]float64{}
	for _, p := range pages {
		uniform[p.URL] = 10
	}
	AssignAuthoritySegments(pages, uniform)
	if got := pages[8].AutoSegments.Authority; got != "high" {
		t.Errorf("uniform PageRank should fall back to inlinks: page 8 = %q", got)
	}
	if got := pages[1].AutoSegments.Authority; got != "low" {
		t.Errorf("uniform PageRank should fall back to inlinks: page 1 = %q", got)
	}

	scores := map[string]float64{}
	for i, p := range pages {
		scores[p.URL] = float64(9-i) / 10 // reverse of inlinks: page 0 has the most PageRank
	}
	AssignAuthoritySegments(pages, scores)
	if got := pages[0].AutoSegments.Authority; got != "high" {
		t.Errorf("PageRank should win over inlinks: %q", got)
	}
}

func TestPropagateTemplateClusters(t *testing.T) {
	const fp = uint64(0xF0F0F0F0F0F0F0F0)
	var pages []*types.PageData
	add := func(path, tpl string, f uint64) *types.PageData {
		p := &types.PageData{URL: "https://ex.com" + path, StatusCode: 200, TemplateType: tpl, DOMFingerprint: f}
		pages = append(pages, p)
		return p
	}
	// /shop/: 3 known products + 2 unknown pages with a near-identical skeleton.
	add("/shop/a", TemplateProduct, fp)
	add("/shop/b", TemplateProduct, fp^1)
	add("/shop/c", TemplateProduct, fp^3)
	u1 := add("/shop/d", TemplateOther, fp^7)
	u2 := add("/shop/e", TemplateOther, fp)
	// Same skeleton but another directory must not inherit.
	other := add("/misc/x", TemplateOther, fp)
	// /tools/: 5 unknown pages sharing a skeleton -> unnamed group.
	var tools []*types.PageData
	for i := 0; i < 5; i++ {
		tools = append(tools, add(fmt.Sprintf("/tools/t%d", i), TemplateOther, ^fp^uint64(i)))
	}

	labels := PropagateTemplateClusters(pages)
	if u1.TemplateType != TemplateProduct || u2.TemplateType != TemplateProduct {
		t.Errorf("unknown /shop/ pages should inherit product, got %q %q", u1.TemplateType, u2.TemplateType)
	}
	if other.TemplateType != TemplateOther {
		t.Errorf("/misc/ page should stay other, got %q", other.TemplateType)
	}
	for _, p := range tools {
		if labels[p.URL] != "group 1 · /tools/" {
			t.Errorf("%s label = %q", p.URL, labels[p.URL])
		}
	}
	if _, ok := labels[other.URL]; ok {
		t.Errorf("single unknown page should not get a group label")
	}
}

func TestBuildAutoSegmentStatsAndRules(t *testing.T) {
	pages := []*types.PageData{
		{URL: "https://ex.com/blog/a", StatusCode: 200, WordCount: 900, Inlinks: 4, ResponseTimeMs: 100, TemplateType: TemplateArticle, Indexability: types.IndexabilityData{Indexable: true}},
		{URL: "https://ex.com/blog/b", StatusCode: 200, WordCount: 300, Inlinks: 2, ResponseTimeMs: 300, TemplateType: TemplateArticle},
		{URL: "https://ex.com/shop/x", StatusCode: 404},
	}
	rules := []Segment{{Name: "blog", Pattern: regexp.MustCompile(`/blog/`)}}
	AssignAutoSegments(pages, nil, AutoSegmentOptions{Rules: rules})
	if len(pages[0].Segments) != 1 || pages[0].Segments[0] != "blog" {
		t.Errorf("rule segments not assigned: %v", pages[0].Segments)
	}
	stats := BuildAutoSegmentStats(pages)
	var dir1 *types.SegmentDimensionStats
	for i := range stats {
		if stats[i].Dimension == "dir1" {
			dir1 = &stats[i]
		}
	}
	if dir1 == nil || len(dir1.Values) != 2 {
		t.Fatalf("dir1 stats missing or wrong: %+v", dir1)
	}
	blog := dir1.Values[0]
	if blog.Value != "/blog/" || blog.Pages != 2 || blog.IndexablePct != 50 || blog.AvgWords != 600 || blog.AvgInlinks != 3 || blog.AvgResponseMs != 200 {
		t.Errorf("blog stats wrong: %+v", blog)
	}
	if dir1.Values[1].ErrorPct != 100 {
		t.Errorf("shop error pct = %v", dir1.Values[1].ErrorPct)
	}
}

func BenchmarkAutoSegments100K(b *testing.B) {
	pages := make([]*types.PageData, 100000)
	for i := range pages {
		pages[i] = &types.PageData{
			URL:            fmt.Sprintf("https://ex.com/section%d/sub%d/item-%d?page=%d", i%40, i%300, i, i%5),
			StatusCode:     200,
			WordCount:      (i * 37) % 3000,
			Depth:          i % 9,
			Inlinks:        i % 50,
			TemplateType:   []string{TemplateOther, TemplateArticle, TemplateListing, TemplateProduct}[i%4],
			DOMFingerprint: uint64(i%60) * 0x9E3779B97F4A7C15,
			LinkZones:      &types.LinkZoneCounts{Content: i % 7, Navigation: 20, Footer: 10},
			StructuredData: []types.StructuredDataEntry{{Type: "BreadcrumbList"}, {Type: "Product"}},
		}
	}
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for _, p := range pages {
			p.AutoSegments = nil
		}
		labels := PropagateTemplateClusters(pages)
		AssignAutoSegments(pages, labels, AutoSegmentOptions{})
		_ = BuildAutoSegmentStats(pages)
	}
}
