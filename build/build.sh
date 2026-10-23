#!/usr/bin/env bash
# Linear build script — local and GitHub Actions.
# Usage:
#   ./build/build.sh              # same as: all
#   ./build/build.sh deps|test|build|image|up|down|all
#   TARGET=mock-server ./build/build.sh build
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${ROOT}/build"
BACKEND_DIR="${ROOT}/backend"
OUT_DIR="${BUILD_DIR}/out"
COMPOSE_FILE="${BUILD_DIR}/docker-compose.yml"
TARGET="${TARGET:-mock-server}"
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

step_build() {
  log "build: ${TARGET} → ${OUT_DIR}/${TARGET}"
  need go
  mkdir -p "${OUT_DIR}"
  case "${TARGET}" in
    mock-server)
      (
        cd "${BACKEND_DIR}"
        CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "${OUT_DIR}/mock-server" ./mock-server
      )
      ;;
    *)
      die "unknown TARGET=${TARGET} (supported: mock-server)"
      ;;
  esac
  log "build ok: $(ls -la "${OUT_DIR}/${TARGET}")"
}

step_image() {
  log "image: docker build mock-server"
  need docker
  docker build \
    -f "${BUILD_DIR}/Dockerfile.mock-server" \
    -t st-do-asign/mock-server:local \
    "${ROOT}"
}

step_up() {
  log "up: docker compose"
  need docker
  compose up -d --build
  log "mock-server → http://localhost:8081"
  log "  GET  /{tenantId}/deviceList"
  log "  POST /{tenantId}/registerDevice  (header: US-API-Key)"
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
