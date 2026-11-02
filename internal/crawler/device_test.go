package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestSecChUaMobile(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Mobile Safari/537.36": "?1",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.0.0 Safari/537.36":       "?0",
	} {
		req := httptest.NewRequest("GET", "https://example.com/", nil)
		setHeaders(req, FetchOptions{UserAgent: ua})
		if got := req.Header.Get("Sec-Ch-Ua-Mobile"); got != want {
			t.Errorf("%s: Sec-Ch-Ua-Mobile = %q, want %q", ua[:40], got, want)
		}
		if req.Header.Get("Sec-Ch-Ua-Platform") == "" {
			t.Error("no Sec-Ch-Ua-Platform")
		}
	}
}

// A site that serves different links to phones (dynamic serving): crawling
// as a mobile device follows the mobile links, robots.txt picks the group
// for the device's bot token, and a typed User-Agent still wins.
func TestCrawlAsDevice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: Googlebot\nDisallow: /desktop-only\n\nUser-agent: *\nAllow: /\n")
		case "/":
			w.Header().Set("Vary", "User-Agent")
			if strings.Contains(ua, "Mobile") {
				fmt.Fprint(w, `<html><head><title>m</title></head><body><a href="/m-only">m</a></body></html>`)
			} else {
				fmt.Fprint(w, `<html><head><title>d</title></head><body><a href="/desktop-only">d</a></body></html>`)
			}
		default:
			fmt.Fprint(w, `<html><head><title>leaf</title></head><body>leaf</body></html>`)
		}
	}))
	defer srv.Close()
	crawl := func(cfg types.CrawlConfig) map[string]bool {
		t.Helper()
		cfg.SeedURL, cfg.Mode, cfg.MaxDepth, cfg.MaxPages, cfg.Concurrency, cfg.SkipSSRF, cfg.RespectRobots, cfg.ShowBlockedInternal = srv.URL, types.ModeSpider, 2, 10, 1, true, true, false
		res, err := Crawl(context.Background(), cfg, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, p := range res.Pages {
			got[strings.TrimPrefix(p.URL, srv.URL)] = true
		}
		return got
	}
	if got := crawl(types.CrawlConfig{Device: &types.DeviceProfile{ID: "mobile"}}); !got["/m-only"] || got["/desktop-only"] {
		t.Errorf("mobile crawl = %v", got)
	}
	if got := crawl(types.CrawlConfig{Device: &types.DeviceProfile{ID: "desktop"}}); !got["/desktop-only"] || got["/m-only"] {
		t.Errorf("desktop crawl = %v", got)
	}
	// Googlebot Desktop gets the desktop page, and its robots group blocks /desktop-only.
	if got := crawl(types.CrawlConfig{Device: &types.DeviceProfile{ID: "googlebot-desktop"}}); got["/desktop-only"] || got["/m-only"] {
		t.Errorf("googlebot desktop crawl = %v", got)
	}
	if got := crawl(types.CrawlConfig{Device: &types.DeviceProfile{ID: "mobile"}, UserAgent: "Desk/1"}); !got["/desktop-only"] {
		t.Errorf("typed UA did not win: %v", got)
	}
}

