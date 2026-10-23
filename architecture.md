# Architecture — Distributed Rate Limiter

## Language & Technology Choices

| Layer | Choice | Role |
|-------|--------|------|
| Control plane API | **Go** | Admin config APIs: keys, paths, upstreams, rate-limit policies |
| Data plane proxy | **Go** | Inline reverse proxy; server-side rate limiting before forwarding |
| Load generator | **Go (Vegeta)** | Configurable ramp / burst / constant load against the proxy |
| Mock upstream | **Go** | Simple mock APIs with random delay (no auth) |
| Console UI | **React** | Single UI; Control Plane and Data Plane tabs |
| Policy / config store | **MongoDB** | Durable namespaces, rules, algorithm configs (document model) |
| Shared runtime state | **Redis + Lua** | Atomic quota counters across proxy instances |
| Local bootstrap | **Docker Compose** | MongoDB, Redis, ≥2 proxy replicas, control plane, mock upstream, UI |

### Why this split

- **MongoDB** owns *what the limit is* and *how traffic is matched* (namespaces, polymorphic rules, algorithm configs). Loose document shapes fit header/path/regex matchers without schema migrations.
- **Redis + Lua** owns *how much has been consumed*. All proxy replicas evaluate against the same counters under contention.
- **Control plane** mutates config; **data plane** enforces it on the request path. Auth is explicitly out of scope.

Schema detail: [`ratelimiter-schema.md`](ratelimiter-schema.md) (Mongo), [`redis-schema.md`](redis-schema.md) (quota). Runtime flows: [`sequences.md`](sequences.md).

---

## Architecture Setup

The system is split into a **control plane** (configure) and a **data plane** (enforce + validate). One React console serves both, separated by tabs.

### 1) Control Plane

Admin surface for rate-limiter configuration. All durable config is written here and read by the data plane at evaluation time.

| Component | Responsibility |
|-----------|----------------|
| **React UI — Rate Limiter page** | Operator console for CRUD on namespaces, rules, algorithm configs; control-plane self-tests |
| **Control Plane API (Go)** | REST admin APIs backing the UI; validates and persists config |
| **MongoDB** | Source of truth for namespaces, match rules, upstream targets, and algorithm configs |

**Registrations managed here**

- **Key** — tenant / client identifier used as the rate-limit subject
- **Path** — request path (or namespace) the limit applies to
- **Upstream** — backend target the proxy forwards allowed traffic to

No request forwarding or load generation happens on this plane.

### 2) Data Plane

Request path and validation tooling. The proxy sits **inline** as a reverse proxy: rate-limit decision first, then forward to the mock (or registered) upstream. **No authentication** — auth scope is intentionally excluded.

| Component | Responsibility |
|-----------|----------------|
| **React UI — Data Plane tab** | Configure and trigger Vegeta load profiles; observe allow/deny / quota signals from runs |
| **Inline reverse proxy (Go)** | Receives client traffic; evaluates rate limit (Redis/Lua); allows → forward upstream, denies → reject with quota headers/body; `GET /api/v1/quota` peek |
| **Proxy replicas (≥2)** | `rate-limiter-a` + `rate-limiter-b` behind nginx `rate-limiter`; shared Redis state |
| **Vegeta load client (Go)** | Configurable generator: **ramp**, **burst**, and **constant** load flavors to validate enforcement |
| **Mock backend server (Go)** | Dummy APIs with **random delay**; stands in for a real upstream so limiting behavior is observable without business logic |

### Shared infrastructure

| Component | Responsibility |
|-----------|----------------|
| **Redis + Lua** | Shared counters / token state; atomic check-and-consume across proxy instances |
| **MongoDB** | Read by data plane for active namespaces + rules + configs (cached) |
| **Docker Compose** | Local bootstrap of all components above |

---

## Component Diagram

```mermaid
flowchart TB
  subgraph ui [React_Console]
    CPTab[ControlPlane_Tab]
    DPTab[DataPlane_Tab]
  end

  subgraph controlPlane [Control_Plane]
    CPAPI[ControlPlane_API_Go]
    Mongo[(MongoDB_namespaces_rules_configs)]
  end

  subgraph dataPlane [Data_Plane]
    Vegeta[Vegeta_Load_Client_Go]
    LB[nginx_rate_limiter_LB]
    Proxy1[rate_limiter_a]
    Proxy2[rate_limiter_b]
    Mock[Mock_Backend_random_delay]
    Redis[(Redis_Lua_shared_state)]
  end

  CPTab -->|admin_CRUD| CPAPI
  CPAPI -->|persist_namespaces_rules_configs| Mongo

  DPTab -->|configure_and_start_load| Vegeta
  Vegeta -->|HTTP_load| LB
  LB --> Proxy1
  LB --> Proxy2

  Proxy1 -->|poll_policy_cache| Mongo
  Proxy2 -->|poll_policy_cache| Mongo
  Proxy1 -->|EVAL_consume_or_peek| Redis
  Proxy2 -->|EVAL_consume_or_peek| Redis

  Proxy1 -->|allow_forward| Mock
  Proxy2 -->|allow_forward| Mock
  Proxy1 -.->|deny_no_forward| Drop[RateLimited_Response]
  Proxy2 -.->|deny_no_forward| Drop
```

