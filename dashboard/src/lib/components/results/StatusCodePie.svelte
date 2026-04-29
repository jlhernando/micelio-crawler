<script lang="ts">
  import DonutChart from './DonutChart.svelte';

  let { stats, onFilter }: {
    stats: Record<string, unknown>;
    onFilter?: (filter: { status?: string }) => void;
  } = $props();

  const CODE_COLORS: Record<string, string> = {
    '200': '#3fb950', '201': '#3fb950', '204': '#3fb950',
    '301': '#d29922', '302': '#eab308', '303': '#eab308', '307': '#eab308', '308': '#d29922',
    '400': '#f97316', '401': '#f97316', '403': '#f97316', '404': '#f97316', '410': '#9ca3af', '429': '#f97316', '451': '#f97316',
    '500': '#f85149', '502': '#f85149', '503': '#f85149', '504': '#f85149',
  };

  const GROUP_COLORS: Record<string, string> = { '2xx': '#3fb950', '3xx': '#d29922', '4xx': '#f97316', '5xx': '#f85149' };

  let statusCodes = $derived((stats.statusCodes as Record<string, number>) || {});
  let entries = $derived.by(() => {
    return Object.entries(statusCodes).filter(([, c]) => c > 0).sort((a, b) => b[1] - a[1]);
  });
  let labels = $derived(entries.map(([code]) => code));
  let data = $derived(entries.map(([, count]) => count));
  let colors = $derived(entries.map(([code]) => CODE_COLORS[code] || '#8b949e'));

  let groups = $derived.by(() => {
    const g: Record<string, number> = {};
    for (const [code, count] of entries) {
      const key = code[0] + 'xx';
      g[key] = (g[key] || 0) + count;
    }
    return Object.entries(g).sort((a, b) => a[0].localeCompare(b[0]));
  });
</script>

{#if entries.length > 0}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="text-xs font-medium text-fg-2 mb-2">Status Code Distribution</div>
    <DonutChart {labels} {data} {colors} height={180} />
    {#if onFilter && groups.length > 0}
      <div class="flex gap-3 mt-2 justify-center">
        {#each groups as [group, count]}
          <button
            type="button"
            class="flex items-center gap-1 text-xs cursor-pointer hover:underline"
            onclick={() => onFilter({ status: group })}
          >
            <span class="w-2 h-2 rounded-full" style="background: {GROUP_COLORS[group]}"></span>
            {group}: {count.toLocaleString()}
          </button>
        {/each}
      </div>
    {/if}
  </div>
{/if}
