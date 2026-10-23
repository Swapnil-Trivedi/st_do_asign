import { SERVICES } from './config'

export type HealthState = 'healthy' | 'down' | 'planned' | 'checking'

export async function checkHealth(
  hostUrl: string,
  healthPath: string,
  planned?: boolean,
): Promise<HealthState> {
  if (planned || !healthPath) return 'planned'
  try {
    const ctrl = new AbortController()
    const t = setTimeout(() => ctrl.abort(), 2500)
    const res = await fetch(`${hostUrl}${healthPath}`, { signal: ctrl.signal })
    clearTimeout(t)
    return res.ok ? 'healthy' : 'down'
  } catch {
    return 'down'
  }
}

export async function fetchMockLogs(limit = 150) {
  const res = await fetch(`${SERVICES.mockServer.hostUrl}/api/v1/logs?limit=${limit}`)
  if (!res.ok) throw new Error(`logs ${res.status}`)
  return res.json() as Promise<{ logs: { ts: string; level: string; message: string }[] }>
}

export async function fetchVegetaProfiles() {
  const res = await fetch(`${SERVICES.vegetaClient.hostUrl}/api/v1/profiles`)
  if (!res.ok) throw new Error(`profiles ${res.status}`)
  return res.json()
}

export async function fetchVegetaDefaults() {
  const res = await fetch(`${SERVICES.vegetaClient.hostUrl}/api/v1/defaults`)
  if (!res.ok) throw new Error(`defaults ${res.status}`)
  return res.json()
}

export async function startVegetaRun(body: unknown) {
  const res = await fetch(`${SERVICES.vegetaClient.hostUrl}/api/v1/runs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error ?? `start ${res.status}`)
  return data
}

export async function getVegetaRun(id: string) {
  const res = await fetch(`${SERVICES.vegetaClient.hostUrl}/api/v1/runs/${id}`)
  if (!res.ok) throw new Error(`run ${res.status}`)
  return res.json()
}

export async function stopVegetaRun(id: string) {
  const res = await fetch(`${SERVICES.vegetaClient.hostUrl}/api/v1/runs/${id}/stop`, {
    method: 'POST',
  })
  if (!res.ok) throw new Error(`stop ${res.status}`)
  return res.json()
}

export async function listVegetaRuns() {
  const res = await fetch(`${SERVICES.vegetaClient.hostUrl}/api/v1/runs/`)
  if (!res.ok) throw new Error(`runs ${res.status}`)
  return res.json() as Promise<{ runs: unknown[] }>
}

export type VegetaScenario = {
  id: string
  name: string
  description: string
  intent: string
  tags: string[]
  durationHint: string
  request: unknown
}

export async function fetchVegetaScenarios() {
  const res = await fetch(`${SERVICES.vegetaClient.hostUrl}/api/v1/scenarios`)
  if (!res.ok) throw new Error(`scenarios ${res.status}`)
  return res.json() as Promise<{ scenarios: VegetaScenario[]; note?: string }>
}

export async function runVegetaScenario(id: string) {
  const res = await fetch(`${SERVICES.vegetaClient.hostUrl}/api/v1/scenarios/${id}/run`, {
    method: 'POST',
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error ?? `scenario run ${res.status}`)
  return data as { scenarioId: string; scenario: string; run: unknown }
}
