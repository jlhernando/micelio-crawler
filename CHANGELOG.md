# Changelog

All notable changes to Micelio. Versions follow [semantic versioning](https://semver.org/). The version shown in the app and used by the in-app updater comes from the git tag.

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

[1.3.0]: https://github.com/jlhernando/micelio-crawler/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/jlhernando/micelio-crawler/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/jlhernando/micelio-crawler/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/jlhernando/micelio-crawler/releases/tag/v1.0.0
