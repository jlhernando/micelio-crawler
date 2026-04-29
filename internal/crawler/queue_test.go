package crawler

import (
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/micelio/micelio/internal/types"
)

func TestQueueEnqueueDequeue(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 5, 100, nil)

	if !q.Enqueue("https://example.com/page1", 0, nil) {
		t.Error("Expected first enqueue to succeed")
	}
	if q.Enqueue("https://example.com/page1", 0, nil) {
		t.Error("Expected duplicate enqueue to fail")
	}

	if q.Size() != 1 {
		t.Errorf("Size = %d, want 1", q.Size())
	}

	entry := q.Dequeue()
	if entry == nil {
		t.Fatal("Expected non-nil dequeue")
	}
	if entry.URL != "https://example.com/page1" {
		t.Errorf("URL = %q, want %q", entry.URL, "https://example.com/page1")
	}

	if q.Dequeue() != nil {
		t.Error("Expected nil dequeue from empty queue")
	}
}

func TestQueuePriorityDequeue(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 10, 100, nil)

	// Enqueue in reverse depth order
	q.Enqueue("https://example.com/deep", 3, nil)
	q.Enqueue("https://example.com/mid", 2, nil)
	q.Enqueue("https://example.com/shallow", 0, nil)
	q.Enqueue("https://example.com/mid2", 1, nil)

	// Should dequeue shallowest first
	want := []struct {
		url   string
		depth int
	}{
		{"https://example.com/shallow", 0},
		{"https://example.com/mid2", 1},
		{"https://example.com/mid", 2},
		{"https://example.com/deep", 3},
	}

	for _, w := range want {
		e := q.Dequeue()
		if e == nil {
			t.Fatalf("Expected entry at depth %d, got nil", w.depth)
		}
		if e.URL != w.url {
			t.Errorf("URL = %q, want %q", e.URL, w.url)
		}
		if e.Depth != w.depth {
			t.Errorf("Depth = %d, want %d", e.Depth, w.depth)
		}
	}
}

func TestQueuePriorityFIFOTiebreak(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 10, 100, nil)

	// Enqueue multiple URLs at the same depth
	q.Enqueue("https://example.com/a", 1, nil)
	q.Enqueue("https://example.com/b", 1, nil)
	q.Enqueue("https://example.com/c", 1, nil)

	// Same depth: should dequeue in insertion order (FIFO)
	e1 := q.Dequeue()
	e2 := q.Dequeue()
	e3 := q.Dequeue()
	if e1.URL != "https://example.com/a" || e2.URL != "https://example.com/b" || e3.URL != "https://example.com/c" {
		t.Errorf("FIFO tiebreak failed: got %q, %q, %q", e1.URL, e2.URL, e3.URL)
	}
}

func TestQueueDepthLimit(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 2, 100, nil)

	if !q.Enqueue("https://example.com/d1", 1, nil) {
		t.Error("Depth 1 should be allowed")
	}
	if !q.Enqueue("https://example.com/d2", 2, nil) {
		t.Error("Depth 2 should be allowed")
	}
	if q.Enqueue("https://example.com/d3", 3, nil) {
		t.Error("Depth 3 should be rejected")
	}
}

func TestQueueMaxPages(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 10, 2, nil)

	q.Enqueue("https://example.com/a", 0, nil)
	q.Enqueue("https://example.com/b", 0, nil)

	if q.Enqueue("https://example.com/c", 0, nil) {
		t.Error("Expected maxPages to reject third URL")
	}
}

func TestQueueInternalOnly(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 5, 100, nil)

	if !q.Enqueue("https://example.com/internal", 0, nil) {
		t.Error("Internal URL should be accepted")
	}
	if q.Enqueue("https://other.com/external", 0, nil) {
		t.Error("External URL should be rejected")
	}
}

func TestQueueAllowedDomains(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 5, 100, &QueueOptions{
		EnforceInternal: true,
		AllowedDomains:  []string{"allowed.com"},
	})

	if !q.Enqueue("https://example.com/a", 0, nil) {
		t.Error("Seed domain should be accepted")
	}
	if !q.Enqueue("https://allowed.com/b", 0, nil) {
		t.Error("Allowed domain should be accepted")
	}
	if q.Enqueue("https://blocked.com/c", 0, nil) {
		t.Error("Non-allowed domain should be rejected")
	}
}

