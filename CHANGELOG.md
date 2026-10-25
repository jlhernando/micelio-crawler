# Changelog

All notable changes to Micelio. Versions follow [semantic versioning](https://semver.org/). The version shown in the app and used by the in-app updater comes from the git tag.

## [1.3.4] - 2026-10-25

### Fixed

- **Links with spaces or accents in the query no longer return 400.** A link like `?filter=Acero inoxidable` was sent with a raw space, and servers answered 400 Bad Request. Micelio now encodes the query the way a browser does (`%20`, `%C3%AD`). On a 3,000-page ecommerce test crawl this removed 172 false errors and raised indexable pages from 82.7% to 88.3%.

## [1.3.3] - 2026-10-25

Scheduled crawls get the full crawl setup, show up in History, and can be limited to an allowed network.

### Added

- **Schedule any crawl.** "Schedule…" on New Crawl schedules the current setup: sitemap or list mode, include/exclude patterns, adaptive rate, extractions and every other option. The Schedules page keeps a quick form.
- **Runs appear in History.** Each scheduled run is a normal crawl, tagged with its schedule, so you can open it, compare runs with Diff and get alerts.
- **Schedules survive restarts.** Dashboard schedules are saved and resume when Micelio starts.
- **"Only run when…" checks.** Limit runs to a public IP range (for example, your office network). A failing check skips the run with a reason. During a crawl the check repeats every minute: the crawl pauses until it passes again and stops (with analysis) after 6 hours paused.
- **Run now** and **Test check** buttons, the reason for skipped or stopped runs, and a link to the last run's crawl.
- Webhooks also report skipped and stopped runs (`crawl_skipped`, `crawl_stopped`) with a reason.
- **CLI:** `micelio schedule --config file.json` takes a crawl config in the dashboard's JSON format. New flags: `--name`, `--precheck-egress`, `--precheck-cmd` (shell command that must exit 0; CLI only), `--precheck-interval` and `--check-only`.

### Changed

- Signal and report descriptions now describe Micelio's crawl-time proxies plainly and point to Search Console, logs and analytics for validation.

### Fixed

- A flaky server test that raced post-crawl work against temp-dir cleanup.

## [1.3.2] - 2026-10-24

Kinder to the sites you crawl, much lighter on your machine, and a smaller dashboard.

### Fixed: crawling politeness

- **Crawls started from the dashboard ignored the delay.** The dashboard sent `delay` and the server read `delayMs`, so every dashboard crawl ran with no delay between requests. List mode from the dashboard also crawled nothing. Both now work.
- **Adaptive rate no longer goes faster than your delay or the site's robots.txt Crawl-delay.** It used to speed up to 50 ms between requests. Each host now has its own rate.
- **robots.txt follows Google's rules:** `*` and `$` wildcards, query strings, longest match wins, groups chosen by product token. It is fetched through your proxy, follows redirects, is parsed once per host, and is checked for every allowed domain, not only the start URL. Expect more URLs to be blocked than before.
- **429 and 503 responses:** `Retry-After` pauses the host for all workers (up to 10 minutes). At most 3 attempts per URL. Timeouts and network errors also slow the crawl down.
- **Non-HTML files** (PDFs, videos, archives) are no longer downloaded in full just to read the status code. At most 64 KB is read.
- **Page weight checks** use the crawl's client and proxy, with at most 2 requests at a time per host.

### Fixed: stability

- Two requests to `/stats` at the same time could crash the server.
- Diff crashed on the URLs and Technical tabs when a list was empty.

### Changed: memory and speed (measured on 200,000 pages)

- Stats are capped: long URL lists keep the top 1,000 plus a total, and PageRank is stored per page with a histogram. Stats JSON: 25.8 MB → 0.22 MB.
- Pages are written in batches outside the crawl lock: 83.5 → 7.4 µs per page with 16 workers.
- Inlinks use a shared reverse index: 2.7 ms → 42 ns per lookup. Self-links no longer count as inlinks anywhere.
- The link graph, PageRank and HITS scores are stored in the crawl database, so reopening a crawl is about twice as fast. Authority and hub scores no longer reset to 0 after a restart.
- Embeddings compare pages 37 times faster. Above 10,000 pages they compare pages within the same folder.
- `/pages` caches sorted results; the crawl cache loads each crawl once and can evict by memory when `--memlimit` is set.
- Custom-search regexes are compiled once.
- Removed the unused `/api/crawl/{id}/results` and `/api/crawl/{id}/graph` endpoints, which could allocate several GB on large crawls.

### Changed: dashboard

- Main bundle: 827 KB → 369 KB (247 → 108 KB gzipped). Secondary pages, Explorer, Universe, Treemap and charts load when opened.
- The Content tab's word-count histogram now covers every page (it fetched 50,000 pages and used 100).
- Diff lists show 200 rows with "Show more".
- One WebSocket for the whole dashboard; shared number, date and status formatting.
- Removed 7 unused dependencies.

### Docker

- The Docker image now builds the Go binary with the embedded dashboard. It used to build the old TypeScript crawler. Add `--build-arg WITH_CHROMIUM=true` for JS rendering.

## [1.3.1] - 2026-10-24

### Added

- **Universe page search.** Find a page by title or URL and fly to it.
- **Metric colours in Universe:** Depth, PageRank, Authority and Hub, with a legend. Authority and hub use percentiles because a few pages hold most of the score.
- **Open in Explorer** on a selected page switches to Graph Explorer with that page open.

### Removed

- **Full Graph.** Universe covers the whole-site view for any crawl size. This drops the Cosmos.gl dependency and three web workers.

### Fixed

- Graph Explorer search left the view empty: the camera jumped to graph coordinates instead of screen coordinates.
- Authority and hub scores were 0 for every crawl opened after a restart, because they were never stored with the pages. They are now recomputed from the link graph when a crawl loads (crawls up to 200,000 pages, the same limit as during a crawl).

## [1.3.0] - 2026-10-24

A 3D map of your site's internal links, and directory views that stay fast on 1M-page crawls.

### Added

- **Universe** (Links > Universe). A 3D map where every dot is a page and every line is a real internal link.
  - Each page sits next to the page that links to it on its shortest click path from the start page. Branches with more pages below them get more room.
  - Pages the start page cannot reach by links sit on an outer shell and are labelled as such.
  - Layout toggle: **Link structure** (default) or **Grouped** (pages around their cluster's centre, with lines between clusters showing real link counts).
  - Colour by **Section**, Folder, Subfolder or Template. Section finds the folder most of the site shares and groups by the folder below it.
  - **Cross links** and **Menu, footer & section nav links** toggles add the links that hide the structure on large sites.
  - Click a page to see its inlinks (green), outlinks (amber), click path ("Reached in 2 clicks via …") and a thumbnail.
  - Side panel with sortable cluster and page tables.
- **Page thumbnails**, captured with headless Chrome when you open a page and cached in `~/.micelio/ui/thumbs/`. They are removed when the crawl is deleted.
- **Progressive loading** for large crawls. Universe loads the first 10,000 pages by PageRank, then keeps going until a limit set by the device's memory, CPU and GPU, or until a frame takes longer than 14 ms. Pages and links travel as compact binary data. The 3D view only redraws when something changes.

### Changed

- **Directory tree and treemap** load one folder at a time instead of the whole site. Open a folder to fetch its subfolders and pages, with **Load more** for the rest. A failed folder shows Retry instead of hiding the tree.
- The full directory tree response is capped at 50 children per folder and 5,000 nodes.
- Dashboard built with Vite 8, with unused dependencies removed. The JS renderer uses the chromedp v0.21 API.

### Fixed

- The Links tab could use more than 4 GB of browser memory on a 200,000-page crawl, because the directory tree sent one row per page. A 1M-page crawl now stays near 1 GB with a 9 MB JavaScript heap.
- The directory tree root showed as `//`.
- GSC, GA4 and Plausible numbers in the directory tree are kept after a crawl's pages are trimmed in memory. Values that cannot be known show "—" instead of 0.
- Encoded paths such as `/caf%C3%A9/` now match between the tree views.

## [1.2.0] - 2026-10-22

Automatic page segmentation, correct PageRank on large crawls, and link calculations you can trust.

### Added

- **Segments tab.** Every page gets a value in 15 dimensions: template, directory L1 to L3, extension, text amount, link profile, language, URL parameters, click depth, authority, index state, schema type, host and freshness. Click a value to open All Pages filtered to it.
- JavaScript rendering differences in Issues, and Link Structure in Links.

### Changed

- Sturdier template detection: ties no longer resolve at random, and commerce signals tell product pages from listings.

### Fixed

- PageRank on DB-backed crawls: every page used to score 10.
- Orphans, inlinks, broken-link sources, links to non-indexable pages and link counts now come from the link graph.
- Anchor stats and nofollow counts are collected during the crawl.
- Check External works from the dashboard.
- Structured data is validated.
- Template-filtered stats no longer show false zeros.

## [1.1.0] - 2026-10-20

A redesigned dashboard, clearer crawl results, and security fixes.

### Added

- Crawl coverage on the results overview, with the reason for each exclusion and example URLs.
- Priority issues at the top of the overview.
- User Guide and CLI Reference in `docs/`.

### Changed

- New dashboard look across every page.

### Security

- Patched vulnerable Go and npm dependencies. Requires Go 1.26.5.
- GitHub Actions pinned to commit SHAs, with tighter token permissions. Dependabot enabled.
- Release binaries no longer embed the build machine's source path.

## [1.0.0] - 2026-07-05

First public release: spider, list, sitemap, HEAD-only and scheduled crawls; embedded web dashboard; SEO, content and link analysis; server log analysis; PSI, CrUX, GSC, GA4, Plausible and AI integrations; JS rendering; robots.txt testing; and an in-app updater.

[1.3.3]: https://github.com/jlhernando/micelio-crawler/compare/v1.3.2...v1.3.3
[1.3.2]: https://github.com/jlhernando/micelio-crawler/compare/v1.3.1...v1.3.2
[1.3.1]: https://github.com/jlhernando/micelio-crawler/compare/v1.3.0...v1.3.1
[1.3.0]: https://github.com/jlhernando/micelio-crawler/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/jlhernando/micelio-crawler/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/jlhernando/micelio-crawler/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/jlhernando/micelio-crawler/releases/tag/v1.0.0
