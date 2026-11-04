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

// A link that redirects to another site is stored as the redirect, not as a
// page of the crawled site with the other site's content.
func TestCrawlStopsAtOffsiteRedirect(t *testing.T) {
	var storeHits int
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storeHits++
		fmt.Fprint(w, `<html lang="es"><head><title>App store</title><link rel="alternate" hreflang="es-419" href="/es"></head></html>`)
	}))
	defer store.Close()
	// Same port space, other host name: 127.0.0.1 (site) vs localhost (store).
	storeURL := strings.Replace(store.URL, "127.0.0.1", "localhost", 1)

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<html><head><title>Home</title></head><body><a href="/install">App</a><a href="/old">Old</a></body></html>`)
		case "/install":
			http.Redirect(w, r, storeURL+"/app", http.StatusFound)
		case "/old":
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
		case "/new":
			fmt.Fprint(w, `<html><head><title>New</title></head></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()

	result, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: site.URL, Mode: types.ModeSpider, MaxDepth: 2, MaxPages: 10,
		Concurrency: 1, UserAgent: "TestBot/1.0", SkipSSRF: true, NoSoft404Probe: true, NoAgentCheck: true,
	}, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	pages := map[string]*types.PageData{}
	for _, p := range result.Pages {
		pages[strings.TrimPrefix(p.URL, site.URL)] = p
	}

	install, ok := pages["/install"]
	if !ok {
		t.Fatalf("/install not crawled; pages: %v", pages)
	}
	if install.StatusCode != http.StatusFound || install.FinalURL != storeURL+"/app" {
		t.Errorf("/install: status %d final %q, want 302 to %s/app", install.StatusCode, install.FinalURL, storeURL)
	}
	if install.Title != nil || len(install.Hreflang) > 0 {
		t.Errorf("/install carries the store's content: title %v hreflang %v", install.Title, install.Hreflang)
	}
	if len(install.RedirectChain) != 1 {
		t.Errorf("/install chain = %v, want 1 hop", install.RedirectChain)
	}
	if storeHits != 0 {
		t.Errorf("store fetched %d times, want 0", storeHits)
	}

	old := pages["/old"]
	if old.StatusCode != 200 || old.Title == nil || old.Title.Text != "New" {
		t.Errorf("same-host redirect not followed: status %d title %v", old.StatusCode, old.Title)
	}
}

// The seed may move host (apex -> www): the crawl follows it and goes on.
func TestCrawlFollowsSeedRedirectToOtherHost(t *testing.T) {
	var siteHost string
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != strings.TrimPrefix(siteHost, "http://") {
			http.Redirect(w, r, siteHost+r.URL.Path, http.StatusMovedPermanently)
			return
		}
		if r.URL.Path == "/" {
			fmt.Fprint(w, `<html><head><title>Home</title></head><body><a href="/a">A</a></body></html>`)
			return
		}
		fmt.Fprint(w, `<html><head><title>A</title></head></html>`)
	}))
	defer site.Close()
	siteHost = site.URL
	seed := strings.Replace(site.URL, "127.0.0.1", "localhost", 1)

	result, err := Crawl(context.Background(), types.CrawlConfig{
		SeedURL: seed, Mode: types.ModeSpider, MaxDepth: 2, MaxPages: 10,
		Concurrency: 1, UserAgent: "TestBot/1.0", SkipSSRF: true, NoSoft404Probe: true, NoAgentCheck: true,
	}, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	var titles []string
	for _, p := range result.Pages {
		if p.Title != nil {
			titles = append(titles, p.Title.Text)
		}
	}
	if len(titles) < 2 {
		t.Errorf("seed redirect not followed: titles %v", titles)
	}
}

// In list mode the first URL is not a seed: its off-site redirect is not
// followed and does not move the crawl to that host.
func TestListModeFirstURLOffsiteRedirect(t *testing.T) {
	var storeHits int
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storeHits++
		fmt.Fprint(w, `<html><head><title>Store</title></head></html>`)
	}))
	defer store.Close()
	storeURL := strings.Replace(store.URL, "127.0.0.1", "localhost", 1)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/install" {
			http.Redirect(w, r, storeURL+"/app", http.StatusFound)
			return
		}
		fmt.Fprint(w, `<html><head><title>Page</title></head></html>`)
	}))
	defer site.Close()

	result, err := Crawl(context.Background(), types.CrawlConfig{
		Mode: types.ModeList, URLs: []string{site.URL + "/install", site.URL + "/page"},
		MaxPages: 10, Concurrency: 1, UserAgent: "TestBot/1.0", SkipSSRF: true, NoSoft404Probe: true, NoAgentCheck: true,
	}, nil)
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	for _, p := range result.Pages {
		if strings.HasSuffix(p.URL, "/install") && p.StatusCode != http.StatusFound {
			t.Errorf("/install status %d, want 302", p.StatusCode)
		}
	}
	if storeHits != 0 {
		t.Errorf("store fetched %d times, want 0", storeHits)
	}
}
