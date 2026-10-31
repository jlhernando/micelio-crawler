package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/micelio/micelio/internal/types"
)

type capturedCrawl struct {
	config  types.CrawlConfig
	analyse bool
	calls   int
}

// runCaptured executes cmd with args and returns the config it would crawl.
func runCaptured(t *testing.T, cmd *cobra.Command, args ...string) (capturedCrawl, error) {
	t.Helper()
	var got capturedCrawl
	orig := startCrawl
	startCrawl = func(config types.CrawlConfig, analyse bool) error {
		got.config, got.analyse = config, analyse
		got.calls++
		return nil
	}
	t.Cleanup(func() { startCrawl = orig })
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	return got, err
}

func mustRun(t *testing.T, cmd *cobra.Command, args ...string) capturedCrawl {
	t.Helper()
	got, err := runCaptured(t, cmd, args...)
	if err != nil {
		t.Fatalf("%s %v: %v", cmd.Name(), args, err)
	}
	if got.calls != 1 {
		t.Fatalf("%s %v: startCrawl called %d times, want 1", cmd.Name(), args, got.calls)
	}
	return got
}

func urlListFile(t *testing.T, urls ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "urls.txt")
	body := ""
	for _, u := range urls {
		body += u + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Every option the dashboard Setup page sends must exist as a flag on all
// three crawl commands (spider, list, sitemap).
func TestCrawlCommandsShareDashboardFlags(t *testing.T) {
	flags := []string{
		"concurrency", "delay", "user-agent", "output", "format", "proxy", "max-errors",
		"timeout", "header", "cookies", "include", "exclude", "path-only-filters",
		"allowed-domains", "no-robots", "show-blocked-internal", "retries",
		"js", "render-block-resources", "render-timeout", "snippet",
		"ngrams", "language", "link-intelligence", "li-no-centrality", "li-max-suggestions",
		"embeddings", "embedding-model", "embedding-provider", "embedding-key", "similarity-threshold",
		"segment", "psi", "psi-key", "ai-prompt", "ai-provider", "ai-model", "ai-key",
		"gsc", "gsc-property", "gsc-key-file", "gsc-days", "gsc-bq",
		"ga4", "ga4-property", "ga4-key-file", "ga4-days",
		"crux", "crux-key", "crux-form-factor",
		"plausible", "plausible-site-id", "plausible-api-key", "plausible-days", "plausible-host",
		"extract", "search", "check-external", "page-weight", "full-anchors", "full-page-weight",
		"html", "html-open", "sitemap-out", "save-html", "save-rendered",
		"db", "no-db", "db-only", "no-analysis", "no-soft404-probe", "resume",
		"adaptive-rate", "delay-factor", "stealth", "discover-sitemaps",
		"tor-control-port", "tor-password", "tor-rotate", "config",
	}
	for _, cmd := range []*cobra.Command{crawlCmd(), listCmd(), sitemapCmd()} {
		for _, name := range flags {
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("%s: missing --%s", cmd.Name(), name)
			}
		}
	}
}

func TestCrawlNewFlags(t *testing.T) {
	got := mustRun(t, crawlCmd(), "example.com", "--no-db")
	if got.config.Retries != nil {
		t.Errorf("retries without flag = %d, want nil (types.DefaultRetries)", *got.config.Retries)
	}
	if got.config.SaveHTML || got.config.SaveRendered || got.config.PathOnlyFilters {
		t.Errorf("save-html/save-rendered/path-only-filters on by default: %+v", got.config)
	}
	if got.config.Mode != types.ModeSpider || got.config.SeedURL != "https://example.com" || !got.analyse {
		t.Errorf("mode/seed/analyse = %v/%q/%v", got.config.Mode, got.config.SeedURL, got.analyse)
	}

	got = mustRun(t, crawlCmd(), "https://example.com", "--no-db", "--retries", "0",
		"--save-html", "--save-rendered", "--path-only-filters")
	if got.config.Retries == nil || *got.config.Retries != 0 {
		t.Errorf("--retries 0 = %v, want explicit 0", got.config.Retries)
	}
	if !got.config.SaveHTML || !got.config.SaveRendered || !got.config.PathOnlyFilters {
		t.Errorf("save-html/save-rendered/path-only-filters not set: %+v", got.config)
	}
}

