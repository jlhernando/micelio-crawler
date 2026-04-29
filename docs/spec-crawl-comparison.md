# Crawl Comparison Report — Feature Spec

> **Status**: Draft
> **Date**: 2026-03-11
> **Scope**: Enhanced crawl-over-crawl comparison with deep insights

---

## 1. Problem Statement

The current `micelio diff` compares two crawls at a basic level: added/removed/changed URLs with field-level diffs for ~10 fields (title, description, h1, status code, word count, canonical, indexable, link counts, depth). It also flags disappeared URLs that had traffic.

This is useful but shallow. Enterprise SEO teams need **actionable, insight-rich comparison reports** that answer questions like:

- Is the site getting healthier or sicker over time?
- Did a deployment break things? What exactly broke and how severe is it?
- Are content improvements actually improving quality metrics?
- Is the internal linking strategy working? How did link equity shift?
- Are we gaining or losing search visibility?
- Which page segments improved vs degraded?

Competitors like Botify charge $50K+/yr largely because they surface these cross-crawl insights. This spec designs a comparison report that leverages all 100+ per-page fields and 150+ aggregate stats already captured by micelio.

---

## 2. Design Principles

1. **Diff everything, surface what matters** — Compare all available data points but present only meaningful changes, not noise.
2. **Segment-aware** — All comparisons should be available at the site level AND per URL segment (e.g., `/blog/` vs `/products/`).
3. **Directional framing** — Always frame changes as improvements or regressions, not just deltas. "5 more pages without title" is a regression; "12 fewer broken links" is an improvement.
4. **Severity scoring** — Assign severity (critical / warning / info) to changes so users focus on what matters.
5. **Actionable** — Every insight should suggest or imply a concrete action.

---

## 3. Comparison Architecture

### 3.1 Input

Two `CrawlResults` (pages + stats) for the **same domain**, identified by crawl IDs in the dashboard or JSONL file paths on CLI.

### 3.2 URL Matching

Pages are matched by **normalized URL** (scheme + host + path + sorted query params, trailing slash normalized). This is the existing behavior.

Three URL populations:
- **Persisted** — URLs present in both crawls (matched by normalized URL)
- **New** — URLs in the new crawl but not the old
- **Disappeared** — URLs in the old crawl but not the new

### 3.3 Output

A `ComparisonReport` struct containing:
- Site-level health score delta
- Per-section insight groups (see Section 4)
- Per-segment breakdowns
- Sorted by severity

---

## 4. Insight Sections

Each section compares a domain of data between old and new crawls. Every section produces:
- A **headline metric** (the most important number)
- A **direction** (improved / regressed / stable)
- **Detail rows** with old value, new value, delta, and severity

### 4.1 Site Health Score

**Headline**: Composite score (0-100) summarizing overall SEO health.

**Scoring formula** (weighted components):

| Component | Weight | Source | Score Logic |
|-----------|--------|--------|-------------|
| Indexability rate | 15% | `IndexabilityStats` | % of crawled pages that are indexable |
| HTTP health | 15% | `StatusCodes` | % of 2xx responses (penalize 4xx/5xx) |
| Title coverage | 10% | `PagesWithoutTitle` | % of pages with title |
| Description coverage | 8% | `PagesWithoutDescription` | % with meta description |
| H1 coverage | 7% | `PagesWithoutH1` | % with H1 |
| Duplicate content | 10% | `DuplicateContentGroups` + `NearDuplicateGroups` | % unique (non-duplicate) |
| Internal linking | 10% | `LinkAnalysis` | Avg inlinks per page, orphan % |
| Response time | 10% | `ResponseTimePercentiles` | p90 < 1s = 100, > 3s = 0 |
| Security | 5% | `MixedContentPages`, `NonHTTPSPages` | % HTTPS + no mixed content |
| Structured data | 5% | `PagesWithStructuredData` | % with valid schema |
| Image accessibility | 5% | `ImagesMissingAlt` | % images with alt text |

**Comparison**: Show old score vs new score with delta. Color: green if improved, red if regressed.

