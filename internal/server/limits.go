package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Limits on what one HTTP request can make the dashboard do. They are
// generous (a 250K-page Wikipedia crawl and three 6.5 GB log files in one
// upload fit easily) but finite, so a single request cannot exhaust memory
// or disk. Each can be changed with an environment variable (also read from
// ~/.micelio/micelio.env). The CLI uses only the import limits
// (LimitImport), since `micelio import` may be given an untrusted file.
type serverLimits struct {
	// MaxCrawlPages caps a crawl's page limit (the crawl queue sizes a
	// bloom filter from it). MICELIO_MAX_CRAWL_PAGES.
	MaxCrawlPages int
	// LogUploadBytes caps one log upload (all files together).
	// MICELIO_MAX_LOG_UPLOAD_GB.
	LogUploadBytes int64
	// LogAnalyses is how many log analyses run at once; more wait.
	// MICELIO_MAX_LOG_ANALYSES.
	LogAnalyses int
	// LogPending caps running + waiting log analyses; uploads past it are
	// refused. MICELIO_MAX_LOG_PENDING.
	LogPending int
	// ImportBytes caps an uploaded crawl file. MICELIO_MAX_IMPORT_GB.
	ImportBytes int64
	// ImportDecompressedBytes caps the decompressed size of an uploaded
	// .gz/.zst JSONL crawl. MICELIO_MAX_IMPORT_DECOMPRESSED_GB.
	ImportDecompressedBytes int64
}

const (
	maxCrawlConcurrency   = 100 // same cap as live tuning
	maxCrawlDepth         = 1000
	maxRenderTimeoutSec   = 600
	maxLISuggestions      = 100_000
	maxLogUploadFiles     = 256
	maxImportLineBytes    = 256 << 20 // one JSONL page (saved HTML included)
	defaultMaxCrawlPages  = 10_000_000
	defaultLogUploadGB    = 64
	defaultLogAnalyses    = 2
	defaultLogPending     = 8
	defaultImportGB       = 32
	defaultImportDecompGB = 64
)

const gib = int64(1) << 30

// limits returns the limits, read from the environment once.
var limits = sync.OnceValue(func() serverLimits {
	return serverLimits{
		MaxCrawlPages:           envInt("MICELIO_MAX_CRAWL_PAGES", defaultMaxCrawlPages),
		LogUploadBytes:          int64(envInt("MICELIO_MAX_LOG_UPLOAD_GB", defaultLogUploadGB)) * gib,
		LogAnalyses:             envInt("MICELIO_MAX_LOG_ANALYSES", defaultLogAnalyses),
		LogPending:              envInt("MICELIO_MAX_LOG_PENDING", defaultLogPending),
		ImportBytes:             int64(envInt("MICELIO_MAX_IMPORT_GB", defaultImportGB)) * gib,
		ImportDecompressedBytes: int64(envInt("MICELIO_MAX_IMPORT_DECOMPRESSED_GB", defaultImportDecompGB)) * gib,
	}
})

// LimitImport applies the dashboard upload limits to a local import
// (`micelio import --local`, or no dashboard running): the file may be at
// most MICELIO_MAX_IMPORT_GB, a .gz/.zst JSONL may expand to at most
// MICELIO_MAX_IMPORT_DECOMPRESSED_GB, and the crawl may hold at most
// MICELIO_MAX_CRAWL_PAGES pages. A small compressed file could otherwise
// fill the disk and memory.
func LimitImport(path string, opts ImportOptions) (ImportOptions, error) {
	lim := limits()
	st, err := os.Stat(path)
	if err != nil {
		return opts, err
	}
	if st.Size() > lim.ImportBytes {
		return opts, fmt.Errorf("%w: %s is larger than the %d GB import limit (MICELIO_MAX_IMPORT_GB)",
			errImportTooLarge, filepath.Base(path), lim.ImportBytes/gib)
	}
	opts.limit = importLimit{maxDecompressed: lim.ImportDecompressedBytes, maxPages: lim.MaxCrawlPages}
	return opts, nil
}

// envInt reads a positive integer from the environment, else def.
func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		log.Printf("limits: ignoring %s=%q (want a positive integer)", name, s)
		return def
	}
	return n
}

// logQueue bounds log analyses: at most LogAnalyses parse at once and at
// most LogPending are running or waiting. An upload reserves a place before
// it reads its body, so a refused upload costs no disk.
type logQueue struct {
	once    sync.Once
	sem     chan struct{}
	mu      sync.Mutex
	pending int
}

// reserve takes a place; false if LogPending analyses are already queued.
func (q *logQueue) reserve() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending >= limits().LogPending {
		return false
	}
	q.pending++
	return true
}

// release gives back a place taken by reserve.
func (q *logQueue) release() {
	q.mu.Lock()
	q.pending--
	q.mu.Unlock()
}

// run waits for a free analysis slot, runs fn and releases the place.
func (q *logQueue) run(fn func()) {
	defer q.release()
	q.once.Do(func() { q.sem = make(chan struct{}, limits().LogAnalyses) })
	q.sem <- struct{}{}
	defer func() { <-q.sem }()
	fn()
}
