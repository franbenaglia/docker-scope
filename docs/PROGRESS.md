# Progreso

> Actualizar este archivo **antes de cada commit**. Es lo primero que lee una sesión nueva.

## Estado actual
- **Etapa:** 12 — Verificación final
- **Último paso completado:** Etapa 11 cerrada. Compose de prueba + `scenario.sh`, unidad systemd con
  hardening, `make test-env-up/down` y README completo (requisitos, compilación, configuración, permisos del
  socket y rootless, systemd, túnel SSH, retención, fórmulas, alertas, logs, demo, Compose, seguridad,
  limitaciones, desarrollo).
- **Próximo paso:** Etapa 12: `make check` limpio; recorrido E2E en el navegador con el Compose de prueba
  (panel → contenedor → métricas → logs → fallo controlado → incidente → recuperación); desconexión y
  reconexión de Docker (proxy socat) y cancelación de streams; resumen final en PROGRESS.md.
- **Bloqueos:** ninguno.

## Entorno verificado (2026-10-14)
- Go 1.27.2, Node 20.20.2 / npm 10.8.2 (Node 20 sin soporte desde 2026-04; funciona para build,
  recomendable migrar a 22/24 LTS), Docker 29.1.3 (API 1.52), usuario en grupo `docker`.

## Verificaciones pendientes
_(lo que no pudo probarse y por qué)_
- Etapa 1: `make dev` (agente + Vite en paralelo) no se probó de forma interactiva; `make check` y
  el binario sí.
- Etapa 11: la unidad systemd no se instaló ni se ejecutó como servicio (requiere root); sólo `systemd-analyze verify`.
- Etapa 10 (posible mejora fuera de SPEC): en hosts multiusuario, cualquier usuario local puede abrir el
  panel. Opciones futuras: escuchar en un socket unix con permisos (accesible por `ssh -L`) o un token local.
- Etapa 9: notificaciones del navegador no probadas de forma interactiva (el navegador headless no concede el permiso).
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
| 2026-10-14 | 11 | Compose de prueba con escenarios controlados, unidad systemd con hardening, README completo. Corregido en la revisión: /etc/dockerscope debía pertenecer al grupo dockerscope. |
| 2026-10-14 | 10 | Demo dinámico (4 tipos de incidente en un ciclo de 4 min) y endurecimiento (MaxHeaderBytes, auditoría de logs, govulncheck/npm audit, CSP). |
| 2026-10-14 | 9 | Visor de logs, vistas de incidentes/reglas/mantenimiento, navegación, notificaciones opt-in. Bug corregido: mutaciones sin cuerpo → 415 (el cliente ahora siempre envía JSON). Render seguro verificado con logs reales maliciosos. |
| 2026-10-14 | 8 | Panel: componentes, router por hash, tabla (filtros, orden, agrupación Compose), detalle con gráficos Chart.js (eje X fijado al rango), reloj compartido, estilos globales. Verificado en demo, escritorio y móvil. |
| 2026-10-14 | 7 | Logs: parser (frames/TTY/timestamps), sanitizado, buffer acotado, REST tail + SSE follow con límite de streams, demo. Bug corregido: el truncado contaba dos veces. Verificado contra Docker real. |
| 2026-10-14 | 6 | Motor de alertas (tick 1s, pendiente en memoria, abierta/resuelta persistidas, histéresis, cooldown, recover_for, mantenimiento con supresión, causas probables por códigos y eventos), API y SSE. Verificado contra Docker real con contenedores dsp-*. Corregido: dur() y los contenedores eliminados entre ticks (tracks inactivos). |
| 2026-10-14 | 5 | Seguridad HTTP (Host/Origin/CSP), detalle/filtros/paginación, hub SSE + /api/stream, panel en vivo. Desvío: el commit b5b413a salió sin PROGRESS ni el middleware conectado (corregido en 271c32f). Corregida una carrera: los listeners se registraban después de arrancar las goroutines. |
| 2026-10-14 | 4 | Store SQLite (migraciones, crudas + rollup 1m, retención, writer con cola acotada), endpoint de series. Bug corregido: los lotes desencolados durante el apagado se perdían por usar el contexto cancelado. Verificado contra Docker real reiniciando el agente. |
| 2026-10-14 | 3 | Colector de métricas + Info del host; métricas en la API y el panel; demo con stats sintéticas. Contrastado con `docker stats` real (contenedores dsp-*). |
| 2026-10-14 | 2 | Inventario (eventos, reconciliación, backoff, dedup, reinicios observados), fake Docker, API desde el inventario, panel con stale. Verificado contra Docker real (contenedores de prueba dsprobe*) y con desconexión vía proxy. |
| 2026-10-14 | 1→2 | Módulo renombrado a github.com/franbenaglia/docker-scope (ADR-007), remoto origin configurado (sin push). |
| 2026-10-14 | 1 | Esqueleto Go + Svelte, ADR-006 (versiones), /api/summary real, embed, Makefile, config de ejemplo. Bug detectado en el host real: salud "unhealthy" en contenedores exited. |
