import { SERVICES } from './config'

const base = () => SERVICES.controlPlane.hostUrl

export type Algorithm = {
  id: string
  label: string
  description?: string
  paramsSchema: Record<string, unknown>
  enabled: boolean
}

export type AlgorithmConfig = {
  id: string
  name: string
  algorithm: string
  params: Record<string, number>
  enabled: boolean
  createdAt?: string
  updatedAt?: string
}

export type Namespace = {
  id: string
  label: string
  upstreamHost: string
  path?: string | null
  algorithm: string
  configId: string
  matchMode: 'all' | 'any'
  priority: number
  enabled: boolean
  createdAt?: string
  updatedAt?: string
}

export type RuleMatch = {
  kind: 'header' | 'path' | 'path_attr' | 'method' | 'regex'
  name?: string
  op?: string
  value?: string
  values?: string[]
  pattern?: string
  attr?: string
  field?: string
}

export type Rule = {
  id: string
  namespaceId: string
  label?: string
  enabled: boolean
  match: RuleMatch
}

export class ControlPlaneError extends Error {
  code?: string
  status: number
  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'ControlPlaneError'
    this.status = status
    this.code = code
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${base()}${path}`, {
      ...init,
      headers: {
        Accept: 'application/json',
        ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
        ...init?.headers,
      },
    })
  } catch {
    throw new ControlPlaneError(
      'Cannot reach control-plane at http://localhost:8091 — is it running?',
      0,
      'unreachable',
    )
  }
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new ControlPlaneError(
      (data as { error?: string }).error ?? `request failed ${res.status}`,
      res.status,
      (data as { code?: string }).code,
    )
  }
  return data as T
}

export async function fetchAlgorithms() {
  return request<{ algorithms: Algorithm[] }>('/api/v1/algorithms')
}

export async function fetchConfigs() {
  return request<{ configs: AlgorithmConfig[] }>('/api/v1/configs')
}

export async function createConfig(body: {
  name: string
  algorithm: string
  params: Record<string, number>
  enabled?: boolean
}) {
  return request<AlgorithmConfig>('/api/v1/configs', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function updateConfig(
  id: string,
  body: Partial<{ name: string; algorithm: string; params: Record<string, number>; enabled: boolean }>,
) {
  return request<AlgorithmConfig>(`/api/v1/configs/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export async function deleteConfig(id: string) {
  return request<void>(`/api/v1/configs/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function fetchNamespaces() {
  return request<{ namespaces: Namespace[] }>('/api/v1/namespaces')
}

export async function createNamespace(body: {
  label: string
  upstreamHost: string
  path?: string | null
  configId: string
  matchMode: 'all' | 'any'
  priority?: number
  enabled?: boolean
}) {
  return request<Namespace>('/api/v1/namespaces', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function updateNamespace(
  id: string,
  body: Partial<{
    label: string
    upstreamHost: string
    path: string | null
    configId: string
    matchMode: 'all' | 'any'
    priority: number
    enabled: boolean
  }>,
) {
  return request<Namespace>(`/api/v1/namespaces/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  })
}

export async function deleteNamespace(id: string) {
  return request<void>(`/api/v1/namespaces/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function fetchRules(namespaceId: string) {
  return request<{ rules: Rule[] }>(
    `/api/v1/namespaces/${encodeURIComponent(namespaceId)}/rules`,
  )
}

export async function createRule(
  namespaceId: string,
  body: { label?: string; match: RuleMatch; enabled?: boolean },
) {
  return request<Rule>(`/api/v1/namespaces/${encodeURIComponent(namespaceId)}/rules`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function deleteRule(id: string) {
  return request<void>(`/api/v1/rules/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/** Build default params for an algorithm (UI helper, unit-tested). */
export function defaultParamsForAlgorithm(algorithm: string): Record<string, number> {
  switch (algorithm) {
    case 'fixed_token':
      return { sustainedRps: 10, burst: 20 }
    case 'fixed_window':
    case 'sliding_window':
      return { limit: 10, windowSeconds: 1 }
    default:
      return {}
  }
}

export type TestSuiteInfo = {
  id: string
  label: string
  description: string
}

export type HTTPExchange = {
  method: string
  path: string
  headers?: Record<string, string>
  body?: unknown
}

export type HTTPResponseCapture = {
  status: number
  body?: unknown
  headers?: Record<string, string>
}

export type TestStep = {
  name: string
  request?: HTTPExchange
  response?: HTTPResponseCapture
  ok: boolean
  detail?: string
}

export type TestCaseResult = {
  id: string
  name: string
  description: string
  suite: string
  status: 'passed' | 'failed' | 'skipped'
  durationMs: string
  message?: string
  assertions?: string[]
  request?: HTTPExchange
  response?: HTTPResponseCapture
  steps?: TestStep[]
  meta?: Record<string, unknown>
}

export type TestRunReport = {
  id: string
  suite: string
  startedAt: string
  endedAt: string
  duration: string
  passed: number
  failed: number
  skipped?: number
  total: number
  ok: boolean
  results: TestCaseResult[]
}

export type TestSuiteId = 'unit' | 'functional' | 'concurrency' | 'e2e' | 'all'

export async function fetchTestCatalog() {
  return request<{ suites: TestSuiteInfo[] }>('/api/v1/tests/catalog')
}

export async function runControlPlaneTests(suite: TestSuiteId | string = 'all') {
  return request<TestRunReport>(`/api/v1/tests/run?suite=${encodeURIComponent(suite)}`, {
    method: 'POST',
  })
}

export function formatMatch(m: RuleMatch): string {
  switch (m.kind) {
    case 'header':
      return `header ${m.name} ${m.op} ${m.value ?? (m.values ?? []).join(',')}`
    case 'path':
      return `path ${m.op} ${m.value}`
    case 'path_attr':
      return `path_attr ${m.attr}=${m.value}`
    case 'method':
      return `method ${m.op} ${m.value ?? (m.values ?? []).join(',')}`
    case 'regex':
      return `regex(${m.field}) ${m.pattern}`
    default:
      return m.kind
  }
}
