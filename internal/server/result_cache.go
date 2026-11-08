package server

import (
	"context"
	"math"
	"runtime/debug"
	"sync"
)

// Result cache limits. Results are evicted oldest first when there are more
// than maxCachedResults, or, when a Go memory limit is set (--memlimit /
// GOMEMLIMIT), when their estimated size passes cacheMemoryShare of it.
// Estimates use the page count: cached pages are stripped, and with the link
// graph and indexes a cached page costs a few KB.
const (
	estimatedBytesPerCachedPage = 6 << 10
	cacheMemoryShare            = 0.6
	// maxConcurrentPageLoads bounds full-crawl loads from the crawl DB
	// (each holds every page in memory while it decodes).
	maxConcurrentPageLoads = 1
)

func estimatedResultBytes(pageCount int) int64 {
	return int64(pageCount) * estimatedBytesPerCachedPage
}

// cacheMemoryBudget returns the byte budget for cached results, or 0 when no
// Go memory limit is set.
func cacheMemoryBudget() int64 {
	limit := debug.SetMemoryLimit(-1) // read without changing
	if limit <= 0 || limit == math.MaxInt64 {
		return 0
	}
	return int64(float64(limit) * cacheMemoryShare)
}

// resultLoads de-duplicates concurrent loads of the same crawl and bounds
// how many full loads run at once.
type resultLoads struct {
	mu       sync.Mutex
	inflight map[string]*resultLoad
	sem      chan struct{}
}

type resultLoad struct {
	done chan struct{}
	res  *CrawlResults
}

func newResultLoads() *resultLoads {
	return &resultLoads{inflight: map[string]*resultLoad{}, sem: make(chan struct{}, maxConcurrentPageLoads)}
}

// do runs load once per crawl ID at a time; concurrent callers for the same
// ID wait for and share its result.
func (l *resultLoads) do(id string, load func() *CrawlResults) *CrawlResults {
	l.mu.Lock()
	if c, ok := l.inflight[id]; ok {
		l.mu.Unlock()
		<-c.done
		return c.res
	}
	c := &resultLoad{done: make(chan struct{})}
	l.inflight[id] = c
	l.mu.Unlock()

	defer func() {
		l.mu.Lock()
		delete(l.inflight, id)
		l.mu.Unlock()
		close(c.done)
	}()
	c.res = load()
	return c.res
}

// acquire takes a full-load slot; false when ctx ends first.
func (l *resultLoads) acquire(ctx context.Context) bool {
	select {
	case l.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (l *resultLoads) release() { <-l.sem }

// prepareResultLoad evicts other histories before the next full decode, rather
// than waiting until both the old and new page sets are resident. The incoming
// placeholder is already estimated at its full size: do not count it twice or
// cancel its load. A single oversized result remains supported by the UI.
func (m *CrawlManager) prepareResultLoad(ctx context.Context, id string, pageCount int, expected *CrawlResults) (evicted, allowed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil || expected != nil && m.results[id] != expected {
		return false, false
	}
	budget := cacheMemoryBudget()
	var total = estimatedResultBytes(pageCount)
	count := 1
	for _, old := range m.resultOrder {
		if old != id {
			total += m.resultSize[old]
			count++
		}
	}
	for _, old := range append([]string(nil), m.resultOrder...) {
		if count <= maxCachedResults && (budget == 0 || total <= budget) {
			break
		}
		if old == id {
			continue
		}
		total -= m.resultSize[old]
		count--
		m.removeCachedResultLocked(old)
		evicted = true
	}
	return evicted, true
}

func (m *CrawlManager) removeCachedResultLocked(id string) {
	if old := m.results[id]; old != nil && old.loadCancel != nil {
		old.loadCancel()
	}
	delete(m.results, id)
	delete(m.resultSize, id)
	for i, old := range m.resultOrder {
		if old == id {
			m.resultOrder = append(m.resultOrder[:i], m.resultOrder[i+1:]...)
			break
		}
	}
}

// cacheResultLocked stores r under id (replacing any previous entry, which
// moves to the newest position) and evicts old results over the count or
// memory limits. The entry just stored is never evicted. Caller holds m.mu.
// pageCount sizes the entry for the memory budget.
func (m *CrawlManager) cacheResultLocked(id string, r *CrawlResults, pageCount int) {
	if old := m.results[id]; old != nil && old != r && old.loadCancel != nil {
		old.loadCancel()
	}
	m.results[id] = r
	m.resultSize[id] = estimatedResultBytes(pageCount)
	for i, o := range m.resultOrder {
		if o == id {
			m.resultOrder = append(m.resultOrder[:i], m.resultOrder[i+1:]...)
			break
		}
	}
	m.resultOrder = append(m.resultOrder, id)

	budget := cacheMemoryBudget()
	for len(m.resultOrder) > 1 {
		over := len(m.resultOrder) > maxCachedResults
		if !over && budget > 0 {
			var total int64
			for _, o := range m.resultOrder {
				total += m.resultSize[o]
			}
			over = total > budget
		}
		if !over {
			return
		}
		m.evictOldestResult()
	}
}
