# Micelio Architecture

This page is for people who read or change the code. To use Micelio, see the
[User Guide](USER-GUIDE.md) and the [CLI Reference](CLI-REFERENCE.md).

## Stack

- **Language**: Go 1.26. One binary, no CGO.
- **CLI**: cobra.
- **Server**: net/http and gorilla/websocket. The dashboard gets live crawl
  updates over WebSocket.
- **Frontend**: Svelte 5 (runes), Tailwind CSS 4 and Vite 8. The built dashboard
  is embedded in the binary with `go:embed`, so no separate web server is needed.
- **Graphs**: Sigma.js 3, graphology and ForceAtlas2 for Graph Explorer;
  three.js for Universe.
- **Storage**: pure Go SQLite (zombiezen.com/go/sqlite) for dashboard state and
  crawl databases. gzip/zstd for JSONL output; zstd for SQLite blobs such as raw
  HTML.
- **JS rendering**: chromedp (headless Chrome over the DevTools Protocol), with
  DOM stability polling, resource blocking and JS error capture.
- **Auth for Google APIs**: JWT service accounts (golang-jwt/jwt/v5) for GSC and
  GA4, or a local OAuth token from `micelio gsc-auth`.
- **Anti-bot**: Chrome TLS fingerprint mimicry, Tor circuit rotation through the
  control port, and browser-like headers.

## Crawl engine

The crawler is a goroutine pool with configurable concurrency, delay, depth and
memory limit. It supports adaptive rate limiting, pause, resume, cancel, Stop &
Analyze, and live tuning of delay, concurrency and page limit
(`internal/server/tune.go`, `POST /api/crawl/{id}/tune`). The delay is per site,
and live tuning never goes below the robots.txt Crawl-delay.

Before the first request only robots.txt is read. Sitemap discovery and the
soft-404 probe run while the crawl goes.

Post-crawl analysis has two phases:

- `RunCoreAnalysis` is fast and synchronous: stats, issues, PageRank and what
  the Results page needs first.
- `RunDeferredAnalysis` runs slow work (link suggestions and similar) in a
  background goroutine started by the crawl manager.

External links are checked after the results are shown. Each site has its own
queue, served in turn; 20 checks run at once, at most 2 per site (6 for large
platforms). A site that answers 429 is checked one URL at a time with growing
pauses, and URLs it never answers are left unchecked, not broken.

Dashboard crawls and imported crawls (`internal/server/import.go`) share the
same analysis code (`analyzeAndCache`).

## Universe

`internal/server/universe.go` builds the layout once per crawl. It groups pages
into clusters by folder or template, places high-PageRank hubs at each cluster's
centre, and counts links between clusters. Pages stream to the dashboard as
binary chunks of 10 bytes per page, ordered by PageRank: 10,000 first, then more
until the device budget or a 14 ms frame limit is reached. Real links from the
crawl graph follow as an index buffer.

Links to navigation targets (pages linked from 40%+ of the site, or from half of
their own section) are hidden by default. Thumbnails are captured on demand with
headless Chrome and cached in `~/.micelio/ui/thumbs/` (`universe_thumbs.go`).

## Directory tree

`GET /directory-tree?lazy=1&path=&offset=&limit=&depth=` serves one directory at
a time from a directory-only index (`internal/server/dirtree_lazy.go`). The
dashboard fetches children on expand, 100 at a time. Without `lazy` the endpoint
returns the full tree capped at 50 children per directory and 5,000 nodes (the
treemap uses it).

## Server log pipeline

The log pipeline (`internal/logs/`) is built for 50-100M+ lines across several
large files. It was tested with 3 files of 6.5 GB each (19.4 GB, 28M lines):
2.5 minutes, about 303K lines/s, and instant deletion.

- **GC suppression during parsing.** `debug.SetGCPercent(-1)` turns GC off
  while parsing. The aggregator's maps hold millions of pointer-bearing entries,
  and GC used 58% of CPU in profiles. With GOMEMLIMIT set, the runtime still
  runs GC at the memory cap, so it does not run out of memory.
- **Zero-I/O parsing.** No SQLite writes while parsing. URL stats stay in memory
  and are saved in one bulk INSERT at the end. Before, UPSERTs used 99.9% of
  post-parse CPU on B-tree index lookups.
