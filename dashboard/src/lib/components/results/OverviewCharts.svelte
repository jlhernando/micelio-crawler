<script lang="ts">
  import DonutChart from './DonutChart.svelte';
  let { stats, pageCount, onFilter }: {
    stats: Record<string, unknown>; pageCount: number;
    onFilter: (filter: { indexable?: string; template?: string }) => void;
  } = $props();
  let indexability = $derived(stats.indexabilityStats as { indexable: number; nonIndexable: number } | undefined);
  let total = $derived(indexability ? indexability.indexable + indexability.nonIndexable : 0);
  let types = $derived(Object.entries((stats.templateTypeDistribution as Record<string, number>) || {}).filter(([, n]) => n > 0).sort((a,b) => b[1]-a[1]));
  const colors = ['#75a6ff', '#b09aff', '#73d9ac', '#e5b66e', '#57c6c9', '#f58f98'];
</script>

<section class="chart-panel">
  <div class="chart-panel-heading"><div><h2>Indexability</h2><p>Which pages allow search indexing</p></div><span class="text-xs text-fg-2">{pageCount.toLocaleString()} pages</span></div>
  {#if indexability && total > 0}
    <div class="coverage-chart">
      <DonutChart labels={['Indexable', 'Non-indexable']} data={[indexability.indexable, indexability.nonIndexable]} colors={['#73d9ac', '#e5b66e']} height={215} legend={false} />
      <div class="coverage-number"><strong>{(indexability.indexable / total * 100).toFixed(1)}%</strong><span>indexable</span></div>
    </div>
    <div class="coverage-legend">
      <button onclick={() => onFilter({ indexable: 'yes' })}><span><i style="background:#73d9ac"></i>Indexable</span><b>{indexability.indexable.toLocaleString()} ↗</b></button>
      <button onclick={() => onFilter({ indexable: 'no' })}><span><i style="background:#e5b66e"></i>Non-indexable</span><b>{indexability.nonIndexable.toLocaleString()} ↗</b></button>
    </div>
    <p class="chart-note mt-4">Indexable does not mean indexed by Google.</p>
  {:else}<div class="chart-empty">Indexability data is not available for this crawl.</div>{/if}
</section>
<section class="chart-panel">
  <div class="chart-panel-heading"><div><h2>Page types</h2><p>Explore the composition of your site</p></div></div>
  {#if types.length > 0}
    <DonutChart labels={types.map(([name]) => name)} data={types.map(([, n]) => n)} colors={types.map((_, i) => colors[i % colors.length])} height={225} />
    <div class="flex flex-wrap gap-2 mt-4">{#each types as [name, count]}<button class="text-xs border border-border rounded-md px-2 py-1 cursor-pointer hover:bg-surface-3" onclick={() => onFilter({ template: name })}>{name} <span class="text-fg-2 ml-1">{count.toLocaleString()} ↗</span></button>{/each}</div>
  {:else}<div class="chart-empty">Page-type classification is not available for this crawl.</div>{/if}
</section>
<style>
  .coverage-chart { position: relative; max-width: 280px; margin: 0 auto 12px; }
  .coverage-number { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; flex-direction: column; pointer-events: none; }
  .coverage-number strong { font-size: 29px; letter-spacing: -1px; font-weight: 550; }
  .coverage-number span { color: var(--color-fg-2); font-size: 11px; }
  .coverage-legend button { display: flex; justify-content: space-between; align-items: center; width: 100%; font-size: 12px; padding: 6px 0; cursor: pointer; }
  .coverage-legend span { display: flex; align-items: center; gap: 9px; color: var(--color-fg-2); }
  .coverage-legend i { width: 7px; height: 7px; border-radius: 50%; }
  .coverage-legend b { font-weight: 500; }
</style>
