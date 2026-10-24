package server

import (
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestResultLoadsDeduplicates(t *testing.T) {
	l := newResultLoads()
	var calls atomic.Int32
	want := &CrawlResults{}
	var wg sync.WaitGroup
	results := make([]*CrawlResults, 10)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = l.do("c1", func() *CrawlResults {
				calls.Add(1)
				time.Sleep(50 * time.Millisecond)
				return want
			})
		}(i)
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("load ran %d times, want 1", calls.Load())
	}
	for i, r := range results {
		if r != want {
			t.Errorf("caller %d got a different result", i)
		}
	}
}

func TestCacheEvictsByCountAndReplacesInPlace(t *testing.T) {
	m := NewCrawlManager(nil)
	defer m.Close()
	m.mu.Lock()
	for i := 0; i < maxCachedResults+2; i++ {
		m.cacheResultLocked(fmt.Sprint(i), &CrawlResults{}, 10)
	}
	// Re-caching an existing ID must not duplicate it in the order.
	m.cacheResultLocked("6", &CrawlResults{}, 10)
	m.mu.Unlock()
	if len(m.results) != maxCachedResults || len(m.resultOrder) != maxCachedResults {
		t.Fatalf("cached %d / order %d, want %d", len(m.results), len(m.resultOrder), maxCachedResults)
	}
	if _, ok := m.results["0"]; ok {
		t.Error("oldest result not evicted")
	}
}

func TestCacheEvictsByMemoryBudget(t *testing.T) {
	prev := debug.SetMemoryLimit(100 << 20) // budget = 60 MB
	defer debug.SetMemoryLimit(prev)
	m := NewCrawlManager(nil)
	defer m.Close()
	perResult := int(30<<20) / estimatedBytesPerCachedPage // ~30 MB each
	m.mu.Lock()
	for i := 0; i < 3; i++ {
		m.cacheResultLocked(fmt.Sprint(i), &CrawlResults{}, perResult)
	}
	// One result larger than the whole budget is still kept (never evict
	// the entry just stored).
	m.cacheResultLocked("huge", &CrawlResults{}, perResult*10)
	m.mu.Unlock()
	if len(m.results) != 1 || m.results["huge"] == nil {
		t.Errorf("cached %v, want only huge", m.resultOrder)
	}
}