func TestListRunsFullAnalysis(t *testing.T) {
	file := urlListFile(t, "https://example.com/a", "https://example.com/b")
	db := filepath.Join(t.TempDir(), "list.db")
	got := mustRun(t, listCmd(), file, "--ngrams", "--link-intelligence", "--html", "--js",
		"--db", db, "--extract", "h1:h1", "--search", "/foo/", "--segment", "blog:^/blog/",
		"--retries", "5", "--psi", "--psi-key", "k")
	c := got.config
	if c.Mode != types.ModeList || len(c.URLs) != 2 || c.MaxPages != 2 || c.SeedURL != "https://example.com/a" {
		t.Errorf("mode/urls/maxPages/seed = %v/%v/%d/%q", c.Mode, c.URLs, c.MaxPages, c.SeedURL)
	}
	if !c.Ngrams || !c.LinkIntelligence || !c.HTMLReport || !c.JSRendering || !c.PSI || c.PSIKey != "k" {
		t.Errorf("analysis flags not applied: %+v", c)
	}
	if c.DBPath != db || len(c.CustomExtractions) != 1 || len(c.CustomSearches) != 1 || len(c.SegmentRules) != 1 {
		t.Errorf("db/extract/search/segment = %q/%d/%d/%d", c.DBPath, len(c.CustomExtractions), len(c.CustomSearches), len(c.SegmentRules))
	}
	if c.Retries == nil || *c.Retries != 5 {
		t.Errorf("retries = %v, want 5", c.Retries)
	}
	if !got.analyse {
		t.Error("list must run post-crawl analysis by default")
	}
}

func TestListKeepsItsDefaults(t *testing.T) {
	file := urlListFile(t, "https://example.com/a")
	c := mustRun(t, listCmd(), file, "--no-db").config
	if c.Concurrency != 3 || c.DelayMs != 100 || c.OutputPath != "output.jsonl" {
		t.Errorf("concurrency/delay/output = %d/%d/%q, want 3/100/output.jsonl", c.Concurrency, c.DelayMs, c.OutputPath)
	}
	if c.MaxDepth != 0 {
		t.Errorf("list depth = %d, want 0", c.MaxDepth)
	}
}

func TestListNoAnalysis(t *testing.T) {
	file := urlListFile(t, "https://example.com/a")
	got := mustRun(t, listCmd(), file, "--no-db", "--no-analysis")
	if got.analyse {
		t.Error("--no-analysis: analyse = true")
	}
}

func TestSitemapRunsFullAnalysis(t *testing.T) {
	got := mustRun(t, sitemapCmd(), "https://example.com/sitemap.xml", "https://example.com/news.xml",
		"--no-db", "--js", "--link-intelligence", "--check-external", "--save-html")
	c := got.config
	if c.Mode != types.ModeSitemap || len(c.SitemapURLs) != 2 || c.MaxPages != 100000 {
		t.Errorf("mode/sitemaps/maxPages = %v/%v/%d", c.Mode, c.SitemapURLs, c.MaxPages)
	}
	if !c.JSRendering || !c.LinkIntelligence || !c.CheckExternal || !c.SaveHTML {
		t.Errorf("flags not applied: %+v", c)
	}
	if c.Concurrency != 3 || c.DelayMs != 200 || c.OutputPath != "output.jsonl" {
		t.Errorf("concurrency/delay/output = %d/%d/%q, want 3/200/output.jsonl", c.Concurrency, c.DelayMs, c.OutputPath)
	}
	if !got.analyse {
		t.Error("sitemap must run post-crawl analysis by default")
	}
	c = mustRun(t, sitemapCmd(), "https://example.com/sitemap.xml", "--no-db", "--limit", "40").config
	if c.MaxPages != 40 {
		t.Errorf("--limit 40: maxPages = %d", c.MaxPages)
	}
}

