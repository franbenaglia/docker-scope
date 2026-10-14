<script lang="ts">
  import type { Summary } from '../lib/api';
  import { fmtBytes, fmtCores } from '../lib/format';

  interface Props {
    summary: Summary;
    dim: boolean;
  }
  let { summary, dim }: Props = $props();
</script>

<section class="cards" class:stale={dim} aria-label="Resumen">
  {#if summary.containers}
    {@const c = summary.containers}
    <div class="card"><span class="n ok">{c.running}</span><span>En ejecución</span></div>
    <div class="card"><span class="n">{c.stopped}</span><span>Detenidos</span></div>
    <div class="card" title="Contenedores en ejecución con healthcheck en estado unhealthy">
      <span class="n" class:bad={c.unhealthy > 0}>{c.unhealthy}</span><span>Unhealthy</span>
    </div>
    {#if c.paused > 0}
      <div class="card"><span class="n warn">{c.paused}</span><span>Pausados</span></div>
    {/if}
    {#if c.restarting > 0}
      <div class="card"><span class="n warn">{c.restarting}</span><span>Reiniciando</span></div>
    {/if}
    {#if summary.alerts}
      {@const a = summary.alerts}
      <div class="card" title="Incidentes abiertos; reconocer un incidente no lo resuelve">
        <span class="n" class:bad={a.open > 0}>{a.open}</span>
        <span>Incidentes abiertos</span>
        <span class="muted small">{a.unacked} sin reconocer{a.pending > 0 ? ` · ${a.pending} pendientes` : ''}</span>
      </div>
    {/if}
  {:else}
    <div class="card"><span class="n muted">—</span><span>Contenedores (sin datos)</span></div>
  {/if}
  {#if summary.resources}
    {@const r = summary.resources}
    <div class="card wide" title="Suma del uso de CPU de los contenedores en ejecución; 1 núcleo = 100% de una CPU">
      <span class="n">{r.cpu_cores_used !== null ? fmtCores(r.cpu_cores_used) : '—'}</span>
      <span>núcleos de CPU en uso, de {r.host_cpus} del host</span>
      <span class="muted small">suma de {r.cpu_sampled} de {r.running} contenedores en ejecución</span>
    </div>
    <div class="card wide" title="Suma de la memoria usada (sin caché inactiva) de los contenedores en ejecución">
      <span class="n">{r.mem_used_bytes !== null ? fmtBytes(r.mem_used_bytes) : '—'}</span>
      <span>de memoria en uso, de {fmtBytes(r.host_mem_bytes)} del host</span>
      <span class="muted small">suma de {r.mem_sampled} de {r.running} contenedores en ejecución</span>
    </div>
  {/if}
  <div class="card daemon" title={summary.daemon.last_error ?? ''}>
    <span class="n small-n" class:ok={summary.daemon.status === 'connected'} class:bad={summary.daemon.status === 'disconnected'}>
      {summary.mode === 'demo' ? 'Demo' : summary.daemon.status === 'connected' ? 'Conectado' : summary.daemon.status === 'connecting' ? 'Conectando' : 'Desconectado'}
    </span>
    <span>Daemon Docker</span>
    {#if summary.daemon.api_version && summary.mode !== 'demo'}
      <span class="muted small">API {summary.daemon.api_version}</span>
    {/if}
  </div>
</section>

<style>
  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 0.75rem;
  }
  @media (min-width: 480px) {
    .wide {
      grid-column: span 2;
    }
  }
  .card {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    padding: 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  .n {
    font-size: 1.8rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .small-n {
    font-size: 1.3rem;
    line-height: 2.2rem;
  }
</style>
