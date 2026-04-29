package crawler

import (
	"bufio"
	"container/heap"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
	"github.com/micelio/micelio/internal/types"
)

// priorityEntry wraps QueueEntry with a sequence number for stable ordering.
type priorityEntry struct {
	types.QueueEntry
	seq int64
}

// priorityQueue implements heap.Interface as a min-heap on (Depth, seq).
// Shallower pages are dequeued first; ties broken by insertion order (FIFO).
type priorityQueue []priorityEntry

func (pq priorityQueue) Len() int { return len(pq) }
func (pq priorityQueue) Less(i, j int) bool {
	if pq[i].Depth != pq[j].Depth {
		return pq[i].Depth < pq[j].Depth
	}
	return pq[i].seq < pq[j].seq
}
func (pq priorityQueue) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }
func (pq *priorityQueue) Push(x any)   { *pq = append(*pq, x.(priorityEntry)) }
func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	e := old[n-1]
	old[n-1] = priorityEntry{}
	*pq = old[:n-1]
	return e
}

const (
	// queueSpillThreshold is the max in-memory pending entries before spilling to disk.
	// 50K entries at ~150 bytes each = ~7.5 MB. Beyond that, entries go to a temp file.
	queueSpillThreshold = 50000
	// queueRefillBatch is the number of entries to read from disk per refill.
	queueRefillBatch = 10000
)

// ExcludedStats holds per-pattern exclusion counts.
type ExcludedStats struct {
	Pattern string `json:"pattern"`
	Count   int    `json:"count"`
}

// CrawlQueue manages the URL frontier with dedup, depth control, and filtering.
// Thread-safe for concurrent access from multiple goroutines.
// Uses a bloom filter for visited URL dedup (~240 KB for 100K URLs vs ~10 MB map).
// Dequeues by priority: shallower pages first (min-heap on depth, FIFO tiebreak).
// When the pending queue exceeds 50K entries, overflow spills to a temp file
// and is loaded back in batches as the in-memory buffer drains.
type CrawlQueue struct {
	visited           *bloom.BloomFilter
	visitedCount      int
	excludedByPattern []int // per-pattern counts (same order as excludePatterns)
	excludedByInclude int   // URLs rejected for not matching any include pattern
	seedDomain        string
	pending           priorityQueue
	nextSeq           int64
	includePatterns   []*regexp.Regexp
	excludePatterns   []*regexp.Regexp
	allowedDomains    []string
	// Disk spill: overflow entries written to temp file when pending > threshold.
	spillFile       *os.File
	spillBuf        *bufio.Writer
	spillReadOff    int64 // byte offset for next read from spill file
	spillCount      int   // entries on disk not yet consumed
	maxDepth        int
	maxPages        int
	mu              sync.Mutex
	enforceInternal bool
	pathOnlyFilters bool
}

// QueueOptions holds optional configuration for CrawlQueue.
type QueueOptions struct {
	IncludePatterns []*regexp.Regexp
	ExcludePatterns []*regexp.Regexp
	AllowedDomains  []string
	EnforceInternal bool
	PathOnlyFilters bool // match include/exclude patterns against path only (no query string)
}

// NewCrawlQueue creates a new crawl queue rooted at seedURL.
func NewCrawlQueue(seedURL string, maxDepth, maxPages int, opts *QueueOptions) *CrawlQueue {
	parsed, _ := url.Parse(seedURL)
	domain := ""
	if parsed != nil {
		domain = parsed.Hostname()
	}

	q := &CrawlQueue{
		visited:         bloom.NewWithEstimates(uint(maxPages), 0.0001),
		seedDomain:      domain,
		maxDepth:        maxDepth,
		maxPages:        maxPages,
		enforceInternal: true,
	}

	if opts != nil {
		q.includePatterns = opts.IncludePatterns
		q.excludePatterns = opts.ExcludePatterns
		q.excludedByPattern = make([]int, len(opts.ExcludePatterns))
		q.enforceInternal = opts.EnforceInternal
		q.allowedDomains = opts.AllowedDomains
		q.pathOnlyFilters = opts.PathOnlyFilters
	}

	return q
}