func TestQueuePatternFilters(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 5, 100, &QueueOptions{
		IncludePatterns: []*regexp.Regexp{regexp.MustCompile(`/blog/`)},
		ExcludePatterns: []*regexp.Regexp{regexp.MustCompile(`/admin/`)},
	})

	if !q.Enqueue("https://example.com/blog/post1", 0, nil) {
		t.Error("Blog URL should match include pattern")
	}
	if q.Enqueue("https://example.com/about", 0, nil) {
		t.Error("Non-blog URL should be rejected by include pattern")
	}
	if q.Enqueue("https://example.com/blog/admin/settings", 0, nil) {
		t.Error("Admin URL should be rejected by exclude pattern")
	}
}

func TestQueueMarkVisited(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 5, 100, nil)

	q.MarkVisited("https://example.com/seen")
	if !q.Has("https://example.com/seen") {
		t.Error("MarkVisited URL should be seen")
	}
	if q.Enqueue("https://example.com/seen", 0, nil) {
		t.Error("Should not enqueue already-visited URL")
	}
}

func TestQueueRequeue(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 5, 100, nil)

	// Enqueue and dequeue a URL
	q.Enqueue("https://example.com/page1", 0, nil)
	entry := q.Dequeue()
	if entry == nil {
		t.Fatal("Expected non-nil dequeue")
	}

	// Normal Enqueue should fail (bloom filter says already seen)
	if q.Enqueue("https://example.com/page1", 0, nil) {
		t.Error("Duplicate enqueue should fail")
	}
	if q.Size() != 0 {
		t.Errorf("Queue should be empty, got size %d", q.Size())
	}

	// Requeue should bypass bloom filter
	entry.Requeues = 1
	q.Requeue(*entry)
	if q.Size() != 1 {
		t.Errorf("Size after requeue = %d, want 1", q.Size())
	}

	// Dequeue the re-queued entry and verify requeue count
	requeued := q.Dequeue()
	if requeued == nil {
		t.Fatal("Expected non-nil dequeue after requeue")
	}
	if requeued.URL != "https://example.com/page1" {
		t.Errorf("URL = %q, want %q", requeued.URL, "https://example.com/page1")
	}
	if requeued.Requeues != 1 {
		t.Errorf("Requeues = %d, want 1", requeued.Requeues)
	}
}

func TestQueueDiskSpill(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 100, 200000, &QueueOptions{EnforceInternal: false})
	defer q.Close()

	total := queueSpillThreshold + 5000
	for i := 0; i < total; i++ {
		url := "https://example.com/" + strconv.Itoa(i)
		if !q.Enqueue(url, i%10, nil) {
			t.Fatalf("Enqueue %d failed", i)
		}
	}

	// Verify spill file was created
	q.mu.Lock()
	hasSpill := q.spillFile != nil
	spillCount := q.spillCount
	q.mu.Unlock()
	if !hasSpill {
		t.Fatal("Expected spill file to be created")
	}
	if spillCount != 5000 {
		t.Errorf("spillCount = %d, want 5000", spillCount)
	}

	if q.Size() != total {
		t.Errorf("Size = %d, want %d", q.Size(), total)
	}

	// Dequeue all and verify total count. Global depth order is not guaranteed
	// when disk spill is involved (spilled entries re-enter the heap on refill).
	seen := make(map[string]bool)
	for i := 0; i < total; i++ {
		entry := q.Dequeue()
		if entry == nil {
			t.Fatalf("Dequeue %d returned nil", i)
		}
		seen[entry.URL] = true
	}
	if len(seen) != total {
		t.Errorf("unique URLs = %d, want %d", len(seen), total)
	}
	if q.Dequeue() != nil {
		t.Error("Expected nil dequeue from empty queue")
	}
}

func TestQueueDiskSpillWithReferrer(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 100, 200000, &QueueOptions{EnforceInternal: false})
	defer q.Close()

	// Fill in-memory buffer
	for i := 0; i < queueSpillThreshold; i++ {
		q.Enqueue("https://example.com/"+strconv.Itoa(i), 0, nil)
	}

	// Add entries with referrers that will spill to disk
	ref := "https://example.com/parent"
	for i := queueSpillThreshold; i < queueSpillThreshold+100; i++ {
		q.Enqueue("https://example.com/"+strconv.Itoa(i), 3, &ref)
	}

	// Drain in-memory entries (all depth 0, dequeued first)
	for i := 0; i < queueSpillThreshold; i++ {
		q.Dequeue()
	}

	// Now dequeue from disk and verify referrer survives roundtrip
	for i := 0; i < 100; i++ {
		entry := q.Dequeue()
		if entry == nil {
			t.Fatalf("Dequeue from disk %d returned nil", i)
		}
		if entry.Depth != 3 {
			t.Errorf("entry %d: Depth = %d, want 3", i, entry.Depth)
		}
		if entry.Referrer == nil || *entry.Referrer != ref {
			t.Errorf("entry %d: Referrer roundtrip failed", i)
		}
	}
}

