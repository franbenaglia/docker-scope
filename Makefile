# DockerScope — tareas de desarrollo, pruebas y build.
# Requisitos: Go (ver go.mod), Node ^20.19 o >=22.12 + npm (sólo para compilar el panel).

BIN        := bin/dockerscope
WEB        := web
GOFLAGS_B  := -trimpath -ldflags="-s -w" -buildvcs=false
LISTEN     ?= 127.0.0.1:8080

.PHONY: help dev dev-api dev-web demo test vet web-install web-build web-check build check clean

help: ## Muestra esta ayuda
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

$(WEB)/node_modules: $(WEB)/package.json $(WEB)/package-lock.json
	cd $(WEB) && npm ci
	@touch $@

web-install: $(WEB)/node_modules ## Instala dependencias del frontend (npm ci, respeta el lockfile)

web-build: web-install ## Compila el panel en web/dist
	cd $(WEB) && npm run build
	@touch $(WEB)/dist/.gitkeep

web-check: web-install ## svelte-check (TypeScript + Svelte)
	cd $(WEB) && npm run check

vet: ## go vet
	go vet ./...

test: ## Tests Go
	go test ./...

build: web-build ## Binario único con el panel embebido en bin/dockerscope
	CGO_ENABLED=0 go build $(GOFLAGS_B) -o $(BIN) ./cmd/dockerscope

check: vet test web-check build ## Verificación completa (usar antes de cada commit)
	@echo "check OK"

dev-api: ## Agente Go en modo desarrollo (Docker real)
	go run ./cmd/dockerscope -listen $(LISTEN) -data-dir ./data

dev-web: web-install ## Servidor Vite con recarga en http://127.0.0.1:5173 (proxy /api → agente)
	cd $(WEB) && npm run dev

dev: web-install ## Agente + Vite en paralelo (Ctrl+C detiene ambos)
	@trap 'kill 0' INT TERM EXIT; \
	$(MAKE) --no-print-directory dev-api & \
	$(MAKE) --no-print-directory dev-web & \
	wait

demo: build ## Ejecuta el binario en modo demo (datos simulados, sin Docker)
	$(BIN) -demo -listen $(LISTEN) -data-dir ./data-demo

clean: ## Borra artefactos de build
	rm -rf bin $(WEB)/dist/assets $(WEB)/dist/index.html
