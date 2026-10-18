# Micelio dashboard

The Svelte dashboard uses a shared dark interface inspired by the approved Resend concept. Semantic colors distinguish chart series and statuses; primary actions use a neutral light background.

## Development

```sh
npm ci
npm run dev
```

Vite proxies API and WebSocket requests to `http://localhost:3100`. Set `VITE_API_PROXY_TARGET` to use another local backend.

## Validation

```sh
npm run check
npm run build
npx playwright install chromium
npm run test:ui
```

To use an existing Chrome installation instead of Playwright's browser:

```sh
CHROME_PATH='/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' npm run test:ui
```

The browser suite mocks REST and WebSocket data. It covers all nine route screens at desktop and mobile sizes, navigation, crawl submission, result filters, schedule creation, settings persistence, and missing/error states. It does not validate the crawl engine or external integrations. Set `SCREENSHOT_DIR` to an absolute directory to save desktop previews.

## Interface behavior

- All routes share the sidebar, responsive navigation, page headings, controls, and chart palette.
- Results open on Overview. Detailed URL tables and specialized analysis have their own tabs. Additional diagnostics retain the health score, canonicals, images, PageRank, content, security, and structured data panels.
- Indexability and page-type controls retain API-backed filtering. Missing metrics display unavailable states.
- History shows coverage across the latest eight completed crawls for one selected hostname. Crawl settings can affect counts; the chart does not imply SEO performance improvement.
- Schedules and log uploads summarize saved jobs. Log results retain their existing analytical tabs.
- Test data stays in `tests/fixtures.ts`; the application does not ship demonstration records.

### Persisted crawl exclusions

The status endpoint exposes `coverage` for newly recorded executions. The crawler
records the first rejected check for depth, page limits, domain scope, include and
exclude filters, invalid URLs, robots.txt, and path budgets. Counts are rejected
attempts, not unique URLs; repeated discoveries can count again. Samples are
bounded to five URLs per reason/rule and 2,048 characters per URL.

Snapshots are saved every ten page-progress events and after the execution,
including zero-page and interrupted runs. A partial checkpoint can omit decisions
made after the last save. A resumed execution replaces the previous snapshot;
these are not cumulative counts across runs. Legacy jobs omit `coverage` rather
than reporting zero exclusions. Coverage survives UI database reopen and remains
available when page statistics are unavailable.
