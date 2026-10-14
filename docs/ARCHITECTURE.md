# Arquitectura

> Mantener actualizado al crear/cambiar paquetes, interfaces, endpoints o eventos SSE.
> Estado: etapa 1 implementada (config, docker, demo, api, web). El resto de los paquetes son previstos.

## Estructura de directorios (prevista)
```
cmd/dockerscope/        main: flags, config, wiring, apagado ordenado
internal/config/        carga y validación de configuración
internal/docker/        interfaz DockerAPI, cliente real, fake para tests
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
- `docker.API` ([internal/docker/docker.go](../internal/docker/docker.go)): `Ping`, `ListContainers`.
  Se amplía en la etapa 2. Implementaciones: `docker.Client` (moby SDK) y `demo.Source`.
- Config ([internal/config](../internal/config/config.go)): valores por defecto < JSON (`--config`) < flags.
  Ejemplo en [deploy/config.example.json](../deploy/config.example.json).
- Frontend embebido: paquete `web` (`web.Dist()`), `//go:embed all:dist`. `web/dist/.gitkeep`
  existe para que `go build` funcione sin compilar antes el panel.

## API REST
Errores: `{"error":{"code":"...","message":"..."}}` (code en snake_case). JSON con `Cache-Control: no-store`.
Rutas no-API: archivos de `web/dist`; si no existen, `index.html` (rutas de la SPA). `assets/*` son inmutables.

| Método | Ruta | Respuesta | Notas |
|---|---|---|---|
| GET | `/api/summary` | `SummaryDTO` | `containers` es `null` si Docker no responde (desconocido ≠ 0). `unhealthy` cuenta sólo los que están en ejecución. `mode`: `docker`\|`demo`. |
| * | `/api/*` desconocida | 404 `not_found` | |

_(completar el resto en la etapa 5)_

## Eventos SSE
_(completar en etapa 5)_

## Esquema SQLite
_(completar en etapa 4)_
