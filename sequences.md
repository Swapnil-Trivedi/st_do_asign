# End-to-end sequences

How the stack interacts when a **client** (or Vegeta) hits the rate limiter, and how that fits the control / data plane split.

| Doc | Role |
|-----|------|
| [`architecture.md`](architecture.md) | Component map |
| [`ratelimiter-schema.md`](ratelimiter-schema.md) | Mongo policy |
| [`redis-schema.md`](redis-schema.md) | Redis quota keys + Lua |
| **This file** | Runtime call order |

---

## Architecture fit (where the client sits)

```mermaid
flowchart LR
  subgraph clients [Clients]
    App[Client_service]
    Veg[Vegeta_loadgen]
    Op[Operator_UI]
  end

  subgraph controlPlane [Control_plane]
    CP[control_plane_API]
    Mongo[(MongoDB_policy)]
  end

  subgraph dataPlane [Data_plane]
    LB[nginx_rate_limiter]
    A[rate_limiter_a]
    B[rate_limiter_b]
    Redis[(Redis_Lua_quota)]
    Mock[mock_server_upstream]
  end

  Op -->|CRUD_rules_configs| CP
  CP -->|write_only| Mongo

  App -->|HTTP| LB
  Veg -->|HTTP_load| LB
  LB --> A
  LB --> B
  A -->|read_cache_poll| Mongo
  B -->|read_cache_poll| Mongo
  A -->|EVAL_quota| Redis
  B -->|EVAL_quota| Redis
  A -->|allow_forward| Mock
  B -->|allow_forward| Mock
```

**Planes meet only at Mongo** (policy) and **Redis** (shared counters). Clients never call Redis or Mongo directly; operators never call the proxy for config writes.

---

## 1. Cold start — policy into memory

Before the first client request, each proxy replica loads Mongo and starts the poller.

```mermaid
sequenceDiagram
  participant Proxy as rate_limiter_a
  participant Mongo as MongoDB
  participant Redis as Redis

  Proxy->>Redis: PING
  Redis-->>Proxy: PONG
  Proxy->>Mongo: load enabled namespaces rules configs
  Mongo-->>Proxy: snapshot
  Note over Proxy: atomic.Store snapshot
  loop every POLICY_POLL_INTERVAL 2s
    Proxy->>Mongo: reload snapshot
    alt success
      Note over Proxy: atomic swap new snapshot
    else failure
      Note over Proxy: keep last good snapshot log error
    end
  end
```

Same sequence runs independently on `rate-limiter-b`. No restart is required when Mongo policy later changes.

---

## 2. Client request — allow path

Happy path for the seeded demo: `GET /1001/deviceList` + `US-API-Key: ak_demo_1001_alpha`.

```mermaid
sequenceDiagram
  participant Client as Client_service
  participant LB as nginx_LB
  participant Proxy as rate_limiter_a_or_b
  participant Cache as PolicyCache
  participant Redis as Redis_Lua
  participant Upstream as mock_server

  Client->>LB: GET /1001/deviceList + US-API-Key
  LB->>Proxy: round_robin forward
  Proxy->>Cache: match namespaces rules
  Cache-->>Proxy: demo-deviceList + fixed_token params
  Note over Proxy: subject = hdr:US-API-Key:ak_demo_1001_alpha
  Proxy->>Redis: EVAL fixed_token.lua
  Redis-->>Proxy: allowed=1 remaining limit resetMs
  Proxy->>Upstream: reverse proxy same method path headers
  Upstream-->>Proxy: 200 JSON devices
  Proxy-->>LB: 200 + X-RateLimit-* + X-Proxy-Instance
  LB-->>Client: 200 upstream body
```

---

## 3. Client request — deny path (429)

When the bucket / window is exhausted (e.g. concurrent burst over `burst=20`).

```mermaid
sequenceDiagram
  participant Client as Client_service
  participant LB as nginx_LB
  participant Proxy as rate_limiter_replica
  participant Redis as Redis_Lua

  Client->>LB: GET /1001/deviceList + US-API-Key
  LB->>Proxy: forward
  Proxy->>Proxy: match namespace OK
  Proxy->>Redis: EVAL consume script
  Redis-->>Proxy: allowed=0 remaining=0 resetMs
  Note over Proxy: no upstream call
  Proxy-->>LB: 429 JSON + X-RateLimit-* + Retry-After
  LB-->>Client: 429 rate limit exceeded
```

Redis / Lua errors use the same fail-closed outcome (`429`, `X-RateLimit-Error: redis`) without forwarding.

---

## 4. No matching namespace

Enabled namespaces exist, but rules do not match (wrong key, path, or method).

```mermaid
sequenceDiagram
  participant Client as Client_service
  participant Proxy as rate_limiter_replica
  participant Cache as PolicyCache

  Client->>Proxy: GET /other + wrong key
  Proxy->>Cache: match
  Cache-->>Proxy: no namespace
  Proxy-->>Client: 404 no matching namespace
```

If the snapshot has **zero** enabled namespaces → open response (`{"status":"open"}`) instead of enforcing.

---

## 5. Hot config change (no proxy restart)

Operator updates Mongo via control plane; proxies pick it up on the next poll (≤ ~2s).

