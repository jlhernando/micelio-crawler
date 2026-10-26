package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
)

// testSitePages is a small site: home links to 5 sections, each section to
// 3 products; one product is a 404.
func testSitePages() []*types.PageData {
	base := "https://shop.example"
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	var pages []*types.PageData
	home := &types.PageData{URL: base + "/", StatusCode: 200, Depth: 0, CrawledAt: t0}
	pages = append(pages, home)
	n := 1
	for s := 0; s < 5; s++ {
		sec := fmt.Sprintf("%s/c/s%d/", base, s)
		home.InternalLinks = append(home.InternalLinks, sec)
		home.Anchors = append(home.Anchors, types.AnchorData{Href: sec, Text: "click here", IsInternal: true})
		sp := &types.PageData{URL: sec, StatusCode: 200, Depth: 1, InternalLinks: []string{base + "/"}, CrawledAt: t0.Add(time.Duration(n) * time.Second)}
		n++
		pages = append(pages, sp)
		for p := 0; p < 3; p++ {
			u := fmt.Sprintf("%s/p/s%d-p%d/", base, s, p)
			sp.InternalLinks = append(sp.InternalLinks, u)
			code := 200
			if s == 4 && p == 2 {
				code = 404
			}
			pages = append(pages, &types.PageData{URL: u, StatusCode: code, Depth: 2, InternalLinks: []string{sec}, CrawledAt: t0.Add(time.Duration(n) * time.Second)})
			n++
		}
	}
	return pages
}

func writeTestCrawlDB(t *testing.T, path string) {
	t.Helper()
	cs, err := storage.NewCrawlStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if err := cs.SaveConfig(types.CrawlConfig{SeedURL: "https://shop.example/", Mode: types.ModeSpider, MaxPages: 100, EmbeddingKey: "secret"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range testSitePages() {
		if err := cs.InsertPage(p); err != nil {
			t.Fatal(err)
		}
	}
	cs.SetStatus("complete")
}

func checkImported(t *testing.T, s *UiStore, m *CrawlManager, job *CrawlJob) {
	t.Helper()
	m.WaitBackground()
	got, err := s.GetCrawlJob(job.ID)
	if err != nil || got == nil {
		t.Fatalf("job not found: %v", err)
	}
	if got.Status != "completed" || got.PageCount != 21 || got.ErrorCount != 1 {
		t.Errorf("job = status %s, %d pages, %d errors; want completed, 21, 1", got.Status, got.PageCount, got.ErrorCount)
	}
	if got.SeedURL != "https://shop.example/" {
		t.Errorf("seed = %q", got.SeedURL)
	}
	if _, ok := got.Config["embeddingKey"]; ok {
		t.Error("credentials must not be copied into the imported job config")
	}
	stats, err := s.LoadCrawlStats(job.ID)
	if err != nil || stats == nil {
		t.Fatalf("no stats saved: %v", err)
	}
	if stats["anchorHealthStats"] == nil {
		t.Error("anchor stats should be rebuilt from stored anchors (--full-anchors crawls)")
	}
	if stats["totalPages"] != float64(21) {
		t.Errorf("stats totalPages = %v", stats["totalPages"])
	}
	if _, err := os.Stat(got.DBPath); err != nil {
		t.Errorf("crawl DB not copied: %v", err)
	}
	res := m.GetCachedResults(job.ID)
	if res == nil || len(res.Pages) != 21 {
		t.Fatalf("results not cached")
	}
	if l := buildUniverseLayout(res, "", ""); l == nil || l.errors != 1 {
		t.Errorf("universe layout errors = %v, want 1", l)
	}
}

func TestImportCrawlDB(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	src := filepath.Join(t.TempDir(), "site.db")
	writeTestCrawlDB(t, src)
	before, _ := os.ReadFile(src)

	job, err := m.ImportCrawl(context.Background(), src, ImportOptions{LinkIntelligence: true})
	if err != nil {
		t.Fatal(err)
	}
	checkImported(t, s, m, job)
	if after, _ := os.ReadFile(src); !bytes.Equal(before, after) {
		t.Error("the source database must not be modified")
	}
}

func TestImportCrawlJSONLZstd(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	enc := json.NewEncoder(zw)
	for _, p := range testSitePages() {
		enc.Encode(p)
	}
	zw.Close()
	src := filepath.Join(t.TempDir(), "site.jsonl.zst")
	os.WriteFile(src, buf.Bytes(), 0o644)

	job, err := m.ImportCrawl(context.Background(), src, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	checkImported(t, s, m, job)
	got, _ := s.GetCrawlJob(job.ID)
	if got.DurationMs == nil || *got.DurationMs != 20000 {
		t.Errorf("duration from crawledAt = %v, want 20000", got.DurationMs)
	}
	if left, _ := filepath.Glob(filepath.Join(s.crawlDir, "import-*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestImportRejectsOtherFiles(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	dir := t.TempDir()
	for name, body := range map[string]string{"notes.txt": "hello", "other.jsonl": `{"foo":1}` + "\n"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := m.ImportCrawl(context.Background(), p, ImportOptions{}); !errors.Is(err, ErrNotCrawlFile) {
			t.Errorf("%s: err = %v, want ErrNotCrawlFile", name, err)
		}
	}
	jobs, _ := s.ListCrawlJobs()
	if len(jobs) != 0 {
		t.Errorf("rejected files created %d History entries", len(jobs))
	}
}

func TestImportUploadEndpoint(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	mux := http.NewServeMux()
	registerImportRoutes(mux, s, m)
	src := filepath.Join(t.TempDir(), "site.db")
	writeTestCrawlDB(t, src)
	body, _ := os.ReadFile(src)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/crawls/import?name=../../etc/site.db", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var job CrawlJob
	json.Unmarshal(rec.Body.Bytes(), &job)
	checkImported(t, s, m, &job)
	if left, _ := filepath.Glob(filepath.Join(s.crawlDir, "upload-*")); len(left) > 0 {
		t.Errorf("upload file left behind: %v", left)
	}
	if imp, _ := job.Config["imported"].(map[string]interface{}); imp["file"] != "site.db" {
		t.Errorf("shown name = %v, want site.db (no path)", imp["file"])
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/crawls/import", bytes.NewReader([]byte("nope"))))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad file: status %d, want 400", rec.Code)
	}
}
