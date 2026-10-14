// Tipos y cliente de la API del agente. Mantener en sincronía con internal/api (DTOs Go).

export interface Counts {
  total: number;
  running: number;
  paused: number;
  restarting: number;
  stopped: number;
  unhealthy: number;
}

export interface Daemon {
  status: 'connecting' | 'connected' | 'disconnected';
  api_version?: string;
  since: string;
  last_error?: string;
  attempts?: number;
}

/** Qué tan actuales son los datos de contenedores. */
export interface DataState {
  /** false hasta el primer listado completo: sin datos = desconocido, no "ninguno". */
  synced: boolean;
  /** true si Docker no responde: los datos son el último estado conocido. */
  stale: boolean;
  as_of: string;
}

/** Suma de uso de contenedores en ejecución, con unidad y denominador explícitos. */
export interface Resources {
  /** Núcleos en uso (suma de cpu_percent/100). */
  cpu_cores_used: number | null;
  host_cpus: number;
  mem_used_bytes: number | null;
  host_mem_bytes: number;
  cpu_sampled: number;
  mem_sampled: number;
  running: number;
  interval_seconds: number;
}

export interface Maintenance {
  id: number;
  /** Nombre del contenedor; "" = global. */
  container: string;
  global: boolean;
  until: string;
  reason: string;
  created_at: string;
}

export interface AlertsSummary {
  open: number;
  /** Abiertos sin reconocer (reconocer no resuelve). */
  unacked: number;
  pending: number;
  maintenance: Maintenance[];
}

export interface Summary extends DataState {
  mode: 'docker' | 'demo';
  daemon: Daemon;
  /** null hasta la primera sincronización. */
  containers: Counts | null;
  resources: Resources | null;
  alerts: AlertsSummary | null;
  generated_at: string;
}

/** Última muestra. null = no disponible (nunca un cero inventado). */
export interface Metrics {
  at: string;
  /** % de un núcleo: 100 = 1 núcleo; puede llegar a online_cpus×100. */
  cpu_percent: number | null;
  online_cpus: number;
  mem_usage_bytes: number | null;
  mem_cache_excluded: boolean;
  /** Límite efectivo; si mem_limit_explicit es false, es la memoria del host. */
  mem_limit_bytes: number | null;
  mem_limit_explicit: boolean;
  mem_percent: number | null;
  net_rx_bytes: number | null;
  net_tx_bytes: number | null;
  net_rx_bps: number | null;
  net_tx_bps: number | null;
  blk_read_bytes: number | null;
  blk_write_bytes: number | null;
  blk_read_bps: number | null;
  blk_write_bps: number | null;
  pids: number | null;
}

export type Health = 'none' | 'starting' | 'healthy' | 'unhealthy';

export interface Container {
  id: string;
  short_id: string;
  name: string;
  image: string;
  state: string;
  health: Health;
  /** false si no está en ejecución: health es el último valor que guardó Docker. */
  health_current: boolean;
  created_at: string | null;
  started_at: string | null;
  finished_at: string | null;
  uptime_seconds: number | null;
  exit_code: number | null;
  oom_killed: boolean;
  restart_policy: string;
  /** Informado por Docker (reinicios por política). */
  restart_count: number;
  /** Observados por el agente (cualquier causa). */
  observed_restarts: number;
  last_observed_restart: string | null;
  compose_project?: string;
  compose_service?: string;
  mem_limit_bytes: number | null;
  cpu_limit_cores: number | null;
  metrics: Metrics | null;
}

export interface ContainerList extends DataState {
  items: Container[];
}

export interface ContainerDetail extends Container, DataState {
  image_id: string;
  labels: Record<string, string>;
  restart_max_retry: number;
  log_driver: string;
  tty: boolean;
  health_failing_streak: number;
  error?: string;
}

/** Punto de una serie reducida; null = sin datos en el intervalo. */
export interface SeriesPoint {
  t: string;
  n: number;
  cpu_avg: number | null;
  cpu_max: number | null;
  mem_avg: number | null;
  mem_max: number | null;
  mem_limit: number | null;
  mem_pct_avg: number | null;
  mem_pct_max: number | null;
  net_rx_bps: number | null;
  net_tx_bps: number | null;
  blk_read_bps: number | null;
  blk_write_bps: number | null;
  pids_max: number | null;
}

export interface Series {
  container_id: string;
  from: string;
  to: string;
  bucket_seconds: number;
  step_seconds: number;
  /** raw = muestras originales agrupadas; 1m = resúmenes por minuto. */
  source: 'raw' | '1m';
  points: SeriesPoint[];
}

