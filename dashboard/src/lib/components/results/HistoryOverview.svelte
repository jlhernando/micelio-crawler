<script lang="ts">
  import { C } from '../../palette';
  let { crawls }: { crawls: { id: string; seedUrl: string; status: string; pageCount: number; startedAt: string }[] } = $props();
  let domain = $state('');
  let chartWidth = $state(650);
  let viewportWidth = $derived(Math.max(chartWidth, 1));
  let plotEnd = $derived(Math.max(viewportWidth - 20, 61));
  function host(url: string) { try { return new URL(url).hostname; } catch { return url; } }
  let domains = $derived([...new Set(crawls.map(c => host(c.seedUrl)))].sort());
  let selectedDomain = $derived(domains.includes(domain) ? domain : domains[0] || '');
  let series = $derived(crawls.filter(c => host(c.seedUrl) === selectedDomain && c.status === 'completed').sort((a,b) => new Date(a.startedAt).getTime() - new Date(b.startedAt).getTime()).slice(-8));
  let max = $derived(Math.max(...series.map(c => c.pageCount), 1));
  let points = $derived(series.map((c, i) => `${x(i)},${220 - c.pageCount / max * 185}`).join(' '));
  function x(i: number) { return series.length > 1 ? 60 + i * (plotEnd - 60) / (series.length - 1) : (60 + plotEnd) / 2; }
  function date(value: string) { return new Date(value).toLocaleDateString('en-GB', { day: '2-digit', month: 'short' }); }
</script>
<dl class="metric-strip">
  <div><dt>Saved crawls</dt><dd>{crawls.length.toLocaleString()}</dd><small>All sites</small></div>
  <div><dt>Completed</dt><dd>{crawls.filter(c => c.status === 'completed').length.toLocaleString()}</dd><small>Ready to explore</small></div>
  <div><dt>Active</dt><dd>{crawls.filter(c => ['running', 'paused'].includes(c.status)).length.toLocaleString()}</dd><small>Running or paused</small></div>
  <div><dt>Sites</dt><dd>{domains.length.toLocaleString()}</dd><small>Distinct hostnames</small></div>
</dl>
<section class="chart-panel mb-6">
  <div class="chart-panel-heading"><div><h2>Crawl coverage over time</h2><p>Pages in the latest completed crawls for one site</p></div><label class="text-xs"><span class="sr-only">Site for coverage chart</span><select class="bg-surface border border-border rounded-md px-3 py-2 max-w-full" value={selectedDomain} onchange={(event) => domain = event.currentTarget.value}>{#each domains as name}<option value={name}>{name}</option>{/each}</select></label></div>
  {#if series.length > 0}
    <div class="coverage-plot" bind:clientWidth={chartWidth}>
    <svg viewBox={`0 0 ${viewportWidth} 265`} role="img" aria-label={series.map(c => `${date(c.startedAt)}: ${c.pageCount} pages`).join(', ')}>
      <defs><linearGradient id="history-coverage-fill" x1="0" y1="0" x2="0" y2="1"><stop stop-color={C.blue} stop-opacity=".25"/><stop offset="1" stop-color={C.blue} stop-opacity=".01"/></linearGradient></defs>
      {#each [0, .5, 1] as tick}<line x1="60" x2={plotEnd} y1={220-tick*185} y2={220-tick*185} stroke={C.border} stroke-dasharray="3 5"/><text x="50" y={224-tick*185} text-anchor="end">{Math.round(tick*max).toLocaleString()}</text>{/each}
      {#if series.length > 1}<polygon points={`60,220 ${points} ${plotEnd},220`} fill="url(#history-coverage-fill)"/><polyline {points} fill="none" stroke={C.blue} stroke-width="2.5"/>{/if}
      {#each series as crawl, i}<circle cx={x(i)} cy={220 - crawl.pageCount / max * 185} r="4" fill={C.blue}><title>{date(crawl.startedAt)} · {crawl.pageCount.toLocaleString()} pages</title></circle>{#if chartWidth >= 600 || [0, Math.round((series.length - 1) / 2), series.length - 1].includes(i)}<text x={x(i)} y="250" text-anchor={i === 0 ? 'start' : i === series.length - 1 ? 'end' : 'middle'}>{date(crawl.startedAt)}</text>{/if}{/each}
    </svg>
    </div>
    <p class="chart-note">{series.length} completed {series.length === 1 ? 'crawl. Run another crawl to see a trend.' : 'crawls. Counts can change with crawl scope and settings.'}</p>
  {:else}<div class="chart-empty">No completed crawls for this site yet.</div>{/if}
</section>
<style>.coverage-plot { width: 100%; min-width: 0; } svg { width: 100%; height: 265px; display: block; } text { font: 11px -apple-system, BlinkMacSystemFont, sans-serif; fill: var(--color-fg-2); } .chart-panel-heading { flex-wrap: wrap; }</style>