### 4.2 Crawl Coverage

**Headline**: Total pages crawled (old → new).

| Metric | Source |
|--------|--------|
| Total pages | `TotalPages` |
| New URLs count | Computed (in new, not in old) |
| Disappeared URLs count | Computed (in old, not in new) |
| Persisted URLs count | Computed (in both) |
| Robots-blocked pages | `RobotsBlockedCount` |
| Depth distribution shift | `DepthDistribution` |

**Insights**:
- If >5% of pages disappeared → **warning**: "X pages disappeared since last crawl"
- If new URLs > 20% of old total → **info**: "Significant content expansion detected"
- If robots-blocked increased → **warning**: "Y more pages blocked by robots.txt"

### 4.3 SEO Funnel

**Headline**: Conversion through the SEO funnel (crawled → indexable → visible → active).

Uses `SeoFunnelStats` from both crawls.

| Stage | Metric | Source |
|-------|--------|--------|
| Crawled | Total pages | `TotalPages` |
| Indexable | Pages passing all index checks | `IndexabilityStats.Indexable` |
| Visible | Pages with ≥1 GSC impression | `GscStats` (pages with impressions > 0) |
| Active | Pages with ≥1 click/visit | `GscStats` / `Ga4Stats` / `PlausibleStats` |

**Comparison**: Side-by-side funnel with conversion rates at each stage. Highlight stages where drop-off worsened.

**Insights**:
- Indexable rate dropped → **critical**: "Indexability dropped from X% to Y% — Z pages became non-indexable"
- Visible-to-active ratio improved → **info**: "CTR improvement: more visible pages are driving clicks"

### 4.4 HTTP Status & Technical Health

**Headline**: 2xx rate (old → new).

| Metric | Source |
|--------|--------|
| Status code distribution | `StatusCodes` map |
| New 4xx pages | Pages that were 2xx, now 4xx |
| New 5xx pages | Pages that were 2xx, now 5xx |
| Fixed errors | Pages that were 4xx/5xx, now 2xx |
| Redirect changes | Pages that gained/lost redirects |
| New broken links | `BrokenLinks` delta |
| Soft 404 changes | `Soft404Pages` delta |

**Status Migration Matrix**: A table showing how URLs moved between status code groups:

```
          New: 2xx  3xx  4xx  5xx
Old: 2xx   950    10    5    2
     3xx    3     45    1    0
     4xx    8      0   12    0
     5xx    2      0    0    3
```

**Insights**:
- Pages moving 2xx → 4xx/5xx → **critical**: "X pages broke since last crawl" (list top 10 by traffic)
- Pages moving 4xx → 2xx → **info**: "Y pages fixed"
- New redirect chains > 2 hops → **warning**: "Z new long redirect chains"

### 4.5 Indexability Changes

**Headline**: Indexable page count (old → new).

| Metric | Source |
|--------|--------|
| Indexable count | `IndexabilityStats` |
| Non-indexable count | `IndexabilityStats` |
| Reason breakdown | `IndexabilityStats.Reasons` |
| Pages that became non-indexable | Per-URL `Indexability` comparison |
| Pages that became indexable | Per-URL `Indexability` comparison |

**Insights**:
- Pages that lost indexability AND had traffic → **critical**: list them with GSC/GA4 data
- New noindex tags on >10 pages → **warning**: "Mass noindex detected — was this intentional?"
- Canonical changes causing non-indexability → **warning**: "X pages became non-indexable due to canonical changes"

### 4.6 On-Page SEO

**Headline**: SEO element coverage rate.

| Metric | Old | New | Delta | Severity |
|--------|-----|-----|-------|----------|
| Pages without title | count | count | ±N | warn if increased |
| Pages without description | count | count | ±N | warn if increased |
| Pages without H1 | count | count | ±N | warn if increased |
| Duplicate titles | count | count | ±N | warn if increased |
| Duplicate descriptions | count | count | ±N | warn if increased |
| Title too long (>60) | count | count | ±N | info |
| Title too short (<30) | count | count | ±N | info |
| Description too long (>160) | count | count | ±N | info |
| Multiple H1s | count | count | ±N | info |
| Pages without OG tags | count | count | ±N | info |

