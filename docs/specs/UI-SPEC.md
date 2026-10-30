# Micelio Web UI Specification

> **Command:** `micelio ui [--port <number>]`
> **Status:** Proposed
> **Version:** 1.0
> **Date:** 2026-02-28

---

## 1. Overview

### 1.1 Purpose

The `micelio ui` command launches a full web-based interface for configuring, running, monitoring, and analyzing crawls. It replaces the need to memorize CLI flags and provides a persistent dashboard for crawl history, results comparison, and settings management.

### 1.2 Command Signature

```
micelio ui [--port <number>]
```

- `--port <number>` -- HTTP server port (default: `3000`)
- Opens `http://localhost:<port>` in the default browser on launch
- Runs until terminated with `Ctrl+C` (SIGINT/SIGTERM)

### 1.3 Technology Stack

| Layer | Technology | Rationale |
|-------|-----------|-----------|
| Frontend framework | Svelte 5 (runes mode) | Reactive, compiled, small bundle size |
| CSS framework | Tailwind CSS 4 | Utility-first, consistent design tokens |
| Component library | shadcn-svelte (bits-ui + tailwind-variants) | Accessible, unstyled primitives |
| Build tool | Vite 6 | Fast HMR in dev, optimized production builds |
| Backend HTTP | Node.js `node:http` + custom router | Zero dependencies, matches existing project style |
| Real-time | WebSocket via `ws` npm package | Crawl progress streaming |
| Storage | SQLite via `better-sqlite3` (reuses `CrawlStore` pattern) | Already a project dependency |
| Charts | D3.js (moved from CDN to npm) | Already used in HTML report graph |
| Client routing | Hash-based SPA router (custom, ~50 LOC) | No server-side routing needed |

### 1.4 Design Principles

1. **Reuse existing engine** -- The UI calls the same `crawl()` function from `src/orchestrator.ts` with the same `CrawlConfig` type. No duplicate crawl logic.
2. **Zero new runtime dependencies in core** -- The UI server uses `node:http` from the standard library. Only `ws` is added for WebSocket support.
3. **Offline-first** -- Static assets are bundled at build time and served from the Node process. No CDN dependencies at runtime.
4. **Progressive disclosure** -- The crawl setup form shows basic options by default; advanced sections are collapsible.
5. **Dark/light theme** -- Matches the existing HTML report's dark theme aesthetic, with a toggle for light mode.

---

## 2. Architecture

### 2.1 High-Level Diagram

```
Browser (SPA)                          Node.js Process
+--------------------------+           +---------------------------+
|  Svelte 5 App            |           |  micelio ui               |
|  - Crawl Setup           |  REST     |  +---------------------+  |
|  - Crawl Monitor    <------------>   |  | ui-api.ts           |  |
|  - Results Dashboard     |  (JSON)   |  | REST endpoints      |  |
|  - Crawl History         |           |  +---------------------+  |
|  - Settings              |           |                           |
|                          |  WebSocket|  +---------------------+  |
|  stores/websocket.ts <---------->    |  | ui-server.ts        |  |
|  (real-time progress)    |  (/ws)    |  | HTTP + WS server    |  |
+--------------------------+           |  | Static file serving  |  |
                                       |  +---------------------+  |
                                       |                           |
                                       |  +---------------------+  |
                                       |  | orchestrator.ts     |  |
                                       |  | (existing crawl)    |  |
                                       |  +---------------------+  |
                                       |                           |
                                       |  +---------------------+  |
                                       |  | ui-store.ts         |  |
                                       |  | SQLite (crawl jobs,  |  |
                                       |  |  presets, settings)  |  |
                                       |  +---------------------+  |
                                       +---------------------------+
```

### 2.2 Communication Protocols

| Channel | Purpose | Format |
|---------|---------|--------|
| `REST API` | CRUD operations (start/pause/cancel crawl, history, settings, presets) | JSON request/response |
| `WebSocket /ws/crawl/:id` | Real-time crawl progress (pages crawled, current URL, status codes, errors) | JSON messages |

**REST** is used for all operations that follow request-response semantics. **WebSocket** is used exclusively for streaming crawl progress events to avoid polling.

### 2.3 Build Pipeline

```
dashboard/                          dist/dashboard/
  src/ (Svelte source)    -------->   index.html
  vite.config.ts          (vite     assets/
  svelte.config.js         build)     app-[hash].js
  tailwind.config.ts                  app-[hash].css
  package.json
```

- **Development:** `npm run dev:ui` starts Vite dev server with HMR on port 5173, proxying API requests to the Node backend on port 3000.
- **Production:** `npm run build:ui` compiles static assets into `dist/dashboard/`. The Node server serves these files for any non-`/api/` and non-`/ws/` routes.
- The `npm run build` script (existing TypeScript compilation) is extended to also run `build:ui` via a workspace script.

### 2.4 Source File Layout (Backend)

| File | Responsibility |
|------|---------------|
| `src/ui-server.ts` | HTTP server creation, static file serving, WebSocket upgrade handling, graceful shutdown |
| `src/ui-api.ts` | REST route handlers (crawl CRUD, settings, presets, schedules, diff) |
| `src/ui-store.ts` | SQLite schema and queries for UI-specific data (crawl jobs, presets, app settings) |
| `src/ui-crawl-manager.ts` | Manages active crawls: wraps `crawl()` from orchestrator, tracks state, emits WebSocket events |

---

## 3. Pages

The UI is a single-page application with 5 pages, navigated via hash-based routing (`#/setup`, `#/monitor/:id`, `#/results/:id`, `#/history`, `#/settings`).

### 3.1 Crawl Setup Page (`#/setup`)

