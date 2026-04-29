<script lang="ts">
  let { stats }: { stats: Record<string, unknown> } = $props();

  type UrlStructure = { avgPathDepth: number; topDirectories: { directory: string; count: number }[] };

  let urlStats = $derived((stats.urlStructureStats as UrlStructure) || null);
  let dirs = $derived(urlStats?.topDirectories?.slice(0, 10) || []);
  let maxCount = $derived(dirs.length > 0 ? dirs[0].count : 1);
</script>

{#if dirs.length > 0}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="flex items-center justify-between mb-3">
      <div class="text-xs font-medium text-fg-2">Top Directories</div>
      {#if urlStats}
        <div class="text-xs text-fg-2">avg depth: {urlStats.avgPathDepth.toFixed(1)}</div>
      {/if}
    </div>
    <div class="space-y-1.5">
      {#each dirs as { directory, count }}
        <div class="flex items-center gap-2">
          <span class="w-28 text-xs text-fg-2 text-right truncate font-mono" title={directory}>{directory}</span>
          <div class="flex-1 h-4 rounded bg-surface-3 overflow-hidden">
            <div class="h-full rounded bg-accent/60 transition-all" style="width: {(count / maxCount) * 100}%"></div>
          </div>
          <span class="w-14 text-xs text-right font-mono">{count.toLocaleString()}</span>
        </div>
      {/each}
    </div>
  </div>
{/if}
