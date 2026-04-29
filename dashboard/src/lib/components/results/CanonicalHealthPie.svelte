<script lang="ts">
  import DonutChart from './DonutChart.svelte';

  let { stats }: { stats: Record<string, unknown> } = $props();

  type CanonicalStats = { selfReferencing: number; canonicalized: number; crossDomain: unknown[] | null; multipleCanonicals: number; totalWithoutCanonical: number; relativeCanonicals: unknown[] | null; canonicalChains: unknown[] | null; canonicalLoops: unknown[] | null; canonicalToNon200: unknown[] | null; httpHttpsMismatch: unknown[] | null };

  let cs = $derived((stats.canonicalStats as CanonicalStats) || null);

  let entries = $derived.by(() => {
    if (!cs) return [];
    const self = cs.selfReferencing || 0;
    const canonicalized = cs.canonicalized || 0;
    const cross = cs.crossDomain?.length || 0;
    const missing = cs.totalWithoutCanonical || 0;
    const items: [string, number, string][] = [];
    if (self > 0) items.push(['Self-referencing', self, '#3fb950']);
    if (canonicalized > 0) items.push(['Canonicalized', canonicalized, '#d29922']);
    if (cross > 0) items.push(['Cross-domain', cross, '#58a6ff']);
    if (missing > 0) items.push(['Missing', missing, '#9ca3af']);
    return items;
  });

  let issues = $derived.by(() => {
    if (!cs) return [];
    const items: [string, number, string][] = [];
    if (cs.multipleCanonicals > 0) items.push(['Multiple', cs.multipleCanonicals, 'text-danger']);
    if (cs.canonicalChains?.length) items.push(['Chains', cs.canonicalChains.length, 'text-warning']);
    if (cs.canonicalLoops?.length) items.push(['Loops', cs.canonicalLoops.length, 'text-danger']);
    if (cs.canonicalToNon200?.length) items.push(['To non-200', cs.canonicalToNon200.length, 'text-warning']);
    if (cs.httpHttpsMismatch?.length) items.push(['HTTP/HTTPS', cs.httpHttpsMismatch.length, 'text-warning']);
    return items;
  });

  let labels = $derived(entries.map(([l]) => l));
  let data = $derived(entries.map(([, d]) => d));
  let colors = $derived(entries.map(([,, c]) => c));
</script>

{#if cs && entries.length > 0}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="text-xs font-medium text-fg-2 mb-2">Canonical Tags</div>
    <DonutChart {labels} {data} {colors} height={180} />
    {#if issues.length > 0}
      <div class="flex gap-2 mt-2 justify-center flex-wrap">
        {#each issues as [label, count, cls]}
          <span class="text-xs {cls}">{label}: {(count as number).toLocaleString()}</span>
        {/each}
      </div>
    {/if}
  </div>
{/if}
