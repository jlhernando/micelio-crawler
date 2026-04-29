package storage

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func sampleHeadResult() *types.HeadResult {
	xRobots := "noindex"
	canonical := "https://example.com/"
	cacheControl := "max-age=3600"
	contentLen := int64(12345)
	return &types.HeadResult{
		URL:        "https://example.com/page",
		FinalURL:   "https://example.com/page",
		StatusCode: 200,
		RedirectChain: []types.RedirectHop{
			{URL: "http://example.com/page", StatusCode: 301},
		},
		ResponseTimeMs: 150,
		ContentType:    "text/html",
		ContentLength:  &contentLen,
		Server:         "nginx",
		XRobotsTag:     &xRobots,
		LinkCanonical:  &canonical,
		HSTS:           true,
		CSP:            false,
		CacheControl:   &cacheControl,
	}
}

func TestHeadResultWriterJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "head.jsonl")

	w, err := NewHeadResultWriter(path, "jsonl")
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Write(sampleHeadResult()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if w.Count() != 1 {
		t.Fatalf("expected count 1, got %d", w.Count())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, `"statusCode":200`) {
		t.Fatal("expected statusCode in JSONL output")
	}
	if !strings.Contains(content, `"url":"https://example.com/page"`) {
		t.Fatal("expected URL in JSONL output")
	}
}

func TestHeadResultWriterCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "head.csv")

	w, err := NewHeadResultWriter(path, "csv")
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Write(sampleHeadResult()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	f, _ := os.Open(path)
	defer f.Close()
	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	if len(records) != 2 { // header + 1 row
		t.Fatalf("expected 2 rows (header + data), got %d", len(records))
	}

	// Check header
	headers := records[0]
	if headers[0] != "url" || headers[2] != "status_code" {
		t.Fatalf("unexpected headers: %v", headers)
	}

	// Check data row
	row := records[1]
	if row[0] != "https://example.com/page" {
		t.Fatalf("expected URL, got %q", row[0])
	}
	if row[2] != "200" {
		t.Fatalf("expected status 200, got %q", row[2])
	}
	if row[3] != "1" { // redirect_chain_length
		t.Fatalf("expected redirect chain length 1, got %q", row[3])
	}
	if !strings.Contains(row[4], "301:http://example.com/page") {
		t.Fatalf("expected redirect chain, got %q", row[4])
	}
}
