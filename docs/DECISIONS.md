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

## ADR-013 — Motor de alertas
**Estado:** Aceptada (2026-10-14)
**Decisión:**
- Evaluación por tick (1 s) sobre el snapshot del inventario y las últimas métricas: idempotente y
  testeable con reloj inyectado (`Engine.Tick(now)`). Con Docker desconectado o sin sincronizar no
  se evalúa, así que los incidentes no se abren ni se resuelven con datos viejos.
- Por cada (regla, contenedor por ID) hay una máquina de estados. Pendiente (sólo en memoria,
  `GET /api/incidents/pending`) → abierta → resuelta (persistidas). Condición de 4 valores: alta,
  banda (umbral − histéresis ≤ v ≤ umbral: cancela el pendiente pero no resuelve), despejada, y
  desconocida (sin muestra: no hay transición).
- `for` = duración mínima antes de abrir; `cooldown` = tiempo mínimo entre resolver y reabrir (el
  pendiente indica `in_cooldown`); `recover_for` = tiempo despejado antes de resolver (contra el
  flapping); `hysteresis` sólo para cpu y memory. Deduplicación: un abierto por par, también como índice
  único en SQLite. Un fallo al escribir no deja el estado en memoria: se reintenta en el siguiente tick.
- Unidades: cpu en % de un núcleo (como la UI); memory en % del límite efectivo (si no hay límite,
  de la memoria del host, y el resumen del incidente lo dice); restarts = reinicios observados (ADR-008)
  dentro de `window`. El contador previo al arranque del motor no cuenta.
- not_running: exige alcance (nombre o proyecto). Sólo puede abrir si el agente vio el contenedor
  en ejecución (los ya detenidos al arrancar no alertan). Pausado no cuenta. Las reglas siguen al
  contenedor por nombre: si se elimina (aunque sea entre ticks: se mantiene un track inactivo) se abre;
  si se recrea con otro ID y queda en ejecución se resuelve como "recreado".
- Causa probable: código de salida (137/143/OOM/128+N/125–127…), State.Error, secuencia reciente de
  eventos (p. ej. "kill → die") y `exitCode` del último `die`, siempre como posibilidad.
- Mantenimiento global o por nombre, con vencimiento (máx. 30 días): se recolecta y evalúa igual, y
  los incidentes que se abren se marcan `suppressed` (sin aviso). Reconocer (ack) no resuelve.
- Editar una regla resuelve sus abiertos ("regla modificada") y reinicia los pendientes; borrarla o
  deshabilitarla resuelve con el motivo correspondiente. El historial se conserva 90 días.
- Reglas sembradas: unhealthy 30 s y restarts 3/5 min habilitadas, memory 80 % durante 60 s
  habilitada, cpu 90 %/2 min deshabilitada (ejemplo). not_running no se siembra porque necesita alcance.
- Limitación: el cooldown y los pendientes no sobreviven a un reinicio del agente (sí los abiertos).

## ADR-014 — Logs
**Estado:** Aceptada (2026-10-14)
**Decisión:**
- Se piden a Docker con `timestamps=true` y stdout+stderr. Si el contenedor no tiene TTY el stream
  viene multiplexado: se parsea con un demuxer propio (no `stdcopy.StdCopy`) para conservar el
  orden de llegada y etiquetar cada línea (stdout, stderr, system = stream 3 del daemon). Con TTY hay
  un único stream sin frames: se etiqueta `tty`, porque Docker no permite distinguir stdout de stderr.
- Líneas de máx. 8 KiB (el resto se descarta y se marca `truncated`); `\r` final eliminado; la
  última línea sin `\n` se emite igual. Un frame inválido (p. ej. TTY tratado como multiplexado) da error.
- Sanitizado en el servidor: se eliminan las secuencias ESC (CSI, OSC como títulos o hipervínculos,
  DCS/APC/PM/SOS), los controles C0/C1 (salvo tab) y los overrides bidi/separadores; el UTF-8 inválido
  pasa a U+FFFD. No se escapa HTML: la UI renderiza nodos de texto, nunca `{@html}`. Los colores ANSI se
  pierden (decisión de seguridad y simplicidad).
- `GET /logs?tail=N` (0–5000, 200 por defecto; timeout de 15 s; corta a 2 MiB → `limited`).
  `GET /logs/stream?tail=N`: SSE propio (no el hub), eventos hello, lines (lotes de hasta 200 cada
  200 ms), dropped {count}, heartbeat y end {reason eof|error}. Máximo 8 streams concurrentes (503).
  Cola por stream de 2000 líneas / 1 MiB que descarta las más viejas y avisa con `dropped`. Cerrar la
  conexión cancela el contexto, y con él la petición a Docker.
- Driver sin lectura (Docker devuelve NotImplemented) → 409 `logs_unavailable`, con un mensaje que nombra
  el driver y las alternativas (json-file, local, journald, dual logging).
- Nunca se guardan logs en SQLite ni en disco.