func TestInvalidCruxFormFactorRejectedEverywhere(t *testing.T) {
	file := urlListFile(t, "https://example.com/a")
	cases := []struct {
		cmd  *cobra.Command
		args []string
	}{
		{crawlCmd(), []string{"https://example.com", "--no-db", "--crux-form-factor", "TV"}},
		{listCmd(), []string{file, "--no-db", "--crux-form-factor", "TV"}},
		{sitemapCmd(), []string{"https://example.com/sitemap.xml", "--no-db", "--crux-form-factor", "TV"}},
	}
	for _, tc := range cases {
		got, err := runCaptured(t, tc.cmd, tc.args...)
		if err == nil || got.calls != 0 {
			t.Errorf("%s: invalid crux-form-factor accepted", tc.cmd.Name())
		}
	}
}

func TestDBOnlyAndResumeChecksEverywhere(t *testing.T) {
	file := urlListFile(t, "https://example.com/a")
	for _, args := range [][]string{
		{file, "--db-only", "--no-db"},
		{file, "--resume", "--no-db"},
	} {
		if _, err := runCaptured(t, listCmd(), args...); err == nil {
			t.Errorf("list %v: want error", args)
		}
	}
	out := filepath.Join(t.TempDir(), "run.jsonl")
	c := mustRun(t, listCmd(), file, "-o", out, "--db-only").config
	if c.OutputPath != "" || c.DBPath == "" {
		t.Errorf("--db-only: output=%q db=%q, want no output and a db", c.OutputPath, c.DBPath)
	}
}

// --config: these tests also need loadConfigFile and overlayFlags
// (configfile.go).

func TestConfigFileCrawl(t *testing.T) {
	path := writeConfigFile(t, `{"url":"https://file.example/","limit":50,"delay":700,
		"ngrams":true,"retries":1,"pathOnlyFilters":true,"respectRobots":true}`)

	c := mustRun(t, crawlCmd(), "--config", path, "--no-db").config
	if c.SeedURL != "https://file.example/" || c.MaxPages != 50 || c.DelayMs != 700 || !c.Ngrams {
		t.Errorf("file values lost: seed=%q max=%d delay=%d ngrams=%v", c.SeedURL, c.MaxPages, c.DelayMs, c.Ngrams)
	}
	if c.Retries == nil || *c.Retries != 1 || !c.PathOnlyFilters || !c.RespectRobots {
		t.Errorf("retries/pathOnly/robots = %v/%v/%v", c.Retries, c.PathOnlyFilters, c.RespectRobots)
	}
	if c.Mode != types.ModeSpider {
		t.Errorf("mode = %v, want spider", c.Mode)
	}

	c = mustRun(t, crawlCmd(), "other.example", "--config", path, "--no-db", "--max-pages", "20").config
	if c.SeedURL != "https://other.example" || c.MaxPages != 20 || c.DelayMs != 700 {
		t.Errorf("arg/flag must override file: seed=%q max=%d delay=%d", c.SeedURL, c.MaxPages, c.DelayMs)
	}
}

func TestConfigFileList(t *testing.T) {
	path := writeConfigFile(t, `{"mode":"list","urlList":"https://example.com/a\nhttps://example.com/b","ngrams":true}`)
	c := mustRun(t, listCmd(), "--config", path, "--no-db").config
	if c.Mode != types.ModeList || len(c.URLs) != 2 || c.MaxPages != 2 || !c.Ngrams || c.SeedURL != "https://example.com/a" {
		t.Errorf("list from config: mode=%v urls=%v max=%d ngrams=%v seed=%q", c.Mode, c.URLs, c.MaxPages, c.Ngrams, c.SeedURL)
	}
	file := urlListFile(t, "https://example.com/z")
	c = mustRun(t, listCmd(), file, "--config", path, "--no-db").config
	if len(c.URLs) != 1 || c.URLs[0] != "https://example.com/z" {
		t.Errorf("file arg must replace config urls: %v", c.URLs)
	}
}

