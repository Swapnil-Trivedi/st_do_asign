import { SERVICES } from './config'

export type TrafficEvent = {
  id: number
  ts: string
  instance: string
  method: string
  path: string
  decision: 'allow' | 'deny' | 'no_match' | 'open' | 'error' | string
  namespace?: string
  algorithm?: string
  subject?: string
  status: number
  limit?: number
  remaining?: number
  resetMs?: number
  latencyMs: number
  requestId?: string
}

export type TrafficCounters = {
  allowed: number
  denied: number
  noMatch: number
  open: number
  errors: number
  total: number
}

export type TrafficSnapshot = {
  instance: string
  observedAt: string
  policy: { loaded: boolean; namespaces: number }
  counters: TrafficCounters
  byNamespace: Record<string, { allowed: number; denied: number }>
  events: TrafficEvent[]
}

export type QuotaPeek = {
  namespaceId?: string
  namespace: string
  algorithm: string
  subject: string
  redisKey?: string
  limit: number
  remaining: number
  consumed: number
  resetMs: number
  resetAt?: string
  observedAt?: string
  params?: Record<string, unknown>
  error?: string
}

const EMPTY_COUNTERS: TrafficCounters = {
  allowed: 0,
  denied: 0,
  noMatch: 0,
  open: 0,
  errors: 0,
  total: 0,
}

async function fetchJSON<T>(url: string, init?: RequestInit, timeoutMs = 2500): Promise<T | null> {
  const ctrl = new AbortController()
  const timer = window.setTimeout(() => ctrl.abort(), timeoutMs)
  try {
    const res = await fetch(url, { ...init, signal: ctrl.signal })
    if (!res.ok) return null
    return (await res.json()) as T
  } catch {
    return null
  } finally {
    window.clearTimeout(timer)
  }
}

async function fetchTrafficFrom(base: string, limit = 80): Promise<TrafficSnapshot | null> {
  return fetchJSON<TrafficSnapshot>(`${base}/api/v1/traffic?limit=${limit}`)
}

export async function fetchMergedTraffic(limit = 100): Promise<{
  snapshots: { id: string; url: string; data: TrafficSnapshot | null }[]
  counters: TrafficCounters
  byNamespace: Record<string, { allowed: number; denied: number }>
  events: TrafficEvent[]
  policyNamespaces: number
  policyLoaded: boolean
}> {
  const targets = [
    { id: 'a', url: SERVICES.rateLimiterA.hostUrl },
    { id: 'b', url: SERVICES.rateLimiterB.hostUrl },
    // LB sample — useful if a direct replica port is down; tagged so we don't double-count.
    { id: 'lb', url: SERVICES.rateLimiter.hostUrl },
  ]

  const snapshots = await Promise.all(
    targets.map(async (t) => ({
      id: t.id,
      url: t.url,
      data: await fetchTrafficFrom(t.url, limit),
    })),
  )

  const counters: TrafficCounters = { ...EMPTY_COUNTERS }
  const byNamespace: Record<string, { allowed: number; denied: number }> = {}
  const events: TrafficEvent[] = []
  let policyNamespaces = 0
  let policyLoaded = false

  // Prefer replica A+B for counters (authoritative). Fall back to LB if both down.
  const replicaSnaps = snapshots.filter((s) => s.id === 'a' || s.id === 'b')
  const anyReplica = replicaSnaps.some((s) => s.data != null)
  const counterSources = anyReplica ? replicaSnaps : snapshots.filter((s) => s.id === 'lb')

  for (const s of counterSources) {
    if (!s.data) continue
    const c = s.data.counters
    counters.allowed += c.allowed ?? 0
    counters.denied += c.denied ?? 0
    counters.noMatch += c.noMatch ?? 0
    counters.open += c.open ?? 0
    counters.errors += c.errors ?? 0
    counters.total += c.total ?? 0
    for (const [ns, v] of Object.entries(s.data.byNamespace ?? {})) {
      const cur = byNamespace[ns] ?? { allowed: 0, denied: 0 }
      cur.allowed += v.allowed
      cur.denied += v.denied
      byNamespace[ns] = cur
    }
  }

  for (const s of snapshots) {
    if (!s.data) continue
    // Deduplicate events: skip LB events when we already have replica streams.
    if (s.id === 'lb' && anyReplica) {
      if (s.data.policy?.loaded) policyLoaded = true
      policyNamespaces = Math.max(policyNamespaces, s.data.policy?.namespaces ?? 0)
      continue
    }
    events.push(...(s.data.events ?? []))
    if (s.data.policy?.loaded) policyLoaded = true
    policyNamespaces = Math.max(policyNamespaces, s.data.policy?.namespaces ?? 0)
  }

  events.sort((a, b) => new Date(b.ts).getTime() - new Date(a.ts).getTime())

  return {
    snapshots: snapshots.filter((s) => s.id === 'a' || s.id === 'b'),
    counters,
    byNamespace,
    events: events.slice(0, limit),
    policyNamespaces,
    policyLoaded,
  }
}

export async function resetTrafficCounters(): Promise<void> {
  await Promise.allSettled([
    fetchJSON(`${SERVICES.rateLimiterA.hostUrl}/api/v1/traffic/reset`, { method: 'POST' }),
    fetchJSON(`${SERVICES.rateLimiterB.hostUrl}/api/v1/traffic/reset`, { method: 'POST' }),
  ])
}

export async function peekQuota(namespace: string, apiKey: string): Promise<QuotaPeek> {
  const q = new URLSearchParams({ namespace, apiKey })
  const data = await fetchJSON<QuotaPeek & { error?: string }>(
    `${SERVICES.rateLimiter.hostUrl}/api/v1/quota?${q}`,
  )
  if (!data || data.error) {
    return {
      namespace,
      algorithm: '',
      subject: '',
      limit: 0,
      remaining: 0,
      consumed: 0,
      resetMs: 0,
      error: data?.error ?? 'quota unreachable',
    }
  }
  return data
}

/** Clear Redis quota for the API key across all loaded namespaces (LB → any replica). */
export async function clearQuotaGauges(apiKey: string): Promise<{ deleted: number }> {
  const q = new URLSearchParams({ apiKey })
  const ctrl = new AbortController()
  const timer = window.setTimeout(() => ctrl.abort(), 5000)
  try {
    const res = await fetch(`${SERVICES.rateLimiter.hostUrl}/api/v1/quota/reset?${q}`, {
      method: 'POST',
      signal: ctrl.signal,
    })
    const data = (await res.json().catch(() => ({}))) as { deleted?: number; error?: string }
    if (!res.ok) {
      throw new Error(data.error ?? `quota reset ${res.status}`)
    }
    return { deleted: data.deleted ?? 0 }
  } finally {
    window.clearTimeout(timer)
  }
}

/** Build RPS buckets (allow/deny) for the last `windowSec` seconds from events. */
export function buildRateSeries(
  events: TrafficEvent[],
  windowSec = 30,
): { t: number; allow: number; deny: number }[] {
  const now = Date.now()
  const buckets = Array.from({ length: windowSec }, (_, i) => ({
    t: windowSec - 1 - i,
    allow: 0,
    deny: 0,
  }))
  for (const e of events) {
    const ageSec = Math.floor((now - new Date(e.ts).getTime()) / 1000)
    if (ageSec < 0 || ageSec >= windowSec) continue
    const idx = windowSec - 1 - ageSec
    if (e.decision === 'allow') buckets[idx].allow++
    else if (e.decision === 'deny') buckets[idx].deny++
  }
  return buckets
}
