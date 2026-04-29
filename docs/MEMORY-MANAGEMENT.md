# Memory Management in Micelio

Micelio uses several complementary techniques to keep memory bounded during large crawls. This document explains each mechanism, how they interact, and provides real-world data from stress testing.

## Table of Contents

1. [GOMEMLIMIT — Soft Memory Ceiling](#gomemlimit--soft-memory-ceiling)
2. [sync.Pool — Buffer Reuse](#syncpool--buffer-reuse)
3. [Disk-Backed Maps — Off-Heap Accumulators](#disk-backed-maps--off-heap-accumulators)
4. [GC Checkpoint — Phase Transition](#gc-checkpoint--phase-transition)
5. [Struct Alignment — Zero-Cost Savings](#struct-alignment--zero-cost-savings)
6. [How They Work Together](#how-they-work-together)
7. [Does GOMEMLIMIT Cause Data Loss?](#does-gomemlimit-cause-data-loss)
8. [Stress Test Results](#stress-test-results)
9. [Configuration Guide](#configuration-guide)

---

## GOMEMLIMIT — Soft Memory Ceiling

### What It Is

`GOMEMLIMIT` is a Go 1.19+ runtime feature that sets a **soft memory limit** for the garbage collector. Micelio exposes it via the `--memlimit` CLI flag.

```bash
micelio crawl https://example.com --memlimit 4GiB
```

### How It Works

Without GOMEMLIMIT, Go's GC uses `GOGC` (default 100%) to decide when to run: it triggers when the heap grows to 2x the size of live data after the last collection. This means:

- After GC finds 500 MB of live data, it won't run again until heap reaches ~1 GB
- After GC finds 1 GB of live data, next trigger is at ~2 GB
- Memory grows proportionally to live data — no upper bound

With GOMEMLIMIT set (e.g., 4 GiB):

1. **Below the limit**: GC behaves normally using GOGC rules
2. **Approaching the limit**: GC becomes progressively more aggressive — it runs at smaller heap growth increments (as low as 5-10% instead of 100%)
3. **At the limit**: GC runs almost continuously to keep memory under the cap
4. **Above the limit**: Go will **exceed the limit rather than crash or drop data** — it's a *soft* limit, not a hard cap

### Where It's Implemented

```
cmd/micelio/main.go
```

The `--memlimit` flag is parsed in the root command's `PersistentPreRun`, so it applies to all subcommands (crawl, list, sitemap, head, ui, etc.):

```go
rootCmd.PersistentFlags().StringVar(&memLimit, "memlimit", "",
    "soft memory limit (e.g. 2GiB, 512MiB)")
```

The `parseMemLimit()` function accepts human-readable sizes: `B`, `KB`, `MB`, `GB`, `TB` (SI) and `KiB`, `MiB`, `GiB`, `TiB` (binary). It calls `debug.SetMemoryLimit()` from Go's `runtime/debug` package.

### Why It Matters for Crawling

Large crawls generate massive throughput of short-lived objects:

- HTTP response bodies (20-500 KB each)
- Parsed HTML DOM trees (goquery documents)
- Extracted SEO data structs
- URL strings, link slices, header maps

Without GOMEMLIMIT, Go allows heap to grow freely between GC cycles. A crawl processing 3-5 pages/second with complex extraction can easily accumulate 500 MB+ of garbage between collections. GOMEMLIMIT caps this growth, trading slightly more CPU for GC in exchange for bounded memory.

### What GOMEMLIMIT Does NOT Do

- **Does not kill the process** — if live data exceeds the limit, Go keeps running
- **Does not drop or truncate data** — GC only collects unreachable objects
- **Does not affect disk I/O** — output files (JSONL, SQLite) are unaffected
- **Does not reduce throughput significantly** — in stress tests, crawl speed was maintained or improved (see [Stress Test Results](#stress-test-results))

---

## sync.Pool — Buffer Reuse

### What It Is

`sync.Pool` is Go's built-in object pool that reuses allocated objects instead of creating new ones. Micelio uses it for HTTP response body buffers.

### Where It's Implemented

```
internal/crawler/fetcher.go
```

```go
var bodyBufPool = sync.Pool{
    New: func() interface{} {
        return bytes.NewBuffer(make([]byte, 0, 64*1024)) // 64 KB initial
    },
}

const maxPoolBufSize = 256 * 1024 // don't return oversized buffers to pool
```

### How It Works

1. **Get**: Before reading an HTTP response body, fetch a buffer from the pool
2. **Use**: Read the response into the buffer (up to 10 MB body cap)
3. **Put**: After extracting data, return the buffer to the pool — but only if it hasn't grown beyond 256 KB (prevents memory leaks from outlier pages)
4. **GC interaction**: `sync.Pool` items are cleared on every GC cycle. This is fine — the pool rebuilds naturally on the next fetch

### Why the 256 KB Cap

Most HTML pages are 20-100 KB. Occasionally, a page might be 5-10 MB (e.g., Wikipedia articles with large tables). Without the cap, one oversized page would cause its buffer to be returned to the pool and reused at its inflated capacity, permanently consuming extra memory. The 256 KB cap ensures oversized buffers are discarded (eligible for GC) instead.

### Impact

In benchmarks:
- **8,192 bytes/op → 0 bytes/op** (100% allocation elimination for the hot path)
- **705.4 ns/op → 7.455 ns/op** (94x faster buffer acquisition)
- **30% reduction in GC frequency** — fewer allocations means less garbage

The reduced GC pressure from sync.Pool is why the optimized binary crawls **44% faster** than the baseline in stress tests. Less time in GC = more time fetching and parsing.

---

## Disk-Backed Maps — Off-Heap Accumulators

### The Problem

During a crawl, two data structures grow linearly with pages crawled:

1. **externalLinks**: maps each external URL → list of source pages that link to it
2. **resourceRefs**: maps each page URL → list of resources it references (images, scripts, stylesheets)

These are only needed *after* the crawl completes (for HEAD checking and page weight analysis). But if kept in memory during the crawl, they grow unboundedly:

- externalLinks: ~3 KB per unique external URL
- resourceRefs: ~4 KB per page
- At 50K pages: ~150 MB for externalLinks + ~200 MB for resourceRefs = **350 MB** of avoidable memory

### The Solution

Stream entries to temp files during crawl, read them back when needed.

### Where It's Implemented

```
internal/crawler/diskmap.go
```

**externalLinksDisk** — TSV format:
```
<escaped-url>\t<escaped-source-page>\n
```
- Uses `bufio.Writer` with 64 KB buffer for efficient disk writes
- Tab and newline characters in URLs are escaped to preserve TSV integrity
- `Add()` is called under the crawler's mutex — no internal locking needed

**resourceRefsDisk** — JSONL format:
```json
{"p":"https://example.com/page","r":[{"url":"style.css","type":"stylesheet"}]}
```
- Uses `json.Encoder` writing to a `bufio.Writer`
- One JSON line per page, compact field names (`p`, `r`) to minimize disk I/O

### Lifecycle

1. **Crawl start**: Create temp files in OS temp directory
2. **During crawl**: Stream entries via `Add()` — memory cost is only the 64 KB write buffer
3. **Crawl complete**: Call `Load()` — flushes buffer, seeks to start, reads entire file into a map
4. **Cleanup**: Temp files are deleted after `Load()` (or on error)

### Impact

At 50K pages, RSS is **3.8 GB** instead of a projected **10.7 GB** — a **64% reduction**. The disk-backed maps contribute ~0 bytes to RSS during the crawl phase, compared to ~350 MB for in-memory maps.

---

## GC Checkpoint — Phase Transition

### What It Is

A single `debug.FreeOSMemory()` call between the crawl phase and the post-crawl analysis phase.

### Where It's Implemented

```
internal/crawler/orchestrator.go (line ~740)
```

```go
// GC checkpoint: release crawl-phase allocations before loading post-crawl data.
debug.FreeOSMemory()
```

### Why It Exists

When the crawl completes, these objects become garbage:
- The URL frontier queue (potentially thousands of URLs)
- The HTTP client and connection pool
- sync.Pool buffers
- Temporary parsing structures

Immediately after, `Load()` reads disk-backed maps into memory (potentially hundreds of MB). Without the GC checkpoint, both sets of allocations coexist briefly, causing a **peak memory spike**.

`debug.FreeOSMemory()` forces an immediate GC cycle and then tells the OS to reclaim freed pages (`madvise(MADV_DONTNEED)` on Linux/macOS). This creates a clean memory state before loading post-crawl data.

### Impact

Prevents a 200-500 MB transient RSS spike at the crawl/analysis phase boundary. This is especially important when GOMEMLIMIT is set — it avoids unnecessary GC thrashing during the transition.

---

## Struct Alignment — Zero-Cost Savings

### What It Is

Go structs have padding bytes inserted between fields to satisfy CPU alignment requirements. By reordering fields from largest to smallest, padding is minimized.

### Where It's Applied

The main types in `internal/types/` (PageData, CrawlStats, etc.) have fields ordered for optimal alignment. This was verified with `go vet -fieldalignment`.

### Impact

Typically 15-33% reduction in per-struct size, with **zero runtime cost**. At 100K pages, this saves several MB of RSS.

---

## How They Work Together

Here's the lifecycle of memory during a typical large crawl:

```
Crawl Start
│
├─ GOMEMLIMIT set (e.g., 4 GiB)
│
├─ Phase 1: CRAWLING
│   │
│   ├─ For each page:
│   │   1. Get buffer from sync.Pool (0 bytes allocated)
│   │   2. Fetch HTTP response into buffer
│   │   3. Parse HTML, extract SEO data
│   │   4. Write PageData to JSONL + SQLite (data safe on disk)
│   │   5. Stream external links to disk-backed TSV
│   │   6. Stream resource refs to disk-backed JSONL
│   │   7. Return buffer to sync.Pool (if ≤ 256 KB)
│   │   8. PageData, DOM tree, etc. become garbage
│   │
│   ├─ GC runs periodically:
│   │   - Below GOMEMLIMIT: normal GOGC schedule
│   │   - Near GOMEMLIMIT: aggressive, frequent collections
│   │   - sync.Pool cleared each cycle (rebuilds on next fetch)
│   │   - RSS may decrease as Go returns pages to OS
│   │
│   └─ Memory profile:
│       - Live data: URL frontier, visited set, crawl config, stats
│       - Disk-backed maps contribute ~0 bytes
│       - RSS grows sub-linearly (KB/page ratio decreases)
│
├─ GC CHECKPOINT (debug.FreeOSMemory)
│   └─ Releases: frontier, HTTP client, pools, temp structures
│
├─ Phase 2: POST-CRAWL ANALYSIS
│   │
│   ├─ Load external links from disk → in-memory map
│   ├─ Load resource refs from disk → in-memory map
│   ├─ HEAD check external URLs
│   ├─ Calculate page weight, link intelligence, PageRank
│   │
│   └─ Memory profile:
│       - Maps loaded from disk (bounded, one-time)
│       - Crawl-phase allocations already freed
│       - No peak overlap between phases
│
└─ Crawl Complete
    └─ Output: JSONL file + SQLite DB (unchanged by any memory optimization)
```

### Key Insight: No Data Loss Is Possible

Each optimization targets a different layer, and none can affect output data:

| Optimization | What it reclaims | Can it lose data? |
|---|---|---|
| GOMEMLIMIT | Unreachable (garbage) objects | No — only collects objects nothing references |
| sync.Pool | Reuses temporary buffers | No — data is extracted before buffer is returned |
| Disk-backed maps | Moves accumulation to disk | No — data is read back in full at Load() |
| GC checkpoint | Crawl-phase temporaries | No — called after crawl is complete |
| Struct alignment | Padding bytes | No — padding contains no data |

---

## Does GOMEMLIMIT Cause Data Loss?

**No. It is impossible for GOMEMLIMIT to cause data loss.** Here's why:

### How Go's Garbage Collector Works

Go uses a **tracing garbage collector**. Starting from known "roots" (global variables, stack variables, goroutine stacks), it traces every pointer to find all **reachable** objects. Everything not reachable is garbage.

An object is reachable if and only if some live variable, struct field, map entry, slice element, or channel holds a reference to it. The GC **cannot** free an object that any part of the program can still access.

### What GOMEMLIMIT Changes

GOMEMLIMIT only changes **when** GC runs, not **what** it collects:

- Without GOMEMLIMIT: GC runs when heap doubles (GOGC=100%)
- With GOMEMLIMIT: GC runs more frequently near the limit

In both cases, GC collects the exact same set of objects — unreachable garbage. GOMEMLIMIT just makes it happen sooner.

### Micelio's Data Flow

```
HTTP Response → Parse → Extract → Write to JSONL + SQLite → Done
                                   ^^^^^^^^^^^^^^^^^^^^^^^^
                                   Data is on disk here.
                                   In-memory copy is now garbage.
```

By the time GC runs, crawl data is already persisted to disk. The in-memory objects (HTML body, DOM tree, parsed structs) are temporary processing buffers. GC frees them — the disk copies are untouched.

### The Soft Limit Guarantee

If the program genuinely needs more live memory than the GOMEMLIMIT value, **Go will exceed the limit**. It will never sacrifice correctness for the memory target. The Go specification explicitly states:

> "The memory limit is a soft limit; the runtime may exceed it in certain circumstances."

---

## Stress Test Results

### Test Setup

- **URL**: https://en.wikipedia.org/wiki/Main_Page (100K max pages)
- **Settings**: depth 10, concurrency 3, delay 500ms
- **Features**: link-intelligence, ngrams, embeddings, page-weight, check-external, sitemap-out, html
- **GOMEMLIMIT**: 4 GiB

### Head-to-Head Comparison at ~11,500 Pages

| Metric | Before Optimizations | After Optimizations | Change |
|---|---|---|---|
| RSS memory | 2,444 MB | 1,935 MB | **-21%** |
| Crawl speed | 3.1-3.2 p/s | 4.5-4.6 p/s | **+44% faster** |
| RSS per page | 213 KB/page | 169 KB/page | -21% |
| JSONL output | 542 MB | 539 MB | ~same |
| SQLite DB | 133 MB | 132 MB | ~same |

### Projected Comparison at 50,000 Pages

| Metric | After Optimizations (actual) | Before Optimizations (projected) | Change |
|---|---|---|---|
| RSS memory | 3,801 MB | ~10,650 MB | **-64%** |
| Crawl speed | 3.4 p/s | ~3.1 p/s | +10% |
| RSS per page | 76 KB/page | ~213 KB/page | -64% |

### RSS Growth Over Time (Optimized Binary)

| Pages | RSS (MB) | KB/page | Notes |
|---|---|---|---|
| 175 | 305 | 1,786 | Startup overhead |
| 1,712 | 768 | 460 | |
| 4,376 | 1,157 | 271 | |
| 8,585 | 1,672 | 200 | |
| 11,474 | 1,935 | 169 | Head-to-head point |
| 17,218 | 2,539 | 148 | |
| 24,168 | 3,258 | 135 | |
| 33,240 | 3,955 | 119 | GOMEMLIMIT kicking in |
| 35,000 | 3,949 | 113 | RSS plateauing |
| 42,277 | 4,222 | 100 | |
| 45,948 | 3,854 | 84 | RSS decreased |
| 49,754 | 3,801 | 76 | 50K milestone |

### Why RSS Decreases After 40K Pages

1. **GOMEMLIMIT pressure**: At ~4 GiB, GC runs very aggressively, freeing garbage faster than it's created
2. **Go scavenger**: Background goroutine returns freed memory pages to the OS via `madvise`
3. **Frontier stabilization**: The URL frontier grows slowly as most discovered URLs are already visited
4. **Disk-backed maps**: externalLinks and resourceRefs add 0 bytes to RSS — no linear growth component
5. **Net effect**: RSS *decreases* as the per-page overhead ratio drops, because fixed startup costs are amortized over more pages

---

## Configuration Guide

### Recommended GOMEMLIMIT Values

| Machine RAM | Recommended `--memlimit` | Reasoning |
|---|---|---|
| 8 GB | `2GiB` | Leave room for OS, browser, other apps |
| 16 GB | `4GiB` | Good balance for large crawls |
| 32 GB | `8GiB` | Enterprise-scale crawls |
| 64 GB+ | `16GiB` | Maximum throughput for 100K+ page crawls |

General rule: set GOMEMLIMIT to **25-50% of available RAM**. This leaves room for:
- OS file cache (speeds up disk-backed map I/O)
- Other applications
- The Go runtime itself (stacks, GC metadata) which is outside the heap limit

### When to Use --memlimit

- **Always** for crawls over 10,000 pages
- **Recommended** for any crawl with `--check-external` (many URLs tracked)
- **Optional** for small crawls (< 1,000 pages) — overhead is negligible either way

### When NOT to Use --memlimit

- If you want maximum throughput on a dedicated machine with plenty of RAM, omitting `--memlimit` lets Go use all available memory with minimal GC overhead
- If your crawl is small (< 5,000 pages), the overhead is too small to matter

### Example Commands

```bash
# Standard enterprise crawl with memory control
micelio crawl https://example.com -d 10 -p 50000 --memlimit 4GiB

# Large crawl on a dedicated 64 GB server
micelio crawl https://big-site.com -p 500000 --memlimit 16GiB

# Quick audit — no memlimit needed
micelio crawl https://small-site.com -p 500
```
