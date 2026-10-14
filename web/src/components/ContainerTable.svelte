<script lang="ts">
  import type { Container } from '../lib/api';
  import { since } from '../lib/clock.svelte';
  import { fmtBytes, fmtCores, fmtDuration, fmtPercent, fmtRate } from '../lib/format';
  import { containerHref } from '../lib/router.svelte';

  interface Props {
    containers: Container[];
    dim: boolean;
  }
  let { containers, dim }: Props = $props();

  type SortKey = 'name' | 'state' | 'cpu' | 'mem' | 'uptime' | 'restarts';
  type Prefs = { q: string; state: string; health: string; project: string; sort: SortKey; desc: boolean; grouped: boolean };

  const KEY = 'dockerscope.table';
  const defaults: Prefs = { q: '', state: 'all', health: 'all', project: '', sort: 'name', desc: false, grouped: true };
  function load(): Prefs {
    try {
      return { ...defaults, ...(JSON.parse(localStorage.getItem(KEY) ?? '{}') as Partial<Prefs>) };
    } catch {
      return defaults;
    }
  }
  let p = $state<Prefs>(load());
  $effect(() => {
    const snapshot = JSON.stringify(p);
    try {
      localStorage.setItem(KEY, snapshot);
    } catch {
      // almacenamiento no disponible (modo privado): las preferencias no se recuerdan
    }
  });

  const NO_PROJECT = '\u0000';
  const stateGroup = (s: string) => (['running', 'paused', 'restarting'].includes(s) ? s : 'stopped');
  const currentHealth = (c: Container) => (c.health === 'none' ? 'none' : c.health_current ? c.health : 'stale');

  const projects = $derived([...new Set(containers.map((c) => c.compose_project ?? ''))].sort());

  const filtered = $derived.by(() => {
    const q = p.q.trim().toLowerCase();
    return containers.filter((c) => {
      if (p.state !== 'all' && stateGroup(c.state) !== p.state) return false;
      if (p.health !== 'all' && currentHealth(c) !== p.health) return false;
      if (p.project && (c.compose_project ?? '') !== (p.project === NO_PROJECT ? '' : p.project)) return false;
      if (!q) return true;
      return (
        c.name.toLowerCase().includes(q) ||
        c.image.toLowerCase().includes(q) ||
        c.id.startsWith(q) ||
        (c.compose_project ?? '').toLowerCase().includes(q) ||
        (c.compose_service ?? '').toLowerCase().includes(q)
      );
    });
  });

  // Valor numérico de orden; null = sin dato (siempre al final).
  function key(c: Container): number | string | null {
    switch (p.sort) {
      case 'name':
        return c.name;
      case 'state':
        return stateGroup(c.state);
      case 'cpu':
        return c.metrics?.cpu_percent ?? null;
      case 'mem':
        return c.metrics?.mem_usage_bytes ?? null;
      case 'uptime':
        return c.started_at && c.state === 'running' ? -Date.parse(c.started_at) : null;
      case 'restarts':
        return c.observed_restarts;
    }
  }

  const sorted = $derived.by(() => {
    const dir = p.desc ? -1 : 1;
    return [...filtered].sort((a, b) => {
      const ka = key(a);
      const kb = key(b);
      if (ka === null && kb === null) return a.name.localeCompare(b.name);
      if (ka === null) return 1;
      if (kb === null) return -1;
      const r = typeof ka === 'string' ? ka.localeCompare(kb as string) : ka - (kb as number);
      return r === 0 ? a.name.localeCompare(b.name) : r * dir;
    });
  });

  type Group = { project: string; items: Container[]; running: number; cores: number | null; mem: number | null };
  const groups = $derived.by((): Group[] => {
    if (!p.grouped) return [{ project: '', items: sorted, running: 0, cores: null, mem: null }];
    const by = new Map<string, Container[]>();
    for (const c of sorted) {
      const k = c.compose_project ?? '';
      by.set(k, [...(by.get(k) ?? []), c]);
    }
    return [...by.entries()]
      .sort(([a], [b]) => (a === '' ? 1 : b === '' ? -1 : a.localeCompare(b)))
      .map(([project, items]) => {
        let cores: number | null = null;
        let mem: number | null = null;
        for (const c of items) {
          if (c.metrics?.cpu_percent != null) cores = (cores ?? 0) + c.metrics.cpu_percent / 100;
          if (c.metrics?.mem_usage_bytes != null) mem = (mem ?? 0) + c.metrics.mem_usage_bytes;
        }
        return { project, items, running: items.filter((c) => c.state === 'running').length, cores, mem };
      });
  });

  function sortBy(k: SortKey) {
    if (p.sort === k) p.desc = !p.desc;
    else {
      p.sort = k;
      p.desc = k !== 'name' && k !== 'state'; // métricas: mayor primero
    }
  }
  const ariaSort = (k: SortKey) => (p.sort === k ? (p.desc ? 'descending' : 'ascending') : 'none');
  const arrow = (k: SortKey) => (p.sort === k ? (p.desc ? ' ▼' : ' ▲') : '');
  const filtersActive = $derived(p.q !== '' || p.state !== 'all' || p.health !== 'all' || p.project !== '');

  const healthLabel: Record<string, string> = {
    none: 'Sin healthcheck',
    starting: 'Iniciando',
    healthy: 'Healthy',
    unhealthy: 'Unhealthy',
  };