The primary entry point. A form that mirrors every CLI option from `src/cli.ts`, organized into collapsible sections for progressive disclosure.

#### 3.1.1 Form Sections

**Basic Configuration** (always visible)

| Field | Type | Maps to CLI | Default |
|-------|------|-------------|---------|
| URL | text input (validated) | `<url>` argument | -- |
| Mode | radio: Spider / List / Sitemap | `spider` / `list` / `sitemap` subcommand | Spider |
| URL List | textarea (one per line, shown when Mode=List) | `<file>` argument | -- |
| Sitemap URLs | textarea (one per line, shown when Mode=Sitemap) | `<url...>` arguments | -- |
| Max Depth | number input (0-10) | `--depth` | 3 |
| Max Pages | number input (1-100000) | `--limit` | 500 |
| Concurrency | slider (1-20) | `--concurrency` | 5 |
| Delay (ms) | number input | `--delay` | -- (auto) |
| Include Patterns | tag input (regex) | `--include` | -- |
| Exclude Patterns | tag input (regex) | `--exclude` | -- |

**Headers & Authentication** (collapsed by default)

| Field | Type | Maps to CLI |
|-------|------|-------------|
| Custom Headers | key-value editor (add/remove rows) | `--header` |
| Cookies | text input | `--cookie` |
| User Agent | text input with preset dropdown | `--user-agent` |
| Proxy | text input (URL format) | `--proxy` |

**JavaScript Rendering** (collapsed)

| Field | Type | Maps to CLI |
|-------|------|-------------|
| Enable JS Rendering | toggle | `--js` |
| Snippet Files | file path list (add/remove) | `--snippet` |

**Custom Extraction** (collapsed)

| Field | Type | Maps to CLI |
|-------|------|-------------|
| CSS Extractions | key-value editor (name:selector) | `--extract` |
| Text/Regex Searches | list editor with regex toggle | `--search` |

**AI Analysis** (collapsed)

| Field | Type | Maps to CLI |
|-------|------|-------------|
| AI Provider | select: OpenAI / Anthropic / Ollama | `--ai-provider` |
| AI Model | text input | `--ai-model` |
| AI API Key | password input (pre-filled from settings) | `--ai-key` |
| AI Prompt | textarea | `--ai-prompt` |

**Performance & Web Vitals** (collapsed)

| Field | Type | Maps to CLI |
|-------|------|-------------|
| PageSpeed Insights | toggle | `--psi` |
| PSI API Key | password input (pre-filled from settings) | `--psi-key` |
| CrUX (Real User Metrics) | toggle | `--crux` |
| CrUX API Key | password input (pre-filled from settings) | `--crux-key` |
| CrUX Form Factor | select: All / Phone / Desktop | `--crux-form-factor` |
| Page Weight Analysis | toggle | `--page-weight` |

**Google Integrations** (collapsed)

| Field | Type | Maps to CLI |
|-------|------|-------------|
| Google Search Console | toggle | `--gsc` |
| GSC Property | text input (auto-detect hint) | `--gsc-property` |
| GSC Days | number input | `--gsc-days` |
| GSC Key File | file picker | `--gsc-key-file` |
| GSC BigQuery Dataset | text input | `--gsc-bq` |
| Google Analytics 4 | toggle | `--ga4` |
| GA4 Property ID | text input | `--ga4-property` |
| GA4 Days | number input | `--ga4-days` |
| GA4 Key File | file picker | `--ga4-key-file` |

**Content Intelligence** (collapsed)

| Field | Type | Maps to CLI |
|-------|------|-------------|
| Embeddings | toggle | `--embeddings` |
| Embedding Model | text input | `--embedding-model` |
| Similarity Threshold | slider (0-1, step 0.05) | `--similarity-threshold` |
| N-Grams | toggle | `--ngrams` |
| Link Intelligence | toggle | `--link-intelligence` |
| Max Link Suggestions | number input | `--li-max-suggestions` |
| Skip Centrality | toggle | `--li-no-centrality` |

**Segmentation** (collapsed)

| Field | Type | Maps to CLI |
|-------|------|-------------|
| Segment Rules | key-value editor (name:pattern) | `--segment` |

**Output** (collapsed)

| Field | Type | Maps to CLI |
|-------|------|-------------|
| Output Format | radio: JSONL / CSV | `--csv` |
| Check External Links | toggle | `--check-external` |
| Generate XML Sitemap | toggle | `--sitemap-out` |

#### 3.1.2 Preset Configurations

Four built-in presets appear as cards above the form. Clicking a preset pre-fills the form:

| Preset | Description | Key Settings |
|--------|-------------|-------------|
| **Quick Scan** | Fast technical overview | depth=2, limit=100, concurrency=10, no integrations |
| **Full Audit** | Comprehensive analysis | depth=5, limit=5000, all features enabled except AI |
| **Content Audit** | Content-focused analysis | depth=3, limit=1000, ngrams, embeddings, readability |
| **Technical Audit** | Infrastructure focus | depth=5, limit=2000, page-weight, link-intelligence, check-external |

Users can also:
- **Save as Preset** -- Save current form state as a named preset (stored in SQLite)
- **Load Preset** -- Load a saved preset from a dropdown
- **Delete Preset** -- Remove a saved preset

#### 3.1.3 Form Actions

- **Start Crawl** button -- Validates form, sends `POST /api/crawl/start`, navigates to Monitor page (`#/monitor/:id`)
- **Export as CLI Command** button -- Generates the equivalent `micelio spider ...` CLI command and copies to clipboard
- Form state is auto-saved to `localStorage` so refreshing does not lose configuration

### 3.2 Crawl Monitor Page (`#/monitor/:id`)

Real-time dashboard showing crawl progress, powered by WebSocket.

