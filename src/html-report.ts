import type { PageData, CrawlStats } from './types.js';

/**
 * Lightweight page data for embedding in HTML (strip heavy fields).
 */
interface LightPageData {
  url: string;
  finalUrl: string;
  statusCode: number;
  responseTimeMs: number;
  title: string;
  titleLength: number;
  metaDescription: string;
  descriptionLength: number;
  canonical: string;
  metaRobots: string;
  h1: string[];
  h2Count: number;
  internalLinksCount: number;
  externalLinksCount: number;
  imagesCount: number;
  imagesMissingAlt: number;
  depth: number;
  wordCount: number;
  isHttps: boolean;
  hasMixedContent: boolean;
  psiScore: number | null;
  aiAnalysis: string;
  error: string;
  pageRank: number;
  inSitemap: boolean | null;
  totalWeight: number | null;
  indexable: boolean;
  indexabilityReason: string;
  readabilityScore: number | null;
  urlIssues: string[];
  isSoft404: boolean;
  textToCodeRatio: number;
  schemaTypes: string[];
  schemaErrors: string[];
  richResultTypes: string[];
  // Phase 10: Link targets for graph visualization
  linkTargets: string[];
  // Phase 11: GSC data
  gscImpressions: number | null;
  gscClicks: number | null;
  gscCtr: number | null;
  gscPosition: number | null;
  // Phase 11: GA4 data
  ga4Sessions: number | null;
  ga4Pageviews: number | null;
  ga4BounceRate: number | null;
  ga4Conversions: number | null;
  // Phase 12: Segments
  segments: string[];
  // Phase 12: Render diffs
  renderDiffs: { field: string; original: string; rendered: string }[] | null;
  // Phase 14.2: Image audit data
  images: { src: string; hasAltAttribute: boolean; altLength: number; altTooLong: boolean; missingWidth: boolean; missingHeight: boolean }[];
  // Canonical tag data
  canonicalCount: number;
  // Redirect chain data
  redirectChainLength: number;
  redirectChain: { url: string; statusCode: number }[];
  // Template type
  templateType: string;
}

function lightenPages(pages: PageData[], prScores: Map<string, number>, crawledUrls: Set<string>): LightPageData[] {
  return pages.map((p) => ({
    url: p.url,
    finalUrl: p.finalUrl,
    statusCode: p.statusCode,
    responseTimeMs: p.responseTimeMs,
    title: p.title?.text || '',
    titleLength: p.title?.length || 0,
    metaDescription: p.metaDescription?.text || '',
    descriptionLength: p.metaDescription?.length || 0,
    canonical: p.canonical || '',
    metaRobots: p.metaRobots || '',
    h1: p.headings.h1,
    h2Count: p.headings.h2.length,
    internalLinksCount: p.internalLinks.length,
    externalLinksCount: p.externalLinks.length,
    imagesCount: p.images.length,
    imagesMissingAlt: p.images.filter((i) => i.missingAlt).length,
    depth: p.depth,
    wordCount: p.wordCount,
    isHttps: p.security.isHttps,
    hasMixedContent: p.security.hasMixedContent,
    psiScore: p.pagespeed && !p.pagespeed.error ? p.pagespeed.performanceScore : null,
    aiAnalysis: p.aiAnalysis || '',
    error: p.error || '',
    pageRank: prScores.get(p.url) ?? 0,
    inSitemap: p.sitemapData ? p.sitemapData.inSitemap : null,
    totalWeight: p.pageWeight ? p.pageWeight.totalBytes : null,
    indexable: p.indexability?.indexable ?? true,
    indexabilityReason: p.indexability?.reason || '',
    readabilityScore: p.readability?.fleschKincaid ?? null,
    urlIssues: p.urlIssues || [],
    isSoft404: p.isSoft404 ?? false,
    textToCodeRatio: p.textToCodeRatio ?? 0,
    schemaTypes: p.schemaValidation?.map(sv => sv.type) || [],
    schemaErrors: p.schemaValidation
      ?.flatMap(sv => sv.issues.filter(i => i.severity === 'error').map(i => i.message)) || [],
    richResultTypes: p.schemaValidation
      ?.filter(sv => sv.richResultEligible)
      .map(sv => sv.richResultType)
      .filter((t): t is string => t !== null) || [],
    // Only include links to other crawled pages (for graph visualization)
    linkTargets: p.internalLinks.filter(link => crawledUrls.has(link)),
    // Phase 11: GSC data
    gscImpressions: p.gscData?.impressions ?? null,
    gscClicks: p.gscData?.clicks ?? null,
    gscCtr: p.gscData?.ctr ?? null,
    gscPosition: p.gscData?.position ?? null,
    // Phase 11: GA4 data
    ga4Sessions: p.ga4Data?.sessions ?? null,
    ga4Pageviews: p.ga4Data?.pageviews ?? null,
    ga4BounceRate: p.ga4Data?.bounceRate ?? null,
    ga4Conversions: p.ga4Data?.conversions ?? null,
    segments: p.segments || [],
    renderDiffs: p.renderDiffs || null,
    images: p.images.map((i) => ({
      src: i.src, hasAltAttribute: i.hasAltAttribute, altLength: i.altLength,
      altTooLong: i.altTooLong, missingWidth: i.missingWidth, missingHeight: i.missingHeight,
    })),
    canonicalCount: p.canonicalCount,
    redirectChainLength: p.redirectChain.length,
    redirectChain: p.redirectChain.map(h => ({ url: h.url, statusCode: h.statusCode })),
    templateType: p.templateType || 'other',
  }));
}

export function generateHtmlReport(
  pages: PageData[],
  stats: CrawlStats,
  seedUrl: string,
): string {
  const crawledUrls = new Set(pages.map(p => p.url));
  const lightPages = lightenPages(pages, stats.pageRankScores, crawledUrls);
  // #1 fix: Escape </script> sequences to prevent XSS via embedded JSON
  const pagesJson = JSON.stringify(lightPages).replace(/<\//g, '<\\/');
  // Convert Map to plain object for JSON serialization; strip vectors to avoid bloating HTML
  const embeddingStatsClean = stats.embeddingStats
    ? { ...stats.embeddingStats, vectors: undefined }
    : null;
  const statsForJson = { ...stats, pageRankScores: undefined, embeddingStats: embeddingStatsClean };
  const statsJson = JSON.stringify(statsForJson).replace(/<\//g, '<\\/');

  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Micelio Report — ${esc(seedUrl)}</title>
<style>
${CSS}
</style>
</head>
<body>
<header>
  <div class="header-inner">
    <h1>Micelio</h1>
    <div class="header-meta">
      <span class="site-url">${esc(seedUrl)}</span>
      <span class="crawl-date">${new Date().toLocaleDateString('en-US', { year: 'numeric', month: 'short', day: 'numeric' })}</span>
    </div>
  </div>
</header>

<main>
  <section class="cards" id="cards"></section>
  <section class="charts" id="charts"></section>
  <section class="issues" id="issues"></section>
  <section class="ngrams-section" id="ngrams-section"></section>
  <section class="viz-section" id="viz-section">
    <div class="viz-tabs">
      <button class="viz-tab active" data-viz="graph">Site Graph</button>
      <button class="viz-tab" data-viz="tree">Directory Tree</button>
      <button class="viz-tab" data-viz="redirects">Redirect Chains</button>
    </div>
    <div id="viz-graph" class="viz-panel active">
      <div id="graph-container" style="width:100%;height:500px;background:var(--bg);border-radius:var(--radius);border:1px solid var(--border);position:relative;overflow:hidden;"></div>
    </div>
    <div id="viz-tree" class="viz-panel">
      <div id="tree-container" style="padding:16px;max-height:500px;overflow:auto;background:var(--bg);border-radius:var(--radius);border:1px solid var(--border);"></div>
    </div>
    <div id="viz-redirects" class="viz-panel">
      <div id="redirects-container" style="padding:16px;max-height:600px;overflow:auto;background:var(--bg);border-radius:var(--radius);border:1px solid var(--border);"></div>
    </div>
  </section>
  <section class="table-section">
    <div class="table-header">
      <h2>All Pages</h2>
      <input type="text" id="search" placeholder="Filter pages..." />
    </div>
    <div class="table-wrap">
      <table id="pages-table">
        <thead><tr id="table-head"></tr></thead>
        <tbody id="table-body"></tbody>
      </table>
    </div>
  </section>
</main>

<footer>Generated by <strong>Micelio</strong> &mdash; ${stats.totalPages} pages in ${(stats.crawlDurationMs / 1000).toFixed(1)}s</footer>

<script src="https://d3js.org/d3.v7.min.js" integrity="sha384-CjloA8y00+1SDAUkjs099PVfnY2KmDC2BZnws9kh8D/lX1s46w6EPhpXdqMfjK6i" crossorigin="anonymous"></script>
<script>
const PAGES = ${pagesJson};
const STATS = ${statsJson};
${JS}
</script>
</body>
</html>`;
}

function esc(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

// ── Inline CSS ──────────────────────────────────────────────

const CSS = `
:root {
  --bg: #f8f9fa; --bg2: #ffffff; --fg: #1a1a2e; --fg2: #555; --accent: #4361ee;
  --green: #2ec4b6; --yellow: #f5a623; --red: #e63946; --blue: #4361ee;
  --border: #e0e0e0; --card-shadow: 0 2px 8px rgba(0,0,0,0.06);
  --radius: 8px; --font: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg: #0f0f1a; --bg2: #1a1a2e; --fg: #e0e0e0; --fg2: #999; --border: #2a2a3e;
    --card-shadow: 0 2px 8px rgba(0,0,0,0.3);
  }
}
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: var(--font); background: var(--bg); color: var(--fg); line-height: 1.5; }
header { background: var(--bg2); border-bottom: 1px solid var(--border); padding: 16px 24px; }
.header-inner { max-width: 1400px; margin: 0 auto; display: flex; align-items: center; gap: 16px; flex-wrap: wrap; }
header h1 { font-size: 20px; color: var(--accent); letter-spacing: -0.5px; }
.header-meta { display: flex; gap: 16px; font-size: 13px; color: var(--fg2); }
.site-url { font-weight: 600; color: var(--fg); }
main { max-width: 1400px; margin: 0 auto; padding: 24px; }
footer { text-align: center; padding: 24px; font-size: 12px; color: var(--fg2); border-top: 1px solid var(--border); margin-top: 32px; }

/* Cards */
.cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; margin-bottom: 24px; }
.card { background: var(--bg2); border-radius: var(--radius); padding: 20px; box-shadow: var(--card-shadow); border: 1px solid var(--border); }
.card .label { font-size: 12px; text-transform: uppercase; letter-spacing: 0.5px; color: var(--fg2); margin-bottom: 4px; }
.card .value { font-size: 28px; font-weight: 700; }
.card .sub { font-size: 12px; color: var(--fg2); margin-top: 4px; }
.card.green .value { color: var(--green); }
.card.yellow .value { color: var(--yellow); }
.card.red .value { color: var(--red); }
.card.blue .value { color: var(--blue); }

