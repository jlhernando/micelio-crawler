<script lang="ts">
  let { pages }: { pages: { wordCount?: number }[] } = $props();

  const BUCKETS: [string, number, number, string][] = [
    ['0-100', 0, 100, '#f85149'],
    ['100-300', 100, 300, '#f97316'],
    ['300-500', 300, 500, '#d29922'],
    ['500-1K', 500, 1000, '#3fb950'],
    ['1K-2K', 1000, 2000, '#58a6ff'],
    ['2K+', 2000, Infinity, '#bc8cff'],
  ];

  let bucketCounts = $derived.by(() => {
    const counts = BUCKETS.map(() => 0);
    for (const p of pages) {
      const wc = p.wordCount || 0;
      for (let i = 0; i < BUCKETS.length; i++) {
        if (wc >= BUCKETS[i][1] && wc < BUCKETS[i][2]) { counts[i]++; break; }
      }
    }
    return counts;
  });

  let maxCount = $derived(Math.max(...bucketCounts, 1));
  let avgWords = $derived.by(() => {
    const total = pages.reduce((s, p) => s + (p.wordCount || 0), 0);
    return pages.length > 0 ? Math.round(total / pages.length) : 0;
  });
  let hasData = $derived(pages.length > 0);
</script>

{#if hasData}
  <div class="rounded-md border border-border bg-surface-2 p-5">
    <div class="flex items-center justify-between mb-3">
      <h3 class="text-sm font-medium text-fg-2">Word Count Distribution</h3>
      <span class="text-xs text-fg-2">avg: {avgWords.toLocaleString()} words</span>
    </div>
    <div class="flex items-end gap-1 h-28">
      {#each BUCKETS as [label,,,color], i}
        {@const pct = (bucketCounts[i] / maxCount) * 100}
        <div class="flex-1 flex flex-col items-center gap-1">
          <span class="text-[10px] text-fg-2 font-mono">{bucketCounts[i].toLocaleString()}</span>
          <div class="w-full rounded-t transition-all" style="height: {Math.max(pct, 3)}%; background: {color}"></div>
          <span class="text-[10px] text-fg-2">{label}</span>
        </div>
      {/each}
    </div>
  </div>
{/if}
