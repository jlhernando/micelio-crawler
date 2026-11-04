package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/storage"
)

// Crawl databases written before configs were scrubbed lose their stored
// credentials on the first start, other settings stay, and the sweep runs
// only once.
func TestScrubLegacyCrawlConfigs(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "old.db")
	cs, err := storage.NewCrawlStore(p)
	if err != nil {
		t.Fatal(err)
	}
	cs.SaveMeta("config", `{"seedUrl":"https://shop.example/","maxPages":100,"cookies":"sid=COOKIE-SECRET","customHeaders":{"Authorization":"Bearer HEADER-SECRET"},"psiKey":"PSI-SECRET"}`)
	cs.Close()

	scrubLegacyCrawlConfigs(dir)

	cs, err = storage.NewCrawlStore(p)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := cs.GetMeta("config")
	cs.Close()
	for _, w := range []string{"COOKIE-SECRET", "HEADER-SECRET", "PSI-SECRET"} {
		if strings.Contains(raw, w) {
			t.Errorf("%s still stored: %s", w, raw)
		}
	}
	if !strings.Contains(raw, "shop.example") || !strings.Contains(raw, `"maxPages":100`) {
		t.Errorf("settings lost: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, scrubbedMarker)); err != nil {
		t.Fatalf("marker not written: %v", err)
	}

	// Second start: the marker skips the sweep.
	cs, _ = storage.NewCrawlStore(p)
	cs.SaveMeta("config", `{"psiKey":"LATER-SECRET"}`)
	cs.Close()
	scrubLegacyCrawlConfigs(dir)
	cs, _ = storage.NewCrawlStore(p)
	raw, _ = cs.GetMeta("config")
	cs.Close()
	if !strings.Contains(raw, "LATER-SECRET") {
		t.Errorf("sweep ran again despite the marker")
	}
}
