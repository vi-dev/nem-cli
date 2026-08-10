#!/usr/bin/env bash
# Ephemeral local OCI registry + catalog for nem development.
#
# up [dir]       start registry, publish dir, wire nem to it
# publish [dir]  re-publish dir + re-sync (registry stays up)
# down           remove the nem catalog entry, stop the registry
# status         report registry container + catalog entry state
#
# dir defaults to a sibling nem-official-catalog checkout.
# NEM overrides how nem is invoked (default: go run ./cmd/nem).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONTAINER=nem-local-registry
IMAGE=registry:3
ADDR=127.0.0.1:5001
REF=localhost:5001/nem-local-catalog:v2
CATALOG_NAME=local
DEFAULT_DIR="$REPO_ROOT/../nem-official-catalog"
NEM=${NEM:-go run ./cmd/nem}

# shellcheck disable=SC2086  # NEM is intentionally word-split (go run ...)
run_nem() { (cd "$REPO_ROOT" && $NEM "$@"); }

die() { echo "error: $*" >&2; exit 1; }

require_docker() {
  command -v docker >/dev/null || die "docker not found on PATH"
  docker info >/dev/null 2>&1 || die "docker daemon not running"
}

registry_running() {
  [ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER" 2>/dev/null)" = "true" ]
}

container_exists() {
  docker inspect "$CONTAINER" >/dev/null 2>&1
}

resolve_catalog_dir() {
  local dir="${1:-$DEFAULT_DIR}"
  [ -d "$dir" ] || die "catalog dir $dir not found (default is the sibling nem-official-catalog checkout; pass a directory argument to override)"
  (cd "$dir" && pwd)
}

wait_ready() {
  for _ in $(seq 1 50); do
    curl -fsS "http://$ADDR/v2/" >/dev/null 2>&1 && return 0
    sleep 0.2
  done
  die "registry did not become ready at http://$ADDR/v2/"
}

up() {
  local dir
  dir="$(resolve_catalog_dir "${1:-}")"
  require_docker
  if registry_running; then
    echo "registry $CONTAINER already running"
  else
    if container_exists && ! registry_running; then
      docker rm "$CONTAINER" >/dev/null
    fi
    docker run --rm -d --name "$CONTAINER" -p "$ADDR:5000" "$IMAGE" >/dev/null \
      || die "failed to start registry (is port 5001 already in use?)"
  fi
  wait_ready
  echo "publishing $dir -> $REF"
  run_nem catalog publish "$REF" "$dir"
  run_nem catalog remove "$CATALOG_NAME" >/dev/null 2>&1 || true
  run_nem catalog add "$CATALOG_NAME" "$REF"
  run_nem catalog update "$CATALOG_NAME"
  echo
  echo "catalog '$CATALOG_NAME' now resolves from $REF"
  echo "tear down with: $0 down"
}

publish() {
  local dir
  dir="$(resolve_catalog_dir "${1:-}")"
  require_docker
  registry_running || die "registry not running; run '$0 up' first"
  run_nem catalog publish "$REF" "$dir"
  run_nem catalog update "$CATALOG_NAME"
}

down() {
  run_nem catalog remove "$CATALOG_NAME" 2>/dev/null || true
  if command -v docker >/dev/null && registry_running; then
    docker stop "$CONTAINER" >/dev/null
  fi
  echo "local catalog torn down"
}

status() {
  if command -v docker >/dev/null && registry_running; then
    echo "registry: running ($CONTAINER at http://$ADDR)"
  else
    echo "registry: not running"
  fi
  if run_nem catalog list 2>/dev/null | grep -qw "$CATALOG_NAME"; then
    echo "catalog entry '$CATALOG_NAME': configured"
  else
    echo "catalog entry '$CATALOG_NAME': not configured"
  fi
}

usage() {
  sed -n '2,10p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

case "${1:-}" in
  up) shift; up "$@" ;;
  publish) shift; publish "$@" ;;
  down) down ;;
  status) status ;;
  *) usage; exit 2 ;;
esac
