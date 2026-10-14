# Plan de implementación

Cada etapa deja el proyecto compilando y con tests en verde. Marcar `[x]` al completar.
Referencia de alcance: [SPEC.md](SPEC.md). Decisiones: [DECISIONS.md](DECISIONS.md).

---

## Etapa 0 — Harness
- [x] git init, SPEC.md, CLAUDE.md, PLAN.md, PROGRESS.md, DECISIONS.md, ARCHITECTURE.md

## Etapa 1 — Esqueleto e integración mínima real
- [x] Verificar y fijar versiones: módulo del SDK Docker, driver SQLite, Svelte, Vite, Chart.js (ADR)
- [x] `go mod init`, estructura `cmd/dockerscope`, `internal/...`
- [x] Frontend `web/` con Vite + Svelte + TS estricto
- [x] `go:embed` del build de `web/dist`; servir SPA con fallback a index.html
- [x] Config mínima (flags + archivo de ejemplo): socket, listen addr, data dir, intervalo
- [x] Endpoint `GET /api/summary` real contra Docker (conteo running/stopped, estado daemon)
- [x] Página que muestra el resumen
- [x] Makefile: `dev`, `test`, `check`, `build`, `demo` (demo con datos estáticos)
- [x] `.gitignore`

**Terminado cuando:** `make check` pasa y `bin/dockerscope` sirve el panel en 127.0.0.1 mostrando
conteos reales de Docker.

## Etapa 2 — Cliente Docker, inventario y eventos
- [x] Interfaz pequeña `DockerAPI` (list, inspect, stats, events, logs, ping) + implementación real
- [x] Fake configurable para tests
- [x] Inventario en memoria indexado por ID completo; campos de SPEC "Información de contenedores"
- [x] Etiquetas Compose (proyecto/servicio)
- [x] Suscripción a eventos (start, stop, die, destroy, restart, health_status)
- [x] Reconexión con backoff + reconciliación por re-listado + deduplicación de eventos
- [x] Estado de conexión del daemon (connected/disconnected/stale con timestamp)
- [x] Contador de reinicios observados separado de RestartCount
- [x] Tests con fake: reconexión, reconciliación, recreación con mismo nombre

**Terminado cuando:** con Docker reiniciado (`systemctl restart docker`, sólo si el usuario lo
autoriza; si no, con el fake) el agente marca desconexión y reconcilia sin falsos "caídos".

_Cerrada: verificado con el fake (tests) y contra Docker real cortando un proxy socat del socket;
no se reinició el daemon del host._

## Etapa 3 — Colector de métricas
- [x] Muestreador único con intervalo configurable (5s), concurrencia limitada, timeouts
- [x] CPU % de un núcleo (ADR-002), memoria sin caché (ADR-003), red, block I/O
- [x] Tasas por delta; manejo de resets (no negativos); primera muestra = no disponible
- [x] Límite de memoria: distinguir explícito vs. heredado del host
- [x] cgroup v1/v2
- [x] Último snapshot compartido para todos los clientes
- [x] Tests de deltas, resets, valores ausentes, cgroup v1/v2

**Terminado cuando:** tests de cálculo pasan y `/api/containers` devuelve métricas reales.

_Cerrada: valores contrastados con `docker stats` en el host (CPU 101 % / 50,5 % con --cpus 0.5, memoria, block I/O)._

## Etapa 4 — Persistencia SQLite
- [x] Migraciones versionadas embebidas
- [x] Tablas: containers, samples, samples_1m, settings (incidents, rules y maintenance se crean con
      una migración en la etapa 6, junto al motor que las define)
- [x] Inserción por lotes, timestamps UTC, índices
- [x] Retención (7 días por defecto) con limpieza periódica (crudas min(24h, retención), rollups 1m retención)
- [x] Consultas acotadas + downsampling para rangos largos (`GET /api/containers/{id}/series`)
- [x] Permisos del archivo (0600) y del data dir (0700)
- [x] Tests de persistencia, retención y downsampling

**Terminado cuando:** las series sobreviven a un reinicio del agente y la retención funciona en tests.

_Cerrada: verificado con Docker real (contenedor dsp-hist), reiniciando el agente con SIGTERM._

## Etapa 5 — API REST y SSE general
- [x] DTOs, validación, formato de error uniforme, límites de rango, paginación
- [x] Endpoints: summary, containers (filtros/orden/paginación), container detail, series
- [ ] → Etapa 6: endpoints de incidents, rules y maintenance (se definen con el motor de alertas)
- [x] Hub SSE: eventos tipados, heartbeat, cola acotada por cliente, política de descarte →
      evento `resync`