#### 3.2.1 Progress Overview (top row)

| Widget | Data Source | Update Frequency |
|--------|-----------|-----------------|
| Progress bar with percentage | `completed / total` | Every page |
| Pages crawled counter | `completed` | Every page |
| Errors counter | `failed` | Every page |
| Pages/second | Computed from `completed` and elapsed time | Every 2 seconds |
| Estimated time remaining | `(total - completed) / pagesPerSecond` | Every 2 seconds |
| Elapsed time | Client-side timer from `startedAt` | Every second |

#### 3.2.2 Live Panels

**Current Activity**
- Displays the URL currently being crawled (last received from WebSocket)
- Shows the last 10 crawled URLs in a scrolling list with status code badges

**Status Code Distribution** (live donut chart)
- Updates as each page completes
- Color-coded: green (2xx), yellow (3xx), orange (4xx), red (5xx), gray (0/error)

**Response Time Timeline** (live line chart)
- X-axis: page index, Y-axis: response time in ms
- Rolling window of last 200 data points
- Horizontal threshold lines at 1s (yellow) and 3s (red)

**Error Log** (live list)
- Scrollable list of failed URLs with error messages
- Auto-scrolls to bottom on new entries
- Click to copy URL

#### 3.2.3 Control Buttons

| Button | API Call | Behavior |
|--------|---------|----------|
| **Pause** | `POST /api/crawl/:id/pause` | Suspends crawl workers. Button changes to "Resume". |
| **Resume** | `POST /api/crawl/:id/resume` | Resumes crawl workers. Button changes to "Pause". |
| **Cancel** | `POST /api/crawl/:id/cancel` | Stops crawl permanently. Navigates to Results with partial data. |

#### 3.2.4 Completion Behavior

When the crawl completes:
1. WebSocket sends a `crawl:complete` message with the crawl job ID
2. A "Crawl Complete" banner appears with summary stats
3. After 3 seconds, automatically navigates to `#/results/:id`
4. User can click "View Results" immediately to skip the delay

### 3.3 Results Dashboard Page (`#/results/:id`)

Displays all analysis results for a completed crawl. Migrates every visualization and data panel from the existing HTML report (`src/html-report.ts`) into Svelte components.

#### 3.3.1 Top Bar

- **Crawl metadata:** URL, date, duration, page count, mode
- **Segment/Filter selector:** Dropdown to filter all panels by segment (when segments were defined)
- **Export buttons:** CSV (data table), JSON (full results), PDF (future phase)
- **Share button:** Generates a shareable report URL (copies to clipboard)

#### 3.3.2 Summary Cards Row

Eight summary cards matching the existing HTML report:

| Card | Value | Condition Color |
|------|-------|----------------|
| Total Pages | `totalPages` | -- |
| Indexable | `indexabilityStats.indexable` / `nonIndexable` | Green if >80% indexable |
| Status 2xx | `statusCodes[200]` + others | Green |
| Client Errors (4xx) | Sum of 4xx codes | Red if >0 |
| Server Errors (5xx) | Sum of 5xx codes | Red if >0 |
| Avg Response Time | `responseTimePercentiles.p50` | Green <1s, Yellow <2s, Red >2s |
| Broken Links | `brokenLinks.length` | Red if >0 |
| Images Missing Alt | `imagesMissingAlt` | Yellow if >0 |

Additional cards shown conditionally:
- GSC: Total Impressions, Total Clicks, Avg Position (when GSC data present)
- GA4: Total Sessions, Total Pageviews (when GA4 data present)
- CrUX: Avg LCP, Avg INP, Avg CLS (when CrUX data present)

#### 3.3.3 Charts Panel (tab-based)

| Chart | Type | Source |
|-------|------|--------|
| Status Code Distribution | Donut chart (D3) | `statusCodes` |
| Crawl Depth Distribution | Bar chart | `depthDistribution` |
| Response Time by Depth | Bar chart (color-coded) | Computed from pages |
| PageRank by Depth | Bar chart (color-coded) | Computed from pages |
| Template Type Distribution | Horizontal bar chart | `templateTypeDistribution` |
| Page Weight by Type | Stacked bar chart | `pageWeightStats.byType` (when present) |

#### 3.3.4 Issues Panel

Expandable groups matching the existing HTML report issue categories:

| Group | Items |
|-------|-------|
| Missing Titles | Pages without `<title>` tag |
| Missing Meta Descriptions | Pages without meta description |
| Missing H1 | Pages without H1 heading |
| Duplicate Titles | Groups of pages sharing the same title |
| Duplicate Content | Exact duplicate groups (by content hash) |
| Near Duplicates | SimHash near-duplicate groups |
| Thin Content | Pages with word count < 200 |
| Broken Internal Links | 4xx/5xx internal links with source pages |
| Broken External Links | 4xx/5xx external links (when `--check-external` used) |
| Redirect Chains | Long chains (3+ hops) with visualization |
| Redirect Chain Visualization | Visual diagram of redirect flows |
| Canonical Issues | Chains, loops, cross-domain, HTTP/HTTPS mismatch, missing |
| Orphan Pages | Pages with zero internal inlinks |
| Soft 404s | 200-status pages detected as error pages |
| Mixed Content | HTTPS pages loading HTTP resources |
| URL Issues | Uppercase, spaces, tracking params, etc. |
| Dead-End Pages | Pages with zero internal outlinks |
| Nofollow Analysis | Internal links with `rel="nofollow"` |
| Schema Validation Errors | Structured data validation issues |
| Hreflang Issues | Missing return links, missing x-default |
| Security Issues | No HSTS, no CSP, no X-Frame-Options |
| Image Accessibility | Missing alt attributes, oversized images, missing dimensions |
| Zombie Pages (GSC) | Indexed pages with 0 clicks |
| No-Traffic Pages (GA4) | Indexable pages with 0 sessions |
| CrUX Issues | Poor LCP/INP/CLS pages |
| Cannibalization Groups | Semantically similar pages (when embeddings used) |
| Link Intelligence | Near-orphans, dilution warnings, unreachable pages, link suggestions |

