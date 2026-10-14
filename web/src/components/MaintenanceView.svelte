<script lang="ts">
  import { ApiError, api, type Maintenance } from '../lib/api';
  import { clock } from '../lib/clock.svelte';
  import { fmtDateTime, fmtDuration } from '../lib/format';
  import { live } from '../lib/live.svelte';

  let items = $state<Maintenance[]>([]);
  let error = $state<string | null>(null);
  let formError = $state<string | null>(null);
  let saving = $state(false);

  let scope = $state<'global' | 'container'>('global');
  let container = $state('');
  let minutes = $state(60);
  let reason = $state('');
  const PRESETS = [15, 30, 60, 120, 240, 480, 1440];

  async function load() {
    try {
      items = (await api.maintenance()).items;
      error = null;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }
  // Recarga cuando el resumen en vivo cambia (otro navegador pudo agregar o quitar).
  $effect(() => {
    void live.summary?.alerts?.maintenance.length;
    void load();
  });

  async function add(e: SubmitEvent) {
    e.preventDefault();
    formError = null;
    if (scope === 'container' && container.trim() === '') {
      formError = 'Indique el nombre del contenedor.';
      return;
    }
    saving = true;
    try {
      await api.addMaintenance({
        container: scope === 'container' ? container.trim() : '',
        duration_seconds: minutes * 60,
        reason: reason.trim(),
      });
      reason = '';
      await load();
    } catch (err) {
      formError = err instanceof ApiError ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  async function end(m: Maintenance) {
    try {
      await api.endMaintenance(m.id);
      await load();
    } catch (err) {
      error = err instanceof ApiError ? err.message : String(err);
    }
  }

  const names = $derived([...new Set(Object.values(live.containers).map((c) => c.name))].sort());
</script>

<h2>Modo mantenimiento</h2>
<p class="muted">
  Durante un mantenimiento el agente sigue recolectando métricas y evaluando reglas, pero los incidentes que se
  abren quedan marcados como «en mantenimiento» y no generan avisos. Vence solo.
</p>

<form class="form" onsubmit={add}>
  <div class="grid">
    <fieldset class="scope">
      <legend>Alcance</legend>
      <label class="check"><input type="radio" bind:group={scope} value="global" /> Global (todos los contenedores)</label>
      <label class="check"><input type="radio" bind:group={scope} value="container" /> Un contenedor</label>
      {#if scope === 'container'}
        <input list="ds-maint-names" bind:value={container} placeholder="nombre del contenedor" aria-label="Contenedor" />
        <datalist id="ds-maint-names">{#each names as n (n)}<option value={n}></option>{/each}</datalist>
      {/if}
    </fieldset>
    <label>
      Duración
      <select bind:value={minutes}>
        {#each PRESETS as p (p)}<option value={p}>{fmtDuration(p * 60)}</option>{/each}
      </select>
    </label>
    <label>Motivo <input maxlength="500" bind:value={reason} placeholder="p. ej. despliegue" /></label>
  </div>
  {#if formError}<div class="banner bad" role="alert">{formError}</div>{/if}
  <button type="submit" disabled={saving}>{saving ? 'Guardando…' : 'Iniciar mantenimiento'}</button>
</form>

{#if error}<div class="banner bad" role="alert">{error}</div>{/if}
<h3>Activos</h3>
{#if items.length === 0}
  <p class="muted">No hay mantenimientos activos.</p>
{:else}
  <ul class="list">
    {#each items as m (m.id)}
      <li>
        <strong>{m.global ? 'Global' : m.container}</strong>
        <span class="muted">
          hasta {fmtDateTime(m.until)} (faltan {fmtDuration((Date.parse(m.until) - clock.now) / 1000)}){m.reason ? ` · ${m.reason}` : ''}
        </span>
        <button type="button" onclick={() => end(m)}>Finalizar</button>
      </li>
    {/each}
  </ul>
{/if}

<style>
  h2 {
    margin: 0 0 0.5rem;
    font-size: 1.4rem;
  }
  h3 {
    font-size: 1.05rem;
  }
  .form {
    padding: 1rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
    margin: 1rem 0;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 16rem), 1fr));
    gap: 0.75rem;
    margin-bottom: 0.75rem;
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
  }
  fieldset {
    border: 1px solid var(--border);
    border-radius: var(--radius);
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  .list {
    list-style: none;
    padding: 0;
    display: grid;
    gap: 0.5rem;
  }
  .list li {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    align-items: center;
    padding: 0.6rem 0.8rem;
    background: var(--surface);
    border: 1px solid var(--border);
    border-left: 4px solid var(--info);
    border-radius: var(--radius);
  }
  .list button {
    margin-left: auto;
  }
</style>
