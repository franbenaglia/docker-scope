<script lang="ts">
  import { onMount } from 'svelte';
  import { ApiError, api, type Rule, type RuleInput, type RuleKind } from '../lib/api';
  import { fmtDuration } from '../lib/format';
  import { live } from '../lib/live.svelte';

  let rules = $state<Rule[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let editing = $state<{ id: number | null; form: RuleInput } | null>(null);
  let formError = $state<string | null>(null);
  let saving = $state(false);

  async function load() {
    try {
      rules = (await api.rules()).items;
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }
  onMount(() => void load());

  const KINDS: { v: RuleKind; label: string; help: string }[] = [
    { v: 'unhealthy', label: 'Unhealthy persistente', help: 'El healthcheck está en unhealthy durante al menos la duración mínima.' },
    { v: 'not_running', label: 'Dejó de ejecutarse', help: 'Un contenedor vigilado (que el agente vio en ejecución) se detiene o se elimina. Requiere alcance.' },
    { v: 'restarts', label: 'Reinicios repetidos', help: 'Al menos N reinicios observados por el agente dentro de la ventana.' },
    { v: 'cpu', label: 'CPU alta', help: 'CPU por encima del umbral, en % de un núcleo (100 = 1 núcleo; 250 = 2,5 núcleos).' },
    { v: 'memory', label: 'Memoria alta', help: 'Memoria por encima del umbral, en % del límite (o de la memoria del host si no hay límite).' },
  ];
  const kindLabel = (k: RuleKind) => KINDS.find((x) => x.v === k)?.label ?? k;

  const blank = (): RuleInput => ({
    name: '',
    kind: 'unhealthy',
    enabled: true,
    scope: { container: '', project: '', service: '' },
    threshold: 0,
    for_seconds: 30,
    window_seconds: 300,
    cooldown_seconds: 300,
    recover_seconds: 0,
    hysteresis: 0,
  });

  function create() {
    formError = null;
    editing = { id: null, form: blank() };
  }
  function edit(r: Rule) {
    formError = null;
    const { id, threshold_unit: _u, created_at: _c, updated_at: _up, ...form } = r;
    editing = { id, form: structuredClone(form) };
  }

  // Valores por defecto razonables al cambiar de tipo en una regla nueva.
  function onKind() {
    if (!editing || editing.id !== null) return;
    const f = editing.form;
    if (f.kind === 'cpu') Object.assign(f, { threshold: 90, hysteresis: 10, for_seconds: 120 });
    else if (f.kind === 'memory') Object.assign(f, { threshold: 80, hysteresis: 5, for_seconds: 60 });
    else if (f.kind === 'restarts') Object.assign(f, { threshold: 3, window_seconds: 300, for_seconds: 0, hysteresis: 0 });
    else Object.assign(f, { threshold: 0, hysteresis: 0, for_seconds: f.kind === 'not_running' ? 10 : 30 });
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!editing) return;
    saving = true;
    formError = null;
    const f = editing.form;
    const body: RuleInput = {
      ...f,
      name: f.name.trim(),
      scope: { container: f.scope.container.trim(), project: f.scope.project.trim(), service: f.scope.service.trim() },
    };
    try {
      if (editing.id === null) await api.createRule(body);
      else await api.updateRule(editing.id, body);
      editing = null;
      await load();
    } catch (err) {
      formError = err instanceof ApiError ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  async function toggle(r: Rule) {
    const { id, threshold_unit: _u, created_at: _c, updated_at: _up, ...form } = r;
    try {
      await api.updateRule(id, { ...form, enabled: !r.enabled });
      await load();
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    }
  }

  async function remove(r: Rule) {
    if (!confirm(`¿Eliminar la regla «${r.name}»? Sus incidentes abiertos se resolverán; el historial se conserva.`)) return;
    try {
      await api.deleteRule(r.id);
      if (editing?.id === r.id) editing = null;
      await load();
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    }
  }

  function scopeText(r: Rule): string {
    const parts = [];
    if (r.scope.container) parts.push(`contenedor ${r.scope.container}`);
    if (r.scope.project) parts.push(`proyecto ${r.scope.project}`);
    if (r.scope.service) parts.push(`servicio ${r.scope.service}`);
    return parts.length ? parts.join(' · ') : 'todos los contenedores';
  }
  function thresholdText(r: Rule): string {
    switch (r.kind) {
      case 'cpu':
        return `> ${r.threshold}% de 1 núcleo` + (r.hysteresis ? ` (resuelve < ${r.threshold - r.hysteresis}%)` : '');
      case 'memory':
        return `> ${r.threshold}% del límite` + (r.hysteresis ? ` (resuelve < ${r.threshold - r.hysteresis}%)` : '');
      case 'restarts':
        return `≥ ${r.threshold} en ${fmtDuration(r.window_seconds)}`;
      default:
        return '—';
    }
  }

  const names = $derived([...new Set(Object.values(live.containers).map((c) => c.name))].sort());
  const projects = $derived([...new Set(Object.values(live.containers).map((c) => c.compose_project ?? '').filter(Boolean))].sort());
  const human = (s: number) => (s > 0 ? `= ${fmtDuration(s)}` : '');
</script>

<header class="head">
  <h2>Reglas de alerta</h2>
  {#if !editing}<button type="button" onclick={create}>Nueva regla</button>{/if}
</header>

{#if editing}
  {@const f = editing.form}
  <form class="form" onsubmit={save}>
    <h3>{editing.id === null ? 'Nueva regla' : 'Editar regla'}</h3>
    {#if editing.id !== null}
      <p class="muted small">Guardar los cambios resuelve los incidentes abiertos de esta regla ("regla modificada") y reinicia sus pendientes.</p>
    {/if}
    <div class="grid">
      <label>Nombre <input required maxlength="100" bind:value={f.name} /></label>
      <label>
        Tipo
        <select bind:value={f.kind} onchange={onKind}>
          {#each KINDS as k (k.v)}<option value={k.v}>{k.label}</option>{/each}
        </select>
      </label>
      <label class="check"><input type="checkbox" bind:checked={f.enabled} /> Habilitada</label>
    </div>
    <p class="muted small">{KINDS.find((k) => k.v === f.kind)?.help}</p>

    <fieldset>
      <legend>Alcance <span class="muted small">(vacío = cualquiera)</span></legend>
      <div class="grid">
        <label>Contenedor (nombre) <input list="ds-names" bind:value={f.scope.container} placeholder="p. ej. api" /></label>
        <label>Proyecto Compose <input list="ds-projects" bind:value={f.scope.project} /></label>
        <label>Servicio Compose <input bind:value={f.scope.service} /></label>
      </div>
    </fieldset>

    <div class="grid">
      {#if f.kind === 'cpu' || f.kind === 'memory' || f.kind === 'restarts'}
        <label>
          Umbral
          <input type="number" step="any" min="0" bind:value={f.threshold} />
          <span class="muted small">{f.kind === 'cpu' ? '% de 1 núcleo' : f.kind === 'memory' ? '% del límite' : 'reinicios'}</span>
        </label>
      {/if}
      {#if f.kind === 'cpu' || f.kind === 'memory'}
        <label>
          Histéresis
          <input type="number" step="any" min="0" bind:value={f.hysteresis} />
          <span class="muted small">resuelve por debajo de {f.threshold - f.hysteresis}%</span>
        </label>
      {/if}
      {#if f.kind === 'restarts'}
        <label>Ventana (s) <input type="number" min="60" bind:value={f.window_seconds} /><span class="muted small">{human(f.window_seconds)}</span></label>
      {:else}
        <label>Duración mínima (s) <input type="number" min="0" bind:value={f.for_seconds} /><span class="muted small">{human(f.for_seconds)}</span></label>
      {/if}
      <label>Cooldown (s) <input type="number" min="0" bind:value={f.cooldown_seconds} /><span class="muted small">{human(f.cooldown_seconds)}</span></label>
      <label>Recuperación (s) <input type="number" min="0" bind:value={f.recover_seconds} /><span class="muted small">{f.recover_seconds > 0 ? `${human(f.recover_seconds)} despejado antes de resolver` : 'resuelve en cuanto se despeja'}</span></label>
    </div>

    {#if formError}<div class="banner bad" role="alert">{formError}</div>{/if}
    <div class="actions">
      <button type="submit" disabled={saving}>{saving ? 'Guardando…' : 'Guardar'}</button>
      <button type="button" onclick={() => (editing = null)}>Cancelar</button>
    </div>
    <datalist id="ds-names">{#each names as n (n)}<option value={n}></option>{/each}</datalist>
    <datalist id="ds-projects">{#each projects as p (p)}<option value={p}></option>{/each}</datalist>
  </form>
{/if}

{#if loading}
  <p class="muted">Cargando…</p>
{:else if error}
  <div class="banner bad" role="alert">{error}</div>
{/if}
{#if rules.length > 0}
  <div class="list">
    <table>
      <thead>
        <tr><th>Regla</th><th>Tipo</th><th>Alcance</th><th>Umbral</th><th>Tiempos</th><th>Estado</th><th></th></tr>
      </thead>
      <tbody>
        {#each rules as r (r.id)}
          <tr class:off={!r.enabled}>
            <td><strong>{r.name}</strong></td>
            <td>{kindLabel(r.kind)}</td>
            <td>{scopeText(r)}</td>
            <td class="num">{thresholdText(r)}</td>
            <td class="small">
              {#if r.kind !== 'restarts'}duración {fmtDuration(r.for_seconds)} · {/if}cooldown {fmtDuration(r.cooldown_seconds)}
              {#if r.recover_seconds}· recuperación {fmtDuration(r.recover_seconds)}{/if}
            </td>
            <td>
              <button type="button" class="link" onclick={() => toggle(r)} title="Cambiar">{r.enabled ? 'habilitada' : 'deshabilitada'}</button>
            </td>
            <td class="row-actions">
              <button type="button" onclick={() => edit(r)}>Editar</button>
              <button type="button" onclick={() => remove(r)}>Eliminar</button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else if !loading}
  <p class="muted">No hay reglas.</p>
{/if}

<style>
  .head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 1rem;
  }
  h2 {
    margin: 0;
    font-size: 1.4rem;
  }
  h3 {
    margin: 0 0 0.5rem;
  }
  .form {
    padding: 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    margin-bottom: 1.25rem;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 14rem), 1fr));
    gap: 0.75rem;
    margin-bottom: 0.5rem;
  }
  label {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.9rem;
  }
  label.check {
    flex-direction: row;
    align-items: center;
    align-self: end;
  }
  fieldset {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    margin: 0.5rem 0 0.75rem;
  }
  .actions {
    display: flex;
    gap: 0.5rem;
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
  th {
    background: var(--surface-2);
  }
  tr.off td:not(.row-actions) {
    opacity: 0.6;
  }
  .row-actions {
    white-space: nowrap;
  }
</style>
