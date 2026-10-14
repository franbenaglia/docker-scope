# Arquitectura

> Mantener actualizado al crear/cambiar paquetes, interfaces, endpoints o eventos SSE.
> Estado: **propuesta inicial** (aún sin código).

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
_(completar en etapa 2)_

## API REST
_(completar en etapa 5)_

## Eventos SSE
_(completar en etapa 5)_

## Esquema SQLite
_(completar en etapa 4)_