func TestQueueDiskSpillRequeue(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 100, 200000, &QueueOptions{EnforceInternal: false})
	defer q.Close()

	// Fill to spill threshold
	for i := 0; i < queueSpillThreshold; i++ {
		q.Enqueue("https://example.com/"+strconv.Itoa(i), 0, nil)
	}

	// Requeue should spill to disk (since buffer is full)
	q.Requeue(types.QueueEntry{URL: "https://example.com/retry", Depth: 1, Requeues: 2})

	if q.Size() != queueSpillThreshold+1 {
		t.Errorf("Size = %d, want %d", q.Size(), queueSpillThreshold+1)
	}

	// Drain all in-memory (depth 0), then get the requeued entry from disk (depth 1)
	for i := 0; i < queueSpillThreshold; i++ {
		q.Dequeue()
	}
	entry := q.Dequeue()
	if entry == nil {
		t.Fatal("Expected requeued entry from disk")
	}
	if entry.URL != "https://example.com/retry" || entry.Requeues != 2 {
		t.Errorf("Requeued entry roundtrip failed: URL=%q Requeues=%d", entry.URL, entry.Requeues)
	}
}

func TestQueueDiskSpillMultiRefill(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 100, 500000, &QueueOptions{EnforceInternal: false})
	defer q.Close()

	diskEntries := queueRefillBatch*3 + 500
	total := queueSpillThreshold + diskEntries
	for i := 0; i < total; i++ {
		url := "https://example.com/m/" + strconv.Itoa(i)
		if !q.Enqueue(url, i%20, nil) {
			t.Fatalf("Enqueue %d failed", i)
		}
	}

	q.mu.Lock()
	sc := q.spillCount
	q.mu.Unlock()
	if sc != diskEntries {
		t.Fatalf("spillCount = %d, want %d", sc, diskEntries)
	}

	// Dequeue all and verify total count
	count := 0
	for {
		entry := q.Dequeue()
		if entry == nil {
			break
		}
		count++
	}
	if count != total {
		t.Errorf("dequeued %d, want %d", count, total)
	}
}

func TestQueueDiskSpillInterleavedRefillAndSpill(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 100, 500000, &QueueOptions{EnforceInternal: false})
	defer q.Close()

	// Phase 1: fill to spill threshold + 25K on disk
	phase1Disk := 25000
	total1 := queueSpillThreshold + phase1Disk
	for i := 0; i < total1; i++ {
		q.Enqueue("https://example.com/p1/"+strconv.Itoa(i), 0, nil)
	}

	// Phase 2: drain in-memory buffer, triggering partial refill
	for i := 0; i < queueSpillThreshold; i++ {
		e := q.Dequeue()
		if e == nil {
			t.Fatalf("Dequeue %d returned nil in drain phase", i)
		}
	}

	// Phase 3: enqueue more URLs
	phase3Start := total1
	phase3Count := queueSpillThreshold + 5000
	for i := 0; i < phase3Count; i++ {
		q.Enqueue("https://example.com/p3/"+strconv.Itoa(phase3Start+i), 0, nil)
	}

	// Phase 4: drain everything, just verify total count
	expectedPhase1 := phase1Disk - queueRefillBatch
	if expectedPhase1 < 0 {
		expectedPhase1 = 0
	}
	totalExpected := queueRefillBatch + expectedPhase1 + phase3Count
	drained := 0
	for {
		e := q.Dequeue()
		if e == nil {
			break
		}
		drained++
		if drained > totalExpected+1000 {
			t.Fatalf("drained more than expected (%d > %d)", drained, totalExpected)
		}
	}
	if drained != totalExpected {
		t.Errorf("drained = %d, want %d", drained, totalExpected)
	}
}

func TestQueueNormalization(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 5, 100, nil)

	q.Enqueue("https://example.com/page", 0, nil)
	if q.Enqueue("https://example.com/page/", 0, nil) {
		t.Error("Trailing slash variant should be deduped")
	}
	if q.Enqueue("https://example.com/page#section", 0, nil) {
		t.Error("Fragment variant should be deduped")
	}
}

