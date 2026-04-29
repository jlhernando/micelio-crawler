// Package storage handles crawl output and persistence.
package storage

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/micelio/micelio/internal/types"
)

// ReadJSONLPages reads all PageData entries from a JSONL file.
// Supports plain, gzip (.gz), and zstd (.zst) compressed files.
func ReadJSONLPages(path string) ([]*types.PageData, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open JSONL: %w", err)
	}
	defer f.Close()

	reader, cleanup, err := DecompressReader(f, path)
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	var pages []*types.PageData
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 256*1024), 10*1024*1024) // 10MB max line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var page types.PageData
		if err := json.Unmarshal(line, &page); err != nil {
			return nil, fmt.Errorf("unmarshal page: %w", err)
		}
		// Strip heavy fields not needed by post-crawl analysis to reduce RSS.
		page.BodyText = ""
		if page.PageWeight != nil {
			page.PageWeight.Resources = nil
		}
		pages = append(pages, &page)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read JSONL: %w", err)
	}
	return pages, nil
}

// DecompressReader wraps a file reader with the appropriate decompressor
// based on file extension (.gz for gzip, .zst for zstd).
// Returns the reader, a cleanup function (may be nil), and any error.
func DecompressReader(f *os.File, path string) (io.Reader, func(), error) {
	switch {
	case strings.HasSuffix(path, ".gz"):
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, nil, fmt.Errorf("open gzip reader: %w", err)
		}
		return gz, func() { gz.Close() }, nil
	case strings.HasSuffix(path, ".zst"):
		dec, err := zstd.NewReader(f)
		if err != nil {
			return nil, nil, fmt.Errorf("open zstd reader: %w", err)
		}
		return dec, func() { dec.Close() }, nil
	default:
		return f, nil, nil
	}
}

// ResultWriter writes crawl results to JSONL or CSV format.
// Supports transparent gzip (.gz) and zstd (.zst) compression
// based on the output file extension.
type ResultWriter struct {
	compressor io.WriteCloser
	file       *os.File
	buf        *bufio.Writer
	csv        *csv.Writer
	format     string
	compress   string
	count      int
	closed     bool
}

// NewResultWriter creates a new writer for the given output path and format.
// Compression is auto-detected from file extension: .gz for gzip, .zst for zstd.
func NewResultWriter(path string, format string) (*ResultWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create output file: %w", err)
	}

	w := &ResultWriter{
		format: format,
		file:   f,
	}

	// Auto-detect compression from file extension
	target := io.Writer(f)
	switch {
	case strings.HasSuffix(path, ".gz"):
		gz, err := gzip.NewWriterLevel(f, gzip.DefaultCompression)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("create gzip writer: %w", err)
		}
		w.compressor = gz
		w.compress = "gz"
		target = gz
	case strings.HasSuffix(path, ".zst"):
		enc, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("create zstd writer: %w", err)
		}
		w.compressor = enc
		w.compress = "zst"
		target = enc
	}

	w.buf = bufio.NewWriterSize(target, 64*1024)

	if format == "csv" {
		w.csv = csv.NewWriter(w.buf)
		// Write header row
		if err := w.csv.Write(csvHeaders()); err != nil {
			if w.compressor != nil {
				w.compressor.Close()
			}
			f.Close()
			return nil, fmt.Errorf("write CSV header: %w", err)
		}
	}

	return w, nil
}

// Write writes a single PageData result.
func (w *ResultWriter) Write(page *types.PageData) error {
	if w.format == "csv" {
		return w.writeCSV(page)
	}
	return w.writeJSONL(page)
}

// Count returns the number of results written.
func (w *ResultWriter) Count() int {
	return w.count
}

