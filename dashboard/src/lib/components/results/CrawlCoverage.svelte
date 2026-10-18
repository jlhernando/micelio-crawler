<script lang="ts">
  let { job, stats, pageCount, unavailable = false }: {
    job: Record<string, unknown> | null; stats: Record<string, unknown>; pageCount: number; unavailable?: boolean;
  } = $props();
  let config = $derived((job?.config || {}) as Record<string, unknown>);
  const number = (v: unknown): number | null => typeof v === 'number' && Number.isFinite(v) && v >= 0 ? v : null;
  let limit = $derived(number(config.maxPages ?? config.limit));
  let depth = $derived(number(config.maxDepth ?? config.depth));
  let limitReached = $derived(limit !== null && limit > 0 && pageCount >= limit);
  let codes = $derived(stats.statusCodes as Record<string, number> | undefined);
  let errors = $derived(codes ? Object.entries(codes).reduce((n, [code, count]) => n + (Number(code) >= 400 && Number(code) < 600 ? count : 0), 0) : null);
  let sitemap = $derived(stats.sitemapStats as { totalSitemapUrls?: number; uncrawledSitemapUrls?: string[] } | null);
  let rows = $derived([
    { label: 'Pages recorded', count: pageCount, note: 'Stored in this crawl', color: '#75a6ff' },
    { label: 'HTTP errors', count: errors, note: 'Recorded 4xx and 5xx responses', color: '#f58f98' },
    { label: 'Robots blocked', count: number(stats.robotsBlockedCount), note: 'Recorded blocks only; not all excluded URLs', color: '#e5b66e' },
  ]);
  let max = $derived(Math.max(pageCount, ...rows.map(r => r.count ?? 0), 1));
</script>
<section class="chart-panel" aria-label="Crawl coverage">
  <div class="chart-panel-heading"><div><h2>Crawl coverage</h2><p>What this crawl can tell you about the site</p></div><span class="scope-badge">{limitReached ? 'Page limit reached' : job?.status === 'completed' ? 'Crawl finished' : job?.status || 'Scope unavailable'}</span></div>
  <div class="coverage-grid">
    <div class="space-y-4">
      {#each rows as row}<div><div class="flex justify-between gap-3 text-sm"><span>{row.label}</span><strong>{row.count === null ? 'Not recorded' : row.count.toLocaleString()}</strong></div><div class="track"><i style:width={`${(row.count ?? 0) / max * 100}%`} style:background={row.color}></i></div><p class="chart-note">{row.note}</p></div>{/each}
      <p class="chart-note">Counts can overlap. These bars do not measure whole-site coverage.</p>
    </div>
    <div class="scope-detail">
      <dl>
        <div><dt>Mode</dt><dd>{config.mode || job?.mode || 'Unknown'}</dd></div>
        <div><dt>Page limit</dt><dd>{limit === null ? 'Unknown' : limit === 0 ? 'Default not recorded' : limit.toLocaleString()}</dd></div>
        <div><dt>Maximum depth</dt><dd>{depth === null ? 'Unknown' : depth === 0 ? 'Default not recorded' : depth}</dd></div>
        <div><dt>Respect robots.txt</dt><dd>{typeof config.respectRobots === 'boolean' ? config.respectRobots ? 'Yes' : 'No' : 'Unknown'}</dd></div>
        <div><dt>Sitemap URLs not crawled</dt><dd>{sitemap && Array.isArray(sitemap.uncrawledSitemapUrls) ? sitemap.uncrawledSitemapUrls.length.toLocaleString() : 'Not measured'}</dd></div>
      </dl>
      <p class="scope-note">{limitReached ? 'The stored page count reached the configured limit. More URLs may remain outside this crawl.' : 'Finishing a crawl does not prove that the entire site was visited.'}</p>
      <p class="chart-note">Discovery totals and exclusions by depth or filters are not saved for completed crawls. Whole-site coverage is unknown.</p>
      {#if unavailable}<p class="chart-note" role="status">Could not load crawl settings. Results are still available.</p>{/if}
    </div>
  </div>
</section>
<style>
  .coverage-grid { display:grid; grid-template-columns:1fr 1fr; gap:32px; }
  .scope-badge { border:1px solid var(--color-border); border-radius:6px; padding:5px 9px; font-size:11px; color:#e5b66e; }
  .chart-panel-heading {flex-wrap:wrap;}
  .track { height:10px; background:var(--color-surface-3); border-radius:4px; overflow:hidden; margin:8px 0; }
  .track i {display:block;height:100%;border-radius:4px;}
  dl div {display:flex;justify-content:space-between;gap:16px;font-size:12px;padding:7px 0;border-bottom:1px solid var(--color-border);}
  dt {color:var(--color-fg-2);} dd {text-align:right;}
  .scope-note {font-size:13px;line-height:1.6;margin:16px 0 8px;}
  @media(max-width:700px){.coverage-grid{grid-template-columns:1fr;gap:20px;}}
</style>
