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

export interface Summary extends DataState {
  mode: 'docker' | 'demo';
  daemon: Daemon;
  /** null hasta la primera sincronización. */
  containers: Counts | null;
  resources: Resources | null;
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

async function getJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, { signal, headers: { Accept: 'application/json' } });
  if (!res.ok) {
    let code = 'http_error';
    let message = `HTTP ${res.status}`;
    try {
      const body = (await res.json()) as ApiErrorBody;
      code = body.error.code;
      message = body.error.message;
    } catch {
      // cuerpo no JSON: conservar el mensaje genérico
    }
    throw new ApiError(res.status, code, message);
  }
  return (await res.json()) as T;
}

export const api = {
  summary: (signal?: AbortSignal) => getJSON<Summary>('/api/summary', signal),
  containers: (signal?: AbortSignal) => getJSON<ContainerList>('/api/containers', signal),
};
