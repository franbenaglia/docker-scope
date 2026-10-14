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
- [ ] Muestreador único con intervalo configurable (5s), concurrencia limitada, timeouts
- [ ] CPU % de un núcleo (ADR-002), memoria sin caché (ADR-003), red, block I/O
- [ ] Tasas por delta; manejo de resets (no negativos); primera muestra = no disponible
- [ ] Límite de memoria: distinguir explícito vs. heredado del host
- [ ] cgroup v1/v2
- [ ] Último snapshot compartido para todos los clientes
- [ ] Tests de deltas, resets, valores ausentes, cgroup v1/v2

**Terminado cuando:** tests de cálculo pasan y `/api/containers` devuelve métricas reales.

## Etapa 4 — Persistencia SQLite
- [ ] Migraciones versionadas embebidas
- [ ] Tablas: samples, incidents, rules, maintenance, settings
- [ ] Inserción por lotes, timestamps UTC, índices
- [ ] Retención (7 días por defecto) con limpieza periódica
- [ ] Consultas acotadas + downsampling para rangos largos
- [ ] Permisos del archivo (0600) y del data dir (0700)
- [ ] Tests de persistencia, retención y downsampling

**Terminado cuando:** las series sobreviven a un reinicio del agente y la retención funciona en tests.

## Etapa 5 — API REST y SSE general
- [ ] DTOs, validación, formato de error uniforme, límites de rango, paginación
- [ ] Endpoints: summary, containers, container detail, series, incidents, rules, maintenance
- [ ] Hub SSE: eventos tipados, heartbeat, cola acotada por cliente, política de descarte →
      evento `resync`
- [ ] Seguridad: validación de Host, Origin en mutaciones, sin CORS abierto
- [ ] Tests de validación API, Host/Origin, cliente lento

**Terminado cuando:** contratos documentados en ARCHITECTURE.md y tests de API pasan.

## Etapa 6 — Motor de alertas
- [ ] Reglas: deja de ejecutarse, unhealthy persistente, reinicios en ventana, CPU, memoria
- [ ] Ámbito: contenedor y proyecto/servicio Compose
- [ ] Estados pending/open/resolved; duración mínima, cooldown, deduplicación, histéresis
- [ ] Acknowledge ≠ resolve
- [ ] Sin alertas "se detuvo" para contenedores ya detenidos al arranque
- [ ] Causa probable a partir de eventos y exit code (redacción no categórica)
- [ ] Mantenimiento global y por contenedor con vencimiento; suprime avisos, no recolección
- [ ] Persistencia de incidentes y recuperación
- [ ] Tests exhaustivos: duración, recuperación, cooldown, mantenimiento, reinicios en ventana,
      histéresis, arranque

**Terminado cuando:** todos los tests del motor pasan con reloj simulado.

## Etapa 7 — Logs
- [ ] Tail inicial 200 líneas, límite configurable acotado
- [ ] Follow por SSE en stream separado; cancelación al cerrar/cambiar
- [ ] Demux stdout/stderr (stdcopy) vs. TTY
- [ ] Buffers acotados por líneas y bytes; tamaño máximo por línea
- [ ] Mensaje claro si el logging driver no permite leer
- [ ] Sanitizado de secuencias de terminal peligrosas
- [ ] Tests de demux, truncado, cancelación

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
