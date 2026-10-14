# Decisiones (ADR)

Formato: número, título, estado (Aceptada / Reemplazada por ADR-N / Pendiente), contexto breve,
decisión, consecuencias. No editar decisiones aceptadas: añadir una nueva que las reemplace.

---

## ADR-001 — Proceso de desarrollo con harness documental
**Estado:** Aceptada (2026-10-14)
**Contexto:** El alcance excede una sesión; el contexto puede perderse.
**Decisión:** Estado del proyecto en `docs/PROGRESS.md`, plan en `docs/PLAN.md`, decisiones aquí,
arquitectura en `docs/ARCHITECTURE.md`; commits por paso con árbol en verde.
**Consecuencias:** Toda sesión nueva arranca leyendo PROGRESS.md (ver CLAUDE.md).

## ADR-002 — Unidad de CPU: porcentaje de un núcleo
**Estado:** Aceptada (2026-10-14)
**Contexto:** SPEC exige unidad inequívoca, igual en UI y reglas.
**Decisión:** CPU en "% de un núcleo", igual que `docker stats`: rango 0 – N×100 donde N =
`online_cpus`. Fórmula: `(Δcontainer_cpu / Δsystem_cpu) × online_cpus × 100`. La UI siempre
muestra la etiqueta "% (100% = 1 núcleo)" y, en detalle, el N del host. Los umbrales de reglas
usan la misma unidad. Si `online_cpus` es 0, usar `len(percpu_usage)`.
**Consecuencias:** Valores >100% son normales en multinúcleo; documentar en README. Agregados
de CPU se muestran como "núcleos usados" con denominador explícito.

## ADR-003 — Memoria: uso sin caché de página inactiva
**Estado:** Aceptada (2026-10-14)
**Contexto:** SPEC pide documentar si se excluye caché y manejar cgroup v1/v2.
**Decisión:** `usage − inactive_file` (cgroup v2: `stats.inactive_file`; v1:
`stats.total_inactive_file`), como hace el CLI de Docker. Si falta el campo, usar `usage` y
marcarlo. Límite: si `limit` ≥ memoria total del host (o el valor "sin límite"), se presenta como
"sin límite (host: X)" y el porcentaje se calcula contra el host, rotulado así.
**Consecuencias:** Las reglas de memoria % usan el mismo denominador que la UI.

## ADR-004 — Sin escucha externa en v1
**Estado:** Aceptada (2026-10-14)
**Contexto:** SPEC prohíbe escuchar externamente sin autenticación configurada, sin definir
mecanismo de auth.
**Decisión:** El agente sólo acepta direcciones loopback (127.0.0.1 / ::1). Una dirección no
loopback es error de configuración. Acceso remoto: túnel SSH documentado en README.
**Consecuencias:** No se implementa auth en v1. Validación de Host contra
`localhost`/`127.0.0.1`/`[::1]` con el puerto configurado.

## ADR-005 — Router HTTP: net/http estándar
**Estado:** Aceptada (2026-10-14)
**Decisión:** Usar `http.ServeMux` con patrones de método y comodines (Go ≥ 1.22). Sin router
externo.

## ADR-006 — Dependencias y versiones
**Estado:** Aceptada (2026-10-14). Versiones consultadas en proxy.golang.org y registro npm.
**Decisión:**
- SDK Docker: `github.com/moby/moby/client` v0.6.2 (+ `github.com/moby/moby/api` v1.56.x).
  `github.com/docker/docker` quedó congelado en v28.5.2 y el proyecto moby migró a estos
  módulos. La negociación de versión de API está activa por defecto en `client.New`
  (`WithAPIVersionNegotiation` está deprecada y no hace nada). API soportada: 1.40–1.56.
  Nota: la API v0.x usa structs `XxxOptions`/`XxxResult` en todos los métodos.
- SQLite sin cgo: `modernc.org/sqlite` (v1.60.x).
- Frontend (versiones exactas, sin `^`): svelte 5.57.2 (runes), vite 8.3.4,
  @sveltejs/vite-plugin-svelte 7.3.1, svelte-check 4.7.6, @tsconfig/svelte 5.0.8,
  chart.js 4.5.1.
