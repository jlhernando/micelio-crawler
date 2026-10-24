<script lang="ts">
  import { C } from '../../palette';
  let { stats }: { stats: Record<string, unknown> } = $props();

  let scores = $derived((stats.pageRankScores as Record<string, number>) || {});

  const BUCKETS: [string, number, number, string][] = [
    ['0-1', 0, 1, '#9ca3af'],
    ['1-3', 1, 3, C.blue],
    ['3-5', 3, 5, C.warning],
    ['5-10', 5, 10, C.orange],
    ['10+', 10, Infinity, C.success],
  ];

  // Large crawls send only the server-side histogram; small ones may still
  // carry the full per-URL map (older stored crawls too).
  let hist = $derived(stats.pageRankHistogram as { buckets: number[]; total: number } | undefined);

  let bucketCounts = $derived.by(() => {
    if (hist?.buckets?.length === BUCKETS.length) return hist.buckets;
    const counts = BUCKETS.map(() => 0);
    for (const score of Object.values(scores)) {
      for (let i = 0; i < BUCKETS.length; i++) {
        if (score >= BUCKETS[i][1] && score < BUCKETS[i][2]) { counts[i]++; break; }
      }
    }
    return counts;
  });

  let total = $derived(hist?.total ?? Object.keys(scores).length);
  let hasData = $derived(total > 0);

  function fmtPct(count: number): string {
    if (count === 0) return '0%';
    const p = (count / total) * 100;
    return p >= 1 ? `${p.toFixed(0)}%` : p >= 0.01 ? `${p.toFixed(2)}%` : '<0.01%';
  }

  function fmtCount(n: number): string {
    return n >= 1000 ? `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}K` : String(n);
  }
</script>

{#if hasData}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="text-xs font-medium text-fg-2 mb-3">PageRank Distribution</div>
    <div class="flex gap-1.5">
      {#each BUCKETS as [label,,,color], i}
        <div class="flex-1 bg-surface-3 rounded p-2 text-center" style="border-bottom: 2px solid {color}">
          <div class="text-base font-semibold text-fg font-mono">{fmtCount(bucketCounts[i])}</div>
          <div class="text-[9px] mt-0.5" style="color: {color}">{label}</div>
          <div class="text-[9px] text-fg-2 mt-0.5">{fmtPct(bucketCounts[i])}</div>
        </div>
      {/each}
    </div>
  </div>
{/if}