Each group shows: badge count, expandable list with URL links, detail on click.

#### 3.3.5 Data Table

Full-featured data table component:

| Feature | Description |
|---------|-------------|
| Columns | URL, Status, Title, Description, H1, Canonical, Depth, Word Count, Response Time, PageRank, Indexable, Template Type (+ optional: GSC, GA4, CrUX, segments, custom extractions) |
| Sorting | Click column header to sort asc/desc |
| Filtering | Per-column text filter, global search, status code dropdown |
| Pagination | 50 rows per page with page navigation |
| Row Expansion | Click row to expand full detail panel (all page data) |
| Column Visibility | Toggle columns via dropdown menu |
| Selection | Checkbox selection for bulk export |
| Virtual Scrolling | Only render visible rows for performance with 10K+ pages |

#### 3.3.6 Site Graph (D3 Force-Directed)

Migrated from the existing HTML report:
- Nodes colored by crawl depth, sized by PageRank
- Interactive: zoom, pan, drag nodes
- Hover tooltip: URL path, status code, PageRank, depth, title
- Performance: 2,000 node cap with sampling for larger sites
- Depth legend overlay
- Toggle: show/hide external link edges

#### 3.3.7 Directory Tree

Migrated from the existing HTML report:
- Hierarchical collapsible tree built from URL path segments
- Page count per directory (pre-computed bottom-up)
- Status code color coding (green/yellow/red per directory)
- Click directory to filter data table to that path prefix

#### 3.3.8 Redirect Chain Visualization

Migrated from the existing HTML report:
- Visual flowchart of redirect chains
- Color-coded by redirect type (301/302/307)
- Classification labels: HTTP-to-HTTPS, www normalization, trailing slash, cross-domain

#### 3.3.9 N-Grams Panel

Three-column grid (when n-grams data present):
- Unigrams, Bigrams, Trigrams
- Sortable by count, pages, TF-IDF
- Color-coded frequency bars
- Search filter within each column

#### 3.3.10 Additional Result Panels (conditional)

| Panel | Shown When |
|-------|-----------|
| Embedding Similarity | `embeddingStats` present |
| Link Intelligence Graph | `linkIntelligenceStats` present |
| CrUX Web Vitals | `cruxStats` present |
| Segment Comparison | `segmentStats` present |
| Render Comparison | `renderCompareStats` present |

### 3.4 Crawl History Page (`#/history`)

Lists all past crawls stored in the UI database.

#### 3.4.1 History Table

| Column | Description |
|--------|-------------|
| Status | Badge: completed (green), running (blue), failed (red), cancelled (gray) |
| URL | Seed URL or "List mode (N URLs)" |
| Mode | Spider / List / Sitemap |
| Pages | Total pages crawled |
| Duration | Human-readable (e.g., "2m 34s") |
| Started | Date/time with relative timestamp (e.g., "2 hours ago") |
| Actions | View Results, Compare, Delete |

Sortable by any column. Filterable by status and date range.

#### 3.4.2 Crawl Comparison

- Select two crawls via checkboxes
- "Compare" button becomes active when exactly 2 are selected
- Sends `POST /api/crawl/diff` with both crawl IDs
- Navigates to a diff results view that reuses the existing `diffCrawls()` logic from `src/diff.ts`
- Diff view shows: added URLs, removed URLs, changed URLs with field-level detail (matching the existing HTML diff report)

#### 3.4.3 Bulk Actions

- **Delete Selected** -- Delete multiple crawls and their associated data
- **Export Selected** -- Download JSONL files for selected crawls

### 3.5 Settings Page (`#/settings`)

Persistent application settings stored in SQLite.

#### 3.5.1 API Keys Management

| Key | Used By |
|-----|---------|
| PSI API Key | PageSpeed Insights integration |
| CrUX API Key | Chrome UX Report integration |
| OpenAI API Key | AI analysis + embeddings |
| Anthropic API Key | AI analysis |
| GSC Service Account Key File Path | Google Search Console |
| GA4 Service Account Key File Path | Google Analytics 4 |

- All keys stored encrypted in SQLite (AES-256 with a machine-derived key)
- Keys are auto-filled in the Crawl Setup form when present
- "Test Connection" button for each key to validate it works

#### 3.5.2 Default Crawl Configuration

- Set default values for common crawl options (concurrency, depth, limit, delay, user-agent)
- These defaults pre-fill the Crawl Setup form for new crawls
- Override the `DEFAULT_CONFIG` from `src/config.ts`

#### 3.5.3 Scheduled Crawls Management

- Lists all schedules from `~/.micelio/schedules/` (reuses `listSchedules()` from `src/scheduler.ts`)
- Shows: URL, cron expression (human-readable), last run status, next run, total runs
- Actions: View schedule details, Edit (future), Delete

#### 3.5.4 Appearance

- Theme toggle: Dark / Light / System
- Theme persisted in `localStorage` and SQLite

#### 3.5.5 Data Management

- **Clear All History** -- Delete all crawl jobs and results
- **Database Size** -- Shows current SQLite database file size
- **Export All Settings** -- Download settings as JSON
- **Import Settings** -- Upload a settings JSON file

---

## 4. API Endpoints

### 4.1 Crawl Operations

