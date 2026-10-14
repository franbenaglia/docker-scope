# Progreso

> Actualizar este archivo **antes de cada commit**. Es lo primero que lee una sesión nueva.

## Estado actual
- **Etapa:** 2 — Cliente Docker, inventario y eventos
- **Último paso completado:** Etapa 1 cerrada. Binario único con el panel embebido; `/api/summary`
  verificado contra Docker real (API 1.52), con Docker inaccesible (`containers: null`) y en modo demo.
  Rechaza listen no loopback.
- **Próximo paso:** Ampliar `docker.API` (inspect, stats, events, logs) y crear un fake reutilizable
  en `internal/docker/dockertest`; después, el paquete `internal/inventory`. Revisar antes los
  "Pendientes" de la etapa 2 que están más abajo.
- **Bloqueos:** ninguno.

## Entorno verificado (2026-10-14)
- Go 1.27.2, Node 20.20.2 / npm 10.8.2 (Node 20 sin soporte desde 2026-04; funciona para build,
  recomendable migrar a 22/24 LTS), Docker 29.1.3 (API 1.52), usuario en grupo `docker`.

## Verificaciones pendientes
_(lo que no pudo probarse y por qué)_
- Etapa 1: `make dev` (agente + Vite en paralelo) no se probó de forma interactiva; `make check` y
  el binario sí.

## Pendientes / ideas fuera de etapa
_(no ampliar el alcance de SPEC.md; anotar aquí)_
- **Etapa 2:** si el primer ping falla (Docker caído al arrancar), el SDK negocia la API mínima
  (1.40) y no vuelve a negociar. En la reconexión, usar `PingOptions{NegotiateAPIVersion: true,
  ForceNegotiate: true}`.
- **Etapa 2:** Docker conserva `Health.Status` tras salir el contenedor (visto en el host: 8
  exited con "unhealthy"). La salud sólo se considera con el contenedor en ejecución; en la UI
  mostrar "última salud conocida" para detenidos, sin contarla como unhealthy.
- **Etapa 2:** `/api/summary` consulta Docker en cada request (temporal); pasarlo al inventario
  compartido.

## Bitácora
| Fecha | Etapa | Resumen |
|-------|-------|---------|
| 2026-10-14 | 0 | Harness inicial: CLAUDE.md, PLAN, PROGRESS, DECISIONS, ARCHITECTURE. |
| 2026-10-14 | 1→2 | Módulo renombrado a github.com/franbenaglia/docker-scope (ADR-007), remoto origin configurado (sin push). |
| 2026-10-14 | 1 | Esqueleto Go + Svelte, ADR-006 (versiones), /api/summary real, embed, Makefile, config de ejemplo. Bug detectado en el host real: salud "unhealthy" en contenedores exited. |
