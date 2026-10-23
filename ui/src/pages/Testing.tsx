import { useCallback, useEffect, useMemo, useState } from 'react'
import { PlaneTag } from '../components/PlaneTag'
import {
  ControlPlaneError,
  fetchTestCatalog,
  runControlPlaneTests,
  type HTTPExchange,
  type HTTPResponseCapture,
  type TestCaseResult,
  type TestRunReport,
  type TestSuiteInfo,
} from '../lib/controlPlaneApi'
import {
  DataPlaneError,
  fetchDataPlaneTestCatalog,
  runDataPlaneTests,
} from '../lib/dataPlaneApi'
import { SERVICES } from '../lib/config'

type Plane = 'control' | 'data'

const CONTROL_FALLBACK: { id: string; label: string }[] = [
  { id: 'unit', label: 'Unit' },
  { id: 'functional', label: 'Functional' },
  { id: 'concurrency', label: 'Concurrency' },
  { id: 'e2e', label: 'E2E' },
  { id: 'all', label: 'All' },
]

const DATA_FALLBACK: { id: string; label: string }[] = [
  { id: 'unit', label: 'Unit' },
  { id: 'functional', label: 'Functional' },
  { id: 'all', label: 'All' },
]

function errMsg(e: unknown): string {
  if (e instanceof ControlPlaneError || e instanceof DataPlaneError) return e.message
  if (e instanceof Error) return e.message
  return 'unexpected error'
}

function pretty(v: unknown): string {
  if (v == null) return '—'
  if (typeof v === 'string') {
    try {
      return JSON.stringify(JSON.parse(v), null, 2)
    } catch {
      return v
    }
  }
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}

function ExchangeBlock({
  title,
  request,
  response,
}: {
  title?: string
  request?: HTTPExchange
  response?: HTTPResponseCapture
}) {
  if (!request && !response) return null
  return (
    <div className="test-exchange">
      {title ? <h4 className="test-exchange__title">{title}</h4> : null}
      <div className="test-exchange__grid">
        <div className="test-exchange__pane">
          <div className="test-exchange__label">Request</div>
          {request ? (
            <>
              <div className="test-exchange__line">
                <code>
                  {request.method} {request.path}
                </code>
              </div>
              <pre className="docs__pre docs__pre--compact">{pretty(request.body)}</pre>
            </>
          ) : (
            <p className="muted">—</p>
          )}
        </div>
        <div className="test-exchange__pane">
          <div className="test-exchange__label">Response</div>
          {response ? (
            <>
              <div className="test-exchange__line">
                <code className={response.status >= 400 ? 'error' : ''}>HTTP {response.status}</code>
              </div>
              <pre className="docs__pre docs__pre--compact">{pretty(response.body)}</pre>
            </>
          ) : (
            <p className="muted">—</p>
          )}
        </div>
      </div>
    </div>
  )
}