| Method | Path | Description | Request Body | Response |
|--------|------|-------------|-------------|----------|
| `POST` | `/api/crawl/start` | Start a new crawl | `CrawlConfig` (partial, merged with defaults) | `{ id: string, status: "running" }` |
| `GET` | `/api/crawl/:id/status` | Get crawl status | -- | `CrawlJob` |
| `POST` | `/api/crawl/:id/pause` | Pause a running crawl | -- | `{ status: "paused" }` |
| `POST` | `/api/crawl/:id/resume` | Resume a paused crawl | -- | `{ status: "running" }` |
| `POST` | `/api/crawl/:id/cancel` | Cancel a running crawl | -- | `{ status: "cancelled" }` |
| `GET` | `/api/crawl/:id/results` | Get full crawl results | -- | `{ pages: LightPageData[], stats: CrawlStats }` |
| `GET` | `/api/crawl/:id/results/pages` | Get paginated page data | `?page=1&limit=50&sort=url&order=asc&filter=...` | `{ pages: LightPageData[], total: number }` |
| `DELETE` | `/api/crawl/:id` | Delete a crawl and its data | -- | `{ deleted: true }` |

### 4.2 Crawl History & Comparison

| Method | Path | Description | Request Body | Response |
|--------|------|-------------|-------------|----------|
| `GET` | `/api/crawls` | List all past crawls | `?status=completed&limit=50&offset=0` | `{ crawls: CrawlJob[], total: number }` |
| `POST` | `/api/crawl/diff` | Compare two crawls | `{ oldId: string, newId: string, fields?: string[] }` | `DiffResult` |

### 4.3 Presets

| Method | Path | Description | Request Body | Response |
|--------|------|-------------|-------------|----------|
| `GET` | `/api/presets` | List all saved presets | -- | `SavedPreset[]` |
| `POST` | `/api/presets` | Save a new preset | `{ name: string, config: Partial<CrawlConfig> }` | `SavedPreset` |
| `PUT` | `/api/presets/:id` | Update a preset | `{ name?: string, config?: Partial<CrawlConfig> }` | `SavedPreset` |
| `DELETE` | `/api/presets/:id` | Delete a preset | -- | `{ deleted: true }` |

### 4.4 Settings

| Method | Path | Description | Request Body | Response |
|--------|------|-------------|-------------|----------|
| `GET` | `/api/settings` | Get application settings | -- | `AppSettings` |
| `PUT` | `/api/settings` | Update application settings | `Partial<AppSettings>` | `AppSettings` |

### 4.5 Schedules

| Method | Path | Description | Request Body | Response |
|--------|------|-------------|-------------|----------|
| `GET` | `/api/schedules` | List all schedules | -- | `ScheduleInfo[]` |
| `POST` | `/api/schedules` | Create a new schedule | `ScheduleConfig` | `ScheduleInfo` |
| `DELETE` | `/api/schedules/:id` | Delete a schedule | -- | `{ deleted: true }` |

### 4.6 WebSocket

| Endpoint | Direction | Message Types |
|----------|-----------|--------------|
| `ws://localhost:<port>/ws/crawl/:id` | Server -> Client | `crawl:progress`, `crawl:page`, `crawl:error`, `crawl:complete`, `crawl:paused`, `crawl:resumed`, `crawl:cancelled` |

#### WebSocket Message Formats

```typescript
// Server -> Client: Progress update (every page)
interface WsProgressMessage {
  type: 'crawl:progress';
  data: {
    completed: number;
    failed: number;
    pending: number;
    total: number;
    pagesPerSecond: number;
    elapsedMs: number;
    estimatedRemainingMs: number;
  };
}

// Server -> Client: Page crawled (every page)
interface WsPageMessage {
  type: 'crawl:page';
  data: {
    url: string;
    statusCode: number;
    responseTimeMs: number;
    error?: string;
  };
}

// Server -> Client: Error occurred
interface WsErrorMessage {
  type: 'crawl:error';
  data: {
    url: string;
    error: string;
  };
}

// Server -> Client: Crawl completed
interface WsCompleteMessage {
  type: 'crawl:complete';
  data: {
    crawlId: string;
    totalPages: number;
    totalErrors: number;
    durationMs: number;
  };
}

// Server -> Client: Status change (paused/resumed/cancelled)
interface WsStatusMessage {
  type: 'crawl:paused' | 'crawl:resumed' | 'crawl:cancelled';
  data: { crawlId: string };
}
```

### 4.7 Error Response Format

All API endpoints return errors in a consistent format:

```json
{
  "error": {
    "code": "CRAWL_NOT_FOUND",
    "message": "No crawl found with ID abc123"
  }
}
```

HTTP status codes: `400` (validation), `404` (not found), `409` (conflict, e.g., crawl already running), `500` (internal error).

---

## 5. Data Models

### 5.1 CrawlJob

Stored in the `crawl_jobs` table. Represents a single crawl execution.

```typescript
interface CrawlJob {
  id: string;                    // UUID v4
  config: Partial<CrawlConfig>;  // Full configuration used for this crawl
  status: 'pending' | 'running' | 'paused' | 'completed' | 'failed' | 'cancelled';
  startedAt: string;             // ISO 8601
  completedAt: string | null;    // ISO 8601
  pageCount: number;             // Total pages crawled
  errorCount: number;            // Total errors
  durationMs: number | null;     // Total duration
  seedUrl: string;               // Primary URL (for display)
  mode: 'spider' | 'list' | 'sitemap';
  dbPath: string;                // Path to per-crawl SQLite database (stores page data)
}
```

**Storage strategy:** Each crawl stores its page data in a separate SQLite database file under `~/.micelio/ui/crawls/<id>.db`, reusing the existing `CrawlStore` class from `src/db-store.ts`. The `crawl_jobs` table in the main UI database only stores metadata.