- TypeScript **6.0.3**, no 7.x: svelte-check 4.7.6 declara peer `typescript ^5 || ^6`.
- Vite 8 requiere Node ^20.19 o ≥22.12 (el entorno tiene 20.20.2: OK).
**Consecuencias:** Revisar la compatibilidad de TS 7 cuando svelte-check la soporte (añadir un ADR nuevo).

## ADR-007 — Ruta del módulo Go y repositorio
**Estado:** Aceptada (2026-10-14)
**Decisión:** El módulo es `github.com/franbenaglia/docker-scope` y el remoto `origin` es
https://github.com/franbenaglia/docker-scope.git. El binario conserva el nombre `dockerscope`.
**Consecuencias:** Push sólo cuando el usuario lo pida.

## ADR-008 — Reinicios observados y salud de contenedores detenidos
**Estado:** Aceptada (2026-10-14)
**Contexto:** SPEC pide distinguir el `RestartCount` de Docker de los reinicios observados. Docker
conserva el último `Health.Status` de los contenedores que ya salieron (en el host había 8 exited
con "unhealthy").
**Decisión:**
- Un reinicio observado es un avance de `State.StartedAt` en un contenedor que ya había corrido,
  sea cual sea la causa (política, manual, reinicio del daemon). Se detecta comparando StartedAt al
  procesar eventos y al reconciliar, así que es idempotente frente a eventos repetidos. Si hubo
  varios reinicios mientras Docker estaba desconectado, cuentan como uno.
- La salud sólo es "actual" con el contenedor en `running`. Para los demás se expone como última
  salud conocida (`health_current: false`) y no cuenta como unhealthy.
- `HealthNone` ("none") = sin HEALTHCHECK; nunca equivale a healthy.

## ADR-009 — Modo demo sobre el fake de Docker
**Estado:** Aceptada (2026-10-14)
**Decisión:** `demo.New` devuelve un `dockertest.Fake` poblado. El modo demo recorre exactamente
el mismo camino (inventario, API, panel) que Docker real, sin código especial en esas capas. El agente
usa una sola fuente: demo o Docker, nunca las dos.

## ADR-010 — Muestreo y cálculo de métricas
**Estado:** Aceptada (2026-10-14). Precisa ADR-002 y ADR-003 con lo observado en el host (cgroup v2).
**Decisión:**
- Un único colector hace una ronda cada `sample_interval`: stats one-shot (`stream=false`, sin
  `IncludePreviousSample`, que hace esperar 1 s al daemon) para cada contenedor en ejecución, con
  concurrencia 4 y timeout min(intervalo, 10 s). Las rondas no se solapan. Sin conexión no hay rondas.
- Las tasas se calculan entre muestras propias consecutivas, usando el `read` del daemon como reloj.
  No hay tasa en la primera muestra, si el contador retrocede (reset), si cambió `StartedAt`
  (reinicio) ni si el intervalo supera 10× el intervalo de muestreo.
- CPU: `Δtotal_usage / Δsystem_cpu_usage × online_cpus × 100` (online_cpus o, si falta, len(percpu)).
- Memoria: `usage − total_inactive_file` (v1) o `− inactive_file` (v2) cuando es menor que usage.
  El límite efectivo es `memory_stats.limit`; es "explícito" sólo si `HostConfig.Memory > 0`. Sin
  límite, Docker informa la memoria del host y la UI lo muestra como "sin límite". Un límite ≥ 2^60
  (el "ilimitado" de v1) no produce porcentaje.
- Block I/O: suma de `io_service_bytes_recursive` por op read/write (sin distinguir mayúsculas). Si
  la lista es null (cgroup v2 sin I/O, o sin soporte) el valor es no disponible, no 0; la UI lo
  explica con un tooltip. `docker stats` muestra 0 B en ese caso: es una diferencia deliberada.
- Red: suma de todas las interfaces; sin `networks` (p. ej. `--network none`), no disponible.
- Un contenedor detenido o que falla en la ronda sale del conjunto de "últimas muestras".
- Agregados (`/api/summary.resources`): suma de núcleos y de bytes de los contenedores con dato,
  con el denominador del host (Info: NCPU, MemTotal) y cuántos contenedores entran en la suma.

