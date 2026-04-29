<script lang="ts">
  import DonutChart from './DonutChart.svelte';

  let { stats }: { stats: Record<string, unknown> } = $props();

  let buckets = $derived((stats.responseTimeBuckets as { fast: number; medium: number; slow: number; slowest: number }) || null);
  let hasBuckets = $derived(buckets && (buckets.fast + buckets.medium + buckets.slow + buckets.slowest) > 0);

  const labels = ['Fast (<500ms)', 'Medium (500ms-1s)', 'Slow (1s-2s)', 'Slowest (>2s)'];
  const colors = ['#3fb950', '#d29922', '#f97316', '#f85149'];
  let data = $derived(buckets ? [buckets.fast, buckets.medium, buckets.slow, buckets.slowest] : []);
</script>

{#if hasBuckets}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="text-xs font-medium text-fg-2 mb-2">Load Time Distribution</div>
    <DonutChart {labels} {data} {colors} height={180} />
  </div>
{/if}
