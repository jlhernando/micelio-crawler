<script lang="ts">
  let { stats }: { stats: Record<string, unknown> } = $props();

  let hasAnyData = $derived(stats.totalPages != null);

  let metrics = $derived.by(() => {
    const n = (k: string) => (stats[k] as number) || 0;
    return [
      { group: 'Title', items: [
        { label: 'Too short', count: n('titleTooShortCount') },
        { label: 'Too long', count: n('titleTooLongCount') },
        { label: 'Duplicate', count: n('duplicateTitleCount') },
        { label: 'Missing', count: n('pagesWithoutTitle') },
      ]},
      { group: 'Description', items: [
        { label: 'Too long', count: n('descriptionTooLongCount') },
        { label: 'Duplicate', count: n('duplicateDescriptionCount') },
        { label: 'Missing', count: n('pagesWithoutDescription') },
      ]},
      { group: 'H1', items: [
        { label: 'Multiple', count: n('multipleH1Count') },
        { label: 'Missing', count: n('pagesWithoutH1') },
      ]},
      { group: 'Other', items: [
        { label: 'No OG tags', count: n('pagesWithoutOg') },
        { label: 'No alt', count: n('imagesMissingAlt') },
      ]},
    ];
  });

  function cellColor(count: number): string {
    if (count === 0) return 'text-success';
    if (count <= 3) return 'text-warning';
    return 'text-danger';
  }
</script>

{#if hasAnyData}
<div class="rounded-md border border-border bg-surface-2 p-4">
  <div class="text-xs font-medium text-fg-2 mb-3">Content Quality</div>
  <div class="space-y-3">
    {#each metrics as { group, items }}
      <div>
        <div class="text-[10px] text-fg-2 uppercase tracking-wider mb-1">{group}</div>
        <div class="flex gap-2 flex-wrap">
          {#each items as { label, count }}
            <div class="flex items-center gap-1.5 px-2 py-1 rounded bg-surface-3 text-xs">
              <span class="text-fg-2">{label}</span>
              <span class="font-bold {cellColor(count)}">{count.toLocaleString()}</span>
            </div>
          {/each}
        </div>
      </div>
    {/each}
  </div>
</div>
{/if}