</script>

<section class="toolbar" aria-label="Filtros">
  <input type="search" placeholder="Buscar nombre, imagen, ID, Compose…" bind:value={p.q} aria-label="Buscar" />
  <select bind:value={p.state} aria-label="Estado">
    <option value="all">Todos los estados</option>
    <option value="running">En ejecución</option>
    <option value="stopped">Detenidos</option>
    <option value="paused">Pausados</option>
    <option value="restarting">Reiniciando</option>
  </select>
  <select bind:value={p.health} aria-label="Salud">
    <option value="all">Toda salud</option>
    <option value="unhealthy">Unhealthy</option>
    <option value="healthy">Healthy</option>
    <option value="starting">Iniciando</option>
    <option value="none">Sin healthcheck</option>
    <option value="stale">Salud no actual (detenidos)</option>
  </select>
  <select bind:value={p.project} aria-label="Proyecto Compose">
    <option value="">Todos los proyectos</option>
    {#each projects as pr (pr)}
      {#if pr === ''}
        <option value={NO_PROJECT}>Sin Compose</option>
      {:else}
        <option value={pr}>{pr}</option>
      {/if}
    {/each}
  </select>
  <label class="check"><input type="checkbox" bind:checked={p.grouped} /> Agrupar por Compose</label>
  {#if filtersActive}
    <button type="button" class="link" onclick={() => (p = { ...p, q: '', state: 'all', health: 'all', project: '' })}>
      Limpiar filtros
    </button>
  {/if}
  <span class="muted small count">{filtered.length} de {containers.length}</span>
</section>

<section class="list" class:stale={dim} aria-label="Contenedores">
  <table>
    <thead>
      <tr>
        <th aria-sort={ariaSort('name')}><button type="button" onclick={() => sortBy('name')}>Nombre{arrow('name')}</button></th>
        <th>ID</th>
        <th aria-sort={ariaSort('state')}><button type="button" onclick={() => sortBy('state')}>Estado{arrow('state')}</button></th>
        <th>Salud</th>
        <th aria-sort={ariaSort('cpu')} title="% de un núcleo: 100% = 1 CPU completa; puede superar 100% en hosts multinúcleo">
          <button type="button" onclick={() => sortBy('cpu')}>CPU (% 1 núcleo){arrow('cpu')}</button>
        </th>
        <th aria-sort={ariaSort('mem')} title="Uso sin caché de página inactiva / límite">
          <button type="button" onclick={() => sortBy('mem')}>Memoria{arrow('mem')}</button>
        </th>
        <th title="Tasa de red recibida / enviada">Red ↓ / ↑</th>
        <th title="Tráfico de block I/O leído / escrito (no es espacio en disco)">Disco L / E</th>
        <th aria-sort={ariaSort('restarts')} title="RestartCount de Docker / reinicios observados por el agente">
          <button type="button" onclick={() => sortBy('restarts')}>Reinicios D / obs.{arrow('restarts')}</button>
        </th>
      </tr>
    </thead>
    {#each groups as g (g.project)}
      <tbody>
        {#if p.grouped}
          <tr class="group">
            <th colspan="9" scope="rowgroup">
              {g.project || 'Sin Compose'}
              <span class="muted small">
                · {g.running}/{g.items.length} en ejecución
                {#if g.cores !== null}· CPU {fmtCores(g.cores)} núcleos{/if}
                {#if g.mem !== null}· memoria {fmtBytes(g.mem)}{/if}
              </span>
            </th>
          </tr>
        {/if}
        {#each g.items as c (c.id)}
          <tr>
            <td class="name">
              <a href={containerHref(c.id)}>{c.name}</a>
              <div class="muted small ellipsis" title={c.image}>{c.image}</div>
              {#if !p.grouped && c.compose_project}
                <div class="muted small">{c.compose_project} / {c.compose_service ?? '?'}</div>
              {:else if p.grouped && c.compose_service}
                <div class="muted small">servicio {c.compose_service}</div>
              {/if}
            </td>
            <td><code>{c.short_id}</code></td>
            <td>
              <span class="state {c.state}">{c.state}</span>
              {#if c.state === 'running' && c.started_at}
                <div class="muted small">activo {fmtDuration(since(c.started_at))}</div>
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
                <span class="muted" title="El contenedor no está en ejecución">último: {healthLabel[c.health]}</span>
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
                    {m.mem_limit_explicit ? `${fmtPercent(m.mem_percent)} de ${fmtBytes(m.mem_limit_bytes)}` : 'sin límite'}
                  </div>
                {/if}
              </td>
              <td class="num">
                {m.net_rx_bps !== null ? fmtRate(m.net_rx_bps) : '—'}
                <div class="muted small">{m.net_tx_bps !== null ? fmtRate(m.net_tx_bps) : '—'}</div>
              </td>
              {#if m.blk_read_bytes === null}
                <td class="muted" title="Docker no informa block I/O para este contenedor (en cgroup v2 ocurre mientras no haya hecho I/O)">n/d</td>
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
          </tr>
        {/each}
      </tbody>
    {:else}
      <tbody>
        <tr>
          <td colspan="9" class="empty muted">
            {#if containers.length === 0}
              No hay contenedores en este host.
            {:else}
              Ningún contenedor coincide con los filtros.
              <button type="button" class="link" onclick={() => (p = { ...p, q: '', state: 'all', health: 'all', project: '' })}>
                Limpiar filtros
              </button>
            {/if}
          </td>
        </tr>
      </tbody>
    {/each}
  </table>
</section>

<style>
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    align-items: center;
    margin: 1.25rem 0 0.75rem;
  }
  .toolbar input[type='search'] {
    flex: 1 1 16rem;
    min-width: 0;
  }
  .check {
    display: inline-flex;
    gap: 0.35rem;
    align-items: center;
    font-size: 0.9rem;
  }
  .count {
    margin-left: auto;
  }
  .list {
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
  thead th {
    background: var(--surface-2);
    font-weight: 600;
    white-space: nowrap;
    position: sticky;
    top: 0;
  }
  thead button {
    all: unset;
    cursor: pointer;
  }
  thead button:focus-visible {
    outline: 2px solid var(--info);
  }
  tr.group th {
    background: var(--surface);
    font-weight: 600;
    border-top: 2px solid var(--border);
  }
  .name {
    max-width: 22rem;
    overflow-wrap: anywhere;
  }
  .name a {
    font-weight: 600;
    text-decoration: none;
  }
  .name a:hover {
    text-decoration: underline;
  }
  .empty {
    text-align: center;
    padding: 2rem;
  }
</style>
