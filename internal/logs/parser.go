package logs

import (
	"bufio"
	"compress/gzip"
	"io"
	"os"
	"strings"
)

const (
	sampleLines        = 20
	progressEveryLines = 10_000
	progressEveryBytes = 1 << 20 // 1 MiB — emit at least once per MB of bytes read
	maxLineSize        = 1 << 20 // 1 MB max line length
)

// ProgressFunc reports parsing progress. `processed` is lines scanned (including
// skipped comments/blanks), `bytesRead` is bytes consumed from the source, and
// `fileSize` is the raw file size in bytes (0 if unknown, e.g., streaming reader).
type ProgressFunc func(processed, bytesRead, fileSize int64)

// countingReader wraps an io.Reader and tracks bytes consumed.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// openSized returns the size of a file (without opening the contents).
func openSized(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// ParseFileWithFormat opens a log file with an optional format hint (skips auto-detection if provided).
// Uses the package-level default ParseState. For concurrent multi-file parses,
// use ParseFileWithState directly.
func ParseFileWithFormat(path string, formatHint Format, onEntry func(*LogEntry), onProgress ProgressFunc) (Format, int64, error) {
	// Clear default state on single-file parses so a prior run doesn't leak in.
	ResetHeaderFields()
	return ParseFileWithState(path, formatHint, defaultParseState, onEntry, onProgress)
}

// ParseFileWithState parses a log file using the caller-provided ParseState.
// Safe to invoke concurrently with different states and different paths.
func ParseFileWithState(path string, formatHint Format, state *ParseState, onEntry func(*LogEntry), onProgress ProgressFunc) (Format, int64, error) {
	state = resolveState(state)
	f, err := os.Open(path)
	if err != nil {
		return FormatUnknown, 0, err
	}
	defer f.Close()

	fi, _ := f.Stat()
	fileSize := fi.Size()

	var base io.Reader = f
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return FormatUnknown, 0, err
		}
		defer gz.Close()
		base = gz
		// When gzipped, byte-based progress against fileSize under-reports
		// the decompressed work. We fall back to fileSize denominator anyway;
		// UI will show "approximately".
	}

	counter := &countingReader{r: base}
	scanner := bufio.NewScanner(counter)
	scanner.Buffer(make([]byte, 1024*1024), maxLineSize)

	// Collect sample lines for format detection. `firstLines` holds raw
	// pre-detection lines we'll replay; headers are captured and dropped
	// here so they don't get re-parsed as data rows.
	var sample []string
	var firstLines []string
	headerCaptured := false
	for scanner.Scan() && len(sample) < sampleLines {
		line := scanner.Text()
		// Strip a BOM on the very first line (common in Windows-exported CSVs).
		trimmed := stripBOM(strings.TrimSpace(line))
		if trimmed == "" {
			firstLines = append(firstLines, line)
			continue
		}
		// W3C #Fields directive: consume, don't add to firstLines (not data).
		if strings.HasPrefix(trimmed, "#Fields:") {
			state.SetW3C(trimmed)
			headerCaptured = true
			continue
		}
		if trimmed[0] == '#' {
			firstLines = append(firstLines, line)
			continue
		}
		// First non-comment line: check whether it's a TSV or CSV header row
		// with known column names. If so, capture and skip — don't replay.
		if !headerCaptured {
			if fields := detectTSVHeader(trimmed); fields != nil {
				state.SetTSV(fields)
				headerCaptured = true
				continue
			}
			if fields := detectCSVHeader(trimmed); fields != nil {
				state.SetCSV(fields)
				headerCaptured = true
				continue
			}
		}
		firstLines = append(firstLines, line)
		sample = append(sample, trimmed)
	}
	if err := scanner.Err(); err != nil {
		return FormatUnknown, 0, err
	}

	format := formatHint
	if format == "" || format == FormatUnknown {
		// If we captured a header row, we already know this is header-driven
		// (CloudFront/W3C) — skip the sample-based detectors.
		if headerCaptured && len(state.Fields) > 0 {
			format = FormatCloudFront
		} else {
			format = DetectFormatWithState(sample, state)
			if format == FormatUnknown {
				preview := ""
				if len(sample) > 0 {
					preview = sample[0]
					if len(preview) > 200 {
						preview = preview[:200] + "…"
					}
				}
				return FormatUnknown, 0, parseError("could not detect log format — try selecting one manually from the format dropdown. First line seen: " + preview)
			}
		}
	}

	// If user forced a CloudFront hint but we haven't captured a header yet,
	// scan firstLines one more time.
	if format == FormatCloudFront && len(state.Fields) == 0 {
		for i, raw := range firstLines {
			trimmed := stripBOM(strings.TrimSpace(raw))
			if trimmed == "" || trimmed[0] == '#' {
				continue
			}
			if fields := detectTSVHeader(trimmed); fields != nil {
				state.SetTSV(fields)
				firstLines = append(firstLines[:i], firstLines[i+1:]...)
			} else if fields := detectCSVHeader(trimmed); fields != nil {
				state.SetCSV(fields)
				firstLines = append(firstLines[:i], firstLines[i+1:]...)
			}
			break
		}
	}

	var processed int64
	var lastBytesEmit int64

	emit := func() {
		if onProgress != nil {
			onProgress(processed, counter.n, fileSize)
			lastBytesEmit = counter.n
		}
	}

	// Parse the sample lines we already read.
	for _, line := range firstLines {
		processed++
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed[0] == '#' {
			continue
		}
		entry, err := ParseLineWithState(trimmed, format, state)
		if err != nil {
			continue
		}
		entry.Bot = IdentifyBot(entry.UserAgent)
		onEntry(entry)
	}

	// Continue with remaining lines.
	for scanner.Scan() {
		processed++
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" {
			continue
		}
		if trimmed[0] == '#' {
			if strings.HasPrefix(trimmed, "#Fields:") {
				state.SetW3C(trimmed)
			}
			continue
		}
		entry, err := ParseLineWithState(trimmed, format, state)
		if err != nil {
			continue
		}
		entry.Bot = IdentifyBot(entry.UserAgent)
		onEntry(entry)

		if onProgress != nil {
			// Emit on either line-count or byte-delta threshold, whichever hits first.
			if processed%progressEveryLines == 0 || counter.n-lastBytesEmit >= progressEveryBytes {
				emit()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return format, processed, err
	}
	emit()
	return format, processed, nil
}

// ParseReader is like ParseFile but reads from an io.Reader with a known format.
func ParseReader(reader io.Reader, format Format, onEntry func(*LogEntry), onProgress ProgressFunc) (int64, error) {
	counter := &countingReader{r: reader}
	scanner := bufio.NewScanner(counter)
	scanner.Buffer(make([]byte, 1024*1024), maxLineSize)

	var processed int64
	var lastBytesEmit int64
	for scanner.Scan() {
		processed++
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" || trimmed[0] == '#' {
			continue
		}
		entry, err := ParseLine(trimmed, format)
		if err != nil {
			continue
		}
		entry.Bot = IdentifyBot(entry.UserAgent)
		onEntry(entry)

		if onProgress != nil {
			if processed%progressEveryLines == 0 || counter.n-lastBytesEmit >= progressEveryBytes {
				onProgress(processed, counter.n, 0)
				lastBytesEmit = counter.n
			}
		}
	}
	return processed, scanner.Err()
}
