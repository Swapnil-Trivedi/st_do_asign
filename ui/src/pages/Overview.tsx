import { useCallback, useEffect, useState } from 'react'
import { HealthDot } from '../components/HealthDot'
import { PlaneTag } from '../components/PlaneTag'
import { checkHealth, type HealthState } from '../lib/api'
import { SERVICES, TOPOLOGY_EDGES, type ServiceDef } from '../lib/config'

const SERVICE_LIST = Object.values(SERVICES) as ServiceDef[]

export function Overview() {
  const [health, setHealth] = useState<Record<string, HealthState>>({})

  const refresh = useCallback(async () => {
    const next: Record<string, HealthState> = {}
    await Promise.all(
      SERVICE_LIST.map(async (s) => {
        next[s.id] = 'checking'
        setHealth((h) => ({ ...h, [s.id]: 'checking' }))
        next[s.id] = await checkHealth(s.hostUrl, s.healthPath, 'planned' in s && !!s.planned)
      }),
    )
    setHealth(next)
  }, [])

  useEffect(() => {
    void refresh()
    const id = setInterval(() => void refresh(), 5000)
    return () => clearInterval(id)
  }, [refresh])

  return (
    <div className="page">
      <header className="page__header">
        <div>
          <h1>Overview</h1>
          <p className="muted">
            Docker Compose topology on <code>st_net</code>. Green = reachable health check; red = down;
            amber = planned.
          </p>
        </div>
        <button type="button" className="btn" onClick={() => void refresh()}>
          Refresh health
        </button>
      </header>

      <section className="panel">
        <h2>Containers</h2>
        <div className="svc-grid">
          {SERVICE_LIST.map((s) => (
            <article key={s.id} className="svc-card">
              <div className="svc-card__top">
                <strong>{s.name}</strong>
                <PlaneTag plane={s.plane} />
              </div>
              <p className="muted">{s.role}</p>
              <HealthDot state={health[s.id] ?? 'checking'} />
              <dl className="meta-list">
                <div>
                  <dt>container</dt>
                  <dd>
                    <code>{s.container}</code>
                  </dd>
                </div>
                <div>
                  <dt>host</dt>
                  <dd>
                    <code>{s.hostUrl}</code>
                  </dd>
                </div>
                <div>
                  <dt>st_net</dt>
                  <dd>
                    <code>{s.networkUrl}</code>
                  </dd>
                </div>
              </dl>
            </article>
          ))}
        </div>
      </section>

      <section className="panel">
        <h2>Connectivity</h2>
        <div className="topo">
          {TOPOLOGY_EDGES.map((e) => {
            const fromH = health[e.from] ?? 'checking'
            const toH = health[e.to] ?? 'checking'
            const ok =
              (fromH === 'healthy' || fromH === 'planned') &&
              (toH === 'healthy' || toH === 'planned')
            const edgeClass =
              fromH === 'planned' || toH === 'planned'
                ? 'topo__edge--planned'
                : ok && fromH === 'healthy' && toH === 'healthy'
                  ? 'topo__edge--up'
                  : 'topo__edge--down'
            return (
              <div key={`${e.from}-${e.to}-${e.label}`} className={`topo__edge ${edgeClass}`}>
                <span className="topo__node">
                  <HealthDot state={fromH} />
                  <code>{e.from}</code>
                </span>
                <span className="topo__arrow">
                  <span className="topo__label">{e.label}</span>
                  →
                </span>
                <span className="topo__node">
                  <HealthDot state={toH} />
                  <code>{e.to}</code>
                </span>
              </div>
            )
          })}
        </div>
      </section>
    </div>
  )
}
