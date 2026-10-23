#!/usr/bin/env bash
# Linear build script — local and GitHub Actions.
# Usage:
#   ./build/build.sh              # same as: all
#   ./build/build.sh deps|test|build|image|up|down|all
#   TARGET=mock-server|vegeta-client|console-api|control-plane|rate-limiter|all ./build/build.sh build
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT}/build"
BACKEND_DIR="${ROOT}/backend"
OUT_DIR="${BUILD_DIR}/out"
COMPOSE_FILE="${BUILD_DIR}/docker-compose.yml"
TARGET="${TARGET:-all}"
CMD="${1:-all}"

export PATH="${PATH}:/opt/homebrew/bin:/usr/local/go/bin"

log()  { printf '==> %s\n' "$*"; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || die "missing required tool: $1"; }

compose() {
  if docker compose version >/dev/null 2>&1; then
    docker compose -f "${COMPOSE_FILE}" "$@"
  elif command -v docker-compose >/dev/null 2>&1; then
    docker-compose -f "${COMPOSE_FILE}" "$@"
  else
    die "docker compose not available"
  fi
}

resolve_targets() {
  case "${TARGET}" in
    all) echo "mock-server vegeta-client console-api control-plane rate-limiter rate-limiter-lb" ;;
    mock-server|vegeta-client|console-api|control-plane|rate-limiter|rate-limiter-lb) echo "${TARGET}" ;;
    *) die "unknown TARGET=${TARGET} (mock-server|vegeta-client|console-api|control-plane|rate-limiter|rate-limiter-lb|all)" ;;
  esac
}

step_deps() {
  log "deps: download Go modules"
  need go
  mkdir -p "${OUT_DIR}"
  (
    cd "${BACKEND_DIR}"
    go mod download
    go mod verify
  )
}

step_test() {
  log "test: backend (./...)"
  need go
  (
    cd "${BACKEND_DIR}"
    go test ./...
  )
}

build_one() {
  local name="$1"
  log "build: ${name} → ${OUT_DIR}/${name}"
  (
    cd "${BACKEND_DIR}"
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "${OUT_DIR}/${name}" "./${name}"
  )
}

step_build() {
  need go
  mkdir -p "${OUT_DIR}"
  for t in $(resolve_targets); do
    build_one "${t}"
  done
}

image_one() {
  local name="$1"
  local dockerfile="${BUILD_DIR}/Dockerfile.${name}"
  log "image: docker build ${name}"
  docker build \
    -f "${dockerfile}" \
    -t "st-do-asign/${name}:local" \
    "${ROOT}"
}

step_image() {
  need docker
  for t in $(resolve_targets); do
    image_one "${t}"
  done
}

step_up() {
  log "up: docker compose (network: st_net)"
  need docker
  compose up -d --build
  log "network DNS: mongo, redis, mock-server, rate-limiter-a/b, rate-limiter (LB), vegeta-client, control-plane, console-api"
  log "mongo          → localhost:27017 (db=rate_limiter)"
  log "redis          → localhost:6379"
  log "rate-limiter   → http://localhost:8080  (nginx LB → a/b)"
  log "rate-limiter-a → http://localhost:18080"
  log "rate-limiter-b → http://localhost:18081"
  log "mock-server    → http://localhost:8081"
  log "vegeta-client  → http://localhost:8090"
  log "control-plane  → http://localhost:8091  (namespaces / configs)"
  log "console-api    → http://localhost:8095  (UI pipeline control)"
}

step_down() {
  log "down: docker compose"
  need docker
  compose down
}

step_all() {
  step_deps
  step_test
  step_build
  step_image
}

main() {
  log "root=${ROOT} cmd=${CMD} target=${TARGET}"
  case "${CMD}" in
    deps)  step_deps ;;
    test)  step_test ;;
    build) step_build ;;
    image) step_image ;;
    up)    step_up ;;
    down)  step_down ;;
    all)   step_all ;;
    *)
      die "unknown command '${CMD}' (deps|test|build|image|up|down|all)"
      ;;
  esac
  log "done: ${CMD}"
}

main
