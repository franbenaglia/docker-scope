# Progreso

> Actualizar este archivo **antes de cada commit**. Es lo primero que lee una sesión nueva.

## Estado actual
- **Etapa:** — (plan completo: etapas 0–12 cerradas)
- **Último paso completado:** Etapa 12 cerrada: verificación final contra Docker real.
- **Próximo paso:** ninguno obligatorio. Para continuar, ver "Pendientes / ideas fuera de etapa" y
  "Verificaciones pendientes"; cualquier trabajo nuevo debe agregarse como etapa en PLAN.md.
- **Bloqueos:** ninguno.

## Resumen final

**Implementado** (ver README y docs/ARCHITECTURE.md):
- Agente Go de un solo binario (panel Svelte embebido, SQLite sin cgo) que sólo lee de Docker: inventario
  por eventos + reconciliación con reconexión y backoff (datos desactualizados marcados, sin falsos
  "detenidos"), colector compartido de métricas (CPU en % de un núcleo, memoria sin caché con límite
  explícito o del host, red, block I/O, pids), historial de 7 días (crudas 24 h + resúmenes por minuto),
  motor de alertas (5 tipos, pendiente/abierta/resuelta, duración, cooldown, recuperación, histéresis,
  deduplicación, mantenimiento, ack ≠ resolver, causas probables), logs (tail + follow por SSE, stdout/stderr
  vs TTY, saneado, límites) y API REST + SSE con seguridad HTTP (loopback, Host, Origin, JSON, CSP).
- Panel: resumen con totales con unidad, tabla (búsqueda, filtros, orden, agrupación Compose), detalle con 4
  gráficos y visor de logs, incidentes, reglas, mantenimiento, notificaciones opt-in, estados de
  carga/error/vacío/reconexión/desactualizado, responsive.
- Modo demo con escenario dinámico y BD separada; Compose de prueba con escenarios controlados; unidad
  systemd con hardening; README.

**Comandos:** `make build` · `./bin/dockerscope` (http://127.0.0.1:8080) · `make demo` · `make check` ·
`make dev` · `make test-env-up` + `deploy/test-compose/scenario.sh …` · `make test-env-down`.

**Verificaciones realizadas:**
- Tests Go (unidad e integración, con `-race`) de todos los paquetes, incluido el fuzzing del parser de logs;
  `svelte-check` sin errores ni advertencias; govulncheck y npm audit sin hallazgos.
- Contra Docker 29.1 real (sólo contenedores de prueba): métricas contrastadas con `docker stats`; eventos,
  recreación, reinicios; logs json-file/TTY/driver none/ANSI-OSC/HTML; incidentes de unhealthy, bucle de
  reinicios, contenedor detenido (también durante una desconexión) y su recuperación; desconexión y
  reconexión cortando un proxy socat del socket; cancelación de streams.
- Navegador (Playwright): todas las vistas, flujos de reglas/ack/mantenimiento, render seguro, CSP sin
  violaciones, 390 px de ancho sin scroll horizontal.

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
- Mejora futura acordada con el usuario (ADR-017): acceso restringido en hosts multiusuario (escuchar en un
  socket unix con permisos, accesible por `ssh -L`, o un token local).
- Posibles mejoras: persistir el cooldown y los pendientes de alertas; paginación en el cliente para más de
  500 contenedores.

## Bitácora
| Fecha | Etapa | Resumen |
|-------|-------|---------|
| 2026-10-14 | 0 | Harness inicial: CLAUDE.md, PLAN, PROGRESS, DECISIONS, ARCHITECTURE. |
| 2026-10-14 | 12 | Verificación final: make check + go test -race limpios; E2E en el navegador con el Compose de prueba contra Docker real; desconexión/reconexión; cancelación de streams. Corregido: secuencia de eventos de la causa (OnEvent) y el contador de reintentos en el panel (ADR-019). |
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
