<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type Container, type Summary } from './lib/api';
  import { fmtDuration, fmtTime } from './lib/format';

  // Etapa 2: sondeo periódico del inventario (el agente no consulta Docker por
  // cada request). La etapa 5 lo reemplaza por SSE + snapshot.
  const POLL_MS = 3000;

  let summary = $state<Summary | null>(null);
  let containers = $state<Container[]>([]);
  let loadError = $state<string | null>(null);
  let loading = $state(true);

  async function refresh(signal: AbortSignal) {
    try {
      const [s, list] = await Promise.all([api.summary(signal), api.containers(signal)]);
      summary = s;
      containers = list.items;
      loadError = null;
    } catch (e) {
      if (signal.aborted) return;
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    const ctrl = new AbortController();
    void refresh(ctrl.signal);
    const t = setInterval(() => void refresh(ctrl.signal), POLL_MS);
    return () => {
      clearInterval(t);
      ctrl.abort();
    };
  });

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
  {#if loading}
    <p class="muted">Cargando…</p>
  {:else if loadError && !summary}
    <div class="banner bad" role="alert">No se pudo contactar al agente: {loadError}</div>
  {:else if summary}
    {#if loadError}
      <div class="banner warn" role="status">
        Sin respuesta del agente; los datos mostrados son de las {fmtTime(summary.generated_at)}.
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

    <section class="cards" class:stale={summary.stale} aria-label="Resumen">
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
      {:else}
        <div class="card"><span class="n muted">—</span><span>Contenedores (sin datos)</span></div>
      {/if}
    </section>

    {#if summary.synced}
      <section class="list" class:stale={summary.stale} aria-label="Contenedores">
        <table>
          <thead>
            <tr>
              <th>Nombre</th>
              <th>ID</th>
              <th>Estado</th>
              <th>Salud</th>
              <th title="Docker RestartCount / reinicios observados por el agente">Reinicios D / obs.</th>
              <th>Compose</th>
            </tr>
          </thead>
          <tbody>
            {#each containers as c (c.id)}
              <tr>
                <td>{c.name}<div class="muted small">{c.image}</div></td>
                <td><code>{c.short_id}</code></td>
                <td>
                  <span class="state {c.state}">{c.state}</span>
                  {#if c.uptime_seconds !== null}
                    <div class="muted small">activo {fmtDuration(c.uptime_seconds)}</div>
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
                <td class="num">{c.restart_count} / {c.observed_restarts}</td>
                <td class="muted">
                  {c.compose_project ? `${c.compose_project} / ${c.compose_service ?? '?'}` : '—'}
                </td>
              </tr>
            {:else}
              <tr><td colspan="6" class="muted">No hay contenedores.</td></tr>
            {/each}
          </tbody>
        </table>
      </section>
    {/if}

    <p class="muted small">
      {summary.stale ? 'Datos desactualizados de las' : 'Actualizado:'}
      {fmtTime(summary.stale ? summary.as_of : summary.generated_at)}
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
  .num {
    font-variant-numeric: tabular-nums;
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
