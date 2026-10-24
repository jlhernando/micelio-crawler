<script lang="ts">
  import { C } from '../../palette';
  // Server-computed histogram (stats.wordCountBuckets). Edges are inclusive
  // lower bounds; the last bucket is open-ended.
  interface WordCountBuckets {
    edges: number[];
    counts: number[];
    avgWords: number;
    pages: number;
  }

  let { buckets }: { buckets: WordCountBuckets | null } = $props();

  const COLORS = [C.danger, C.orange, C.warning, C.success, C.blue, '#bc8cff'];

  function edgeLabel(n: number): string {
    return n >= 1000 ? `${n / 1000}K` : String(n);
  }

  let bars = $derived.by(() => {
    if (!buckets) return [];
    const { edges, counts } = buckets;
    return edges.map((lo, i) => ({
      label: i < edges.length - 1 ? `${lo}-${edgeLabel(edges[i + 1])}` : `${edgeLabel(lo)}+`,
      count: counts[i] || 0,
      color: COLORS[i % COLORS.length],
    }));
  });

  let maxCount = $derived(Math.max(...bars.map((b) => b.count), 1));
  let avgWords = $derived(Math.round(buckets?.avgWords || 0));
  let hasData = $derived((buckets?.pages || 0) > 0);
</script>

{#if hasData}
  <div class="rounded-md border border-border bg-surface-2 p-5">
    <div class="flex items-center justify-between mb-3">
      <h3 class="text-sm font-medium text-fg-2">Word Count Distribution</h3>
      <span class="text-xs text-fg-2">avg: {avgWords.toLocaleString()} words</span>
    </div>
    <div class="flex items-end gap-1 h-28">
      {#each bars as bar}
        {@const pct = (bar.count / maxCount) * 100}
        <div class="flex-1 flex flex-col items-center gap-1">
          <span class="text-[10px] text-fg-2 font-mono">{bar.count.toLocaleString()}</span>
          <div class="w-full rounded-t transition-all" style="height: {Math.max(pct, 3)}%; background: {bar.color}"></div>
          <span class="text-[10px] text-fg-2">{bar.label}</span>
        </div>
      {/each}
    </div>
  </div>
{/if}
