package crawler

import (
	"context"
	"encoding/csv"
	"fmt"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type reloadSpyStore struct {
	*storage.CrawlStore
	loads int
}

func (s *reloadSpyStore) GetAllPagesWithProgress(ctx context.Context, f func(int)) ([]*types.PageData, error) {
	s.loads++
	return s.CrawlStore.GetAllPagesWithProgress(ctx, f)
}
func (s *reloadSpyStore) GetAllPages() ([]*types.PageData, error) {
	s.loads++
	return s.CrawlStore.GetAllPages()
}

func storeOnlySite(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /blocked\n")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><title>Example</title><a href="/good">good</a><a href="/blocked">blocked</a></html>`)
	}))
}
func storeOnlyConfig(seed string) types.CrawlConfig {
	return types.CrawlConfig{SeedURL: seed, StoreOnly: true, SkipSSRF: true, MaxPages: 10, MaxDepth: 3, Concurrency: 1, DelayExplicit: true, RespectRobots: true, ShowBlockedInternal: true, NoSoft404Probe: true, NoAgentCheck: true}
}

func TestStoreOnlyPersistsWithoutReload(t *testing.T) {
	srv := storeOnlySite(t)
	defer srv.Close()
	db, err := storage.NewCrawlStore(filepath.Join(t.TempDir(), "crawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	spy := &reloadSpyStore{CrawlStore: db}
	cfg := storeOnlyConfig(srv.URL)
	var analysisLoading bool
	r, err := CrawlWithOptions(context.Background(), cfg, nil, []CrawlStore{spy}, nil, func(phase string, _, _ int) {
		if phase == "analysis_loading" {
			analysisLoading = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Pages != nil || r.PageCount != 3 || spy.loads != 0 || analysisLoading {
		t.Fatalf("pages=%d count=%d reloads=%d loading=%t", len(r.Pages), r.PageCount, spy.loads, analysisLoading)
	}
	if r.AnchorAggregator != nil || r.InternalLinksIter != nil {
		t.Fatal("store-only retained analysis collectors")
	}
	n, err := db.PageCount()
	if err != nil || n != 3 {
		t.Fatalf("stored=%d err=%v", n, err)
	}
	// A later analysis still has the persisted links and rows.
	pages, err := db.GetAllPages()
	if err != nil || len(pages) != 3 {
		t.Fatalf("reload=%d err=%v", len(pages), err)
	}
	found := false
	err = db.IterateInternalLinks(func(_ string, links []string) { found = found || len(links) > 0 })
	if err != nil || !found {
		t.Fatalf("persisted links absent: %v", err)
	}
	// Resumption counts the previous rows without materializing them again.
	cfg.Resume = true
	resumed, err := Crawl(context.Background(), cfg, nil, spy)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.PageCount != 3 || resumed.Pages != nil || spy.loads != 0 {
		t.Fatalf("resume count=%d pages=%d reloads=%d", resumed.PageCount, len(resumed.Pages), spy.loads)
	}
}

func TestStoreOnlyFileFormats(t *testing.T) {
	for _, format := range []types.OutputFormat{types.FormatJSONL, types.FormatCSV} {
		t.Run(string(format), func(t *testing.T) {
			srv := storeOnlySite(t)
			defer srv.Close()
			cfg := storeOnlyConfig(srv.URL)
			cfg.NoDB = true
			cfg.OutputFormat = format
			cfg.OutputPath = filepath.Join(t.TempDir(), "rows."+string(format))
			r, err := Crawl(context.Background(), cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if r.Pages != nil || r.PageCount != 3 {
				t.Fatalf("count=%d pages=%d", r.PageCount, len(r.Pages))
			}
			body, err := os.ReadFile(cfg.OutputPath)
			if err != nil {
				t.Fatal(err)
			}
			if format == types.FormatCSV {
				records, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
				if err != nil || len(records) != 4 {
					t.Fatalf("CSV rows=%d err=%v", len(records), err)
				}
			} else {
				rows, err := storage.ReadJSONLPages(cfg.OutputPath)
				if err != nil || len(rows) != 3 {
					t.Fatalf("JSONL rows=%d err=%v", len(rows), err)
				}
			}
		})
	}
}

func TestStoreOnlyRequiresPersistence(t *testing.T) {
	srv := storeOnlySite(t)
	defer srv.Close()
	_, err := Crawl(context.Background(), storeOnlyConfig(srv.URL), nil)
	if err == nil || !strings.Contains(err.Error(), "requires an output") {
		t.Fatalf("err=%v", err)
	}
}

func TestStoreOnlyPreservesRequestedFeatures(t *testing.T) {
	srv := storeOnlySite(t)
	defer srv.Close()
	cfg := storeOnlyConfig(srv.URL)
	cfg.CheckExternal = true
	r, err := Crawl(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.InternalLinksClose != nil {
		defer r.InternalLinksClose()
	}
	if len(r.Pages) != 3 || r.PageCount != 3 {
		t.Fatalf("requested feature lost pages=%d count=%d", len(r.Pages), r.PageCount)
	}
}

func TestStoreOnlyCancellationAndResumeChangeCounts(t *testing.T) {
	srv := storeOnlySite(t)
	defer srv.Close()
	db, err := storage.NewCrawlStore(filepath.Join(t.TempDir(), "resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	changed, unchanged := true, false
	for _, p := range []*types.PageData{{URL: srv.URL, StatusCode: 200, ContentChanged: &changed, InternalLinks: []string{srv.URL + "/new"}}, {URL: srv.URL + "/good", StatusCode: 200, ContentChanged: &unchanged}} {
		if err = db.InsertPage(p); err != nil {
			t.Fatal(err)
		}
	}
	cfg := storeOnlyConfig(srv.URL)
	cfg.Resume = true
	cfg.ShowBlockedInternal = false
	r, err := Crawl(context.Background(), cfg, nil, db)
	if err != nil {
		t.Fatal(err)
	}
	if r.PageCount != 3 || r.ChangedPages != 1 || r.UnchangedPages != 1 || r.Pages != nil {
		t.Fatalf("resume count=%d changed=%d unchanged=%d pages=%d", r.PageCount, r.ChangedPages, r.UnchangedPages, len(r.Pages))
	}
	other, err := storage.NewCrawlStore(filepath.Join(t.TempDir(), "partial.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg.Resume = false
	partial, err := Crawl(ctx, cfg, func(p CrawlProgress) {
		if p.Crawled >= 1 {
			cancel()
		}
	}, other)
	if err != nil {
		t.Fatal(err)
	}
	count, err := other.PageCount()
	if err != nil || count < 1 || partial.PageCount != count || partial.Pages != nil {
		t.Fatalf("partial=%d db=%d err=%v", partial.PageCount, count, err)
	}
}
