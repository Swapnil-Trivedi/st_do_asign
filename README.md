# Distributed Rate Limiter (`st_do_asign`)

Production-style rate limiting stack: **control plane** (config) + **data plane** (inline proxy + load validation). Auth is out of scope.

| Doc | Purpose |
|-----|---------|
| [`architecture.md`](architecture.md) | Design notes |
| [`ratelimiter-schema.md`](ratelimiter-schema.md) | MongoDB control-plane schema |
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
├── build/
│   ├── build.sh
│   ├── docker-compose.yml       # network: st_net
│   ├── Dockerfile.mock-server   # scratch
│   ├── Dockerfile.vegeta-client # scratch
│   ├── Dockerfile.console-api   # scratch (+ docker.sock at runtime)
│   └── out/                     # local binaries (gitignored)
├── backend/
│   ├── mock-server/             # Chi mock upstream
│   ├── vegeta-client/           # loadgen control API (steady/ramp/burst)
│   └── console-api/             # pipeline control via Docker Engine API
└── ui/                          # React console (scaffold)
```

## Build script

```bash
chmod +x build/build.sh   # once

./build/build.sh deps
./build/build.sh test
./build/build.sh build    # TARGET=all → mock-server + vegeta-client
./build/build.sh image
./build/build.sh up
./build/build.sh down
./build/build.sh all      # deps → test → build → image
```

```bash
TARGET=mock-server ./build/build.sh build
TARGET=vegeta-client ./build/build.sh image
TARGET=all ./build/build.sh build
```

## Docker Compose & network

Compose: [`build/docker-compose.yml`](build/docker-compose.yml).

All services join the bridge network **`st_net`** and reach each other by **service name** (no localhost confusion inside containers).

| Service | Host port | In-network URL |
|---------|-----------|----------------|
| `mongo` | `27017` | `mongo:27017` (db `rate_limiter`) |
| `redis` | `6379` | `redis:6379` |
| `mock-server` | `8081` | `http://mock-server:8081` |
| `vegeta-client` | `8090` | `http://vegeta-client:8090` |
| `console-api` | `8095` | `http://console-api:8095` |
| `rate-limiter` (next) | `8080` | `http://rate-limiter:8080` |

```bash
./build/build.sh up
```

Env for vegeta (compose default until the proxy exists):

| Variable | Default in compose | Meaning |
|----------|--------------------|---------|
| `RATE_LIMITER_URL` | `http://mock-server:8081` | Base URL loadgen attacks (switch to `http://rate-limiter:8080` later) |
| `VEGETA_CLIENT_ADDR` | `0.0.0.0:8090` | Control API listen address |
| `MOCK_SERVER_ADDR` | `0.0.0.0:8081` | Mock upstream listen address |
| `MONGO_ROOT_USER` / `MONGO_ROOT_PASSWORD` | `root` / `stroot` | Mongo root (local demo) |

Control-plane schema: [`ratelimiter-schema.md`](ratelimiter-schema.md).  
Seed: [`build/mongo/001_seed.js`](build/mongo/001_seed.js) (first Mongo volume init).

```bash
# smoke infra
docker exec st-mongo mongosh -u root -p stroot --authenticationDatabase admin rate_limiter --eval 'db.namespaces.find().toArray()'
docker exec st-redis redis-cli PING
```

All runtime images are **`FROM scratch`** (mock-server, vegeta-client, console-api).  
`console-api` talks to the Docker Engine over a mounted `docker.sock` — no Docker CLI / Alpine in the image (~10MB).

```bash
./build/build.sh down
```

## Mock server (upstream)

Chi mock with ~**500ms** delay. API keys are **not validated** — logging / observability only.

| Setting | Value |
|---------|--------|
| Host URL | `http://localhost:8081` |
| In-net | `http://mock-server:8081` |

| Method | Path |
|--------|------|
| `GET` | `/healthz` |
| `GET` | `/{tenantId}/deviceList` |
| `POST` / `PUT` | `/{tenantId}/registerDevice` |

**Request:** `US-API-Key`  
**Response headers:** `US-API-Key`, `US-Tenant`, `US-Upstream-Latency-Ms`, `US-Upstream-Response`, `US-Mapped-API-Keys`, `US-API-Key-Match`

| Tenant | API keys |
|--------|----------|
| `1001` | `ak_demo_1001_alpha`, `ak_demo_1001_mobile`, `ak_demo_1001_partner` |
| `1002` | `ak_demo_1002_beta`, `ak_demo_1002_ci` |
| `1003` | `ak_demo_1003_gamma` |

