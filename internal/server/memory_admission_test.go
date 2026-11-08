package server

import (
	"context"
	"github.com/micelio/micelio/internal/types"
	"runtime/debug"
	"testing"
)

func TestPrepareResultLoadBudgetAndPlaceholder(t *testing.T) {
	prev := debug.SetMemoryLimit(100 << 20)
	defer debug.SetMemoryLimit(prev)
	m := NewCrawlManager(nil)
	defer m.Close()
	pages := func(mib int) int { return (mib << 20) / estimatedBytesPerCachedPage }
	old := &CrawlResults{}
	placeholder := &CrawlResults{}
	m.mu.Lock()
	m.cacheResultLocked("old", old, pages(20))
	m.cacheResultLocked("incoming", placeholder, pages(30))
	m.mu.Unlock()
	evicted, allowed := m.prepareResultLoad(context.Background(), "incoming", pages(30), placeholder)
	if evicted || !allowed || m.GetCachedResults("old") != old || m.GetCachedResults("incoming") != placeholder {
		t.Fatal("placeholder counted twice or canceled")
	}
	evicted, allowed = m.prepareResultLoad(context.Background(), "next", pages(50), nil)
	if !evicted || !allowed || m.GetCachedResults("old") != nil || m.GetCachedResults("incoming") != nil {
		t.Fatal("old results not evicted before allocation")
	}
}
func TestPrepareResultLoadCanceledOrReplaced(t *testing.T) {
	prev := debug.SetMemoryLimit(100 << 20)
	defer debug.SetMemoryLimit(prev)
	m := NewCrawlManager(nil)
	defer m.Close()
	old := &CrawlResults{}
	m.mu.Lock()
	m.cacheResultLocked("old", old, 100)
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evicted, allowed := m.prepareResultLoad(ctx, "incoming", 1000000, nil)
	if evicted || allowed || m.GetCachedResults("old") != old {
		t.Fatal("canceled load evicted usable data")
	}
	evicted, allowed = m.prepareResultLoad(context.Background(), "incoming", 1000000, &CrawlResults{})
	if evicted || allowed || m.GetCachedResults("old") != old {
		t.Fatal("replaced load evicted usable data")
	}
}
func TestPublishStatsPersistsWithoutCache(t *testing.T) {
	s := newTestStore(t)
	m := newTestManager(t, s)
	job, err := s.CreateCrawlJob(map[string]interface{}{"url": "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	m.publishStats(job.ID, types.CrawlStats{TotalPages: 42})
	saved, err := s.LoadCrawlStats(job.ID)
	if err != nil || saved["totalPages"] != float64(42) {
		t.Fatalf("persisted stats=%v err=%v", saved, err)
	}
	if m.GetCachedResults(job.ID) != nil {
		t.Fatal("publishing uncached stats materialized a cache entry")
	}
	res := &CrawlResults{Stats: map[string]interface{}{}}
	m.mu.Lock()
	m.cacheResultLocked(job.ID, res, 42)
	m.mu.Unlock()
	m.publishStats(job.ID, types.CrawlStats{TotalPages: 43})
	res.mu.RLock()
	n := res.Stats["totalPages"]
	res.mu.RUnlock()
	if n != float64(43) {
		t.Fatalf("cached stats shape/value=%v", n)
	}
}
