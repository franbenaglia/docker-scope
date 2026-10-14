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
  status: 'connected' | 'disconnected';
  api_version?: string;
  error?: string;
}

export interface Summary {
  mode: 'docker' | 'demo';
  daemon: Daemon;
  /** null cuando Docker no responde: desconocido, no cero. */
  containers: Counts | null;
  generated_at: string;
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
};