```bash
curl -D - -H "US-API-Key: ak_demo_1001_alpha" \
  http://localhost:8081/1001/deviceList
```

## Vegeta client (loadgen)

Control API designed for the **Data Plane** UI tab. Sends traffic at configurable RPS through the rate limiter URL (compose DNS).

| Setting | Value |
|---------|--------|
| Host URL | `http://localhost:8090` |
| In-net | `http://vegeta-client:8090` |

### Profiles

| Profile | JSON field | Behavior |
|---------|------------|----------|
| `steady` | `steady` | Constant RPS for `duration` |
| `ramp` | `ramp` | Linear RPS from `fromRps` → `toRps` over `steps` × `stepDuration` |
| `burst` | `burst` | `baseRps` with spikes of `burstRps` every `burstEvery` for `burstDuration` |

### Control API (UI-ready)

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/healthz` | Liveness |
| `GET` | `/api/v1/profiles` | Profile metadata + defaults for forms |
| `GET` | `/api/v1/defaults` | Suggested target (`US-API-Key`, path) |
| `POST` | `/api/v1/runs` | Start a run → `202` + run view |
| `GET` | `/api/v1/runs` | List runs |
| `GET` | `/api/v1/runs/{id}` | Poll status + metrics |
| `POST` | `/api/v1/runs/{id}/stop` | Cancel in-flight run |

### Example — steady via mock (compose network name)

```bash
curl -s -X POST http://localhost:8090/api/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "profile": "steady",
    "target": {
      "baseUrl": "http://mock-server:8081",
      "method": "GET",
      "path": "/1001/deviceList",
      "headers": { "US-API-Key": "ak_demo_1001_alpha" }
    },
    "steady": { "rps": 10, "duration": "15s" }
  }'
```

Ramp / burst:

```bash
# ramp
curl -s -X POST http://localhost:8090/api/v1/runs -H 'Content-Type: application/json' -d '{
  "profile": "ramp",
  "target": {
    "baseUrl": "http://mock-server:8081",
    "method": "GET",
    "path": "/1001/deviceList",
    "headers": { "US-API-Key": "ak_demo_1001_mobile" }
  },
  "ramp": { "fromRps": 2, "toRps": 20, "steps": 4, "stepDuration": "3s" }
}'

# burst
curl -s -X POST http://localhost:8090/api/v1/runs -H 'Content-Type: application/json' -d '{
  "profile": "burst",
  "target": {
    "baseUrl": "http://mock-server:8081",
    "method": "GET",
    "path": "/1002/deviceList",
    "headers": { "US-API-Key": "ak_demo_1002_ci" }
  },
  "burst": {
    "baseRps": 5, "burstRps": 40,
    "burstEvery": "8s", "burstDuration": "2s", "duration": "24s"
  }
}'
```

When the inline proxy lands, set:

```bash
RATE_LIMITER_URL=http://rate-limiter:8080 ./build/build.sh up
# or export in a .env next to compose
```

## Configuration summary

| Variable / file | Component | Purpose |
|-----------------|-----------|---------|
| `st_net` | Compose network | Service-name DNS |
| `MOCK_SERVER_ADDR` | mock-server | Listen address |
| `VEGETA_CLIENT_ADDR` | vegeta-client | Control API listen |
| `RATE_LIMITER_URL` | vegeta-client | Attack base URL (proxy or mock) |
| `build/docker-compose.yml` | Compose | Wiring + network |
| `backend/vegeta-client/` | loadgen | Profiles + UI control API |

## React console

```bash
# terminals: backends up, then UI
./build/build.sh up
cd ui && npm install && npm run dev
# → http://localhost:5173
```

| Page | Plane | What it does |
|------|-------|----------------|
| Overview | — | Compose containers + `st_net` connectivity; health green / red / planned |
| Mock Backend | Data | Live logs + hardcoded API docs in the UI |
| Vegeta Client | Data | Configure steady / ramp / burst and run / stop / metrics |
| Control Plane | Control | Placeholder for keys / paths / rules |

Env overrides: `VITE_MOCK_URL`, `VITE_VEGETA_URL`, `VITE_PROXY_URL`.

Mock extras: `GET /api/v1/logs`, `GET /openapi.json`, `GET /docs`.

## Status

**Done:** mock upstream (+ logs/swagger), vegeta loadgen, scratch images, `st_net`, React console shell.

**Next:** control-plane API, MySQL/Redis, inline `rate-limiter` proxy.
