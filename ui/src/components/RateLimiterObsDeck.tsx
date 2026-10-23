import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Namespace } from '../lib/controlPlaneApi'
import {
  buildRateSeries,
  clearQuotaGauges,
  fetchMergedTraffic,
  peekQuota,
  resetTrafficCounters,
  type QuotaPeek,
  type TrafficCounters,
  type TrafficEvent,
} from '../lib/proxyObsApi'
import { SERVICES } from '../lib/config'

const DEMO_API_KEY = 'ak_demo_1001_alpha'

function Sparkline({
  series,
}: {
  series: { t: number; allow: number; deny: number }[]
}) {
  const w = 560
  const h = 120
  const max = Math.max(1, ...series.map((s) => s.allow + s.deny))
  const step = w / Math.max(1, series.length - 1)
  const pad = 6

  const allowPts = series
    .map((s, i) => `${i * step},${h - (s.allow / max) * (h - pad * 2) - pad}`)
    .join(' ')
  const denyPts = series
    .map((s, i) => `${i * step},${h - (s.deny / max) * (h - pad * 2) - pad}`)
    .join(' ')

  return (
    <svg className="obs-spark" viewBox={`0 0 ${w} ${h}`} role="img" aria-label="allow/deny RPS">
      <polyline fill="none" stroke="var(--healthy)" strokeWidth="2.5" points={allowPts} />
      <polyline fill="none" stroke="var(--down)" strokeWidth="2.5" points={denyPts} />
    </svg>
  )
}

function DecisionBadge({ decision }: { decision: string }) {
  return <span className={`obs-decision obs-decision--${decision}`}>{decision}</span>
}

function StatCard({
  label,
  value,
  tone,
}: {
  label: string
  value: number | string
  tone?: 'ok' | 'bad' | 'muted'
}) {
  return (
    <div className={`obs-stat${tone ? ` obs-stat--${tone}` : ''}`}>
      <div className="obs-stat__label">{label}</div>
      <div className="obs-stat__value">{value}</div>
    </div>
  )
}

