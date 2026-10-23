import { SERVICES } from './config'

const base = () => SERVICES.consoleApi.hostUrl

export type E2EGroupInfo = { id: string; label: string; description: string }
export type E2ESetupInfo = {
  id: string
  group: string
  name: string
  description: string
  driver: string
}

export type TrafficStats = {
  total: number
  allowed: number
  rateLimited: number
  other: number
  statusCodes: Record<string, number>
}

export type VegetaRunSlice = {
  runId: string
  profile: string
  status: string
  metrics?: {
    requests: number
    successRatio: number
    latencyP95?: string
    latencyMean?: string
    throughput?: number
    statusCodes?: Record<string, number>
  }
}

export type QuotaSnapshot = {
  namespace: string
  subject: string
  limit: number
  remaining: number
  consumed: number
  resetMs: number
  algorithm: string
}

export type ServiceAnalytics = {
  proxyInstances: Record<string, number>
  rateLimitHeaders: {
    lastAllow?: Record<string, string>
    lastDeny?: Record<string, string>
  }
  upstreamForwards: number
  upstreamLatencyMs?: number[]
  mockLogDelta: {
    access: number
    upstream: number
    warn: number
    info: number
    total: number
  }
  keyMatchTrue: number
  concurrentVegetaJobs?: number
}

export type E2EStep = { name: string; ok: boolean; detail?: string }

export type E2ESetupResult = {
  setupId: string
  group: string
  name: string
  description: string
  status: string
  message?: string
  driver: string
  durationMs: number
  traffic: TrafficStats
  vegetaRuns?: VegetaRunSlice[]
  quota?: QuotaSnapshot
  analytics: ServiceAnalytics
  steps?: E2EStep[]
}

export type E2EReport = {
  id: string
  startedAt: string
  endedAt: string
  duration: string
  ok: boolean
  setups: E2ESetupResult[]
  summary: {
    totalSetups: number
    passed: number
    failed: number
    skipped: number
    allowed: number
    rateLimited: number
    other: number
    byGroupPassed: Record<string, number>
    vegetaJobs: number
  }
}

/** Local mirror of console-api catalog so the UI can list cases if the API is stale/down. */
export const FALLBACK_E2E_CATALOG: { groups: E2EGroupInfo[]; setups: E2ESetupInfo[] } = {
  groups: [
    { id: 'fixtures', label: 'Fixtures', description: 'Direct HTTP algorithm / hot-reload checks' },
    { id: 'vegeta', label: 'Vegeta', description: 'Single Vegeta profile runs through the LB' },
    {
      id: 'concurrency',
      label: 'Concurrency',
      description: 'Multi-Vegeta + hybrid load on shared Redis',
    },
  ],
  setups: [
    {
      id: 'fixed-token-burst',
      group: 'fixtures',
      name: 'Fixed token burst',
      description: '40 concurrent against burst=20 → ~20 allow / ~20×429',
      driver: 'direct',
    },
    {
      id: 'fixed-window-tight',
      group: 'fixtures',
      name: 'Fixed window tight',
      description: '12 sequential against limit=5/10s → 5 allow / 7 deny',
      driver: 'direct',
    },
    {
      id: 'sliding-window-roll',
      group: 'fixtures',
      name: 'Sliding window',
      description: '15 concurrent against sliding limit=8 → ≤8 allow',
      driver: 'direct',
    },
    {
      id: 'demo-seed-smoke',
      group: 'fixtures',
      name: 'Demo seed smoke',
      description: '8 sequential under seed demo-deviceList — all should allow',
      driver: 'direct',
    },
    {
      id: 'multi-instance-mix',
      group: 'fixtures',
      name: 'Multi-instance mix',
      description: '60 concurrent; both proxy replicas a and b should appear',
      driver: 'direct',
    },
    {
      id: 'hot-reload-disable',
      group: 'fixtures',
      name: 'Hot reload disable',
      description: 'Allow → disable namespace → poll → no match → re-enable',
      driver: 'hot-reload',
    },
    {
      id: 'vegeta-steady-under',
      group: 'vegeta',
      name: 'Vegeta steady under limit',
      description: 'Steady 5 RPS / 6s on shared burst=20 — few/no 429s',
      driver: 'vegeta',
    },
    {
      id: 'vegeta-burst-over',
      group: 'vegeta',
      name: 'Vegeta burst over limit',
      description: 'Burst spike 40 RPS — expect 429s',
      driver: 'vegeta',
    },
    {
      id: 'vegeta-ramp-smoke',
      group: 'vegeta',
      name: 'Vegeta ramp smoke',
      description: 'Ramp 2→12 over 3 steps — completes with metrics',
      driver: 'vegeta',
    },
    {
      id: 'concurrency-dual-vegeta-burst',
      group: 'concurrency',
      name: 'Dual Vegeta burst',
      description: '2 parallel Vegeta bursts — combined allows ≤ shared burst',
      driver: 'multi-vegeta',
    },
    {
      id: 'concurrency-triple-steady',
      group: 'concurrency',
      name: 'Triple Vegeta steady',
      description: '3 parallel steady 3 RPS × 5s — aggregate allows bounded',
      driver: 'multi-vegeta',
    },
    {
      id: 'concurrency-direct-plus-vegeta',
      group: 'concurrency',
      name: 'Direct + Vegeta hybrid',
      description: '20 concurrent direct + 1 Vegeta burst on shared Redis',
      driver: 'hybrid',
    },
    {
      id: 'concurrency-multi-instance-vegeta',
      group: 'concurrency',
      name: 'Multi-instance + dual Vegeta',
      description: '2 Vegeta bursts + probes for X-Proxy-Instance a/b',
      driver: 'multi-vegeta',
    },
  ],
}

export class E2EApiError extends Error {
  status?: number
  constructor(message: string, status?: number) {
    super(message)
    this.name = 'E2EApiError'
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${base()}${path}`, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const hint =
      res.status === 404
        ? ' — rebuild console-api: TARGET=console-api ./build/build.sh image && docker compose -f build/docker-compose.yml up -d --force-recreate console-api'
        : ''
    throw new E2EApiError(
      ((data as { error?: string }).error ?? `request failed ${res.status}`) + hint,
      res.status,
    )
  }
  return data as T
}

export async function fetchE2ECatalog() {
  return request<{ groups: E2EGroupInfo[]; setups: E2ESetupInfo[] }>('/api/v1/e2e/catalog')
}

export async function runE2E(body: { setups?: string[]; groups?: string[] }) {
  return request<E2EReport>('/api/v1/e2e/run', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function listE2ERuns() {
  return request<{ runs: E2EReport[] }>('/api/v1/e2e/runs')
}
