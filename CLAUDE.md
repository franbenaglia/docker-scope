# DockerScope — instrucciones para Claude Code

Monitor local de Docker: agente Go + panel Svelte/TS embebido. El alcance completo está en
[docs/SPEC.md](docs/SPEC.md) (fuente de verdad, **no modificar**).

## Al iniciar cada sesión (obligatorio, en este orden)
1. Leer [docs/PROGRESS.md](docs/PROGRESS.md): etapa actual, último paso hecho, próximo paso, bloqueos.
2. Leer la etapa actual en [docs/PLAN.md](docs/PLAN.md) y las decisiones en [docs/DECISIONS.md](docs/DECISIONS.md).
3. `git status` y `git log --oneline -10` para confirmar que el estado coincide con PROGRESS.md.
   Si hay cambios sin commitear, revisarlos antes de seguir (puede ser trabajo cortado a mitad).
4. Si existe el Makefile, correr `make check` para confirmar que el árbol está en verde.
5. Continuar desde "Próximo paso". No rehacer etapas cerradas.

## Reglas de trabajo
- **Una etapa a la vez.** Una etapa se cierra sólo cuando cumple su criterio "Terminado cuando"
  de PLAN.md y `make check` pasa.
- **Commits pequeños y frecuentes**, cada uno con el árbol compilando. Formato:
  `etapa N: <qué>`. Cierre de etapa: `etapa N: cierre — <resumen>`.
- **Actualizar PROGRESS.md antes de cada commit** (no al final de la sesión: el corte puede
  ocurrir en cualquier momento). Incluirlo en el mismo commit.
- **Decisiones:** toda decisión no trivial (dependencia, versión, unidad, formato, contrato API,
  política) se registra en DECISIONS.md como ADR corto. No contradecir una decisión registrada
  sin añadir un ADR nuevo que la reemplace (marcar el viejo como "Reemplazada por ADR-N").
- **Arquitectura:** al crear o cambiar paquetes, interfaces, endpoints o eventos SSE, actualizar
  [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).
- Marcar en PLAN.md los ítems completados (`[x]`). Si surge trabajo nuevo, añadirlo a la etapa
  correspondiente o a "Pendientes" en PROGRESS.md; no expandir el alcance de SPEC.md.
- Verificar APIs y versiones contra documentación oficial antes de fijarlas (ver SPEC). Fijar
  versiones y commitear `go.sum` y `package-lock.json`.
- Docker real: sólo actuar sobre los contenedores del compose de prueba (`deploy/test-compose`).
  Nunca parar/borrar contenedores ajenos del host.
- Si algo no puede verificarse (falta Docker, red, etc.), registrarlo en PROGRESS.md
  ("Verificaciones pendientes") en vez de darlo por hecho.

## Comandos (se crean en la etapa 1)
- `make dev` — backend + Vite dev server
- `make test` — tests Go (`go test ./...`)
- `make check` — `go vet` + tests Go + `svelte-check` + build frontend + build Go
- `make build` — binario único con el frontend embebido en `bin/dockerscope`
- `make demo` — ejecuta en modo demo (sin Docker)

## Estilo
- Go: stdlib primero, interfaces pequeñas definidas donde se consumen, `context` en toda E/S,
  errores con `%w`. Sin frameworks pesados.
- Frontend: Svelte + TS estricto, sin CDN, sin `{@html}` para contenido de logs.
- Documentación y comentarios de usuario en español; identificadores en inglés.
