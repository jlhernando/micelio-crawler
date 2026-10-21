<script lang="ts">
  import { segmentDimensionLabel, segmentDimensionHelp, segmentValueLabel, type SegmentDimension } from '../../segments';

  let {
    stats,
    onOpenPages,
  }: {
    stats: Record<string, unknown>;
    onOpenPages: (dimension: string, value: string) => void;
  } = $props();

  let dimensions = $derived((stats.autoSegmentStats as SegmentDimension[] | undefined) ?? []);
  let thresholds = $derived((stats.textAmountThresholds as number[] | undefined) ?? null);
  let selected = $state('template');
  let current = $derived(dimensions.find((d) => d.dimension === selected) ?? dimensions[0]);
  let totalPages = $derived(current ? current.values.reduce((sum, v) => sum + v.pages, 0) + (current.unassigned ?? 0) : 0);
  let maxPages = $derived(current ? Math.max(1, ...current.values.map((v) => v.pages)) : 1);
  // Inlinks are missing when the crawl ran without link data; hide an all-zero column.
  let showInlinks = $derived(dimensions.some((d) => d.values.some((v) => v.avgInlinks > 0)));

  function pctClass(pct: number, goodHigh: boolean): string {
    const bad = goodHigh ? pct < 50 : pct > 10;
    const warn = goodHigh ? pct < 80 : pct > 0;
    if (bad) return 'text-danger';
    if (warn) return 'text-warning';
    return 'text-fg';
  }
</script>

{#if dimensions.length === 0}
  <div class="rounded-md border border-border bg-surface-2 p-6 text-sm text-fg-2">
    Segments are not available for this crawl yet. They appear once pages finish loading.
  </div>
{:else}
  <div class="space-y-4">
    <div class="rounded-md border border-border bg-surface-2 p-4">
      <div class="flex flex-wrap gap-1.5" role="tablist" aria-label="Segment dimension">
        {#each dimensions as d (d.dimension)}
          <button
            type="button"
            role="tab"
            aria-selected={current?.dimension === d.dimension}
            class="px-2.5 py-1 text-xs rounded-md border transition-colors {current?.dimension === d.dimension ? 'bg-fg text-surface-1 border-fg' : 'bg-surface-3 text-fg-2 border-border hover:text-fg'}"
            onclick={() => (selected = d.dimension)}
          >
            {segmentDimensionLabel(d.dimension)}
          </button>
        {/each}
      </div>
      {#if current}
        <p class="text-xs text-fg-2 mt-3">
          {segmentDimensionHelp(current.dimension)}
          {#if current.dimension === 'textAmount' && thresholds}
            Low is under {thresholds[0].toLocaleString()} words, high is {thresholds[1].toLocaleString()} or more.
          {/if}
        </p>
      {/if}
    </div>

    {#if current}
      <div class="rounded-md border border-border bg-surface-2 p-4 overflow-x-auto">
        <table class="w-full min-w-[760px]">
          <thead>
            <tr class="text-[11px] text-fg-2 border-b border-border">
              <th class="text-left pb-2 font-medium">{segmentDimensionLabel(current.dimension)}</th>
              <th class="text-left pb-2 font-medium w-56">Pages</th>
              <th class="text-right pb-2 font-medium">Indexable</th>
              <th class="text-right pb-2 font-medium">Errors</th>
              <th class="text-right pb-2 font-medium">Avg words</th>
              {#if showInlinks}<th class="text-right pb-2 font-medium">Avg inlinks</th>{/if}
              <th class="text-right pb-2 font-medium">Avg response</th>
              <th class="pb-2"><span class="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            {#each current.values as v (v.value)}
              <tr class="text-sm border-b border-border last:border-0">
                <td class="py-2 pr-3 max-w-xs">
                  <div class="truncate font-medium" title={v.value}>{segmentValueLabel(current.dimension, v.value)}</div>
                  {#if v.sampleUrl}
                    <div class="truncate text-[11px] text-fg-2 font-mono" title={v.sampleUrl}>{v.sampleUrl}</div>
                  {/if}
                </td>
                <td class="py-2 pr-3">
                  <div class="flex items-center gap-2">
                    <div class="h-1.5 flex-1 rounded-full bg-surface-3">
                      <div class="h-1.5 rounded-full bg-accent" style="width: {(v.pages / maxPages) * 100}%"></div>
                    </div>
                    <span class="tabular-nums w-20 text-right">{v.pages.toLocaleString()}</span>
                    <span class="tabular-nums w-12 text-right text-[11px] text-fg-2">{totalPages ? ((v.pages / totalPages) * 100).toFixed(1) : 0}%</span>
                  </div>
                </td>
                <td class="py-2 text-right tabular-nums {pctClass(v.indexablePct, true)}">{v.indexablePct}%</td>
                <td class="py-2 text-right tabular-nums {pctClass(v.errorPct, false)}">{v.errorPct}%</td>
                <td class="py-2 text-right tabular-nums">{Math.round(v.avgWords).toLocaleString()}</td>
                {#if showInlinks}<td class="py-2 text-right tabular-nums">{v.avgInlinks}</td>{/if}
                <td class="py-2 text-right tabular-nums">{v.avgResponseMs.toLocaleString()} ms</td>
                <td class="py-2 pl-3 text-right">
                  <button
                    type="button"
                    class="text-xs text-accent hover:underline whitespace-nowrap"
                    onclick={() => onOpenPages(current.dimension, v.value)}
                  >View pages →</button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        <div class="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-fg-2">
          {#if current.totalValues > current.values.length}
            <span>Showing the {current.values.length} largest of {current.totalValues.toLocaleString()} values.</span>
          {/if}
          {#if current.unassigned}
            <span>{current.unassigned.toLocaleString()} pages have no value here (redirects, errors, or data this crawl did not collect).</span>
          {/if}
        </div>
      </div>
    {/if}
  </div>
{/if}
