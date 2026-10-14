<script lang="ts">
  import { tick } from 'svelte';
  import { ApiError, api, type LogLine } from '../lib/api';

  interface Props {
    id: string;
  }
  let { id }: Props = $props();

  // Límites del visor (SPEC: buffers acotados por líneas y bytes).
  const MAX_LINES = 5000;
  const MAX_BYTES = 4 << 20;
  const RENDER_LIMIT = 2000; // líneas dibujadas como máximo (las más recientes que coinciden)
  const TAILS = [100, 200, 500, 1000, 5000];

  type Entry = LogLine & { seq: number; ms: number | null };
  type Status = 'connecting' | 'live' | 'reconnecting' | 'ended' | 'error';

  let tail = $state(200);
  let status = $state<Status>('connecting');
  let message = $state('');
  let tty = $state(false);
  let driver = $state('');

  let lines = $state.raw<Entry[]>([]);
  let pending = $state.raw<Entry[]>([]); // recibidas en pausa
  let clientDropped = $state(0);
  let serverDropped = $state(0);
  let paused = $state(false);
  let autoscroll = $state(true);
  let query = $state('');
  let showOut = $state(true);
  let showErr = $state(true);

  let box: HTMLDivElement;
  let es: EventSource | null = null;
  let seq = 0;
  let lastMs: number | null = null; // última marca de tiempo recibida
  let dedupeUntil: number | null = null; // tras reconectar, descartar lo ya visto

  // Conserva las líneas más nuevas que entran en los límites; cuenta las descartadas.
  function bound(list: Entry[]): Entry[] {
    let size = 0;
    let start = list.length;
    while (start > 0) {
      const len = list[start - 1]!.text.length;
      const kept = list.length - start;
      if (kept >= MAX_LINES || (kept > 0 && size + len > MAX_BYTES)) break;
      size += len;
      start--;
    }
    if (start === 0) return list;
    clientDropped += start;
    return list.slice(start);
  }

  function receive(batch: LogLine[]) {
    const fresh: Entry[] = [];
    for (const l of batch) {
      const ms = l.time ? Date.parse(l.time) : null;
      if (dedupeUntil !== null && ms !== null && ms <= dedupeUntil) continue;
      if (ms !== null) lastMs = ms;
      fresh.push({ ...l, seq: seq++, ms });
    }
    dedupeUntil = null;
    if (fresh.length === 0) return;
    if (paused) pending = bound([...pending, ...fresh]);
    else lines = bound([...lines, ...fresh]);
  }

  async function explainFailure() {
    try {
      await api.logs(id, 0);
      message = 'No se pudo abrir el stream de logs (¿demasiados streams abiertos?). Reintente en unos segundos.';
    } catch (e) {
      message = e instanceof ApiError ? e.message : 'No se pudo contactar al agente.';
    }
    status = 'error';
  }

  function open() {
    close();
    status = 'connecting';
    message = '';
    if (lastMs !== null) dedupeUntil = lastMs;
    const src = new EventSource(`/api/containers/${encodeURIComponent(id)}/logs/stream?tail=${tail}`);
    es = src;
    let greeted = false;
    src.addEventListener('hello', (e) => {
      const h = JSON.parse((e as MessageEvent<string>).data) as { tty: boolean; log_driver: string };
      greeted = true;
      tty = h.tty;
      driver = h.log_driver;
      status = 'live';
    });
    src.addEventListener('lines', (e) => receive((JSON.parse((e as MessageEvent<string>).data) as { lines: LogLine[] }).lines));
    src.addEventListener('dropped', (e) => (serverDropped += (JSON.parse((e as MessageEvent<string>).data) as { count: number }).count));
    src.addEventListener('end', (e) => {
      const d = JSON.parse((e as MessageEvent<string>).data) as { reason: string; message: string };
      status = d.reason === 'error' ? 'error' : 'ended';
      message = d.message;
      close(); // evita que EventSource reconecte solo y repita el tail
    });
    src.onerror = () => {
      if (es !== src) return;
      if (!greeted) {
        close();
        void explainFailure();
      } else if (src.readyState === EventSource.CLOSED) {
        status = 'error';
        message = 'Se cerró la conexión con el agente.';
        close();
      } else {
        status = 'reconnecting'; // EventSource reintenta; al volver se descartan las líneas repetidas
        if (lastMs !== null) dedupeUntil = lastMs;
      }
    };
  }

  function close() {
    es?.close();
    es = null;
  }

  // Abrir al montar y al cambiar el tail; cerrar al desmontar (cambiar de contenedor).
  $effect(() => {
    void tail;
    lines = [];
    pending = [];
    clientDropped = serverDropped = 0;
    lastMs = null;
    open();
    return close;
  });

  function togglePause() {
    paused = !paused;
    if (!paused && pending.length > 0) {
      lines = bound([...lines, ...pending]);
      pending = [];
    }
  }

  function clearView() {
    lines = [];
    pending = [];
    clientDropped = 0;
    serverDropped = 0;
  }

  const q = $derived(query.trim().toLowerCase());
  const matching = $derived(
    lines.filter((l) => {
      if (l.stream === 'stdout' && !showOut) return false;
      if (l.stream === 'stderr' && !showErr) return false;
      return q === '' || l.text.toLowerCase().includes(q);
    }),
  );
  const shown = $derived(matching.length > RENDER_LIMIT ? matching.slice(-RENDER_LIMIT) : matching);

  // Segmentos para resaltar coincidencias sin interpretar HTML.
  function segments(text: string): { t: string; hit: boolean }[] {
    if (q === '') return [{ t: text, hit: false }];
    const out: { t: string; hit: boolean }[] = [];
    const low = text.toLowerCase();
    let i = 0;
    for (let j = low.indexOf(q); j >= 0; j = low.indexOf(q, i)) {
      if (j > i) out.push({ t: text.slice(i, j), hit: false });
      out.push({ t: text.slice(j, j + q.length), hit: true });
      i = j + q.length;
    }
    if (i < text.length) out.push({ t: text.slice(i), hit: false });
    return out;
  }

  const fmtTs = (ms: number | null) =>
    ms === null ? '' : new Date(ms).toLocaleTimeString(undefined, { hour12: false }) + '.' + String(ms % 1000).padStart(3, '0');

  // Autoscroll: seguir el final salvo que la persona suba a leer.
  $effect(() => {
    void shown;
    if (!autoscroll || !box) return;
    void tick().then(() => box && (box.scrollTop = box.scrollHeight));
  });
  function onScroll() {
    const atBottom = box.scrollHeight - box.scrollTop - box.clientHeight < 30;
    if (!atBottom && autoscroll) autoscroll = false;
    else if (atBottom && !autoscroll && !paused) autoscroll = true;
  }

  const statusText: Record<Status, string> = {
    connecting: 'conectando…',
    live: 'siguiendo',
    reconnecting: 'reconectando…',
    ended: 'stream terminado',
    error: 'error',
  };