- **Per-job URL tables.** Each log job gets its own table (`url_stats_<job_id>`)
  instead of one shared table with a `job_id` column. Deleting a job is a DROP
  TABLE (it was 8-10 minutes for 12.7M rows). Rows are smaller and jobs do not
  compete for one index. The old shared table is still read for older jobs.
- **Sorted bulk INSERT.** URLs are sorted by path before insertion, so the
  WITHOUT ROWID B-tree gets sequential writes and avoids page splits.
- **Single aggregator, parallel parsers.** Parser goroutines send entries
  through a channel with a 65K buffer to one aggregator. One writer means no
  locks.
- **Zero-alloc bot matching.** `containsCI()` matches case-insensitively without
  `strings.ToLower()`, which saves about 66M string allocations per run. More
  than 100 bot signatures are matched.
- **Work moved to the parsers.** Timestamp formatting, hour and weekday are
  computed in the parallel parsers, not in the aggregator.
- **Fast CSV parser.** `splitSimpleQuotedCSV()` skips `encoding/csv` for
  CloudFront CSV lines without escaped quotes, about 3x faster.
- **Scanner buffer.** 1 MB initial buffer instead of the default 64 KB, so fewer
  syscalls.
- **Bot name caching.** `addBot()` caches `e.Bot.Name`, saving about 20 map key
  hashes per bot entry.
- **Batch map allocation.** All nested bot maps (URLs, IPs, heatmaps, daily,
  hourly) are allocated together the first time a bot is seen.
- **Async deletion.** The DELETE API returns at once and cleanup runs in the
  background. The dashboard shows a "deleting..." badge; on failure the status
  goes back to "failed", and a second delete is refused.
- **Per-phase timing.** Upload, parse and analysis times are stored in the
  `log_jobs` table and shown in the dashboard.
- **Stale job recovery.** At start, log jobs stuck in "processing" or "deleting"
  and crawl jobs stuck in "running" or "paused" are marked "failed", because
  their goroutines are gone after a restart.
- **Profiling.** pprof endpoints are served at `/debug/pprof/` for CPU profiles
  and PGO builds.

For how memory stays bounded on large crawls, see
[Memory management](MEMORY-MANAGEMENT.md).

## Project structure

```
cmd/micelio/          CLI entry point and dashboard embed
internal/
  alerts/             Alert rules, crawl reports (email, Telegram, Slack)
  analysis/           PageRank, link intelligence, n-grams, embeddings, soft 404, reporter
  browser/            Headless Chrome rendering (chromedp)
  crawler/            HTTP fetcher, queue, robots.txt, URL normalization, stealth, rate limiting
  envfile/            Loads ~/.micelio/micelio.env at start
  extract/            HTML extraction (goquery), hreflang, schema, structured data
  integration/        PSI, CrUX, Plausible, GSC, GA4 clients
  logs/               Server log parser, analyser, bot detection, multi-file merge
  report/             HTML report generator, diff comparison
  scheduler/          Cron scheduler with state persistence and prechecks
  server/             HTTP API, WebSocket, SQLite store, crawl manager, Universe
  storage/            JSONL/CSV writers, crawl store for merged log and crawl pages
  types/              Shared types (PageData, CrawlConfig, CrawlStats)
  updater/            In-app updater with checksum verification and rollback
  urlutil/            URL escaping and normalization helpers
  webhook/            HTTP webhook dispatch with retry
dashboard/            Svelte 5 dashboard source
  src/routes/         Pages (Setup, Monitor, Results, History, Diff, Logs, LogResults, Schedules, Settings)
  src/lib/components/ UI components (GraphExplorer, UniverseGraph, PagesTable, DirectoryTree...)
  tests/              Playwright UI tests
src/                  TypeScript crawler engine (the original implementation)
docs/                 User docs; docs/specs/ holds design specs
packaging/macos/      macOS .app bundle files
scripts/              Helper scripts (secret check, etc.)
snippets/             Example JS snippets for --snippet
```

## Design specs

Older design documents live in [docs/specs/](specs/). They record the plans
behind features and may not match the current code in every detail:

- [UI-SPEC.md](specs/UI-SPEC.md)
- [LINK-GRAPH-PERFORMANCE-SPEC.md](specs/LINK-GRAPH-PERFORMANCE-SPEC.md)
- [spec-crawl-comparison.md](specs/spec-crawl-comparison.md)
- [spec-internal-link-intelligence.md](specs/spec-internal-link-intelligence.md)
- [spec-server-log-analysis.md](specs/spec-server-log-analysis.md)
