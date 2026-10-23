import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { PlaneTag } from '../components/PlaneTag'
import { RunHistory, type RunView } from '../components/RunHistory'
import { ScenarioInfoModal } from '../components/ScenarioInfoModal'
import {
  clearVegetaRuns,
  deleteVegetaRun,
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

const HISTORY_KEY = 'vegeta-run-history'

function persistRuns(runs: RunView[]) {
  try {
    sessionStorage.setItem(HISTORY_KEY, JSON.stringify(runs.slice(0, 40)))
  } catch {
    /* quota */
  }
}

function loadCachedHistory(): RunView[] {
  try {
    const raw = sessionStorage.getItem(HISTORY_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as RunView[]
    return Array.isArray(parsed) ? parsed : []
  } catch {
    return []
  }
}

function upsertRun(list: RunView[], run: RunView): RunView[] {
  const next = list.filter((r) => r.id !== run.id)
  next.push(run)
  return next.sort((a, b) => {
    const at = a.startedAt ?? a.endedAt ?? ''
    const bt = b.startedAt ?? b.endedAt ?? ''
    return bt.localeCompare(at)
  })
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
  const [runs, setRuns] = useState<RunView[]>(() => loadCachedHistory())
  const [openRunId, setOpenRunId] = useState<string | null>(null)
  const [scenarios, setScenarios] = useState<VegetaScenario[]>([])
  const [infoScenario, setInfoScenario] = useState<VegetaScenario | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const defaultsApplied = useRef(false)

  const loadMeta = useCallback(async () => {
    try {
      const [defs, sc] = await Promise.all([
        fetchVegetaDefaults(),
        fetchVegetaProfiles(),
        fetchVegetaScenarios(),
      ]).then(async ([defs, , sc]) => [defs, sc] as const)
      // Apply server defaults only once so background meta refresh does not clobber form edits.
      if (!defaultsApplied.current) {
        if (defs?.target?.baseUrl) setBaseUrl(defs.target.baseUrl)
        if (defs?.target?.path) setPath(defs.target.path)
        if (defs?.target?.headers?.['US-API-Key']) setApiKey(defs.target.headers['US-API-Key'])
        defaultsApplied.current = true
      }
      setScenarios(sc.scenarios ?? [])
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'vegeta-client unreachable')
    }
  }, [])

  const refreshRuns = useCallback(async () => {
    try {
      const data = await listVegetaRuns()
      const remote = ((data.runs as RunView[]) ?? []).slice().sort((a, b) => {
        const at = a.startedAt ?? a.endedAt ?? ''
        const bt = b.startedAt ?? b.endedAt ?? ''
        return bt.localeCompare(at)
      })
      setRuns(remote)
      persistRuns(remote)
      setActive((prev) => {
        if (!prev) return prev
        const fresh = remote.find((r) => r.id === prev.id)
        return fresh ?? prev
      })
      setOpenRunId((id) => (id && !remote.some((r) => r.id === id) ? null : id))
    } catch {
      /* keep last known while down */
    }
  }, [])

  useEffect(() => {
    void loadMeta()
    void refreshRuns()
  }, [loadMeta, refreshRuns])

  useEffect(() => {
    const id = setInterval(() => void refreshRuns(), 4000)
    return () => clearInterval(id)
  }, [refreshRuns])

  useEffect(() => {
    if (!active?.id) return
    if (active.status === 'completed' || active.status === 'stopped' || active.status === 'failed') {
      void refreshRuns()
      return
    }
    const id = setInterval(() => {
      void getVegetaRun(active.id)
        .then((r) => {
          const view = r as RunView
          setActive(view)
          setRuns((prev) => {
            const next = upsertRun(prev, view)
            persistRuns(next)
            return next
          })
        })
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
      const run = res.run as RunView
      setActive(run)
      await refreshRuns()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'scenario failed')
    } finally {
      setBusy(false)
    }
  }

  async function onDeleteRun(id: string) {
    setBusy(true)
    setError(null)
    try {
      await deleteVegetaRun(id)
      if (active?.id === id) setActive(null)
      if (openRunId === id) setOpenRunId(null)
      await refreshRuns()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'delete failed')
    } finally {
      setBusy(false)
    }
  }

  async function onClearRuns() {
    if (!window.confirm('Delete all Vegeta test runs?')) return
    setBusy(true)
    setError(null)
    try {
      await clearVegetaRuns()
      setActive(null)
      setOpenRunId(null)
      setRuns([])
      persistRuns([])
    } catch (e) {
      setError(e instanceof Error ? e.message : 'clear failed')
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
                <>
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
                      <dt>throughput</dt>
                      <dd>{active.metrics.throughput.toFixed(2)} rps</dd>
                    </div>
                    <div>
                      <dt>status codes</dt>
                      <dd>
                        {Object.entries(active.metrics.statusCodes ?? {})
                          .map(([c, n]) => `${c}:${n}`)
                          .join(' ') || '—'}
                      </dd>
                    </div>
                  </dl>
                  <div className="latency-matrix latency-matrix--compact">
                    <div className="latency-matrix__grid">
                      {[
                        ['min', active.metrics.latencyMin],
                        ['mean', active.metrics.latencyMean],
                        ['p50', active.metrics.latencyP50],
                        ['p95', active.metrics.latencyP95],
                        ['p99', active.metrics.latencyP99],
                        ['max', active.metrics.latencyMax],
                      ].map(([label, value]) => (
                        <div key={label} className="latency-matrix__cell">
                          <span className="latency-matrix__label">{label}</span>
                          <strong className="latency-matrix__value">{value ?? '—'}</strong>
                        </div>
                      ))}
                    </div>
                  </div>
                </>
              ) : null}
            </div>
          )}
        </section>
      </div>

      <RunHistory
        runs={runs}
        openId={openRunId}
        busy={busy}
        onOpen={(r) => setOpenRunId(r.id)}
        onClose={() => setOpenRunId(null)}
        onRefresh={() => void refreshRuns()}
        onDelete={(id) => void onDeleteRun(id)}
        onClear={() => void onClearRuns()}
      />
    </div>
  )
}
