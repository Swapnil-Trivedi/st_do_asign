import { SERVICES } from './config'
import type { TestRunReport, TestSuiteInfo } from './controlPlaneApi'

const base = () => SERVICES.rateLimiter.hostUrl

export class DataPlaneError extends Error {
  code?: string
  status: number
  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'DataPlaneError'
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
    throw new DataPlaneError(
      `Cannot reach rate-limiter at ${base()} — is the LB / replicas running?`,
      0,
      'unreachable',
    )
  }
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    throw new DataPlaneError(
      (data as { error?: string }).error ?? `request failed ${res.status}`,
      res.status,
      (data as { code?: string }).code,
    )
  }
  return data as T
}

export type DataPlaneTestSuiteId = 'unit' | 'functional' | 'all'

export async function fetchDataPlaneTestCatalog() {
  return request<{ suites: TestSuiteInfo[] }>('/api/v1/tests/catalog')
}

export async function runDataPlaneTests(suite: DataPlaneTestSuiteId | string = 'all') {
  return request<TestRunReport>(`/api/v1/tests/run?suite=${encodeURIComponent(suite)}`, {
    method: 'POST',
  })
}
