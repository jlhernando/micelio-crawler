<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Chart,
    BarController,
    BarElement,
    CategoryScale,
    LinearScale,
    Tooltip,
    Legend,
    Filler,
  } from 'chart.js';

  Chart.register(BarController, BarElement, CategoryScale, LinearScale, Tooltip, Legend, Filler);

  let {
    botDailyHits,
    botHourlyHits,
    dateRange,
    height = 260,
  }: {
    botDailyHits: Record<string, Record<string, number>>;
    botHourlyHits: Record<string, Record<string, number>>;
    dateRange: [string, string];
    height?: number;
  } = $props();

  const PALETTE = [
    { fill: 'rgba(88,166,255,0.78)',  border: '#58a6ff' }, // search blue
    { fill: 'rgba(192,132,252,0.78)', border: '#c084fc' }, // AI training purple
    { fill: 'rgba(244,114,182,0.78)', border: '#f472b6' }, // AI search pink
    { fill: 'rgba(52,211,153,0.78)',  border: '#34d399' }, // social green
    { fill: 'rgba(251,146,60,0.78)',  border: '#fb923c' }, // SEO tool orange
    { fill: 'rgba(250,204,21,0.78)',  border: '#facc15' }, // monitoring yellow
    { fill: 'rgba(148,163,184,0.78)', border: '#94a3b8' }, // other / generic slate
  ];
  const OTHER = { fill: 'rgba(107,114,128,0.60)', border: '#6b7280' };

  let canvas: HTMLCanvasElement;
  let chart: Chart<'bar'> | null = null;

  // Decide granularity: use hourly for timeframes ≤ 48h, daily otherwise.
  // Also fall back to daily if hourly stats aren't populated.
  const hoursBetween = $derived.by(() => {
    if (!dateRange || !dateRange[0] || !dateRange[1]) return 0;
    const a = new Date(dateRange[0]).getTime();
    const b = new Date(dateRange[1]).getTime();
    if (isNaN(a) || isNaN(b)) return 0;
    return Math.max(0, (b - a) / 3_600_000);
  });
  const useHourly = $derived.by(() => {
    if (hoursBetween === 0) return false;
    if (hoursBetween > 48) return false;
    return botHourlyHits && Object.keys(botHourlyHits).length > 0;
  });

  // Build the x-axis buckets (day or hour) from bot entries.
  const buckets = $derived.by(() => {
    const set = new Set<string>();
    const source = useHourly ? botHourlyHits : botDailyHits;
    for (const byBucket of Object.values(source || {})) {
      for (const k of Object.keys(byBucket || {})) set.add(k);
    }
    return Array.from(set).sort();
  });

  // Top 6 bots by total hits across all buckets; rest → "Other".
  const series = $derived.by(() => {
    const source = useHourly ? botHourlyHits : botDailyHits;
    if (!source) return [] as Array<{ label: string; data: number[]; color: { fill: string; border: string } }>;
    const totals = Object.entries(source).map(([name, byBucket]) => {
      let sum = 0;
      for (const v of Object.values(byBucket || {})) sum += v;
      return { name, sum };
    });
    totals.sort((a, b) => b.sum - a.sum);
    const top = totals.slice(0, 6);
    const rest = totals.slice(6);
    const datasets: Array<{ label: string; data: number[]; color: { fill: string; border: string } }> = [];
    top.forEach((t, i) => {
      const data = buckets.map(bk => (source[t.name] || {})[bk] || 0);
      datasets.push({ label: t.name, data, color: PALETTE[i % PALETTE.length] });
    });
    if (rest.length > 0) {
      const data = buckets.map(bk => {
        let sum = 0;
        for (const r of rest) sum += (source[r.name] || {})[bk] || 0;
        return sum;
      });
      datasets.push({ label: `Other (${rest.length})`, data, color: OTHER });
    }
    return datasets;
  });

  // Human-readable bucket labels.
  const labels = $derived.by(() => {
    if (!useHourly) {
      return buckets.map(d => {
        // "2026-04-07" → "Apr 7"
        const dt = new Date(d + 'T00:00:00Z');
        if (isNaN(dt.getTime())) return d;
        return dt.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
      });
    }
    return buckets.map(h => {
      // "2026-04-07T14" → "Apr 7 14:00"
      const dt = new Date(h + ':00:00Z');
      if (isNaN(dt.getTime())) return h;
      return dt.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', timeZone: 'UTC' });
    });
  });

  function build() {
    if (!canvas) return;
    chart?.destroy();
    chart = new Chart(canvas, {
      type: 'bar',
      data: {
        labels,
        datasets: series.map(s => ({
          label: s.label,
          data: s.data,
          backgroundColor: s.color.fill,
          borderColor: s.color.border,
          borderWidth: 1,
          stack: 'bots',
          borderRadius: 2,
          categoryPercentage: 0.85,
          barPercentage: 0.95,
        })),
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        interaction: { mode: 'index', intersect: false },
        plugins: {
          legend: {
            position: 'bottom',
            labels: { boxWidth: 10, padding: 10, font: { size: 11 }, usePointStyle: true, color: '#c9d1d9' },
          },
          tooltip: {
            callbacks: {
              footer: (items) => {
                let total = 0;
                for (const it of items) total += (it.raw as number) || 0;
                return 'Total: ' + total.toLocaleString();
              },
            },
          },
        },
        scales: {
          x: {
            stacked: true,
            grid: { display: false },
            ticks: { color: '#8b949e', maxRotation: 0, autoSkip: true, autoSkipPadding: 10 },
          },
          y: {
            stacked: true,
            beginAtZero: true,
            grid: { color: 'rgba(139,148,158,0.1)' },
            ticks: { color: '#8b949e', callback: (v) => Number(v).toLocaleString() },
          },
        },
      },
    });
  }

  onMount(() => {
    build();
    return () => chart?.destroy();
  });

  // Rebuild on data/granularity change.
  $effect(() => {
    // Depend on relevant inputs so Svelte re-runs.
    void labels;
    void series;
    if (chart) build();
  });
</script>

<div class="space-y-2">
  <div class="flex items-center justify-between">
    <h3 class="text-sm font-medium text-fg">Bot Activity</h3>
    <span class="text-xs text-fg-2">{useHourly ? 'Hourly' : 'Daily'} · top 6 bots{series.length > 6 ? ' + Other' : ''}</span>
  </div>
  <div style="position:relative;height:{height}px">
    <canvas bind:this={canvas}></canvas>
  </div>
  {#if buckets.length === 0}
    <div class="text-center text-fg-2 text-xs py-6">No bot activity data yet.</div>
  {/if}
</div>
