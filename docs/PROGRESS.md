# Progreso

> Actualizar este archivo **antes de cada commit**. Es lo primero que lee una sesión nueva.

## Estado actual
- **Etapa:** 1 — Esqueleto e integración mínima real
- **Último paso completado:** Backend Go mínimo (config, docker.API, demo estático, /api/summary,
  SPA embebida, apagado ordenado) con tests en verde. ADR-006 resuelto.
- **Próximo paso:** Frontend `web/` (Vite + Svelte + TS) que muestre /api/summary, luego Makefile.
- **Bloqueos:** ninguno.

## Entorno verificado (2026-10-14)
- Go 1.27.2, Node 20.20.2 / npm 10.8.2 (Node 20 sin soporte desde 2026-04; funciona para build,
  recomendable migrar a 22/24 LTS), Docker 29.1.3 (API 1.52), usuario en grupo `docker`.

## Verificaciones pendientes
_(lo que no pudo probarse y por qué)_

## Pendientes / ideas fuera de etapa
_(no ampliar el alcance de SPEC.md; anotar aquí)_
- **Etapa 2:** si el primer ping falla (Docker caído al arrancar), el SDK negocia la API mínima
  (1.40) y no vuelve a negociar. En la reconexión, usar `PingOptions{NegotiateAPIVersion: true,
  ForceNegotiate: true}`.
- **Etapa 2:** `/api/summary` consulta Docker en cada request (temporal); pasarlo al inventario
  compartido.

## Bitácora
| Fecha | Etapa | Resumen |
|-------|-------|---------|
| 2026-10-14 | 0 | Harness inicial: CLAUDE.md, PLAN, PROGRESS, DECISIONS, ARCHITECTURE. |