**Per-URL detail**: Which specific URLs lost/gained title, description, H1 — especially high-traffic pages.

**Insights**:
- Titles lost on pages with >100 GSC clicks → **critical**
- Bulk title changes (>50 pages) → **warning**: "Mass title change detected — review for regressions"

### 4.7 Content Quality

**Headline**: Average word count and quality distribution.

| Metric | Source |
|--------|--------|
| Avg word count (persisted pages) | Per-URL `WordCount` comparison |
| Thin content pages (<200 words) | `ThinContentPages` delta |
| Readability avg (Flesch-Kincaid) | `ReadabilityStats` |
| Text-to-code ratio avg | `TextToCodeStats` |
| Content hash changes | Per-URL `ContentHash` comparison |
| Near-duplicate groups | `NearDuplicateGroups` delta |
| Exact duplicate groups | `DuplicateContentGroups` delta |

**Content Change Detection** (new capability):
For persisted URLs, compare `ContentHash`:
- **Modified pages**: Different hash = content changed. Count and list them.
- **Unchanged pages**: Same hash = identical content.
- **Word count delta per page**: Flag pages with >50% word count change (possible content strip or expansion).

**Insights**:
- Thin content pages increased → **warning**
- Duplicate groups increased → **warning**: "X new duplicate content groups"
- Large word count drops on traffic pages → **critical**: "Content stripped from high-traffic pages"
- Readability degraded significantly → **info**

### 4.8 Link Intelligence

**Headline**: Average PageRank and orphan count.

This is one of micelio's strongest differentiators. Deep link graph comparison.

| Metric | Source |
|--------|--------|
| Avg PageRank | `LinkIntelligenceStats` |
| PageRank distribution shift | `PageRankScores` per-URL comparison |
| Orphan pages | `OrphanPages` delta |
| Near-orphan pages | `LinkIntelligenceStats.NearOrphans` delta |
| Avg in-degree | Computed from `Inlinks` |
| Link dilution warnings | `LinkIntelligenceStats.DilutionWarnings` delta |
| Single points of failure | `LinkIntelligenceStats.SinglePointsOfFailure` delta |

**PageRank Movers**:
- **Winners**: Top 20 pages with biggest PageRank increase (with old/new scores)
- **Losers**: Top 20 pages with biggest PageRank decrease
- Filter by: only pages with traffic (GSC clicks > 0) for maximum relevance

**Hub/Authority Shifts**:
- Pages that gained/lost hub status (HITS hub score)
- Pages that gained/lost authority status (HITS authority score)
- Useful for understanding how content strategy changes affect link equity flow

**Centrality Changes**:
- Betweenness centrality winners/losers — "bridge" pages that gained/lost importance
- Closeness centrality changes — pages that became more/less accessible

**New Orphans**: Pages that had inlinks before but now have zero — likely caused by navigation changes or page removals.

**Insights**:
- New orphan pages with traffic → **critical**: "X pages with traffic lost all internal links"
- PageRank concentrated in fewer pages → **warning**: "Link equity becoming more concentrated"
- Hub pages removed → **warning**: "Key hub pages disappeared, affecting link distribution"

### 4.9 Internal Linking

**Headline**: Total internal links and linking efficiency.

| Metric | Source |
|--------|--------|
| Total internal links | `TotalInternalLinks` |
| Total external links | `TotalExternalLinks` |
| Dead-end pages (0 outlinks) | `LinkAnalysis.DeadEnds` |
| Links to non-indexable pages | `LinkAnalysis.LinksToNonIndexable` |
| Non-descriptive anchors | `NonDescriptiveAnchors` count |
| Avg internal links per page | Computed |

**Link Addition/Removal**: For persisted URLs, compare internal link count. Flag:
- Pages that lost >50% of internal links → possible navigation change or template regression
- Pages that gained many links → possibly good (or link spam)