### 5.2 SavedPreset

Stored in the `presets` table.

```typescript
interface SavedPreset {
  id: string;                        // UUID v4
  name: string;                      // User-defined name
  config: Partial<CrawlConfig>;      // Crawl configuration (only non-default values)
  builtIn: boolean;                   // true for the 4 built-in presets
  createdAt: string;                  // ISO 8601
  updatedAt: string;                  // ISO 8601
}
```

### 5.3 AppSettings

Stored in the `settings` table (key-value pairs).

```typescript
interface AppSettings {
  // API Keys (stored encrypted)
  psiKey: string;
  cruxKey: string;
  openaiKey: string;
  anthropicKey: string;
  gscKeyFilePath: string;
  ga4KeyFilePath: string;

  // Default crawl config overrides
  defaults: {
    concurrency: number;
    maxDepth: number;
    maxPages: number;
    delayMs: number;
    userAgent: string;
    outputFormat: 'jsonl' | 'csv';
  };

  // Appearance
  theme: 'dark' | 'light' | 'system';
}
```

### 5.4 SQLite Schema (UI Database)

Located at `~/.micelio/ui/micelio-ui.db`:

```sql
CREATE TABLE IF NOT EXISTS crawl_jobs (
  id TEXT PRIMARY KEY,
  config TEXT NOT NULL,          -- JSON
  status TEXT NOT NULL DEFAULT 'pending',
  started_at TEXT NOT NULL,
  completed_at TEXT,
  page_count INTEGER NOT NULL DEFAULT 0,
  error_count INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER,
  seed_url TEXT NOT NULL,
  mode TEXT NOT NULL DEFAULT 'spider',
  db_path TEXT NOT NULL
);
CREATE INDEX idx_crawl_jobs_status ON crawl_jobs(status);
CREATE INDEX idx_crawl_jobs_started ON crawl_jobs(started_at);

CREATE TABLE IF NOT EXISTS presets (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  config TEXT NOT NULL,          -- JSON
  built_in INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL            -- JSON or encrypted string
);
```

---

## 6. File Structure

### 6.1 Dashboard (Frontend)

```
dashboard/
  package.json                     # Svelte/Vite/Tailwind dependencies
  vite.config.ts                   # Vite config with Svelte plugin, proxy in dev
  svelte.config.js                 # Svelte 5 runes mode
  tailwind.config.ts               # Tailwind 4 config with custom theme
  tsconfig.json                    # TypeScript config for frontend
  postcss.config.js                # PostCSS with Tailwind plugin
  src/
    app.html                       # HTML shell (single <div id="app">)
    app.css                        # Tailwind base imports + custom CSS variables
    main.ts                        # Svelte mount + router initialization
    lib/
      components/
        layout/
          Header.svelte            # Top navigation bar + breadcrumbs
          Sidebar.svelte           # Navigation sidebar (collapsible)
          Layout.svelte            # Main layout wrapper
        ui/                        # shadcn-svelte primitives
          Button.svelte
          Card.svelte
          Input.svelte
          Select.svelte
          Toggle.svelte
          Slider.svelte
          Badge.svelte
          Dialog.svelte
          Tabs.svelte
          Tooltip.svelte
          DataTable.svelte         # Generic sortable/filterable table
          TagInput.svelte          # Multi-value input for patterns
          KeyValueEditor.svelte    # Name:Value pair editor
          CollapsibleSection.svelte
        charts/
          DonutChart.svelte        # Status code distribution
          BarChart.svelte          # Depth, response time, PageRank
          LineChart.svelte         # Real-time response time timeline
          StackedBarChart.svelte   # Page weight by type
          ForceGraph.svelte        # D3 force-directed site graph
        crawl/
          CrawlForm.svelte         # Full crawl configuration form
          PresetCard.svelte        # Preset selection card
          PresetManager.svelte     # Save/load/delete presets
          CrawlProgress.svelte     # Real-time progress overview
          CrawlControls.svelte     # Pause/Resume/Cancel buttons
          LiveStatusChart.svelte   # Live updating status code donut
          LiveErrorLog.svelte      # Scrolling error list
          UrlActivityFeed.svelte   # Current URL being crawled
        results/
          SummaryCards.svelte      # Summary stat cards row
          IssuePanel.svelte        # Expandable issue group
          IssueList.svelte         # List of issues within a group
          PageDetailPanel.svelte   # Expanded row detail
          DirectoryTree.svelte     # Collapsible directory tree
          RedirectChainViz.svelte  # Redirect chain flowchart
          NgramsPanel.svelte       # N-grams three-column grid
          SegmentSelector.svelte   # Segment filter dropdown
          ExportButtons.svelte     # CSV/JSON/PDF export
        history/
          HistoryTable.svelte      # Crawl history list
          DiffView.svelte          # Crawl comparison view
        settings/
          ApiKeysForm.svelte       # API key management
          DefaultsForm.svelte      # Default crawl config
          SchedulesList.svelte     # Scheduled crawls manager
          ThemeToggle.svelte       # Dark/Light/System toggle
          DataManagement.svelte    # Clear history, export/import
      stores/
        crawl.ts                   # Active crawl state (Svelte store)
        websocket.ts               # WebSocket connection manager
        settings.ts                # App settings store
        theme.ts                   # Theme store (localStorage + system)
        presets.ts                  # Presets store
        history.ts                 # Crawl history store
      api.ts                       # Fetch wrapper for REST API calls
      router.ts                    # Hash-based SPA router (~50 LOC)
      utils.ts                     # Frontend utility functions
      types.ts                     # Frontend-specific TypeScript types
    routes/
      Setup.svelte                 # #/setup
      Monitor.svelte               # #/monitor/:id
      Results.svelte               # #/results/:id
      History.svelte               # #/history
      Settings.svelte              # #/settings
```

