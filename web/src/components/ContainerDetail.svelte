<script lang="ts">
  import { ApiError, api, type ContainerDetail, type Series } from '../lib/api';
  import { since } from '../lib/clock.svelte';
  import { fmtBytes, fmtCores, fmtDateTime, fmtDuration, fmtPercent, fmtRate } from '../lib/format';
  import { live } from '../lib/live.svelte';
  import LogViewer from './LogViewer.svelte';
  import MetricChart from './MetricChart.svelte';

  interface Props {
    id: string;
  }
  let { id }: Props = $props();

  const RANGES = [
    { v: '15m', label: '15 min', refresh: 10_000 },
    { v: '1h', label: '1 h', refresh: 15_000 },
    { v: '6h', label: '6 h', refresh: 60_000 },
    { v: '24h', label: '24 h', refresh: 60_000 },
    { v: '7d', label: '7 días', refresh: 300_000 },
  ];
  let range = $state('1h');

  // Estado vivo (SSE) y metadatos completos (REST).
  const c = $derived(live.containers[id]);
  let detail = $state<ContainerDetail | null>(null);
  let detailError = $state<string | null>(null);
  let gone = $state(false);

  let series = $state<Series | null>(null);
  let seriesError = $state<string | null>(null);
  let seriesLoading = $state(true);

  async function loadDetail(signal: AbortSignal) {
    try {
      detail = await api.container(id, signal);
      gone = false;
      detailError = null;
    } catch (e) {
      if (signal.aborted) return;
      if (e instanceof ApiError && e.status === 404) gone = true;
      else detailError = e instanceof Error ? e.message : String(e);
    }
  }

  async function loadSeries(signal: AbortSignal) {
    try {
      series = await api.series(id, range, 300, signal);
      seriesError = null;
    } catch (e) {
      if (signal.aborted) return;
      series = null;
      seriesError = e instanceof ApiError && e.status === 404 ? 'Sin historial de métricas para este contenedor.' : e instanceof Error ? e.message : String(e);
    } finally {
      seriesLoading = false;
    }
  }

  // Metadatos: al abrir y cuando cambia el estado (inicio, parada, eliminación).
  $effect(() => {
    void c?.state;
    void c?.started_at;
    const ctl = new AbortController();
    void loadDetail(ctl.signal);
    return () => ctl.abort();
  });

  // Serie: al cambiar el rango y periódicamente.
  $effect(() => {
    const r = RANGES.find((x) => x.v === range) ?? RANGES[1]!;
    const ctl = new AbortController();
    seriesLoading = true;
    void loadSeries(ctl.signal);
    const t = setInterval(() => void loadSeries(ctl.signal), r.refresh);
    return () => {
      clearInterval(t);
      ctl.abort();
    };
  });

  const view = $derived(c ?? detail);
  const times = $derived(series?.points.map((p) => Date.parse(p.t)) ?? []);
  const from = $derived(series ? Date.parse(series.from) : 0);
  const to = $derived(series ? Date.parse(series.to) : 0);
  const col = <K extends keyof Series['points'][number]>(k: K) => series?.points.map((p) => p[k] as number | null) ?? [];
  const hasExplicitLimit = $derived(view?.mem_limit_bytes != null);
  const memLimitLine = $derived(hasExplicitLimit ? col('mem_limit') : []);

  const healthLabel: Record<string, string> = { none: 'Sin healthcheck', starting: 'Iniciando', healthy: 'Healthy', unhealthy: 'Unhealthy' };
  let showLabels = $state(false);
  const labels = $derived(Object.entries(detail?.labels ?? {}).sort(([a], [b]) => a.localeCompare(b)));
</script>

<nav class="crumbs"><a href="#/">← Contenedores</a></nav>

