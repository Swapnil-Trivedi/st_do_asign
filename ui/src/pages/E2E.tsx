import { useCallback, useEffect, useMemo, useState } from 'react'
import { PlaneTag } from '../components/PlaneTag'
import { SERVICES } from '../lib/config'
import {
  E2EApiError,
  FALLBACK_E2E_CATALOG,
  fetchE2ECatalog,
  listE2ERuns,
  runE2E,
  type E2EGroupInfo,
  type E2EReport,
  type E2ESetupInfo,
  type E2ESetupResult,
} from '../lib/e2eApi'

function errMsg(e: unknown): string {
  if (e instanceof E2EApiError) return e.message
  if (e instanceof Error) return e.message
  return 'unexpected error'
}

function TrafficBar({ allowed, rateLimited, other }: { allowed: number; rateLimited: number; other: number }) {
  const total = Math.max(1, allowed + rateLimited + other)
  const a = (allowed / total) * 100
  const d = (rateLimited / total) * 100
  const o = (other / total) * 100
  return (
    <div className="e2e-bar" title={`allow ${allowed} · 429 ${rateLimited} · other ${other}`}>
      <span className="e2e-bar__allow" style={{ width: `${a}%` }} />
      <span className="e2e-bar__deny" style={{ width: `${d}%` }} />
      <span className="e2e-bar__other" style={{ width: `${o}%` }} />
    </div>
  )
}