/* Charts */
.charts { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 16px; margin-bottom: 24px; }
.chart-box { background: var(--bg2); border-radius: var(--radius); padding: 20px; box-shadow: var(--card-shadow); border: 1px solid var(--border); }
.chart-box h3 { font-size: 14px; margin-bottom: 12px; color: var(--fg2); }
.bar-chart { display: flex; flex-direction: column; gap: 6px; }
.bar-row { display: flex; align-items: center; gap: 8px; }
.bar-label { font-size: 12px; min-width: 60px; text-align: right; color: var(--fg2); }
.bar-track { flex: 1; height: 22px; background: var(--border); border-radius: 4px; overflow: hidden; position: relative; }
.bar-fill { height: 100%; border-radius: 4px; transition: width 0.3s; display: flex; align-items: center; padding-left: 6px; }
.bar-fill span { font-size: 11px; color: #fff; font-weight: 600; white-space: nowrap; }
.donut-wrap { display: flex; align-items: center; gap: 20px; }
.donut-legend { display: flex; flex-direction: column; gap: 4px; }
.legend-item { display: flex; align-items: center; gap: 6px; font-size: 12px; }
.legend-dot { width: 10px; height: 10px; border-radius: 50%; }

/* Issues */
.issues { margin-bottom: 24px; }
.issue-group { background: var(--bg2); border-radius: var(--radius); margin-bottom: 8px; border: 1px solid var(--border); overflow: hidden; }
.issue-header { padding: 12px 16px; cursor: pointer; display: flex; justify-content: space-between; align-items: center; user-select: none; }
.issue-header:hover { background: var(--border); }
.issue-header h3 { font-size: 14px; font-weight: 600; }
.issue-count { font-size: 13px; padding: 2px 8px; border-radius: 10px; font-weight: 600; }
.issue-count.warn { background: #fef3cd; color: #856404; }
.issue-count.error { background: #f8d7da; color: #721c24; }
.issue-count.ok { background: #d4edda; color: #155724; }
.issue-count.info { background: #cce5ff; color: #004085; }
@media (prefers-color-scheme: dark) {
  .issue-count.warn { background: #3d3200; color: #f5a623; }
  .issue-count.error { background: #3d0000; color: #e63946; }
  .issue-count.ok { background: #003d00; color: #2ec4b6; }
  .issue-count.info { background: #002a4d; color: #66b2ff; }
}
.issue-body { padding: 0 16px 12px; display: none; }
.issue-body.open { display: block; }
.issue-list { list-style: none; }
.issue-list li { padding: 4px 0; font-size: 13px; border-bottom: 1px solid var(--border); }
.issue-list li:last-child { border-bottom: none; }
.issue-list .url { color: var(--accent); word-break: break-all; }
.issue-list .meta { color: var(--fg2); font-size: 12px; }

/* Visualization */
.viz-section { margin-bottom: 24px; background: var(--bg2); border-radius: var(--radius); border: 1px solid var(--border); overflow: hidden; }
.viz-tabs { display: flex; border-bottom: 1px solid var(--border); padding: 0 16px; }
.viz-tab { padding: 10px 16px; border: none; background: none; color: var(--fg2); cursor: pointer; font-size: 14px; font-weight: 500; border-bottom: 2px solid transparent; margin-bottom: -1px; }
.viz-tab.active { color: var(--accent); border-bottom-color: var(--accent); }
.viz-tab:hover { color: var(--fg); }
.viz-panel { display: none; padding: 16px; }
.viz-panel.active { display: block; }
.graph-tooltip { position: absolute; background: var(--bg2); border: 1px solid var(--border); border-radius: 6px; padding: 8px 12px; font-size: 12px; pointer-events: none; z-index: 10; box-shadow: 0 2px 8px rgba(0,0,0,0.15); }
.tree-node { cursor: pointer; user-select: none; }
.tree-node:hover { background: var(--border); border-radius: 4px; }
.tree-toggle { display: inline-block; width: 16px; text-align: center; color: var(--fg2); font-size: 11px; }
.tree-label { padding: 3px 6px; font-size: 13px; }
.tree-count { color: var(--fg2); font-size: 11px; margin-left: 4px; }
.tree-children { padding-left: 20px; }
.tree-children.collapsed { display: none; }

/* Redirect Chains */
.redirect-filters { display: flex; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; align-items: center; }
.redirect-filters select, .redirect-filters input { padding: 4px 8px; border: 1px solid var(--border); border-radius: 4px; background: var(--bg2); color: var(--fg); font-size: 13px; }
.redirect-summary { display: flex; gap: 16px; margin-bottom: 16px; flex-wrap: wrap; }
.redirect-stat { background: var(--bg2); border: 1px solid var(--border); border-radius: var(--radius); padding: 8px 14px; text-align: center; }
.redirect-stat .rs-val { font-size: 20px; font-weight: 700; }
.redirect-stat .rs-lbl { font-size: 11px; color: var(--fg2); text-transform: uppercase; }
.chain-item { margin-bottom: 12px; padding: 10px 14px; background: var(--bg2); border: 1px solid var(--border); border-radius: var(--radius); }
.chain-flow { display: flex; align-items: center; gap: 0; flex-wrap: wrap; margin: 6px 0; }
.chain-hop { display: inline-flex; align-items: center; gap: 4px; padding: 3px 8px; border-radius: 4px; font-size: 12px; font-family: monospace; word-break: break-all; max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chain-hop.s301, .chain-hop.s308 { background: #d4edda; color: #155724; }
.chain-hop.s302, .chain-hop.s307 { background: #fef3cd; color: #856404; }
.chain-hop.s3xx { background: #cce5ff; color: #004085; }
.chain-hop.final { background: var(--bg); border: 1px solid var(--green); color: var(--green); }
@media (prefers-color-scheme: dark) {
  .chain-hop.s301, .chain-hop.s308 { background: #003d00; color: #2ec4b6; }
  .chain-hop.s302, .chain-hop.s307 { background: #3d3200; color: #f5a623; }
  .chain-hop.s3xx { background: #002a4d; color: #66b2ff; }
  .chain-hop.final { background: var(--bg); }
}
.chain-arrow { color: var(--fg2); font-size: 16px; margin: 0 4px; flex-shrink: 0; }
.chain-meta { font-size: 11px; color: var(--fg2); }

/* N-Grams */
.ngrams-section { margin-bottom: 24px; }
.ngrams-section h2 { font-size: 16px; margin-bottom: 12px; }
.ngrams-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(340px, 1fr)); gap: 16px; }
.ngram-panel { background: var(--bg2); border-radius: var(--radius); border: 1px solid var(--border); box-shadow: var(--card-shadow); overflow: hidden; }
.ngram-panel h3 { font-size: 14px; padding: 14px 16px 10px; color: var(--fg2); border-bottom: 1px solid var(--border); }
.ngram-table { width: 100%; border-collapse: collapse; font-size: 12px; }
.ngram-table th { padding: 6px 12px; text-align: left; font-size: 11px; text-transform: uppercase; letter-spacing: 0.3px; color: var(--fg2); border-bottom: 1px solid var(--border); font-weight: 600; }
.ngram-table th:not(:first-child) { text-align: right; }
.ngram-table td { padding: 5px 12px; border-bottom: 1px solid var(--border); }
.ngram-table td:not(:first-child) { text-align: right; font-variant-numeric: tabular-nums; }
.ngram-table tr:last-child td { border-bottom: none; }
.ngram-table tr:hover td { background: var(--bg); }
.ngram-term { font-weight: 500; max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ngram-bar { display: inline-block; height: 14px; border-radius: 2px; margin-right: 6px; vertical-align: middle; min-width: 2px; }
.ngram-meta { padding: 12px 16px; font-size: 12px; color: var(--fg2); border-top: 1px solid var(--border); }

/* Table */
.table-section { background: var(--bg2); border-radius: var(--radius); border: 1px solid var(--border); overflow: hidden; }
.table-header { padding: 16px; display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid var(--border); }
.table-header h2 { font-size: 16px; }
#search { padding: 6px 12px; border: 1px solid var(--border); border-radius: 6px; background: var(--bg); color: var(--fg); font-size: 13px; width: 240px; }
.table-wrap { overflow-x: auto; }
table { width: 100%; border-collapse: collapse; font-size: 13px; }
th { padding: 8px 12px; text-align: left; font-weight: 600; font-size: 12px; text-transform: uppercase; letter-spacing: 0.3px; color: var(--fg2); border-bottom: 2px solid var(--border); cursor: pointer; user-select: none; white-space: nowrap; }
th:hover { color: var(--fg); }
th .sort-arrow { margin-left: 4px; font-size: 10px; }
td { padding: 8px 12px; border-bottom: 1px solid var(--border); max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
tr:hover td { background: var(--bg); }
.status-ok { color: var(--green); font-weight: 600; }
.status-redirect { color: var(--yellow); font-weight: 600; }
.status-error { color: var(--red); font-weight: 600; }
.expand-row td { white-space: normal; background: var(--bg); font-size: 12px; padding: 12px; }
.detail-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 8px; }
.detail-item .dl { font-size: 11px; color: var(--fg2); text-transform: uppercase; }
.detail-item .dv { font-size: 13px; word-break: break-all; }
`;

// ── Inline JavaScript ───────────────────────────────────────

const JS = `
(function() {
  // #2 fix: Escape HTML to prevent XSS from crawled page data
  function esc(s) {
    if (typeof s !== 'string') return String(s == null ? '' : s);
    return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
  }

  function formatBytesJs(bytes) {
    if (!bytes || bytes === 0) return '0 B';
    var units = ['B', 'KB', 'MB', 'GB'];
    var i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    var value = bytes / Math.pow(1024, i);
    return value.toFixed(i === 0 ? 0 : 1) + ' ' + units[i];
  }

  // ─── Cards ─────────────────
  const cardsEl = document.getElementById('cards');
  const totalPages = STATS.totalPages;
  const ok = Object.entries(STATS.statusCodes).reduce((s,[c,n]) => +c < 400 ? s+n : s, 0);
  const errors = totalPages - ok;
  const avgTime = PAGES.length > 0 ? Math.round(PAGES.reduce((s,p) => s+p.responseTimeMs, 0) / PAGES.length) : 0;
  const issueCount = STATS.pagesWithoutTitle + STATS.pagesWithoutDescription + STATS.pagesWithoutH1
    + STATS.brokenLinks.length + STATS.brokenExternalLinks.length + STATS.mixedContentPages.length;

  // #4 fix: Guard against zero division
  var pagesPerSec = STATS.crawlDurationMs > 0 ? (totalPages / (STATS.crawlDurationMs/1000)).toFixed(1) : '0';
  var okPct = totalPages > 0 ? ((ok/totalPages)*100).toFixed(0) : '0';

  cardsEl.innerHTML = [
    card('Total Pages', totalPages, pagesPerSec + ' pages/s', 'blue'),
    card('Successful', ok, okPct + '% OK', 'green'),
    card('Errors', errors, errors > 0 ? 'Needs attention' : 'All clear', errors > 0 ? 'red' : 'green'),
    card('Avg Response', avgTime + 'ms', 'p90: ' + STATS.responseTimePercentiles.p90 + 'ms', avgTime > 2000 ? 'red' : avgTime > 1000 ? 'yellow' : 'green'),
    card('Issues Found', issueCount, issueCount > 0 ? 'See details below' : 'No issues', issueCount > 0 ? 'yellow' : 'green'),
    STATS.performanceScores ? card('PSI Avg', STATS.performanceScores.avg + '/100', 'Min: ' + STATS.performanceScores.min + ' Max: ' + STATS.performanceScores.max, STATS.performanceScores.avg >= 90 ? 'green' : STATS.performanceScores.avg >= 50 ? 'yellow' : 'red') : '',
  ].join('');

  function card(label, value, sub, color) {
    return '<div class="card '+color+'"><div class="label">'+label+'</div><div class="value">'+value+'</div><div class="sub">'+sub+'</div></div>';
  }

  // ─── Charts ────────────────
  const chartsEl = document.getElementById('charts');
  let chartsHtml = '';

  // Status code donut
  const statusColors = {'2':'#2ec4b6','3':'#4361ee','4':'#f5a623','5':'#e63946'};
  const statusEntries = Object.entries(STATS.statusCodes).sort(([a],[b]) => +a - +b);
  let segments = '', offset = 0;
  const total = statusEntries.reduce((s,[,n]) => s+n, 0);
  for (const [code, count] of statusEntries) {
    const pct = (count / total) * 100;
    const dash = pct * 2.51327; // circumference = 251.327
    const color = statusColors[code[0]] || '#999';
    segments += '<circle r="40" cx="50" cy="50" fill="none" stroke="'+color+'" stroke-width="16" stroke-dasharray="'+dash+' 251.327" stroke-dashoffset="'+(-(offset * 2.51327))+'" />';
    offset += pct;
  }
  const donutLegend = statusEntries.map(([code, count]) => {
    const color = statusColors[code[0]] || '#999';
    return '<div class="legend-item"><div class="legend-dot" style="background:'+color+'"></div>'+code+': '+count+' ('+((count/total)*100).toFixed(0)+'%)</div>';
  }).join('');

  chartsHtml += '<div class="chart-box"><h3>Status Codes</h3><div class="donut-wrap"><svg viewBox="0 0 100 100" width="120" height="120" style="transform:rotate(-90deg)">'+segments+'</svg><div class="donut-legend">'+donutLegend+'</div></div></div>';

  // Depth distribution bar chart
  const depthEntries = Object.entries(STATS.depthDistribution).sort(([a],[b]) => +a - +b);
  const maxDepth = Math.max(...depthEntries.map(([,n]) => n), 1);
  let depthBars = '';
  for (const [depth, count] of depthEntries) {
    const pct = (count / maxDepth) * 100;
    depthBars += '<div class="bar-row"><div class="bar-label">Depth '+depth+'</div><div class="bar-track"><div class="bar-fill" style="width:'+pct+'%;background:var(--blue)"><span>'+count+'</span></div></div></div>';
  }
  chartsHtml += '<div class="chart-box"><h3>Crawl Depth Distribution</h3><div class="bar-chart">'+depthBars+'</div></div>';

  // Enhanced depth analysis: avg PageRank & avg response time per depth
  const depthAnalysis = {};
  for (const pg of PAGES) {
    const dKey = pg.depth;
    if (!depthAnalysis[dKey]) depthAnalysis[dKey] = { count: 0, prSum: 0, rtSum: 0 };
    depthAnalysis[dKey].count++;
    depthAnalysis[dKey].prSum += pg.pageRank;
    depthAnalysis[dKey].rtSum += pg.responseTimeMs;
  }
  const daEntries = Object.entries(depthAnalysis).sort(([a],[b]) => +a - +b);
  if (daEntries.length > 1) {
    const maxAvgPR = Math.max(...daEntries.map(e => e[1].prSum / e[1].count));
    let prBars = '';
    for (const de of daEntries) {
      const avgPR = de[1].prSum / de[1].count;
      const prPct = maxAvgPR > 0 ? (avgPR / maxAvgPR) * 100 : 0;
      const prColor = avgPR >= 3 ? 'var(--green)' : avgPR >= 1 ? 'var(--blue)' : 'var(--fg2)';
      prBars += '<div class="bar-row"><div class="bar-label">Depth '+de[0]+'</div><div class="bar-track"><div class="bar-fill" style="width:'+prPct+'%;background:'+prColor+'"><span>'+avgPR.toFixed(2)+'</span></div></div></div>';
    }
    chartsHtml += '<div class="chart-box"><h3>Avg PageRank by Depth</h3><div class="bar-chart">'+prBars+'</div></div>';

    const maxAvgRT = Math.max(...daEntries.map(e => e[1].rtSum / e[1].count));
    let rtBars = '';
    for (const re of daEntries) {
      const avgRT = re[1].rtSum / re[1].count;
      const rtPct = maxAvgRT > 0 ? (avgRT / maxAvgRT) * 100 : 0;
      const rtColor = avgRT > 2000 ? 'var(--red)' : avgRT > 1000 ? 'var(--yellow)' : 'var(--green)';
      rtBars += '<div class="bar-row"><div class="bar-label">Depth '+re[0]+'</div><div class="bar-track"><div class="bar-fill" style="width:'+rtPct+'%;background:'+rtColor+'"><span>'+Math.round(avgRT)+'ms</span></div></div></div>';
    }
    chartsHtml += '<div class="chart-box"><h3>Avg Response Time by Depth</h3><div class="bar-chart">'+rtBars+'</div></div>';
  }

  // Response time distribution
  const timeBuckets = [
    {label:'<500ms', max:500, color:'var(--green)'},
    {label:'500ms-1s', max:1000, color:'var(--green)'},
    {label:'1-2s', max:2000, color:'var(--yellow)'},
    {label:'2-3s', max:3000, color:'var(--yellow)'},
    {label:'>3s', max:Infinity, color:'var(--red)'},
  ];
  const timeCounts = timeBuckets.map(b => ({...b, count:0}));
  for (const p of PAGES) {
    for (const b of timeCounts) {
      if (p.responseTimeMs < b.max || b.max === Infinity) { b.count++; break; }
    }
  }
  const maxTimeCount = Math.max(...timeCounts.map(b => b.count), 1);
  let timeBars = '';
  for (const b of timeCounts) {
    const pct = (b.count / maxTimeCount) * 100;
    timeBars += '<div class="bar-row"><div class="bar-label">'+b.label+'</div><div class="bar-track"><div class="bar-fill" style="width:'+pct+'%;background:'+b.color+'"><span>'+b.count+'</span></div></div></div>';
  }
  chartsHtml += '<div class="chart-box"><h3>Response Time Distribution</h3><div class="bar-chart">'+timeBars+'</div></div>';

  // Template type distribution chart
  if (STATS.templateTypeDistribution) {
    const templateColors = {
      homepage:'#2ec4b6', listing:'#4361ee', product:'#f5a623', article:'#7209b7',
      legal:'#6c757d', contact:'#e63946', faq:'#06d6a0', search:'#118ab2',
      login:'#ef476f', other:'#adb5bd'
    };
    const ttdEntries = Object.entries(STATS.templateTypeDistribution).sort(([,a],[,b]) => b - a);
    const maxTemplateCount = Math.max(...ttdEntries.map(([,n]) => n), 1);
    let templateBars = '';
    for (const [type, count] of ttdEntries) {
      const pct = (count / maxTemplateCount) * 100;
      const color = templateColors[type] || '#adb5bd';
      templateBars += '<div class="bar-row"><div class="bar-label">'+type+'</div><div class="bar-track"><div class="bar-fill" style="width:'+pct+'%;background:'+color+'"><span>'+count+'</span></div></div></div>';
    }
    chartsHtml += '<div class="chart-box"><h3>Page Template Types</h3><div class="bar-chart">'+templateBars+'</div></div>';
  }

  // PSI scores chart (if available)
  const psiPages = PAGES.filter(p => p.psiScore !== null);
  if (psiPages.length > 0) {
    const psiBuckets = [
      {label:'90-100', min:90, color:'var(--green)'},
      {label:'50-89', min:50, color:'var(--yellow)'},
      {label:'0-49', min:0, color:'var(--red)'},
    ];
    const psiCounts = psiBuckets.map(b => ({...b, count:0}));
    for (const p of psiPages) {
      for (const b of psiCounts) {
        if (p.psiScore >= b.min) { b.count++; break; }
      }
    }
    const maxPsi = Math.max(...psiCounts.map(b => b.count), 1);
    let psiBars = '';
    for (const b of psiCounts) {
      const pct = (b.count / maxPsi) * 100;
      psiBars += '<div class="bar-row"><div class="bar-label">'+b.label+'</div><div class="bar-track"><div class="bar-fill" style="width:'+pct+'%;background:'+b.color+'"><span>'+b.count+'</span></div></div></div>';
    }
    chartsHtml += '<div class="chart-box"><h3>PageSpeed Scores (Mobile)</h3><div class="bar-chart">'+psiBars+'</div></div>';
  }

  chartsEl.innerHTML = chartsHtml;

  // ─── Issues ────────────────
  const issuesEl = document.getElementById('issues');
  let issuesHtml = '';

  function issueGroup(title, items, severity) {
    const count = items.length;
    if (count === 0) return '';
    const cls = severity === 'error' ? 'error' : severity === 'warn' ? 'warn' : severity === 'info' ? 'info' : 'ok';
    const bodyHtml = '<ul class="issue-list">' + items.slice(0, 50).map(i => '<li>' + i + '</li>').join('') + (count > 50 ? '<li class="meta">... and ' + (count - 50) + ' more</li>' : '') + '</ul>';
    return '<div class="issue-group"><div class="issue-header"><h3>'+title+'</h3><span class="issue-count '+cls+'">'+count+'</span></div><div class="issue-body">'+bodyHtml+'</div></div>';
  }

  issuesHtml += issueGroup('Missing Title', STATS.pagesWithoutTitle > 0 ? PAGES.filter(p => !p.title && p.statusCode === 200).map(p => '<span class="url">'+esc(p.url)+'</span>') : [], 'warn');
  issuesHtml += issueGroup('Missing Meta Description', STATS.pagesWithoutDescription > 0 ? PAGES.filter(p => !p.metaDescription && p.statusCode === 200).map(p => '<span class="url">'+esc(p.url)+'</span>') : [], 'warn');
  issuesHtml += issueGroup('Missing H1', STATS.pagesWithoutH1 > 0 ? PAGES.filter(p => p.h1.length === 0 && p.statusCode === 200).map(p => '<span class="url">'+esc(p.url)+'</span>') : [], 'warn');
  issuesHtml += issueGroup('Broken Internal Links', STATS.brokenLinks.map(b => '<span class="url">['+b.statusCode+'] '+esc(b.url)+'</span>' + (b.foundOn.length > 0 ? ' <span class="meta">linked from: '+esc(b.foundOn[0])+'</span>' : '')), 'error');
  issuesHtml += issueGroup('Broken External Links', STATS.brokenExternalLinks.map(b => '<span class="url">['+b.statusCode+'] '+esc(b.url)+'</span>' + (b.foundOn.length > 0 ? ' <span class="meta">linked from: '+esc(b.foundOn[0])+'</span>' : '')), 'error');
  issuesHtml += issueGroup('Thin Content (<200 words)', STATS.thinContentPages.map(t => '<span class="url">'+esc(t.url)+'</span> <span class="meta">'+t.wordCount+' words</span>'), 'warn');
  issuesHtml += issueGroup('Duplicate Content', STATS.duplicateContentGroups.map(g => '<span class="meta">'+g.urls.length+' pages:</span> ' + g.urls.slice(0,3).map(u => '<span class="url">'+esc(u)+'</span>').join(', ')), 'warn');
  issuesHtml += issueGroup('Hreflang Issues', STATS.hreflangIssues.map(h => '<span class="url">'+esc(h.url)+'</span> <span class="meta">'+esc(h.issue)+'</span>'), 'warn');
  issuesHtml += issueGroup('Mixed Content', STATS.mixedContentPages.map(u => '<span class="url">'+esc(u)+'</span>'), 'error');
  issuesHtml += issueGroup('Non-HTTPS Pages', STATS.nonHttpsPages.map(u => '<span class="url">'+esc(u)+'</span>'), 'error');
  issuesHtml += issueGroup('Slow Pages (>3s)', STATS.slowPages.map(s => '<span class="url">'+esc(s.url)+'</span> <span class="meta">'+(s.responseTimeMs/1000).toFixed(1)+'s</span>'), 'warn');
  issuesHtml += issueGroup('Long Redirect Chains (>2 hops)', STATS.longRedirectChains.map(r => '<span class="url">'+esc(r.url)+'</span> <span class="meta">'+r.hops+' hops</span>'), 'warn');
  issuesHtml += issueGroup('Orphan Pages (0 inlinks)', STATS.orphanPages.map(u => '<span class="url">'+esc(u)+'</span>'), 'warn');
  issuesHtml += issueGroup('Non-Descriptive Anchors', STATS.nonDescriptiveAnchors.map(a => esc('"' + (a.text || '<empty>') + '"') + ' &rarr; <span class="url">'+esc(a.url)+'</span> <span class="meta">on: '+esc(a.foundOn)+'</span>'), 'warn');

  // Phase 6: Sitemap audit issues
  if (STATS.sitemapStats) {
    const sm = STATS.sitemapStats;
    issuesHtml += issueGroup('Sitemap: Missing from Sitemap', sm.missingFromSitemap.map(u => '<span class="url">'+esc(u)+'</span>'), 'warn');
    issuesHtml += issueGroup('Sitemap: Orphan URLs (in sitemap, no internal links)', sm.orphanUrls.map(u => '<span class="url">'+esc(u)+'</span>'), 'warn');
    issuesHtml += issueGroup('Sitemap: Non-Indexable in Sitemap', sm.nonIndexableInSitemap.map(u => '<span class="url">'+esc(u)+'</span>'), 'error');
    // Phase 6.2: Redirects in sitemap
    if (sm.redirectsInSitemap.length > 0) {
      issuesHtml += issueGroup('Sitemap: Redirecting URLs', sm.redirectsInSitemap.map(u => '<span class="url">'+esc(u)+'</span>'), 'warn');
    }
    // Phase 6.2: Validation warnings
    if (sm.validationWarnings.length > 0) {
      issuesHtml += issueGroup('Sitemap: Validation Warnings', sm.validationWarnings.map(w => esc(w)), 'warn');
    }
    // Phase 6.3: Duplicate across sitemaps
    if (sm.duplicateAcrossSitemaps.length > 0) {
      issuesHtml += issueGroup('Sitemap: URLs in Multiple Sitemaps', sm.duplicateAcrossSitemaps.map(d => '<span class="url">'+esc(d.url)+'</span> <span style="color:#888">('+d.sources.length+' sitemaps)</span>'), 'warn');
    }
    // Phase 6.3: Uncrawled sitemap URLs
    if (sm.uncrawledSitemapUrls.length > 0) {
      issuesHtml += issueGroup('Sitemap: Uncrawled URLs (in sitemap, not reached)', sm.uncrawledSitemapUrls.map(u => '<span class="url">'+esc(u)+'</span>'), 'warn');
    }
  }

  // Phase 6: Page weight issues
  if (STATS.pageWeightStats && STATS.pageWeightStats.oversizedPages.length > 0) {
    issuesHtml += issueGroup('Oversized Pages (>3MB)', STATS.pageWeightStats.oversizedPages.map(p => '<span class="url">'+esc(p.url)+'</span> <span class="meta">' + formatBytesJs(p.totalBytes) + '</span>'), 'warn');
  }

  // Phase 7: Content & Indexability issues
  issuesHtml += issueGroup('Non-Indexable Pages', PAGES.filter(p => !p.indexable && p.statusCode === 200).map(p => '<span class="url">'+esc(p.url)+'</span> <span class="meta">'+esc(p.indexabilityReason)+'</span>'), 'warn');
  if (STATS.nearDuplicateGroups && STATS.nearDuplicateGroups.length > 0) {
    issuesHtml += issueGroup('Near-Duplicate Content', STATS.nearDuplicateGroups.map(g => '<span class="meta">~'+g.similarity+'% similar ('+g.urls.length+' pages):</span> ' + g.urls.slice(0,3).map(u => '<span class="url">'+esc(u)+'</span>').join(', ')), 'warn');
  }
  issuesHtml += issueGroup('Suspected Soft 404', PAGES.filter(p => p.isSoft404).map(p => '<span class="url">'+esc(p.url)+'</span>'), 'error');
  issuesHtml += issueGroup('Dead-End Pages (0 outgoing internal links)', STATS.linkAnalysis ? STATS.linkAnalysis.deadEndPages.map(u => '<span class="url">'+esc(u)+'</span>') : [], 'warn');
  var urlIssueKeys = STATS.urlIssueStats ? Object.keys(STATS.urlIssueStats) : [];
  if (urlIssueKeys.length > 0) {
    var urlIssueItems = [];
    for (var ui = 0; ui < urlIssueKeys.length; ui++) {
      var k = urlIssueKeys[ui];
      var urls = STATS.urlIssueStats[k];
      urlIssueItems.push('<span class="meta">'+esc(k.replace(/_/g, ' '))+' ('+urls.length+'):</span> ' + urls.slice(0,2).map(function(u){return '<span class="url">'+esc(u)+'</span>'}).join(', '));
    }
    issuesHtml += issueGroup('URL Issues', urlIssueItems, 'warn');
  }
  if (STATS.textToCodeStats && STATS.textToCodeStats.contentPoor.length > 0) {
    issuesHtml += issueGroup('Content-Poor Pages (text/code ratio <10%)', STATS.textToCodeStats.contentPoor.map(p => '<span class="url">'+esc(p.url)+'</span> <span class="meta">'+p.ratio.toFixed(1)+'%</span>'), 'warn');
  }
  // Phase 9: Schema validation issues
  if (STATS.schemaValidationStats) {
    var svs = STATS.schemaValidationStats;
    if (svs.pagesWithErrors > 0) {
      var schemaItems = svs.topIssues.slice(0, 10).map(function(i) { return '<span class="meta">'+i.count+'x</span> '+esc(i.message); });
      issuesHtml += issueGroup('Schema Validation Errors ('+svs.pagesWithErrors+' pages)', schemaItems, 'error');
    }
    var richKeys = Object.keys(svs.richResultEligible);
    if (richKeys.length > 0) {
      var richItems = richKeys.map(function(k) { return '<span class="meta">'+svs.richResultEligible[k]+' page(s)</span> eligible for <strong>'+esc(k)+'</strong> rich results'; });
      issuesHtml += issueGroup('Rich Result Eligible', richItems, 'ok');
    }
  }

  // Phase 11: GSC issues
  if (STATS.gscStats) {
    const gs = STATS.gscStats;
    if (gs.zombiePages.length > 0) {
      issuesHtml += issueGroup('Zombie Pages (visible in search, 0 clicks)', gs.zombiePages.map(function(u) { return '<span class="url">'+esc(u)+'</span>'; }), 'warn');
    }
  }

  // Phase 11: GA4 issues
  if (STATS.ga4Stats) {
    const g4 = STATS.ga4Stats;
    if (g4.noTrafficPages.length > 0) {
      issuesHtml += issueGroup('No-Traffic Pages (indexed, 0 sessions)', g4.noTrafficPages.map(function(u) { return '<span class="url">'+esc(u)+'</span>'; }), 'warn');
    }
  }

  if (STATS.plausibleStats) {
    const pl = STATS.plausibleStats;
    if (pl.noTrafficPages.length > 0) {
      issuesHtml += issueGroup('No-Traffic Pages — Plausible (indexed, 0 visits)', pl.noTrafficPages.map(function(u) { return '<span class="url">'+esc(u)+'</span>'; }), 'warn');
    }
  }

  // Phase 12: Render comparison issues
  if (STATS.renderCompareStats && STATS.renderCompareStats.criticalDiffs.length > 0) {
    issuesHtml += issueGroup(
      'JS Rendering Differences (pre vs post render)',
      STATS.renderCompareStats.criticalDiffs.map(function(d) {
        return '<span class="url">'+esc(d.url)+'</span> <span class="meta">'+esc(d.field)+': "'+esc(d.original.substring(0,80))+'" &rarr; "'+esc(d.rendered.substring(0,80))+'"</span>';
      }),
      'warn'
    );
  }

  // Phase 13.2: Semantic similarity / cannibalization issues
  if (STATS.embeddingStats && STATS.embeddingStats.cannibalizationGroups.length > 0) {
    var cannItems = STATS.embeddingStats.cannibalizationGroups.map(function(g) {
      return '<span class="meta">'+g.urls.length+' pages (avg '+Math.round(g.similarity * 100)+'% similar):</span> ' +
        g.urls.slice(0, 3).map(function(u) { return '<span class="url">'+esc(u)+'</span>'; }).join(', ') +
        (g.urls.length > 3 ? ' <span class="meta">+' + (g.urls.length - 3) + ' more</span>' : '');
    });
    issuesHtml += issueGroup('Content Cannibalization (semantically similar pages)', cannItems, 'warn');
  }
  if (STATS.embeddingStats && STATS.embeddingStats.similarPairs.length > 0) {
    var simItems = STATS.embeddingStats.similarPairs.slice(0, 30).map(function(p) {
      return '<span class="url">'+esc(p.url1)+'</span> &harr; <span class="url">'+esc(p.url2)+'</span> <span class="meta">'+Math.round(p.similarity * 100)+'% similar</span>';
    });
    issuesHtml += issueGroup(
      'Semantically Similar Pages (' + STATS.embeddingStats.pagesEmbedded + ' pages, ' + esc(STATS.embeddingStats.provider) + '/' + esc(STATS.embeddingStats.model) + ')',
      simItems, 'warn'
    );
  }

  // Phase 14.2: Image accessibility audit issues
  if (STATS.imageAuditStats) {
    var ias = STATS.imageAuditStats;
    if (ias.missingAltAttribute > 0) {
      issuesHtml += issueGroup('Images Missing Alt Attribute ('+ias.missingAltAttribute+')', PAGES.filter(p => p.statusCode === 200).reduce(function(acc, p) {
        var missing = (p.images || []).filter(function(i) { return !i.hasAltAttribute; });
        for (var m = 0; m < missing.length && acc.length < 20; m++) {
          acc.push('<span class="url">'+esc(missing[m].src.substring(0,100))+'</span> <span class="meta">on '+esc(p.url)+'</span>');
        }
        return acc;
      }, []), 'error');
    }
    if (ias.missingDimensions > 0) {
      issuesHtml += issueGroup('Images Missing Width/Height — CLS Risk ('+ias.missingDimensions+')', PAGES.filter(p => p.statusCode === 200).reduce(function(acc, p) {
        var noSize = (p.images || []).filter(function(i) { return i.missingWidth || i.missingHeight; });
        for (var m = 0; m < noSize.length && acc.length < 20; m++) {
          acc.push('<span class="url">'+esc(noSize[m].src.substring(0,100))+'</span> <span class="meta">on '+esc(p.url)+'</span>');
        }
        return acc;
      }, []), 'warn');
    }
    if (ias.altTooLong.length > 0) {
      issuesHtml += issueGroup('Images with Alt Text >100 chars', ias.altTooLong.slice(0,20).map(function(i) { return '<span class="url">'+esc(i.src.substring(0,100))+'</span> <span class="meta">'+i.altLength+' chars on '+esc(i.url)+'</span>'; }), 'warn');
    }
    if (ias.oversizedImages.length > 0) {
      issuesHtml += issueGroup('Oversized Images (>100KB)', ias.oversizedImages.slice(0,20).map(function(i) { return '<span class="url">'+esc(i.src.substring(0,100))+'</span> <span class="meta">'+(i.sizeBytes/1024).toFixed(1)+' KB on '+esc(i.url)+'</span>'; }), 'warn');
    }
  }

  // Link Intelligence issues
  if (STATS.linkIntelligenceStats) {
    var liStats = STATS.linkIntelligenceStats;
    if (liStats.unreachablePagesCount > 0) {
      var unreachableItems = liStats.unreachablePages.map(function(u) { return '<span class="url">'+esc(u)+'</span>'; });
      if (liStats.unreachablePagesCount > liStats.unreachablePages.length) {
        unreachableItems.push('<span class="meta">... and ' + (liStats.unreachablePagesCount - liStats.unreachablePages.length) + ' more</span>');
      }
      issuesHtml += issueGroup('Unreachable from Homepage (' + liStats.unreachablePagesCount + ')', unreachableItems, 'error');
    }
    if (liStats.nearOrphansCount > 0) {
      var orphanItems = liStats.nearOrphans.map(function(no) {
        var depthLabel = no.worstSourceDepth !== null ? 'source depth ' + no.worstSourceDepth : 'unreachable source';
        return '<span class="url">'+esc(no.url)+'</span> <span class="meta">In-Degree '+no.inDegree+', '+depthLabel+'</span>';
      });
      if (liStats.nearOrphansCount > liStats.nearOrphans.length) {
        orphanItems.push('<span class="meta">... and ' + (liStats.nearOrphansCount - liStats.nearOrphans.length) + ' more</span>');
      }
      issuesHtml += issueGroup('Near-Orphan Pages (' + liStats.nearOrphansCount + ')', orphanItems, 'warn');
    }
    var excessiveDilution = liStats.dilutionWarnings.filter(function(d) { return d.warning === 'excessive'; });
    var highDilution = liStats.dilutionWarnings.filter(function(d) { return d.warning === 'high'; });
    if (excessiveDilution.length > 0) {
      issuesHtml += issueGroup('Excessive Outgoing Links (>200)', excessiveDilution.map(function(d) { return '<span class="url">'+esc(d.url)+'</span> <span class="meta">'+d.outDegree+' outgoing links</span>'; }), 'error');
    }
    if (highDilution.length > 0) {
      issuesHtml += issueGroup('High Outgoing Links (>100)', highDilution.map(function(d) { return '<span class="url">'+esc(d.url)+'</span> <span class="meta">'+d.outDegree+' outgoing links</span>'; }), 'warn');
    }
    if (liStats.pagesWithNoContentLinksCount > 0) {
      var noContentItems = liStats.pagesWithNoContentLinks.map(function(u) { return '<span class="url">'+esc(u)+'</span> <span class="meta">all inlinks from nav/footer/sidebar</span>'; });
      if (liStats.pagesWithNoContentLinksCount > liStats.pagesWithNoContentLinks.length) {
        noContentItems.push('<span class="meta">... and ' + (liStats.pagesWithNoContentLinksCount - liStats.pagesWithNoContentLinks.length) + ' more</span>');
      }
      issuesHtml += issueGroup('Pages with 0 Content-Area Inlinks (' + liStats.pagesWithNoContentLinksCount + ')', noContentItems, 'warn');
    }
    // Click depth distribution summary
    var deepPages = [];
    for (var depthKey in liStats.clickDepthDistribution) {
      if (Number(depthKey) > 3) {
        deepPages.push({depth: Number(depthKey), count: liStats.clickDepthDistribution[depthKey]});
      }
    }
    if (deepPages.length > 0) {
      var totalDeep = deepPages.reduce(function(s, d) { return s + d.count; }, 0);
      var deepItems = deepPages.map(function(d) { return '<span class="meta">Depth ' + d.depth + ':</span> ' + d.count + ' pages'; });
      deepItems.unshift('<span class="meta">Total pages >3 clicks from homepage:</span> ' + totalDeep);
      issuesHtml += issueGroup('Deep Pages (>3 clicks from homepage)', deepItems, 'warn');
    }

    // HITS Top Authorities
    if (liStats.topAuthorities && liStats.topAuthorities.length > 0) {
      var authItems = liStats.topAuthorities.map(function(a) {
        return '<span class="meta">' + a.score.toFixed(2) + '</span> <span class="url">' + esc(a.url) + '</span>';
      });
      issuesHtml += issueGroup('Top Authority Pages (HITS)', authItems, 'info');
    }
    // HITS Top Hubs
    if (liStats.topHubs && liStats.topHubs.length > 0) {
      var hubItems = liStats.topHubs.map(function(h) {
        return '<span class="meta">' + h.score.toFixed(2) + '</span> <span class="url">' + esc(h.url) + '</span>';
      });
      issuesHtml += issueGroup('Top Hub Pages (HITS)', hubItems, 'info');
    }
    // Link Suggestions
    if (liStats.linkSuggestions && liStats.linkSuggestionsCount > 0) {
      var suggItems = liStats.linkSuggestions.slice(0, 20).map(function(s) {
        return '<div style="margin-bottom:8px"><span class="meta">Score ' + s.score + '</span> ' +
          '<span class="url">' + esc(s.sourceUrl) + '</span>' +
          ' <span class="meta">&rarr;</span> ' +
          '<span class="url">' + esc(s.targetUrl) + '</span>' +
          '<br><small style="color:#888">' + esc(s.reason) + '</small></div>';
      });
      if (liStats.linkSuggestionsCount > 20) {
        suggItems.push('<span class="meta">... and ' + (liStats.linkSuggestionsCount - 20) + ' more (see link-suggestions CSV)</span>');
      }
      issuesHtml += issueGroup('Internal Linking Suggestions (' + liStats.linkSuggestionsCount + ')', suggItems, 'info');
    }

    // Betweenness Centrality: Bridge Pages
    if (!liStats.centralitySkipped) {
      if (liStats.singlePointOfFailure && liStats.singlePointOfFailure.length > 0) {
        var spofItems = liStats.singlePointOfFailure.map(function(url) {
          return '<span class="url">' + esc(url) + '</span> <span class="meta">Critical connector -- removing this page would disconnect parts of the site</span>';
        });
        issuesHtml += issueGroup('Single Point of Failure Pages', spofItems, 'error');
      }
      if (liStats.topBridges && liStats.topBridges.length > 0) {
        var bridgeItems = liStats.topBridges.map(function(b) {
          return '<span class="meta">' + b.score.toFixed(2) + '</span> <span class="url">' + esc(b.url) + '</span>';
        });
        issuesHtml += issueGroup('Top Bridge Pages (Betweenness Centrality)', bridgeItems, 'info');
      }
      if (liStats.mostConnected && liStats.mostConnected.length > 0) {
        var connItems = liStats.mostConnected.map(function(c) {
          return '<span class="meta">' + c.score.toFixed(2) + '</span> <span class="url">' + esc(c.url) + '</span>';
        });
        issuesHtml += issueGroup('Most Connected Pages (Closeness Centrality)', connItems, 'info');
      }
      if (liStats.mostIsolated && liStats.mostIsolated.length > 0) {
        var isoItems = liStats.mostIsolated.map(function(iso) {
          return '<span class="meta">' + iso.score.toFixed(2) + '</span> <span class="url">' + esc(iso.url) + '</span>';
        });
        issuesHtml += issueGroup('Most Isolated Pages (Closeness Centrality)', isoItems, 'warn');
      }
    }

    // Semantic Link Distance
    if (liStats.semanticLinkAnalysis) {
      var sla = liStats.semanticLinkAnalysis;
      if (sla.weakLinksCount > 0) {
        var weakItems = sla.weakLinks.slice(0, 20).map(function(l) {
          return '<span class="meta">' + (l.similarity * 100).toFixed(1) + '%</span> ' +
            '<span class="url">' + esc(l.source) + '</span>' +
            ' <span class="meta">&rarr;</span> ' +
            '<span class="url">' + esc(l.target) + '</span>';
        });
        if (sla.weakLinksCount > 20) {
          weakItems.push('<span class="meta">... and ' + (sla.weakLinksCount - 20) + ' more</span>');
        }
        issuesHtml += issueGroup('Topically Unrelated Links (' + sla.weakLinksCount + ', avg relevance ' + (sla.avgSemSimilarity * 100).toFixed(1) + '%)', weakItems, 'warn');
      }
      if (sla.strongLinksCount > 0) {
        var strongItems = sla.strongLinks.slice(0, 20).map(function(l) {
          return '<span class="meta">' + (l.similarity * 100).toFixed(1) + '%</span> ' +
            '<span class="url">' + esc(l.source) + '</span>' +
            ' <span class="meta">&rarr;</span> ' +
            '<span class="url">' + esc(l.target) + '</span>';
        });
        if (sla.strongLinksCount > 20) {
          strongItems.push('<span class="meta">... and ' + (sla.strongLinksCount - 20) + ' more</span>');
        }
        issuesHtml += issueGroup('Strong Topical Links (' + sla.strongLinksCount + ')', strongItems, 'info');
      }
    }
  }

  // Redirect chain issue panels
  if (STATS.redirectStats) {
    var rs = STATS.redirectStats;
    if (rs.temporaryRedirects.length > 0) {
      issuesHtml += issueGroup('Temporary Redirects (302/307) — should be 301/308', rs.temporaryRedirects.map(function(r) {
        var chain = r.chain.map(function(h) { return '[' + h.statusCode + '] ' + esc(h.url); }).join(' &rarr; ');
        return '<span class="meta">' + chain + '</span>';
      }), 'warn');
    }
    if (rs.chainLoops.length > 0) {
      issuesHtml += issueGroup('Redirect Loops', rs.chainLoops.map(function(r) {
        var chain = r.chain.map(function(h) { return '[' + h.statusCode + '] ' + esc(h.url); }).join(' &rarr; ');
        return '<span class="url">' + esc(r.url) + '</span> <span class="meta">' + chain + '</span>';
      }), 'error');
    }
    if (rs.selfRedirects.length > 0) {
      issuesHtml += issueGroup('Self-Redirects', rs.selfRedirects.map(function(u) {
        return '<span class="url">' + esc(u) + '</span> <span class="meta">redirects to itself</span>';
      }), 'error');
    }
    if (rs.crossDomain.length > 0) {
      issuesHtml += issueGroup('Cross-Domain Redirects', rs.crossDomain.map(function(r) {
        var chain = r.chain.map(function(h) { return '[' + h.statusCode + '] ' + esc(h.url); }).join(' &rarr; ');
        return '<span class="meta">' + chain + '</span>';
      }), 'info');
    }
  }

  // Canonical tag validation panels
  if (STATS.canonicalStats) {
    var cs = STATS.canonicalStats;
    if (cs.totalWithoutCanonical > 0) {
      issuesHtml += issueGroup('Missing Canonical Tag (' + cs.totalWithoutCanonical + ')', PAGES.filter(function(p) { return p.statusCode === 200 && !p.canonical; }).map(function(p) { return '<span class="url">' + esc(p.url) + '</span>'; }), 'warn');
    }
    if (cs.multipleCanonicals > 0) {
      issuesHtml += issueGroup('Multiple Canonical Tags (' + cs.multipleCanonicals + ')', PAGES.filter(function(p) { return p.statusCode === 200 && p.canonicalCount > 1; }).map(function(p) { return '<span class="url">' + esc(p.url) + '</span> <span class="meta">' + p.canonicalCount + ' tags</span>'; }), 'error');
    }
    if (cs.canonicalLoops.length > 0) {
      issuesHtml += issueGroup('Canonical Loops', cs.canonicalLoops.map(function(cl) { return '<span class="url">' + esc(cl.url) + '</span> <span class="meta">' + cl.loop.map(function(u) { return esc(u); }).join(' &rarr; ') + '</span>'; }), 'error');
    }
    if (cs.canonicalChains.length > 0) {
      issuesHtml += issueGroup('Canonical Chains', cs.canonicalChains.map(function(cc) { return '<span class="meta">' + cc.chain.map(function(u) { return esc(u); }).join(' &rarr; ') + '</span>'; }), 'warn');
    }
    if (cs.canonicalToNon200.length > 0) {
      issuesHtml += issueGroup('Canonical Pointing to Non-200', cs.canonicalToNon200.map(function(cn) { return '<span class="url">' + esc(cn.url) + '</span> <span class="meta">&rarr; [' + cn.targetStatus + '] ' + esc(cn.canonical) + '</span>'; }), 'error');
    }
    if (cs.canonicalToNonIndexable.length > 0) {
      issuesHtml += issueGroup('Canonical Pointing to Non-Indexable', cs.canonicalToNonIndexable.map(function(ci) { return '<span class="url">' + esc(ci.url) + '</span> <span class="meta">&rarr; ' + esc(ci.canonical) + '</span>'; }), 'warn');
    }
    if (cs.crossDomain.length > 0) {
      issuesHtml += issueGroup('Cross-Domain Canonicals', cs.crossDomain.map(function(cd) { return '<span class="url">' + esc(cd.url) + '</span> <span class="meta">&rarr; ' + esc(cd.canonical) + '</span>'; }), 'info');
    }
    if (cs.httpHttpsMismatch.length > 0) {
      issuesHtml += issueGroup('Canonical HTTP/HTTPS Mismatch', cs.httpHttpsMismatch.map(function(hm) { return '<span class="url">' + esc(hm.url) + '</span> <span class="meta">&rarr; ' + esc(hm.canonical) + '</span>'; }), 'error');
    }
    if (cs.relativeCanonicals.length > 0) {
      issuesHtml += issueGroup('Relative Canonical URLs', cs.relativeCanonicals.map(function(rc) { return '<span class="url">' + esc(rc.url) + '</span> <span class="meta">raw: "' + esc(rc.rawHref) + '"</span>'; }), 'warn');
    }
    if (cs.canonicalWithQueryString.length > 0) {
      issuesHtml += issueGroup('Canonicals with Query Strings', cs.canonicalWithQueryString.map(function(qs) { return '<span class="url">' + esc(qs.url) + '</span> <span class="meta">&rarr; ' + esc(qs.canonical) + '</span>'; }), 'warn');
    }
  }

  // CrUX panels
  if (STATS.cruxStats) {
    var crux = STATS.cruxStats;
    if (crux.worstLcp && crux.worstLcp.length > 0) {
      issuesHtml += issueGroup('Slow LCP (Largest Contentful Paint)', crux.worstLcp.filter(function(e) { return e.lcpMs > 2500; }).map(function(e) {
        return '<span class="url">' + esc(e.url) + '</span> <span class="meta">' + (e.lcpMs / 1000).toFixed(2) + 's</span>';
      }), 'warn');
    }
    if (crux.worstInp && crux.worstInp.length > 0) {
      issuesHtml += issueGroup('Slow INP (Interaction to Next Paint)', crux.worstInp.filter(function(e) { return e.inpMs > 200; }).map(function(e) {
        return '<span class="url">' + esc(e.url) + '</span> <span class="meta">' + e.inpMs + 'ms</span>';
      }), 'warn');
    }
    if (crux.worstCls && crux.worstCls.length > 0) {
      issuesHtml += issueGroup('High CLS (Cumulative Layout Shift)', crux.worstCls.filter(function(e) { return e.cls > 0.1; }).map(function(e) {
        return '<span class="url">' + esc(e.url) + '</span> <span class="meta">' + e.cls.toFixed(3) + '</span>';
      }), 'warn');
    }
  }

  if (!issuesHtml) {
    issuesHtml = '<div class="chart-box" style="text-align:center;padding:32px"><h3 style="color:var(--green)">No issues found!</h3></div>';
  }
  issuesEl.innerHTML = issuesHtml;

  // ─── N-Grams ──────────────
  if (STATS.ngramStats) {
    const ng = STATS.ngramStats;
    const ngramsEl = document.getElementById('ngrams-section');
    let ngh = '<h2>N-Gram Analysis</h2><div class="ngrams-grid">';

    function ngramPanel(title, subtitle, entries, maxCount) {
      if (!entries || entries.length === 0) return '';
      var top = entries.slice(0, 20);
      var html = '<div class="ngram-panel"><h3>' + title + ' <span style="font-weight:400;font-size:12px;color:var(--fg2)">' + subtitle + '</span></h3>';
      html += '<table class="ngram-table"><thead><tr><th>Term</th><th>Count</th><th>Pages</th><th>TF-IDF</th></tr></thead><tbody>';
      for (var i = 0; i < top.length; i++) {
        var e = top[i];
        var barW = maxCount > 0 ? Math.max(2, Math.round((e.count / maxCount) * 80)) : 2;
        var barColor = i < 3 ? 'var(--accent)' : i < 10 ? 'var(--green)' : 'var(--fg2)';
        html += '<tr><td class="ngram-term"><span class="ngram-bar" style="width:'+barW+'px;background:'+barColor+'"></span>' + esc(e.term) + '</td>';
        html += '<td>' + e.count + '</td>';
        html += '<td>' + e.pages + '</td>';
        html += '<td>' + e.tfidf.toFixed(4) + '</td></tr>';
      }
      html += '</tbody></table>';
      html += '<div class="ngram-meta">' + entries.length + ' unique terms found</div>';
      html += '</div>';
      return html;
    }

    var uniMax = ng.unigrams.length > 0 ? ng.unigrams[0].count : 1;
    var biMax = ng.bigrams.length > 0 ? ng.bigrams[0].count : 1;
    var triMax = ng.trigrams.length > 0 ? ng.trigrams[0].count : 1;

    ngh += ngramPanel('Unigrams', 'single words', ng.unigrams, uniMax);
    ngh += ngramPanel('Bigrams', 'two-word phrases', ng.bigrams, biMax);
    ngh += ngramPanel('Trigrams', 'three-word phrases', ng.trigrams, triMax);

    ngh += '</div>';
    ngh += '<div style="margin-top:8px;font-size:12px;color:var(--fg2)">Analyzed ' + ng.totalPages + ' pages, ' + ng.totalTokens.toLocaleString() + ' tokens. Stop words filtered. Sorted by frequency.</div>';
    ngramsEl.innerHTML = ngh;
  }

  // ─── Table ─────────────────
  const columns = [
    {key:'statusCode', label:'Status', width:'70px'},
    {key:'url', label:'URL', width:'auto'},
    {key:'title', label:'Title', width:'200px'},
    {key:'wordCount', label:'Words', width:'70px'},
    {key:'responseTimeMs', label:'Time (ms)', width:'90px'},
    {key:'depth', label:'Depth', width:'60px'},
    {key:'internalLinksCount', label:'Int Links', width:'80px'},
    {key:'imagesCount', label:'Images', width:'70px'},
    {key:'pageRank', label:'PageRank', width:'80px'},
  ];
  if (psiPages.length > 0) columns.push({key:'psiScore', label:'PSI', width:'60px'});
  var hasWeight = PAGES.some(p => p.totalWeight !== null);
  if (hasWeight) columns.push({key:'totalWeight', label:'Weight', width:'80px'});
  var hasSitemap = PAGES.some(p => p.inSitemap !== null);
  if (hasSitemap) columns.push({key:'inSitemap', label:'Sitemap', width:'70px'});
  columns.push({key:'indexable', label:'Indexable', width:'75px'});
  var hasReadability = PAGES.some(p => p.readabilityScore !== null);
  if (hasReadability) columns.push({key:'readabilityScore', label:'FK Score', width:'75px'});
  columns.push({key:'textToCodeRatio', label:'Text/Code', width:'80px'});
  var hasGsc = PAGES.some(p => p.gscClicks !== null);
  if (hasGsc) {
    columns.push({key:'gscClicks', label:'Clicks', width:'70px'});
    columns.push({key:'gscImpressions', label:'Impr.', width:'70px'});
    columns.push({key:'gscPosition', label:'Pos.', width:'60px'});
  }
  var hasGa4 = PAGES.some(p => p.ga4Sessions !== null);
  if (hasGa4) {
    columns.push({key:'ga4Sessions', label:'Sessions', width:'80px'});
    columns.push({key:'ga4Pageviews', label:'Views', width:'70px'});
    columns.push({key:'ga4Conversions', label:'Conv.', width:'60px'});
  }
  var hasSegments = PAGES.some(p => p.segments && p.segments.length > 0);
  if (hasSegments) {
    columns.push({key:'segments', label:'Segment', width:'100px'});
  }
  var hasRenderDiffs = PAGES.some(p => p.renderDiffs && p.renderDiffs.length > 0);
  if (hasRenderDiffs) {
    columns.push({key:'renderDiffCount', label:'Render Diffs', width:'90px'});
  }
  columns.push({key:'templateType', label:'Template', width:'90px'});

  const thead = document.getElementById('table-head');
  thead.innerHTML = columns.map((c,i) => '<th style="width:'+c.width+'" data-col="'+i+'">'+c.label+'<span class="sort-arrow"></span></th>').join('');

  let sortCol = -1, sortAsc = true, expandedRow = -1;
  let filtered = [...PAGES];

  function renderTable() {
    const tbody = document.getElementById('table-body');
    let html = '';
    for (let i = 0; i < filtered.length; i++) {
      const p = filtered[i];
      const statusCls = p.statusCode < 300 ? 'status-ok' : p.statusCode < 400 ? 'status-redirect' : 'status-error';
      html += '<tr data-idx="'+i+'">';
      html += '<td class="'+statusCls+'">'+p.statusCode+'</td>';
      html += '<td title="'+esc(p.url)+'">'+esc(p.url)+'</td>';
      html += '<td title="'+esc(p.title)+'">'+esc(p.title)+'</td>';
      html += '<td>'+p.wordCount+'</td>';
      html += '<td>'+p.responseTimeMs+'</td>';
      html += '<td>'+p.depth+'</td>';
      html += '<td>'+p.internalLinksCount+'</td>';
      html += '<td>'+p.imagesCount+'</td>';
      html += '<td>'+p.pageRank.toFixed(2)+'</td>';
      if (psiPages.length > 0) html += '<td>'+(p.psiScore !== null ? p.psiScore : '-')+'</td>';
      if (hasWeight) html += '<td>'+(p.totalWeight !== null ? formatBytesJs(p.totalWeight) : '-')+'</td>';
      if (hasSitemap) html += '<td>'+(p.inSitemap === true ? 'Yes' : p.inSitemap === false ? 'No' : '-')+'</td>';
      html += '<td>'+(p.indexable ? '<span style="color:var(--green)">Yes</span>' : '<span style="color:var(--red)">No</span>')+'</td>';
      if (hasReadability) html += '<td>'+(p.readabilityScore !== null ? p.readabilityScore.toFixed(1) : '-')+'</td>';
      html += '<td>'+p.textToCodeRatio.toFixed(1)+'%</td>';
      if (hasGsc) {
        html += '<td>'+(p.gscClicks !== null ? p.gscClicks : '-')+'</td>';
        html += '<td>'+(p.gscImpressions !== null ? p.gscImpressions : '-')+'</td>';
        html += '<td>'+(p.gscPosition !== null ? p.gscPosition : '-')+'</td>';
      }
      if (hasGa4) {
        html += '<td>'+(p.ga4Sessions !== null ? p.ga4Sessions : '-')+'</td>';
        html += '<td>'+(p.ga4Pageviews !== null ? p.ga4Pageviews : '-')+'</td>';
        html += '<td>'+(p.ga4Conversions !== null ? p.ga4Conversions : '-')+'</td>';
      }
      if (hasSegments) {
        html += '<td>'+(p.segments && p.segments.length > 0 ? esc(p.segments.join(', ')) : '-')+'</td>';
      }
      if (hasRenderDiffs) {
        var rdCount = p.renderDiffs ? p.renderDiffs.length : 0;
        html += '<td>'+(rdCount > 0 ? '<span style="color:var(--yellow)">'+rdCount+'</span>' : '-')+'</td>';
      }
      html += '<td>'+esc(p.templateType || 'other')+'</td>';
      html += '</tr>';
      if (expandedRow === i) {
        html += '<tr class="expand-row"><td colspan="'+columns.length+'">'+renderDetail(p)+'</td></tr>';
      }
    }
    tbody.innerHTML = html;
  }

  function renderDetail(p) {
    let d = '<div class="detail-grid">';
    d += di('Final URL', esc(p.finalUrl));
    d += di('Canonical', esc(p.canonical) || '-');
    d += di('Meta Robots', esc(p.metaRobots) || '-');
    d += di('Title Length', p.titleLength);
    d += di('Description', esc(p.metaDescription) || '-');
    d += di('Desc Length', p.descriptionLength);
    d += di('H1', esc(p.h1.join(', ')) || '-');
    d += di('H2 Count', p.h2Count);
    d += di('External Links', p.externalLinksCount);
    d += di('Images Missing Alt', p.imagesMissingAlt);
    d += di('HTTPS', p.isHttps ? 'Yes' : 'No');
    d += di('Mixed Content', p.hasMixedContent ? 'Yes' : 'No');
    d += di('PageRank', p.pageRank.toFixed(2) + ' / 10');
    if (p.psiScore !== null) d += di('PSI Score', p.psiScore + '/100');
    if (p.totalWeight !== null) d += di('Page Weight', formatBytesJs(p.totalWeight));
    if (p.inSitemap !== null) d += di('In Sitemap', p.inSitemap ? 'Yes' : 'No');
    d += di('Indexable', p.indexable ? 'Yes' : 'No — ' + esc(p.indexabilityReason));
    if (p.readabilityScore !== null) d += di('Readability (FK)', p.readabilityScore.toFixed(1) + '/100');
    d += di('Text/Code Ratio', p.textToCodeRatio.toFixed(1) + '%');
    if (p.urlIssues && p.urlIssues.length > 0) d += di('URL Issues', esc(p.urlIssues.join(', ')));
    if (p.isSoft404) d += di('Soft 404', '<span style="color:var(--red)">Suspected</span>');
    if (p.aiAnalysis) d += '<div class="detail-item" style="grid-column:1/-1"><div class="dl">AI Analysis</div><div class="dv" style="white-space:pre-wrap">'+esc(p.aiAnalysis)+'</div></div>';
    if (p.gscClicks !== null) {
      d += di('GSC Clicks', p.gscClicks);
      d += di('GSC Impressions', p.gscImpressions);
      d += di('GSC CTR', p.gscCtr !== null ? (p.gscCtr * 100).toFixed(1) + '%' : '-');
      d += di('GSC Avg Position', p.gscPosition !== null ? p.gscPosition : '-');
    }
    if (p.ga4Sessions !== null) {
      d += di('GA4 Sessions', p.ga4Sessions);
      d += di('GA4 Pageviews', p.ga4Pageviews);
      d += di('GA4 Bounce Rate', p.ga4BounceRate !== null ? (p.ga4BounceRate * 100).toFixed(1) + '%' : '-');
      d += di('GA4 Conversions', p.ga4Conversions);
    }
    if (p.segments && p.segments.length > 0) {
      d += di('Segments', esc(p.segments.join(', ')));
    }
    if (p.renderDiffs && p.renderDiffs.length > 0) {
      var rdHtml = '<table style="width:100%;font-size:12px;border-collapse:collapse;margin-top:4px">';
      rdHtml += '<tr><th style="text-align:left;padding:2px 6px;border-bottom:1px solid var(--border)">Field</th><th style="text-align:left;padding:2px 6px;border-bottom:1px solid var(--border)">Original</th><th style="text-align:left;padding:2px 6px;border-bottom:1px solid var(--border)">Rendered</th></tr>';
      for (var rdi = 0; rdi < p.renderDiffs.length; rdi++) {
        var rd = p.renderDiffs[rdi];
        rdHtml += '<tr><td style="padding:2px 6px;border-bottom:1px solid var(--border);font-weight:600">'+esc(rd.field)+'</td>';
        rdHtml += '<td style="padding:2px 6px;border-bottom:1px solid var(--border);color:var(--red)">'+esc(rd.original.substring(0,120) || '(empty)')+'</td>';
        rdHtml += '<td style="padding:2px 6px;border-bottom:1px solid var(--border);color:var(--green)">'+esc(rd.rendered.substring(0,120) || '(empty)')+'</td></tr>';
      }
      rdHtml += '</table>';
      d += '<div class="detail-item" style="grid-column:1/-1"><div class="dl">Render Differences ('+p.renderDiffs.length+')</div><div class="dv">'+rdHtml+'</div></div>';
    }
    if (p.redirectChainLength > 0) {
      var chainHtml = p.redirectChain.map(function(h) {
        var cls = h.statusCode === 301 || h.statusCode === 308 ? 'chain-hop s301' : h.statusCode === 302 || h.statusCode === 307 ? 'chain-hop s302' : 'chain-hop s3xx';
        return '<span class="' + cls + '" style="padding:2px 6px;border-radius:3px;font-size:12px">[' + h.statusCode + '] ' + esc(h.url) + '</span>';
      }).join(' <span style="color:var(--fg2)">&rarr;</span> ');
      chainHtml += ' <span style="color:var(--fg2)">&rarr;</span> <span class="chain-hop final" style="padding:2px 6px;border-radius:3px;font-size:12px">' + esc(p.finalUrl) + '</span>';
      d += '<div class="detail-item" style="grid-column:1/-1"><div class="dl">Redirect Chain (' + p.redirectChainLength + ' hop' + (p.redirectChainLength > 1 ? 's' : '') + ')</div><div class="dv">' + chainHtml + '</div></div>';
    }
    d += di('Template Type', esc(p.templateType || 'other'));
    if (p.error) d += di('Error', esc(p.error));
    d += '</div>';
    return d;
  }
  function di(label, value) { return '<div class="detail-item"><div class="dl">'+esc(label)+'</div><div class="dv">'+value+'</div></div>'; }

  // #5 fix: Extract sort logic for reuse
  function applySort() {
    if (sortCol < 0) return;
    const key = columns[sortCol].key;
    filtered.sort((a,b) => {
      let va = a[key], vb = b[key];
      if (va === null) va = -1;
      if (vb === null) vb = -1;
      if (typeof va === 'string') return sortAsc ? va.localeCompare(vb) : vb.localeCompare(va);
      return sortAsc ? va - vb : vb - va;
    });
  }

  // Sort
  thead.addEventListener('click', (e) => {
    const th = e.target.closest('th');
    if (!th) return;
    const col = +th.dataset.col;
    if (sortCol === col) { sortAsc = !sortAsc; } else { sortCol = col; sortAsc = true; }
    applySort();
    document.querySelectorAll('.sort-arrow').forEach(s => s.textContent = '');
    th.querySelector('.sort-arrow').textContent = sortAsc ? ' \\u25B2' : ' \\u25BC';
    expandedRow = -1;
    renderTable();
  });

  // Click to expand
  document.getElementById('table-body').addEventListener('click', (e) => {
    const tr = e.target.closest('tr[data-idx]');
    if (!tr) return;
    const idx = +tr.dataset.idx;
    expandedRow = expandedRow === idx ? -1 : idx;
    renderTable();
  });

  // Search (preserves current sort order)
  document.getElementById('search').addEventListener('input', (e) => {
    const q = e.target.value.toLowerCase();
    filtered = PAGES.filter(p => p.url.toLowerCase().includes(q) || p.title.toLowerCase().includes(q));
    applySort();
    expandedRow = -1;
    renderTable();
  });

  renderTable();

  // Issue toggle via event delegation
  document.getElementById('issues').addEventListener('click', function(e) {
    var header = e.target.closest('.issue-header');
    if (header) header.nextElementSibling.classList.toggle('open');
  });

  // ── Phase 10: Visualizations ─────────────────────────────

  // Viz tab switching
  document.querySelectorAll('.viz-tab').forEach(tab => {
    tab.addEventListener('click', () => {
      document.querySelectorAll('.viz-tab').forEach(t => t.classList.remove('active'));
      document.querySelectorAll('.viz-panel').forEach(p => p.classList.remove('active'));
      tab.classList.add('active');
      document.getElementById('viz-' + tab.dataset.viz).classList.add('active');
    });
  });

  // ── 10.1 Force-Directed Graph ──────────────────
  let graphRendered = false;
  const depthColors = ['#4361ee', '#2ec4b6', '#f5a623', '#e63946', '#9b59b6', '#1abc9c', '#e67e22', '#e74c3c', '#3498db', '#2ecc71'];

  function renderGraph() {
    if (typeof d3 === 'undefined') {
      document.getElementById('graph-container').innerHTML = '<p style="padding:24px;text-align:center;color:var(--fg2)">D3.js could not be loaded (offline?). Site graph requires an internet connection.</p>';
      graphRendered = true;
      return;
    }
    if (PAGES.length > 2000) {
      document.getElementById('graph-container').innerHTML = '<p style="padding:24px;text-align:center;color:var(--fg2)">Site graph disabled for crawls over 2,000 pages for browser performance. This crawl has ' + PAGES.length + ' pages.</p>';
      graphRendered = true;
      return;
    }
    graphRendered = true;

    const container = document.getElementById('graph-container');
    const width = container.clientWidth;
    const height = container.clientHeight;

    // Build node/edge data from crawled pages
    const urlSet = new Set(PAGES.map(p => p.url));
    const nodes = PAGES.map(p => ({ id: p.url, depth: p.depth, pr: p.pageRank, status: p.statusCode, title: p.title }));
    const links = [];
    const linkSet = new Set();
    PAGES.forEach(p => {
      if (!p.linkTargets) return;
      p.linkTargets.forEach(target => {
        if (urlSet.has(target) && target !== p.url) {
          const key = p.url + '>' + target;
          if (!linkSet.has(key)) {
            linkSet.add(key);
            links.push({ source: p.url, target: target });
          }
        }
      });
    });

    const svg = d3.select(container).append('svg')
      .attr('width', width).attr('height', height);

    // Tooltip
    const tooltip = d3.select(container).append('div')
      .attr('class', 'graph-tooltip').style('display', 'none');

    // Zoom
    const g = svg.append('g');
    svg.call(d3.zoom().scaleExtent([0.1, 4]).on('zoom', event => {
      g.attr('transform', event.transform);
    }));

    // Simulation
    const sim = d3.forceSimulation(nodes)
      .force('link', d3.forceLink(links).id(d => d.id).distance(60))
      .force('charge', d3.forceManyBody().strength(-120))
      .force('center', d3.forceCenter(width / 2, height / 2))
      .force('collision', d3.forceCollide().radius(12));

    // Links
    const linkEls = g.append('g').attr('stroke', '#999').attr('stroke-opacity', 0.3)
      .selectAll('line').data(links).join('line').attr('stroke-width', 0.8);

    // Nodes
    const nodeEls = g.append('g').selectAll('circle').data(nodes).join('circle')
      .attr('r', d => Math.max(4, Math.min(14, d.pr * 1.2)))
      .attr('fill', d => depthColors[d.depth % depthColors.length])
      .attr('stroke', '#fff').attr('stroke-width', 1)
      .style('cursor', 'pointer')
      .call(d3.drag()
        .on('start', (event, d) => { if (!event.active) sim.alphaTarget(0.3).restart(); d.fx = d.x; d.fy = d.y; })
        .on('drag', (event, d) => { d.fx = event.x; d.fy = event.y; })
        .on('end', (event, d) => { if (!event.active) sim.alphaTarget(0); d.fx = null; d.fy = null; })
      );

    nodeEls.on('mouseover', (event, d) => {
      let path;
      try { path = new URL(d.id).pathname; } catch(e) { path = d.id; }
      tooltip.style('display', 'block')
        .html('<strong>' + esc(path) + '</strong><br>Status: ' + d.status + ' | PR: ' + d.pr.toFixed(1) + ' | Depth: ' + d.depth + (d.title ? '<br>' + esc(d.title.substring(0, 60)) : ''));
    }).on('mousemove', event => {
      const rect = container.getBoundingClientRect();
      tooltip.style('left', (event.clientX - rect.left + 12) + 'px').style('top', (event.clientY - rect.top - 10) + 'px');
    }).on('mouseout', () => { tooltip.style('display', 'none'); });

    sim.on('tick', () => {
      linkEls.attr('x1', d => d.source.x).attr('y1', d => d.source.y)
        .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
      nodeEls.attr('cx', d => d.x).attr('cy', d => d.y);
    });

    // Legend
    const graphMaxDepth = d3.max(nodes, d => d.depth) || 0;
    const legend = d3.select(container).append('div')
      .style('position', 'absolute').style('top', '8px').style('right', '8px')
      .style('background', 'var(--bg2)').style('border', '1px solid var(--border)')
      .style('border-radius', '6px').style('padding', '8px').style('font-size', '11px');
    for (let ld = 0; ld <= Math.min(graphMaxDepth, 5); ld++) {
      legend.append('div').style('display', 'flex').style('align-items', 'center').style('gap', '4px').style('margin-bottom', '2px')
        .html('<span style="width:10px;height:10px;border-radius:50%;background:' + depthColors[ld] + ';display:inline-block"></span> Depth ' + ld);
    }
  }
  // Render graph immediately if tab is active
  if (PAGES.length <= 2000) renderGraph();

  // ── 10.2 Directory Tree ──────────────────────────
  (() => {
    // Build tree from URL paths
    const tree = {};
    PAGES.forEach(p => {
      try {
        const u = new URL(p.url);
        const parts = u.pathname.split('/').filter(Boolean);
        let node = tree;
        for (let i = 0; i < parts.length; i++) {
          if (!node[parts[i]]) node[parts[i]] = { _pages: [], _children: {} };
          if (i === parts.length - 1) node[parts[i]]._pages.push(p);
          node = node[parts[i]]._children;
        }
        if (parts.length === 0) {
          if (!tree._root) tree._root = { _pages: [], _children: {} };
          tree._root._pages.push(p);
        }
      } catch(e) {}
    });

    // Compute total page counts bottom-up (single pass, avoids O(n^2))
    function computeCounts(data) {
      let count = (data._pages || []).length;
      const children = data._children || {};
      Object.keys(children).forEach(k => { count += computeCounts(children[k]); });
      data._totalPages = count;
      return count;
    }

    function renderTreeNode(name, data, depth) {
      const pages = data._pages || [];
      const children = data._children || {};
      const childKeys = Object.keys(children).sort();
      const totalPages = data._totalPages || pages.length;

      const hasChildren = childKeys.length > 0;
      let html = '<div class="tree-node">';
      html += '<span class="tree-toggle">' + (hasChildren ? '\\u25BC' : '\\u00B7') + '</span>';
      html += '<span class="tree-label">' + (name === '_root' ? '/' : esc(name + '/')) + '</span>';
      html += '<span class="tree-count">' + totalPages + ' page' + (totalPages !== 1 ? 's' : '') + '</span>';
      if (pages.length > 0) {
        const statusCounts = {};
        pages.forEach(p => { statusCounts[p.statusCode] = (statusCounts[p.statusCode] || 0) + 1; });
        Object.keys(statusCounts).forEach(s => {
          const color = +s < 300 ? 'var(--green)' : +s < 400 ? 'var(--yellow)' : 'var(--red)';
          html += ' <span style="font-size:11px;color:' + color + '">' + s + ':' + statusCounts[s] + '</span>';
        });
      }
      html += '</div>';
      if (hasChildren) {
        html += '<div class="tree-children">';
        childKeys.forEach(k => { html += renderTreeNode(k, children[k], depth + 1); });
        html += '</div>';
      }
      return html;
    }

    // Pre-compute page counts for all nodes (single bottom-up pass)
    const topKeys = Object.keys(tree).sort();
    topKeys.forEach(k => { computeCounts(tree[k]); });

    let treeHtml = '';
    topKeys.forEach(k => { treeHtml += renderTreeNode(k, tree[k], 0); });
    if (!treeHtml) treeHtml = '<p style="color:var(--fg2);text-align:center;padding:16px">No pages to display</p>';
    document.getElementById('tree-container').innerHTML = treeHtml;

    // Toggle collapsed/expanded
    document.getElementById('tree-container').addEventListener('click', e => {
      const node = e.target.closest('.tree-node');
      if (!node) return;
      const children = node.nextElementSibling;
      if (children && children.classList.contains('tree-children')) {
        children.classList.toggle('collapsed');
        const toggle = node.querySelector('.tree-toggle');
        toggle.textContent = children.classList.contains('collapsed') ? '\\u25B6' : '\\u25BC';
      }
    });
  })();

  // ── Redirect Chains Visualization ──────────────────────
  (() => {
    const container = document.getElementById('redirects-container');
    const redirectPages = PAGES.filter(p => p.redirectChainLength > 0);

    if (redirectPages.length === 0) {
      container.innerHTML = '<p style="color:var(--fg2);text-align:center;padding:24px">No redirect chains detected in this crawl.</p>';
      return;
    }

    // Classify each chain
    function classifyChain(page) {
      var chain = page.redirectChain;
      var types = [];
      var hasTemp = chain.some(function(h) { return h.statusCode === 302 || h.statusCode === 307; });
      var hasPerm = chain.some(function(h) { return h.statusCode === 301 || h.statusCode === 308; });
      if (hasTemp && hasPerm) types.push('mixed');
      else if (hasTemp) types.push('temporary');
      else if (hasPerm) types.push('permanent');

      try {
        var firstUrl = new URL(chain[0].url);
        var finalUrl = new URL(page.finalUrl);
        if (firstUrl.protocol === 'http:' && finalUrl.protocol === 'https:') types.push('http_to_https');
        var fHost = firstUrl.hostname.replace(/^www\\./, '');
        var lHost = finalUrl.hostname.replace(/^www\\./, '');
        if (fHost === lHost && firstUrl.hostname !== finalUrl.hostname) types.push('www');
        if (firstUrl.hostname === finalUrl.hostname && firstUrl.protocol === finalUrl.protocol) {
          var fp = firstUrl.pathname, lp = finalUrl.pathname;
          if ((fp === lp + '/' || fp + '/' === lp) && firstUrl.search === finalUrl.search) types.push('trailing_slash');
        }
        if (fHost !== lHost) types.push('cross_domain');
      } catch(e) {}
      if (types.length === 0) types.push('other');
      return types;
    }

    // Build chain data with classifications
    var chainData = redirectPages.map(function(p) {
      return { page: p, types: classifyChain(p), hops: p.redirectChainLength };
    }).sort(function(a, b) { return b.hops - a.hops; });

    // Summary stats
    var totalChains = chainData.length;
    var totalHops = chainData.reduce(function(s, c) { return s + c.hops; }, 0);
    var maxHops = chainData.length > 0 ? chainData[0].hops : 0;
    var tempCount = chainData.filter(function(c) { return c.types.indexOf('temporary') >= 0 || c.types.indexOf('mixed') >= 0; }).length;

    // Count by status
    var statusCounts = {};
    for (var ci = 0; ci < chainData.length; ci++) {
      for (var hi = 0; hi < chainData[ci].page.redirectChain.length; hi++) {
        var sc = chainData[ci].page.redirectChain[hi].statusCode;
        statusCounts[sc] = (statusCounts[sc] || 0) + 1;
      }
    }

    var html = '';

    // Summary cards
    html += '<div class="redirect-summary">';
    html += '<div class="redirect-stat"><div class="rs-val">' + totalChains + '</div><div class="rs-lbl">Chains</div></div>';
    html += '<div class="redirect-stat"><div class="rs-val">' + totalHops + '</div><div class="rs-lbl">Total Hops</div></div>';
    html += '<div class="redirect-stat"><div class="rs-val">' + maxHops + '</div><div class="rs-lbl">Max Hops</div></div>';
    var scKeys = Object.keys(statusCounts).sort();
    for (var ski = 0; ski < scKeys.length; ski++) {
      var scLabel = scKeys[ski] === '301' ? '301 Permanent' : scKeys[ski] === '302' ? '302 Temporary' : scKeys[ski] === '307' ? '307 Temp' : scKeys[ski] === '308' ? '308 Permanent' : scKeys[ski];
      html += '<div class="redirect-stat"><div class="rs-val">' + statusCounts[scKeys[ski]] + '</div><div class="rs-lbl">' + scLabel + '</div></div>';
    }
    if (tempCount > 0) {
      html += '<div class="redirect-stat" style="border-color:var(--yellow)"><div class="rs-val" style="color:var(--yellow)">' + tempCount + '</div><div class="rs-lbl">Temporary</div></div>';
    }
    html += '</div>';

    // Filters
    html += '<div class="redirect-filters">';
    html += '<label style="font-size:13px;color:var(--fg2)">Filter:</label>';
    html += '<select id="redirect-type-filter"><option value="all">All types</option><option value="permanent">Permanent (301/308)</option><option value="temporary">Temporary (302/307)</option><option value="mixed">Mixed</option><option value="http_to_https">HTTP to HTTPS</option><option value="www">www normalization</option><option value="trailing_slash">Trailing slash</option><option value="cross_domain">Cross-domain</option></select>';
    html += '<label style="font-size:13px;color:var(--fg2)">Min hops:</label>';
    html += '<input id="redirect-min-hops" type="number" value="1" min="1" max="' + maxHops + '" style="width:60px">';
    html += '<label style="font-size:13px;color:var(--fg2)">Sort:</label>';
    html += '<select id="redirect-sort"><option value="hops_desc">Longest first</option><option value="hops_asc">Shortest first</option></select>';
    html += '<span id="redirect-count" style="font-size:12px;color:var(--fg2);margin-left:auto"></span>';
    html += '</div>';

    html += '<div id="redirect-list"></div>';

    container.innerHTML = html;

    function renderChainHop(hop) {
      var cls = 'chain-hop s' + hop.statusCode;
      if (hop.statusCode !== 301 && hop.statusCode !== 302 && hop.statusCode !== 307 && hop.statusCode !== 308) cls = 'chain-hop s3xx';
      var path = hop.url;
      try { path = new URL(hop.url).pathname + new URL(hop.url).search; } catch(e) {}
      return '<span class="' + cls + '" title="' + esc(hop.url) + '">[' + hop.statusCode + '] ' + esc(path) + '</span>';
    }

    function renderChains(filtered) {
      var listEl = document.getElementById('redirect-list');
      var countEl = document.getElementById('redirect-count');
      countEl.textContent = filtered.length + ' of ' + totalChains + ' chains';

      if (filtered.length === 0) {
        listEl.innerHTML = '<p style="text-align:center;color:var(--fg2);padding:16px">No chains match the filter.</p>';
        return;
      }

      var items = '';
      var limit = Math.min(filtered.length, 100);
      for (var i = 0; i < limit; i++) {
        var c = filtered[i];
        var flow = '';
        for (var j = 0; j < c.page.redirectChain.length; j++) {
          flow += renderChainHop(c.page.redirectChain[j]);
          flow += '<span class="chain-arrow">\\u2192</span>';
        }
        var finalPath = c.page.finalUrl;
        try { finalPath = new URL(c.page.finalUrl).pathname + new URL(c.page.finalUrl).search; } catch(e) {}
        flow += '<span class="chain-hop final" title="' + esc(c.page.finalUrl) + '">' + esc(finalPath) + '</span>';

        items += '<div class="chain-item">';
        items += '<div class="chain-meta">' + c.hops + ' hop' + (c.hops > 1 ? 's' : '') + ' &mdash; ' + esc(c.types.join(', ')) + '</div>';
        items += '<div class="chain-flow">' + flow + '</div>';
        items += '</div>';
      }
      if (filtered.length > 100) {
        items += '<p style="text-align:center;color:var(--fg2);padding:8px">Showing 100 of ' + filtered.length + ' chains</p>';
      }
      listEl.innerHTML = items;
    }

    function applyFilters() {
      var typeFilter = document.getElementById('redirect-type-filter').value;
      var minHops = parseInt(document.getElementById('redirect-min-hops').value, 10) || 1;
      var sortMode = document.getElementById('redirect-sort').value;

      var filtered = chainData.filter(function(c) {
        if (c.hops < minHops) return false;
        if (typeFilter !== 'all' && c.types.indexOf(typeFilter) < 0) return false;
        return true;
      });

      if (sortMode === 'hops_asc') {
        filtered.sort(function(a, b) { return a.hops - b.hops; });
      } else {
        filtered.sort(function(a, b) { return b.hops - a.hops; });
      }

      renderChains(filtered);
    }

    document.getElementById('redirect-type-filter').addEventListener('change', applyFilters);
    document.getElementById('redirect-min-hops').addEventListener('input', applyFilters);
    document.getElementById('redirect-sort').addEventListener('change', applyFilters);

    applyFilters();
  })();

})();
`;