**Separation summary**

- Left / top (**control plane**): UI Rate Limiter page → Control Plane API → MongoDB only.
- Right / bottom (**data plane**): UI Vegeta page → nginx LB → proxy replicas → Redis (decide) + Mongo (cached policy) → Mock upstream on allow.
- Planes meet only at **MongoDB** (config) and **Redis** (shared quotas). Proxies never mutate Mongo on the hot path. Quota peek: `GET /api/v1/quota` on the LB.

---

## Clients and Functionality

| Client | Plane | Functionality |
|--------|-------|----------------|
| **React Console — Rate Limiter** | Control | CRUD namespaces / rules / algorithm configs; run control-plane test suites |
| **React Console — Vegeta Client** | Data | Select load flavor (**ramp** / **burst** / **steady**), start/stop runs, inspect allow/deny outcomes |
| **Control Plane API** | Control | Admin REST for namespace / rule / config CRUD; sole writer to MongoDB |
| **Vegeta load client** | Data | HTTP load generator aimed at `rate-limiter` LB; validates limits under sustained, spiked, and ramped traffic |
| **Inline reverse proxy** | Data | Match → Redis Lua → forward or 429; dual replicas behind nginx; `GET /api/v1/quota` peek |
| **Mock backend** | Data | Upstream stand-in with ~500ms delay; no auth; proves forward-on-allow |

---

## Explicit non-goals (current scope)

- Authentication / authorization on either plane
- Real third-party upstreams (mock only for local validation)
- Adaptive limits and DigitalOcean deploy (extensions later)

---

## Implementation status

All interview slices below are **done**. See [`README.md`](README.md) assignment coverage and [`sequences.md`](sequences.md) for runtime flows.

### Locked defaults

| Decision | Choice | Why |
|----------|--------|-----|
| Algorithms | **fixed_token**, **fixed_window**, **sliding_window** | Catalog in Mongo; Lua per algorithm |
| Match model | **namespaces** + **rules** (`header` / `path` / `path_attr` / `method` / `regex`) | Flexible subjects without SQL join sprawl |
| Config store | **MongoDB** | Polymorphic rules + algorithm params |
| Hot state | **Redis + Lua** | Atomic check-and-consume across ≥2 proxy replicas |
| Hot reload | Mongo poll every `2s` | No proxy restart on rule/config change |
| Fail mode | Fail-closed on Redis errors (deny) | Safer demo default; log clearly |
| Observe | `GET /api/v1/quota` | Peek remaining / consumed / reset without consume |

Schema: [`ratelimiter-schema.md`](ratelimiter-schema.md), [`redis-schema.md`](redis-schema.md).

### Slice checklist

1. **Infra** — MongoDB + Redis on `st_net` ✅
2. **Schema + seed** — algorithms / demo namespace + rules ✅
3. **Control plane API + UI** — CRUD namespaces / rules / configs ✅
4. **Policy cache** — proxy loads active namespaces+rules (2s poll) ✅
5. **Lua evaluate** — fixed_token / fixed_window / sliding_window ✅
6. **Inline proxy** — match rules → Lua → allow forward / 429 ✅
7. **Multi-replica** — `rate-limiter-a` + `rate-limiter-b` behind nginx LB; shared Redis ✅
8. **Point vegeta** — `RATE_LIMITER_URL=http://rate-limiter:8080` ✅
9. **Quota observe** — `GET /api/v1/quota` (peek, no consume) ✅

### Redis key sketch

```
rl:{algorithm}:{namespaceId}:{subject}
```

Full detail: [`redis-schema.md`](redis-schema.md).

### Mongo ownership

- **Control plane** is the only writer.
- **Data plane** reads namespaces / rules / configs (cached); never mutates Mongo on the request path.

### Success criteria for the demo

- Steady under limit → high success, mock logs show forwards
- Burst over `burst` → 429s appear; latency matrix + status codes in vegeta history
- Two proxies → same limits (no double quota); `X-Proxy-Instance` shows `a`/`b`
- Quota peek reflects consumption without changing it
