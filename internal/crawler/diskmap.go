package crawler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/micelio/micelio/internal/types"
)

var (
	tsvEscaper   = strings.NewReplacer("\\", "\\\\", "\t", "\\t", "\n", "\\n")
	tsvUnescaper = strings.NewReplacer("\\n", "\n", "\\t", "\t", "\\\\", "\\")
)

// --- Disk-backed external links accumulator ---

// externalLinksDisk streams external link → source page pairs to a temp file
// during crawl, then reads them back into a map for post-crawl HEAD checking.
// This avoids accumulating a large map in memory during the crawl phase.
//
// Write errors in Add() are buffered by bufio.Writer and surface on Load()/Flush().
type externalLinksDisk struct {
	file *os.File
	buf  *bufio.Writer
}

func newExternalLinksDisk() (*externalLinksDisk, error) {
	f, err := os.CreateTemp("", "micelio-extlinks-*.tsv")
	if err != nil {
		return nil, fmt.Errorf("create extlinks temp file: %w", err)
	}
	return &externalLinksDisk{
		file: f,
		buf:  bufio.NewWriterSize(f, 64*1024),
	}, nil
}

// Add writes an external URL and source page pair. Thread-safe via caller's mutex.
// Tabs and newlines in URLs are escaped to preserve TSV integrity.
func (d *externalLinksDisk) Add(extURL, sourcePage string) {
	d.buf.WriteString(escapeTSV(extURL))
	d.buf.WriteByte('\t')
	d.buf.WriteString(escapeTSV(sourcePage))
	d.buf.WriteByte('\n')
}

// Load reads the temp file back into a map and removes the file.
func (d *externalLinksDisk) Load() (map[string][]string, error) {
	if err := d.buf.Flush(); err != nil {
		d.cleanup()
		return nil, fmt.Errorf("flush extlinks: %w", err)
	}
	if _, err := d.file.Seek(0, 0); err != nil {
		d.cleanup()
		return nil, fmt.Errorf("seek extlinks: %w", err)
	}

	result := make(map[string][]string)
	scanner := bufio.NewScanner(d.file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.IndexByte(line, '\t')
		if idx < 0 {
			continue
		}
		extURL := unescapeTSV(line[:idx])
		srcPage := unescapeTSV(line[idx+1:])
		result[extURL] = append(result[extURL], srcPage)
	}

	d.cleanup()
	return result, scanner.Err()
}

func (d *externalLinksDisk) cleanup() {
	name := d.file.Name()
	d.file.Close()
	os.Remove(name)
}

// escapeTSV replaces tab and newline characters that would corrupt TSV format.
func escapeTSV(s string) string {
	if !strings.ContainsAny(s, "\t\n\\") {
		return s // fast path: no escaping needed
	}
	return tsvEscaper.Replace(s)
}

// unescapeTSV reverses escapeTSV.
func unescapeTSV(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	return tsvUnescaper.Replace(s)
}

// --- Disk-backed resource refs accumulator ---

// resourceRefsDisk streams per-page resource entries to a temp file during crawl.
//
// Write errors in Add() are buffered by json.Encoder/bufio.Writer and surface on Load()/Flush().
type resourceRefsDisk struct {
	file *os.File
	buf  *bufio.Writer
	enc  *json.Encoder
}

type resourceRefsEntry struct {
	PageURL string               `json:"p"`
	Refs    []types.ResourceEntry `json:"r"`
}

func newResourceRefsDisk() (*resourceRefsDisk, error) {
	f, err := os.CreateTemp("", "micelio-resrefs-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("create resrefs temp file: %w", err)
	}
	buf := bufio.NewWriterSize(f, 64*1024)
	return &resourceRefsDisk{
		file: f,
		buf:  buf,
		enc:  json.NewEncoder(buf),
	}, nil
}

// Add writes a page's resource refs. Thread-safe via caller's mutex.
func (d *resourceRefsDisk) Add(pageURL string, refs []types.ResourceEntry) {
	d.enc.Encode(resourceRefsEntry{PageURL: pageURL, Refs: refs})
}

// Load reads the temp file back into a map and removes the file.
func (d *resourceRefsDisk) Load() (map[string][]types.ResourceEntry, error) {
	if err := d.buf.Flush(); err != nil {
		d.cleanup()
		return nil, fmt.Errorf("flush resrefs: %w", err)
	}
	if _, err := d.file.Seek(0, 0); err != nil {
		d.cleanup()
		return nil, fmt.Errorf("seek resrefs: %w", err)
	}

	result := make(map[string][]types.ResourceEntry)
	dec := json.NewDecoder(bufio.NewReaderSize(d.file, 64*1024))
	var decErr error
	for dec.More() {
		var entry resourceRefsEntry
		if err := dec.Decode(&entry); err != nil {
			decErr = fmt.Errorf("decode resrefs entry: %w", err)
			break
		}
		result[entry.PageURL] = entry.Refs
	}

	d.cleanup()
	if decErr != nil {
		return result, decErr
	}
	return result, nil
}

