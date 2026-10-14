# Progreso

> Actualizar este archivo **antes de cada commit**. Es lo primero que lee una sesión nueva.

## Estado actual
- **Etapa:** 6 — Motor de alertas
- **Último paso completado:** Migración 002 (rules con 4 reglas sembradas, maintenance, incidents con
  índice único de abiertos) y paquete `internal/alerts` (modelo, validación, motor evaluado por tick
  con reloj inyectable, causas probables, CRUD de reglas, ack, mantenimiento) con 17 tests en verde.
  El store SQLite todavía NO implementa `alerts.Store` y el motor no está conectado.
- **Próximo paso:** Implementar `alerts.Store` en `internal/store/alerts.go` (+ tests y retención de
  incidentes), conectar el motor en main (Load, Observe, Run), endpoints REST (rules, incidents, ack,
  pending, maintenance), evento SSE `incident`, contadores en summary y ADR-013.
- **Bloqueos:** ninguno.

## Entorno verificado (2026-10-14)
- Go 1.27.2, Node 20.20.2 / npm 10.8.2 (Node 20 sin soporte desde 2026-04; funciona para build,
  recomendable migrar a 22/24 LTS), Docker 29.1.3 (API 1.52), usuario en grupo `docker`.

## Verificaciones pendientes
_(lo que no pudo probarse y por qué)_
- Etapa 1: `make dev` (agente + Vite en paralelo) no se probó de forma interactiva; `make check` y
  el binario sí.
- Etapa 5: `make dev` (Vite + -allow-origin) sigue sin probarse de forma interactiva.
- Etapa 2: no se probó un `systemctl restart docker` real (requiere la autorización del usuario). Sí se
  probó la desconexión real cortando un proxy socat del socket, y el reinicio del daemon con el fake.

## Pendientes / ideas fuera de etapa
_(no ampliar el alcance de SPEC.md; anotar aquí)_
- Los reintentos de conexión registran un WARN por intento (con un máximo de 30 s es aceptable);
  si molesta, bajar a Debug después del primero.
- Etapa 10: el modo demo usa `dockertest.Fake` (ADR-009); agregar ahí el escenario dinámico.

## Bitácora
| Fecha | Etapa | Resumen |
|-------|-------|---------|
| 2026-10-14 | 0 | Harness inicial: CLAUDE.md, PLAN, PROGRESS, DECISIONS, ARCHITECTURE. |
| 2026-10-14 | 5 | Seguridad HTTP (Host/Origin/CSP), detalle/filtros/paginación, hub SSE + /api/stream, panel en vivo. Desvío: el commit b5b413a salió sin PROGRESS ni el middleware conectado (corregido en 271c32f). Corregida una carrera: los listeners se registraban después de arrancar las goroutines. |
| 2026-10-14 | 4 | Store SQLite (migraciones, crudas + rollup 1m, retención, writer con cola acotada), endpoint de series. Bug corregido: los lotes desencolados durante el apagado se perdían por usar el contexto cancelado. Verificado contra Docker real reiniciando el agente. |
| 2026-10-14 | 3 | Colector de métricas + Info del host; métricas en la API y el panel; demo con stats sintéticas. Contrastado con `docker stats` real (contenedores dsp-*). |
| 2026-10-14 | 2 | Inventario (eventos, reconciliación, backoff, dedup, reinicios observados), fake Docker, API desde el inventario, panel con stale. Verificado contra Docker real (contenedores de prueba dsprobe*) y con desconexión vía proxy. |
| 2026-10-14 | 1→2 | Módulo renombrado a github.com/franbenaglia/docker-scope (ADR-007), remoto origin configurado (sin push). |
| 2026-10-14 | 1 | Esqueleto Go + Svelte, ADR-006 (versiones), /api/summary real, embed, Makefile, config de ejemplo. Bug detectado en el host real: salud "unhealthy" en contenedores exited. |
