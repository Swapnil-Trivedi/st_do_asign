# Distributed Rate Limiter (`st_do_asign`)

Production-style rate limiting stack: **control plane** (config) + **data plane** (inline proxy + load validation). Auth is out of scope.

| Doc | Purpose |
|-----|---------|
| [`architecture.md`](architecture.md) | Design notes |
| [`report.html`](report.html) | Slide deck (open in browser) |

## Prerequisites

- **Go** 1.27+ (`go version`)
- **Docker** + Compose (`docker compose version`)
- **Node.js** 20+ (React UI — later)
- Optional: `curl` for smoke checks

## Repository layout

```
st_do_asign/
├── architecture.md
├── report.html
├── build/                    # shared build + compose (local & CI)
│   ├── build.sh
│   ├── docker-compose.yml
│   ├── Dockerfile.mock-server   # scratch-based image
│   └── out/                     # local binaries (gitignored)
├── backend/
│   ├── go.mod
│   └── mock-server/          # Chi mock upstream
└── ui/                       # React console (scaffold)
```

## Build script

Single linear script for local use and GitHub Actions later:

```bash
chmod +x build/build.sh   # once

./build/build.sh deps     # go mod download + verify
./build/build.sh test     # go test ./...
./build/build.sh build    # binary → build/out/mock-server
./build/build.sh image    # Docker image (scratch)
./build/build.sh up       # compose up --build
./build/build.sh down     # compose down
./build/build.sh all      # deps → test → build → image
./build/build.sh          # same as all
```

Override binary target (default `mock-server`):

```bash
TARGET=mock-server ./build/build.sh build
```

## Docker Compose

Compose file: [`build/docker-compose.yml`](build/docker-compose.yml).

```bash
./build/build.sh up
# mock-server → http://localhost:8081
```

- Image: `st-do-asign/mock-server:local` (**`FROM scratch`**, ~7MB)
- Bind: `0.0.0.0:8081` inside the container
- No in-container healthcheck (scratch has no shell/`wget`); use host `curl` against `/healthz`

Stop:

```bash
./build/build.sh down
```

### Run mock-server without Docker

```bash
cd backend
MOCK_SERVER_ADDR=0.0.0.0:8081 go run ./mock-server
# or
../build/out/mock-server
```

## Mock server (data-plane upstream)

Chi HTTP mock with ~**500ms** business delay. API keys are **not validated** — used for logging / observability only.

### Host

| Setting | Value |
|---------|--------|
| Default listen | `0.0.0.0:8081` |
| Env override | `MOCK_SERVER_ADDR` |
| Local URL | `http://localhost:8081` |

### Endpoints

| Method | Path | Notes |
|--------|------|--------|
| `GET` | `/healthz` | Liveness |
| `GET` | `/{tenantId}/deviceList` | `tenantId` = int32 |
| `POST` | `/{tenantId}/registerDevice` | JSON body optional |
| `PUT` | `/{tenantId}/registerDevice` | JSON body optional |

### Request header

| Header | Required | Behavior |
|--------|----------|----------|
| `US-API-Key` | No (recommended) | Logged + echoed; not enforced |

### Response observability headers

| Header | Meaning |
|--------|---------|
| `US-API-Key` | Echo of request key |
| `US-Tenant` | Parsed tenant id |
| `US-Upstream-Latency-Ms` | Measured handler latency |
| `US-Upstream-Response` | `mock-server` |
| `US-Mapped-API-Keys` | Comma-separated keys mapped to that tenant |
| `US-API-Key-Match` | `true` if request key ∈ mapped set |

### Mock tenant → API keys

One tenant can have multiple keys:

| Tenant (`int32`) | API keys |
|------------------|----------|
| `1001` | `ak_demo_1001_alpha`, `ak_demo_1001_mobile`, `ak_demo_1001_partner` |
| `1002` | `ak_demo_1002_beta`, `ak_demo_1002_ci` |
| `1003` | `ak_demo_1003_gamma` |

### Smoke test

```bash
curl -D - \
  -H "US-API-Key: ak_demo_1001_alpha" \
  http://localhost:8081/1001/deviceList

curl -D - \
  -H "US-API-Key: ak_demo_1002_ci" \
  -X POST http://localhost:8081/1002/registerDevice \
  -H "Content-Type: application/json" \
  -d '{"id":"dev-003","name":"gate"}'
```

## Configuration summary

| Variable / file | Component | Purpose |
|-----------------|-----------|---------|
| `MOCK_SERVER_ADDR` | mock-server | Listen address (default `0.0.0.0:8081`) |
| `build/docker-compose.yml` | Compose | Service wiring, ports, env |
| `build/Dockerfile.mock-server` | Image | Scratch runtime image |
| `backend/mock-server/main.go` | mock-server | Routes, delays, tenant↔key map, `US-*` headers |

## Status

**Done:** mock upstream + scratch image + shared `build/` script & compose.

**Next:** control-plane API, MySQL/Redis in compose, inline rate-limit proxy, Vegeta client, React console.
