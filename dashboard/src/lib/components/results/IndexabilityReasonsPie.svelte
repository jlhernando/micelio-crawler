<script lang="ts">
  import DonutChart from './DonutChart.svelte';

  let { stats, onFilter }: {
    stats: Record<string, unknown>;
    onFilter?: (filter: { indexable?: string }) => void;
  } = $props();

  const REASON_COLORS = ['#bc8cff', '#d29922', '#f97316', '#9ca3af', '#39d2c0', '#f778ba', '#58a6ff', '#f85149'];

  let indexability = $derived((stats.indexabilityStats as { indexable: number; nonIndexable: number; reasons: Record<string, number> }) || null);
  let entries = $derived.by(() => {
    if (!indexability?.reasons) return [];
    return Object.entries(indexability.reasons).filter(([, c]) => c > 0).sort((a, b) => b[1] - a[1]);
  });
  let labels = $derived(entries.map(([reason]) => reason));
  let data = $derived(entries.map(([, count]) => count));
  let colors = $derived(entries.map((_, i) => REASON_COLORS[i % REASON_COLORS.length]));
</script>

{#if entries.length > 0}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="text-xs font-medium text-fg-2 mb-2">Non-Indexable Reasons</div>
    <DonutChart {labels} {data} {colors} height={180} />
    {#if onFilter && indexability}
      <div class="flex gap-3 mt-2 justify-center">
        <button type="button" class="flex items-center gap-1 text-xs cursor-pointer hover:underline"
          onclick={() => onFilter({ indexable: 'yes' })}>
          <span class="w-2 h-2 rounded-full bg-success"></span>
          Indexable: {indexability.indexable.toLocaleString()}
        </button>
        <button type="button" class="flex items-center gap-1 text-xs cursor-pointer hover:underline"
          onclick={() => onFilter({ indexable: 'no' })}>
          <span class="w-2 h-2 rounded-full bg-danger"></span>
          Non-indexable: {indexability.nonIndexable.toLocaleString()}
        </button>
      </div>
    {/if}
  </div>
{/if}
