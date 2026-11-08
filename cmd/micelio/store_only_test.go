package main

import (
	"fmt"
	"github.com/micelio/micelio/internal/storage"
	"github.com/micelio/micelio/internal/types"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLINoAnalysisPersistsAndReportsCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nAllow: /\n")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><title>Product</title><a href="/second">Next</a></html>`)
	}))
	defer srv.Close()
	out := filepath.Join(t.TempDir(), "crawl.jsonl")
	cfg := types.CrawlConfig{SeedURL: srv.URL, MaxPages: 2, MaxDepth: 2, Concurrency: 1, DelayExplicit: true, SkipSSRF: true, RespectRobots: true, NoSoft404Probe: true, NoAgentCheck: true, NoDB: true, OutputPath: out, OutputFormat: types.FormatJSONL}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	err = runCrawlWithProgress(cfg, nil, nil)
	w.Close()
	os.Stderr = old
	body, readErr := io.ReadAll(r)
	r.Close()
	if err != nil || readErr != nil {
		t.Fatalf("crawl=%v read=%v", err, readErr)
	}
	if !strings.Contains(string(body), "Done! 2 pages crawled") || strings.Contains(string(body), "Reloaded") {
		t.Fatalf("unexpected CLI output: %s", body)
	}
	pages, err := storage.ReadJSONLPages(out)
	if err != nil || len(pages) != 2 {
		t.Fatalf("stored=%d err=%v", len(pages), err)
	}
	if _, err = os.Stat(summaryFilePath(out)); !os.IsNotExist(err) {
		t.Fatalf("no-analysis wrote summary: %v", err)
	}
}