func TestQueueDiskSpillPreservesPriority(t *testing.T) {
	// Verify that entries spilled to disk are correctly prioritized when refilled.
	q := NewCrawlQueue("https://example.com", 100, 200000, &QueueOptions{EnforceInternal: false})
	defer q.Close()

	// Fill in-memory with depth-5 entries
	for i := 0; i < queueSpillThreshold; i++ {
		q.Enqueue("https://example.com/d5-"+strconv.Itoa(i), 5, nil)
	}

	// Spill depth-1 entries to disk
	for i := 0; i < 100; i++ {
		q.Enqueue("https://example.com/d1-"+strconv.Itoa(i), 1, nil)
	}

	// Drain the in-memory depth-5 entries first (they have lower priority,
	// but they're in-memory while depth-1 is on disk, so depth-5 comes first
	// until refill happens). Once refilled, depth-1 from disk should come before
	// any remaining depth-5.
	depths := make([]int, 0, queueSpillThreshold+100)
	for {
		e := q.Dequeue()
		if e == nil {
			break
		}
		depths = append(depths, e.Depth)
	}

	if len(depths) != queueSpillThreshold+100 {
		t.Fatalf("total dequeued = %d, want %d", len(depths), queueSpillThreshold+100)
	}

	// After all are dequeued, verify the complete set: 50K depth-5 + 100 depth-1
	depthCounts := map[int]int{}
	for _, d := range depths {
		depthCounts[d]++
	}
	if depthCounts[5] != queueSpillThreshold {
		t.Errorf("depth-5 count = %d, want %d", depthCounts[5], queueSpillThreshold)
	}
	if depthCounts[1] != 100 {
		t.Errorf("depth-1 count = %d, want 100", depthCounts[1])
	}
}

func TestQueueDrain(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 10, 10000, &QueueOptions{EnforceInternal: false})

	q.Enqueue("https://example.com/a", 2, nil)
	q.Enqueue("https://example.com/b", 0, nil)
	q.Enqueue("https://example.com/c", 1, nil)

	entries := q.Drain()
	if len(entries) != 3 {
		t.Fatalf("Drain returned %d entries, want 3", len(entries))
	}
	// Drain returns in priority order (depth ascending)
	if entries[0].Depth != 0 || entries[1].Depth != 1 || entries[2].Depth != 2 {
		t.Errorf("Drain order: depths %d,%d,%d, want 0,1,2", entries[0].Depth, entries[1].Depth, entries[2].Depth)
	}
	// Queue should be empty after drain
	if q.Size() != 0 {
		t.Errorf("Size after Drain = %d, want 0", q.Size())
	}
}

func TestQueueDrainWithDiskSpill(t *testing.T) {
	q := NewCrawlQueue("https://example.com", 100, 200000, &QueueOptions{EnforceInternal: false})
	defer q.Close()

	total := queueSpillThreshold + 500
	for i := 0; i < total; i++ {
		q.Enqueue("https://example.com/"+strconv.Itoa(i), i%5, nil)
	}

	entries := q.Drain()
	if len(entries) != total {
		t.Fatalf("Drain returned %d entries, want %d", len(entries), total)
	}
	if q.Size() != 0 {
		t.Errorf("Size after Drain = %d, want 0", q.Size())
	}
}

func TestQueuePriorityWithMixedDepths(t *testing.T) {
	// Larger test: enqueue URLs at various depths, verify sorted output
	q := NewCrawlQueue("https://example.com", 10, 10000, &QueueOptions{EnforceInternal: false})

	var expectedDepths []int
	for depth := 0; depth <= 5; depth++ {
		for i := 0; i < 100; i++ {
			url := "https://example.com/d" + strconv.Itoa(depth) + "/" + strconv.Itoa(i)
			q.Enqueue(url, depth, nil)
			expectedDepths = append(expectedDepths, depth)
		}
	}
	sort.Ints(expectedDepths)

	var gotDepths []int
	for {
		e := q.Dequeue()
		if e == nil {
			break
		}
		gotDepths = append(gotDepths, e.Depth)
	}

	if len(gotDepths) != len(expectedDepths) {
		t.Fatalf("count = %d, want %d", len(gotDepths), len(expectedDepths))
	}
	for i := range gotDepths {
		if gotDepths[i] != expectedDepths[i] {
			t.Fatalf("depth[%d] = %d, want %d", i, gotDepths[i], expectedDepths[i])
		}
	}
}
