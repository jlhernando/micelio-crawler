package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/micelio/micelio/internal/types"
)

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigFileMapsDashboardFields(t *testing.T) {
	path := writeConfigFile(t, `{
		"url": "https://example.com/",
		"limit": 50,
		"depth": 3,
		"delay": 700,
		"retries": 0,
		"ngrams": true,
		"pathOnlyFilters": true,
		"saveHtml": true,
		"urlList": "https://example.com/a\n\n https://example.com/b \n"
	}`)
	cfg, limit, err := loadConfigFile(path, types.CrawlConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SeedURL != "https://example.com/" || cfg.MaxPages != 50 || cfg.MaxDepth != 3 || cfg.DelayMs != 700 {
		t.Errorf("seed/limit/depth/delay = %q/%d/%d/%d", cfg.SeedURL, cfg.MaxPages, cfg.MaxDepth, cfg.DelayMs)
	}
	if limit != 50 {
		t.Errorf("file limit = %d, want 50", limit)
	}
	if _, limit, _ := loadConfigFile(writeConfigFile(t, `{"url":"https://example.com/"}`), types.CrawlConfig{}); limit != 0 {
		t.Errorf("file without limit: limit = %d, want 0", limit)
	}
	if cfg.Retries == nil || *cfg.Retries != 0 {
		t.Errorf("retries = %v, want explicit 0", cfg.Retries)
	}
	if !cfg.Ngrams || !cfg.PathOnlyFilters || !cfg.SaveHTML {
		t.Errorf("ngrams/pathOnlyFilters/saveHtml = %v/%v/%v", cfg.Ngrams, cfg.PathOnlyFilters, cfg.SaveHTML)
	}
	want := []string{"https://example.com/a", "https://example.com/b"}
	if strings.Join(cfg.URLs, ",") != strings.Join(want, ",") {
		t.Errorf("urls = %v, want %v", cfg.URLs, want)
	}
}

func TestLoadConfigFileErrors(t *testing.T) {
	if _, _, err := loadConfigFile(filepath.Join(t.TempDir(), "missing.json"), types.CrawlConfig{}); err == nil {
		t.Error("missing file: want error")
	}
	bad := writeConfigFile(t, `{"url": `)
	_, _, err := loadConfigFile(bad, types.CrawlConfig{})
	if err == nil || !strings.Contains(err.Error(), bad) {
		t.Errorf("bad JSON: err = %v, want error naming %s", err, bad)
	}
}

// overlayCmd registers a few real crawl flag names on a bare command.
func overlayCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	f := cmd.Flags()
	f.Int("max-pages", 1000, "")
	f.Int("delay", 200, "")
	f.Bool("ngrams", false, "")
	f.Bool("no-robots", false, "")
	f.Int("retries", 2, "")
	f.StringArray("header", nil, "")
	f.StringArray("extract", nil, "")
	f.Bool("path-only-filters", false, "")
	f.String("ai-key", "", "")
	f.String("psi-key", "", "")
	if err := f.Parse(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestOverlayFlagsOnlyChangedFlagsWin(t *testing.T) {
	one := 1
	base := types.CrawlConfig{
		SeedURL: "https://file.example/", MaxPages: 50, DelayMs: 700, Ngrams: true,
		RespectRobots: true, Retries: &one, PathOnlyFilters: true,
		CustomHeaders: map[string]string{"X-File": "1"},
	}
	five := 5
	fromFlags := types.CrawlConfig{
		SeedURL: "https://flag.example/", MaxPages: 20, DelayMs: 200, DelayExplicit: true,
		Ngrams: false, RespectRobots: false, Retries: &five, PathOnlyFilters: false,
		CustomHeaders:     map[string]string{"X-Flag": "1"},
		CustomExtractions: []types.CustomExtractionRule{{Name: "h1"}},
	}

	// Nothing changed: base stays as it is.
	got := overlayFlags(overlayCmd(t), base, fromFlags)
	if got.MaxPages != 50 || got.DelayMs != 700 || !got.Ngrams || !got.RespectRobots ||
		*got.Retries != 1 || !got.PathOnlyFilters || got.CustomHeaders["X-File"] != "1" ||
		len(got.CustomExtractions) != 0 || got.SeedURL != "https://file.example/" {
		t.Errorf("no flags changed, base was overridden: %+v", got)
	}

	// Changed flags win, including the inverted --no-robots.
	got = overlayFlags(overlayCmd(t, "--max-pages", "20", "--delay", "200", "--no-robots",
		"--retries", "5", "--header", "X-Flag: 1", "--extract", "h1:h1"), base, fromFlags)
	if got.MaxPages != 20 || got.DelayMs != 200 || !got.DelayExplicit || got.RespectRobots ||
		*got.Retries != 5 || got.CustomHeaders["X-Flag"] != "1" || len(got.CustomExtractions) != 1 {
		t.Errorf("changed flags did not win: %+v", got)
	}
	if !got.Ngrams || !got.PathOnlyFilters {
		t.Errorf("unchanged flags overrode base: ngrams=%v pathOnly=%v", got.Ngrams, got.PathOnlyFilters)
	}
}

func TestOverlayFlagsFillsEmptyKeysFromEnvironment(t *testing.T) {
	base := types.CrawlConfig{AIKey: "from-file"}
	fromFlags := types.CrawlConfig{AIKey: "from-env", PSIKey: "psi-from-env"}
	got := overlayFlags(overlayCmd(t), base, fromFlags)
	if got.AIKey != "from-file" {
		t.Errorf("AIKey = %q, file value must win over env", got.AIKey)
	}
	if got.PSIKey != "psi-from-env" {
		t.Errorf("PSIKey = %q, empty file key must take env value", got.PSIKey)
	}
}
