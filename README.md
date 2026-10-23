# Distributed Rate Limiter (`st_do_asign`)

Production-style rate limiting stack: **control plane** (config) + **data plane** (inline proxy + load validation). Auth is out of scope.

| Doc | Purpose |
|-----|---------|
| [`architecture.md`](architecture.md) | Design notes |
| [`ratelimiter-schema.md`](ratelimiter-schema.md) | MongoDB control-plane schema |
| [`redis-schema.md`](redis-schema.md) | Redis keys, Lua algorithms, peek/consume |
| [`sequences.md`](sequences.md) | End-to-end sequence diagrams (client → proxy → Redis/Mongo) |
| [`report.html`](report.html) | Slide deck (open in browser) |

## Prerequisites

- **Go** 1.27+ (`go version`)
- **Docker** + Compose (`docker compose version`)
- **Node.js** 20+ (React console)
- Optional: `curl` for smoke checks

## Repository layout

```
st_do_asign/
├── architecture.md              # planes + topology
├── ratelimiter-schema.md        # Mongo policy + APIs
├── redis-schema.md              # Redis keys + Lua
├── sequences.md                 # end-to-end sequences
├── report.html                  # slide deck
├── build/
│   ├── build.sh
│   ├── docker-compose.yml       # network: st_net
│   ├── nginx/rate-limiter.conf  # LB → proxy replicas
│   ├── mongo/001_seed.js
│   ├── Dockerfile.*             # scratch apps + rate-limiter / LB
│   └── out/                     # local binaries (gitignored)
├── backend/
│   ├── rate-limiter/            # inline proxy + Redis Lua + quota peek
│   ├── control-plane/           # Mongo admin CRUD
│   ├── mock-server/             # Chi mock upstream
│   ├── vegeta-client/           # loadgen control API
│   └── console-api/             # pipeline control via docker.sock
└── ui/                          # React console
```

## Build script

```bash
chmod +x build/build.sh   # once

./build/build.sh deps
./build/build.sh test
./build/build.sh build    # TARGET=all → all Go services + rate-limiter-lb image target
./build/build.sh image
./build/build.sh up
./build/build.sh down
./build/build.sh all      # deps → test → build → image

# Optional: JUnit + coverage HTML (same as CI)
REPORT_DIR="$(pwd)/reports" ./build/build.sh test
```

```bash
TARGET=rate-limiter ./build/build.sh build
TARGET=control-plane ./build/build.sh image
TARGET=all ./build/build.sh build
```

## CI (GitHub Actions)

Workflow: [`.github/workflows/ci.yml`](.github/workflows/ci.yml).

| Check | What it does |
|-------|----------------|
| **Backend unit tests** | `gotestsum` → `reports/backend-junit.xml` + coverage HTML |
| **UI unit tests** | Vitest JUnit → `reports/ui-junit.xml` |
| **Unit test results** | Publishes JUnit as a commit check |
| **Unit tests green** | Aggregate gate — **green only when all UTs pass** (use this for branch protection) |

Reports are uploaded as workflow artifacts on every run.

## Docker Compose & network

Compose: [`build/docker-compose.yml`](build/docker-compose.yml).

All services join the bridge network **`st_net`** and reach each other by **service name** (no localhost confusion inside containers).

| Service | Host port | In-network URL |
|---------|-----------|----------------|
| `mongo` | `27017` | `mongo:27017` (db `rate_limiter`) |
| `redis` | `6379` | `redis:6379` |
| `rate-limiter` (nginx LB) | `8080` | `http://rate-limiter:8080` |
| `rate-limiter-a` / `-b` | `18080` / `18081` | `http://rate-limiter-a:8080` (replicas) |
| `mock-server` | `8081` | `http://mock-server:8081` |
| `vegeta-client` | `8090` | `http://vegeta-client:8090` |
| `control-plane` | `8091` | `http://control-plane:8091` |
| `console-api` | `8095` | `http://console-api:8095` |

## Assignment coverage