function SetupCard({
  result,
  open,
  onToggle,
}: {
  result: E2ESetupResult
  open: boolean
  onToggle: () => void
}) {
  const t = result.traffic
  return (
    <article className={`test-card test-card--${result.status}`}>
      <button type="button" className="test-card__head" onClick={onToggle}>
        <div className="test-card__head-main">
          <span className="test-card__suite">{result.group}</span>
          <strong>{result.name}</strong>
          <span className="muted">{result.driver}</span>
        </div>
        <div className="test-card__head-meta">
          <span className={`status status--${result.status}`}>{result.status}</span>
          <span className="muted">{result.durationMs}ms</span>
        </div>
      </button>
      <p className="test-card__desc">{result.description}</p>
      <div className="e2e-card__traffic">
        <TrafficBar allowed={t.allowed} rateLimited={t.rateLimited} other={t.other} />
        <div className="e2e-card__counts">
          <span>
            allow <strong>{t.allowed}</strong>
          </span>
          <span>
            429 <strong>{t.rateLimited}</strong>
          </span>
          <span>
            other <strong>{t.other}</strong>
          </span>
          <span>
            total <strong>{t.total}</strong>
          </span>
        </div>
      </div>
      {open ? (
        <div className="test-card__body">
          {result.message ? (
            <div className="test-card__block">
              <h4>Result</h4>
              <p>{result.message}</p>
            </div>
          ) : null}
          {result.quota ? (
            <div className="test-card__block">
              <h4>Quota peek</h4>
              <pre className="docs__pre">
                {JSON.stringify(
                  {
                    namespace: result.quota.namespace,
                    remaining: result.quota.remaining,
                    consumed: result.quota.consumed,
                    limit: result.quota.limit,
                    algorithm: result.quota.algorithm,
                  },
                  null,
                  2,
                )}
              </pre>
            </div>
          ) : null}
          <div className="test-card__block">
            <h4>Service analytics</h4>
            <ul className="e2e-analytics">
              <li>
                Proxy instances:{' '}
                {Object.entries(result.analytics.proxyInstances ?? {})
                  .map(([k, v]) => `${k}=${v}`)
                  .join(', ') || '—'}
              </li>
              <li>Upstream forwards: {result.analytics.upstreamForwards}</li>
              <li>API key match true: {result.analytics.keyMatchTrue}</li>
              <li>
                Mock log delta: access={result.analytics.mockLogDelta.access} upstream=
                {result.analytics.mockLogDelta.upstream} total={result.analytics.mockLogDelta.total}
              </li>
              {result.analytics.concurrentVegetaJobs ? (
                <li>Concurrent Vegeta jobs: {result.analytics.concurrentVegetaJobs}</li>
              ) : null}
            </ul>
            {result.analytics.rateLimitHeaders?.lastAllow ||
            result.analytics.rateLimitHeaders?.lastDeny ? (
              <pre className="docs__pre">
                {JSON.stringify(result.analytics.rateLimitHeaders, null, 2)}
              </pre>
            ) : null}
          </div>
          {result.vegetaRuns && result.vegetaRuns.length > 0 ? (
            <div className="test-card__block">
              <h4>Vegeta runs</h4>
              <table className="e2e-table">
                <thead>
                  <tr>
                    <th>Profile</th>
                    <th>Status</th>
                    <th>Requests</th>
                    <th>Success</th>
                    <th>429</th>
                    <th>p95</th>
                  </tr>
                </thead>
                <tbody>
                  {result.vegetaRuns.map((vr) => (
                    <tr key={vr.runId}>
                      <td>{vr.profile}</td>
                      <td>{vr.status}</td>
                      <td>{vr.metrics?.requests ?? '—'}</td>
                      <td>
                        {vr.metrics
                          ? `${(vr.metrics.successRatio * 100).toFixed(1)}%`
                          : '—'}
                      </td>
                      <td>{vr.metrics?.statusCodes?.['429'] ?? 0}</td>
                      <td>{vr.metrics?.latencyP95 ?? '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
          {result.steps && result.steps.length > 0 ? (
            <div className="test-card__block">
              <h4>Steps</h4>
              <ul className="e2e-steps">
                {result.steps.map((st, i) => (
                  <li key={`${st.name}-${i}`} className={st.ok ? 'ok' : 'bad'}>
                    <strong>{st.name}</strong>
                    {st.detail ? <span className="muted"> — {st.detail}</span> : null}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      ) : null}
    </article>
  )
}

function applyCatalog(
  data: { groups: E2EGroupInfo[]; setups: E2ESetupInfo[] },
  setGroups: (g: E2EGroupInfo[]) => void,
  setSetups: (s: E2ESetupInfo[]) => void,
  setSelected: (fn: (prev: Record<string, boolean>) => Record<string, boolean>) => void,
) {
  setGroups(data.groups ?? [])
  setSetups(data.setups ?? [])
  setSelected(() => {
    const init: Record<string, boolean> = {}
    for (const s of data.setups ?? []) init[s.id] = true
    return init
  })
}

export function E2E() {
  const [groups, setGroups] = useState<E2EGroupInfo[]>(FALLBACK_E2E_CATALOG.groups)
  const [setups, setSetups] = useState<E2ESetupInfo[]>(FALLBACK_E2E_CATALOG.setups)
  const [catalogLive, setCatalogLive] = useState(false)
  const [selectedGroup, setSelectedGroup] = useState<string>('all')
  const [selected, setSelected] = useState<Record<string, boolean>>(() => {
    const init: Record<string, boolean> = {}
    for (const s of FALLBACK_E2E_CATALOG.setups) init[s.id] = true
    return init
  })
  const [report, setReport] = useState<E2EReport | null>(null)
  const [history, setHistory] = useState<E2EReport[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [openIds, setOpenIds] = useState<Record<string, boolean>>({})

  const loadCatalog = useCallback(async () => {
    try {
      const data = await fetchE2ECatalog()
      applyCatalog(data, setGroups, setSetups, setSelected)
      setCatalogLive(true)
      setError(null)
    } catch (e) {
      applyCatalog(FALLBACK_E2E_CATALOG, setGroups, setSetups, setSelected)
      setCatalogLive(false)
      setError(errMsg(e))
    }
  }, [])

  useEffect(() => {
    void loadCatalog()
    void listE2ERuns()
      .then((d) => setHistory(d.runs ?? []))
      .catch(() => undefined)
  }, [loadCatalog])

  const visibleSetups = useMemo(() => {
    if (selectedGroup === 'all') return setups
    return setups.filter((s) => s.group === selectedGroup)
  }, [setups, selectedGroup])

  const selectedCount = useMemo(
    () => visibleSetups.filter((s) => selected[s.id]).length,
    [visibleSetups, selected],
  )

  function toggleSetup(id: string) {
    setSelected((prev) => ({ ...prev, [id]: !prev[id] }))
  }

  function selectVisible(on: boolean) {
    setSelected((prev) => {
      const next = { ...prev }
      for (const s of visibleSetups) next[s.id] = on
      return next
    })
  }

  async function onRun() {
    const ids = Object.entries(selected)
      .filter(([, v]) => v)
      .map(([k]) => k)
    if (ids.length === 0) {
      setError('Select at least one setup')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const next = await runE2E({ setups: ids })
      setReport(next)
      setHistory((prev) => [next, ...prev].slice(0, 12))
      const open: Record<string, boolean> = {}
      for (const s of next.setups) {
        if (s.status === 'failed') open[s.setupId] = true
      }
      setOpenIds(open)
    } catch (e) {
      setError(errMsg(e))
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
          <h1>E2E automation</h1>
          <p className="muted">
            Cross-stack fixtures, Vegeta, and concurrency against the rate-limiter LB ·{' '}
            <code>{SERVICES.consoleApi.hostUrl}</code>
          </p>
        </div>
        <button type="button" className="btn btn--ghost" disabled={busy} onClick={() => void loadCatalog()}>
          Reload catalog
        </button>
      </header>

      {error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : null}

      {!catalogLive ? (
        <p className="muted" role="status">
          Showing offline test-case list. Run is disabled until{' '}
          <code>/api/v1/e2e/catalog</code> responds (rebuild console-api if you still see 404).
        </p>
      ) : null}

      <section className="panel">
        <div className="panel__toolbar">
          <h2>
            Test cases{' '}
            <span className="muted">
              ({selectedCount}/{visibleSetups.length} selected
              {selectedGroup !== 'all' ? ` · ${selectedGroup}` : ''} · {setups.length} total)
            </span>
          </h2>
          <div className="panel__actions">
            <button type="button" className="btn btn--ghost" disabled={busy} onClick={() => selectVisible(true)}>
              Select visible
            </button>
            <button type="button" className="btn btn--ghost" disabled={busy} onClick={() => selectVisible(false)}>
              Clear visible
            </button>
            <button
              type="button"
              className="btn"
              disabled={busy || !catalogLive}
              onClick={() => void onRun()}
              title={catalogLive ? undefined : 'Catalog API unavailable'}
            >
              {busy ? 'Running…' : 'Run E2E'}
            </button>
          </div>
        </div>

        <div className="rl-subnav" role="tablist" aria-label="Setup groups">
          <button
            type="button"
            className={`rl-subnav__btn${selectedGroup === 'all' ? ' rl-subnav__btn--active' : ''}`}
            onClick={() => setSelectedGroup('all')}
            disabled={busy}
          >
            All ({setups.length})
          </button>
          {groups.map((g) => {
            const n = setups.filter((s) => s.group === g.id).length
            return (
              <button
                key={g.id}
                type="button"
                className={`rl-subnav__btn${selectedGroup === g.id ? ' rl-subnav__btn--active' : ''}`}
                onClick={() => setSelectedGroup(g.id)}
                disabled={busy}
                title={g.description}
              >
                {g.label} ({n})
              </button>
            )
          })}
        </div>

        <div className="e2e-case-table-wrap">
          <table className="e2e-table e2e-case-table">
            <thead>
              <tr>
                <th scope="col" className="e2e-case-table__check">
                  <span className="sr-only">Select</span>
                </th>
                <th scope="col">Id</th>
                <th scope="col">Name</th>
                <th scope="col">Group</th>
                <th scope="col">Driver</th>
                <th scope="col">Expect / load</th>
              </tr>
            </thead>
            <tbody>
              {visibleSetups.map((s) => (
                <tr key={s.id} className={selected[s.id] ? 'e2e-case-table__row--on' : undefined}>
                  <td className="e2e-case-table__check">
                    <input
                      type="checkbox"
                      checked={!!selected[s.id]}
                      disabled={busy}
                      onChange={() => toggleSetup(s.id)}
                      aria-label={`Select ${s.id}`}
                    />
                  </td>
                  <td>
                    <code className="e2e-case-id">{s.id}</code>
                  </td>
                  <td>
                    <strong>{s.name}</strong>
                  </td>
                  <td>
                    <span className="e2e-chip">{s.group}</span>
                  </td>
                  <td>
                    <span className="e2e-chip e2e-chip--driver">{s.driver}</span>
                  </td>
                  <td className="muted">{s.description}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {visibleSetups.length === 0 ? <p className="muted">No test cases in this group.</p> : null}
        {busy ? (
          <p className="muted">Running selected setups (policy wait + load). This can take a few minutes…</p>
        ) : null}
      </section>

      {report ? (
        <section className="panel">
          <div className="panel__toolbar">
            <h2>Report</h2>
            <p
              className={`test-summary${report.ok ? ' test-summary--ok' : ' test-summary--fail'}`}
            >
              {report.ok ? 'OK' : 'FAILED'} · {report.summary.passed}/{report.summary.totalSetups}{' '}
              passed · allow {report.summary.allowed} · 429 {report.summary.rateLimited} · vegeta
              jobs {report.summary.vegetaJobs} · {report.duration}
            </p>
          </div>
          <div className="test-card-list">
            {report.setups.map((s) => (
              <SetupCard
                key={s.setupId}
                result={s}
                open={!!openIds[s.setupId]}
                onToggle={() => setOpenIds((prev) => ({ ...prev, [s.setupId]: !prev[s.setupId] }))}
              />
            ))}
          </div>
        </section>
      ) : null}

      {history.length > 1 ? (
        <section className="panel">
          <h2>History</h2>
          <ul className="e2e-history">
            {history.map((h) => (
              <li key={h.id}>
                <button
                  type="button"
                  className="btn btn--ghost"
                  onClick={() => {
                    setReport(h)
                  }}
                >
                  {h.startedAt} · {h.ok ? 'ok' : 'fail'} · allow {h.summary.allowed} / 429{' '}
                  {h.summary.rateLimited}
                </button>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  )
}
