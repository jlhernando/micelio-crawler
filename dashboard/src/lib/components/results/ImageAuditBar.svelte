<script lang="ts">
  let { stats }: { stats: Record<string, unknown> } = $props();

  type ImageAudit = { missingAltAttribute: number; emptyAlt: number; altTooLong: unknown[] | null; missingDimensions: number; oversizedImages: unknown[] | null; totalImages: number; totalImageBytes: number };

  let audit = $derived((stats.imageAuditStats as ImageAudit) || null);
  let totalImages = $derived(audit?.totalImages || (stats.totalImages as number) || 0);

  let bars = $derived.by(() => {
    if (!audit) return [];
    const items: [string, number, string][] = [];
    if (audit.missingAltAttribute > 0) items.push(['Missing alt', audit.missingAltAttribute, '#f85149']);
    if (audit.emptyAlt > 0) items.push(['Empty alt', audit.emptyAlt, '#f97316']);
    if (audit.altTooLong?.length) items.push(['Alt too long', audit.altTooLong.length, '#d29922']);
    if (audit.missingDimensions > 0) items.push(['No dimensions', audit.missingDimensions, '#9ca3af']);
    if (audit.oversizedImages?.length) items.push(['Oversized', audit.oversizedImages.length, '#f97316']);
    return items;
  });
</script>

{#if audit && (bars.length > 0 || totalImages > 0)}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="flex items-center justify-between mb-3">
      <div class="text-xs font-medium text-fg-2">Image Audit</div>
      <div class="text-xs text-fg-2">{totalImages.toLocaleString()} images</div>
    </div>
    {#if bars.length > 0}
      <div class="space-y-2">
        {#each bars as [label, count, color]}
          {@const pct = totalImages > 0 ? (count / totalImages) * 100 : 0}
          <div class="flex items-center gap-2">
            <span class="w-24 text-xs text-fg-2 text-right truncate">{label}</span>
            <div class="flex-1 h-4 rounded bg-surface-3 overflow-hidden">
              <div class="h-full rounded transition-all" style="width: {Math.max(pct, 2)}%; background: {color}"></div>
            </div>
            <span class="w-14 text-xs text-right font-mono">{count.toLocaleString()}</span>
          </div>
        {/each}
      </div>
    {:else}
      <div class="text-xs text-success text-center py-4">All images pass audit checks</div>
    {/if}
  </div>
{/if}
