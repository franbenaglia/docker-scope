# Progreso

> Actualizar este archivo **antes de cada commit**. Es lo primero que lee una sesión nueva.

## Estado actual
- **Etapa:** 1 — Esqueleto e integración mínima real
- **Último paso completado:** Etapa 0 (harness creado).
- **Próximo paso:** Verificar en documentación oficial las versiones vigentes del SDK Docker
  (módulo `github.com/moby/moby/client` vs `github.com/docker/docker/client`), del driver SQLite
  y del stack frontend; registrarlas en DECISIONS.md (ADR-006) y hacer `go mod init`.
- **Bloqueos:** ninguno.

## Entorno verificado (2026-10-14)
- Go 1.27.2, Node 20.20.2 / npm 10.8.2 (Node 20 sin soporte desde 2026-04; funciona para build,
  recomendable migrar a 22/24 LTS), Docker 29.1.3 (API 1.52), usuario en grupo `docker`.

## Verificaciones pendientes
_(lo que no pudo probarse y por qué)_

## Pendientes / ideas fuera de etapa
_(no ampliar el alcance de SPEC.md; anotar aquí)_

## Bitácora
| Fecha | Etapa | Resumen |
|-------|-------|---------|
| 2026-10-14 | 0 | Harness inicial: CLAUDE.md, PLAN, PROGRESS, DECISIONS, ARCHITECTURE. |
