<script lang="ts">
  let { stats, pageCount, onFilter }: { stats: Record<string, unknown>; pageCount: number; onFilter?: (filter: { robotsBlocked?: string }) => void } = $props();

  let nonHttps = $derived(Array.isArray(stats.nonHttpsPages) ? stats.nonHttpsPages as string[] : []);
  let mixed = $derived(Array.isArray(stats.mixedContentPages) ? stats.mixedContentPages as string[] : []);
  let robotsBlocked = $derived((stats.robotsBlockedCount as number) || 0);

  let items = $derived.by(() => {
    const list: { label: string; value: string; status: 'good' | 'warn' | 'bad' }[] = [];
    const httpsCount = pageCount - nonHttps.length;
    const httpsPct = pageCount > 0 ? Math.round((httpsCount / pageCount) * 100) : 100;
    list.push({
      label: 'HTTPS',
      value: `${httpsPct}%`,
      status: httpsPct === 100 ? 'good' : httpsPct >= 90 ? 'warn' : 'bad',
    });
    list.push({
      label: 'Mixed Content',
      value: `${mixed.length.toLocaleString()}`,
      status: mixed.length === 0 ? 'good' : 'bad',
    });
    if (robotsBlocked > 0) {
      list.push({
        label: 'Robots Blocked',
        value: `${robotsBlocked.toLocaleString()}`,
        status: 'warn',
      });
    }
    return list;
  });

  let hasIssues = $derived(nonHttps.length > 0 || mixed.length > 0 || robotsBlocked > 0);
</script>

{#if hasIssues || pageCount > 0}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="text-xs font-medium text-fg-2 mb-3">Security</div>
    <div class="space-y-1.5">
      {#each items as { label, value, status }}
        {#if label === 'Robots Blocked' && onFilter}
          <button class="flex items-center justify-between px-2 py-1 rounded bg-surface-3 w-full hover:bg-surface-1 cursor-pointer transition-colors" onclick={() => onFilter({ robotsBlocked: 'yes' })}>
            <span class="text-xs text-fg-2">{label}</span>
            <span class="text-xs font-bold {status === 'good' ? 'text-success' : status === 'warn' ? 'text-warning' : 'text-danger'}">{value}</span>
          </button>
        {:else}
          <div class="flex items-center justify-between px-2 py-1 rounded bg-surface-3">
            <span class="text-xs text-fg-2">{label}</span>
            <span class="text-xs font-bold {status === 'good' ? 'text-success' : status === 'warn' ? 'text-warning' : 'text-danger'}">{value}</span>
          </div>
        {/if}
      {/each}
    </div>
  </div>
{/if}
