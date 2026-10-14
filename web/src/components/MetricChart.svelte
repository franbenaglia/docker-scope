<script lang="ts" module>
  import {
    Chart,
    Filler,
    Legend,
    LinearScale,
    LineController,
    LineElement,
    PointElement,
    Tooltip,
  } from 'chart.js';

  // Sólo lo necesario (tree-shaking); el eje X es lineal en milisegundos y se
  // formatea a mano: no hace falta un adaptador de fechas.
  Chart.register(LineController, LineElement, PointElement, LinearScale, Tooltip, Legend, Filler);

  export interface ChartSeries {
    label: string;
    values: (number | null)[];
    /** Variable CSS del color, p. ej. "--chart-1". */
    color: string;
    dashed?: boolean;
    fill?: boolean;
  }
</script>

<script lang="ts">
  import { onMount } from 'svelte';

  interface Props {
    title: string;
    /** Aclaración de unidad, visible bajo el título. */
    unit: string;
    times: number[];
    series: ChartSeries[];
    format: (v: number) => string;
    /** Rango pedido (ms): el eje X lo muestra completo aunque falten datos. */
    from: number;
    to: number;
    suggestedMax?: number;
  }

  let { title, unit, times, series, format, from, to, suggestedMax }: Props = $props();
  const spanMs = $derived(to - from);

  let canvas: HTMLCanvasElement;
  type Pt = { x: number; y: number | null };
  let chart: Chart<'line', Pt[]> | null = null;

  const css = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();

  const tickLabel = (ms: number) => {
    const d = new Date(ms);
    if (spanMs > 36 * 3600_000) return d.toLocaleDateString(undefined, { day: '2-digit', month: '2-digit' }) + ' ' + d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
    return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', ...(spanMs <= 3600_000 ? { second: '2-digit' } : {}) });
  };

  function datasets() {
    return series.map((s) => {
      const color = css(s.color);
      return {
        label: s.label,
        data: times.map((x, i): Pt => ({ x, y: s.values[i] ?? null })),
        borderColor: color,
        backgroundColor: color + '33',
        borderWidth: 1.5,
        borderDash: s.dashed ? [6, 4] : [],
        pointRadius: 0,
        pointHitRadius: 6,
        fill: s.fill ?? false,
        spanGaps: false, // un hueco es "sin datos", no una línea inventada
        tension: 0,
      };
    });
  }

  onMount(() => {
    const grid = css('--border');
    const text = css('--muted');
    chart = new Chart(canvas, {
      type: 'line',
      data: { datasets: datasets() },
      options: {
        animation: false,
        responsive: true,
        maintainAspectRatio: false,
        interaction: { mode: 'index', intersect: false },
        normalized: true,
        scales: {
          x: {
            type: 'linear',
            min: from,
            max: to,
            grid: { color: grid },
            ticks: { color: text, maxTicksLimit: 6, callback: (v) => tickLabel(Number(v)) },
          },
          y: {
            beginAtZero: true,
            suggestedMax,
            grid: { color: grid },
            ticks: { color: text, maxTicksLimit: 5, callback: (v) => format(Number(v)) },
          },
        },
        plugins: {
          legend: { labels: { color: text, boxWidth: 12 } },
          tooltip: {
            callbacks: {
              title: (items) => (items[0] ? new Date(items[0].parsed.x ?? 0).toLocaleString() : ''),
              label: (item) =>
                `${item.dataset.label}: ${item.parsed.y === null ? 'sin datos' : format(item.parsed.y)}`,
            },
          },
        },
      },
    });
    return () => {
      chart?.destroy();
      chart = null;
    };
  });

  // Actualiza datos sin recrear el gráfico.
  $effect(() => {
    const ds = datasets();
    const [min, max] = [from, to];
    if (!chart) return;
    chart.data.datasets = ds;
    const x = chart.options.scales?.['x'];
    if (x) {
      x.min = min;
      x.max = max;
    }
    chart.update('none');
  });
</script>

<figure class="chart">
  <figcaption>
    <span class="title">{title}</span>
    <span class="unit">{unit}</span>
  </figcaption>
  <div class="canvas" role="img" aria-label={`Gráfico: ${title} (${unit})`}>
    <canvas bind:this={canvas} aria-hidden="true"></canvas>
  </div>
</figure>

<style>
  .chart {
    margin: 0;
    padding: 0.75rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    min-width: 0;
  }
  figcaption {
    display: flex;
    flex-direction: column;
    margin-bottom: 0.5rem;
  }
  .title {
    font-weight: 600;
  }
  .unit {
    color: var(--muted);
    font-size: 0.8rem;
  }
  .canvas {
    position: relative;
    height: 200px;
  }
</style>
