// Estado en vivo del panel: snapshot REST + eventos SSE de /api/stream.
//
// SSE no garantiza entrega: tras cada (re)conexión y ante "resync" se vuelve a
// pedir el snapshot. Los eventos que llegan mientras el snapshot está en
// curso se guardan y se aplican después, en orden: cada evento trae el estado
// completo del objeto, así que el resultado final es siempre el más reciente.

import { api, type Container, type Daemon, type Metrics, type Summary } from './api';

export type StreamStatus = 'connecting' | 'live' | 'reconnecting';

interface Hello {
  server_time: string;
  mode: 'docker' | 'demo';
  interval_seconds: number;
  heartbeat_seconds: number;
}

type Ev = { name: string; data: unknown };

const EVENTS = ['hello', 'summary', 'daemon', 'container', 'container_removed', 'metrics', 'incident', 'heartbeat', 'resync'];

class Live {
  summary = $state<Summary | null>(null);
  containers = $state<Record<string, Container>>({});
  stream = $state<StreamStatus>('connecting');
  /** Error del último snapshot, si falló. */
  error = $state<string | null>(null);
  /** Momento (ms) del último dato recibido del agente. */
  lastSeen = $state(0);

  sorted = $derived(Object.values(this.containers).sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id)));

  #es: EventSource | null = null;
  #pending: Ev[] | null = null; // eventos recibidos durante un snapshot
  #heartbeatMs = 15000;
  #watchdog: ReturnType<typeof setInterval> | null = null;
  #retry: ReturnType<typeof setTimeout> | null = null;
  #snapshotCtl: AbortController | null = null;

  start() {
    this.#open();
    this.#watchdog = setInterval(() => this.#checkAlive(), 5000);
  }

  stop() {
    this.#es?.close();
    this.#es = null;
    this.#snapshotCtl?.abort();
    if (this.#watchdog) clearInterval(this.#watchdog);
    if (this.#retry) clearTimeout(this.#retry);
  }

  #open() {
    this.#es?.close();
    const es = new EventSource('/api/stream');
    this.#es = es;
    for (const name of EVENTS) {
      es.addEventListener(name, (e) => this.#onEvent(name, (e as MessageEvent<string>).data));
    }
    es.onerror = () => {
      if (this.#es !== es) return;
      this.stream = 'reconnecting';
      // EventSource reintenta solo salvo que el servidor responda con error
      // HTTP (p. ej. 503 por demasiadas conexiones): entonces reabrimos a mano.
      if (es.readyState === EventSource.CLOSED && !this.#retry) {
        this.#retry = setTimeout(() => {
          this.#retry = null;
          this.#open();
        }, 5000);
      }
    };
  }

  /** Reabre el stream si no llegó nada durante 2,5 heartbeats. */
  #checkAlive() {
    if (this.stream === 'live' && Date.now() - this.lastSeen > this.#heartbeatMs * 2.5) {
      this.stream = 'reconnecting';
      this.#open();
    }
  }

  #onEvent(name: string, raw: string) {
    let data: unknown;
    try {
      data = JSON.parse(raw);
    } catch {
      return;
    }
    this.lastSeen = Date.now();
    if (name === 'hello') {
      const h = data as Hello;
      this.#heartbeatMs = h.heartbeat_seconds * 1000;
      this.stream = 'live';
      void this.#snapshot();
      return;
    }
    if (name === 'resync') {
      void this.#snapshot();
      return;
    }
    if (this.#pending) {
      this.#pending.push({ name, data });
      return;
    }
    this.#apply({ name, data });
  }

  async #snapshot() {
    this.#snapshotCtl?.abort();
    const ctl = new AbortController();
    this.#snapshotCtl = ctl;
    this.#pending ??= [];
    try {
      const [s, list] = await Promise.all([api.summary(ctl.signal), api.containers(ctl.signal)]);
      const next: Record<string, Container> = {};
      for (const c of list.items) next[c.id] = c;
      this.summary = s;
      this.containers = next;
      this.error = null;
    } catch (e) {
      if (ctl.signal.aborted) return;
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      if (this.#snapshotCtl === ctl) {
        const queued = this.#pending ?? [];
        this.#pending = null;
        for (const ev of queued) this.#apply(ev);
      }
    }
  }

  #apply({ name, data }: Ev) {
    switch (name) {
      case 'summary':
        this.summary = data as Summary;
        break;
      case 'daemon':
        if (this.summary) this.summary.daemon = data as Daemon;
        break;
      case 'container': {
        const c = data as Container;
        this.containers[c.id] = c;
        break;
      }
      case 'container_removed':
        delete this.containers[(data as { id: string }).id];
        break;
      case 'metrics': {
        const items = (data as { items: Record<string, Metrics> }).items;
        // Ausente en la ronda = sin muestra actual (detenido o falló), no "igual que antes".
        for (const c of Object.values(this.containers)) {
          c.metrics = items[c.id] ?? null;
        }
        break;
      }
    }
  }
}

export const live = new Live();
