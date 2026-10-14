<script lang="ts">
  import { onMount } from 'svelte';
  import { live } from './lib/live.svelte';
  import { fmtBytes, fmtCores, fmtDuration, fmtPercent, fmtRate, fmtTime } from './lib/format';

  // Datos en vivo: snapshot REST + eventos SSE (ver lib/live.svelte.ts).
  // Reloj local para que el uptime avance entre eventos.
  let now = $state(Date.now());

  onMount(() => {
    live.start();
    const tick = setInterval(() => (now = Date.now()), 1000);
    return () => {
      live.stop();
      clearInterval(tick);
    };
  });

  const uptime = (startedAt: string) => (now - Date.parse(startedAt)) / 1000;

  const summary = $derived(live.summary);
  const containers = $derived(live.sorted);
  const agentLost = $derived(live.stream === 'reconnecting');

  const healthLabel: Record<string, string> = {
    none: 'Sin healthcheck',
    starting: 'Iniciando',
    healthy: 'Healthy',
    unhealthy: 'Unhealthy',
  };
</script>

<header>
  <h1>DockerScope</h1>
  {#if summary?.mode === 'demo'}
    <span class="pill demo">Fuente simulada</span>
  {:else if summary}
    {@const d = summary.daemon}
    <span class="pill {d.status}" title={d.last_error ?? ''}>
      {#if d.status === 'connected'}
        Docker conectado (API {d.api_version ?? '?'})
      {:else if d.status === 'connecting'}
        Conectando a Docker…
      {:else}
        Docker desconectado · reintentando{d.attempts ? ` (${d.attempts})` : ''}
      {/if}
    </span>
  {/if}
</header>

{#if summary?.mode === 'demo'}
  <div class="banner demo" role="status">MODO DEMO — datos simulados, no corresponden a Docker real</div>
{/if}

<main>
  {#if !summary && agentLost}
    <div class="banner bad" role="alert">No se pudo contactar al agente. Reintentando…</div>
  {:else if !summary && live.error}
    <div class="banner bad" role="alert">Error al obtener datos del agente: {live.error}</div>
  {:else if !summary}
    <p class="muted">Cargando…</p>
  {:else}
    {#if agentLost}
      <div class="banner warn" role="status">
        Se perdió la conexión en vivo con el agente; reconectando. Los datos son de las
        {fmtTime(summary.generated_at)} y pueden estar desactualizados.
      </div>
    {/if}
    {#if summary.daemon.status === 'disconnected'}
      <div class="banner bad" role="alert">
        Docker no responde desde las {fmtTime(summary.daemon.since)}.
        {#if summary.stale}
          Se muestra el <strong>último estado conocido</strong> (de las {fmtTime(summary.as_of)}); no
          significa que los contenedores se hayan detenido.
        {:else}
          Todavía no hay datos de contenedores: el estado es <strong>desconocido</strong>.
        {/if}
      </div>
    {/if}

    {#if summary.alerts && summary.alerts.maintenance.length > 0}
      <div class="banner info" role="status">
        Mantenimiento activo:
        {#each summary.alerts.maintenance as m, i (m.id)}
          {i > 0 ? ' · ' : ''}{m.global ? 'global' : m.container} hasta las {fmtTime(m.until)}{m.reason
            ? ` (${m.reason})`
            : ''}
        {/each}
        — se sigue recolectando, pero los avisos se suprimen.
      </div>
    {/if}

    <section class="cards" class:stale={summary.stale || agentLost} aria-label="Resumen">
      {#if summary.containers}
        {@const c = summary.containers}
        <div class="card"><span class="n ok">{c.running}</span><span>En ejecución</span></div>
        <div class="card"><span class="n">{c.stopped}</span><span>Detenidos</span></div>
        <div class="card">
          <span class="n" class:bad={c.unhealthy > 0}>{c.unhealthy}</span><span>Unhealthy</span>
        </div>
        {#if c.paused > 0}
          <div class="card"><span class="n warn">{c.paused}</span><span>Pausados</span></div>
        {/if}
        {#if c.restarting > 0}
          <div class="card"><span class="n warn">{c.restarting}</span><span>Reiniciando</span></div>
        {/if}
        <div class="card"><span class="n muted">{c.total}</span><span>Total</span></div>
        {#if summary.alerts}
          {@const a = summary.alerts}
          <div class="card" title="Incidentes abiertos; reconocer un incidente no lo resuelve">
            <span class="n" class:bad={a.open > 0}>{a.open}</span>
            <span>Incidentes abiertos</span>
            <span class="muted small">
              {a.unacked} sin reconocer{a.pending > 0 ? ` · ${a.pending} pendientes` : ''}
            </span>
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
    </section>

    {#if summary.synced}
      <section class="list" class:stale={summary.stale || agentLost} aria-label="Contenedores">
        <table>
          <thead>
            <tr>
              <th>Nombre</th>
              <th>ID</th>
              <th>Estado</th>
              <th>Salud</th>
              <th title="% de un núcleo: 100% = 1 CPU completa; puede superar 100% en hosts multinúcleo">CPU (% 1 núcleo)</th>
              <th title="Uso sin caché de página inactiva / límite">Memoria</th>
              <th title="Tasa de red recibida / enviada">Red ↓ / ↑</th>
              <th title="Tráfico de block I/O leído / escrito (no es espacio en disco)">Disco L / E</th>
              <th title="Docker RestartCount / reinicios observados por el agente">Reinicios D / obs.</th>
              <th>Compose</th>
            </tr>
          </thead>
          <tbody>
            {#each containers as c (c.id)}
              <tr>
                <td class="name">
                  {c.name}<div class="muted small ellipsis" title={c.image}>{c.image}</div>
                </td>
                <td><code>{c.short_id}</code></td>
                <td>
                  <span class="state {c.state}">{c.state}</span>
                  {#if c.uptime_seconds !== null && c.started_at}
                    <div class="muted small">activo {fmtDuration(uptime(c.started_at))}</div>
                  {:else if c.exit_code !== null}
                    <div class="muted small">código {c.exit_code}{c.oom_killed ? ' · OOM' : ''}</div>
                  {/if}
                </td>
                <td>
                  {#if c.health === 'none'}
                    <span class="muted">{healthLabel.none}</span>
                  {:else if c.health_current}
                    <span class="health {c.health}">{healthLabel[c.health]}</span>
                  {:else}
                    <span class="muted" title="El contenedor no está en ejecución">
                      último: {healthLabel[c.health]}
                    </span>
                  {/if}
                </td>
                {#if c.metrics}
                  {@const m = c.metrics}
                  <td class="num">
                    {m.cpu_percent !== null ? fmtPercent(m.cpu_percent) : '—'}
                    {#if c.cpu_limit_cores !== null}
                      <div class="muted small">límite {fmtCores(c.cpu_limit_cores)} núcl.</div>
                    {/if}
                  </td>
                  <td class="num">
                    {m.mem_usage_bytes !== null ? fmtBytes(m.mem_usage_bytes) : '—'}
                    {#if m.mem_limit_bytes !== null && m.mem_percent !== null}
                      <div class="muted small">
                        {m.mem_limit_explicit
                          ? `${fmtPercent(m.mem_percent)} de ${fmtBytes(m.mem_limit_bytes)}`
                          : 'sin límite'}
                      </div>
                    {/if}
                  </td>
                  <td class="num">
                    {m.net_rx_bps !== null ? fmtRate(m.net_rx_bps) : '—'}
                    <div class="muted small">{m.net_tx_bps !== null ? fmtRate(m.net_tx_bps) : '—'}</div>
                  </td>
                  {#if m.blk_read_bytes === null}
                    <td
                      class="muted"
                      title="Docker no informa block I/O para este contenedor (en cgroup v2 ocurre mientras no haya hecho I/O)"
                    >
                      n/d
                    </td>
                  {:else}
                    <td class="num">
                      {m.blk_read_bps !== null ? fmtRate(m.blk_read_bps) : '—'}
                      <div class="muted small">{m.blk_write_bps !== null ? fmtRate(m.blk_write_bps) : '—'}</div>
                    </td>
                  {/if}
                {:else}
                  <td class="muted" colspan="4" title="Sin muestra: contenedor detenido o primera ronda pendiente">
                    {c.state === 'running' ? 'midiendo…' : '—'}
                  </td>
                {/if}
                <td class="num">{c.restart_count} / {c.observed_restarts}</td>
                <td class="muted">
                  {c.compose_project ? `${c.compose_project} / ${c.compose_service ?? '?'}` : '—'}
                </td>
              </tr>
            {:else}
              <tr><td colspan="10" class="muted">No hay contenedores.</td></tr>
            {/each}
          </tbody>
        </table>
      </section>
    {/if}

    <p class="muted small">
      {#if summary.stale}
        Datos desactualizados de las {fmtTime(summary.as_of)}
      {:else}
        Actualizado: {fmtTime(summary.generated_at)}
      {/if}
      · {live.stream === 'live' ? 'en vivo' : 'sin conexión en vivo'}
    </p>
  {/if}
</main>

<style>
  header {
    display: flex;
    align-items: center;
    gap: 1rem;
    flex-wrap: wrap;
    padding: 0.75rem 1.25rem;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }
  h1 {
    font-size: 1.15rem;
    margin: 0;
  }
  main {
    padding: 1.25rem;
    max-width: 1200px;
    margin: 0 auto;
  }
  .pill {
    font-size: 0.85rem;
    padding: 0.2rem 0.6rem;
    border-radius: 999px;
    border: 1px solid var(--border);
  }
  .pill.connected {
    color: var(--ok);
  }
  .pill.connecting {
    color: var(--muted);
  }
  .pill.demo {
    color: #ffd666;
  }
  .pill.disconnected {
    color: var(--bad);
    border-color: var(--bad);
  }
  .banner {
    padding: 0.6rem 1rem;
    border-radius: var(--radius);
    margin-bottom: 1rem;
    border: 1px solid;
  }
  .banner.demo {
    margin: 0;
    border-radius: 0;
    border-width: 0 0 1px;
    background: #3a2a00;
    color: #ffd666;
    text-align: center;
    font-weight: 600;
  }
  .banner.bad {
    border-color: var(--bad);
    color: var(--bad);
  }
  .banner.info {
    border-color: var(--info);
    color: var(--info);
  }
  .banner.warn {
    border-color: var(--warn);
    color: var(--warn);
  }
  .stale {
    opacity: 0.55;
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
    gap: 0.75rem;
  }
  @media (min-width: 480px) {
    .card.wide {
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
  .list {
    margin-top: 1.25rem;
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.9rem;
  }
  th,
  td {
    text-align: left;
    padding: 0.5rem 0.75rem;
    border-bottom: 1px solid var(--border);
    vertical-align: top;
  }
  th {
    background: var(--surface-2);
    font-weight: 600;
    white-space: nowrap;
  }
  .name {
    max-width: 22rem;
    overflow-wrap: anywhere;
  }
  .ellipsis {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .num {
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .state.running,
  .health.healthy {
    color: var(--ok);
  }
  .state.exited,
  .state.dead,
  .health.unhealthy {
    color: var(--bad);
  }
  .state.paused,
  .state.restarting,
  .health.starting {
    color: var(--warn);
  }
  .ok {
    color: var(--ok);
  }
  .warn {
    color: var(--warn);
  }
  .bad {
    color: var(--bad);
  }
  .muted {
    color: var(--muted);
  }
  .small {
    font-size: 0.8rem;
  }
</style>
