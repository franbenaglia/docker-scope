# Instrucciones para Claude Code: DockerScope — agente Docker con Go y Svelte

Quiero que implementes una aplicación completa llamada DockerScope para monitorear Docker localmente. No te limites a diseñar la arquitectura: crea el código, configura el proyecto, ejecuta las verificaciones disponibles y documenta cómo usarlo.

## Objetivo y alcance
El agente y Docker estarán inicialmente en una misma máquina Linux. El usuario abre un panel web local para ver contenedores, recursos, reinicios, estado de salud, logs e incidentes. No requiere servidor central ni servicios pagos. No incluir administración de contenedores en esta primera versión.

## Tecnologías
- Backend y agente en Go, usando el SDK oficial de Docker compatible con las versiones seleccionadas y negociación de versión de API.
- Frontend con Svelte, TypeScript y Vite. No React. No requiere SvelteKit ni un servidor Node en producción.
- Servidor HTTP con net/http; puedes añadir un router pequeño si aporta claridad.
- SQLite para configuración, métricas históricas e incidentes. Prefiere un driver mantenido que facilite una distribución sin dependencias nativas.
- Una biblioteca de gráficos mantenida y apta para series temporales, por ejemplo Chart.js.
- HTTP JSON para consultas y configuración; Server-Sent Events (SSE) para actualizaciones en vivo.
- Frontend compilado embebido en el ejecutable con go:embed.
Consulta documentación oficial para verificar APIs y dependencias antes de usarlas. Fija versiones compatibles y conserva lockfiles.

## Funcionamiento del agente
1. Conectar al Docker local, por defecto mediante unix:///var/run/docker.sock, respetando configuración explícita.
2. Listar todos los contenedores, incluidos los detenidos.
3. Obtener detalles, métricas y healthchecks.
4. Escuchar eventos Docker de inicio, stop, die, destroy, restart y cambios de salud.
5. Reconectar con backoff cuando Docker se reinicie; reconciliar el estado mediante nuevas consultas y deduplicar eventos recuperados.
6. Mostrar claramente Docker desconectado y datos desactualizados. No confundir pérdida de conexión con caída de todos los contenedores.
7. Usar contextos, timeouts, concurrencia limitada y apagado ordenado. Evitar una goroutine sin control por petición.
8. Separar el muestreo de métricas de las peticiones del navegador: múltiples clientes comparten la misma recolección.

## Información de contenedores
Mostrar ID corto, nombre, imagen, estado, uptime, fecha de inicio, código de salida cuando exista, política de reinicio, RestartCount y salud.
Mostrar etiquetas de Docker Compose (proyecto y servicio) cuando existan; permitir agrupar y filtrar por ellas.
Sin HEALTHCHECK, mostrar “Sin healthcheck”; no asumir que equivale a healthy.
Usar el ID completo como identidad. Una recreación con el mismo nombre es un contenedor nuevo.
Distinguir el RestartCount informado por Docker del número de reinicios observados por el agente; no asumir que RestartCount cuenta todos los reinicios manuales.

## Métricas
Intervalo inicial configurable de 5 segundos.
- CPU con cálculo basado en deltas de Docker stats y número de CPUs. Documentar que puede superar 100% en hosts multinúcleo.
- Memoria: uso, límite efectivo y porcentaje. Documentar si se excluye caché y manejar diferencias cgroup v1/v2. No presentar un límite heredado del host como límite explícitamente configurado.
- Red: bytes recibidos/enviados acumulados y tasas a partir de deltas.
- Disco: bytes de lectura/escritura de block I/O cuando estén disponibles; no confundirlo con espacio ocupado.
- Valores ausentes o primer muestreo deben mostrarse como no disponibles cuando corresponda, no como cero inventado.
- Manejar resets de contadores y evitar tasas negativas.
Conservar muestras históricas, por defecto durante 7 días, con limpieza periódica, índices y consultas acotadas. Guardar timestamps UTC y presentar hora local. Reducir puntos para gráficos largos.

## Panel Svelte
Interfaz responsive, legible y con tema oscuro inicial.
- Resumen: contenedores activos/detenidos, unhealthy, incidentes abiertos y conexión al daemon.
- Tabla con búsqueda, filtros, ordenamiento y agrupación por Compose.
- Vista de detalle con metadatos y gráficos de CPU/memoria/red/disco.
- Visor de logs.
- Historial de incidentes.
- Configuración de reglas y modo mantenimiento.
- Estados de carga, error, vacío, reconexión y datos antiguos.
No mostrar un porcentaje agregado engañoso: si sumas CPU o memoria, identifica la unidad y el denominador.
No usar CDN para archivos necesarios: debe funcionar sin Internet después de compilar e instalar.

## Logs
Obtener inicialmente las últimas 200 líneas; permitir elegir un límite acotado y seguir nuevas líneas mediante SSE.
Permitir pausar, limpiar la vista, buscar en el buffer y habilitar/deshabilitar autoscroll.
Distinguir stdout/stderr cuando Docker lo permita; manejar correctamente streams multiplexados y contenedores TTY.
Usar buffers limitados por líneas y bytes, tamaño máximo por línea y cancelación al cerrar o cambiar la vista.
No guardar logs completos en SQLite. Mantener historial de incidentes, no una plataforma de almacenamiento de logs.
Mostrar mensajes útiles cuando el logging driver no permita leer logs.
Renderizar texto de logs de forma segura, sin interpretar HTML ni secuencias de terminal peligrosas.

