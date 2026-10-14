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