// Flush flushes buffered data to disk without closing the file.
// Used to ensure all data is on disk before reading it back.
func (w *ResultWriter) Flush() error {
	if w.csv != nil {
		w.csv.Flush()
		if err := w.csv.Error(); err != nil {
			return err
		}
	}
	if err := w.buf.Flush(); err != nil {
		return err
	}
	// Flush compressor to push data to disk (sync point)
	if f, ok := w.compressor.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

// Close flushes and closes the writer. Safe to call multiple times.
func (w *ResultWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if w.csv != nil {
		w.csv.Flush()
	}
	if err := w.buf.Flush(); err != nil {
		if w.compressor != nil {
			w.compressor.Close()
		}
		w.file.Close()
		return err
	}
	if w.compressor != nil {
		if err := w.compressor.Close(); err != nil {
			w.file.Close()
			return err
		}
	}
	return w.file.Close()
}

// RewriteAll rewrites the output file with the given pages (for post-crawl enrichment).
func (w *ResultWriter) RewriteAll(pages []*types.PageData) error {
	// Truncate file
	if err := w.file.Truncate(0); err != nil {
		return err
	}
	if _, err := w.file.Seek(0, 0); err != nil {
		return err
	}

	// Close old compressor to release resources (ignore error — stream is discarded)
	if w.compressor != nil {
		w.compressor.Close()
	}

	// Recreate compression chain for the truncated file
	target := io.Writer(w.file)
	switch w.compress {
	case "gz":
		gz, err := gzip.NewWriterLevel(w.file, gzip.DefaultCompression)
		if err != nil {
			return fmt.Errorf("recreate gzip writer: %w", err)
		}
		w.compressor = gz
		target = gz
	case "zst":
		enc, err := zstd.NewWriter(w.file, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			return fmt.Errorf("recreate zstd writer: %w", err)
		}
		w.compressor = enc
		target = enc
	default:
		w.compressor = nil
	}

	w.buf.Reset(target)
	w.count = 0

	if w.format == "csv" {
		w.csv = csv.NewWriter(w.buf)
		if err := w.csv.Write(csvHeaders()); err != nil {
			return err
		}
	}

	for _, page := range pages {
		if err := w.Write(page); err != nil {
			return err
		}
	}

	if w.csv != nil {
		w.csv.Flush()
	}
	return w.buf.Flush()
}

func (w *ResultWriter) writeJSONL(page *types.PageData) error {
	data, err := json.Marshal(page)
	if err != nil {
		return fmt.Errorf("marshal page: %w", err)
	}
	if _, err := w.buf.Write(data); err != nil {
		return err
	}
	if err := w.buf.WriteByte('\n'); err != nil {
		return err
	}
	w.count++
	return nil
}

func (w *ResultWriter) writeCSV(page *types.PageData) error {
	row := flattenPageForCSV(page)
	if err := w.csv.Write(row); err != nil {
		return err
	}
	w.count++
	return nil
}

// --- CSV Helpers ---

func csvHeaders() []string {
	return []string{
		"url", "final_url", "status_code", "redirect_chain", "redirect_type",
		"response_time_ms", "title", "title_length", "meta_description",
		"meta_description_length", "canonical", "canonical_count",
		"canonical_is_self", "meta_robots", "x_robots_tag",
		"h1", "h1_count", "h2_count",
		"internal_links_count", "external_links_count",
		"images_count", "images_missing_alt",
		"depth", "word_count", "content_hash",
		"text_to_code_ratio", "simhash_fingerprint",
		"indexable", "indexability_reason",
		"is_https", "has_hsts", "has_mixed_content",
		"hreflang_count", "hreflang_langs",
		"structured_data_count", "og_title",
		"readability_score", "url_issues",
		"is_soft_404", "error",
		"template_type", "inlinks", "page_rank", "robots_blocked",
		"url_classification",
	}
}

func flattenPageForCSV(page *types.PageData) []string {
	// Title
	titleText := ""
	titleLen := 0
	if page.Title != nil {
		titleText = page.Title.Text
		titleLen = page.Title.Length
	}

	// Meta description
	descText := ""
	descLen := 0
	if page.MetaDescription != nil {
		descText = page.MetaDescription.Text
		descLen = page.MetaDescription.Length
	}

	// Canonical
	canonical := ""
	if page.Canonical != nil {
		canonical = *page.Canonical
	}
	canonicalIsSelf := "false"
	if page.Canonical != nil && *page.Canonical == page.FinalURL {
		canonicalIsSelf = "true"
	}

	// Meta robots
	metaRobots := ""
	if page.MetaRobots != nil {
		metaRobots = *page.MetaRobots
	}
	xRobotsTag := ""
	if page.XRobotsTag != nil {
		xRobotsTag = *page.XRobotsTag
	}

	// Headings
	h1 := ""
	if len(page.Headings.H1) > 0 {
		h1 = page.Headings.H1[0]
	}

	// Redirect chain
	redirectChain := ""
	redirectType := ""
	if len(page.RedirectChain) > 0 {
		parts := make([]string, len(page.RedirectChain))
		hasPerm, hasTemp := false, false
		for i, hop := range page.RedirectChain {
			parts[i] = fmt.Sprintf("%d:%s", hop.StatusCode, hop.URL)
			if hop.StatusCode == 301 || hop.StatusCode == 308 {
				hasPerm = true
			} else {
				hasTemp = true
			}
		}
		redirectChain = strings.Join(parts, " -> ")
		if hasPerm && hasTemp {
			redirectType = "mixed"
		} else if hasPerm {
			redirectType = "permanent"
		} else if hasTemp {
			redirectType = "temporary"
		}
	}

	// Images missing alt
	missingAlt := 0
	for _, img := range page.Images {
		if img.MissingAlt {
			missingAlt++
		}
	}

	// Hreflang
	hreflangLangs := ""
	if len(page.Hreflang) > 0 {
		langs := make([]string, len(page.Hreflang))
		for i, h := range page.Hreflang {
			langs[i] = h.Lang
		}
		hreflangLangs = strings.Join(langs, ",")
	}

	// OG title
	ogTitle := page.OpenGraph["og:title"]

	// Readability
	readability := ""
	if page.Readability != nil {
		readability = fmt.Sprintf("%.2f", page.Readability.FleschKincaid)
	}

	// URL issues
	urlIssues := strings.Join(page.URLIssues, ",")

	return []string{
		page.URL,
		page.FinalURL,
		fmt.Sprintf("%d", page.StatusCode),
		redirectChain,
		redirectType,
		fmt.Sprintf("%d", page.ResponseTimeMs),
		titleText,
		fmt.Sprintf("%d", titleLen),
		descText,
		fmt.Sprintf("%d", descLen),
		canonical,
		fmt.Sprintf("%d", page.CanonicalCount),
		canonicalIsSelf,
		metaRobots,
		xRobotsTag,
		h1,
		fmt.Sprintf("%d", len(page.Headings.H1)),
		fmt.Sprintf("%d", len(page.Headings.H2)),
		fmt.Sprintf("%d", max(len(page.InternalLinks), page.InternalLinkCount)),
		fmt.Sprintf("%d", max(len(page.ExternalLinks), page.ExternalLinkCount)),
		fmt.Sprintf("%d", len(page.Images)),
		fmt.Sprintf("%d", missingAlt),
		fmt.Sprintf("%d", page.Depth),
		fmt.Sprintf("%d", page.WordCount),
		page.ContentHash,
		fmt.Sprintf("%.2f", page.TextToCodeRatio),
		page.SimhashFingerprint,
		fmt.Sprintf("%t", page.Indexability.Indexable),
		page.Indexability.Reason,
		fmt.Sprintf("%t", page.Security.IsHTTPS),
		fmt.Sprintf("%t", page.Security.HasHSTS),
		fmt.Sprintf("%t", page.Security.HasMixedContent),
		fmt.Sprintf("%d", len(page.Hreflang)),
		hreflangLangs,
		fmt.Sprintf("%d", len(page.StructuredData)),
		ogTitle,
		readability,
		urlIssues,
		fmt.Sprintf("%t", page.IsSoft404),
		page.Error,
		page.TemplateType,
		fmt.Sprintf("%d", page.Inlinks),
		fmt.Sprintf("%.2f", page.PageRank),
		fmt.Sprintf("%t", page.RobotsBlocked),
		page.URLClassification,
	}
}