## Alertas
Implementar reglas seleccionables por contenedor y, opcionalmente, proyecto/servicio Compose:
- Contenedor vigilado que deja de ejecutarse.
- Estado unhealthy persistente.
- Reinicios repetidos dentro de una ventana.
- CPU por encima de un umbral durante un tiempo.
- Memoria por encima de un umbral durante un tiempo.
Ejemplos: unhealthy 30 segundos; memoria >80% durante 60 segundos; 3 reinicios observados en 5 minutos.
Implementar estados pendiente/abierta/resuelta, duración mínima, cooldown, deduplicación e histéresis para evitar fluctuaciones.
Persistir incidentes y mostrar recuperación. Reconocer un incidente no equivale a resolverlo.
El CPU de la regla debe usar una unidad inequívoca y coincidir con la interfaz.
No activar alertas de “se detuvo” por todos los contenedores ya detenidos al primer arranque.
Considerar eventos y códigos de salida para describir una posible causa, sin afirmar que se conoce con certeza la intención.
Incluir modo mantenimiento global y por contenedor con vencimiento configurable; durante mantenimiento continuar recolectando, pero suprimir avisos.
Alertas iniciales dentro del panel; notificaciones del navegador solo como opción solicitada por el usuario. No integrar email, Telegram o servicios externos en esta fase.

## API y streaming
Diseña endpoints REST para resumen, contenedores, detalle, series históricas, incidentes, reglas y mantenimiento.
Usa DTOs definidos, validación, errores consistentes, límites de rangos y paginación.
SSE debe enviar eventos tipados, heartbeats y gestionar desconexiones. Para clientes lentos usa colas acotadas y una política explícita de descarte/resincronización.
Tras reconectar, el frontend vuelve a obtener un snapshot; no asumir que SSE garantiza entrega duradera.
Mantén separados el stream general de métricas/eventos y el stream de logs seleccionado.

## Seguridad y distribución
- Escuchar en 127.0.0.1 por defecto.
- Evitar CORS abierto, validar origen en mutaciones y protegerse frente a accesos desde otros sitios; validar Host para mitigar DNS rebinding.
- No exponer una opción de escucha externa sin autenticación configurada. Para acceso remoto documentar primero un túnel SSH.
- El agente consulta Docker; no implementar exec, stop, restart, remove ni endpoints Docker arbitrarios.
- Explicar en README que acceder al socket concede permisos amplios sobre el host. Montarlo con :ro no convierte la API Docker en solo lectura.
- No pedir chmod 666 para el socket. Documentar permisos y alternativas, incluido Docker rootless.
- Proteger el archivo SQLite y no registrar secretos ni contenidos de logs indiscriminadamente.
- Node solo se requiere para desarrollar/compilar. La aplicación final arranca con un ejecutable y su directorio de datos.

## Organización sugerida
Separar cmd, cliente Docker, colector, reglas, persistencia, API y frontend. Diseña interfaces pequeñas para simular Docker en pruebas. Evita abstracciones innecesarias y dependencias pesadas.
Añade un modo demo explícito con datos simulados para probar el panel sin Docker, claramente identificado y sin mezclar datos demo con reales.

## Entregables
1. Código funcional Go + Svelte/TypeScript.
2. Scripts o Makefile para desarrollo, pruebas y build reproducible.
3. Configuración de ejemplo y migraciones SQLite.
4. Docker Compose de prueba con un servicio normal, uno con HEALTHCHECK y un escenario controlado de fallo/reinicio; solo ejecutar estas pruebas sobre los contenedores de prueba.
5. Unidad systemd de ejemplo con rutas y permisos documentados.
6. README con requisitos, instalación, ejecución, acceso al panel, configuración, permisos del socket, retención, fórmulas de métricas y limitaciones.
7. Resumen final de lo implementado, comandos de uso, verificaciones realizadas y pendientes reales.

## Verificación
- Tests del motor de alertas: duración, recuperación, cooldown, mantenimiento y reinicios en ventana.
- Tests de tasas/deltas y valores ausentes.
- Tests de persistencia y validación API.
- Verificar desconexión/reconexión Docker y cancelación de streams.
- Compilar Go y frontend, ejecutar chequeo TypeScript/Svelte y pruebas disponibles.
- Validar el recorrido: abrir panel, seleccionar contenedor, visualizar métricas, seguir logs, provocar un fallo controlado, observar incidente y recuperación.
Si Docker o alguna dependencia no están disponibles, ejecutar lo verificable con fakes y modo demo, indicando qué no pudo probarse.

Implementa por etapas empezando por una integración real mínima de Docker, Go y Svelte, pero continúa hasta completar el alcance. Toma decisiones razonables sin detenerte por preferencias menores. Antes de modificar un repositorio existente, inspecciónalo y respeta sus instrucciones.

