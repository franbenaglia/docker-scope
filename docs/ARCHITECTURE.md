# Arquitectura

> Mantener actualizado al crear/cambiar paquetes, interfaces, endpoints o eventos SSE.
> Estado: etapas 1–3 implementadas (config, docker, dockertest, inventory, collector, demo, api, web). El resto de los paquetes son previstos.

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
internal/logs/          tail/follow, demux, buffers, sanitizado
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
| GET | `/api/containers` | `ContainerListDTO` | `synced`/`stale`/`as_of` + `items[]` ordenados por nombre. Las fechas van en UTC o `null`; `health_current`, `restart_count` (Docker) vs. `observed_restarts` (agente); `mem_limit_bytes`/`cpu_limit_cores` configurados o null; `metrics` (MetricsDTO) o null. Sin filtros ni paginación todavía (etapa 5). |
| * | `/api/*` desconocida | 404 `not_found` | |

`SummaryDTO.resources`: `cpu_cores_used` (núcleos), `host_cpus`, `mem_used_bytes`, `host_mem_bytes`,
`cpu_sampled`/`mem_sampled`/`running`, `interval_seconds`. `MetricsDTO`: `cpu_percent` (% de 1 núcleo),
`mem_*` (uso sin caché, límite efectivo, explícito o no, %), `net_*_bytes` (acumulados) y `net_*_bps`, `blk_*`, `pids`.
Todos los valores numéricos pueden ser null (no disponible).

_(completar el resto en la etapa 5)_

## Eventos SSE
_(completar en etapa 5)_

## Esquema SQLite
_(completar en etapa 4)_
