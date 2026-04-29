<script lang="ts">
  import CrawlHealthScore from './CrawlHealthScore.svelte';

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

  function formatDuration(ms: number): string {
    const s = Math.floor(ms / 1000);
    if (s < 60) return `${s}s`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ${s % 60}s`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ${m % 60}m`;
    const d = Math.floor(h / 24);
    return `${d}d ${h % 24}h ${m % 60}m`;
  }

  function formatSpeed(pages: number, ms: number): string {
    if (ms < 1000) return `${pages}`;
    return ((pages / ms) * 1000).toFixed(1);
  }

</script>

<div class="space-y-4">
  <!-- Key Metrics Row -->
  <div class="grid grid-cols-2 sm:grid-cols-5 gap-3">
    <div class="rounded-md border border-border bg-surface-2 p-4">
      <div class="text-xs text-fg-2">Total Pages</div>
      <div class="text-2xl font-bold mt-1">{pageCount.toLocaleString()}</div>
    </div>
    <div class="rounded-md border border-border bg-surface-2 p-4">
      <div class="text-xs text-fg-2">Duration</div>
      <div class="text-2xl font-bold mt-1">{formatDuration(duration)}</div>
    </div>
    <div class="rounded-md border border-border bg-surface-2 p-4">
      <div class="text-xs text-fg-2">Speed</div>
      <div class="text-2xl font-bold mt-1">{formatSpeed(pageCount, duration)} <span class="text-sm text-fg-2">p/s</span></div>
    </div>
    <CrawlHealthScore {stats} {pageCount} />
    <div class="rounded-md border border-border bg-surface-2 p-4">
      <div class="text-xs text-fg-2">Response Time</div>
      {#if stats.responseTimePercentiles}
        {@const rt = stats.responseTimePercentiles as { p50: number; p90: number; p99: number }}
        <div class="text-2xl font-bold mt-1 {rt.p50 > 1000 ? 'text-danger' : rt.p50 > 500 ? 'text-warning' : 'text-success'}">{Math.round(rt.p50)}<span class="text-sm text-fg-2">ms</span></div>
        <div class="text-xs text-fg-2">p90: <span class="{rt.p90 > 2000 ? 'text-danger' : rt.p90 > 1000 ? 'text-warning' : ''}">{Math.round(rt.p90)}ms</span> · p99: <span class="{rt.p99 > 3000 ? 'text-danger' : rt.p99 > 2000 ? 'text-warning' : ''}">{Math.round(rt.p99)}ms</span></div>
      {:else}
        <div class="text-2xl font-bold mt-1 text-fg-2">—</div>
      {/if}
    </div>
  </div>

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