function TestCaseCard({
  result,
  open,
  onToggle,
}: {
  result: TestCaseResult
  open: boolean
  onToggle: () => void
}) {
  return (
    <article className={`test-card test-card--${result.status}${open ? ' test-card--open' : ''}`}>
      <button type="button" className="test-card__head" onClick={onToggle}>
        <div className="test-card__head-main">
          <span className={`status status--${result.status}`}>{result.status}</span>
          <span className="test-card__suite">{result.suite}</span>
          <strong>{result.name}</strong>
        </div>
        <div className="test-card__head-meta muted">
          <code>{result.id}</code>
          <span>{result.durationMs}</span>
          <span>{open ? 'Hide' : 'Details'}</span>
        </div>
      </button>

      <p className="test-card__desc">{result.description}</p>

      {open ? (
        <div className="test-card__body">
          <dl className="meta-list meta-list--grid">
            <div>
              <dt>result message</dt>
              <dd>
                <code>{result.message || '—'}</code>
              </dd>
            </div>
            <div>
              <dt>suite</dt>
              <dd>
                <code>{result.suite}</code>
              </dd>
            </div>
            <div>
              <dt>duration</dt>
              <dd>{result.durationMs}</dd>
            </div>
            <div>
              <dt>case id</dt>
              <dd>
                <code>{result.id}</code>
              </dd>
            </div>
          </dl>

          {result.assertions && result.assertions.length > 0 ? (
            <div className="test-card__block">
              <h4>Assertions</h4>
              <ul className="test-assert-list">
                {result.assertions.map((a) => (
                  <li key={a}>
                    <code>{a}</code>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}

          {result.meta ? (
            <div className="test-card__block">
              <h4>Meta</h4>
              <pre className="docs__pre docs__pre--compact">{pretty(result.meta)}</pre>
            </div>
          ) : null}

          <ExchangeBlock request={result.request} response={result.response} />

          {result.steps && result.steps.length > 0 ? (
            <div className="test-card__block">
              <h4>Steps ({result.steps.length})</h4>
              <div className="test-steps">
                {result.steps.map((s, idx) => (
                  <div key={`${s.name}-${idx}`} className={`test-step${s.ok ? '' : ' test-step--bad'}`}>
                    <div className="test-step__head">
                      <span className={`status status--${s.ok ? 'passed' : 'failed'}`}>
                        {s.ok ? 'ok' : 'fail'}
                      </span>
                      <strong>
                        {idx + 1}. {s.name}
                      </strong>
                      {s.detail ? <span className="muted">{s.detail}</span> : null}
                    </div>
                    <ExchangeBlock request={s.request} response={s.response} />
                  </div>
                ))}
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
    </article>
  )
}

type HistoryEntry = TestRunReport & { plane: Plane }

export function Testing() {
  const [plane, setPlane] = useState<Plane>('control')
  const [catalog, setCatalog] = useState<TestSuiteInfo[]>([])
  const [suite, setSuite] = useState<string>('all')
  const [report, setReport] = useState<TestRunReport | null>(null)
  const [reportPlane, setReportPlane] = useState<Plane>('control')
  const [history, setHistory] = useState<HistoryEntry[]>([])
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [filter, setFilter] = useState<'all' | 'passed' | 'failed' | 'skipped'>('all')
  const [openIds, setOpenIds] = useState<Record<string, boolean>>({})

  const hostUrl = plane === 'control' ? SERVICES.controlPlane.hostUrl : SERVICES.rateLimiter.hostUrl
  const fallbackSuites = plane === 'control' ? CONTROL_FALLBACK : DATA_FALLBACK

  const loadCatalog = useCallback(async (forPlane: Plane) => {
    try {
      const data =
        forPlane === 'control' ? await fetchTestCatalog() : await fetchDataPlaneTestCatalog()
      setCatalog(data.suites ?? [])
      setError(null)
    } catch (e) {
      setCatalog([])
      setError(errMsg(e))
    }
  }, [])

  useEffect(() => {
    void loadCatalog(plane)
  }, [plane, loadCatalog])

  async function onRun() {
    setBusy(true)
    setError(null)
    try {
      const next =
        plane === 'control' ? await runControlPlaneTests(suite) : await runDataPlaneTests(suite)
      setReport(next)
      setReportPlane(plane)
      setHistory((prev) => [{ ...next, plane }, ...prev].slice(0, 12))
      const open: Record<string, boolean> = {}
      for (const r of next.results) {
        if (r.status === 'failed') open[r.id] = true
      }
      setOpenIds(open)
      setFilter(next.failed > 0 ? 'failed' : 'all')
    } catch (e) {
      setError(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  const suiteTabs = useMemo(() => {
    if (catalog.length > 0) {
      return catalog.map((s) => ({ id: s.id, label: s.label.replace(/\s+tests$/i, '') || s.id }))
    }
    return fallbackSuites
  }, [catalog, fallbackSuites])

  const selectedMeta = catalog.find((s) => s.id === suite)

  const visible = useMemo(() => {
    if (!report) return []
    if (filter === 'all') return report.results
    return report.results.filter((r) => r.status === filter)
  }, [report, filter])

  function toggle(id: string) {
    setOpenIds((prev) => ({ ...prev, [id]: !prev[id] }))
  }

  function expandAll(open: boolean) {
    if (!report) return
    const next: Record<string, boolean> = {}
    for (const r of visible) next[r.id] = open
    setOpenIds((prev) => ({ ...prev, ...next }))
  }

  function switchPlane(next: Plane) {
    if (next === plane || busy) return
    setPlane(next)
    setReport(null)
    setSuite('all')
    setFilter('all')
    setOpenIds({})
  }

  return (
    <div className="page">
      <header className="page__header">
        <div>
          <div className="page__eyebrow">
            <PlaneTag plane={plane} />
          </div>
          <h1>Testing</h1>
          <p className="muted">
            {plane === 'control'
              ? 'Control-plane unit, functional, concurrency, and e2e suites with request/response traces'
              : 'Data-plane unit (matcher / Lua / cache) and functional (live Redis + quota / match) suites'}{' '}
            · <code>{hostUrl}</code>
          </p>
        </div>
        <button
          type="button"
          className="btn btn--ghost"
          disabled={busy}
          onClick={() => void loadCatalog(plane)}
        >
          Reload catalog
        </button>
      </header>

      {error ? <p className="error" role="alert">{error}</p> : null}

      <section className="panel">
        <div className="panel__toolbar">
          <h2>Plane</h2>
        </div>
        <div className="rl-subnav" role="tablist" aria-label="Plane">
          <button
            type="button"
            role="tab"
            aria-selected={plane === 'control'}
            className={`rl-subnav__btn${plane === 'control' ? ' rl-subnav__btn--active' : ''}`}
            onClick={() => switchPlane('control')}
            disabled={busy}
          >
            Control plane
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={plane === 'data'}
            className={`rl-subnav__btn${plane === 'data' ? ' rl-subnav__btn--active' : ''}`}
            onClick={() => switchPlane('data')}
            disabled={busy}
          >
            Data plane
          </button>
        </div>
      </section>

      <section className="panel">
        <div className="panel__toolbar">
          <h2>Run suite</h2>
        </div>
        <div className="rl-subnav" role="tablist" aria-label="Test suites">
          {suiteTabs.map((tab) => (
            <button
              key={tab.id}
              type="button"
              role="tab"
              aria-selected={suite === tab.id}
              className={`rl-subnav__btn${suite === tab.id ? ' rl-subnav__btn--active' : ''}`}
              onClick={() => setSuite(tab.id)}
              disabled={busy}
            >
              {tab.label}
            </button>
          ))}
        </div>
        {selectedMeta ? (
          <p className="muted" style={{ marginBottom: '0.85rem' }}>
            {selectedMeta.description}
          </p>
        ) : null}
        <div className="btn-row">
          <button type="button" className="btn" disabled={busy} onClick={() => void onRun()}>
            {busy ? 'Running…' : `Run ${suite} suite`}
          </button>
        </div>
      </section>

      {report ? (
        <section className="panel">
          <div className="panel__toolbar">
            <h2>Latest run</h2>
            <span className={`test-summary${report.ok ? ' test-summary--ok' : ' test-summary--fail'}`}>
              {report.ok ? 'PASSED' : 'FAILED'} · {report.passed}/{report.total} · {report.duration}
            </span>
          </div>

          <dl className="meta-list meta-list--grid" style={{ marginBottom: '0.85rem' }}>
            <div>
              <dt>run id</dt>
              <dd>
                <code>{report.id}</code>
              </dd>
            </div>
            <div>
              <dt>suite</dt>
              <dd>
                <code>
                  {reportPlane}:{report.suite}
                </code>
              </dd>
            </div>
            <div>
              <dt>started</dt>
              <dd>{new Date(report.startedAt).toLocaleString()}</dd>
            </div>
            <div>
              <dt>failed</dt>
              <dd>{report.failed}</dd>
            </div>
            <div>
              <dt>skipped</dt>
              <dd>{report.skipped ?? report.results.filter((r) => r.status === 'skipped').length}</dd>
            </div>
          </dl>

          <div className="btn-row" style={{ marginBottom: '0.85rem' }}>
            {(['all', 'passed', 'failed', 'skipped'] as const).map((f) => (
              <button
                key={f}
                type="button"
                className="btn btn--ghost"
                style={
                  filter === f
                    ? {
                        borderColor: plane === 'control' ? 'var(--control)' : 'var(--data)',
                        color: plane === 'control' ? 'var(--control)' : 'var(--data)',
                      }
                    : undefined
                }
                onClick={() => setFilter(f)}
              >
                {f} ({f === 'all' ? report.total : report.results.filter((r) => r.status === f).length})
              </button>
            ))}
            <button type="button" className="btn btn--ghost" onClick={() => expandAll(true)}>
              Expand visible
            </button>
            <button type="button" className="btn btn--ghost" onClick={() => expandAll(false)}>
              Collapse
            </button>
          </div>

          <div className="test-card-list">
            {visible.map((r) => (
              <TestCaseCard
                key={r.id}
                result={r}
                open={!!openIds[r.id]}
                onToggle={() => toggle(r.id)}
              />
            ))}
          </div>
        </section>
      ) : (
        <section className="panel">
          <p className="muted">
            No run yet. Choose a plane and suite, then click Run to see cases, assertions, and HTTP
            traces.
          </p>
        </section>
      )}

      {history.length > 1 ? (
        <section className="panel">
          <h2>Run history</h2>
          <div className="test-history">
            {history.map((h) => (
              <button
                key={h.id}
                type="button"
                className={`test-history__item${report?.id === h.id ? ' test-history__item--active' : ''}`}
                onClick={() => {
                  setReport(h)
                  setReportPlane(h.plane)
                  setPlane(h.plane)
                  setFilter('all')
                }}
              >
                <span className={`status status--${h.ok ? 'passed' : 'failed'}`}>
                  {h.ok ? 'passed' : 'failed'}
                </span>
                <strong>
                  {h.plane}:{h.suite}
                </strong>
                <span className="muted">
                  {h.passed}/{h.total} · {h.duration}
                </span>
                <span className="muted">{new Date(h.startedAt).toLocaleString()}</span>
              </button>
            ))}
          </div>
        </section>
      ) : null}
    </div>
  )
}