// Enqueue adds a URL to the queue if it passes all filters.
// Returns true if the URL was added, false if filtered or already seen.
func (q *CrawlQueue) Enqueue(rawURL string, depth int, referrer *string) bool {
	normalized := NormalizeURL(rawURL)
	if normalized == "" {
		return false
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.visited.TestString(normalized) {
		return false
	}
	if depth > q.maxDepth {
		return false
	}
	if q.visitedCount >= q.maxPages {
		return false
	}
	if q.enforceInternal && !q.isInternal(normalized) {
		return false
	}
	if !q.matchesFilters(normalized) {
		return false
	}

	entry := types.QueueEntry{
		URL:      rawURL, // Keep original for fetching
		Depth:    depth,
		Referrer: referrer,
	}
	q.enqueueEntry(entry)
	q.visited.AddString(normalized)
	q.visitedCount++
	return true
}

// EnqueueEntry adds a full QueueEntry to the queue, preserving all fields (including Requeues).
// Used for restoring persisted entries on resume.
func (q *CrawlQueue) EnqueueEntry(entry types.QueueEntry) bool {
	normalized := NormalizeURL(entry.URL)
	if normalized == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.visited.TestString(normalized) {
		return false
	}
	if entry.Depth > q.maxDepth {
		return false
	}
	if q.visitedCount >= q.maxPages {
		return false
	}
	if q.enforceInternal && !q.isInternal(normalized) {
		return false
	}
	if !q.matchesFilters(normalized) {
		return false
	}
	q.enqueueEntry(entry)
	q.visited.AddString(normalized)
	q.visitedCount++
	return true
}

// enqueueEntry pushes to the priority heap or spills to disk. Caller must hold mu.
func (q *CrawlQueue) enqueueEntry(entry types.QueueEntry) {
	q.nextSeq++
	pe := priorityEntry{QueueEntry: entry, seq: q.nextSeq}
	if q.pending.Len() < queueSpillThreshold {
		heap.Push(&q.pending, pe)
		return
	}
	// Spill to disk
	if q.spillFile == nil {
		if err := q.initSpill(); err != nil {
			heap.Push(&q.pending, pe)
			return
		}
	}
	q.writeSpillEntry(entry)
	q.spillCount++
}

// Requeue adds a URL back to the end of the queue, bypassing the bloom filter.
// Used for retrying rate-limited (429) URLs after a delay.
func (q *CrawlQueue) Requeue(entry types.QueueEntry) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.enqueueEntry(entry)
}

// Dequeue removes and returns the highest-priority URL (shallowest depth) from the queue.
// Returns nil if the queue is empty.
func (q *CrawlQueue) Dequeue() *types.QueueEntry {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.pending.Len() == 0 && q.spillCount > 0 {
		q.refillFromDisk()
	}
	if q.pending.Len() == 0 {
		return nil
	}

	pe := heap.Pop(&q.pending).(priorityEntry)
	entry := pe.QueueEntry
	return &entry
}

// Has checks if a URL has been seen (visited or queued).
func (q *CrawlQueue) Has(rawURL string) bool {
	normalized := NormalizeURL(rawURL)
	if normalized == "" {
		return false
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	return q.visited.TestString(normalized)
}

// Size returns the number of pending URLs in the queue (in-memory + disk).
func (q *CrawlQueue) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pending.Len() + q.spillCount
}

// TotalSeen returns the total number of unique URLs seen.
func (q *CrawlQueue) TotalSeen() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.visitedCount
}

// ExcludedSnapshot returns the total excluded count and per-pattern breakdown
// in a single lock acquisition to ensure consistency.
func (q *CrawlQueue) ExcludedSnapshot() (int, []ExcludedStats) {
	q.mu.Lock()
	defer q.mu.Unlock()
	total := q.excludedByInclude
	for _, c := range q.excludedByPattern {
		total += c
	}
	var stats []ExcludedStats
	if q.excludedByInclude > 0 {
		stats = append(stats, ExcludedStats{Pattern: "(no include match)", Count: q.excludedByInclude})
	}
	for i, p := range q.excludePatterns {
		if q.excludedByPattern[i] > 0 {
			stats = append(stats, ExcludedStats{Pattern: p.String(), Count: q.excludedByPattern[i]})
		}
	}
	return total, stats
}

// MarkVisited marks a URL as visited without enqueueing it.
func (q *CrawlQueue) MarkVisited(rawURL string) {
	normalized := NormalizeURL(rawURL)
	if normalized == "" {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.visited.TestString(normalized) {
		q.visited.AddString(normalized)
		q.visitedCount++
	}
}

// UpdateSeedDomain changes the seed domain (e.g., after www redirect).
func (q *CrawlQueue) UpdateSeedDomain(newDomain string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seedDomain = newDomain
}

// SeedDomain returns the current seed domain.
func (q *CrawlQueue) SeedDomain() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.seedDomain
}

// Drain returns all pending entries (in-memory + disk) and empties the queue.
func (q *CrawlQueue) Drain() []types.QueueEntry {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Collect in-memory entries
	entries := make([]types.QueueEntry, 0, q.pending.Len()+q.spillCount)
	for q.pending.Len() > 0 {
		pe := heap.Pop(&q.pending).(priorityEntry)
		entries = append(entries, pe.QueueEntry)
	}
	// Collect disk-spilled entries
	for q.spillCount > 0 {
		q.refillFromDisk()
		for q.pending.Len() > 0 {
			pe := heap.Pop(&q.pending).(priorityEntry)
			entries = append(entries, pe.QueueEntry)
		}
	}
	return entries
}

// Close releases resources (removes spill temp file if any).
func (q *CrawlQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closeSpill()
}

// --- Disk spill internals ---