func (d *resourceRefsDisk) cleanup() {
	name := d.file.Name()
	d.file.Close()
	os.Remove(name)
}

// --- Disk-backed internal links accumulator ---

// internalLinksDisk streams per-page internal link edges to a temp file during crawl.
// Each line: source\ttarget1\ttarget2\t...targetN\n (TSV grouped by source page).
// This avoids keeping InternalLinks []string in memory on each PageData, saving ~5GB for 1M pages.
type internalLinksDisk struct {
	file *os.File
	buf  *bufio.Writer
}

func newInternalLinksDisk() (*internalLinksDisk, error) {
	f, err := os.CreateTemp("", "micelio-intlinks-*.tsv")
	if err != nil {
		return nil, fmt.Errorf("create intlinks temp file: %w", err)
	}
	return &internalLinksDisk{
		file: f,
		buf:  bufio.NewWriterSize(f, 64*1024),
	}, nil
}

// AddPage writes all internal links for a source page. Thread-safe via caller's mutex.
func (d *internalLinksDisk) AddPage(sourceURL string, links []string) {
	if len(links) == 0 {
		return
	}
	d.buf.WriteString(escapeTSV(sourceURL))
	for _, link := range links {
		d.buf.WriteByte('\t')
		d.buf.WriteString(escapeTSV(link))
	}
	d.buf.WriteByte('\n')
}

// Iterate reads all stored edges and calls fn for each source→targets group.
// Must be called after all AddPage calls. Caller should call Close after iteration.
func (d *internalLinksDisk) Iterate(fn func(source string, targets []string)) error {
	if err := d.buf.Flush(); err != nil {
		return fmt.Errorf("flush intlinks: %w", err)
	}
	if _, err := d.file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek intlinks: %w", err)
	}
	scanner := bufio.NewScanner(d.file)
	scanner.Buffer(make([]byte, 0, 256*1024), 4*1024*1024) // 4MB max line
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) < 2 {
			continue
		}
		source := unescapeTSV(parts[0])
		targets := make([]string, len(parts)-1)
		for i := 1; i < len(parts); i++ {
			targets[i-1] = unescapeTSV(parts[i])
		}
		fn(source, targets)
	}
	return scanner.Err()
}

func (d *internalLinksDisk) Close() {
	name := d.file.Name()
	d.file.Close()
	os.Remove(name)
}

// --- Disk-backed dead letter queue ---

// deadLetterDisk streams permanently failed URLs to a temp JSONL file during crawl.
type deadLetterDisk struct {
	file *os.File
	buf  *bufio.Writer
	enc  *json.Encoder
}

func newDeadLetterDisk() (*deadLetterDisk, error) {
	f, err := os.CreateTemp("", "micelio-dlq-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("create dlq temp file: %w", err)
	}
	buf := bufio.NewWriterSize(f, 64*1024)
	return &deadLetterDisk{file: f, buf: buf, enc: json.NewEncoder(buf)}, nil
}

// Add writes a dead letter entry. Thread-safe via caller's mutex.
func (d *deadLetterDisk) Add(entry types.DeadLetterEntry) {
	if err := d.enc.Encode(entry); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] dlq encode: %v\n", err)
	}
}

// Load reads all entries and removes the temp file.
func (d *deadLetterDisk) Load() ([]types.DeadLetterEntry, error) {
	if err := d.buf.Flush(); err != nil {
		d.cleanup()
		return nil, fmt.Errorf("flush dlq: %w", err)
	}
	if _, err := d.file.Seek(0, 0); err != nil {
		d.cleanup()
		return nil, fmt.Errorf("seek dlq: %w", err)
	}
	var entries []types.DeadLetterEntry
	dec := json.NewDecoder(bufio.NewReaderSize(d.file, 64*1024))
	for dec.More() {
		var entry types.DeadLetterEntry
		if err := dec.Decode(&entry); err != nil {
			break
		}
		entries = append(entries, entry)
	}
	d.cleanup()
	return entries, nil
}

func (d *deadLetterDisk) cleanup() {
	name := d.file.Name()
	d.file.Close()
	os.Remove(name)
}