| Requirement | Status | Where |
|-------------|--------|--------|
| **Quota enforcement** — identifier + namespace → allow/deny, remaining, reset; ≥2 algorithms | Done | Proxy path: match → Redis Lua (`fixed_token`, `fixed_window`, `sliding_window`). Headers `X-RateLimit-*`. Subject from API key / tenant / path. |
| **Configuration API** — CRUD rules at runtime, no restart; burst + sustained | Done | Control plane `:8091` CRUD for configs/namespaces/rules. `fixed_token` params: `sustainedRps` + `burst`. Proxy polls Mongo every 2s (atomic snapshot swap). |
| **Multi-instance correctness** — shared state across ≥2 instances | Done | `rate-limiter-a` + `rate-limiter-b` behind nginx `rate-limiter`; all `EVAL` against the same Redis keys. |
| **Observable quota state** — consumption / remaining / reset for an identifier | Done | `GET http://localhost:8080/api/v1/quota?namespace=demo-deviceList&apiKey=ak_demo_1001_alpha` (peek, does not consume). |

```bash
# Enforce (allow → mock upstream; deny → 429)
curl -sS -D- -H 'US-API-Key: ak_demo_1001_alpha' http://localhost:8080/1001/deviceList

# Observe quota (no consume)
curl -sS 'http://localhost:8080/api/v1/quota?namespace=demo-deviceList&apiKey=ak_demo_1001_alpha'

# Hot-reload config (no proxy restart)
curl -sS -X PUT http://localhost:8091/api/v1/namespaces/<id> \
  -H 'Content-Type: application/json' -d '{"enabled":false}'
# wait ≤2s — proxy picks up the change
```

### Control-plane test suites

Console **Testing** tab → **Control plane** (or curl) runs suites against the live control-plane:

| Suite | Coverage |
|-------|----------|
| `unit` | Validators + in-memory API |
| `functional` | Live store CRUD / conflicts |
| `concurrency` | Parallel writers |
| `e2e` | CP → Mongo → rate-limiter → mock (quota, allow, 429) + predefined scenarios: dual API-key configs, tenant-id (single + multi key), per-path limits |
| `all` | Everything above |

```bash
curl -sS -X POST 'http://localhost:8091/api/v1/tests/run?suite=e2e' | jq .
# or: suite=all
```

E2E needs the compose stack up (`RATE_LIMITER_URL` on control-plane). Offline / memory-only runs **skip** stack cases instead of failing CI.

### Data-plane test suites

Console **Testing** tab → **Data plane** (or curl against the LB / a replica) runs suites inside the rate-limiter process:

| Suite | Coverage |
|-------|----------|
| `unit` | Matcher / subject builders / policy-cache swap / Lua algos on miniredis |
| `functional` | Live Redis evaluate/peek, healthz/quota HTTP, match against loaded policy |
| `all` | unit → functional |

```bash
curl -sS -X POST 'http://localhost:8080/api/v1/tests/run?suite=unit' | jq .
curl -sS -X POST 'http://localhost:8080/api/v1/tests/run?suite=functional' | jq .
```

Policy-dependent functional cases **skip** when the cache has no namespaces (empty Mongo / not yet polled).

```bash
./build/build.sh up
```

Env for vegeta:

| Variable | Default in compose | Meaning |
|----------|--------------------|---------|
| `RATE_LIMITER_URL` | `http://rate-limiter:8080` | Base URL loadgen attacks |
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

Go app images are **`FROM scratch`** (mock-server, vegeta-client, console-api, control-plane, rate-limiter).  
LB image is **nginx** (`rate-limiter` / `Dockerfile.rate-limiter-lb`).  
`console-api` talks to the Docker Engine over a mounted `docker.sock`.

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

## Rate limiter (data plane)

