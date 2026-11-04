package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/secrets"
	"github.com/micelio/micelio/internal/types"
)

var testSecrets = []string{"cookie-secret", "header-secret", "proxy-secret", "psi-secret", "ai-secret", "tor-secret", "slack-secret"}

func secretConfig() types.CrawlConfig {
	return types.CrawlConfig{
		SeedURL:       "https://example.com/",
		MaxPages:      7,
		Cookies:       "sid=cookie-secret",
		CustomHeaders: map[string]string{"Authorization": "Bearer header-secret", "Accept-Language": "es"},
		Proxy:         "http://bob:proxy-secret@proxy.example:8080",
		PSIKey:        "psi-secret",
		AIKey:         "ai-secret",
		TorPassword:   "tor-secret",
	}
}

// fileHasSecret reports a secret anywhere in the database files, also in
// free space or the WAL.
func fileHasSecret(t *testing.T, dbPath string) string {
	t.Helper()
	for _, p := range []string{dbPath, dbPath + "-wal"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, s := range testSecrets {
			if bytes.Contains(data, []byte(s)) {
				return s + " in " + filepath.Base(p)
			}
		}
	}
	return ""
}

func TestSaveConfigDropsSecrets(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "crawl.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveConfig(secretConfig()); err != nil {
		t.Fatal(err)
	}
	raw, _ := store.GetMeta("config")
	for _, s := range testSecrets {
		if strings.Contains(raw, s) {
			t.Errorf("stored config contains %q: %s", s, raw)
		}
	}
	cfg, err := store.GetConfig()
	if err != nil || cfg == nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.MaxPages != 7 || cfg.CustomHeaders["Accept-Language"] != "es" || cfg.Proxy != "http://bob:"+secrets.Mask+"@proxy.example:8080" {
		t.Errorf("settings not kept: maxPages %d headers %v proxy %q", cfg.MaxPages, cfg.CustomHeaders, cfg.Proxy)
	}
	store.Close()
	if s := fileHasSecret(t, dbPath); s != "" {
		t.Errorf("crawl DB file holds a secret: %s", s)
	}
}

func TestScrubConfigLegacyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// A config written by an older version, with a field this one does not
	// know and an extra credential.
	legacy := `{"seedUrl":"https://example.com/","maxPages":7,"cookies":"sid=cookie-secret","customHeaders":{"Authorization":"Bearer header-secret"},"proxy":"http://bob:proxy-secret@proxy.example:8080","psiKey":"psi-secret","slackWebhook":"https://hooks.slack.com/slack-secret","futureSetting":true}`
	store.SaveMeta("config", legacy)
	store.Close()

	store, err = NewCrawlStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ScrubConfig(); err != nil {
		t.Fatal(err)
	}
	raw, _ := store.GetMeta("config")
	if !strings.Contains(raw, `"futureSetting":true`) || !strings.Contains(raw, `"maxPages":7`) {
		t.Errorf("non-secret fields must be kept: %s", raw)
	}
	store.Close()
	if s := fileHasSecret(t, dbPath); s != "" {
		t.Errorf("crawl DB file holds a secret after ScrubConfig (free space?): %s", s)
	}

	// No config: nothing to do.
	empty := filepath.Join(t.TempDir(), "empty.db")
	store, err = NewCrawlStore(empty)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.ScrubConfig(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := store.GetMeta("config"); raw != "" {
		t.Errorf("config created: %q", raw)
	}
}

func TestCrawlDBOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "new.db")
	store, err := NewCrawlStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store.SaveConfig(types.CrawlConfig{SeedURL: "https://example.com/"})
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", filepath.Base(p), fi.Mode().Perm())
		}
	}
	store.Close()

	// A database made by an older version is tightened when opened.
	old := filepath.Join(dir, "old.db")
	os.Rename(dbPath, old)
	os.Chmod(old, 0o644)
	store, err = NewCrawlStore(old)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if fi, _ := os.Stat(old); fi.Mode().Perm() != 0o600 {
		t.Errorf("existing DB mode = %v, want 0600", fi.Mode().Perm())
	}
}
