<script lang="ts">
  let { stats, pageCount }: { stats: Record<string, unknown>; pageCount: number } = $props();

  type SchemaStats = { pagesWithSchema: number; pagesWithValidSchema: number; pagesWithErrors: number; typeDistribution: Record<string, number>; richResultEligible: Record<string, number>; topIssues: { issue: string; count: number }[] | null };

  let schema = $derived((stats.schemaValidationStats as SchemaStats) || null);
  let sdCount = $derived((stats.pagesWithStructuredData as number) || schema?.pagesWithSchema || 0);
  let hasData = $derived(sdCount > 0);

  let types = $derived.by(() => {
    if (!schema?.typeDistribution) return [];
    return Object.entries(schema.typeDistribution).sort((a, b) => b[1] - a[1]).slice(0, 8);
  });

  let richTypes = $derived.by(() => {
    if (!schema?.richResultEligible) return [];
    return Object.entries(schema.richResultEligible).sort((a, b) => b[1] - a[1]);
  });

  let pct = $derived(pageCount > 0 ? Math.round((sdCount / pageCount) * 100) : 0);
</script>

{#if hasData}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="text-xs font-medium text-fg-2 mb-2">Structured Data</div>
    <div class="text-lg font-bold">{sdCount.toLocaleString()} <span class="text-sm text-fg-2 font-normal">/ {pageCount.toLocaleString()} pages ({pct}%)</span></div>
    {#if schema && schema.pagesWithErrors > 0}
      <div class="text-xs text-danger mt-1">{schema.pagesWithErrors.toLocaleString()} pages with errors</div>
    {/if}
    {#if types.length > 0}
      <div class="flex flex-wrap gap-1.5 mt-2">
        {#each types as [type, count]}
          <span class="px-1.5 py-0.5 rounded text-[10px] bg-surface-3 font-mono">{type} <span class="text-fg-2">{count.toLocaleString()}</span></span>
        {/each}
      </div>
    {/if}
    {#if richTypes.length > 0}
      <div class="text-[10px] text-fg-2 mt-2">Rich result eligible:</div>
      <div class="flex flex-wrap gap-1.5 mt-1">
        {#each richTypes as [type, count]}
          <span class="px-1.5 py-0.5 rounded text-[10px] bg-success/20 text-success font-mono">{type} {count.toLocaleString()}</span>
        {/each}
      </div>
    {/if}
  </div>
{/if}