func (q *CrawlQueue) initSpill() error {
	f, err := os.CreateTemp("", "micelio-queue-*.tsv")
	if err != nil {
		return fmt.Errorf("create queue spill file: %w", err)
	}
	q.spillFile = f
	q.spillBuf = bufio.NewWriterSize(f, 64*1024)
	q.spillReadOff = 0
	fmt.Fprintf(os.Stderr, "  [queue] Spilling to disk: %s (frontier > %d entries)\n", f.Name(), queueSpillThreshold)
	return nil
}

// writeSpillEntry writes one entry as TSV: depth\trequeues\turl\treferrer\n
func (q *CrawlQueue) writeSpillEntry(e types.QueueEntry) {
	q.spillBuf.WriteString(strconv.Itoa(e.Depth))
	q.spillBuf.WriteByte('\t')
	q.spillBuf.WriteString(strconv.Itoa(e.Requeues))
	q.spillBuf.WriteByte('\t')
	q.spillBuf.WriteString(escapeTSV(e.URL))
	q.spillBuf.WriteByte('\t')
	if e.Referrer != nil {
		q.spillBuf.WriteString(escapeTSV(*e.Referrer))
	}
	q.spillBuf.WriteByte('\n')
}

// refillFromDisk reads up to queueRefillBatch entries from the spill file into pending.
// Caller must hold mu.
func (q *CrawlQueue) refillFromDisk() {
	if q.spillFile == nil || q.spillCount == 0 {
		return
	}
	// Flush any buffered writes before reading
	if err := q.spillBuf.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] queue spill flush: %v\n", err)
		return
	}
	// Seek to read position
	if _, err := q.spillFile.Seek(q.spillReadOff, 0); err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] queue spill seek: %v\n", err)
		return
	}

	// Use ReadString instead of Scanner to track exact bytes consumed.
	// Scanner buffers ahead, so file position after scanning would be wrong for partial refills.
	reader := bufio.NewReaderSize(q.spillFile, 64*1024)
	loaded := 0
	for loaded < queueRefillBatch && q.spillCount > 0 {
		line, err := reader.ReadString('\n')
		if len(line) == 0 && err != nil {
			break
		}
		q.spillReadOff += int64(len(line))
		line = strings.TrimSuffix(line, "\n")
		entry, ok := parseSpillEntry(line)
		if !ok {
			q.spillCount--
			continue
		}
		q.nextSeq++
		heap.Push(&q.pending, priorityEntry{QueueEntry: entry, seq: q.nextSeq})
		loaded++
		q.spillCount--
	}

	// If all disk entries consumed, reset the file for reuse
	if q.spillCount <= 0 {
		q.spillCount = 0
		q.spillReadOff = 0
		q.spillFile.Truncate(0)
		q.spillFile.Seek(0, 0)
		q.spillBuf.Reset(q.spillFile)
	} else {
		// Partial refill: seek fd to EOF so subsequent writeSpillEntry calls
		// append correctly. The bufio.Reader may have read ahead, leaving
		// the fd at a position past spillReadOff.
		q.spillFile.Seek(0, 2)
		q.spillBuf.Reset(q.spillFile)
	}
}

// parseSpillEntry parses a TSV line: depth\trequeues\turl\treferrer
func parseSpillEntry(line string) (types.QueueEntry, bool) {
	parts := strings.SplitN(line, "\t", 4)
	if len(parts) < 3 {
		return types.QueueEntry{}, false
	}
	depth, err := strconv.Atoi(parts[0])
	if err != nil {
		return types.QueueEntry{}, false
	}
	requeues, _ := strconv.Atoi(parts[1])
	entry := types.QueueEntry{
		URL:      unescapeTSV(parts[2]),
		Depth:    depth,
		Requeues: requeues,
	}
	if len(parts) == 4 && parts[3] != "" {
		ref := unescapeTSV(parts[3])
		entry.Referrer = &ref
	}
	return entry, true
}

func (q *CrawlQueue) closeSpill() {
	if q.spillFile != nil {
		name := q.spillFile.Name()
		q.spillFile.Close()
		os.Remove(name)
		q.spillFile = nil
		q.spillBuf = nil
	}
}

func (q *CrawlQueue) matchesFilters(u string) bool {
	matchTarget := u
	if q.pathOnlyFilters {
		if parsed, err := url.Parse(u); err == nil {
			matchTarget = parsed.Path
		}
	}
	if len(q.includePatterns) > 0 {
		matched := false
		for _, p := range q.includePatterns {
			if p.MatchString(matchTarget) {
				matched = true
				break
			}
		}
		if !matched {
			q.excludedByInclude++
			return false
		}
	}
	if len(q.excludePatterns) > 0 {
		for i, p := range q.excludePatterns {
			if p.MatchString(matchTarget) {
				q.excludedByPattern[i]++
				return false
			}
		}
	}
	return true
}

func (q *CrawlQueue) isInternal(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Hostname() == q.seedDomain {
		return true
	}
	for _, d := range q.allowedDomains {
		if parsed.Hostname() == d {
			return true
		}
	}
	return false
}
