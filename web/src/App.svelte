<script lang="ts">
  import { onMount } from 'svelte';
  import { api, type Summary } from './lib/api';

  // Etapa 1: sondeo periódico. La etapa 5 lo reemplaza por SSE + snapshot.
  const POLL_MS = 5000;

  let summary = $state<Summary | null>(null);
  let loadError = $state<string | null>(null);
  let loading = $state(true);

  async function refresh(signal: AbortSignal) {
    try {
      summary = await api.summary(signal);
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

  const fmtTime = (iso: string) => new Date(iso).toLocaleTimeString();
</script>

<header>
  <h1>DockerScope</h1>
  {#if summary?.mode === 'demo'}
    <span class="daemon demo">Fuente simulada</span>
  {:else if summary}
    <span class="daemon {summary.daemon.status}">
      {summary.daemon.status === 'connected'
        ? `Docker conectado (API ${summary.daemon.api_version ?? '?'})`
        : 'Docker desconectado'}
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
        Sin respuesta del agente; mostrando datos de las {fmtTime(summary.generated_at)}.
      </div>
    {/if}
    {#if summary.daemon.status === 'disconnected'}
      <div class="banner bad" role="alert">
        Docker no responde. El estado de los contenedores es <strong>desconocido</strong> (no
        significa que estén detenidos).
      </div>
    {/if}

    <section class="cards" aria-label="Resumen">
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

    <p class="muted small">Actualizado: {fmtTime(summary.generated_at)}</p>
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
  .daemon {
    font-size: 0.85rem;
    padding: 0.2rem 0.6rem;
    border-radius: 999px;
    border: 1px solid var(--border);
  }
  .daemon.connected {
    color: var(--ok);
  }
  .daemon.demo {
    color: #ffd666;
  }
  .daemon.disconnected {
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
