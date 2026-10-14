# Arquitectura

> Mantener actualizado al crear/cambiar paquetes, interfaces, endpoints o eventos SSE.
> Estado: etapas 1–7 implementadas (config, docker, dockertest, inventory, collector, store, sse, alerts, logs, demo, api, web).

## Estructura de directorios (prevista)
```
cmd/dockerscope/        main: flags, config, wiring, apagado ordenado
internal/config/        carga y validación de configuración
internal/docker/        interfaz docker.API + cliente real (moby SDK)
internal/docker/dockertest/  daemon falso en memoria (tests y modo demo)
internal/inventory/     estado de contenedores en memoria, eventos, reconciliación
internal/collector/     muestreo de métricas, cálculo de deltas/tasas
internal/store/         SQLite: migraciones, samples, incidents, rules, maintenance
internal/alerts/        motor de reglas e incidentes
internal/logs/          parser de frames/TTY con timestamps, sanitizado, buffer acotado
internal/api/           handlers REST, DTOs, validación, middleware de seguridad
internal/sse/           hub SSE con colas acotadas
internal/demo/          fuente de datos simulada
web/                    Svelte + TS + Vite (build → web/dist, embebido)
deploy/                 systemd, compose de prueba, config de ejemplo
```

## Flujo de datos
```
Docker daemon ──► docker.Client ──► inventory (eventos + reconciliación)
                                 └► collector (ticker único) ──► snapshot en memoria
                                                              ├► store (samples)
                                                              ├► alerts ──► store (incidents)
                                                              └► sse.Hub ──► navegadores
Navegador ──► api (REST: snapshot/series/config) ; /api/stream (general) ; /api/containers/{id}/logs/stream
```

## Interfaces clave
- `docker.API` ([internal/docker/docker.go](../internal/docker/docker.go)): `Ping` (renegocia la versión
  de la API en cada llamada), `ListContainers`, `InspectContainer` (devuelve `docker.ErrNotFound`),
  `Events(since)`, `ContainerStats` (one-shot), `Info` (NCPU, MemTotal, cgroup). Implementaciones:
  `docker.Client` y `dockertest.Fake` (con `StatsFunc`/`StatsDelay` y métricas de concurrencia).
  `ContainerLogs(id, {Tail, Follow})` → stream crudo (`ErrLogsUnsupported` si el driver no permite leer);
  el fake usa `LogsFunc` y `dockertest.Mux` para armar frames.
- `logs.Parse(r, {TTY, Timestamps, MaxLineBytes}, emit)`, `logs.Sanitize`, `logs.Buffer`. Ver ADR-014.
- `sse.Stream`: escritor SSE para una respuesta (headers, retry, deadline por escritura); lo usan el hub y el stream de logs.
- `inventory.Inventory` ([internal/inventory](../internal/inventory/inventory.go)):
  - Una goroutine (`Run`) muta el estado; `Snapshot()` y `Get(id)` devuelven copias.
  - Sesión: ping → suscripción a eventos (con `since` = último evento procesado) → reconciliación
    (listado + inspect con concurrencia 8) → conectado → eventos y resync cada 60 s.
  - Cualquier error termina la sesión → `disconnected` (se conservan los datos, `Stale`) → backoff
    con jitter de 0,5 s a 30 s.
  - Eventos: dedup por (id, acción, timeNano) sobre los últimos 4096; cada evento relevante → inspect
    (idempotente). `destroy`/not found → Removed.
  - Listeners `OnChange(Change)` / `OnConnection(ConnState)`: se ejecutan en la goroutine de Run y no deben bloquear.
    `Change.Initial` = primera sincronización (estado preexistente, no transición); `Change.Event`
    es nil si el cambio se detectó por reconciliación.
- `collector.Collector` ([internal/collector](../internal/collector/collector.go)): `Run` hace una
  ronda por intervalo, `Latest(id)`/`LatestAll()` son copias, y `OnRound(Round)` es el listener para la
  persistencia (etapa 4), las alertas (6) y el SSE (5). Cálculos puros en `calc.go` (`extract` + `compute`), ver ADR-010.