Nginx LB on `:8080` → `rate-limiter-a` / `rate-limiter-b` (shared Redis). Policy from Mongo (2s poll). Details: [`redis-schema.md`](redis-schema.md), [`sequences.md`](sequences.md).

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/healthz` | Liveness (`instance` = `a` \| `b`) |
| `GET` | `/api/v1/quota` | Peek quota (no consume) |
| `*` | `/*` | Match → Lua → forward or `429` |

## Vegeta client (loadgen)

Control API for the **Data Plane** UI tab. Default target is the rate-limiter LB (`RATE_LIMITER_URL=http://rate-limiter:8080`).

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

### Control API

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/healthz` | Liveness |
| `GET` | `/api/v1/profiles` | Profile metadata + defaults for forms |
| `GET` | `/api/v1/defaults` | Suggested target (`US-API-Key`, path) |
| `POST` | `/api/v1/runs` | Start a run → `202` + run view |
| `GET` | `/api/v1/runs` | List runs |
| `GET` | `/api/v1/runs/{id}` | Poll status + metrics |
| `POST` | `/api/v1/runs/{id}/stop` | Cancel in-flight run |

### Example — steady through the rate limiter

```bash
curl -s -X POST http://localhost:8090/api/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "profile": "steady",
    "target": {
      "baseUrl": "http://rate-limiter:8080",
      "method": "GET",
      "path": "/1001/deviceList",
      "headers": { "US-API-Key": "ak_demo_1001_alpha" }
    },
    "steady": { "rps": 10, "duration": "15s" }
  }'
```

Ramp / burst (same target; alternate keys may `404` if they do not match seeded rules):

```bash
# ramp
curl -s -X POST http://localhost:8090/api/v1/runs -H 'Content-Type: application/json' -d '{
  "profile": "ramp",
  "target": {
    "baseUrl": "http://rate-limiter:8080",
    "method": "GET",
    "path": "/1001/deviceList",
    "headers": { "US-API-Key": "ak_demo_1001_alpha" }
  },
  "ramp": { "fromRps": 2, "toRps": 20, "steps": 4, "stepDuration": "3s" }
}'

# burst — expect 429s when over burst=20
curl -s -X POST http://localhost:8090/api/v1/runs -H 'Content-Type: application/json' -d '{
  "profile": "burst",
  "target": {
    "baseUrl": "http://rate-limiter:8080",
    "method": "GET",
    "path": "/1001/deviceList",
    "headers": { "US-API-Key": "ak_demo_1001_alpha" }
  },
  "burst": {
    "baseRps": 5, "burstRps": 40,
    "burstEvery": "8s", "burstDuration": "2s", "duration": "24s"
  }
}'
```

Override target (e.g. hit mock directly):

```bash
RATE_LIMITER_URL=http://mock-server:8081 ./build/build.sh up
```

## Configuration summary

| Variable / file | Component | Purpose |
|-----------------|-----------|---------|
| `st_net` | Compose network | Service-name DNS |
| `MOCK_SERVER_ADDR` | mock-server | Listen address |
| `VEGETA_CLIENT_ADDR` | vegeta-client | Control API listen |
| `RATE_LIMITER_URL` | vegeta-client | Attack base URL (default LB) |
| `RATE_LIMITER_ADDR` | rate-limiter replicas | Listen address |
| `REDIS_ADDR` | rate-limiter replicas | Redis host |
| `MONGO_URI` / `MONGO_DB` | control-plane + proxies | Policy store |
| `POLICY_POLL_INTERVAL` | rate-limiter replicas | Hot-reload poll (default `2s`) |
| `build/docker-compose.yml` | Compose | Wiring + network |

## React console

```bash
# terminals: backends up, then UI
./build/build.sh up
cd ui && npm install && npm run dev
# → http://localhost:5173
```

| Page | Plane | What it does |
|------|-------|----------------|
| Overview | — | Compose topology + health (incl. LB + proxy replicas) |
| Rate Limiter | Control | Namespace / config / rule CRUD |
| Testing | Both | Control-plane + data-plane unit / functional (and CP concurrency / e2e) |
| E2E | Data | Stack automation: fixtures, Vegeta, multi-Vegeta concurrency + allow/429 analytics |
| Mock Backend | Data | Live logs + API docs |
| Vegeta Client | Data | Steady / ramp / burst runs against the proxy |

### E2E automation API (console-api `:8095`)

```bash
curl -sS http://localhost:8095/api/v1/e2e/catalog | python3 -m json.tool | head

curl -sS -X POST http://localhost:8095/api/v1/e2e/run \
  -H 'Content-Type: application/json' \
  -d '{"groups":["concurrency"]}'

curl -sS -X POST http://localhost:8095/api/v1/e2e/run \
  -H 'Content-Type: application/json' \
  -d '{"setups":["fixed-token-burst","demo-seed-smoke"]}'
```

Report: allow/429 totals, proxy instance mix, upstream headers, mock log delta, per-job Vegeta metrics.

Env overrides: `VITE_MOCK_URL`, `VITE_VEGETA_URL`, `VITE_PROXY_URL`, `VITE_CONTROL_PLANE_URL`, `VITE_CONSOLE_URL`.

Mock extras: `GET /api/v1/logs`, `GET /openapi.json`, `GET /docs`.

## Status

**Done:** full demo stack — Mongo policy + control-plane CRUD/UI, Redis Lua, dual proxy replicas behind nginx, hot policy poll, quota peek, vegeta → rate-limiter → mock, **E2E automation tab** (fixtures + multi-Vegeta concurrency reports).