// Compare crawl: every page is fetched as both devices, links either one
// sees are crawled, and the differences land in page.DeviceCompare.
func TestCompareDevicesDynamicServing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mobile := strings.Contains(r.Header.Get("User-Agent"), "Mobile")
		w.Header().Set("Vary", "User-Agent")
		switch r.URL.Path {
		case "/":
			if mobile {
				fmt.Fprint(w, `<html><head><meta name="viewport" content="width=device-width"><title>Shop</title><link rel="canonical" href="/"></head><body><h1>Shop</h1><p>short</p><a href="/m-only">m</a><a href="/same">s</a></body></html>`)
			} else {
				fmt.Fprint(w, `<html><head><title>Shop | Desktop</title><link rel="canonical" href="/"><script type="application/ld+json">{"@type":"Organization"}</script></head><body><h1>Shop</h1><p>`+strings.Repeat("long desktop text about products ", 60)+`</p><a href="/desktop-only">d</a><a href="/d2">d</a><a href="/d3">d</a><a href="/same">s</a></body></html>`)
			}
		case "/same":
			fmt.Fprint(w, `<html><head><meta name="viewport" content="width=device-width"><title>Same</title></head><body><h1>Same</h1><p>identical page</p></body></html>`)
		default:
			fmt.Fprint(w, `<html><head><meta name="viewport" content="width=device-width"><title>leaf</title></head><body>leaf</body></html>`)
		}
	}))
	defer srv.Close()
	res, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL, Mode: types.ModeSpider, MaxDepth: 2, MaxPages: 20, Concurrency: 1, SkipSSRF: true,
		Device: &types.DeviceProfile{ID: "googlebot-smartphone"}, CompareDevice: &types.DeviceProfile{ID: "googlebot-desktop"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]*types.PageData{}
	for _, p := range res.Pages {
		k := strings.TrimPrefix(p.URL, srv.URL)
		if k == "" {
			k = "/"
		}
		pages[k] = p
	}
	for _, u := range []string{"/m-only", "/desktop-only", "/d2", "/same"} {
		if pages[u] == nil {
			t.Errorf("%s not crawled (union of both devices' links): %v", u, keys(pages))
		}
	}
	home := pages["/"]
	if home == nil || home.DeviceCompare == nil {
		t.Fatalf("home has no comparison: %+v", home)
	}
	c := home.DeviceCompare
	for _, want := range []string{types.DeviceDiffTitle, types.DeviceDiffContent, types.DeviceDiffLinks, types.DeviceDiffSchema} {
		if !slices.Contains(c.Diffs, want) {
			t.Errorf("home diffs %v lack %s", c.Diffs, want)
		}
	}
	for _, not := range []string{types.DeviceDiffStatus, types.DeviceDiffCanonical, types.DeviceDiffH1, types.DeviceNoViewport} {
		if slices.Contains(c.Diffs, not) {
			t.Errorf("home diffs %v should not have %s", c.Diffs, not)
		}
	}
	if c.Device != "googlebot-desktop" || c.LinksOnlySecondaryCount != 3 || c.LinksOnlyPrimaryCount != 1 || !c.Primary.VaryUserAgent || c.Secondary.Title != "Shop | Desktop" {
		t.Errorf("home comparison = %+v", c)
	}
	if same := pages["/same"]; same == nil || same.DeviceCompare == nil || len(same.DeviceCompare.Diffs) != 0 || same.DeviceCompare.TextSimilarity != 100 {
		t.Errorf("/same comparison = %+v", same.DeviceCompare)
	}
}

func keys(m map[string]*types.PageData) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A responsive SPA sends the same HTML to everyone and builds its menu by
// screen width: only a rendered comparison sees the difference.
func TestCompareDevicesRenderedSPA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			fmt.Fprint(w, `<html><head><meta name="viewport" content="width=device-width"><title>leaf</title></head><body>leaf</body></html>`)
			return
		}
		fmt.Fprint(w, `<html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>SPA</title></head><body><div id="app"></div><script>
const app = document.getElementById('app');
const links = innerWidth < 768 ? ['/menu-mobile'] : ['/cat-a', '/cat-b', '/cat-c', '/cat-d'];
app.innerHTML = '<h1>' + (innerWidth < 768 ? 'Mobile' : 'Desktop') + ' home</h1>' + links.map(l => '<a href="' + l + '">' + l + '</a>').join('');
</script></body></html>`)
	}))
	defer srv.Close()
	res, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: srv.URL, Mode: types.ModeSpider, MaxDepth: 1, MaxPages: 10, Concurrency: 1, SkipSSRF: true, JSRendering: true,
		Device: &types.DeviceProfile{ID: "mobile"}, CompareDevice: &types.DeviceProfile{ID: "desktop"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var home *types.PageData
	for _, p := range res.Pages {
		if strings.TrimSuffix(p.URL, "/") == srv.URL {
			home = p
		}
	}
	if home == nil || !home.JSRendered {
		t.Skip("Chrome not available")
	}
	c := home.DeviceCompare
	if c == nil || !c.Primary.Rendered || !c.Secondary.Rendered {
		t.Fatalf("no rendered comparison: %+v", c)
	}
	if !slices.Contains(c.Diffs, types.DeviceDiffLinks) || !slices.Contains(c.Diffs, types.DeviceDiffH1) || c.LinksOnlySecondaryCount != 4 || c.LinksOnlyPrimaryCount != 1 {
		t.Errorf("rendered comparison = %+v", c)
	}
	if !slices.Equal(c.Primary.H1, []string{"Mobile home"}) || !slices.Equal(c.Secondary.H1, []string{"Desktop home"}) {
		t.Errorf("h1 primary %v secondary %v", c.Primary.H1, c.Secondary.H1)
	}
}

func TestLinksOnlyInIgnoresParamOrder(t *testing.T) {
	a := []string{"https://x/p?b=2&a=1", "https://x/q#top", "https://x/only-a"}
	b := []string{"https://x/p?a=1&b=2", "https://x/q"}
	got, n := linksOnlyIn(a, b)
	if n != 1 || len(got) != 1 || got[0] != "https://x/only-a" {
		t.Errorf("linksOnlyIn = %v %d", got, n)
	}
	if n := paramOnlyLinks([]string{"https://x/p?a=1", "https://x/new"}, []string{"https://x/p?a=1&lat=2"}); n != 1 {
		t.Errorf("paramOnlyLinks = %d, want 1", n)
	}
}
