// Package gctune shares the process-wide garbage collector settings
// (GOGC and the soft memory limit) between jobs that run at the same time.
//
// debug.SetGCPercent and debug.SetMemoryLimit change one global value. When
// two jobs each save the old value and put it back on return, the job that
// finishes last can restore the other job's temporary value, for example
// leaving GC switched off for the rest of the process. Hold avoids that: the
// first hold saves the original settings, every hold is counted, and the
// last release restores the original.
package gctune

import (
	"math"
	"runtime/debug"
	"sync"
)

var (
	mu      sync.Mutex
	holds   = map[*hold]struct{}{}
	baseGC  int   // GOGC before the first hold
	baseMem int64 // memory limit before the first hold
	memSet  bool  // a hold installed a default memory limit
)

type hold struct {
	percent  int
	memLimit int64
}

// Hold applies a GOGC value while the caller works and returns a release
// function (safe to call more than once; call it with defer so a panic
// releases too).
//
// With several holds the one that collects most wins: the lowest percent
// that is not negative, and -1 (GC off) only when every hold asks for it.
// A stricter nonnegative user GOGC is preserved when a hold enables GC.
// Memory safety beats speed: a crawl asking for GOGC=50 keeps GC on even
// while a log analysis would like it off.
//
// defaultMemLimit > 0 sets that soft memory limit for as long as a hold
// asks for one, but only if the process had no limit (math.MaxInt64)
// before the first hold; a limit set by the user (--memlimit, GOMEMLIMIT)
// is never changed. With GC off the memory limit is what still makes the
// runtime collect, so callers that pass -1 should pass a default limit too.
func Hold(percent int, defaultMemLimit int64) (release func()) {
	h := &hold{percent: percent, memLimit: defaultMemLimit}
	mu.Lock()
	if len(holds) == 0 {
		baseGC = debug.SetGCPercent(-1)
		debug.SetGCPercent(baseGC)
		baseMem = debug.SetMemoryLimit(-1)
		memSet = false
	}
	holds[h] = struct{}{}
	applyLocked()
	mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			delete(holds, h)
			applyLocked()
		})
	}
}

// applyLocked sets GOGC and the memory limit from the current holds.
func applyLocked() {
	if len(holds) == 0 {
		debug.SetGCPercent(baseGC)
		if memSet {
			debug.SetMemoryLimit(baseMem)
			memSet = false
		}
		return
	}
	pct := math.MaxInt
	var mem int64
	for h := range holds {
		if h.percent >= 0 && h.percent < pct {
			pct = h.percent
		}
		if h.memLimit > mem {
			mem = h.memLimit
		}
	}
	if pct == math.MaxInt {
		pct = -1 // every hold wants GC off
	} else if baseGC >= 0 && baseGC < pct {
		pct = baseGC // never relax a user's stricter positive GC policy
	}
	debug.SetGCPercent(pct)
	if baseMem == math.MaxInt64 {
		switch {
		case mem > 0:
			debug.SetMemoryLimit(mem)
			memSet = true
		case memSet:
			debug.SetMemoryLimit(baseMem)
			memSet = false
		}
	}
}