### 6.2 Backend (New Files)

```
src/
  ui-server.ts                     # HTTP server + WebSocket + static file serving
  ui-api.ts                        # REST route handlers
  ui-store.ts                      # SQLite schema and queries for UI data
  ui-crawl-manager.ts              # Active crawl lifecycle (start/pause/cancel/progress)
```

### 6.3 Build Output

```
dist/
  dashboard/                       # Vite build output (served by ui-server.ts)
    index.html
    assets/
      app-[hash].js
      app-[hash].css
```

---

## 7. Implementation Phases

### Phase 1: Backend Server + Scaffold + Crawl Setup (Weeks 1-2)

**Goal:** Serve a working Svelte app that can configure and start a crawl.

**Backend tasks:**
1. Create `src/ui-server.ts` -- HTTP server with static file serving and API routing
2. Create `src/ui-api.ts` -- Implement `POST /api/crawl/start`, `GET /api/crawls`, `GET /api/presets`, `POST /api/presets`
3. Create `src/ui-store.ts` -- SQLite schema, CRUD for crawl jobs, presets, settings
4. Create `src/ui-crawl-manager.ts` -- Wrap `crawl()` from orchestrator, manage active crawl state
5. Add `ui` subcommand to `src/cli.ts` with `--port` option

**Frontend tasks:**
1. Initialize `dashboard/` with Svelte 5, Vite, Tailwind CSS 4, shadcn-svelte
2. Build Layout (Header + Sidebar), hash router, theme system
3. Build Crawl Setup page with full form (all CLI options)
4. Build preset cards (4 built-in) and save/load preset functionality
5. Wire form submission to `POST /api/crawl/start`

**Scripts to add to root `package.json`:**
```json
{
  "scripts": {
    "build:ui": "cd dashboard && npm run build",
    "dev:ui": "cd dashboard && npm run dev",
    "ui": "tsx src/cli.ts ui"
  }
}
```

**Deliverable:** `micelio ui` opens browser, shows Setup page, user can configure and start a crawl (no live monitoring yet -- redirect to terminal output).

### Phase 2: Crawl Monitor with WebSocket (Week 3)

**Goal:** Real-time crawl progress dashboard.

**Backend tasks:**
1. Add `ws` dependency and WebSocket upgrade handling in `ui-server.ts`
2. Implement `onProgress` callback integration in `ui-crawl-manager.ts` -- forward progress events to all connected WebSocket clients for the active crawl
3. Implement `POST /api/crawl/:id/pause`, `/resume`, `/cancel`
4. Implement pause/resume logic in crawl manager (pause = stop dequeuing, resume = restart dequeuing)

**Frontend tasks:**
1. Build WebSocket store (`stores/websocket.ts`) with auto-reconnect
2. Build Monitor page components: progress bar, counters, live charts
3. Build control buttons (Pause/Resume/Cancel)
4. Auto-navigation from Setup to Monitor on crawl start
5. Auto-navigation from Monitor to Results on crawl complete

**Deliverable:** Start a crawl from the UI, watch real-time progress with live charts, pause/resume/cancel.

### Phase 3: Results Dashboard (Weeks 4-5)

**Goal:** Full results visualization, migrating all HTML report features to Svelte components.

**Backend tasks:**
1. Implement `GET /api/crawl/:id/results` -- load pages from per-crawl SQLite DB, run `generateReport()`, return `CrawlStats` + `LightPageData[]`
2. Implement `GET /api/crawl/:id/results/pages` -- paginated page data with sort/filter

**Frontend tasks:**
1. Build Summary Cards component
2. Build Charts panel (donut, bar, line charts using D3)
3. Build Issues panel with expandable groups
4. Build Data Table with virtual scrolling, sort, filter, column toggle, row expansion
5. Build Force Graph (D3 force-directed, migrated from HTML report)
6. Build Directory Tree (collapsible, migrated from HTML report)
7. Build Redirect Chain Visualization
8. Build N-Grams panel
9. Build conditional panels (CrUX, Segments, Link Intelligence, Embeddings)
10. Implement CSV/JSON export

**Deliverable:** Complete results dashboard matching (and exceeding) the existing static HTML report.

### Phase 4: History + Diff Comparison (Week 6)

**Goal:** Crawl history management and cross-crawl comparison.

**Backend tasks:**
1. Implement `GET /api/crawls` with pagination, sorting, filtering
2. Implement `DELETE /api/crawl/:id` (delete crawl job + per-crawl DB file)
3. Implement `POST /api/crawl/diff` -- load pages from two crawl DBs, run `diffCrawls()`, return `DiffResult`

**Frontend tasks:**
1. Build History Table with sorting, filtering, status badges
2. Build crawl comparison selector (checkbox-based)
3. Build Diff View page (reuse existing diff report layout as Svelte components)
4. Build bulk actions (delete, export)

**Deliverable:** Browse past crawls, view any historical result, compare two crawls side-by-side.

### Phase 5: Settings + Polish (Week 7)

**Goal:** Settings persistence, scheduled crawls management, and polish.

**Backend tasks:**
1. Implement `GET/PUT /api/settings`
2. Implement `GET /api/schedules`, `DELETE /api/schedules/:id`
3. API key encryption at rest

**Frontend tasks:**
1. Build Settings page (API keys, defaults, schedules, theme, data management)
2. Build "Export as CLI Command" button on Setup page
3. Keyboard shortcuts (Ctrl+Enter to start crawl, Escape to cancel dialog)
4. Loading states, error toasts, empty states for all pages
5. Responsive design (tablet-friendly, graceful on mobile)
6. Accessibility audit (keyboard navigation, ARIA labels, focus management)

