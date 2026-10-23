import { useCallback, useEffect, useMemo, useState } from 'react'
import { PlaneTag } from '../components/PlaneTag'
import { ScenarioInfoModal } from '../components/ScenarioInfoModal'
import {
  fetchVegetaDefaults,
  fetchVegetaProfiles,
  fetchVegetaScenarios,
  getVegetaRun,
  listVegetaRuns,
  runVegetaScenario,
  startVegetaRun,
  stopVegetaRun,
  type VegetaScenario,
} from '../lib/api'
import { SERVICES } from '../lib/config'

type Profile = 'steady' | 'ramp' | 'burst'

type RunView = {
  id: string
  profile: string
  status: string
  metrics?: {
    requests: number
    successRatio: number
    latencyP50: string
    latencyP95: string
    throughput: number
    statusCodes?: Record<string, number>
  }
  error?: string
}

export function VegetaClient() {
  const [profile, setProfile] = useState<Profile>('steady')
  const [baseUrl, setBaseUrl] = useState<string>(SERVICES.mockServer.networkUrl)
  const [method, setMethod] = useState('GET')
  const [path, setPath] = useState('/1001/deviceList')
  const [apiKey, setApiKey] = useState('ak_demo_1001_alpha')

  const [steadyRps, setSteadyRps] = useState(10)
  const [steadyDur, setSteadyDur] = useState('15s')

  const [fromRps, setFromRps] = useState(2)
  const [toRps, setToRps] = useState(20)
  const [steps, setSteps] = useState(4)
  const [stepDur, setStepDur] = useState('3s')

  const [baseRps, setBaseRps] = useState(5)
  const [burstRps, setBurstRps] = useState(40)
  const [burstEvery, setBurstEvery] = useState('8s')
  const [burstDur, setBurstDur] = useState('2s')
  const [burstTotal, setBurstTotal] = useState('24s')

  const [active, setActive] = useState<RunView | null>(null)
  const [runs, setRuns] = useState<RunView[]>([])
  const [scenarios, setScenarios] = useState<VegetaScenario[]>([])
  const [infoScenario, setInfoScenario] = useState<VegetaScenario | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const loadMeta = useCallback(async () => {
    try {
      const [defs, sc] = await Promise.all([
        fetchVegetaDefaults(),
        fetchVegetaProfiles(),
        fetchVegetaScenarios(),
      ]).then(async ([defs, , sc]) => [defs, sc] as const)
      if (defs?.target?.baseUrl) setBaseUrl(defs.target.baseUrl)
      if (defs?.target?.path) setPath(defs.target.path)
      if (defs?.target?.headers?.['US-API-Key']) setApiKey(defs.target.headers['US-API-Key'])
      setScenarios(sc.scenarios ?? [])
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'vegeta-client unreachable')
    }
  }, [])

  const refreshRuns = useCallback(async () => {
    try {
      const data = await listVegetaRuns()
      setRuns((data.runs as RunView[]) ?? [])
    } catch {
      /* ignore while down */
    }
  }, [])

  useEffect(() => {
    void loadMeta()
    void refreshRuns()
  }, [loadMeta, refreshRuns])

  useEffect(() => {
    if (!active?.id) return
    if (active.status === 'completed' || active.status === 'stopped' || active.status === 'failed') {
      void refreshRuns()
      return
    }
    const id = setInterval(() => {
      void getVegetaRun(active.id)
        .then((r) => setActive(r as RunView))
        .catch(() => undefined)
    }, 1000)
    return () => clearInterval(id)
  }, [active?.id, active?.status, refreshRuns])

  const payload = useMemo(() => {
    const target = {
      baseUrl,
      method,
      path,
      headers: { 'US-API-Key': apiKey },
    }
    if (profile === 'steady') {
      return { profile, target, steady: { rps: steadyRps, duration: steadyDur } }
    }
    if (profile === 'ramp') {
      return {
        profile,
        target,
        ramp: { fromRps, toRps, steps, stepDuration: stepDur },
      }
    }
    return {
      profile,
      target,
      burst: {
        baseRps,
        burstRps,
        burstEvery,
        burstDuration: burstDur,
        duration: burstTotal,
      },
    }
  }, [
    profile, baseUrl, method, path, apiKey,
    steadyRps, steadyDur, fromRps, toRps, steps, stepDur,
    baseRps, burstRps, burstEvery, burstDur, burstTotal,
  ])

  async function onStart() {
    setBusy(true)
    setError(null)
    try {
      const run = (await startVegetaRun(payload)) as RunView
      setActive(run)
      await refreshRuns()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'start failed')
    } finally {
      setBusy(false)
    }
  }

  async function onStop() {
    if (!active?.id) return
    setBusy(true)
    try {
      const run = (await stopVegetaRun(active.id)) as RunView
      setActive(run)
      await refreshRuns()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'stop failed')
    } finally {
      setBusy(false)
    }
  }

  async function onScenario(id: string) {
    setBusy(true)
    setError(null)
    try {
      const res = await runVegetaScenario(id)
      setActive(res.run as RunView)
      await refreshRuns()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'scenario failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="page">
      <header className="page__header">
        <div>
          <div className="page__eyebrow">
            <PlaneTag plane="data" />
          </div>
          <h1>Vegeta Client</h1>
          <p className="muted">
            One-click scenarios for quick validation, or custom steady / ramp / burst against{' '}
            <code>st_net</code>.
          </p>
        </div>
      </header>

      {error ? <p className="error">{error}</p> : null}

      <section className="panel">
        <div className="panel__toolbar">
          <h2>Preconfigured scenarios</h2>
          <button type="button" className="btn btn--ghost" onClick={() => void loadMeta()}>
            Reload
          </button>
        </div>
        <p className="muted" style={{ marginBottom: '0.85rem' }}>
          Documented test packs — start immediate traffic without filling the form. Uses{' '}
          <code>RATE_LIMITER_URL</code> (mock-server today).
        </p>
        <div className="scenario-grid">
          {scenarios.length === 0 ? (
            <p className="muted">No scenarios loaded — is vegeta-client up?</p>
          ) : null}
          {scenarios.map((s) => (
            <article key={s.id} className="scenario-card">
              <div className="scenario-card__top">
                <strong>{s.name}</strong>
                <span className="docs__badge">{s.durationHint}</span>
              </div>
              <p className="muted">{s.description}</p>
              <div className="scenario-card__tags">
                {s.tags.map((t) => (
                  <span key={t} className="scenario-tag">
                    {t}
                  </span>
                ))}
              </div>
              <div className="scenario-card__foot">
                <code className="scenario-id">{s.id}</code>
                <div className="btn-row">
                  <button
                    type="button"
                    className="btn btn--ghost"
                    onClick={() => setInfoScenario(s)}
                  >
                    Info
                  </button>
                  <button
                    type="button"
                    className="btn"
                    disabled={busy}
                    onClick={() => void onScenario(s.id)}
                  >
                    Run
                  </button>
                </div>
              </div>
            </article>
          ))}
        </div>
      </section>

      {infoScenario ? (
        <ScenarioInfoModal
          scenario={infoScenario}
          busy={busy}
          onClose={() => setInfoScenario(null)}
          onRun={() => {
            const id = infoScenario.id
            setInfoScenario(null)
            void onScenario(id)
          }}
        />
      ) : null}

      <div className="split">
        <section className="panel">
          <h2>Run config</h2>

          <label className="field">
            <span>Profile</span>
            <select value={profile} onChange={(e) => setProfile(e.target.value as Profile)}>
              <option value="steady">Steady</option>
              <option value="ramp">Ramp up</option>
              <option value="burst">Burst</option>
            </select>
          </label>

          <label className="field">
            <span>Target base URL (compose DNS)</span>
            <input value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
          </label>

          <div className="field-row">
            <label className="field">
              <span>Method</span>
              <select value={method} onChange={(e) => setMethod(e.target.value)}>
                <option>GET</option>
                <option>POST</option>
                <option>PUT</option>
              </select>
            </label>
            <label className="field field--grow">
              <span>Path</span>
              <input value={path} onChange={(e) => setPath(e.target.value)} />
            </label>
          </div>

          <label className="field">
            <span>US-API-Key</span>
            <input value={apiKey} onChange={(e) => setApiKey(e.target.value)} />
          </label>

          {profile === 'steady' ? (
            <div className="field-row">
              <label className="field">
                <span>RPS</span>
                <input
                  type="number"
                  value={steadyRps}
                  onChange={(e) => setSteadyRps(Number(e.target.value))}
                />
              </label>
              <label className="field">
                <span>Duration</span>
                <input value={steadyDur} onChange={(e) => setSteadyDur(e.target.value)} />
              </label>
            </div>
          ) : null}

          {profile === 'ramp' ? (
            <>
              <div className="field-row">
                <label className="field">
                  <span>From RPS</span>
                  <input type="number" value={fromRps} onChange={(e) => setFromRps(Number(e.target.value))} />
                </label>
                <label className="field">
                  <span>To RPS</span>
                  <input type="number" value={toRps} onChange={(e) => setToRps(Number(e.target.value))} />
                </label>
              </div>
              <div className="field-row">
                <label className="field">
                  <span>Steps</span>
                  <input type="number" value={steps} onChange={(e) => setSteps(Number(e.target.value))} />
                </label>
                <label className="field">
                  <span>Step duration</span>
                  <input value={stepDur} onChange={(e) => setStepDur(e.target.value)} />
                </label>
              </div>
            </>
          ) : null}

          {profile === 'burst' ? (
            <>
              <div className="field-row">
                <label className="field">
                  <span>Base RPS</span>
                  <input type="number" value={baseRps} onChange={(e) => setBaseRps(Number(e.target.value))} />
                </label>
                <label className="field">
                  <span>Burst RPS</span>
                  <input type="number" value={burstRps} onChange={(e) => setBurstRps(Number(e.target.value))} />
                </label>
              </div>
              <div className="field-row">
                <label className="field">
                  <span>Every</span>
                  <input value={burstEvery} onChange={(e) => setBurstEvery(e.target.value)} />
                </label>
                <label className="field">
                  <span>Burst for</span>
                  <input value={burstDur} onChange={(e) => setBurstDur(e.target.value)} />
                </label>
                <label className="field">
                  <span>Total</span>
                  <input value={burstTotal} onChange={(e) => setBurstTotal(e.target.value)} />
                </label>
              </div>
            </>
          ) : null}

          <div className="btn-row">
            <button type="button" className="btn" disabled={busy} onClick={() => void onStart()}>
              Start run
            </button>
            <button
              type="button"
              className="btn btn--ghost"
              disabled={busy || !active}
              onClick={() => void onStop()}
            >
              Stop
            </button>
          </div>
        </section>

        <section className="panel">
          <h2>Active run</h2>
          {!active ? (
            <p className="muted">No active run. Start a profile to see live metrics.</p>
          ) : (
            <div className="metrics">
              <p>
                <code>{active.id.slice(0, 8)}</code> · <strong>{active.profile}</strong> ·{' '}
                <span className={`status status--${active.status}`}>{active.status}</span>
              </p>
              {active.error ? <p className="error">{active.error}</p> : null}
              {active.metrics ? (
                <dl className="meta-list meta-list--grid">
                  <div>
                    <dt>requests</dt>
                    <dd>{active.metrics.requests}</dd>
                  </div>
                  <div>
                    <dt>success</dt>
                    <dd>{(active.metrics.successRatio * 100).toFixed(1)}%</dd>
                  </div>
                  <div>
                    <dt>p50</dt>
                    <dd>{active.metrics.latencyP50}</dd>
                  </div>
                  <div>
                    <dt>p95</dt>
                    <dd>{active.metrics.latencyP95}</dd>
                  </div>
                  <div>
                    <dt>throughput</dt>
                    <dd>{active.metrics.throughput.toFixed(2)} rps</dd>
                  </div>
                </dl>
              ) : null}
            </div>
          )}

          <h3 className="subhead">Recent runs</h3>
          <ul className="run-list">
            {runs
              .slice()
              .reverse()
              .slice(0, 8)
              .map((r) => (
                <li key={r.id}>
                  <button type="button" className="linkish" onClick={() => setActive(r)}>
                    {r.profile} · {r.status} · {r.id.slice(0, 8)}
                  </button>
                </li>
              ))}
          </ul>
        </section>
      </div>
    </div>
  )
}