export interface ApiErrorBody {
  error: { code: string; message: string };
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  // El agente exige JSON en toda modificación (ADR-012), también sin datos: se envía "{}".
  const mutation = method !== 'GET' && method !== 'HEAD';
  if (mutation) headers['Content-Type'] = 'application/json';
  const res = await fetch(path, {
    method,
    signal,
    headers,
    body: mutation ? JSON.stringify(body ?? {}) : undefined,
  });
  if (!res.ok) {
    let code = 'http_error';
    let message = `HTTP ${res.status}`;
    try {
      const err = (await res.json()) as ApiErrorBody;
      code = err.error.code;
      message = err.error.message;
    } catch {
      // cuerpo no JSON: conservar el mensaje genérico
    }
    throw new ApiError(res.status, code, message);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

const getJSON = <T>(path: string, signal?: AbortSignal) => request<T>('GET', path, undefined, signal);

// --- Alertas ---

export type RuleKind = 'not_running' | 'unhealthy' | 'restarts' | 'cpu' | 'memory';

export interface RuleInput {
  name: string;
  kind: RuleKind;
  enabled: boolean;
  /** Vacío = cualquiera. container es un nombre (sigue al contenedor si se recrea). */
  scope: { container: string; project: string; service: string };
  /** cpu: % de un núcleo; memory: % del límite; restarts: cantidad. */
  threshold: number;
  for_seconds: number;
  window_seconds: number;
  cooldown_seconds: number;
  recover_seconds: number;
  hysteresis: number;
}

export interface Rule extends RuleInput {
  id: number;
  threshold_unit: string;
  created_at: string;
  updated_at: string;
}

export interface Incident {
  id: number;
  rule_id: number;
  rule_name: string;
  kind: RuleKind;
  container_id: string;
  container_name: string;
  state: 'open' | 'resolved';
  started_at: string;
  opened_at: string;
  resolved_at: string | null;
  /** Reconocer no resuelve. */
  acked_at: string | null;
  /** Abierto durante un mantenimiento: sin aviso. */
  suppressed: boolean;
  summary: string;
  /** Causa probable, nunca afirmada como cierta. */
  cause: string;
  resolution: string;
  peak: number | null;
}

export interface IncidentPage {
  items: Incident[];
  total: number;
  limit: number;
  offset: number;
}

export interface Pending {
  rule_id: number;
  rule_name: string;
  kind: RuleKind;
  container_id: string;
  container_name: string;
  since: string;
  opens_at: string;
  in_cooldown: boolean;
  value: number | null;
}

export interface MaintenanceInput {
  /** "" = global. */
  container: string;
  duration_seconds?: number;
  until?: string;
  reason: string;
}

// --- Logs ---

export interface LogLine {
  time: string | null;
  /** stdout | stderr | tty (TTY: no se distinguen) | system (mensaje del daemon). */
  stream: 'stdout' | 'stderr' | 'tty' | 'system';
  /** Texto saneado: mostrar siempre como texto, nunca como HTML. */
  text: string;
  truncated?: boolean;
}

export interface LogsResponse {
  container_id: string;
  tty: boolean;
  log_driver: string;
  lines: LogLine[];
  limited: boolean;
}

const enc = encodeURIComponent;

export const api = {
  summary: (signal?: AbortSignal) => getJSON<Summary>('/api/summary', signal),
  containers: (signal?: AbortSignal) => getJSON<ContainerList>('/api/containers', signal),
  container: (id: string, signal?: AbortSignal) =>
    getJSON<ContainerDetail>(`/api/containers/${encodeURIComponent(id)}`, signal),
  series: (id: string, range: string, points: number, signal?: AbortSignal) =>
    getJSON<Series>(
      `/api/containers/${encodeURIComponent(id)}/series?range=${encodeURIComponent(range)}&points=${points}`,
      signal,
    ),
  logs: (id: string, tail: number, signal?: AbortSignal) =>
    getJSON<LogsResponse>(`/api/containers/${enc(id)}/logs?tail=${tail}`, signal),

  rules: (signal?: AbortSignal) => getJSON<{ items: Rule[] }>('/api/rules', signal),
  createRule: (r: RuleInput) => request<Rule>('POST', '/api/rules', r),
  updateRule: (id: number, r: RuleInput) => request<Rule>('PUT', `/api/rules/${id}`, r),
  deleteRule: (id: number) => request<void>('DELETE', `/api/rules/${id}`),

  incidents: (q: { state?: string; container?: string; limit?: number; offset?: number }, signal?: AbortSignal) => {
    const p = new URLSearchParams();
    for (const [k, v] of Object.entries(q)) if (v !== undefined && v !== '') p.set(k, String(v));
    return getJSON<IncidentPage>(`/api/incidents?${p}`, signal);
  },
  pending: (signal?: AbortSignal) => getJSON<{ items: Pending[] }>('/api/incidents/pending', signal),
  ack: (id: number) => request<Incident>('POST', `/api/incidents/${id}/ack`),

  maintenance: (signal?: AbortSignal) => getJSON<{ items: Maintenance[] }>('/api/maintenance', signal),
  addMaintenance: (m: MaintenanceInput) => request<Maintenance>('POST', '/api/maintenance', m),
  endMaintenance: (id: number) => request<void>('DELETE', `/api/maintenance/${id}`),
};
