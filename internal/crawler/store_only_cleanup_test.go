package crawler

import (
	"context"
	"errors"
	"github.com/micelio/micelio/internal/storage"
	"os"
	"path/filepath"
	"testing"
)

type countErrorStore struct{ *storage.CrawlStore }

func (*countErrorStore) PageCount() (int, error) { return 0, errors.New("count unavailable") }
func TestCrawlCountFailureClosesLinkStore(t *testing.T) {
	srv := storeOnlySite(t)
	defer srv.Close()
	db, err := storage.NewCrawlStore(filepath.Join(t.TempDir(), "rows.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := storeOnlyConfig(srv.URL)
	cfg.StoreOnly = false
	var linkPath string
	_, err = CrawlWithTuner(context.Background(), cfg, nil, []CrawlStore{&countErrorStore{db}}, nil, nil, func(tu *Tuner) { linkPath = tu.s.intLinksDisk.file.Name() })
	if err == nil {
		t.Fatal("count error swallowed")
	}
	if linkPath == "" {
		t.Fatal("test never created link store")
	}
	if _, err = os.Stat(linkPath); !os.IsNotExist(err) {
		t.Fatalf("link store not removed: %v", err)
	}
}
func TestCrawlResultOwnsLinkIterator(t *testing.T) {
	srv := storeOnlySite(t)
	defer srv.Close()
	cfg := storeOnlyConfig(srv.URL)
	cfg.StoreOnly = false
	r, err := Crawl(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.InternalLinksClose()
	links := 0
	if err = r.InternalLinksIter(func(_ string, target []string) { links += len(target) }); err != nil || links == 0 {
		t.Fatalf("iterator closed too early: %d %v", links, err)
	}
}
