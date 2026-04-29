<script lang="ts">
  let { stats, onFilter }: {
    stats: Record<string, unknown>;
    onFilter?: (filter: { classification?: string }) => void;
  } = $props();

  interface FunnelSegment {
    crawled: number;
    renderable: number;
    indexable: number;
    visible: number;
    active: number;
    nonIndexable: number;
    pctActive: number;
  }

  interface FunnelStats {
    crawled: number;
    renderable: number;
    indexable: number;
    visible: number;
    active: number;
    nonIndexable: number;
    pctRenderable: number;
    pctIndexable: number;
    pctVisible: number;
    pctActive: number;
    segments?: Record<string, FunnelSegment>;
  }

  let funnel = $derived((stats.seoFunnelStats as FunnelStats) || null);

  let hasAnalytics = $derived.by(() => {
    if (!funnel) return false;
    return funnel.visible > 0 || funnel.active > 0;
  });

  let segmentKeys = $derived.by(() => {
    if (!funnel?.segments) return [];
    return Object.keys(funnel.segments).sort((a, b) => {
      const sa = funnel!.segments![a];
      const sb = funnel!.segments![b];
      return sb.crawled - sa.crawled;
    });
  });

  let selectedSegment = $state<string | null>(null);

  let activeFunnel = $derived.by((): FunnelStats | FunnelSegment | null => {
    if (!funnel) return null;
    if (selectedSegment && funnel.segments?.[selectedSegment]) {
      return funnel.segments[selectedSegment];
    }
    return funnel;
  });

  let nonIndexableCount = $derived.by(() => {
    if (!activeFunnel) return 0;
    return activeFunnel.nonIndexable;
  });

  interface Stage {
    key: string;
    label: string;
    count: number;
    pct: number;
    dropOff: number;
    color: string;
  }

  let stages = $derived.by((): Stage[] => {
    if (!activeFunnel) return [];
    const f = activeFunnel;
    const total = f.crawled || 1;
    const visibleTotal = f.visible + f.active;
    return [
      { key: 'crawled', label: 'Crawled', count: f.crawled, pct: 100, dropOff: 0, color: '#8b949e' },
      { key: 'renderable', label: 'Renderable', count: f.renderable, pct: f.renderable / total * 100, dropOff: (f.crawled - f.renderable) / total * 100, color: '#79c0ff' },
      { key: 'indexable', label: 'Indexable', count: f.indexable, pct: f.indexable / total * 100, dropOff: (f.renderable - f.indexable) / total * 100, color: '#58a6ff' },
      { key: 'visible', label: 'Ranked', count: visibleTotal, pct: visibleTotal / total * 100, dropOff: (f.indexable - visibleTotal) / total * 100, color: '#d29922' },
      { key: 'active', label: 'Converting', count: f.active, pct: f.active / total * 100, dropOff: (visibleTotal - f.active) / total * 100, color: '#3fb950' },
    ];
  });

  function dropLabel(drop: number): string {
    if (drop <= 0) return '';
    return `-${drop.toFixed(1)}%`;
  }

  function barWidth(pct: number): number {
    return Math.max(pct, 3);
  }
</script>

{#if funnel}
  <div class="rounded-md border border-border bg-surface-2 p-4">
    <div class="flex items-center justify-between mb-3">
      <div class="flex items-center gap-2">
        <div class="text-xs font-medium text-fg-2">SEO Funnel</div>
        {#if nonIndexableCount > 0}
          <span class="px-1.5 py-0.5 rounded text-[10px] font-medium bg-danger/15 text-danger">
            {nonIndexableCount.toLocaleString()} non-indexable
          </span>
        {/if}
      </div>
      {#if segmentKeys.length > 0}
        <div class="flex items-center gap-1">
          <button
            type="button"
            class="px-1.5 py-0.5 rounded text-[10px] transition-colors {selectedSegment === null ? 'bg-accent/20 text-accent font-medium' : 'text-fg-2 hover:bg-surface-3'}"
            onclick={() => selectedSegment = null}
          >All</button>
          {#each segmentKeys as seg}
            <button
              type="button"
              class="px-1.5 py-0.5 rounded text-[10px] transition-colors capitalize {selectedSegment === seg ? 'bg-accent/20 text-accent font-medium' : 'text-fg-2 hover:bg-surface-3'}"
              onclick={() => selectedSegment = selectedSegment === seg ? null : seg}
            >{seg}</button>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Horizontal Funnel -->
    <div class="space-y-1">
      {#each stages as stage, i}
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="w-[72px] text-right text-[10px] text-fg-2 cursor-pointer hover:underline shrink-0"
            onclick={() => onFilter?.({ classification: stage.key })}
          >
            {stage.label}
          </button>
          <div class="flex-1 relative h-6">
            <button
              type="button"
              class="h-full rounded transition-all cursor-pointer hover:opacity-80 flex items-center px-2"
              style="width: {barWidth(stage.pct)}%; background: {stage.color}"
              title="{stage.label}: {stage.count.toLocaleString()} ({stage.pct.toFixed(1)}%)"
              onclick={() => onFilter?.({ classification: stage.key })}
            >
              <span class="text-[10px] font-bold text-white drop-shadow-sm whitespace-nowrap">
                {stage.count.toLocaleString()}
              </span>
            </button>
          </div>
          <div class="w-12 text-right text-[10px] font-medium shrink-0" style="color: {stage.color}">
            {stage.pct.toFixed(1)}%
          </div>
        </div>
        {#if i < stages.length - 1 && stages[i + 1].dropOff > 0}
          <div class="flex items-center gap-2">
            <div class="w-[72px]"></div>
            <div class="flex items-center gap-1 text-[9px] text-danger/70 pl-1">
              <span>↓</span>
              <span>{dropLabel(stages[i + 1].dropOff)}</span>
            </div>
          </div>
        {/if}
      {/each}
    </div>

    {#if !hasAnalytics}
      <div class="text-[10px] text-fg-2 mt-3 text-center italic">
        Enable GSC/GA4/Plausible for Ranked & Converting stages
      </div>
    {/if}
  </div>
{/if}
