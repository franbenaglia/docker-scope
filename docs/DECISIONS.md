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
**Estado:** Pendiente (resolver al inicio de la etapa 1)
**A decidir y verificar en documentación oficial:**
- SDK Docker: módulo actual (`github.com/moby/moby/client` + `github.com/moby/moby/api` vs
  `github.com/docker/docker/client`) y versión; usar negociación de API.
- SQLite sin cgo: candidato `modernc.org/sqlite`.
- Frontend: Svelte (versión estable actual, runes), Vite, TypeScript, `svelte-check`.
- Gráficos: Chart.js (+ adaptador de fechas si hace falta), empaquetado localmente.
