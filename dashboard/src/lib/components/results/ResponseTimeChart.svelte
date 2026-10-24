<script lang="ts">
  import { C } from '../../palette';
  let { stats }: { stats: Record<string, unknown> } = $props();
  let buckets = $derived(stats.responseTimeBuckets as { fast: number; medium: number; slow: number; slowest: number } | undefined);
  let values = $derived(buckets ? [buckets.fast, buckets.medium, buckets.slow, buckets.slowest] : []);
  let total = $derived(values.reduce((sum, n) => sum + n, 0));
  let max = $derived(Math.max(...values, 1));
  let percentiles = $derived(stats.responseTimePercentiles as { p50: number; p90: number } | undefined);
  const labels = ['<500 ms', '500–1,000', '1,000–2,000', '>2,000 ms'];
  const colors = [C.blue, C.blue, C.warning, C.danger];
</script>

<section class="chart-panel">
  <div class="chart-panel-heading">
    <div><h2>Response times</h2><p>Pages per response-time interval</p></div>
    {#if percentiles}<span class="text-xs text-fg-2">p50 · {Math.round(percentiles.p50)} ms</span>{/if}
  </div>
  {#if total > 0}
    <svg viewBox="0 0 480 240" role="img" aria-label={values.map((v, i) => `${labels[i]}: ${v} pages`).join(', ')}>
      {#each [0, 0.5, 1] as tick}
        <line x1="42" x2="465" y1={190 - tick * 155} y2={190 - tick * 155} stroke={C.border} stroke-dasharray="3 5" />
        <text x="33" y={194 - tick * 155} text-anchor="end">{Math.round(max * tick).toLocaleString()}</text>
      {/each}
      {#each values as value, i}
        <rect x={66 + i * 102} y={190 - value / max * 155} width="54" height={value / max * 155} rx="4" fill={colors[i]} opacity={i === 0 ? 0.7 : 1}><title>{labels[i]}: {value.toLocaleString()} pages</title></rect>
        <text x={93 + i * 102} y={180 - value / max * 155} text-anchor="middle" fill={C.fg}>{value.toLocaleString()}</text>
        <text x={93 + i * 102} y="219" text-anchor="middle">{labels[i]}</text>
      {/each}
    </svg>
    <p class="chart-note">{total.toLocaleString()} measured pages{#if percentiles} · p90 {Math.round(percentiles.p90)} ms{/if}</p>
  {:else}<div class="chart-empty">Response-time data is not available for this crawl.</div>{/if}
</section>
<style>svg { width: 100%; display: block; } text { font: 11px -apple-system, BlinkMacSystemFont, sans-serif; fill: var(--color-fg-2); }</style>
