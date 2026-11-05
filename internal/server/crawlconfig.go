package server

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// Crawl configs from HTTP (New Crawl, restart, schedules, MCP) use the same
// keys as the CLI, but the CLI's local file fields must not let a request
// pick files on this computer:
//
//   - outputPath and dbPath are never taken from a request. The dashboard
//     keeps every crawl in its own database (~/.micelio/ui/crawls/<id>.db).
//   - Service account keys (gscKeyFile, ga4KeyFile) are read only from
//     ~/.micelio/keys and JS snippets (snippetPaths) only from
//     ~/.micelio/snippets: a bare file name, or an absolute path directly in
//     that folder. Symlinks are refused.
//
// Numbers that size memory or work (page limit, concurrency, depth...) are
// capped (see limits.go).

// micelioSubdir returns ~/.micelio/<name> ("" if there is no home dir).
func micelioSubdir(name string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".micelio", name)
}

func keysDir() string     { return micelioSubdir("keys") }
func snippetsDir() string { return micelioSubdir("snippets") }

// confinedFile resolves v to a regular file directly inside dir. v is a file
// name, or an absolute path whose folder is dir.
func confinedFile(dir, v string) (string, error) {
	v = strings.TrimSpace(v)
	if dir == "" {
		return "", errors.New("no home directory")
	}
	where := strings.Replace(dir, micelioSubdir(""), "~/.micelio", 1)
	name := v
	if strings.ContainsAny(v, `/\`) {
		clean := filepath.Clean(v)
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("%q must be a file name in %s", v, where)
		}
		if filepath.Dir(clean) != filepath.Clean(dir) {
			return "", fmt.Errorf("%s is outside %s", v, where)
		}
		name = filepath.Base(clean)
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return "", fmt.Errorf("%q must be a file name in %s", v, where)
	}
	p := filepath.Join(dir, name)
	fi, err := os.Lstat(p)
	if err != nil {
		return "", fmt.Errorf("%s not found in %s", name, where)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%s in %s must be a regular file (no symlinks)", name, where)
	}
	return p, nil
}

// serverOwnedPathKeys name files the crawl would create or truncate.
var serverOwnedPathKeys = []string{"outputPath", "dbPath"}

// keyFileFlags maps each key file setting to the switch that uses it.
var keyFileFlags = map[string]string{"gscKeyFile": "gsc", "ga4KeyFile": "ga4"}

// Hints added to a refused or dropped key file or snippet, so an old saved
// config (History, schedules) says how to fix it.
const (
	keyFileHint = "move the key file into ~/.micelio/keys and select it again"
	snippetHint = "move the snippet into ~/.micelio/snippets and select it again"
)

// sanitizeCrawlConfig checks a crawl config that came over HTTP and rewrites
// it in place: server-owned paths are dropped, key files and snippets are
// resolved inside their folders and numbers are checked against the limits.
// A path that does not resolve is an error when its feature is on and is
// dropped otherwise (a saved default key file must not block every crawl).
func sanitizeCrawlConfig(m map[string]interface{}) error {
	_, err := cleanCrawlConfig(m, true)
	return err
}

// relaxCrawlConfig is sanitizeCrawlConfig for configs saved before it
// existed (schedules resumed at start): a key file or snippet outside its
// folder is dropped and numbers are clamped instead of refused. It returns
// what it dropped or clamped.
func relaxCrawlConfig(m map[string]interface{}) []string {
	warnings, _ := cleanCrawlConfig(m, false)
	return warnings
}

func cleanCrawlConfig(m map[string]interface{}, strict bool) (warnings []string, err error) {
	if m == nil {
		return nil, nil
	}
	for _, k := range serverOwnedPathKeys {
		delete(m, k)
	}
	for _, key := range []string{"gscKeyFile", "ga4KeyFile"} {
		flag := keyFileFlags[key]
		raw, ok := m[key]
		if !ok {
			continue
		}
		s, _ := raw.(string)
		if strings.TrimSpace(s) == "" {
			delete(m, key)
			continue
		}
		p, err := confinedFile(keysDir(), s)
		if err != nil {
			on, _ := m[flag].(bool)
			if on && strict {
				return nil, fmt.Errorf("%s: %w; %s", key, err, keyFileHint)
			}
			if on {
				warnings = append(warnings, fmt.Sprintf("%s dropped: %v; %s", key, err, keyFileHint))
			}
			delete(m, key)
			continue
		}
		m[key] = p
	}
	if raw, ok := m["snippetPaths"]; ok {
		list, _ := raw.([]interface{})
		js, _ := m["jsRendering"].(bool)
		var out []interface{}
		for _, item := range list {
			s, _ := item.(string)
			if strings.TrimSpace(s) == "" {
				continue
			}
			p, err := confinedFile(snippetsDir(), s)
			if err != nil {
				if js && strict {
					return nil, fmt.Errorf("snippetPaths: %w; %s", err, snippetHint)
				}
				if js {
					warnings = append(warnings, fmt.Sprintf("snippet dropped: %v; %s", err, snippetHint))
				}
				continue
			}
			out = append(out, p)
		}
		if len(out) == 0 {
			delete(m, "snippetPaths")
		} else {
			m["snippetPaths"] = out
		}
	}
	w, err := checkCrawlNumbers(m, strict)
	return append(warnings, w...), err
}

// checkCrawlNumbers rejects numbers above the limits (strict) or clamps them
// and says so. Zero or negative values keep meaning "use the default".
func checkCrawlNumbers(m map[string]interface{}, strict bool) (warnings []string, err error) {
	lim := limits()
	checks := []struct {
		keys  []string
		max   float64
		label string
	}{
		{[]string{"limit", "maxPages"}, float64(lim.MaxCrawlPages), "page limit"},
		{[]string{"liMaxPages"}, float64(lim.MaxCrawlPages), "link intelligence page limit"},
		{[]string{"concurrency"}, maxCrawlConcurrency, "concurrency"},
		{[]string{"depth", "maxDepth"}, maxCrawlDepth, "depth"},
		{[]string{"renderTimeoutSec"}, maxRenderTimeoutSec, "render timeout"},
		{[]string{"liMaxSuggestions"}, maxLISuggestions, "max suggestions"},
	}
	for _, c := range checks {
		for _, k := range c.keys {
			if v, ok := numberValue(m[k]); ok && v > c.max {
				if strict {
					return nil, fmt.Errorf("%s must be at most %d", c.label, int64(c.max))
				}
				m[k] = c.max
				warnings = append(warnings, fmt.Sprintf("%s lowered to %d", c.label, int64(c.max)))
			}
		}
	}
	return warnings, nil
}

func numberValue(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// confineCrawlConfig is the last check before a dashboard crawl runs, for
// configs saved before sanitizeCrawlConfig existed (History, schedules):
// server-owned paths are cleared, key files and snippets outside their
// folders are dropped and numbers are clamped to the limits. It returns a
// note for each key file or snippet it dropped, for the crawl's events.
func confineCrawlConfig(c *types.CrawlConfig) (warnings []string) {
	c.OutputPath = ""
	c.DBPath = ""
	keyFiles := []struct {
		name string
		path *string
		on   bool
	}{{"GSC", &c.GSCKeyFile, c.GSC}, {"GA4", &c.GA4KeyFile, c.GA4}}
	for _, kf := range keyFiles {
		if *kf.path == "" {
			continue
		}
		p, err := confinedFile(keysDir(), *kf.path)
		if err != nil {
			log.Printf("crawl config: dropping %s key file: %v", kf.name, err)
			if kf.on {
				warnings = append(warnings, fmt.Sprintf("%s key file not used: %v; %s", kf.name, err, keyFileHint))
			}
			p = ""
		}
		*kf.path = p
	}
	var snippets []string
	for _, sp := range c.SnippetPaths {
		p, err := confinedFile(snippetsDir(), sp)
		if err != nil {
			log.Printf("crawl config: dropping snippet: %v", err)
			if c.JSRendering {
				warnings = append(warnings, fmt.Sprintf("JS snippet not run: %v; %s", err, snippetHint))
			}
			continue
		}
		snippets = append(snippets, p)
	}
	c.SnippetPaths = snippets
	lim := limits()
	c.MaxPages = min(c.MaxPages, lim.MaxCrawlPages)
	c.LIMaxPages = min(c.LIMaxPages, lim.MaxCrawlPages)
	c.Concurrency = min(c.Concurrency, maxCrawlConcurrency)
	c.MaxDepth = min(c.MaxDepth, maxCrawlDepth)
	c.RenderTimeoutSec = min(c.RenderTimeoutSec, maxRenderTimeoutSec)
	c.LIMaxSuggestions = min(c.LIMaxSuggestions, maxLISuggestions)
	return warnings
}
