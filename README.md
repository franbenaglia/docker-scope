# DockerScope

Monitor local de Docker: un único ejecutable (Go) que observa el Docker de la misma máquina y sirve un
panel web (Svelte) con contenedores, recursos, reinicios, salud, logs e incidentes. No necesita servidor
central, servicios externos ni Internet una vez compilado.

DockerScope **sólo lee** de Docker: no ofrece exec, stop, restart, remove ni acceso a endpoints arbitrarios.

- [Requisitos](#requisitos)
- [Compilar](#compilar)
- [Ejecutar y acceder al panel](#ejecutar-y-acceder-al-panel)
- [Configuración](#configuración)
- [Permisos del socket de Docker](#permisos-del-socket-de-docker)
- [Instalación como servicio (systemd)](#instalación-como-servicio-systemd)
- [Acceso remoto: túnel SSH](#acceso-remoto-túnel-ssh)
- [Datos y retención](#datos-y-retención)
- [Métricas: fórmulas y unidades](#métricas-fórmulas-y-unidades)
- [Alertas e incidentes](#alertas-e-incidentes)
- [Logs](#logs)
- [Modo demo](#modo-demo)
- [Entorno de prueba (Compose)](#entorno-de-prueba-compose)
- [Seguridad](#seguridad)
- [Limitaciones](#limitaciones)
- [Desarrollo](#desarrollo)

## Requisitos

**Para ejecutar**

- Linux con Docker Engine accesible por socket (API 1.40 o superior; probado con Docker 29.1, API 1.52,
  cgroup v2). La versión de la API se negocia automáticamente.
- Nada más: el binario incluye el panel. No requiere Node, CDN ni conexión a Internet.

**Para compilar**

- Go 1.27 (ver `go.mod`).
- Node.js ^20.19 o ≥ 22.12 y npm (sólo para compilar el panel; Vite 8 lo exige).
- `make`.

## Compilar

```sh
make build          # → bin/dockerscope (panel embebido, sin cgo)
make check          # go vet + tests Go + svelte-check + build completo
```

`make build` instala las dependencias del panel con `npm ci` (respetando `web/package-lock.json`) y compila
con `-trimpath` y `CGO_ENABLED=0`. Las versiones de todas las dependencias están fijadas.

## Ejecutar y acceder al panel

```sh
./bin/dockerscope
```

Abrir **http://127.0.0.1:8080**. El agente:

1. se conecta a Docker (por defecto `unix:///var/run/docker.sock`, o `DOCKER_HOST`);
2. lista todos los contenedores (incluidos los detenidos) y escucha sus eventos;
3. muestrea métricas cada 5 s (una sola recolección compartida por todos los navegadores);
4. guarda el historial en `./data/dockerscope.db` y evalúa las reglas de alerta.

Si Docker se reinicia o deja de responder, el panel lo indica y muestra el **último estado conocido**
marcado como desactualizado (no como "todos detenidos"); el agente reconecta solo, con espera creciente.

Detener con Ctrl+C o `SIGTERM`: el apagado es ordenado (cierra streams y escribe lo pendiente).

## Configuración

Valores por defecto < archivo JSON (`-config`) < flags.

| Flag | Clave JSON | Por defecto | Descripción |
|---|---|---|---|
| `-config` | — | — | Archivo de configuración JSON (se rechazan claves desconocidas). |
| `-listen` | `listen` | `127.0.0.1:8080` | Dirección del panel. **Sólo loopback** (127.0.0.1, ::1, localhost); otra dirección es un error. |
| `-docker-host` | `docker_host` | `DOCKER_HOST` o `unix:///var/run/docker.sock` | Endpoint de Docker. |
| `-data-dir` | `data_dir` | `./data` | Directorio de la base SQLite (se crea con permisos 0700). |
| `-interval` | `sample_interval` | `5s` | Intervalo de muestreo (1s–5m). |
| — | `retention` | `168h` (7 días) | Retención del historial de métricas (1h–365d). |
| `-demo` | `demo` | `false` | Modo demo con datos simulados (ver abajo). |
| `-allow-origin` | `allowed_origins` | — | Orígenes extra (loopback) permitidos para modificar datos; útil sólo en desarrollo. |

Ejemplo: [deploy/config.example.json](deploy/config.example.json).

```json
{
  "listen": "127.0.0.1:8080",
  "docker_host": "unix:///var/run/docker.sock",
  "data_dir": "/var/lib/dockerscope",
  "sample_interval": "5s",
  "retention": "168h"
}
```

## Permisos del socket de Docker

DockerScope necesita leer el socket de Docker. **Tener acceso a ese socket equivale a tener root en el
host**: la API de Docker permite crear contenedores privilegiados y montar el sistema de archivos. Aunque
DockerScope sólo hace lecturas, cualquier proceso con ese acceso podría hacer mucho más.

- **Recomendado:** ejecutarlo como un usuario dedicado (`dockerscope`) que pertenezca al grupo `docker`,
  con la unidad systemd de ejemplo, que además aísla el proceso.
- Alternativa en una máquina personal: ejecutarlo con su propio usuario si ya pertenece al grupo `docker`.
- **No** haga `chmod 666 /var/run/docker.sock`: daría root a cualquier usuario del sistema.
- Montar el socket con `:ro` (por ejemplo, si lo ejecuta dentro de un contenedor) **no** convierte la API
  en sólo lectura: `:ro` afecta al archivo, no a las peticiones que se hacen por él.
- **Docker rootless:** apunte al socket del usuario, por ejemplo
  `-docker-host unix:///run/user/$(id -u)/docker.sock` (o `DOCKER_HOST`). Para que haya métricas de CPU y
  memoria, systemd debe delegar los controladores de cgroup v2 al usuario (ver la documentación de Docker
  rootless, "Limiting resources"); sin delegación esos valores aparecen como no disponibles.

## Instalación como servicio (systemd)

Unidad de ejemplo: [deploy/systemd/dockerscope.service](deploy/systemd/dockerscope.service). Como root:

```sh
useradd --system --no-create-home --shell /usr/sbin/nologin dockerscope
install -m 0755 bin/dockerscope /usr/local/bin/dockerscope
install -d -m 0750 -g dockerscope /etc/dockerscope
install -m 0640 -g dockerscope deploy/config.example.json /etc/dockerscope/config.json
install -m 0644 deploy/systemd/dockerscope.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now dockerscope
journalctl -u dockerscope -f
```

La unidad:

- corre como `dockerscope` con el grupo suplementario `docker`;
- crea `/var/lib/dockerscope` con permisos 0700 (`StateDirectory`) y usa `UMask=0077`;
- aísla el proceso: sistema de archivos de sólo lectura, sin capabilities, sin acceso a `/home`, filtro de
  syscalls y tráfico IP limitado a localhost (`IPAddressAllow=localhost`).

## Acceso remoto: túnel SSH

El panel nunca escucha fuera de loopback y no tiene autenticación propia. Para verlo desde otra máquina,
use un túnel SSH:

```sh
ssh -N -L 8080:127.0.0.1:8080 usuario@servidor
# luego, en su máquina: http://127.0.0.1:8080
```

## Datos y retención

| Qué | Dónde | Cuánto tiempo |
|---|---|---|
| Muestras crudas (una por contenedor y ronda) | `data_dir/dockerscope.db` | min(24 h, retención) |
| Resúmenes por minuto (promedio y máximo) | idem | `retention` (7 días por defecto) |
| Incidentes resueltos | idem | 90 días |
| Contenedores eliminados (nombre/imagen, para el historial) | idem | hasta que no queden datos suyos |
| Logs de contenedores | **no se guardan** | — |

- La base es SQLite (modo WAL), con permisos 0600 en un directorio 0700. El modo demo usa otro archivo
  (`dockerscope-demo.db`): los datos simulados nunca se mezclan con los reales.
- La limpieza corre cada hora y los resúmenes por minuto cada minuto.
- Los tiempos se guardan en UTC; el panel los muestra en la hora local del navegador.
- Los gráficos piden como máximo 300 puntos: dentro de las últimas 24 h se agrupan las muestras crudas; para
  rangos más largos, los resúmenes por minuto. Un hueco en el gráfico significa "sin datos".

## Métricas: fórmulas y unidades

Se calculan entre dos muestras consecutivas tomadas por el propio agente. Un valor **no disponible** se
muestra como "—" o "n/d", nunca como un 0 inventado: primera muestra, contador que retrocede (reinicio del
contenedor), intervalo demasiado largo (más de 10× el intervalo) o dato que Docker no informa.

**CPU — % de un núcleo** (como `docker stats`):

```
CPU% = (Δ uso de CPU del contenedor / Δ uso de CPU del sistema) × CPUs en línea × 100
```

100 % equivale a un núcleo completo, así que en un host de 8 CPUs el valor puede llegar a 800 %.
Las reglas de CPU usan la misma unidad.

**Memoria — uso sin caché inactiva:**

```
uso = usage − inactive_file          (cgroup v2)
uso = usage − total_inactive_file    (cgroup v1)
% = uso / límite efectivo × 100
```

Si Docker no informa la caché se usa `usage` tal cual (el panel lo indica). Si el contenedor **no tiene
límite configurado** (`--memory`), Docker informa como límite la memoria del host: el panel lo muestra
como "sin límite" y el porcentaje es respecto del host.

**Red:** bytes recibidos/enviados acumulados (suma de todas las interfaces) y tasas en bytes/s. Sin red
(`--network none`): no disponible.

**Disco (block I/O):** bytes leídos/escritos por el cgroup y sus tasas. Es **tráfico de I/O, no espacio
ocupado**. En cgroup v2, Docker no informa nada mientras el contenedor no hizo I/O; el panel muestra
"n/d" (donde `docker stats` muestra 0 B).

**Totales del resumen:** se suman núcleos (no porcentajes) y bytes, con el denominador explícito del host
y la cantidad de contenedores incluidos, por ejemplo "2,26 núcleos de CPU en uso, de 8 del host (suma de 3
de 3 contenedores en ejecución)".

## Alertas e incidentes

Reglas (panel → **Reglas**) por contenedor (nombre), proyecto o servicio de Docker Compose:

| Tipo | Condición | Unidad del umbral |
|---|---|---|
| `not_running` | Un contenedor vigilado deja de ejecutarse o se elimina. **Requiere alcance.** | — |
| `unhealthy` | El healthcheck sigue en *unhealthy* | — |
| `restarts` | Al menos N reinicios observados dentro de una ventana | reinicios |
| `cpu` | CPU por encima del umbral | % de un núcleo |
| `memory` | Memoria por encima del umbral | % del límite (o del host si no hay límite) |

Reglas incluidas: *unhealthy* durante 30 s, 3 reinicios en 5 min y memoria > 80 % durante 60 s
(habilitadas); CPU > 90 % durante 2 min (de ejemplo, deshabilitada).

Ciclo de un incidente: **pendiente** (la condición se cumple pero falta la duración mínima) → **abierto** →
**resuelto**. Cada regla define:

- **duración mínima** antes de abrir;
- **cooldown**: tiempo mínimo entre resolver y volver a abrir;
- **recuperación**: tiempo con la condición despejada antes de resolver (evita el parpadeo);
- **histéresis** (CPU/memoria): un incidente abierto sólo se resuelve por debajo de *umbral − histéresis*.

Hay un único incidente abierto por regla y contenedor. **Reconocer** un incidente indica que alguien lo está
mirando; **no lo resuelve**. Cada incidente incluye una **posible causa** basada en el código de salida
(137 = SIGKILL, 143 = SIGTERM, OOM…), el error de Docker y los eventos recientes, siempre redactada como
posibilidad.

Otros detalles:

- Los contenedores que ya estaban detenidos cuando arrancó el agente no generan alertas *not_running*.
- *not_running* sigue al contenedor por nombre: si se elimina y se recrea con el mismo nombre (otro ID) y
  queda en ejecución, el incidente se resuelve como "recreado".
- Mientras Docker no responde no se evalúa nada: no se confunde la desconexión con contenedores caídos.
- **Modo mantenimiento** (panel → **Mantenimiento**), global o por contenedor, con vencimiento (hasta 30
  días): se sigue recolectando y evaluando, pero los incidentes que se abren quedan marcados "en
  mantenimiento" y no generan avisos.
- Los avisos aparecen en el panel. Las **notificaciones del navegador** se activan sólo si usted lo pide
  (panel → **Incidentes**).
- *RestartCount* es el contador de Docker (reinicios por política desde el último inicio manual). Los
  **reinicios observados** los cuenta el agente cada vez que un contenedor que ya había corrido vuelve a
  iniciar, sea cual sea la causa.

## Logs

En el detalle de cada contenedor: últimas 200 líneas (se puede elegir hasta 5000) y nuevas líneas en vivo,
con pausa, limpiar, búsqueda, autoscroll y filtro stdout/stderr.

- stdout y stderr se distinguen salvo en contenedores con **TTY**, donde Docker los mezcla.
- Cada línea se corta a 8 KiB. El visor guarda hasta 5000 líneas / 4 MiB y descarta las más viejas
  avisando; si el navegador se atrasa, el agente descarta líneas y lo informa.
- El texto se **sanea** (se quitan colores, secuencias de terminal y caracteres de control) y se muestra
  siempre como texto, nunca como HTML.
- Docker sólo permite leer logs con los drivers `json-file`, `local` y `journald` (o con la caché de *dual
  logging* activada). Con otros (`none`, `syslog`…) el panel lo explica.
- Los logs **no se guardan**.

## Modo demo

```sh
make demo                     # o: ./bin/dockerscope -demo
```

Simula un host con contenedores de un proyecto `demo`, métricas y logs. Un escenario de 4 minutos que se
repite muestra el ciclo de las alertas: un servicio *unhealthy* que se recupera y vuelve a fallar, un
bucle de reinicios, un pico de memoria y un worker que se detiene y vuelve. El panel muestra el aviso
**MODO DEMO** y los datos van a `dockerscope-demo.db`.

## Entorno de prueba (Compose)

[deploy/test-compose](deploy/test-compose) define el proyecto `dockerscope-test` con un servicio normal
(`web`), uno con HEALTHCHECK controlable (`health`) y uno con fallo controlado (`flaky`, `restart:
on-failure:5`). El script sólo actúa sobre esos contenedores.

```sh
make test-env-up                                  # levanta el entorno
deploy/test-compose/scenario.sh unhealthy         # health → unhealthy (~10 s)
deploy/test-compose/scenario.sh healthy           # recupera
deploy/test-compose/scenario.sh crash             # flaky sale con código 3 → bucle de reinicios
deploy/test-compose/scenario.sh fix               # lo arregla y lo vuelve a iniciar
deploy/test-compose/scenario.sh stop-web          # detiene web (start-web lo inicia)
deploy/test-compose/scenario.sh status
make test-env-down                                # elimina todo
```

Recorrido sugerido: abra el panel, filtre por el proyecto `dockerscope-test`, cree una regla `not_running`
con alcance *proyecto = dockerscope-test*, provoque `unhealthy`, `crash` y `stop-web`, y observe en
**Incidentes** cómo se abren (con su posible causa) y cómo se resuelven al recuperar.

## Seguridad

- El panel escucha sólo en loopback; una dirección externa en `listen` es un error de configuración.
  Para acceso remoto, túnel SSH.
- Se valida el encabezado `Host` (mitiga *DNS rebinding*) y el `Origin` en toda modificación; las
  modificaciones exigen JSON (sin CSRF por formularios). No se envían cabeceras CORS. Política CSP estricta:
  todo se sirve desde el propio agente.
- El agente no registra variables de entorno, etiquetas ni contenido de logs en sus propios logs.
- **Hosts compartidos:** escuchar en loopback protege de la red, **no de otros usuarios del mismo host**.
  Cualquier usuario local puede abrir el panel y ver metadatos, etiquetas y logs de los contenedores, aunque
  no pertenezca al grupo `docker`. Úselo en hosts de un solo usuario o donde todos los usuarios sean de
  confianza.

## Limitaciones

- Un solo host; sin autenticación (ver "Hosts compartidos").
- Sólo contenedores Linux.
- Si un contenedor se reinicia varias veces mientras Docker no responde, cuenta como un único reinicio
  observado. Detener e iniciar a mano también cuenta como reinicio observado.
- Los pendientes y el cooldown de las alertas no sobreviven a un reinicio del agente (los incidentes
  abiertos sí).
- Las actualizaciones en vivo no son durables: tras una desconexión el panel vuelve a pedir el estado
  completo, pero no se generan notificaciones por incidentes que ocurrieron mientras estaba desconectado.
- El panel carga hasta 500 contenedores.
- Block I/O "n/d" en cgroup v2 hasta que el contenedor hace I/O; logs sin colores.
- Los rangos de gráfico que empiezan hace más de 24 h usan resúmenes por minuto: el último minuto puede no
  estar todavía.

## Desarrollo

```sh
make dev            # agente en :8080 + Vite en http://127.0.0.1:5173 (recarga en caliente)
make test           # tests Go
make check          # verificación completa (usar antes de cada commit)
make help           # todos los targets
```

| Ruta | Contenido |
|---|---|
| `cmd/dockerscope` | main: flags, configuración, arranque y apagado |
| `internal/docker` | interfaz mínima sobre el SDK de Docker (+ `dockertest`, un daemon falso) |
| `internal/inventory` | estado de contenedores: eventos, reconciliación, reconexión |
| `internal/collector` | muestreo y fórmulas de métricas |
| `internal/store` | SQLite: migraciones, historial, retención, incidentes |
| `internal/alerts` | motor de reglas e incidentes |
| `internal/logs` | lectura, saneado y límites de logs |
| `internal/api`, `internal/sse` | API REST, seguridad HTTP y streaming |
| `internal/demo` | modo demo |
| `web/` | panel Svelte + TypeScript |
| `deploy/` | configuración de ejemplo, systemd, Compose de prueba |

Documentación del proyecto: [especificación](docs/SPEC.md), [arquitectura y contratos de la API](docs/ARCHITECTURE.md),
[decisiones](docs/DECISIONS.md), [plan](docs/PLAN.md) y [progreso](docs/PROGRESS.md).
