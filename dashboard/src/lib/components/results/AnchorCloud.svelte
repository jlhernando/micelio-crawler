<script lang="ts">
  import { api } from '../../api';

  interface AnchorEntry {
    text: string;
    count: number;
    isInternal: boolean;
    isNonDescriptive: boolean;
  }

  let { crawlId }: { crawlId: string } = $props();

  let filterMode = $state<'all' | 'internal' | 'external'>('internal');
  let showNonDescriptive = $state(false);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let anchors = $state<AnchorEntry[]>([]);
  let totalUnique = $state(0);
  let fetchSeq = 0;

  $effect(() => {
    const seq = ++fetchSeq;
    const _id = crawlId; // subscribe to crawlId
    loading = true;
    error = null;
    api.getAnchorStats(_id).then((data) => {
      if (fetchSeq !== seq) return;
      anchors = data.anchors || [];
      totalUnique = data.totalUnique || 0;
      loading = false;
    }).catch((err) => {
      if (fetchSeq !== seq) return;
      error = err?.message || 'Failed to load anchor data';
      loading = false;
    });
  });

  let filteredAnchors = $derived(
    anchors.filter(a => {
      if (filterMode === 'internal' && !a.isInternal) return false;
      if (filterMode === 'external' && a.isInternal) return false;
      if (!showNonDescriptive && a.isNonDescriptive) return false;
      return true;
    })
  );

  let maxCount = $derived(filteredAnchors.length > 0 ? filteredAnchors[0].count : 1);
  let totalAnchors = $derived(filteredAnchors.reduce((s, a) => s + a.count, 0));

  function fontSize(count: number): number {
    if (maxCount <= 1) return 14;
    const ratio = Math.log(count + 1) / Math.log(maxCount + 1);
    return Math.round(11 + ratio * 25);
  }

  function anchorColor(anchor: AnchorEntry): string {
    if (anchor.isNonDescriptive) return 'var(--color-fg-2, #888)';
    if (anchor.isInternal) return 'var(--color-accent, #4361ee)';
    return '#2ec4b6';
  }

  function opacity(count: number): number {
    if (maxCount <= 1) return 1;
    const ratio = Math.log(count + 1) / Math.log(maxCount + 1);
    return 0.4 + ratio * 0.6;
  }
</script>

<div class="rounded-xl border border-border bg-surface-2 p-5">
  <div class="flex items-center justify-between mb-3">
    <h3 class="text-sm font-medium text-fg-2">Inlink Anchor Text Cloud</h3>
    <div class="flex items-center gap-2">
      <div class="flex rounded-lg border border-border overflow-hidden text-[10px]">
        <button class="px-2 py-0.5 cursor-pointer {filterMode === 'internal' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => filterMode = 'internal'}>Internal</button>
        <button class="px-2 py-0.5 cursor-pointer {filterMode === 'external' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => filterMode = 'external'}>External</button>
        <button class="px-2 py-0.5 cursor-pointer {filterMode === 'all' ? 'bg-accent text-white' : 'bg-surface-3 text-fg-2 hover:bg-surface-1'}" onclick={() => filterMode = 'all'}>All</button>
      </div>
      <label class="flex items-center gap-1 text-[10px] text-fg-2 cursor-pointer">
        <input type="checkbox" bind:checked={showNonDescriptive} class="w-3 h-3 accent-accent" />
        Non-descriptive
      </label>
    </div>
  </div>

  {#if loading}
    <div class="flex items-center justify-center py-8 gap-2">
      <div class="w-4 h-4 border-2 border-accent border-t-transparent rounded-full animate-spin"></div>
      <span class="text-sm text-fg-2">Loading anchor data...</span>
    </div>
  {:else if error}
    <p class="text-sm text-danger text-center py-8">{error}</p>
  {:else if filteredAnchors.length === 0}
    <p class="text-sm text-fg-2 text-center py-8">No anchor text data available.</p>
  {:else}
    <div class="flex flex-wrap gap-x-3 gap-y-1.5 items-baseline justify-center py-4 max-h-[400px] overflow-y-auto">
      {#each filteredAnchors.slice(0, 150) as anchor}
        <span
          class="inline-block leading-tight hover:underline cursor-default transition-opacity"
          style="font-size: {fontSize(anchor.count)}px; color: {anchorColor(anchor)}; opacity: {opacity(anchor.count)}"
          title="{anchor.text} ({anchor.count} occurrences){anchor.isNonDescriptive ? ' — non-descriptive' : ''}"
        >
          {anchor.text}
        </span>
      {/each}
    </div>

    <div class="mt-3 pt-3 border-t border-border flex items-center gap-4 text-[10px] text-fg-2/60">
      <span>{totalUnique.toLocaleString()} unique anchor texts</span>
      <span>{totalAnchors.toLocaleString()} total occurrences</span>
      {#if filteredAnchors.length > 150}
        <span>Showing top 150</span>
      {/if}
      <span class="ml-auto flex items-center gap-2">
        <span class="inline-block w-2 h-2 rounded-full" style="background: var(--color-accent, #4361ee)"></span> Internal
        <span class="inline-block w-2 h-2 rounded-full" style="background: #2ec4b6"></span> External
        <span class="inline-block w-2 h-2 rounded-full" style="background: var(--color-fg-2, #888)"></span> Non-descriptive
      </span>
    </div>
  {/if}
</div>
