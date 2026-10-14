<script lang="ts">
  import { ApiError, api, type Incident, type Pending, type RuleKind } from '../lib/api';
  import { clock } from '../lib/clock.svelte';
  import { fmtDateTime, fmtDuration, fmtPercent } from '../lib/format';
  import { live } from '../lib/live.svelte';
  import { notifier } from '../lib/notify.svelte';
  import { containerHref } from '../lib/router.svelte';

  const PAGE = 50;
  type Tab = 'open' | 'resolved' | 'all';
  let tab = $state<Tab>('open');
  let items = $state<Incident[]>([]);
  let total = $state(0);
  let shownCount = $state(PAGE);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let pending = $state<Pending[]>([]);
  let actionError = $state<string | null>(null);

  async function load(signal: AbortSignal) {
    try {
      const page = await api.incidents({ state: tab, limit: Math.min(shownCount, 200) }, signal);
      items = page.items;
      total = page.total;
      error = null;
    } catch (e) {
      if (!signal.aborted) error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  // Recarga al cambiar de pestaña, al pedir más y ante cada evento de incidentes.
  $effect(() => {
    void live.incidentsVersion;
    void tab;
    void shownCount;
    const ctl = new AbortController();
    void load(ctl.signal);
    return () => ctl.abort();
  });

  // Los pendientes cambian sin eventos (son estado en memoria del motor): sondeo.
  $effect(() => {
    const ctl = new AbortController();
    const refresh = async () => {
      try {
        pending = (await api.pending(ctl.signal)).items;
      } catch {
        // se reintenta en el próximo ciclo
      }
    };
    void refresh();
    const t = setInterval(refresh, 3000);
    return () => {
      clearInterval(t);
      ctl.abort();
    };
  });

  async function ack(inc: Incident) {
    actionError = null;
    try {
      await api.ack(inc.id);
    } catch (e) {
      actionError = e instanceof ApiError ? e.message : String(e);
    }
  }

  function setTab(t: Tab) {
    tab = t;
    shownCount = PAGE;
    loading = true;
  }

  const kindLabel: Record<RuleKind, string> = {
    not_running: 'Dejó de ejecutarse',
    unhealthy: 'Unhealthy',
    restarts: 'Reinicios',
    cpu: 'CPU',
    memory: 'Memoria',
  };

  function value(kind: RuleKind, v: number | null): string {
    if (v === null) return '';
    switch (kind) {
      case 'cpu':
        return `${fmtPercent(v)} de 1 núcleo`;
      case 'memory':
        return `${fmtPercent(v)} del límite`;
      case 'restarts':
        return `${v} reinicios`;
      default:
        return '';
    }
  }

  const duration = (inc: Incident) =>
    fmtDuration(((inc.resolved_at ? Date.parse(inc.resolved_at) : clock.now) - Date.parse(inc.started_at)) / 1000);
  const exists = (id: string) => id in live.containers;
</script>

<header class="head">
  <h2>Incidentes</h2>
  <div class="notify">
    {#if !notifier.supported}
      <span class="muted small">Este navegador no admite notificaciones.</span>
    {:else if notifier.permission === 'denied'}
      <span class="muted small">Notificaciones bloqueadas en el navegador.</span>
    {:else if notifier.active}
      <span class="small ok">Notificaciones del navegador activas</span>
      <button type="button" onclick={() => notifier.disable()}>Desactivar</button>
    {:else}
      <button type="button" onclick={() => notifier.enable()} title="Avisa de incidentes nuevos que no estén en mantenimiento">
        Activar notificaciones del navegador
      </button>
    {/if}
  </div>
</header>

{#if pending.length > 0}
  <section class="pending" aria-label="Condiciones pendientes">
    <h3>Pendientes ({pending.length})</h3>
    <p class="muted small">La condición se cumple pero todavía no alcanzó la duración mínima (o está en cooldown).</p>
    <ul>
      {#each pending as p (p.rule_id + ':' + p.container_id)}
        <li>
          <strong>{p.container_name}</strong> · {p.rule_name}
          {#if p.value !== null}<span class="muted">({value(p.kind, p.value)})</span>{/if}
          <span class="muted small">
            · desde {fmtDuration((clock.now - Date.parse(p.since)) / 1000)}
            · {p.in_cooldown ? 'en cooldown hasta' : 'abre a las'} {new Date(p.opens_at).toLocaleTimeString()}
          </span>
        </li>
      {/each}
    </ul>
  </section>
{/if}

<div class="tabs" role="tablist">
  {#each [['open', 'Abiertos'], ['resolved', 'Resueltos'], ['all', 'Todos']] as [t, label] (t)}
    <button type="button" role="tab" aria-selected={tab === t} class:active={tab === t} onclick={() => setTab(t as Tab)}>{label}</button>
  {/each}
  <span class="muted small">{total} en total</span>
</div>

{#if actionError}<div class="banner bad" role="alert">{actionError}</div>{/if}

{#if loading && items.length === 0}
  <p class="muted">Cargando…</p>
{:else if error}
  <div class="banner bad" role="alert">No se pudo cargar el historial: {error}</div>
{:else if items.length === 0}
  <p class="muted empty">{tab === 'open' ? 'No hay incidentes abiertos.' : 'No hay incidentes.'}</p>
{:else}
  <ul class="list">
    {#each items as inc (inc.id)}
      <li class="inc" class:open={inc.state === 'open'} class:suppressed={inc.suppressed}>
        <div class="top">
          <span class="kind">{kindLabel[inc.kind] ?? inc.kind}</span>
          {#if exists(inc.container_id)}
            <a href={containerHref(inc.container_id)}><strong>{inc.container_name}</strong></a>
          {:else}
            <strong>{inc.container_name}</strong> <span class="muted small">(ya no existe)</span>
          {/if}
          <span class="badge {inc.state}">{inc.state === 'open' ? 'abierto' : 'resuelto'}</span>
          {#if inc.suppressed}<span class="badge maint" title="Se abrió durante un mantenimiento: no generó aviso">en mantenimiento</span>{/if}
          {#if inc.acked_at}<span class="badge acked" title="Reconocido: no implica que esté resuelto">reconocido</span>{/if}
          <span class="muted small rule">regla «{inc.rule_name}» · #{inc.id}</span>
        </div>
        <p class="summary">{inc.summary}</p>
        {#if inc.cause}<p class="cause"><span class="muted">Posible causa:</span> {inc.cause}</p>{/if}
        <p class="muted small">
          Inicio de la condición {fmtDateTime(inc.started_at)} · abierto {fmtDateTime(inc.opened_at)}
          {#if inc.resolved_at}· resuelto {fmtDateTime(inc.resolved_at)}{inc.resolution ? ` (${inc.resolution})` : ''}{/if}
          · duración {duration(inc)}
          {#if inc.peak !== null && value(inc.kind, inc.peak)}· pico {value(inc.kind, inc.peak)}{/if}
        </p>
        {#if inc.state === 'open' && !inc.acked_at}
          <button type="button" onclick={() => ack(inc)} title="Indica que alguien lo está mirando; no lo resuelve">Reconocer</button>
        {/if}
      </li>
    {/each}
  </ul>
  {#if items.length < total && shownCount < 200}
    <button type="button" onclick={() => (shownCount += PAGE)}>Mostrar más</button>
  {:else if items.length < total}
    <p class="muted small">Se muestran los {items.length} más recientes.</p>
  {/if}
{/if}

<style>
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    margin-bottom: 1rem;
  }
  h2 {
    margin: 0;
    font-size: 1.4rem;
  }
  h3 {
    margin: 0 0 0.25rem;
    font-size: 1rem;
  }
  .notify {
    display: flex;
    gap: 0.5rem;
    align-items: center;
  }
  .pending {
    padding: 0.75rem 1rem;
    border: 1px dashed var(--warn);
    border-radius: var(--radius);
    margin-bottom: 1rem;
  }
  .pending ul {
    margin: 0.5rem 0 0;
    padding-left: 1.2rem;
  }
  .tabs {
    display: flex;
    gap: 0.4rem;
    align-items: center;
    margin-bottom: 0.75rem;
  }
  .tabs button.active {
    background: var(--info);
    color: var(--bg);
    border-color: var(--info);
  }
  .list {
    list-style: none;
    padding: 0;
    margin: 0 0 1rem;
    display: grid;
    gap: 0.6rem;
  }
  .inc {
    padding: 0.75rem 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-left: 4px solid var(--muted);
    border-radius: var(--radius);
  }
  .inc.open {
    border-left-color: var(--bad);
  }
  .inc.suppressed {
    border-left-color: var(--info);
  }
  .top {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    align-items: baseline;
  }
  .kind {
    font-size: 0.8rem;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    color: var(--muted);
  }
  .rule {
    margin-left: auto;
  }
  .badge {
    font-size: 0.75rem;
    padding: 0.05rem 0.45rem;
    border-radius: 999px;
    border: 1px solid var(--border);
  }
  .badge.open {
    color: var(--bad);
    border-color: var(--bad);
  }
  .badge.resolved {
    color: var(--ok);
  }
  .badge.maint {
    color: var(--info);
  }
  .badge.acked {
    color: var(--warn);
  }
  .summary {
    margin: 0.4rem 0 0.2rem;
  }
  .cause {
    margin: 0 0 0.3rem;
    font-size: 0.9rem;
  }
  .empty {
    padding: 2rem 0;
  }
</style>