**Insights**:
- Dead-end pages increased → **warning**: "X more pages with zero outgoing links"
- Links to non-indexable pages increased → **warning**: "Wasting link equity on non-indexable targets"

### 4.10 Redirect & Canonical Health

**Headline**: Redirect chain count and canonical issues.

| Metric | Source |
|--------|--------|
| Redirect chains total | `RedirectChains` count |
| Long chains (>2 hops) | `LongRedirectChains` count |
| Redirect loops | `RedirectStats.Loops` |
| HTTP→HTTPS redirects | `RedirectStats.HTTPToHTTPS` |
| Self-redirects | `RedirectStats.SelfRedirects` |
| Canonical to non-200 | `CanonicalStats.ToNon200` |
| Canonical to non-indexable | `CanonicalStats.ToNonIndexable` |
| Canonical loops | `CanonicalStats.Loops` |
| Canonical chains | `CanonicalStats.Chains` |
| Cross-domain canonicals | `CanonicalStats.CrossDomain` |

**Canonical Migration Matrix**: How canonical targets changed:
- Pages that changed canonical target (old URL → new URL)
- Pages that lost canonical (had one, now none)
- Pages that gained canonical (had none, now have one)

**Insights**:
- New redirect loops → **critical**
- Canonical pointing to non-200 pages → **critical**
- Long redirect chains grew → **warning**

### 4.11 Performance & Speed

**Headline**: p90 response time (old → new).

| Metric | Source |
|--------|--------|
| Response time p50/p90/p99 | `ResponseTimePercentiles` |
| Slow pages (>3s) count | `SlowPages` count |
| Avg page weight | `PageWeightStats.AvgBytes` |
| Oversized pages | `PageWeightStats.OversizedPages` count |
| PSI performance avg | `PerformanceScores.Avg` |
| CrUX LCP avg | `CruxStats.AvgLCP` |
| CrUX INP avg | `CruxStats.AvgINP` |
| CrUX CLS avg | `CruxStats.AvgCLS` |

**Per-URL performance changes** (for persisted URLs with PSI/CrUX data):
- Pages where LCP worsened by >500ms
- Pages where CLS increased by >0.05
- Pages where performance score dropped >10 points

**Insights**:
- p90 response time increased >500ms → **warning**: "Site getting slower"
- Page weight increased >20% → **warning**: "Pages are heavier — check for unoptimized assets"
- CrUX metrics degraded → **critical** (real user data, not synthetic)

### 4.12 Structured Data & Schema

**Headline**: Pages with valid structured data.

| Metric | Source |
|--------|--------|
| Pages with structured data | `PagesWithStructuredData` |
| Schema type distribution | `SchemaValidationStats.TypeDistribution` |
| Validation errors | `SchemaValidationStats.PagesWithErrors` |
| Rich result eligible | `SchemaValidationStats.RichResultEligible` |

**Schema Gains/Losses**: Pages that gained or lost structured data markup.

**Insights**:
- Schema markup removed from pages → **warning**
- Validation errors increased → **warning**: "Schema errors may affect rich results"
- Rich result eligibility improved → **info**: "X more pages eligible for rich results"

### 4.13 Image Accessibility

**Headline**: Image alt text coverage.

| Metric | Source |
|--------|--------|
| Total images | `TotalImages` |
| Missing alt text | `ImagesMissingAlt` |
| Missing dimensions | `ImageAuditStats.MissingDimensions` |
| Oversized images | `ImageAuditStats.Oversized` |
| Alt too long | `ImageAuditStats.AltTooLong` |

**Insights**:
- Alt text coverage dropped → **warning**
- Oversized images increased → **info**: "More unoptimized images detected"

### 4.14 Security

**Headline**: HTTPS adoption rate.

| Metric | Source |
|--------|--------|
| Non-HTTPS pages | `NonHTTPSPages` count |
| Mixed content pages | `MixedContentPages` count |
| HSTS adoption | Per-page `Security.HasHSTS` |
| CSP adoption | Per-page `Security.HasCSP` |