## ADR-011 — Almacenamiento de métricas: crudas 24 h + rollups de 1 minuto
**Estado:** Aceptada (2026-10-14)
**Contexto:** 7 días de muestras cada 5 s son unas 120 mil filas por contenedor. SPEC pide 7 días de
retención, consultas acotadas y reducir puntos en los gráficos largos.
**Decisión:**
- `samples`: muestras crudas durante min(24 h, retención). `samples_1m`: avg/max por minuto durante
  toda la retención. Las acumula `Rollup` cada minuto (minutos completos con 30 s de margen), es
  idempotente y retoma desde la marca de agua guardada en `settings`.
- Series: si `from` cae dentro de la ventana de crudas se agrupa sobre `samples`; si no, sobre
  `samples_1m`. Buckets alineados a epoch y redondeados a valores "lindos" (1 s…24 h), dimensionados
  para que el resultado nunca supere `points` (10–1000, 300 por defecto). Rango mínimo 1 min; se
  recorta a la retención y a "ahora"; `to` es inclusivo. Avg ignora los NULL (sin dato ≠ 0).
- Sólo se guardan tasas (no acumulados de red y disco) y la memoria; nunca contenidos de logs.
- Escrituras serializadas en un goroutine (`Writer`) con cola de 256 lotes: si se llena se
  descarta y se cuenta, sin bloquear el muestreo. Cada escritura tiene su propio timeout de 10 s,
  desacoplado de la cancelación, para no perder lotes durante el apagado (había un bug).
- SQLite (modernc, sin cgo) en WAL + `synchronous=NORMAL` + `busy_timeout=5000`. Directorio 0700 y
  archivo 0600, creado antes de abrir para que `-wal`/`-shm` hereden 0600. El modo demo usa
  `dockerscope-demo.db`.
- Las tablas de incidentes, reglas y mantenimiento llegan en la migración 002 (etapa 6).

## ADR-012 — Seguridad HTTP y protocolo SSE
**Estado:** Aceptada (2026-10-14)
**Decisión (seguridad):**
- Host: sólo `localhost` o IPs loopback, con cualquier puerto. Así un dominio hostil que resuelve a
  127.0.0.1 (DNS rebinding) recibe 403.
- `/api/*` con `Sec-Fetch-Site: cross-site` → 403. Nunca se envían cabeceras CORS.
- Mutaciones (todo lo que no es GET/HEAD): `Origin` igual al propio (`http://<host loopback>:<puerto>`)
  o a uno de `allowed_origins` (que también deben ser loopback). Sin Origin, se aceptan si
  Sec-Fetch-Site falta (clientes como curl) o es same-origin/none. Además `Content-Type:
  application/json`, lo que impide CSRF con formularios simples.
- Cabeceras: CSP `default-src 'self'` (estilos con 'unsafe-inline'), nosniff, no-referrer, X-Frame-Options DENY, COOP/CORP.
- Desarrollo: Vite reenvía con `changeOrigin` y `make dev-api` pasa `-allow-origin http://127.0.0.1:5173`.
**Decisión (SSE):**
- `/api/stream` (general) separado de los futuros streams de logs. Eventos: hello, summary,
  daemon, container, container_removed, metrics, heartbeat (15 s), resync. Data = JSON en una línea.
- Hub: máximo 32 clientes (503 por encima), cola de 64 eventos por cliente y deadline de escritura
  de 10 s. Si la cola se desborda se descarta lo pendiente y se envía `resync`. Sin replay por
  Last-Event-ID: los ids sólo sirven de diagnóstico.
- El cliente pide el snapshot REST al recibir `hello` (cada conexión) y ante `resync`. Los eventos que
  llegan durante el snapshot se aplican después y en orden (los eventos llevan el estado completo).
  Un watchdog reabre el stream si pasan 2,5 heartbeats sin datos.
- `summary` se publica tras cada ronda y, con un debounce de 250 ms, tras cambios de contenedores o de conexión.
- Orden de arranque: listeners registrados antes de lanzar inventario, colector y writer. Al apagar,
  `hub.Close()` antes de `http.Server.Shutdown`.
