package analysis

import (
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestValidateHreflangCode(t *testing.T) {
	tests := []struct {
		code  string
		valid bool
		hint  string // substring of the hint
	}{
		{"de-AT", true, ""},
		{"ca-AD", true, ""},
		{"x-default", true, ""},
		{"X-Default", true, ""},
		{"EN-gb", true, ""},
		{"de", true, ""},
		{"zh-Hant", true, ""},
		{"zh-Hant-TW", true, ""},
		{"en-UK", false, "en-GB"},
		{"en_US", false, "en-US"},
		{"jp", false, "ja-JP"},
		{"cn", false, "zh-CN"},
		{"us-en", false, "en-US"},
		{"es-419", false, "country codes"},
		{"en-EU", false, "country codes"},
		{"eng", false, "2-letter"},
		{"de-XX", false, "unknown country"},
		{"qq", false, "unknown language"},
		{"", false, "empty"},
		{"en-GB-london", false, "too many"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			ok, hint := validateHreflangCode(tt.code)
			if ok != tt.valid {
				t.Fatalf("valid = %v, want %v (hint %q)", ok, tt.valid, hint)
			}
			if tt.hint != "" && !strings.Contains(hint, tt.hint) {
				t.Errorf("hint %q does not mention %q", hint, tt.hint)
			}
		})
	}
}

const site = "https://www.petsitters.example"

// multiLangSet is the 27-alternate cluster petsitters.example declares only in its
// sitemap: 26 locales incl. ca-AD, and x-default on the root.
func multiLangSet() []types.HreflangEntry {
	codes := []string{
		"de-AT", "de-DE", "de-CH", "en-GB", "en-US", "en-IE", "es-ES", "es-MX", "es-AR",
		"fr-FR", "fr-BE", "fr-CH", "it-IT", "it-CH", "nl-NL", "nl-BE", "pt-PT", "pt-BR",
		"ca-AD", "ca-ES", "pl-PL", "sv-SE", "da-DK", "fi-FI", "nb-NO", "cs-CZ",
	}
	var out []types.HreflangEntry
	for _, c := range codes {
		out = append(out, types.HreflangEntry{Lang: c, Href: site + "/" + strings.ToLower(c) + "/"})
	}
	return append(out, types.HreflangEntry{Lang: "x-default", Href: site + "/"})
}

func page(url string, status int, mods ...func(*types.PageData)) *types.PageData {
	p := &types.PageData{URL: url, FinalURL: url, StatusCode: status, Indexability: types.IndexabilityData{Indexable: status == 200}}
	for _, m := range mods {
		m(p)
	}
	return p
}

func lang(l string) func(*types.PageData) { return func(p *types.PageData) { p.HTMLLang = l } }

func alts(pairs ...string) func(*types.PageData) {
	return func(p *types.PageData) {
		for i := 0; i+1 < len(pairs); i += 2 {
			p.Hreflang = append(p.Hreflang, types.HreflangEntry{Lang: pairs[i], Href: pairs[i+1]})
		}
	}
}

func noindex(p *types.PageData) {
	p.Indexability = types.IndexabilityData{Reason: "meta robots noindex"}
}

func sitemapWith(entries map[string][]types.HreflangEntry) *types.SitemapHreflang {
	var h types.SitemapHreflang
	for loc, a := range entries {
		h.Add(loc, a)
	}
	return &h
}