**Insights**:
- New mixed content pages → **warning**
- Pages moving HTTPS → HTTP → **critical**

### 4.15 Hreflang & Internationalization

**Headline**: Hreflang issue count.

| Metric | Source |
|--------|--------|
| Pages with hreflang | Count of pages where `len(Hreflang) > 0` |
| Hreflang issues | `HreflangIssues` count |
| New hreflang errors | Delta |

**Insights**:
- New hreflang issues → **warning**: "International targeting may be affected"
- Hreflang removed from pages → **info**: could be intentional or accidental

### 4.16 Sitemap Health

**Headline**: Sitemap coverage rate.

| Metric | Source |
|--------|--------|
| URLs in sitemap | `SitemapStats.InSitemap` |
| Indexable not in sitemap | `SitemapStats.Missing` |
| In sitemap but non-indexable | `SitemapStats.NonIndexableInSitemap` |
| In sitemap but 4xx/5xx | `SitemapStats.ErrorsInSitemap` |
| Sitemap coverage % | `SitemapStats.CoverageRate` |

**Insights**:
- Coverage rate dropped → **warning**: "New indexable pages not in sitemap"
- More errors in sitemap → **warning**: "Sitemap contains broken URLs"

### 4.17 Search Visibility (GSC/GA4/Plausible)

**Headline**: Total impressions, clicks, sessions.

Only available when integration data is present in both crawls.

| Metric | Source |
|--------|--------|
| Total impressions | `GscStats.TotalImpressions` |
| Total clicks | `GscStats.TotalClicks` |
| Avg CTR | `GscStats.AvgCTR` |
| Avg position | `GscStats.AvgPosition` |
| Zombie pages (0 traffic) | `GscStats.ZombiePages` / `Ga4Stats.NoTrafficPages` |
| Total sessions (GA4) | `Ga4Stats.TotalSessions` |
| Total pageviews (GA4) | `Ga4Stats.TotalPageviews` |
| Avg bounce rate | `Ga4Stats.AvgBounceRate` |
| Plausible visitors | `PlausibleStats.TotalVisitors` |

**Traffic Movers** (per-URL GSC comparison):
- **Winners**: Top 20 pages with biggest click increase
- **Losers**: Top 20 pages with biggest click decrease
- **Position improvers**: Pages that moved up in avg position
- **Position decliners**: Pages that dropped in position

**Zombie Page Trend**: Are more or fewer pages receiving zero traffic?

**Insights**:
- Total clicks decreased >10% → **critical**: "Significant traffic loss"
- Zombie pages increased → **warning**: "More pages receiving zero traffic"
- CTR improved while impressions stable → **info**: "Title/snippet optimization working"

### 4.18 N-gram & Topic Evolution

**Headline**: Topic focus shift.

| Metric | Source |
|--------|--------|
| Top unigrams change | `NgramStats.Unigrams` comparison |
| Top bigrams change | `NgramStats.Bigrams` comparison |
| Top trigrams change | `NgramStats.Trigrams` comparison |

**Topic Drift Detection**:
- N-grams that appeared in top 50 of new crawl but not old → "Emerging topics"
- N-grams that dropped out of top 50 → "Declining topics"
- N-grams with significant TF-IDF change → "Shifting emphasis"

**Insights**:
- Major topic shifts → **info**: "Content focus shifting toward [topic]"
- Brand terms dropping → **warning**: "Brand mention frequency declining"

### 4.19 Content Cannibalization (Embeddings)

**Headline**: Cannibalization group count.

Only available when embeddings are present in both crawls.

| Metric | Source |
|--------|--------|
| Similar page pairs | `EmbeddingStats.SimilarPairs` |
| Cannibalization groups | `EmbeddingStats.CannibalizationGroups` |

**Insights**:
- New cannibalization groups → **warning**: "X new groups of pages competing for same queries"
- Cannibalization groups resolved → **info**: "Y cannibalization issues fixed"

### 4.20 Segment-Level Breakdown

**Headline**: Per-segment health comparison.