export function RateLimiterObsDeck({
  namespaces,
  active = true,
}: {
  namespaces: Namespace[]
  active?: boolean
}) {
  const [counters, setCounters] = useState<TrafficCounters>({
    allowed: 0,
    denied: 0,
    noMatch: 0,
    open: 0,
    errors: 0,
    total: 0,
  })
  const [byNamespace, setByNamespace] = useState<
    Record<string, { allowed: number; denied: number }>
  >({})
  const [events, setEvents] = useState<TrafficEvent[]>([])
  const [quotas, setQuotas] = useState<QuotaPeek[]>([])
  const [policyLoaded, setPolicyLoaded] = useState(false)
  const [policyNs, setPolicyNs] = useState(0)
  const [instances, setInstances] = useState<{ id: string; ok: boolean; url: string }[]>([])
  const [error, setError] = useState<string | null>(null)
  const [paused, setPaused] = useState(false)
  const [apiKey, setApiKey] = useState(DEMO_API_KEY)
  const [lastTick, setLastTick] = useState<string | null>(null)
  const [clearingQuota, setClearingQuota] = useState(false)
  const prevTotal = useRef(0)
  const inFlight = useRef(false)
  const namespacesRef = useRef(namespaces)
  const apiKeyRef = useRef(apiKey)
  const [rps, setRps] = useState(0)

  namespacesRef.current = namespaces
  apiKeyRef.current = apiKey

  const tick = useCallback(async () => {
    if (inFlight.current) return
    inFlight.current = true
    try {
      const nsList = namespacesRef.current
      const key = apiKeyRef.current.trim()

      const merged = await fetchMergedTraffic(120)
      setCounters(merged.counters)
      setByNamespace(merged.byNamespace)
      setEvents(merged.events)
      setPolicyLoaded(merged.policyLoaded)
      setPolicyNs(merged.policyNamespaces)
      setInstances(
        merged.snapshots.map((s) => ({
          id: s.id,
          url: s.url,
          ok: s.data != null,
        })),
      )
      const delta = merged.counters.total - prevTotal.current
      if (prevTotal.current > 0 && delta >= 0) setRps(delta)
      prevTotal.current = merged.counters.total
      setLastTick(new Date().toLocaleTimeString())

      const live = merged.snapshots.some((s) => s.data != null)
      setError(live ? null : 'proxies unreachable — is rate-limiter-a/b up?')

      const enabled = nsList.filter((n) => n.enabled).slice(0, 6)
      if (enabled.length > 0 && key) {
        const peeks = await Promise.all(enabled.map((n) => peekQuota(n.label, key)))
        setQuotas(peeks)
      } else {
        setQuotas([])
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'obs poll failed')
      setLastTick(new Date().toLocaleTimeString())
    } finally {
      inFlight.current = false
    }
  }, [])

  useEffect(() => {
    if (!active || paused) return
    let cancelled = false
    const run = () => {
      if (!cancelled) void tick()
    }
    run()
    const id = window.setInterval(run, 1000)
    const onVis = () => {
      if (document.visibilityState === 'visible') run()
    }
    document.addEventListener('visibilitychange', onVis)
    return () => {
      cancelled = true
      window.clearInterval(id)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [tick, paused, active])

  const series = useMemo(() => buildRateSeries(events, 30), [events])
  const denyRatio =
    counters.allowed + counters.denied > 0
      ? Math.round((counters.denied / (counters.allowed + counters.denied)) * 100)
      : 0

  async function onReset() {
    await resetTrafficCounters()
    prevTotal.current = 0
    setRps(0)
    void tick()
  }

  async function onClearGauges() {
    if (!apiKey.trim()) return
    setClearingQuota(true)
    setError(null)
    try {
      await clearQuotaGauges(apiKey.trim())
      await tick()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'quota clear failed')
    } finally {
      setClearingQuota(false)
    }
  }

  return (
    <div className="obs-deck">
      <section className="panel obs-deck__hero">
        <div className="panel__toolbar">
          <div>
            <h2>Live quota &amp; validation</h2>
            <p className="muted" style={{ margin: '0.25rem 0 0' }}>
              Primary obs deck — decisions from both proxy replicas, Redis quota peeks via LB{' '}
              <code>{SERVICES.rateLimiter.hostUrl}</code>
              {lastTick ? <> · last tick {lastTick}</> : null}
            </p>
          </div>
          <div className="btn-row">
            <button type="button" className="btn btn--ghost btn--tiny" onClick={() => setPaused((p) => !p)}>
              {paused ? 'Resume' : 'Pause'}
            </button>
            <button type="button" className="btn btn--ghost btn--tiny" onClick={() => void tick()}>
              Refresh
            </button>
            <button type="button" className="btn btn--ghost btn--tiny" onClick={() => void onReset()}>
              Reset counters
            </button>
          </div>
        </div>

        {error ? <p className="error">{error}</p> : null}

        <div className="obs-stats">
          <StatCard label="Allow" value={counters.allowed} tone="ok" />
          <StatCard label="Deny (429)" value={counters.denied} tone="bad" />
          <StatCard label="No match" value={counters.noMatch} tone="muted" />
          <StatCard label="Errors" value={counters.errors} tone={counters.errors ? 'bad' : 'muted'} />
          <StatCard label="~req/s" value={rps} />
          <StatCard label="Deny %" value={`${denyRatio}%`} tone={denyRatio > 40 ? 'bad' : 'muted'} />
        </div>

        <div className="obs-meta-row">
          <div className="obs-meta">
            <span className="obs-meta__k">Policy</span>
            <span className={policyLoaded ? 'obs-pill obs-pill--ok' : 'obs-pill obs-pill--bad'}>
              {policyLoaded ? `loaded · ${policyNs} ns` : 'not loaded'}
            </span>
          </div>
          {instances.map((inst) => (
            <div key={inst.id} className="obs-meta">
              <span className="obs-meta__k">replica-{inst.id}</span>
              <span className={inst.ok ? 'obs-pill obs-pill--ok' : 'obs-pill obs-pill--bad'}>
                {inst.ok ? 'up' : 'down'}
              </span>
            </div>
          ))}
          <div className="obs-spark-wrap">
            <span className="muted obs-spark-label">30s allow / deny</span>
            <Sparkline series={series} />
          </div>
        </div>
      </section>

      <div className="obs-deck__grid">
        <section className="panel">
          <div className="panel__toolbar">
            <h2>Quota gauges</h2>
            <div className="btn-row">
              <label className="obs-key-field">
                <span>API key</span>
                <input value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
              </label>
              <button
                type="button"
                className="btn btn--ghost btn--tiny"
                disabled={clearingQuota || !apiKey.trim() || namespaces.length === 0}
                onClick={() => void onClearGauges()}
                title="Delete Redis quota keys for this API key across all namespaces"
              >
                {clearingQuota ? 'Clearing…' : 'Clear gauges'}
              </button>
            </div>
          </div>
          {namespaces.length === 0 ? (
            <p className="muted">No namespaces yet — create one to peek Redis quota.</p>
          ) : quotas.length === 0 ? (
            <p className="muted">Waiting for quota peeks…</p>
          ) : (
            <div className="obs-quota-grid">
              {quotas.map((q) => {
                const pct =
                  q.limit > 0 ? Math.max(0, Math.min(100, Math.round((q.remaining / q.limit) * 100))) : 0
                return (
                  <article key={q.namespace} className="obs-quota">
                    <div className="obs-quota__top">
                      <strong>{q.namespace}</strong>
                      {q.algorithm ? <code>{q.algorithm}</code> : null}
                    </div>
                    {q.error ? (
                      <p className="error" style={{ margin: '0.35rem 0 0' }}>
                        {q.error}
                      </p>
                    ) : (
                      <>
                        <div className="obs-quota__nums">
                          <span>
                            {q.remaining}
                            <small> remaining</small>
                          </span>
                          <span className="muted">
                            {q.consumed}/{q.limit}
                          </span>
                        </div>
                        <div className="obs-bar" aria-hidden>
                          <div
                            className={`obs-bar__fill${pct < 20 ? ' obs-bar__fill--low' : ''}`}
                            style={{ width: `${pct}%` }}
                          />
                        </div>
                        <div className="muted obs-quota__sub">
                          reset {q.resetMs}ms
                          {q.subject ? (
                            <>
                              {' '}
                              · <code>{q.subject}</code>
                            </>
                          ) : null}
                        </div>
                      </>
                    )}
                  </article>
                )
              })}
            </div>
          )}
        </section>

        <section className="panel">
          <h2>Per-namespace validation</h2>
          {Object.keys(byNamespace).length === 0 ? (
            <p className="muted">No matched traffic yet — send load via Vegeta or curl the proxy.</p>
          ) : (
            <div className="table-wrap">
              <table className="data-table">
                <thead>
                  <tr>
                    <th>Namespace</th>
                    <th>Allow</th>
                    <th>Deny</th>
                    <th>Deny %</th>
                  </tr>
                </thead>
                <tbody>
                  {Object.entries(byNamespace)
                    .sort((a, b) => b[1].allowed + b[1].denied - (a[1].allowed + a[1].denied))
                    .map(([ns, v]) => {
                      const t = v.allowed + v.denied
                      const pct = t ? Math.round((v.denied / t) * 100) : 0
                      return (
                        <tr key={ns}>
                          <td>
                            <code>{ns}</code>
                          </td>
                          <td>{v.allowed}</td>
                          <td>{v.denied}</td>
                          <td>{pct}%</td>
                        </tr>
                      )
                    })}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </div>

      <section className="panel">
        <div className="panel__toolbar">
          <h2>Decision stream</h2>
          <span className="muted">{events.length} recent</span>
        </div>
        {events.length === 0 ? (
          <p className="muted">
            Idle — try{' '}
            <code>
              curl -H &apos;US-API-Key: {DEMO_API_KEY}&apos; {SERVICES.rateLimiter.hostUrl}/1001/deviceList
            </code>
          </p>
        ) : (
          <div className="obs-stream">
            {events.map((e) => (
              <div key={`${e.instance}-${e.id}-${e.ts}`} className="obs-stream__row">
                <span className="obs-stream__ts">
                  {new Date(e.ts).toLocaleTimeString()}
                </span>
                <DecisionBadge decision={e.decision} />
                <span className="obs-stream__inst">{e.instance}</span>
                <code className="obs-stream__path">
                  {e.method} {e.path}
                </code>
                <span className="obs-stream__ns">{e.namespace ?? '—'}</span>
                <span className="obs-stream__quota">
                  {e.decision === 'allow' || e.decision === 'deny'
                    ? `${e.remaining ?? '—'}/${e.limit ?? '—'}`
                    : e.status}
                </span>
                <span className="obs-stream__lat">{e.latencyMs}ms</span>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}