- `store.Store` ([internal/store](../internal/store/store.go)): `Open(dir, name)` con migraciones,
  `InsertSamples`, `UpsertContainers`, `Rollup`, `Prune`, `Series(SeriesQuery)`, `ContainerKnown`.
  `store.Writer`: un único goroutine de escritura, `EnqueueSamples(Round)` (vía `collector.OnRound`) y
  `EnqueueContainers` (vía `inventory.OnChange`); rollup cada minuto y prune cada hora. Ver ADR-011.
- `alerts.Engine` ([internal/alerts](../internal/alerts/engine.go)): `Load` (reglas, mantenimiento, abiertos),
  `Observe` (vía `inventory.OnChange`: eventos recientes y exitCode), `Run`/`Tick(now)`, `OnEvent(Event{opened|resolved|acked})`,
  CRUD de reglas, `Ack`, `AddMaintenance`/`EndMaintenance`, `Pending`, `Status`. Persistencia: interfaz
  `alerts.Store`, implementada por `store.Store` ([internal/store/alerts.go](../internal/store/alerts.go)). Ver ADR-013.
- Config ([internal/config](../internal/config/config.go)): valores por defecto < JSON (`--config`) < flags.
  Ejemplo en [deploy/config.example.json](../deploy/config.example.json).
- Frontend embebido: paquete `web` (`web.Dist()`), `//go:embed all:dist`. `web/dist/.gitkeep`
  existe para que `go build` funcione sin compilar antes el panel.

## API REST
Errores: `{"error":{"code":"...","message":"..."}}` (code en snake_case). JSON con `Cache-Control: no-store`.
Rutas no-API: archivos de `web/dist`; si no existen, `index.html` (rutas de la SPA). `assets/*` son inmutables.

| Método | Ruta | Respuesta | Notas |
|---|---|---|---|
| GET | `/api/summary` | `SummaryDTO` | `daemon` {status connecting\|connected\|disconnected, since, last_error, attempts}; `synced`/`stale`/`as_of`; `containers` es `null` hasta la primera sincronización (desconocido ≠ 0). `unhealthy` cuenta sólo los que están en ejecución. |
| GET | `/api/containers` | `ContainerListDTO` | Filtros `q` (nombre/imagen/compose o prefijo de ID), `state` (running,paused,restarting,stopped), `health` (none,starting,healthy,unhealthy: salud actual), `project`/`service` (`project=` = fuera de Compose); `sort` name\|state\|cpu\|mem\|uptime\|restarts\|project, `order` asc\|desc (los valores ausentes van siempre al final); `limit` 1–500, `offset`. Respuesta: `total`, `limit`, `offset`, `projects[]`, `synced`/`stale`/`as_of` + `items[]`. Las fechas van en UTC o `null`; `health_current`, `restart_count` (Docker) vs. `observed_restarts` (agente); `mem_limit_bytes`/`cpu_limit_cores` configurados o null; `metrics` (MetricsDTO) o null. Sin filtros ni paginación todavía (etapa 5). |
| GET | `/api/containers/{id}` | `ContainerDetailDTO` | Sólo por ID completo (un prefijo da 404). ContainerDTO + image_id, labels, restart_max_retry, log_driver, tty, health_failing_streak, error. |
| GET | `/api/stream` | SSE | Ver "Eventos SSE". |
| GET | `/api/containers/{id}/series` | `SeriesDTO` | `?range=15m\|6h\|7d` o `?from=&to=` (RFC 3339), `points` 10–1000 (300). 400 `invalid_parameter`/`invalid_range`, 404 si el contenedor no tiene historial. `source` raw\|1m, `bucket_seconds`, `points[]` {t, n, cpu_avg/max, mem_avg/max/limit, mem_pct_avg/max, net_*_bps, blk_*_bps, pids_max}. |
| GET | `/api/containers/{id}/logs` | LogsDTO | `tail` 0–5000 (200). {container_id, tty, log_driver, lines[{time, stream stdout\|stderr\|tty\|system, text, truncated}], limited}. 409 `logs_unavailable`, 502 `docker_error`. |
| GET | `/api/containers/{id}/logs/stream` | SSE | `tail` igual. Eventos: hello {container_id, container_name, tty, log_driver, tail}, lines {lines[]}, dropped {count}, heartbeat, end {reason, message}. 503 `too_many_streams` (máx. 8). |
| GET | `/api/rules` | {items: RuleDTO[]} | RuleDTO: id, name, kind, enabled, scope{container,project,service}, threshold, threshold_unit, for/window/cooldown/recover_seconds, hysteresis, created_at, updated_at. |
| POST | `/api/rules` | 201 RuleDTO | Body RuleInput (JSON estricto, máx. 64 KiB). 400 `validation_error`/`invalid_json`. |
| PUT | `/api/rules/{id}` | RuleDTO | Resuelve los abiertos de la regla ("regla modificada"). 404 si no existe. |
| DELETE | `/api/rules/{id}` | 204 | Resuelve los abiertos ("regla eliminada"). |
| GET | `/api/incidents` | {items, total, limit, offset} | `state` open\|resolved\|all, `container` (ID), `limit` 1–200 (50), `offset`. Más recientes primero. |
| GET | `/api/incidents/pending` | {items: PendingDTO[]} | since, opens_at, in_cooldown, value. |
| POST | `/api/incidents/{id}/ack` | IncidentDTO | Idempotente; no resuelve. |
| GET | `/api/maintenance` | {items: MaintenanceDTO[]} | Activos. |
| POST | `/api/maintenance` | 201 MaintenanceDTO | {container?: nombre, duration_seconds \| until, reason}. Máx. 30 días. |
| DELETE | `/api/maintenance/{id}` | 204 | |
| * | `/api/*` desconocida | 404 `not_found` | |