**Deliverable:** Full-featured web UI ready for release.

---

## 8. Dependencies to Add

### 8.1 Backend (root `package.json`)

| Package | Version | Purpose |
|---------|---------|---------|
| `ws` | `^8.18.0` | WebSocket server for real-time crawl progress |

Note: `better-sqlite3` and `node:http` are already available. No other backend dependencies are needed.

### 8.2 Frontend (`dashboard/package.json`)

| Package | Version | Purpose |
|---------|---------|---------|
| `svelte` | `^5.0.0` | Frontend framework (runes mode) |
| `@sveltejs/vite-plugin-svelte` | `^4.0.0` | Vite integration for Svelte 5 |
| `vite` | `^6.0.0` | Build tool and dev server |
| `tailwindcss` | `^4.0.0` | Utility-first CSS framework |
| `postcss` | `^8.5.0` | CSS processing pipeline |
| `autoprefixer` | `^10.4.0` | CSS vendor prefix automation |
| `bits-ui` | `^1.0.0` | Headless UI primitives (shadcn-svelte foundation) |
| `tailwind-variants` | `^0.3.0` | Variant-based styling API (shadcn-svelte) |
| `clsx` | `^2.1.0` | Conditional className utility |
| `tailwind-merge` | `^2.6.0` | Tailwind class deduplication |
| `d3` | `^7.9.0` | Charts and force-directed graph |
| `lucide-svelte` | `^0.460.0` | Icon library (used by shadcn-svelte) |
| `mode-watcher` | `^0.4.0` | Dark/light theme management for Svelte |
| `typescript` | `^5.7.0` | TypeScript (matches root project) |

### 8.3 Frontend Dev Dependencies

| Package | Version | Purpose |
|---------|---------|---------|
| `@sveltejs/package` | `^2.3.0` | Svelte package tooling |
| `svelte-check` | `^4.0.0` | Svelte type checking |
| `tslib` | `^2.8.0` | TypeScript runtime helpers |

---

## 9. Open Questions and Future Considerations

### 9.1 Decided

| Question | Decision |
|----------|----------|
| Monorepo or separate? | Monorepo. `dashboard/` is a subdirectory with its own `package.json`, built via workspace script. |
| Server framework? | No framework. Plain `node:http` with a lightweight custom router in `ui-server.ts`. Keeps zero new runtime deps in core. |
| Auth for UI? | None in v1. The UI binds to `localhost` only. Future: optional basic auth or token. |
| Concurrent crawls? | v1 supports one active crawl at a time. Queue additional requests with a "pending" status. |
| Data retention? | No automatic cleanup. Users manage via Settings > Data Management. |

### 9.2 Future Enhancements (Post-v1)

- **PDF Export** -- Generate downloadable PDF reports (Phase 10.4 in backlog)
- **Multi-crawl** -- Support multiple concurrent crawls with per-crawl resource limits
- **Remote access** -- Optional `--host 0.0.0.0` flag to expose UI on LAN
- **Authentication** -- Basic auth or API key for remote access scenarios
- **Webhook integration in UI** -- Configure webhook notifications from the Settings page
- **Crawl templates** -- Export/import complete crawl configurations as shareable JSON files
- **Scheduled crawl creation from UI** -- Full schedule creation (not just viewing) with cron expression builder
- **Plugin management** -- Install/configure plugins from Settings (when plugin system is implemented)

---

## 10. CLI Integration

### 10.1 New Subcommand

Add to `src/cli.ts`:

```typescript
program
  .command('ui')
  .description('Launch the web-based dashboard interface')
  .option('-p, --port <number>', 'HTTP server port', '3000')
  .action(async (opts: { port: string }) => {
    const port = parseInt(opts.port, 10);
    if (isNaN(port) || port < 1 || port > 65535) {
      console.error('Error: --port must be a number between 1 and 65535');
      process.exit(1);
    }
    const { startUiServer } = await import('./ui-server.js');
    await startUiServer(port);
  });
```

### 10.2 Behavior

1. Start HTTP + WebSocket server on the specified port
2. Initialize SQLite UI database at `~/.micelio/ui/micelio-ui.db`
3. Create `~/.micelio/ui/crawls/` directory for per-crawl databases
4. Seed built-in presets on first run
5. Print: `Micelio UI running at http://localhost:3000`
6. Open URL in default browser (same logic as HTML report auto-open)
7. Handle SIGINT/SIGTERM: close WebSocket connections, close SQLite, exit cleanly

---

## 11. Testing Strategy

### 11.1 Backend Tests

| Area | Type | Scope |
|------|------|-------|
| `ui-api.ts` routes | Integration | HTTP requests with supertest-like runner |
| `ui-store.ts` queries | Unit | SQLite CRUD operations |
| `ui-crawl-manager.ts` | Unit | Crawl lifecycle state machine |
| WebSocket messages | Integration | Connect, receive progress, disconnect |

### 11.2 Frontend Tests

| Area | Type | Tool |
|------|------|------|
| Component rendering | Unit | Svelte testing library + Vitest |
| Form validation | Unit | Vitest |
| Router navigation | Integration | Vitest + jsdom |
| Full user flows | E2E | Playwright |

### 11.3 E2E Scenarios

1. Start UI, configure a crawl (Quick Scan preset), start, monitor progress, view results
2. Save a custom preset, reload page, verify preset persists
3. Run two crawls, compare them in History page
4. Update API keys in Settings, verify they pre-fill in Setup form
5. Start a crawl, cancel mid-way, verify partial results are available