```mermaid
sequenceDiagram
  participant Op as Operator
  participant UI as React_UI
  participant CP as control_plane
  participant Mongo as MongoDB
  participant Proxy as rate_limiter_a_and_b
  participant Client as Client_service

  Op->>UI: disable namespace or edit rule
  UI->>CP: PUT /api/v1/namespaces/id
  CP->>Mongo: update document
  CP-->>UI: 200 updated

  Note over Proxy,Mongo: background poll
  Proxy->>Mongo: LoadSnapshot
  Mongo-->>Proxy: new policy
  Note over Proxy: atomic swap — process kept running

  Client->>Proxy: request matching old rules
  Proxy-->>Client: 404 or open depending on new policy
```

Control plane is the **sole Mongo writer**. Proxies never mutate policy on the request path.

---

## 6. Observable quota (peek)

Client or operator inspects consumption **without** consuming a unit.

```mermaid
sequenceDiagram
  participant Client as Client_or_Op
  participant LB as nginx_LB
  participant Proxy as rate_limiter_replica
  participant Cache as PolicyCache
  participant Redis as Redis_Lua

  Client->>LB: GET /api/v1/quota?namespace=demo-deviceList&apiKey=...
  LB->>Proxy: forward
  Proxy->>Cache: resolve namespace by id or label
  Cache-->>Proxy: algorithm + params
  Proxy->>Redis: EVAL peek script
  Redis-->>Proxy: remaining limit resetMs consumed
  Proxy-->>Client: 200 JSON QuotaState
```

Example:

```bash
curl -sS 'http://localhost:8080/api/v1/quota?namespace=demo-deviceList&apiKey=ak_demo_1001_alpha'
```

---

## 7. Multi-instance under load (Vegeta)

Two proxies share Redis so total allows ≈ configured burst/limit, not `2 × limit`.

```mermaid
sequenceDiagram
  participant Veg as Vegeta
  participant LB as nginx_LB
  participant A as rate_limiter_a
  participant B as rate_limiter_b
  participant Redis as Redis
  participant Mock as mock_server

  par concurrent requests
    Veg->>LB: GET deviceList
    LB->>A: request N
    A->>Redis: EVAL consume
    Redis-->>A: allow or deny
    alt allow
      A->>Mock: forward
    end
  and
    Veg->>LB: GET deviceList
    LB->>B: request N+1
    B->>Redis: EVAL consume
    Redis-->>B: allow or deny
    alt allow
      B->>Mock: forward
    end
  end
```

Response header `X-Proxy-Instance: a|b` shows which replica answered; Redis remains the single source of truth for quota.

---

## 8. Operator configures a new limit (full loop)

From empty intent → live enforcement for clients.

```mermaid
sequenceDiagram
  participant Op as Operator
  participant CP as control_plane
  participant Mongo as MongoDB
  participant Proxy as rate_limiter_replicas
  participant Client as Client_service
  participant Redis as Redis
  participant Mock as mock_server

  Op->>CP: POST /api/v1/configs fixed_token sustainedRps burst
  CP->>Mongo: insert algorithm_configs
  Op->>CP: POST /api/v1/namespaces upstream + configId
  CP->>Mongo: insert namespaces
  Op->>CP: POST /api/v1/namespaces/id/rules header path method
  CP->>Mongo: insert rules

  Proxy->>Mongo: poll snapshot
  Mongo-->>Proxy: new namespace compiled

  Client->>Proxy: matching request
  Proxy->>Redis: EVAL
  alt allowed
    Proxy->>Mock: forward
    Mock-->>Client: upstream response
  else denied
    Proxy-->>Client: 429
  end
```

---

## 9. E2E automation (console-api)

UI tab **E2E** → `POST /api/v1/e2e/run` on console-api. Ensures fixtures via control-plane, drives direct and/or multi-Vegeta load, peeks quota, diffs mock logs.

```mermaid
sequenceDiagram
  participant UI as E2E_Tab
  participant CA as console_api
  participant CP as control_plane
  participant Veg as vegeta_client
  participant RL as rate_limiter_LB
  participant Mock as mock_server

  UI->>CA: POST /api/v1/e2e/run groups or setups
  CA->>CP: upsert e2e configs namespaces rules
  CA->>CA: wait policy poll
  par multi_vegeta
    CA->>Veg: POST /api/v1/runs job1
    CA->>Veg: POST /api/v1/runs job2
  and direct
    CA->>RL: concurrent GETs
  end
  CA->>Veg: poll until completed
  CA->>RL: GET /api/v1/quota
  CA->>Mock: GET /api/v1/logs
  CA-->>UI: E2EReport allow_429 analytics
```

---

## Timing cheat sheet

| Step | Typical latency driver |
|------|------------------------|
| Policy poll | `POLICY_POLL_INTERVAL` (default `2s`) — config lag after CRUD |
| Redis Lua | Sub-ms local; shared across replicas |
| Allow forward | Dominated by upstream (mock ~500ms demo delay) |
| Deny | Redis only — no upstream |
| E2E multi-Vegeta | Dominated by vegeta duration + mock latency |

---

## Related ports (local)

| Client talks to | URL |
|-----------------|-----|
| Enforce / peek (LB) | `http://localhost:8080` |
| Replica A / B (debug) | `http://localhost:18080` / `18081` |
| Control plane CRUD | `http://localhost:8091` |
| Vegeta control API | `http://localhost:8090` |
| Mock upstream direct | `http://localhost:8081` |
| E2E automation | `http://localhost:8095/api/v1/e2e/*` |
