package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// exampleCodes mirrors petsitters.example: 26 locales (incl. ca-AD) plus x-default,
// declared only in the sitemap.
var exampleCodes = []string{
	"de-AT", "de-DE", "de-CH", "en-GB", "en-US", "en-IE", "es-ES", "es-MX", "es-AR",
	"fr-FR", "fr-BE", "fr-CH", "it-IT", "it-CH", "nl-NL", "nl-BE", "pt-PT", "pt-BR",
	"ca-AD", "ca-ES", "pl-PL", "sv-SE", "da-DK", "fi-FI", "nb-NO", "cs-CZ", "x-default",
}

func examplePath(code string) string {
	if code == "x-default" {
		return "/"
	}
	return "/" + strings.ToLower(code) + "/"
}

// multiSitemap lists one <url> per locale, each with the 27 alternates.
// withNS false drops the xmlns:xhtml declaration (seen in the wild).
func multiSitemap(base string, withNS bool) string {
	var b strings.Builder
	ns := ` xmlns:xhtml="http://www.w3.org/1999/xhtml"`
	if !withNS {
		ns = ""
	}
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"%s>`, ns)
	for _, c := range exampleCodes[:len(exampleCodes)-1] {
		fmt.Fprintf(&b, "<url><loc>%s%s</loc>", base, examplePath(c))
		for _, alt := range exampleCodes {
			fmt.Fprintf(&b, `<xhtml:link rel="alternate" hreflang="%s" href="%s%s"/>`, alt, base, examplePath(alt))
		}
		b.WriteString("</url>")
	}
	// A URL without alternates and a link that is not an alternate.
	fmt.Fprintf(&b, `<url><loc>%s/about</loc><xhtml:link rel="canonical" hreflang="en" href="%s/about"/></url>`, base, base)
	b.WriteString("</urlset>")
	return b.String()
}

func TestFetchSitemapReadsHreflang(t *testing.T) {
	for _, withNS := range []bool{true, false} {
		t.Run(fmt.Sprintf("namespace=%v", withNS), func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprint(w, multiSitemap(srv.URL, withNS))
			}))
			defer srv.Close()

			var urls []string
			var alts [][]types.HreflangEntry
			err := fetchSitemap(context.Background(), srv.URL+"/sitemap.xml", srv.Client(), "test", nil, 0, func(u []string, a [][]types.HreflangEntry) {
				urls, alts = u, a
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(urls) != 27 || len(alts) != 27 {
				t.Fatalf("urls=%d alts=%d, want 27 each", len(urls), len(alts))
			}
			first := alts[0]
			if len(first) != 27 {
				t.Fatalf("first url has %d alternates, want 27", len(first))
			}
			found := false
			for _, a := range first {
				if a.Source != types.HreflangSourceSitemap {
					t.Errorf("source = %q", a.Source)
				}
				if a.Lang == "ca-AD" && a.Href == srv.URL+"/ca-ad/" {
					found = true
				}
			}
			if !found {
				t.Error("ca-AD alternate missing")
			}
			if alts[26] != nil {
				t.Errorf("/about: rel=canonical link kept as alternate: %v", alts[26])
			}

			var h types.SitemapHreflang
			for i, a := range alts {
				h.Add(urls[i], a)
			}
			if h.Len() != 26 || len(h.Sets) != 1 {
				t.Errorf("collected %d urls in %d sets, want 26 in 1 (shared)", h.Len(), len(h.Sets))
			}
		})
	}
}

// Spider mode with sitemap discovery and sitemap mode both keep the
// alternates for analysis.
func TestCrawlKeepsSitemapHreflang(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprintf(w, "User-agent: *\nAllow: /\nSitemap: %s/sitemap.xml\n", srv.URL)
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprint(w, multiSitemap(srv.URL, true))
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html lang="de"><head><title>p</title></head><body>hi</body></html>`)
		}
	}))
	defer srv.Close()

	configs := map[string]types.CrawlConfig{
		"spider":  {SeedURL: srv.URL + "/", Mode: types.ModeSpider, DiscoverSitemaps: true, RespectRobots: true},
		"sitemap": {Mode: types.ModeSitemap, SitemapURLs: []string{srv.URL + "/sitemap.xml"}},
	}
	for name, cfg := range configs {
		t.Run(name, func(t *testing.T) {
			cfg.MaxDepth, cfg.MaxPages, cfg.Concurrency = 3, 5, 2
			cfg.UserAgent, cfg.SkipSSRF, cfg.NoSoft404Probe = "Micelio/1.0", true, true
			res, err := Crawl(context.Background(), cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.SitemapHreflang.Len() != 26 {
				t.Fatalf("SitemapHreflang has %d urls, want 26", res.SitemapHreflang.Len())
			}
			if got := len(res.SitemapHreflang.Alternates(srv.URL + "/ca-ad/")); got != 27 {
				t.Errorf("ca-AD page has %d alternates, want 27", got)
			}
		})
	}
}