func TestAnalyzeHreflang(t *testing.T) {
	set := multiLangSet()
	multiSitemap := map[string][]types.HreflangEntry{}
	for _, a := range set {
		if a.Lang != "x-default" {
			multiSitemap[a.Href] = set
		}
	}

	tests := []struct {
		name    string
		pages   []*types.PageData
		sitemap map[string][]types.HreflangEntry
		want    map[string]int // issue id -> Count; ids not listed must be absent
		check   func(t *testing.T, h *types.HreflangStats)
	}{
		{
			name: "multilingual site: sitemap only, x-default noindex, partial crawl",
			pages: []*types.PageData{
				page(site+"/", 200, lang("en"), noindex),
				page(site+"/de-at/", 200, lang("de")),
				page(site+"/ca-ad/", 200, lang("es")), // Catalan page tagged as Spanish
				page(site+"/fr-ch/", 200, func(p *types.PageData) {
					p.FinalURL = site + "/fr-fr/"
					p.RedirectChain = []types.RedirectHop{{URL: site + "/fr-ch/", StatusCode: 301}}
				}),
				page(site+"/it-ch/", 404),
			},
			sitemap: multiSitemap,
			want: map[string]int{
				// 24 listed pages answer 200 or were not crawled (fr-ch
				// redirects, it-ch is a 404: their own declarations are
				// skipped); each points at the 3 bad targets.
				types.HreflangTargetNoindex:  24, // x-default → noindex root
				types.HreflangTargetRedirect: 24,
				types.HreflangTargetError:    24,
				types.HreflangLangMismatch:   1,
				// 22 locales were not crawled: 22 sitemap-only pages see
				// the 21 others, de-at and ca-ad see all 22.
				types.HreflangTargetNotCrawled: 22*21 + 2*22,
			},
			check: func(t *testing.T, h *types.HreflangStats) {
				if h.PagesWithHreflang != 26 || h.BySource["sitemap"] != 26 || h.BySource["html"] != 0 {
					t.Errorf("pages = %d, by source %v", h.PagesWithHreflang, h.BySource)
				}
				if h.CrawledPages != 4 || h.SitemapOnlyPages != 22 {
					t.Errorf("crawled %d, sitemap-only %d", h.CrawledPages, h.SitemapOnlyPages)
				}
				if len(h.Codes) != 27 || h.XDefaultPages != 26 {
					t.Errorf("codes = %d, x-default pages = %d", len(h.Codes), h.XDefaultPages)
				}
				for _, c := range h.Codes {
					if !c.Valid {
						t.Errorf("code %s flagged invalid", c.Code)
					}
				}
				g := group(h, types.HreflangTargetNoindex)
				if g.Samples[0].Lang != "x-default" || g.Samples[0].Source != "sitemap" {
					t.Errorf("noindex sample = %+v", g.Samples[0])
				}
				if g := group(h, types.HreflangLangMismatch); g.Samples[0].URL != site+"/ca-ad/" || g.Samples[0].Lang != "ca-AD" {
					t.Errorf("mismatch sample = %+v", g.Samples[0])
				}
			},
		},
		{
			name: "sitemap target outside the sitemap, crawled without hreflang",
			pages: []*types.PageData{
				page(site+"/en/", 200),
				page(site+"/landing-de", 200),
			},
			sitemap: map[string][]types.HreflangEntry{
				site + "/en/": {{Lang: "en", Href: site + "/en/"}, {Lang: "de", Href: site + "/landing-de"}, {Lang: "en-UK", Href: site + "/uk/"}},
			},
			want: map[string]int{
				types.HreflangNoReturn:         1,
				types.HreflangInvalidCode:      1,
				types.HreflangMissingXDefault:  1,
				types.HreflangTargetNotCrawled: 1,
			},
			check: func(t *testing.T, h *types.HreflangStats) {
				if d := group(h, types.HreflangNoReturn).Samples[0].Detail; d != "target declares no hreflang" {
					t.Errorf("detail = %q", d)
				}
				if s := group(h, types.HreflangInvalidCode).Samples[0]; !strings.Contains(s.Detail, "en-GB") {
					t.Errorf("invalid sample = %+v", s)
				}
			},
		},
		{
			name: "html: non-reciprocal, missing self, duplicates, relative, missing href",
			pages: []*types.PageData{
				page("https://ex.com/en", 200, lang("en"), alts("en", "https://ex.com/en", "de", "https://ex.com/de", "x-default", "https://ex.com/en")),
				page("https://ex.com/de", 200, lang("de"), alts("de", "https://ex.com/de", "x-default", "https://ex.com/de")),
				page("https://ex.com/fr", 200, lang("fr"), alts("en", "https://ex.com/en", "x-default", "https://ex.com/en")),
				page("https://ex.com/es", 200, lang("es"), alts("es", "/es", "es", "https://ex.com/es-old", "it", "", "x-default", "https://ex.com/es")),
			},
			want: map[string]int{
				types.HreflangNoReturn:      2, // en→de (de omits en); fr→en (en omits fr)
				types.HreflangMissingSelf:   1, // fr
				types.HreflangDuplicateCode: 1,
				types.HreflangRelativeURL:   1,
				types.HreflangMissingHref:   1,
			},
		},
		{
			name: "html and sitemap disagree; target canonicalised and blocked",
			pages: []*types.PageData{
				page("https://ex.com/en", 200, alts("en", "https://ex.com/en", "fr", "https://ex.com/fr", "x-default", "https://ex.com/en")),
				page("https://ex.com/fr", 200, alts("fr", "https://ex.com/fr", "en", "https://ex.com/en", "x-default", "https://ex.com/en"), func(p *types.PageData) {
					c := "https://ex.com/en"
					p.Canonical = &c
					p.Indexability = types.IndexabilityData{Reason: "canonicalized"}
				}),
				page("https://ex.com/it", 0, func(p *types.PageData) { p.RobotsBlocked = true }),
			},
			sitemap: map[string][]types.HreflangEntry{
				"https://ex.com/en": {{Lang: "en", Href: "https://ex.com/en"}, {Lang: "fr", Href: "https://ex.com/fr"}, {Lang: "it", Href: "https://ex.com/it"}, {Lang: "x-default", Href: "https://ex.com/en"}},
			},
			want: map[string]int{
				types.HreflangSourceConflict:  1,
				types.HreflangTargetCanonical: 1,
				types.HreflangTargetBlocked:   1,
			},
			check: func(t *testing.T, h *types.HreflangStats) {
				if d := group(h, types.HreflangSourceConflict).Samples[0].Detail; d != "only in sitemap: it" {
					t.Errorf("conflict detail = %q", d)
				}
				if h.BySource["html"] != 2 || h.BySource["sitemap"] != 1 {
					t.Errorf("by source = %v", h.BySource)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sm *types.SitemapHreflang
			if tt.sitemap != nil {
				sm = sitemapWith(tt.sitemap)
			}
			h := AnalyzeHreflang(tt.pages, sm)
			if h == nil {
				t.Fatal("nil stats")
			}
			got := map[string]int{}
			for _, g := range h.Issues {
				got[g.ID] = g.Count
				if g.Label == "" || g.Severity == "" || len(g.Samples) == 0 || g.Pages == 0 {
					t.Errorf("incomplete group %+v", g)
				}
			}
			for id, n := range tt.want {
				if got[id] != n {
					t.Errorf("%s = %d, want %d", id, got[id], n)
				}
			}
			for id, n := range got {
				if _, ok := tt.want[id]; !ok {
					t.Errorf("unexpected %s (%d): %+v", id, n, group(h, id).Samples[0])
				}
			}
			if tt.check != nil {
				tt.check(t, h)
			}
		})
	}
}

func TestAnalyzeHreflangNothingDeclared(t *testing.T) {
	if h := AnalyzeHreflang([]*types.PageData{page("https://ex.com/", 200)}, nil); h != nil {
		t.Errorf("got %+v, want nil", h)
	}
}

func TestHreflangLegacyIssuesAndReport(t *testing.T) {
	pages := []*types.PageData{
		page("https://ex.com/en", 200, alts("en-UK", "https://ex.com/en", "x-default", "https://ex.com/en")),
	}
	stats := GenerateReport(pages, 0, ReportConfig{SeedURL: "https://ex.com/en"})
	if stats.Hreflang == nil || len(stats.HreflangIssues) != 1 {
		t.Fatalf("hreflang = %+v, issues = %+v", stats.Hreflang, stats.HreflangIssues)
	}
	if is := stats.HreflangIssues[0]; is.Type != types.HreflangInvalidCode || !strings.Contains(is.Issue, "en-GB") {
		t.Errorf("legacy issue = %+v", is)
	}
}

func group(h *types.HreflangStats, id string) types.HreflangIssueGroup {
	for _, g := range h.Issues {
		if g.ID == id {
			return g
		}
	}
	return types.HreflangIssueGroup{Samples: []types.HreflangIssueSample{{}}}
}

// A link that redirects to an app store must not report the store's hreflang.
func TestAnalyzeHreflangSkipsOffsiteRedirect(t *testing.T) {
	store := page("https://ex.com/app/install", 200, alts("es-419", "https://play.example/app?hl=es_419"),
		func(p *types.PageData) { p.FinalURL = "https://play.example/app" })
	if h := AnalyzeHreflang([]*types.PageData{store}, nil); h != nil {
		t.Errorf("got %+v, want nil", h)
	}
	moved := page("https://ex.com/old", 200, alts("en", "https://ex.com/old"),
		func(p *types.PageData) { p.FinalURL = "https://ex.com/new" })
	if h := AnalyzeHreflang([]*types.PageData{moved}, nil); h == nil || h.PagesWithHreflang != 1 {
		t.Errorf("same-host redirect: got %+v, want 1 page", h)
	}
}
