<script lang="ts">
  import { onMount } from 'svelte';
  import ContainerDetail from './components/ContainerDetail.svelte';
  import ContainerTable from './components/ContainerTable.svelte';
  import IncidentsView from './components/IncidentsView.svelte';
  import MaintenanceView from './components/MaintenanceView.svelte';
  import RulesView from './components/RulesView.svelte';
  import StatusBanners from './components/StatusBanners.svelte';
  import SummaryCards from './components/SummaryCards.svelte';
  import { clock } from './lib/clock.svelte';
  import { fmtTime } from './lib/format';
  import { live } from './lib/live.svelte';
  import { router } from './lib/router.svelte';

  // Datos en vivo: snapshot REST + eventos SSE (ver lib/live.svelte.ts).
  onMount(() => {
    live.start();
    const stopClock = clock.use();
    return () => {
      live.stop();
      stopClock();
    };
  });

  const summary = $derived(live.summary);
  const agentLost = $derived(live.stream === 'reconnecting');
  const dim = $derived(!!summary?.stale || agentLost);
</script>

<header class="top">
  <a class="brand" href="#/">DockerScope</a>
  {#if summary?.mode === 'demo'}
    <span class="pill demo">Fuente simulada</span>
  {:else if summary}
    {@const d = summary.daemon}
    <span class="pill {d.status}" title={d.last_error ?? ''}>
      {#if d.status === 'connected'}
        Docker conectado
      {:else if d.status === 'connecting'}
        Conectando a Docker…
      {:else}
        Docker desconectado · reintentando{d.attempts ? ` (${d.attempts})` : ''}
      {/if}
    </span>
  {/if}
  <nav class="nav" aria-label="Secciones">
    <a href="#/" aria-current={router.route.name === 'overview' || router.route.name === 'container' ? 'page' : undefined}>Contenedores</a>
    <a href="#/incidents" aria-current={router.route.name === 'incidents' ? 'page' : undefined}>
      Incidentes
      {#if summary?.alerts && summary.alerts.open > 0}
        <span class="count" title="{summary.alerts.open} abiertos, {summary.alerts.unacked} sin reconocer">{summary.alerts.open}</span>
      {/if}
    </a>
    <a href="#/rules" aria-current={router.route.name === 'rules' ? 'page' : undefined}>Reglas</a>
    <a href="#/maintenance" aria-current={router.route.name === 'maintenance' ? 'page' : undefined}>
      Mantenimiento{#if summary?.alerts && summary.alerts.maintenance.length > 0}<span class="count info">{summary.alerts.maintenance.length}</span>{/if}
    </a>
  </nav>
  <span class="live muted small" title="Conexión en vivo con el agente">
    <span class="dot" class:on={live.stream === 'live'}></span>
    {live.stream === 'live' ? 'en vivo' : live.stream === 'connecting' ? 'conectando' : 'reconectando'}
  </span>
</header>

{#if summary?.mode === 'demo'}
  <div class="demo-banner" role="status">MODO DEMO — datos simulados, no corresponden a Docker real</div>
{/if}

<main>
  {#if !summary && agentLost}
    <div class="banner bad" role="alert">No se pudo contactar al agente. Reintentando…</div>
  {:else if !summary && live.error}
    <div class="banner bad" role="alert">Error al obtener datos del agente: {live.error}</div>
  {:else if !summary}
    <p class="muted">Cargando…</p>
  {:else}
    <StatusBanners {summary} {agentLost} />
    {#if router.route.name === 'overview'}
      <SummaryCards {summary} {dim} />
      {#if summary.synced}
        <ContainerTable containers={live.sorted} {dim} />
      {/if}
      <p class="muted small footer">
        {#if summary.stale}
          Datos desactualizados de las {fmtTime(summary.as_of)}
        {:else}
          Actualizado: {fmtTime(summary.generated_at)}
        {/if}
      </p>
    {:else if router.route.name === 'container'}
      {#key router.route.id}
        <ContainerDetail id={router.route.id} />
      {/key}
    {:else if router.route.name === 'incidents'}
      <IncidentsView />
    {:else if router.route.name === 'rules'}
      <RulesView />
    {:else if router.route.name === 'maintenance'}
      <MaintenanceView />
    {:else}
      <p>Página inexistente. <a href="#/">Volver al resumen</a></p>
    {/if}
  {/if}
</main>

<style>
  .top {
    display: flex;
    align-items: center;
    gap: 1rem;
    flex-wrap: wrap;
    padding: 0.75rem 1.25rem;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }
  .brand {
    font-size: 1.15rem;
    font-weight: 700;
    color: var(--text);
    text-decoration: none;
  }
  .nav {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }
  .nav a {
    color: var(--muted);
    text-decoration: none;
    padding: 0.25rem 0.6rem;
    border-radius: 6px;
  }
  .nav a:hover {
    color: var(--text);
  }
  .nav a[aria-current='page'] {
    color: var(--text);
    background: var(--surface-2);
  }
  .count {
    margin-left: 0.3rem;
    font-size: 0.75rem;
    padding: 0 0.4rem;
    border-radius: 999px;
    background: var(--bad);
    color: #fff;
  }
  .count.info {
    background: var(--info);
    color: var(--bg);
  }
  .live {
    margin-left: auto;
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
  }
  .dot {
    width: 0.55rem;
    height: 0.55rem;
    border-radius: 50%;
    background: var(--warn);
  }
  .dot.on {
    background: var(--ok);
  }
  main {
    padding: 1.25rem;
    max-width: 1400px;
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
  .demo-banner {
    padding: 0.5rem 1rem;
    background: #3a2a00;
    color: #ffd666;
    text-align: center;
    font-weight: 600;
    border-bottom: 1px solid #6b4f00;
  }
  .footer {
    margin-top: 0.75rem;
  }
  @media (max-width: 600px) {
    main {
      padding: 0.75rem;
    }
  }
</style>
