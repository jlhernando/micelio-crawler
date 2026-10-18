<script lang="ts">
  import { onMount } from 'svelte';
  import { Chart, DoughnutController, ArcElement, Tooltip, Legend } from 'chart.js';

  Chart.register(DoughnutController, ArcElement, Tooltip, Legend);

  let { labels, data, colors, height = 200, legend = true }: {
    labels: string[];
    data: number[];
    colors: string[];
    height?: number;
    legend?: boolean;
  } = $props();

  let canvas: HTMLCanvasElement;
  let chart: Chart<'doughnut'> | null = null;

  onMount(() => {
    chart = new Chart(canvas, {
      type: 'doughnut',
      data: { labels, datasets: [{ data, backgroundColor: colors, borderWidth: 0, hoverOffset: 6 }] },
      options: {
        cutout: '65%',
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: { display: legend, position: 'right', labels: { padding: 8, font: { size: 10 }, usePointStyle: true, boxWidth: 8 } },
          tooltip: {
            callbacks: {
              label: (ctx) => {
                const total = (ctx.dataset.data as number[]).reduce((a, b) => a + b, 0);
                const pct = total > 0 ? ((ctx.parsed / total) * 100).toFixed(1) : '0';
                return `${ctx.label}: ${ctx.parsed.toLocaleString()} (${pct}%)`;
              }
            }
          }
        }
      }
    });
    return () => chart?.destroy();
  });

  $effect(() => {
    if (!chart) return;
    chart.data.labels = labels;
    chart.data.datasets[0].data = data;
    chart.data.datasets[0].backgroundColor = colors;
    chart.update();
  });
</script>

<div style="position:relative;height:{height}px" role="img" aria-label={labels.map((label, i) => `${label}: ${(data[i] || 0).toLocaleString()}`).join(', ')}>
  <canvas bind:this={canvas}></canvas>
</div>