Apply all of the above sections **per URL segment** (using `SegmentStats` / `TemplateTypeDistribution`).

Present as a table:

| Segment | Pages (Δ) | Health (Δ) | Indexable% (Δ) | Avg WC (Δ) | Avg PR (Δ) | Traffic (Δ) |
|---------|-----------|------------|-----------------|------------|------------|-------------|
| /blog/ | 500 (+50) | 78 (+3) | 95% (+2%) | 1200 (+50) | 3.2 (+0.1) | 5K (-200) |
| /products/ | 2000 (-10) | 65 (-5) | 88% (-3%) | 300 (+20) | 4.1 (-0.3) | 12K (+500) |

Users can drill into any segment for the full comparison report scoped to that segment.

---

## 5. Severity Framework

| Level | Criteria | Example |
|-------|----------|---------|
| **Critical** | Traffic-impacting regressions, indexability loss on important pages | High-traffic page returned 404 |
| **Warning** | Potential issues that need investigation | 50 pages lost meta descriptions |
| **Info** | Neutral observations or improvements | 20 new pages added with structured data |
| **Stable** | No meaningful change | Title coverage unchanged |

Severity is traffic-weighted: a change affecting a page with 10K clicks/month is more severe than one affecting a zero-traffic page.

---

## 6. Comparison Report Summary

The top of every comparison report shows a **dashboard view** with:

1. **Health Score**: Old → New (with delta badge: +5 ↑ or -3 ↓)
2. **Critical Issues**: Count of critical-severity findings (red badge)
3. **Improvements**: Count of positive changes (green badge)
4. **Key Numbers**: Pages (Δ), Indexable% (Δ), Avg Response Time (Δ), Traffic (Δ)
5. **Top 5 Findings**: The highest-severity insights across all sections, sorted by severity then traffic impact.

Below the summary, sections are presented in order of severity (sections with critical findings first).

---

## 7. Data Availability Handling

Not all crawls have the same data (e.g., some lack GSC, PSI, embeddings). The report gracefully handles this:

| Scenario | Behavior |
|----------|----------|
| Data in both crawls | Full comparison with deltas |
| Data in new crawl only | Show as "New data" (no comparison possible) |
| Data in old crawl only | Show as "Data no longer available" |
| Data in neither crawl | Omit section entirely |

---

## 8. UI Surface: Enhanced Diff Page

The comparison is surfaced by **enhancing the existing Diff page** (`/diff/:oldId/:newId`). No new routes or sidebar items. The entry point remains: **History → select 2 crawls → "Compare"**.

### 8.1 Entry Point (History Page)

