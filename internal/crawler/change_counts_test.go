package crawler

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// Embedding the original interface hides the optional flag-only projection.
type streamingChangeStore struct{ CrawlStore }

type failingChangeCounter struct{ *storage.CrawlStore }

func (*failingChangeCounter) ContentChangeCounts() (int, int, error) {
	return 0, 0, errors.New("change count unavailable")
}

func TestCountStoredChangesCustomStoreFallback(t *testing.T) {
	db, err := storage.NewCrawlStore(filepath.Join(t.TempDir(), "counts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	changed, unchanged := true, false
	for _, p := range []*types.PageData{
		{URL: "https://example.test/changed", ContentChanged: &changed},
		{URL: "https://example.test/unchanged", ContentChanged: &unchanged},
		{URL: "https://example.test/unknown"},
	} {
		if err := db.InsertPage(p); err != nil {
			t.Fatal(err)
		}
	}
	c, u, err := countStoredChanges(streamingChangeStore{db})
	if err != nil || c != 1 || u != 1 {
		t.Fatalf("counts=%d/%d err=%v", c, u, err)
	}
}

func TestStoreOnlyChangeCountErrorPropagates(t *testing.T) {
	site := storeOnlySite(t)
	defer site.Close()
	db, err := storage.NewCrawlStore(filepath.Join(t.TempDir(), "counts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = Crawl(context.Background(), storeOnlyConfig(site.URL), nil, &failingChangeCounter{db})
	if err == nil || !strings.Contains(err.Error(), "count persisted content changes") {
		t.Fatalf("change count error lost: %v", err)
	}
}
