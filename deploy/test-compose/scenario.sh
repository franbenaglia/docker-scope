#!/usr/bin/env sh
# Escenarios controlados sobre el Compose de prueba de DockerScope.
# Sólo actúa sobre el proyecto "dockerscope-test" (nunca sobre otros contenedores).
set -eu

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
dc() { docker compose -f "$DIR/compose.yaml" "$@"; }

case "${1:-}" in
  up)
    mkdir -p "$DIR/state"
    rm -f "$DIR/state/crash"
    dc up -d
    ;;
  down)
    dc down --remove-orphans
    rm -rf "$DIR/state"
    ;;
  unhealthy)
    dc exec health rm -f /tmp/healthy
    echo "health pasará a unhealthy en ~10 s (2 chequeos de 5 s)"
    ;;
  healthy)
    dc exec health touch /tmp/healthy
    ;;
  crash)
    mkdir -p "$DIR/state"
    touch "$DIR/state/crash"
    echo "flaky saldrá con código 3 y entrará en bucle de reinicios (on-failure:5)"
    ;;
  fix)
    rm -f "$DIR/state/crash"
    dc start flaky
    ;;
  stop-web)
    dc stop web
    ;;
  start-web)
    dc start web
    ;;
  status)
    dc ps -a --format 'table {{.Name}}\t{{.State}}\t{{.Status}}'
    ;;
  *)
    echo "uso: $0 up|down|unhealthy|healthy|crash|fix|stop-web|start-web|status" >&2
    exit 2
    ;;
esac
