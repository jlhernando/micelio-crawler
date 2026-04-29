package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

// TestWriterGzip tests gzip-compressed JSONL write + read round-trip.
func TestWriterGzip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.jsonl.gz")

	w, err := NewResultWriter(path, "jsonl")
	if err != nil {
		t.Fatalf("NewResultWriter: %v", err)
	}

	page := testPage()
	for i := 0; i < 5; i++ {
		if err := w.Write(page); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify file is smaller than raw JSONL (gzip compressed)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	rawJSON, _ := json.Marshal(page)
	rawSize := int64(len(rawJSON)+1) * 5 // 5 pages + newlines
	if info.Size() >= rawSize {
		t.Errorf("Gzip file (%d bytes) not smaller than raw (%d bytes)", info.Size(), rawSize)
	}

	// Read back via ReadJSONLPages
	pages, err := ReadJSONLPages(path)
	if err != nil {
		t.Fatalf("ReadJSONLPages: %v", err)
	}
	if len(pages) != 5 {
		t.Fatalf("Expected 5 pages, got %d", len(pages))
	}
	if pages[0].URL != "https://example.com/test" {
		t.Errorf("URL = %q", pages[0].URL)
	}
}

// TestWriterZstd tests zstd-compressed JSONL write + read round-trip.
func TestWriterZstd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.jsonl.zst")

	w, err := NewResultWriter(path, "jsonl")
	if err != nil {
		t.Fatalf("NewResultWriter: %v", err)
	}

	page := testPage()
	for i := 0; i < 5; i++ {
		if err := w.Write(page); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify file is smaller than raw JSONL
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	rawJSON, _ := json.Marshal(page)
	rawSize := int64(len(rawJSON)+1) * 5
	if info.Size() >= rawSize {
		t.Errorf("Zstd file (%d bytes) not smaller than raw (%d bytes)", info.Size(), rawSize)
	}

	// Read back via ReadJSONLPages
	pages, err := ReadJSONLPages(path)
	if err != nil {
		t.Fatalf("ReadJSONLPages: %v", err)
	}
	if len(pages) != 5 {
		t.Fatalf("Expected 5 pages, got %d", len(pages))
	}
	if pages[0].URL != "https://example.com/test" {
		t.Errorf("URL = %q", pages[0].URL)
	}
}

// TestWriterGzipRewriteAll tests that RewriteAll works with gzip compression.
func TestWriterGzipRewriteAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.jsonl.gz")

	w, err := NewResultWriter(path, "jsonl")
	if err != nil {
		t.Fatalf("NewResultWriter: %v", err)
	}

	page := testPage()
	w.Write(page)
	w.Write(page)

	// Rewrite with only 1 page
	pages := []*types.PageData{page}
	if err := w.RewriteAll(pages); err != nil {
		t.Fatalf("RewriteAll: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read back — should have 1 page
	readPages, err := ReadJSONLPages(path)
	if err != nil {
		t.Fatalf("ReadJSONLPages: %v", err)
	}
	if len(readPages) != 1 {
		t.Fatalf("Expected 1 page after rewrite, got %d", len(readPages))
	}
}

func testPage() *types.PageData {
	title := types.TextLength{Text: "Test Title", Length: 10}
	desc := types.TextLength{Text: "Test description", Length: 16}
	canonical := "https://example.com/test"
	return &types.PageData{
		URL:             "https://example.com/test",
		FinalURL:        "https://example.com/test",
		StatusCode:      200,
		ResponseTimeMs:  150,
		Title:           &title,
		MetaDescription: &desc,
		Canonical:       &canonical,
		Headings:        types.HeadingData{H1: []string{"Test"}, H2: []string{"Sub1", "Sub2"}},
		InternalLinks:   []string{"/a", "/b"},
		ExternalLinks:   []string{"https://other.com"},
		WordCount:       100,
		ContentHash:     "abc123",
		Indexability:    types.IndexabilityData{Indexable: true},
		Security:        types.SecurityData{IsHTTPS: true},
	}
}

func TestWriterJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.jsonl")

	w, err := NewResultWriter(path, "jsonl")
	if err != nil {
		t.Fatalf("NewResultWriter: %v", err)
	}

	page := testPage()
	if err := w.Write(page); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read back
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("Expected 1 line, got %d", len(lines))
	}

	var decoded types.PageData
	if err := json.Unmarshal([]byte(lines[0]), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.URL != "https://example.com/test" {
		t.Errorf("URL = %q", decoded.URL)
	}
	if decoded.Title == nil || decoded.Title.Text != "Test Title" {
		t.Errorf("Title = %v", decoded.Title)
	}
}

func TestWriterCSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.csv")

	w, err := NewResultWriter(path, "csv")
	if err != nil {
		t.Fatalf("NewResultWriter: %v", err)
	}

	page := testPage()
	if err := w.Write(page); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Read back
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 { // header + 1 row
		t.Fatalf("Expected 2 lines (header+data), got %d", len(lines))
	}

	// Check header has expected columns
	if !strings.Contains(lines[0], "url") || !strings.Contains(lines[0], "status_code") {
		t.Error("CSV header missing expected columns")
	}
}

func TestWriterCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.jsonl")

	w, err := NewResultWriter(path, "jsonl")
	if err != nil {
		t.Fatalf("NewResultWriter: %v", err)
	}

	page := testPage()
	w.Write(page)
	w.Write(page)
	w.Write(page)

	if w.Count() != 3 {
		t.Errorf("Count = %d, want 3", w.Count())
	}
	w.Close()
}
