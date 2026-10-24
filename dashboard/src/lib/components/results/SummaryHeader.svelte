<script lang="ts">
  import { formatDuration } from '../../format';

  let { stats, pageCount, onFilter, activeTemplate = 'all', templateDistribution = null }: {
    stats: Record<string, unknown>;
    pageCount: number;
    onFilter?: (filter: { status?: string; template?: string; indexable?: string; classification?: string }) => void;
    activeTemplate?: string;
    templateDistribution?: Record<string, number> | null;
  } = $props();

  // Extract key metrics
  let duration = $derived(((stats.crawlDurationMs as number) || 0));
  // Use the passed-in (stable) distribution, falling back to stats for backwards compat
  let templateDist = $derived(templateDistribution || (stats.templateTypeDistribution as Record<string, number>) || null);

  let indexability = $derived(stats.indexabilityStats as { indexable: number; nonIndexable: number } | undefined);
  let httpErrors = $derived(stats.statusCodes ? Object.entries(stats.statusCodes as Record<string, number>).reduce((n, [code, count]) => Number(code) >= 400 ? n + count : n, 0) : null);

  function formatSpeed(pages: number, ms: number): string {
    if (ms < 1000) return `${pages}`;
    return ((pages / ms) * 1000).toFixed(1);
  }

</script>

<div class="space-y-4">
  <dl class="metric-strip">
    <div><dt>Pages crawled</dt><dd>{pageCount.toLocaleString()}</dd><small>{duration ? formatDuration(duration) + ' · ' + formatSpeed(pageCount, duration) + ' pages/s' : 'Crawl scope'}</small></div>
    <div><dt>Indexable pages</dt><dd>{indexability && pageCount > 0 ? (indexability.indexable / pageCount * 100).toFixed(1) + '%' : '—'}</dd><small>{indexability ? indexability.indexable.toLocaleString() + ' pages allow indexing' : 'No indexability data'}</small></div>
    <div><dt>HTTP errors</dt><dd class={httpErrors ? 'text-danger' : ''}>{httpErrors === null ? '—' : httpErrors.toLocaleString()}</dd><small>{httpErrors === null ? 'No status-code data' : 'Pages returning 4xx or 5xx'}</small></div>
    <div><dt>Median response</dt>
      {#if stats.responseTimePercentiles}
        {@const rt = stats.responseTimePercentiles as { p50: number; p90: number }}
        <dd>{Math.round(rt.p50)}<span class="text-sm text-fg-2 ml-1">ms</span></dd><small>p90 · {Math.round(rt.p90)} ms</small>
      {:else}<dd>—</dd><small>No response-time data</small>{/if}
    </div>
  </dl>

  <!-- Page Types -->
  {#if templateDist && Object.keys(templateDist).length > 0}
    <div class="rounded-md border border-border bg-surface-2 p-4">
      <div class="text-xs text-fg-2 mb-3">Page Types</div>
      <div class="flex flex-wrap gap-2">
        {#each Object.entries(templateDist).sort((a, b) => (b[1] as number) - (a[1] as number)) as [type, count]}
          <button
            type="button"
            class="px-2.5 py-1 rounded-md text-xs font-medium cursor-pointer transition-colors {activeTemplate === type ? 'bg-accent text-white' : 'bg-surface-3 text-fg-1 hover:bg-accent/30'}"
            onclick={() => onFilter?.({ template: activeTemplate === type ? 'all' : type })}
          >
            {type} <span class="{activeTemplate === type ? 'text-white/70' : 'text-fg-2'} ml-1">{(count as number).toLocaleString()}</span>
          </button>
        {/each}
      </div>
    </div>
  {/if}
</div>
