# Rate limiter — Redis schema

This document is the **source of truth** for hot quota state stored in Redis.

| Concern | Store |
|---------|--------|
| What to limit, how to match, where to forward | **MongoDB** — see [`ratelimiter-schema.md`](ratelimiter-schema.md) |
| How much quota has been consumed | **Redis + Lua** (this doc) |

- **Data plane proxies** (`rate-limiter-a` / `rate-limiter-b`) are the only writers to Redis on the request path  
- **Control plane** never touches Redis  
- Scripts live under [`backend/rate-limiter/lua/`](backend/rate-limiter/lua/) and are embedded via `//go:embed`  
- Atomicity: every check-and-consume (and every peek) runs as a single Lua `EVAL` / `EVALSHA`

**Local connection**

| | |
|--|--|
| Host | `redis://localhost:6379` |
| In-network (`st_net`) | `redis:6379` |
| Container | `st-redis` (`redis:7.4-alpine`, AOF on) |

```bash
docker exec -it st-redis redis-cli PING
docker exec -it st-redis redis-cli KEYS 'rl:*'
```

---

## Mental model

```
Mongo (policy)                    Redis (quota)
─────────────                     ─────────────
namespace + rules          →      subject identity
algorithm_configs.params   →      Lua ARGV (rate / limit / window)
algorithm id               →      key prefix + which script
```

1. Proxy matches a request to a **namespace** (Mongo cache).  
2. Builds a **subject** string (API key, tenant, or path).  
3. Runs the Lua script for that namespace’s **algorithm**.  
4. Redis returns `{allowed, remaining, limit, resetMs}` (or peek tuple).  
5. All proxy replicas share the same keys → multi-instance correctness.

---

## Key layout

### Primary key form

```
rl:{algorithm}:{namespaceId}:{subject}
```

| Segment | Example | Source |
|---------|---------|--------|
| `algorithm` | `fixed_token` | `namespace.algorithm` / config |
| `namespaceId` | `6adb0f30d436ed6d1d76c931` | Mongo `namespaces._id` (hex) |
| `subject` | `hdr:US-API-Key:ak_demo_1001_alpha` | Built on the request path |

### Subject formats

| Form | When | Example |
|------|------|---------|
| `hdr:{HeaderName}:{value}` | Matched header rule (preferred) | `hdr:US-API-Key:ak_demo_1001_alpha` |
| `tenant:{attr}` | Matched `path_attr` capture | `tenant:1001` |
| `path:{urlPath}` | Fallback | `path:/1001/deviceList` |

Demo subject (seed namespace):

```
rl:fixed_token:6adb0f30d436ed6d1d76c931:hdr:US-API-Key:ak_demo_1001_alpha
```

### Algorithm-specific key variants

| Algorithm | Redis type | Actual key(s) |
|-----------|------------|---------------|
| `fixed_token` | **Hash** | Exact primary key above |
| `fixed_window` | **String** (counter) | `{primary}:{windowId}` where `windowId = floor(nowMs / (windowSeconds*1000))` |
| `sliding_window` | **Sorted set** | Exact primary key; members = request UUIDs, score = timestamp ms |

---

## Scripts

Each algorithm has a **consume** script (request path) and a **peek** script (`GET /api/v1/quota`).

| Algorithm | Consume | Peek |
|-----------|---------|------|
| `fixed_token` | [`lua/fixed_token.lua`](backend/rate-limiter/lua/fixed_token.lua) | [`lua/fixed_token_peek.lua`](backend/rate-limiter/lua/fixed_token_peek.lua) |
| `fixed_window` | [`lua/fixed_window.lua`](backend/rate-limiter/lua/fixed_window.lua) | [`lua/fixed_window_peek.lua`](backend/rate-limiter/lua/fixed_window_peek.lua) |
| `sliding_window` | [`lua/sliding_window.lua`](backend/rate-limiter/lua/sliding_window.lua) | [`lua/sliding_window_peek.lua`](backend/rate-limiter/lua/sliding_window_peek.lua) |

Go loads them once via `redis.NewScript` and runs with `EVALSHA` (auto-reload on `NOSCRIPT`).

### Common consume return shape

```
{ allowed, remaining, limit, reset_ms }
```

| Field | Type | Meaning |
|-------|------|---------|
| `allowed` | `0` \| `1` | Deny / allow |
| `remaining` | int | Units left after this decision |
| `limit` | int | Capacity (`burst` or window `limit`) |
| `reset_ms` | int | Milliseconds until useful refill / window end |

Mapped to response headers:

| Header | Value |
|--------|-------|
| `X-RateLimit-Limit` | `limit` |
| `X-RateLimit-Remaining` | `remaining` |
| `X-RateLimit-Reset` | `reset_ms` |
| `Retry-After` | `ceil(reset_ms/1000)` on deny |
| `X-Proxy-Instance` | `a` \| `b` (which replica handled the request) |

### Common peek return shape

```
{ remaining, limit, reset_ms, consumed }
```

Exposed by `GET /api/v1/quota` as JSON (`remaining`, `limit`, `resetMs`, `consumed`, `resetAt`, `redisKey`, …). Peek does **not** decrement quota (sliding peek may trim expired ZSET members only).

---

## 1. `fixed_token` (token bucket)

**Params (from Mongo `algorithm_configs.params`)**

| Key | Meaning |
|-----|---------|
| `sustainedRps` | Refill rate (tokens / second) |
| `burst` | Max tokens (bucket capacity) |

**Hash fields**

| Field | Type | Meaning |
|-------|------|---------|
| `tokens` | string (float) | Current token balance |
| `ts` | string (ms) | Last update timestamp |

**Consume logic (atomic)**

1. `HMGET tokens ts` (missing → start at `burst`)  
2. Refill: `tokens = min(burst, tokens + elapsedSec * sustainedRps)`  
3. If `tokens >= 1` → allow, decrement by 1; else deny  
4. `HSET` + `PEXPIRE` (TTL ≈ `2 * burst/rate`, minimum 60s)

**ARGV:** `sustainedRps`, `burst`, `now_ms`

---

## 2. `fixed_window`

**Params**

| Key | Meaning |
|-----|---------|
| `limit` | Max requests per window |
| `windowSeconds` | Window length |

**Key:** `rl:fixed_window:{namespaceId}:{subject}:{windowId}`

**Consume logic (atomic)**

1. Compute `windowId = floor(nowMs / windowMs)`  
2. `INCR` counter; on first hit set `PEXPIRE` to window length  
3. Allow if `count <= limit`

**ARGV:** `limit`, `windowSeconds`, `now_ms`

---

## 3. `sliding_window`

**Params**

| Key | Meaning |
|-----|---------|
| `limit` | Max requests in rolling window |
| `windowSeconds` | Rolling window length |

**Sorted set:** score = request time (ms), member = UUID from the proxy

**Consume logic (atomic)**

1. `ZREMRANGEBYSCORE` older than `now - window`  
2. `ZCARD` → if `count < limit`, `ZADD` new member and allow  
3. `PEXPIRE` ≈ window length  
4. On deny, `reset_ms` derived from oldest member leaving the window

**ARGV:** `limit`, `windowSeconds`, `now_ms`, `memberUUID`

---

## Fail mode

| Condition | Behaviour |
|-----------|-----------|
| Redis / Lua error on evaluate | **Fail-closed** → HTTP `429`, log error, header `X-RateLimit-Error: redis` |
| Redis error on peek | HTTP `502` with error body |
| Key missing on peek | Treated as full remaining (`consumed = 0`) |

---

## Multi-instance sharing

```
                    ┌─ rate-limiter-a ─┐
client → nginx LB ──┤                  ├──► Redis (same keys)
                    └─ rate-limiter-b ─┘
```

- Both replicas embed the same Lua scripts and key format.  
- Lua runs server-side on Redis → no double-spend under concurrent `EVAL` from A and B.  
- Vegeta and clients always hit `http://rate-limiter:8080` (LB), not a single replica.

Inspect which replica served a request: response header `X-Proxy-Instance: a|b`.

---

## Operator cheat sheet

```bash
# List quota keys
docker exec st-redis redis-cli KEYS 'rl:*'

# Inspect demo token bucket
docker exec st-redis redis-cli HGETALL \
  'rl:fixed_token:<namespaceId>:hdr:US-API-Key:ak_demo_1001_alpha'

# Peek via API (no consume)
curl -sS 'http://localhost:8080/api/v1/quota?namespace=demo-deviceList&apiKey=ak_demo_1001_alpha'
```

---

## Data plane routes that touch Redis

| Method | Path | Redis effect |
|--------|------|----------------|
| `*` (enforce) | any proxied path | **Consume** Lua (`EVAL`) |
| `GET` | `/api/v1/quota` | **Peek** Lua (no consume) |
| `GET` | `/healthz` | None |

---

## Related docs

- [`ratelimiter-schema.md`](ratelimiter-schema.md) — Mongo namespaces / rules / configs + data-plane API table  
- [`sequences.md`](sequences.md) — end-to-end sequence diagrams  
- [`architecture.md`](architecture.md) — planes and compose topology  
- [`README.md`](README.md) — ports + assignment coverage  
- [`backend/rate-limiter/limiter.go`](backend/rate-limiter/limiter.go) — Go Redis client + script wiring  