func TestConfigFileSitemap(t *testing.T) {
	path := writeConfigFile(t, `{"mode":"sitemap","sitemapUrls":["https://example.com/s.xml"],"limit":30}`)
	c := mustRun(t, sitemapCmd(), "--config", path, "--no-db").config
	if c.Mode != types.ModeSitemap || len(c.SitemapURLs) != 1 || c.MaxPages != 30 || c.SeedURL != "https://example.com/s.xml" {
		t.Errorf("sitemap from config: mode=%v sitemaps=%v max=%d seed=%q", c.Mode, c.SitemapURLs, c.MaxPages, c.SeedURL)
	}
}

func TestNoTargetIsAnError(t *testing.T) {
	path := writeConfigFile(t, `{"ngrams":true}`)
	for _, newCmd := range []func() *cobra.Command{crawlCmd, listCmd, sitemapCmd} {
		got, err := runCaptured(t, newCmd(), "--config", path, "--no-db")
		if err == nil || got.calls != 0 {
			t.Errorf("%s: config without URL must fail", newCmd().Name())
		}
		got, err = runCaptured(t, newCmd(), "--no-db")
		if err == nil || got.calls != 0 {
			t.Errorf("%s: no arg and no config must fail", newCmd().Name())
		}
	}
}

// A key missing from the file keeps the flag default: a hand-written config
// without respectRobots must not crawl ignoring robots.txt.
func TestConfigFileMissingKeysKeepFlagDefaults(t *testing.T) {
	path := writeConfigFile(t, `{"url":"https://file.example/"}`)
	c := mustRun(t, crawlCmd(), "--config", path, "--no-db").config
	if !c.RespectRobots || !c.RenderBlockRes || c.Language != "en" || c.GSCDays != 90 ||
		c.UserAgent != "Micelio/1.0" || c.LIMaxSuggestions != 20 || c.DelayMs != 200 {
		t.Errorf("flag defaults lost: robots=%v block=%v lang=%q gscDays=%d ua=%q li=%d delay=%d",
			c.RespectRobots, c.RenderBlockRes, c.Language, c.GSCDays, c.UserAgent, c.LIMaxSuggestions, c.DelayMs)
	}

	path = writeConfigFile(t, `{"url":"https://file.example/","respectRobots":false,"language":"es"}`)
	c = mustRun(t, crawlCmd(), "--config", path, "--no-db").config
	if c.RespectRobots || c.Language != "es" {
		t.Errorf("file values must beat flag defaults: robots=%v lang=%q", c.RespectRobots, c.Language)
	}
}

// A config file without "limit" must not cap list or sitemap at the
// dashboard default of 1000 pages.
func TestConfigFileWithoutLimitIsNotCapped(t *testing.T) {
	urls := make([]string, 1500)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://example.com/p%d", i)
	}
	list, _ := json.Marshal(map[string]string{"urlList": strings.Join(urls, "\n")})
	c := mustRun(t, listCmd(), "--config", writeConfigFile(t, string(list)), "--no-db").config
	if len(c.URLs) != 1500 || c.MaxPages != 1500 {
		t.Errorf("list: urls=%d maxPages=%d, want 1500/1500", len(c.URLs), c.MaxPages)
	}

	limited, _ := json.Marshal(map[string]any{"urlList": strings.Join(urls, "\n"), "limit": 10})
	c = mustRun(t, listCmd(), "--config", writeConfigFile(t, string(limited)), "--no-db").config
	if c.MaxPages != 10 {
		t.Errorf("list with limit 10: maxPages=%d", c.MaxPages)
	}

	path := writeConfigFile(t, `{"sitemapUrls":["https://example.com/s.xml"]}`)
	c = mustRun(t, sitemapCmd(), "--config", path, "--no-db").config
	if c.MaxPages != 100000 {
		t.Errorf("sitemap without limit: maxPages=%d, want 100000", c.MaxPages)
	}
}