{#if !view && !gone && !detailError}
  <p class="muted">Cargando…</p>
{:else if detailError && !view}
  <div class="banner bad" role="alert">No se pudo cargar el contenedor: {detailError}</div>
{:else}
  <header class="head">
    <h2>{view?.name ?? id.slice(0, 12)}</h2>
    {#if view}
      <span class="state {view.state}">{view.state}</span>
      {#if view.health !== 'none'}
        <span class:muted={!view.health_current} class="health {view.health_current ? view.health : ''}">
          {view.health_current ? healthLabel[view.health] : `último: ${healthLabel[view.health]}`}
        </span>
      {/if}
    {/if}
  </header>
  {#if gone || (!c && live.summary?.synced)}
    <div class="banner warn" role="status">
      Este contenedor ya no existe en Docker (fue eliminado o recreado con otro ID). Se muestra el historial disponible.
    </div>
  {/if}

  {#if view}
    <section class="meta" aria-label="Metadatos">
      <dl>
        <dt>ID</dt>
        <dd><code class="wrap">{view.id}</code></dd>
        <dt>Imagen</dt>
        <dd class="wrap">{view.image}</dd>
        {#if view.compose_project}
          <dt>Compose</dt>
          <dd>{view.compose_project} / {view.compose_service ?? '?'}</dd>
        {/if}
        <dt>Creado</dt>
        <dd>{view.created_at ? fmtDateTime(view.created_at) : '—'}</dd>
        <dt>Inicio</dt>
        <dd>
          {view.started_at ? fmtDateTime(view.started_at) : 'nunca iniciado'}
          {#if view.state === 'running' && view.started_at}
            <span class="muted">(activo {fmtDuration(since(view.started_at))})</span>
          {/if}
        </dd>
        {#if view.state !== 'running' && view.finished_at}
          <dt>Fin</dt>
          <dd>
            {fmtDateTime(view.finished_at)}
            {#if view.exit_code !== null}· código {view.exit_code}{/if}
            {#if view.oom_killed}· <span class="bad">OOMKilled</span>{/if}
          </dd>
        {/if}
        <dt>Reinicios</dt>
        <dd>
          {view.restart_count} según Docker (política {view.restart_policy}{detail?.restart_max_retry ? `, máx. ${detail.restart_max_retry}` : ''})
          · {view.observed_restarts} observados por el agente
          {#if view.last_observed_restart}<span class="muted">(último {fmtDateTime(view.last_observed_restart)})</span>{/if}
        </dd>
        <dt>Límites</dt>
        <dd>
          CPU {view.cpu_limit_cores !== null ? `${fmtCores(view.cpu_limit_cores)} núcleos` : 'sin límite'} ·
          memoria {view.mem_limit_bytes !== null ? fmtBytes(view.mem_limit_bytes) : 'sin límite'}
        </dd>
        {#if detail}
          <dt>Logs</dt>
          <dd>driver {detail.log_driver || '?'}{detail.tty ? ' · TTY (stdout y stderr no se distinguen)' : ''}</dd>
          {#if detail.error}
            <dt>Error</dt>
            <dd class="bad">{detail.error}</dd>
          {/if}
        {/if}
      </dl>

      {#if c?.metrics}
        {@const m = c.metrics}
        <dl class="now" aria-label="Valores actuales">
          <dt>CPU</dt>
          <dd>{m.cpu_percent !== null ? fmtPercent(m.cpu_percent) : '—'} <span class="muted">de 1 núcleo ({m.online_cpus} en el host)</span></dd>
          <dt>Memoria</dt>
          <dd>
            {m.mem_usage_bytes !== null ? fmtBytes(m.mem_usage_bytes) : '—'}
            {#if m.mem_limit_bytes !== null && m.mem_percent !== null}
              <span class="muted">
                · {fmtPercent(m.mem_percent)} de {fmtBytes(m.mem_limit_bytes)}
                {m.mem_limit_explicit ? '(límite)' : '(memoria del host: sin límite)'}
              </span>
            {/if}
            {#if !m.mem_cache_excluded}<span class="muted">· incluye caché</span>{/if}
          </dd>
          <dt>Red</dt>
          <dd>
            ↓ {m.net_rx_bps !== null ? fmtRate(m.net_rx_bps) : '—'} · ↑ {m.net_tx_bps !== null ? fmtRate(m.net_tx_bps) : '—'}
            {#if m.net_rx_bytes !== null && m.net_tx_bytes !== null}
              <span class="muted">(total ↓ {fmtBytes(m.net_rx_bytes)} · ↑ {fmtBytes(m.net_tx_bytes)})</span>
            {/if}
          </dd>
          <dt>Disco (I/O)</dt>
          <dd>
            {#if m.blk_read_bytes === null}
              <span class="muted">no informado por Docker</span>
            {:else}
              L {m.blk_read_bps !== null ? fmtRate(m.blk_read_bps) : '—'} · E {m.blk_write_bps !== null ? fmtRate(m.blk_write_bps) : '—'}
            {/if}
          </dd>
          {#if m.pids !== null}
            <dt>Procesos</dt>
            <dd>{m.pids}</dd>
          {/if}
        </dl>
      {/if}
    </section>

    {#if labels.length > 0}
      <details bind:open={showLabels} class="labels">
        <summary>Etiquetas ({labels.length})</summary>
        {#if showLabels}
          <dl>
            {#each labels as [k, v] (k)}
              <dt><code>{k}</code></dt>
              <dd class="wrap">{v}</dd>
            {/each}
          </dl>
        {/if}
      </details>
    {/if}
  {/if}

  <section aria-label="Historial">
    <div class="range" role="group" aria-label="Rango">
      <h3>Historial</h3>
      {#each RANGES as r (r.v)}
        <button type="button" class:active={range === r.v} aria-pressed={range === r.v} onclick={() => (range = r.v)}>{r.label}</button>
      {/each}
      {#if series}
        <span class="muted small">
          {series.points.length} puntos · cada uno resume {fmtDuration(series.bucket_seconds)}{series.source === '1m' ? ' (resúmenes por minuto)' : ''}
        </span>
      {/if}
    </div>
    {#if seriesError}
      <p class="muted">{seriesError}</p>
    {:else if seriesLoading && !series}
      <p class="muted">Cargando historial…</p>
    {:else if series && series.points.length === 0}
      <p class="muted">Todavía no hay muestras en este rango.</p>
    {:else if series}
      <div class="charts">
        <MetricChart
          title="CPU"
          unit="% de un núcleo (100% = 1 núcleo); promedio y máximo del intervalo"
          {times}
          {from}
          {to}
          format={fmtPercent}
          series={[
            { label: 'promedio', values: col('cpu_avg'), color: '--chart-1', fill: true },
            { label: 'máximo', values: col('cpu_max'), color: '--chart-2' },
          ]}
        />
        <MetricChart
          title="Memoria"
          unit={hasExplicitLimit ? 'uso sin caché inactiva; línea punteada = límite configurado' : 'uso sin caché inactiva (sin límite configurado)'}
          {times}
          {from}
          {to}
          format={fmtBytes}
          series={[
            { label: 'promedio', values: col('mem_avg'), color: '--chart-1', fill: true },
            { label: 'máximo', values: col('mem_max'), color: '--chart-2' },
            ...(hasExplicitLimit ? [{ label: 'límite', values: memLimitLine, color: '--bad', dashed: true }] : []),
          ]}
        />
        <MetricChart
          title="Red"
          unit="bytes por segundo (promedio del intervalo)"
          {times}
          {from}
          {to}
          format={fmtRate}
          series={[
            { label: 'recibido', values: col('net_rx_bps'), color: '--chart-3' },
            { label: 'enviado', values: col('net_tx_bps'), color: '--chart-4' },
          ]}
        />
        <MetricChart
          title="Disco (block I/O)"
          unit="tráfico leído/escrito por segundo; no es espacio ocupado"
          {times}
          {from}
          {to}
          format={fmtRate}
          series={[
            { label: 'lectura', values: col('blk_read_bps'), color: '--chart-3' },
            { label: 'escritura', values: col('blk_write_bps'), color: '--chart-4' },
          ]}
        />
      </div>
    {/if}
  </section>

  <LogViewer {id} />
{/if}

<style>
  .crumbs {
    margin-bottom: 0.75rem;
  }
  .head {
    display: flex;
    align-items: baseline;
    gap: 0.75rem;
    flex-wrap: wrap;
    margin-bottom: 1rem;
  }
  h2 {
    margin: 0;
    font-size: 1.4rem;
    overflow-wrap: anywhere;
  }
  h3 {
    margin: 0 0.5rem 0 0;
    font-size: 1.05rem;
  }
  .meta {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 22rem), 1fr));
    gap: 1rem;
    margin-bottom: 1rem;
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.35rem 1rem;
    margin: 0;
    padding: 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
    min-width: 0;
  }
  .wrap {
    overflow-wrap: anywhere;
  }
  .labels {
    margin-bottom: 1rem;
  }
  .labels summary {
    cursor: pointer;
    margin-bottom: 0.5rem;
  }
  .range {
    display: flex;
    flex-wrap: wrap;
    gap: 0.4rem;
    align-items: center;
    margin: 1.25rem 0 0.75rem;
  }
  .range button.active {
    background: var(--info);
    color: var(--bg);
    border-color: var(--info);
  }
  .charts {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 28rem), 1fr));
    gap: 0.75rem;
  }
</style>