- [x] Seguridad: validación de Host, Origin en mutaciones, sin CORS abierto
- [x] Tests de validación API, Host/Origin, cliente lento
- [x] Panel: SSE + snapshot REST (reemplaza el sondeo), estados de reconexión

**Terminado cuando:** contratos documentados en ARCHITECTURE.md y tests de API pasan.

_Cerrada: verificado con binario -race contra Docker real (alta y baja en vivo, caída y vuelta del agente) y curl (Host/Origin)._

## Etapa 6 — Motor de alertas
- [x] Reglas: deja de ejecutarse, unhealthy persistente, reinicios en ventana, CPU, memoria
- [x] Ámbito: contenedor y proyecto/servicio Compose
- [x] Estados pending/open/resolved; duración mínima, cooldown, deduplicación, histéresis
- [x] Acknowledge ≠ resolve
- [x] Sin alertas "se detuvo" para contenedores ya detenidos al arranque
- [x] Causa probable a partir de eventos y exit code (redacción no categórica)
- [x] Mantenimiento global y por contenedor con vencimiento; suprime avisos, no recolección
- [x] Migración 002: tablas incidents, rules, maintenance
- [x] Endpoints REST de incidents (lista paginada, ack), rules (CRUD) y maintenance (pasan por el middleware de mutaciones)
- [x] Eventos SSE `incident` (alta y cambios)
- [x] Persistencia de incidentes y recuperación
- [x] Tests exhaustivos: duración, recuperación, cooldown, mantenimiento, reinicios en ventana,
      histéresis, arranque

**Terminado cuando:** todos los tests del motor pasan con reloj simulado.

_Cerrada: 17 tests del motor + store + API; verificado contra Docker real (unhealthy, bucle de reinicios, kill de un contenedor vigilado, ack, mantenimiento con supresión, recuperación y eliminación, eventos SSE) con binario -race._

## Etapa 7 — Logs
- [x] Tail inicial 200 líneas, límite configurable acotado (0–5000; REST corta a 2 MiB)
- [x] Follow por SSE en stream separado; cancelación al cerrar/cambiar (el cierre del cliente cancela la petición a Docker)
- [x] Demux stdout/stderr (frames propios, stream 3 = mensajes del daemon) vs. TTY
- [x] Buffers acotados por líneas y bytes (2000 líneas / 1 MiB por stream, descarte con aviso `dropped`); 8 KiB por línea
- [x] Mensaje claro si el logging driver no permite leer (409 `logs_unavailable`)
- [x] Sanitizado de secuencias de terminal peligrosas (CSI, OSC, controles C0/C1, bidi)
- [x] Tests de demux, truncado, cancelación (+ fuzzing del parser)
- [ ] → Etapa 9: visor en el panel

_Cerrada: verificado contra Docker real (json-file con stdout/stderr y ANSI/OSC, TTY, driver none, follow y fin al detener) con binario -race._

## Etapa 8 — Panel: resumen, tabla, detalle
- [ ] Tema oscuro, layout responsive
- [ ] Resumen (activos/detenidos/unhealthy/incidentes abiertos/daemon)
- [ ] Tabla: búsqueda, filtros, ordenamiento, agrupación Compose
- [ ] Detalle: metadatos + gráficos CPU/mem/red/disco (Chart.js, sin CDN)
- [ ] Cliente SSE con reconexión y re-snapshot
- [ ] Estados de carga/error/vacío/reconexión/datos antiguos

## Etapa 9 — Panel: logs, incidentes, reglas, mantenimiento
- [ ] Visor de logs: pausar, limpiar, buscar, autoscroll, stdout/stderr
- [ ] Historial de incidentes con ack
- [ ] Editor de reglas
- [ ] Modo mantenimiento global/por contenedor
- [ ] Notificaciones del navegador opt-in

## Etapa 10 — Modo demo y endurecimiento
- [ ] `--demo` con fake Docker y datos simulados, base de datos separada, banner visible
- [ ] Revisión de seguridad: 127.0.0.1, sin listen externo (ADR-004), no registrar secretos

## Etapa 11 — Distribución y documentación
- [ ] `deploy/test-compose/`: servicio normal, con HEALTHCHECK, fallo/reinicio controlado
- [ ] Unidad systemd de ejemplo
- [ ] Config de ejemplo
- [ ] README completo (requisitos, instalación, permisos del socket, rootless, túnel SSH,
      retención, fórmulas de métricas, limitaciones)

## Etapa 12 — Verificación final
- [ ] `make check` limpio
- [ ] Recorrido E2E: panel → contenedor → métricas → logs → fallo controlado → incidente →
      recuperación
- [ ] Test de desconexión/reconexión Docker y cancelación de streams
- [ ] Resumen final (implementado, comandos, verificaciones, pendientes reales) en PROGRESS.md
