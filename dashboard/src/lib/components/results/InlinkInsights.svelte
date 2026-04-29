<script lang="ts">
  let { stats }: { stats: Record<string, unknown> } = $props();

  let liStats = $derived((stats.linkIntelligenceStats as Record<string, unknown>) || null);
  let buckets = $derived((liStats?.inlinkBuckets as { zero: number; one: number; twoToFive: number; sixToTwenty: number; twentyPlus: number }) || null);
  let linkAnalysis = $derived((stats.linkAnalysis as { linksToNonIndexable?: { from: string; to: string }[] }) || null);
  let non2xx = $derived((liStats?.non2xxWithInlinks as number) || 0);
  let nonIndexableWithInlinks = $derived.by(() => {
    const links = linkAnalysis?.linksToNonIndexable;
    if (!links?.length) return 0;
    return new Set(links.map(l => l.to)).size;
  });

  interface Row { label: string; count: number; severity: 'normal' | 'warning'; }
  let rows = $derived.by((): Row[] => {
    if (!buckets) return [];
    return [
      { label: 'URLs with 1 Follow Inlink', count: buckets.one, severity: 'normal' },
      { label: 'URLs with 2-5 Follow Inlinks', count: buckets.twoToFive, severity: 'normal' },
      { label: 'URLs with 6-20 Follow Inlinks', count: buckets.sixToTwenty, severity: 'normal' },
      { label: 'URLs with 20+ Follow Inlinks', count: buckets.twentyPlus, severity: 'normal' },
      { label: 'URLs with No Follow Inlinks', count: buckets.zero, severity: buckets.zero > 0 ? 'warning' : 'normal' },
      { label: 'Non-Indexable URLs with Follow Inlinks', count: nonIndexableWithInlinks, severity: nonIndexableWithInlinks > 0 ? 'warning' : 'normal' },
      { label: 'Non-2xx URLs with Follow Inlinks', count: non2xx, severity: non2xx > 0 ? 'warning' : 'normal' },
    ];
  });
</script>

{#if buckets}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="text-xs font-medium text-fg-2 mb-3">Inlink Distribution</div>
    <table class="w-full">
      <thead>
        <tr class="text-[11px] text-fg-2 border-b border-border">
          <th class="text-left pb-2 font-medium">Metric</th>
          <th class="text-right pb-2 font-medium"># URLs</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as row}
          <tr class="text-sm border-b border-border last:border-0 {row.severity === 'warning' ? 'bg-orange-500/5' : ''}">
            <td class="py-2 {row.severity === 'warning' ? 'text-orange-400 font-medium' : 'text-fg'}">{row.label}</td>
            <td class="py-2 text-right font-semibold tabular-nums {row.severity === 'warning' ? 'text-orange-400' : ''}">{row.count.toLocaleString()}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