No changes to the History page flow:
1. User navigates to History
2. Checks 2 completed crawls (same-domain enforcement added — disable checkbox if domain doesn't match first selection)
3. Clicks "Compare" button (renamed from "Compare Diff")
4. Navigates to `/diff/:oldId/:newId`

### 8.2 Enhanced Diff Page Layout

The current Diff page is restructured into a **tabbed interface** with the existing basic diff as the first tab and new insight sections as additional tabs.

#### Header (always visible)

```
┌─────────────────────────────────────────────────────────────┐
│  ← Back to History                                          │
│                                                             │
│  example.com                                                │
│  Mar 1, 2026 (8,450 pages)  →  Mar 8, 2026 (8,620 pages)  │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐   │
│  │ Health   │  │ Critical │  │ Warnings │  │ Improved │   │
│  │ 72 → 75  │  │    3     │  │    8     │  │   12     │   │
│  │   +3 ↑   │  │   (red)  │  │ (yellow) │  │  (green) │   │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘   │
│                                                             │
│  Top findings:                                              │
│  🔴 5 pages with traffic returned 404                       │
│  🔴 Indexability dropped from 92% to 87%                    │
│  🟡 23 pages lost meta descriptions                         │
│  🟢 15 broken links fixed                                   │
│  🟢 Response time p90 improved by 200ms                     │
└─────────────────────────────────────────────────────────────┘
```

#### Tab Bar

Horizontal scrollable tabs. Each tab shows a dot indicator (red/yellow/green/gray) reflecting the worst severity in that section. Tabs with no data are hidden.

```
[Overview] [URLs] [SEO] [Content] [Links] [Technical] [Performance] [Visibility] [Segments]
```

#### Tab: Overview (default)

The health score delta, SEO funnel side-by-side, and top 10 findings across all sections. This is the "executive summary" — quick glance at what matters.

Contents:
- Health score hero card (old → new with delta)
- SEO funnel comparison (crawled → indexable → visible → active, both crawls side-by-side)
- Top findings list (all critical + top warnings, sorted by severity then traffic impact)
- Key numbers row: Pages (Δ), Indexable% (Δ), p90 Response Time (Δ), Traffic (Δ)

#### Tab: URLs

The current Diff page content lives here — this is the existing functionality, unchanged:
- Summary cards (new, disappeared, changed, current)
- Disappeared URL reasons breakdown
- Disappeared-with-traffic alert
- Field change summary
- Sub-tabs: Changed / New / Removed (with per-URL field diffs)

#### Tab: SEO

On-page SEO changes:
- Title/Description/H1 coverage deltas (metric cards)
- Duplicate title/description trend
- Canonical health changes
- Hreflang issue changes
- Structured data gains/losses
- Image alt text coverage
- Collapsible per-URL detail: which pages lost/gained titles, etc.

#### Tab: Content

Content quality comparison:
- Avg word count delta
- Thin content page trend
- Content change detection (modified/unchanged pages based on content hash)
- Readability score changes
- Duplicate/near-duplicate group changes
- N-gram topic evolution (emerging/declining topics table)
- Cannibalization trend (if embeddings available)

#### Tab: Links

Link intelligence comparison:
- PageRank movers (winners + losers tables, top 20 each)
- Hub/Authority score shifts
- Centrality changes (betweenness + closeness)
- Orphan/near-orphan page delta
- Internal link count changes
- Dead-end page trend
- Link dilution warnings
- Single points of failure changes

#### Tab: Technical

HTTP and technical health:
- Status migration matrix (interactive table: old status → new status)
- Indexability changes (pages that became non-indexable, with reasons)
- Redirect chain changes
- Broken link delta
- Soft 404 changes
- Security changes (HTTPS, mixed content, HSTS, CSP)
- Sitemap coverage changes

#### Tab: Performance

Speed and weight comparison:
- Response time percentiles (p50/p90/p99) with old vs new bars
- Slow pages trend
- Page weight avg delta
- Oversized pages count
- PSI performance score changes (if available)
- CrUX metric changes (LCP, INP, CLS — if available)
- Per-URL performance regressions (pages that got slower)

#### Tab: Visibility

Search visibility (only shown when GSC/GA4/Plausible data exists):
- Total impressions/clicks/sessions deltas
- CTR and position trend
- Traffic movers (winners + losers tables)
- Zombie page trend
- Segment-level traffic breakdown

#### Tab: Segments

Per-segment comparison table:
- One row per URL segment
- Columns: Pages (Δ), Health (Δ), Indexable% (Δ), Avg WordCount (Δ), Avg PageRank (Δ), Traffic (Δ)
- Click any segment row to expand → shows that segment's key findings inline
- Sortable by any column

### 8.3 Progressive Loading

The enhanced Diff page loads in stages to keep initial load fast:
1. **Instant**: Header + health score + top findings (computed server-side as part of comparison)
2. **On tab click**: Each tab's data is fetched on demand (lazy loading)
3. **Heavy sections**: Link Intelligence and Embeddings tabs show a loading spinner while computing

API design:
- `POST /api/crawl/diff` — Enhanced to return the full `ComparisonReport` (backward-compatible: existing fields preserved, new fields added)
- Optional `?section=links` query param to fetch a single section (for lazy loading)

### 8.4 Responsive Design

- **Desktop (>1024px)**: Side-by-side metric cards, full tables, tab bar horizontal
- **Tablet (768-1024px)**: Stacked metric cards, scrollable tables
- **Mobile (<768px)**: Single column, tabs become a dropdown selector, tables scroll horizontally

### 8.5 Interaction Patterns

- **Metric cards**: Click to jump to the relevant tab section
- **URL lists**: Click any URL to open it in a new tab, or navigate to that URL's detail in the Results page (if results are loaded)
- **Tables**: Sortable columns, filterable (search box for URLs)
- **Sections**: Collapsible detail views (click "Show N affected URLs" to expand)
- **Export**: "Export Comparison" button in header → downloads HTML report (same as `micelio diff --html`)

---

## 9. Backend Implementation

### 9.1 `internal/report/comparison.go`

New file with:
- `ComparisonReport` struct (all sections from Section 4)
- `ComputeComparison(old, new *CrawlResults) *ComparisonReport` — main entry point
- `ComputeHealthScore(stats *CrawlStats) float64` — composite score calculation
- Per-section comparison functions (one per tab)
- Severity assignment logic

### 9.2 Enhanced API

The existing `POST /api/crawl/diff` endpoint is enhanced:
- Returns `ComparisonReport` instead of basic `DiffResult`
- `DiffResult` fields are preserved inside `ComparisonReport.URLs` for backward compatibility
- Optional `section` param for lazy loading individual tabs

### 9.3 CLI

The existing `micelio diff` command is enhanced with a `--full` flag:
```bash
# Basic diff (existing behavior, unchanged)
micelio diff old.jsonl new.jsonl

# Full comparison report
micelio diff old.jsonl new.jsonl --full [--html report.html] [--json report.json]
```

Options added:
- `--full` — Compute full comparison (default: basic diff only)
- `--json` — Machine-readable JSON output
- `--segments` — Include per-segment breakdown
- `--top N` — Number of items in "top movers" lists (default 20)

---

## 10. HTML Report Design

The comparison HTML report follows the existing report's dark-mode aesthetic but adds:

- **Sticky header**: Old crawl date <-> New crawl date, with health score delta
- **Traffic light indicators**: Green/yellow/red dots next to each section header
- **Expandable detail tables**: Click to see per-URL breakdowns
- **Chart.js sparklines**: Mini charts showing distribution comparisons
- **Print-friendly**: Clean layout when printed/exported to PDF
- **Tab navigation**: Same tab structure as the dashboard, but all rendered in static HTML with anchor links

---

## 11. Competitive Advantage

| Feature | Micelio Compare | Screaming Frog | Botify | Sitebulb |
|---------|-----------------|----------------|--------|----------|
| Health score delta | Yes | No | Partial | Partial |
| PageRank movers | Yes | No | No | No |
| Hub/Authority shifts | Yes | No | No | No |
| Centrality changes | Yes | No | No | No |
| Content hash change detection | Yes | Yes | Yes | No |
| Traffic-weighted severity | Yes | No | Yes | No |
| Segment-level comparison | Yes | No | Yes | Yes |
| N-gram topic evolution | Yes | No | No | No |
| Cannibalization trend | Yes | No | No | No |
| SEO funnel comparison | Yes | No | Yes | No |
| Status migration matrix | Yes | No | No | No |
| Link equity flow analysis | Yes | No | No | No |
| Free / local-first | Yes | Yes | No | No |

The combination of **link intelligence comparison** (PageRank movers, hub/authority shifts, centrality changes) and **traffic-weighted severity** is unique. No competitor offers this depth of graph analysis across crawls.

---

## 12. Future Extensions

These are explicitly **out of scope** for v1 but worth noting:

1. **Multi-crawl trends** — Compare 3+ crawls over time with sparkline charts (requires time-series storage)
2. **Automated alerts** — Webhook/email notifications when comparison exceeds severity thresholds
3. **Regression testing** — CI/CD integration: run crawl, compare to baseline, fail build if critical regressions
4. **AI-powered summary** — Use LLM to generate natural language summary of changes ("Your site health improved because...")
5. **Scheduled comparisons** — Auto-compare each scheduled crawl to the previous one
6. **Change attribution** — Correlate changes to git commits or deployment timestamps
