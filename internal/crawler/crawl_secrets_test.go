package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// TestCrawlDBConfigHasNoSecrets: the crawl database keeps the crawl config
// without credentials, and a CLI --db --resume crawl (credentials from its
// own flags) still sends them.
func TestCrawlDBConfigHasNoSecrets(t *testing.T) {
	var mu sync.Mutex
	authed, total := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		total++
		if r.Header.Get("Authorization") == "Bearer header-secret" && strings.Contains(r.Header.Get("Cookie"), "cookie-secret") {
			authed++
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			html := "<html><head><title>Home</title></head><body>"
			for i := 1; i < 7; i++ {
				html += fmt.Sprintf(`<a href="/page%d">Page %d</a>`, i, i)
			}
			fmt.Fprint(w, html+"</body></html>")
			return
		}
		fmt.Fprintf(w, `<html><head><title>%s</title></head><body><a href="/">Home</a></body></html>`, r.URL.Path)
	}))
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "crawl.db")
	config := types.CrawlConfig{
		SeedURL:       srv.URL,
		Mode:          types.ModeSpider,
		MaxDepth:      2,
		MaxPages:      3,
		Concurrency:   1,
		UserAgent:     "TestBot/1.0",
		SkipSSRF:      true,
		NoAgentCheck:  true,
		Cookies:       "sid=cookie-secret",
		CustomHeaders: map[string]string{"Authorization": "Bearer header-secret"},
		PSIKey:        "psi-secret",
		AIKey:         "ai-secret",
		TorPassword:   "tor-secret",
	}
	secrets := []string{"cookie-secret", "header-secret", "psi-secret", "ai-secret", "tor-secret"}
	checkMeta := func(store *storage.CrawlStore) {
		t.Helper()
		raw, err := store.GetMeta("config")
		if err != nil || raw == "" {
			t.Fatalf("no stored config: %v", err)
		}
		for _, s := range secrets {
			if strings.Contains(raw, s) {
				t.Errorf("crawl DB config contains %q", s)
			}
		}
	}

	store1, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := Crawl(context.Background(), config, nil, store1)
	if err != nil {
		t.Fatal(err)
	}
	checkMeta(store1)
	store1.Close()

	store2, err := storage.NewCrawlStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	config.MaxPages = 10
	config.Resume = true
	r2, err := Crawl(context.Background(), config, nil, store2)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Pages) <= len(r1.Pages) {
		t.Errorf("resume should crawl more pages: first=%d, resumed=%d", len(r1.Pages), len(r2.Pages))
	}
	checkMeta(store2)

	mu.Lock()
	defer mu.Unlock()
	// robots.txt and probes may go without them; every page carries them.
	if authed < len(r2.Pages) {
		t.Errorf("%d of %d requests carried the credentials, want every page (%d)", authed, total, len(r2.Pages))
	}
}