## ADR-015 — Estructura del panel
**Estado:** Aceptada (2026-10-14)
**Decisión:**
- SPA en Svelte 5 sin SvelteKit: router mínimo por hash (`lib/router.svelte.ts`), estado en vivo en
  `lib/live.svelte.ts`, reloj compartido (`lib/clock.svelte.ts`) para los tiempos relativos y
  componentes en `src/components`. Las utilidades visuales compartidas viven en `app.css`.
- La tabla filtra y ordena en el cliente sobre el estado en vivo (la API admite los mismos filtros
  para otros consumidores). Los valores ausentes van siempre al final; preferencias en localStorage
  (con try/catch: sin almacenamiento no se recuerdan). La agrupación por Compose muestra por grupo los
  contenedores en ejecución y sumas con unidad explícita (núcleos, bytes).
- Gráficos con Chart.js 4.5.1 tree-shaken (Line, Point, LinearScale, Tooltip, Legend, Filler), eje X
  lineal en ms sin adaptador de fechas y fijado al rango pedido (`from`–`to`), para que la falta de datos se
  vea. `spanGaps: false`: los nulls son huecos. CPU y memoria muestran promedio y máximo del intervalo; la
  memoria agrega una línea punteada de límite sólo si hay límite explícito. Las series se recargan
  periódicamente (de 10 s a 5 min según el rango).
- Bundle ~245 KB (86 KB gzip), servido embebido; sin CDN.

## ADR-016 — Interfaz de alertas, logs y notificaciones
**Estado:** Aceptada (2026-10-14)
**Decisión:**
- Las mutaciones del panel envían siempre `Content-Type: application/json` y un cuerpo JSON (`{}` si
  no hay datos). El servidor mantiene la exigencia de JSON en toda mutación (ADR-012). Lo destapó un 415
  en el ack: los tests de la API ponían la cabecera siempre.
- Vistas: `#/incidents` (pendientes con sondeo cada 3 s, porque son estado en memoria sin eventos;
  historial paginado de hasta 200 recargado con `live.incidentsVersion`, que avanza con cada evento
  `incident` y cada snapshot), `#/rules` (formulario según el tipo, errores de validación del servidor
  tal cual, aviso de que editar resuelve los abiertos) y `#/maintenance`.
- Notificaciones del navegador: desactivadas por defecto; se activan con un botón (pide permiso) y la
  preferencia se guarda en localStorage. Sólo para incidentes `opened` no suprimidos; `tag` por incidente
  para no duplicar. No hay notificaciones de eventos perdidos durante una desconexión (SSE no es durable).
- Visor de logs: un EventSource por contenedor visible (se cierra al salir o al cambiar el tail), buffer de
  5000 líneas / 4 MiB que descarta las viejas con aviso, render de hasta 2000 líneas coincidentes, pausa
  con cola acotada, búsqueda con resaltado por segmentos de texto (nunca `{@html}`), autoscroll que se
  desactiva al subir. Tras un `end` se cierra el EventSource para que no reconecte solo y repita el tail.
  Si se reconecta por error de red, se descartan las líneas con timestamp ≤ al último visto.

## ADR-017 — Escenario del demo y endurecimiento del servidor
**Estado:** Aceptada (2026-10-14)
**Decisión (demo):**
- `demo.Demo` envuelve `dockertest.Fake` y reproduce en bucle un escenario de 4 min:
  0:20 demo-api-1 healthy (resuelve el unhealthy inicial); 0:30–1:00 demo-cache-1 en bucle de
  reinicios (4 AutoRestart); 1:00–2:30 pico de memoria de demo-api-1 al 90 % de 512 MiB; 1:30 demo-worker-1
  muere (137) y 2:10 vuelve; 2:40 demo-api-1 unhealthy otra vez. Así se ven los 4 tipos de incidente
  abrirse y resolverse.
- La regla "Demo: servicio detenido" (not_running, demo/worker, 10 s) se crea sólo si falta, y sólo en
  `dockerscope-demo.db`. El estado de ejecución que consultan los logs sintéticos sale del fake.
- El escenario tiene un factor de escala de tiempo (tests: un ciclo ≈ 48 ms).
**Decisión (servidor):**
- `MaxHeaderBytes` 64 KiB, `ReadHeaderTimeout` 5 s, `IdleTimeout` 60 s. Sin `ReadTimeout` ni
  `WriteTimeout`: en net/http un ReadTimeout que vence durante el handler cancela el contexto de la petición
  y cortaría los SSE. Los cuerpos están limitados a 64 KiB y cada escritura SSE tiene su propio deadline.
- Los logs del agente sólo registran IDs cortos, nombres, contadores y errores; nunca `Config.Env` (no
  se lee), etiquetas ni contenido de logs.
- Se auditó con govulncheck y npm audit (sin hallazgos) y se comprobó que la CSP no produce violaciones.
**Riesgo aceptado:** escuchar en loopback protege de la red, pero no de otros usuarios locales del mismo
host: cualquiera puede abrir el panel y leer metadatos, etiquetas y logs de los contenedores sin pertenecer al
grupo docker. SPEC no pide autenticación local. Se documenta en el README (usar en hosts de un solo
usuario o confiables) y queda como mejora posible (socket unix con permisos o token).
