<script lang="ts">
  let { job, stats, pageCount, unavailable = false }: {
    job: Record<string, unknown> | null; stats: Record<string, unknown>; pageCount: number; unavailable?: boolean;
  } = $props();
  type Reason = { reason: string; rule?: string; count: number; examples: string[] };
  let coverage = $derived(job?.coverage as { acceptedUrls: number; pendingUrls: number; final: boolean; reasons: Reason[] } | undefined);
  let reasons = $derived(coverage?.reasons || []);
  let excluded = $derived(reasons.reduce((n, r) => n + r.count, 0));
  let maxExcluded = $derived(Math.max(...reasons.map(r => r.count), 1));
  const labels: Record<string,string> = {depth:'Maximum depth',page_limit:'Page limit',domain:'Outside allowed domains',include_filter:'No include rule matched',exclude_filter:'Exclude rule matched',robots:'Blocked by robots.txt',path_budget:'Path budget',invalid_url:'Invalid URL'};
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
    { label: 'Robots blocked', count: coverage?.final ? reasons.filter(r => r.reason === 'robots').reduce((n,r) => n+r.count,0) : number(stats.robotsBlockedCount), note: 'Recorded blocks only; not all excluded URLs', color: '#e5b66e' },
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
      <p class="chart-note">{coverage ? 'Exclusion decisions are saved for the latest execution. Resumed executions do not include earlier decisions. Whole-site coverage is unknown.' : 'Exclusion reasons were not recorded for this crawl. Run a new crawl to collect them. Whole-site coverage is unknown.'}</p>
      {#if unavailable}<p class="chart-note" role="status">Could not load crawl settings. Results are still available.</p>{/if}
    </div>
  </div>
  <div class="exclusions">
    <div class="chart-panel-heading"><div><h3>Saved exclusion reasons</h3><p>{coverage ? `${excluded.toLocaleString()} rejected attempts · ${coverage.final ? 'final snapshot' : 'partial checkpoint'}` : 'Not recorded'}</p></div></div>
    {#if coverage}
      <p class="chart-note mb-4">Attempts, not unique URLs. The first failing check is recorded; repeated discoveries can count again. Already queued duplicates are omitted. Up to 5 example URLs per reason and rule.</p>
      <dl class="mb-4"><div><dt>URLs admitted to the queue</dt><dd>{coverage.acceptedUrls.toLocaleString()}</dd></div><div><dt>Still queued at snapshot</dt><dd>{coverage.pendingUrls.toLocaleString()}</dd></div></dl>
      {#each reasons as reason}
        <details class="exclusion-row">
          <summary><span>{labels[reason.reason] || reason.reason}{#if reason.rule}<small>{reason.rule}</small>{/if}</span><span class="reason-bar" aria-hidden="true"><i style:width={`${reason.count/maxExcluded*100}%`}></i></span><strong>{reason.count.toLocaleString()}</strong></summary>
          <div class="examples">{#each reason.examples as url}<p>{url}</p>{/each}</div>
        </details>
      {:else}<p class="chart-note">No exclusion decisions in this {coverage.final ? 'execution' : 'checkpoint'}.</p>{/each}
    {:else}<p class="chart-note">Older crawls cannot reconstruct discarded URLs from their saved pages.</p>{/if}
  </div>
</section>
<style>
  .exclusions {margin-top:24px;border-top:1px solid var(--color-border);padding-top:20px;}
  h3 {font-size:14px;font-weight:500;}
  .exclusion-row {border-top:1px solid var(--color-border);padding:12px 0;font-size:12px;}
  summary {display:grid;grid-template-columns:minmax(0,1fr) 30% 45px;align-items:center;gap:16px;cursor:pointer;}
  summary strong {text-align:right;}
  .reason-bar {height:8px;background:var(--color-surface-3);border-radius:4px;overflow:hidden;}
  .reason-bar i {display:block;height:100%;background:#e5b66e;border-radius:4px;}
  summary small {display:block;color:var(--color-fg-2);overflow-wrap:anywhere;margin-top:4px;}
  .examples p {overflow-wrap:anywhere;color:var(--color-fg-2);margin:8px 0;}
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
