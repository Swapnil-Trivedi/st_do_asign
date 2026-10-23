import { SERVICES } from './config'

export type ConsoleJob = {
  id: string
  action: string
  target: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  output: string
  error?: string
  startedAt: string
  endedAt?: string
}

const base = () => SERVICES.consoleApi.hostUrl

async function postJob(path: string, body?: unknown): Promise<ConsoleJob> {
  const res = await fetch(`${base()}${path}`, {
    method: 'POST',
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  })
  const data = await res.json()
  if (!res.ok) throw new Error(data.error ?? `request failed ${res.status}`)
  return data as ConsoleJob
}

export async function pipelineUp(rebuild = true) {
  return postJob('/api/v1/pipeline/up', { rebuild })
}

export async function pipelineDown() {
  return postJob('/api/v1/pipeline/down')
}

export async function pipelineBuild() {
  return postJob('/api/v1/pipeline/build')
}

export async function serviceUp(name: string, rebuild = false) {
  return postJob(`/api/v1/services/${encodeURIComponent(name)}/up`, { rebuild })
}

export async function serviceDown(name: string) {
  return postJob(`/api/v1/services/${encodeURIComponent(name)}/down`)
}

export async function serviceBuild(name: string) {
  return postJob(`/api/v1/services/${encodeURIComponent(name)}/build`)
}

export async function getConsoleJob(id: string) {
  const res = await fetch(`${base()}/api/v1/jobs/${encodeURIComponent(id)}`)
  if (!res.ok) throw new Error(`job ${res.status}`)
  return res.json() as Promise<ConsoleJob>
}

export async function fetchPipelineStatus() {
  const res = await fetch(`${base()}/api/v1/pipeline/status`)
  if (!res.ok) throw new Error(`status ${res.status}`)
  return res.json() as Promise<{
    ok: boolean
    services?: { name: string; state: string; status: string }[]
    error?: string
  }>
}

export async function fetchInfraHealth() {
  const res = await fetch(`${base()}/api/v1/infra/health`)
  if (!res.ok) throw new Error(`infra health ${res.status}`)
  return res.json() as Promise<{ services: Record<string, HealthState> }>
}

type HealthState = 'healthy' | 'down' | 'planned' | 'checking'
