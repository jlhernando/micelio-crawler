package gctune

import (
	"math"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
)

// GC off (GOGC=-1) still collects when the heap nears the soft memory
// limit; this is what keeps a log analysis from growing without end.
func TestGCOffMemLimitStillCollects(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(100))
	prev := debug.SetMemoryLimit(math.MaxInt64)
	defer debug.SetMemoryLimit(prev)

	rel := Hold(-1, 64<<20) // 64 MiB
	defer rel()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var sink []byte
	for i := 0; i < 2000; i++ { // 2 GB of garbage
		sink = make([]byte, 1<<20)
	}
	_ = sink
	runtime.ReadMemStats(&after)
	if after.NumGC == before.NumGC {
		t.Fatal("no GC ran with GOGC=-1 and a memory limit")
	}
	if after.HeapAlloc > 256<<20 {
		t.Fatalf("heap %d MiB, want near the 64 MiB limit", after.HeapAlloc>>20)
	}
}

func readGC() int {
	v := debug.SetGCPercent(-1)
	debug.SetGCPercent(v)
	return v
}

// The ordering from the finding: A holds, B holds, A releases first, B
// releases last. With plain save/restore B put back A's -1 and GC stayed off.
func TestHoldOverlapRestoresOriginal(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(100))
	relA := Hold(-1, 0)
	relB := Hold(-1, 0)
	if got := readGC(); got != -1 {
		t.Fatalf("during holds GOGC = %d, want -1", got)
	}
	relA()
	if got := readGC(); got != -1 {
		t.Fatalf("after A GOGC = %d, want -1 (B still holds)", got)
	}
	relB()
	if got := readGC(); got != 100 {
		t.Fatalf("after both GOGC = %d, want 100", got)
	}
	relB() // second call is a no-op
	if got := readGC(); got != 100 {
		t.Fatalf("double release changed GOGC to %d", got)
	}
}

func TestHoldGCOnWins(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(100))
	logs := Hold(-1, 0)
	if got := readGC(); got != -1 {
		t.Fatalf("GOGC = %d, want -1", got)
	}
	crawl := Hold(50, 0)
	if got := readGC(); got != 50 {
		t.Fatalf("GOGC = %d, want 50 (GC on wins)", got)
	}
	crawl()
	if got := readGC(); got != -1 {
		t.Fatalf("GOGC = %d, want -1 after crawl release", got)
	}
	logs()
	if got := readGC(); got != 100 {
		t.Fatalf("GOGC = %d, want 100", got)
	}
}

func TestHoldDefaultMemLimit(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(100))
	prev := debug.SetMemoryLimit(math.MaxInt64)
	defer debug.SetMemoryLimit(prev)

	rel := Hold(-1, 3<<30)
	if got := debug.SetMemoryLimit(-1); got != 3<<30 {
		t.Fatalf("memory limit = %d, want 3 GiB", got)
	}
	rel()
	if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
		t.Fatalf("memory limit = %d, want none after release", got)
	}

	// A limit the user set is kept.
	debug.SetMemoryLimit(1 << 30)
	rel = Hold(-1, 3<<30)
	if got := debug.SetMemoryLimit(-1); got != 1<<30 {
		t.Fatalf("user limit replaced: %d", got)
	}
	rel()
	if got := debug.SetMemoryLimit(-1); got != 1<<30 {
		t.Fatalf("user limit changed after release: %d", got)
	}
}

func TestHoldReleasedOnPanic(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(100))
	func() {
		defer func() { _ = recover() }()
		rel := Hold(-1, 0)
		defer rel()
		panic("boom")
	}()
	if got := readGC(); got != 100 {
		t.Fatalf("GOGC = %d after panic, want 100", got)
	}
}

func TestHoldConcurrent(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(100))
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pct := -1
			if i%3 == 0 {
				pct = 50
			}
			rel := Hold(pct, 0)
			defer rel()
		}(i)
	}
	wg.Wait()
	if got := readGC(); got != 100 {
		t.Fatalf("GOGC = %d after concurrent holds, want 100", got)
	}
}

func TestHoldPreservesStricterUserGC(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	release := Hold(50, 0)
	defer release()
	if got := readGC(); got != 10 {
		t.Fatalf("user GOGC10 relaxed to%d", got)
	}
	release()
	if got := readGC(); got != 10 {
		t.Fatalf("restored GOGC=%d", got)
	}
}