`SummaryDTO.resources`: `cpu_cores_used` (núcleos), `host_cpus`, `mem_used_bytes`, `host_mem_bytes`,
`cpu_sampled`/`mem_sampled`/`running`, `interval_seconds`. `MetricsDTO`: `cpu_percent` (% de 1 núcleo),
`mem_*` (uso sin caché, límite efectivo, explícito o no, %), `net_*_bytes` (acumulados) y `net_*_bps`, `blk_*`, `pids`.
Todos los valores numéricos pueden ser null (no disponible).

_(completar el resto en la etapa 5)_

## Seguridad HTTP
`api.Security.Middleware` envuelve todo (ADR-012): Host loopback, `/api` cross-site → 403, mutaciones con
Origin propio o `allowed_origins` + JSON, cabeceras CSP/nosniff/etc., nunca CORS.

## Eventos SSE
`GET /api/stream` (ADR-012). Empieza con `retry: 3000`. Cada evento: `id`, `event`, `data` (JSON en una línea).

| Evento | Data | Cuándo |
|---|---|---|
| `hello` | {server_time, mode, interval_seconds, heartbeat_seconds} | Al conectar. El cliente pide el snapshot REST. |
| `summary` | SummaryDTO | Tras cada ronda; con debounce de 250 ms tras cambios |
| `daemon` | DaemonDTO | Cambio de conexión con Docker |
| `container` | ContainerDTO | Alta o cambio |
| `container_removed` | {id} | Baja |
| `metrics` | {at, items: {id: MetricsDTO}} | Tras cada ronda; un id ausente = sin muestra actual |
| `heartbeat` | {t} | Cada 15 s |
| `resync` | {reason} | Desborde de la cola del cliente: volver a pedir el snapshot |
| `incident` | {type: opened\|resolved\|acked, incident: IncidentDTO} | Cambios de incidentes (seguido de `summary`) |

Frontend: [web/src/lib/live.svelte.ts](../web/src/lib/live.svelte.ts).

## Esquema SQLite
Ver [internal/store/migrations](../internal/store/migrations/). Tiempos en ms Unix UTC.
- `containers(id PK, name, image, compose_project, compose_service, first_seen, last_seen, removed_at)`
- `samples(container_id, ts) PK WITHOUT ROWID`: cpu_pct, mem_used, mem_limit, mem_limit_explicit, mem_pct,
  net_rx/tx_bps, blk_read/write_bps, pids. Índice por ts.
- `samples_1m(container_id, ts) PK`: n, cpu_avg/max, mem_avg/max, mem_limit, mem_pct_avg/max, tasas, pids_max.
- `settings(key PK, value)`: p. ej. `rollup_1m_until`.
- `rules`, `maintenance`, `incidents` (migración 002): índice único `(rule_id, container_id) WHERE state='open'`.
- `schema_migrations(version PK, applied_at)`.
