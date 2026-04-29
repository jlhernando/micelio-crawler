package storage

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

// HeadResultWriter writes HEAD-only crawl results in JSONL or CSV format.
type HeadResultWriter struct {
	file   *os.File
	buf    *bufio.Writer
	csv    *csv.Writer
	format string
	count  int
}

// NewHeadResultWriter creates a writer for HEAD-only crawl output.
func NewHeadResultWriter(path string, format string) (*HeadResultWriter, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create output file: %w", err)
	}

	w := &HeadResultWriter{
		format: format,
		file:   f,
		buf:    bufio.NewWriterSize(f, 64*1024),
	}

	if format == "csv" {
		w.csv = csv.NewWriter(w.buf)
		if err := w.csv.Write(headCSVHeaders()); err != nil {
			f.Close()
			return nil, fmt.Errorf("write CSV header: %w", err)
		}
	}

	return w, nil
}

// Write writes a single HeadResult.
func (w *HeadResultWriter) Write(result *types.HeadResult) error {
	if w.format == "csv" {
		return w.writeCSV(result)
	}
	return w.writeJSONL(result)
}

// Count returns the number of results written.
func (w *HeadResultWriter) Count() int {
	return w.count
}

// Close flushes and closes the writer.
func (w *HeadResultWriter) Close() error {
	if w.csv != nil {
		w.csv.Flush()
	}
	if err := w.buf.Flush(); err != nil {
		w.file.Close()
		return err
	}
	return w.file.Close()
}

func (w *HeadResultWriter) writeJSONL(result *types.HeadResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal head result: %w", err)
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

func (w *HeadResultWriter) writeCSV(result *types.HeadResult) error {
	if err := w.csv.Write(flattenHeadForCSV(result)); err != nil {
		return err
	}
	w.count++
	return nil
}

func headCSVHeaders() []string {
	return []string{
		"url", "final_url", "status_code",
		"redirect_chain_length", "redirect_chain",
		"response_time_ms", "content_type", "content_length",
		"server", "x_robots_tag", "link_canonical",
		"hsts", "csp", "x_frame_options",
		"referrer_policy", "cache_control", "error",
	}
}

func flattenHeadForCSV(r *types.HeadResult) []string {
	// Redirect chain
	chainLen := len(r.RedirectChain)
	chain := ""
	if chainLen > 0 {
		parts := make([]string, chainLen)
		for i, hop := range r.RedirectChain {
			parts[i] = fmt.Sprintf("%d:%s", hop.StatusCode, hop.URL)
		}
		chain = strings.Join(parts, " -> ")
	}

	contentLength := ""
	if r.ContentLength != nil {
		contentLength = fmt.Sprintf("%d", *r.ContentLength)
	}

	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}

	return []string{
		r.URL,
		r.FinalURL,
		fmt.Sprintf("%d", r.StatusCode),
		fmt.Sprintf("%d", chainLen),
		chain,
		fmt.Sprintf("%d", r.ResponseTimeMs),
		r.ContentType,
		contentLength,
		r.Server,
		deref(r.XRobotsTag),
		deref(r.LinkCanonical),
		fmt.Sprintf("%t", r.HSTS),
		fmt.Sprintf("%t", r.CSP),
		deref(r.XFrameOptions),
		deref(r.ReferrerPolicy),
		deref(r.CacheControl),
		r.Error,
	}
}
