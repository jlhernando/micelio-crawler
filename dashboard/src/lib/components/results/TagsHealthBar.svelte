<script lang="ts">
  let { stats, pageCount }: { stats: Record<string, unknown>; pageCount: number } = $props();

  interface TagHealth { label: string; good: number; tooLong: number; duplicate: number; missing: number; }

  function clampTag(n: number, missing: number, length: number, dup: number): TagHealth & { label: string } {
    // Categories can overlap (a page can be both too long and duplicate).
    // Cap total bad segments to n, then compute good as remainder.
    const bad = Math.min(missing + length + dup, n);
    const scale = bad > 0 && (missing + length + dup) > n ? n / (missing + length + dup) : 1;
    return { label: '', good: n - bad, tooLong: Math.round(length * scale), duplicate: Math.round(dup * scale), missing: Math.round(missing * scale) };
  }

  let tags = $derived.by((): TagHealth[] => {
    const n = pageCount || 1;
    const t = clampTag(n, (stats.pagesWithoutTitle as number) || 0, ((stats.titleTooLongCount as number) || 0) + ((stats.titleTooShortCount as number) || 0), (stats.duplicateTitleCount as number) || 0);
    const h = clampTag(n, (stats.pagesWithoutH1 as number) || 0, 0, (stats.multipleH1Count as number) || 0);
    const d = clampTag(n, (stats.pagesWithoutDescription as number) || 0, (stats.descriptionTooLongCount as number) || 0, (stats.duplicateDescriptionCount as number) || 0);
    t.label = 'Title'; h.label = 'H1'; d.label = 'Description';
    return [t, h, d];
  });

  function pct(v: number): string { return pageCount > 0 ? Math.round((v / pageCount) * 100) + '%' : '0%'; }
  function w(v: number): string { return pageCount > 0 ? ((v / pageCount) * 100) + '%' : '0%'; }
</script>

<div class="rounded-md border border-border bg-surface-2 p-4">
  <div class="text-xs font-medium text-fg-2 mb-3">HTML Tags Performance</div>
  <div class="space-y-2">
    {#each tags as tag}
      <div class="flex items-center gap-2">
        <span class="w-20 text-right text-xs text-fg-2 shrink-0">{tag.label}</span>
        <div class="flex-1 flex h-5 rounded overflow-hidden bg-surface-3">
          {#if tag.good > 0}<div style="width:{w(tag.good)};background:#3fb950" class="h-full"></div>{/if}
          {#if tag.tooLong > 0}<div style="width:{w(tag.tooLong)};background:#d29922" class="h-full"></div>{/if}
          {#if tag.duplicate > 0}<div style="width:{w(tag.duplicate)};background:#f97316" class="h-full"></div>{/if}
          {#if tag.missing > 0}<div style="width:{w(tag.missing)};background:#f85149" class="h-full"></div>{/if}
        </div>
        <span class="w-10 text-right text-xs shrink-0" style="color:{tag.good / (pageCount || 1) >= 0.8 ? '#3fb950' : tag.good / (pageCount || 1) >= 0.6 ? '#d29922' : '#f85149'}">{pct(tag.good)}</span>
      </div>
    {/each}
  </div>
  <div class="flex gap-4 mt-2">
    <span class="flex items-center gap-1 text-[10px] text-fg-2"><span class="w-2 h-2 rounded-full" style="background:#3fb950"></span>Good</span>
    <span class="flex items-center gap-1 text-[10px] text-fg-2"><span class="w-2 h-2 rounded-full" style="background:#d29922"></span>Length issue</span>
    <span class="flex items-center gap-1 text-[10px] text-fg-2"><span class="w-2 h-2 rounded-full" style="background:#f97316"></span>Duplicate</span>
    <span class="flex items-center gap-1 text-[10px] text-fg-2"><span class="w-2 h-2 rounded-full" style="background:#f85149"></span>Missing</span>
  </div>
</div>