</script>

<section class="logs" aria-label="Logs">
  <div class="bar">
    <h3>Logs</h3>
    <span class="status {status}">{statusText[status]}</span>
    {#if driver}<span class="muted small">driver {driver}{tty ? ' · TTY: stdout y stderr no se distinguen' : ''}</span>{/if}
  </div>
  <div class="bar controls">
    <input type="search" placeholder="Buscar en el buffer…" bind:value={query} aria-label="Buscar en los logs" />
    <button type="button" onclick={togglePause} aria-pressed={paused}>{paused ? `Reanudar${pending.length ? ` (${pending.length} nuevas)` : ''}` : 'Pausar'}</button>
    <button type="button" onclick={clearView}>Limpiar</button>
    <label class="check"><input type="checkbox" bind:checked={autoscroll} /> Autoscroll</label>
    {#if !tty}
      <label class="check"><input type="checkbox" bind:checked={showOut} /> stdout</label>
      <label class="check"><input type="checkbox" bind:checked={showErr} /> stderr</label>
    {/if}
    <label class="check">
      Últimas
      <select bind:value={tail} aria-label="Líneas iniciales">
        {#each TAILS as t (t)}<option value={t}>{t}</option>{/each}
      </select>
    </label>
    {#if status === 'ended' || status === 'error'}
      <button type="button" onclick={open}>Volver a seguir</button>
    {/if}
  </div>

  {#if message}
    <div class="banner {status === 'error' ? 'bad' : 'info'}" role="status">{message}</div>
  {/if}
  {#if clientDropped > 0 || serverDropped > 0}
    <p class="muted small">
      {#if clientDropped > 0}{clientDropped} líneas antiguas descartadas del visor (máx. {MAX_LINES} líneas / {MAX_BYTES >> 20} MiB).{/if}
      {#if serverDropped > 0}{serverDropped} líneas no llegaron porque el navegador iba atrasado.{/if}
    </p>
  {/if}

  <!-- Área desplazable: necesita foco para recorrerla con teclado (WCAG 2.1.1). -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="box" bind:this={box} onscroll={onScroll} role="log" aria-live="off" tabindex="0">
    {#each shown as l (l.seq)}
      <div class="line {l.stream}">
        <span class="ts">{fmtTs(l.ms)}</span>
        {#if l.stream !== 'tty'}<span class="stream">{l.stream === 'stdout' ? 'out' : l.stream === 'stderr' ? 'err' : 'sys'}</span>{/if}
        <span class="text">{#each segments(l.text) as s, i (i)}{#if s.hit}<mark>{s.t}</mark>{:else}{s.t}{/if}{/each}{#if l.truncated}<span class="trunc" title="Línea truncada a 8 KiB"> …[truncada]</span>{/if}</span>
      </div>
    {:else}
      <p class="muted empty">
        {#if lines.length > 0}Ninguna línea coincide.{:else if status === 'connecting'}Cargando…{:else}Sin líneas.{/if}
      </p>
    {/each}
  </div>
  <p class="muted small">
    {matching.length} de {lines.length} líneas{matching.length > RENDER_LIMIT ? ` (se muestran las últimas ${RENDER_LIMIT})` : ''} · los logs no se guardan
  </p>
</section>

<style>
  .logs {
    margin-top: 1.5rem;
  }
  .bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.5rem;
    margin-bottom: 0.5rem;
  }
  h3 {
    margin: 0;
    font-size: 1.05rem;
  }
  .controls input[type='search'] {
    flex: 1 1 14rem;
    min-width: 0;
  }
  .check {
    display: inline-flex;
    gap: 0.3rem;
    align-items: center;
    font-size: 0.9rem;
  }
  .status {
    font-size: 0.8rem;
    padding: 0.1rem 0.5rem;
    border-radius: 999px;
    border: 1px solid var(--border);
  }
  .status.live {
    color: var(--ok);
  }
  .status.reconnecting,
  .status.connecting {
    color: var(--warn);
  }
  .status.error {
    color: var(--bad);
  }
  .box {
    height: 420px;
    overflow: auto;
    background: #0a0d10;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    padding: 0.5rem;
    font-family: var(--mono);
    font-size: 0.8rem;
    line-height: 1.4;
  }
  .line {
    display: flex;
    gap: 0.6rem;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .ts {
    color: var(--muted);
    flex: none;
  }
  .stream {
    flex: none;
    color: var(--muted);
    width: 2rem;
  }
  .stderr .stream,
  .stderr .text {
    color: #ff9b94;
  }
  .system .text {
    color: var(--warn);
  }
  .text {
    min-width: 0;
  }
  mark {
    background: #6b4f00;
    color: inherit;
  }
  .trunc {
    color: var(--muted);
  }
  .empty {
    margin: 1rem;
  }
</style>
