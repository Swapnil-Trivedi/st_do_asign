import { useEffect } from 'react'

export type RunMetrics = {
  requests: number
  successRatio: number
  latencyMin?: string
  latencyMean?: string
  latencyP50: string
  latencyP95: string
  latencyP99?: string
  latencyMax?: string
  bytesIn?: number
  bytesOut?: number
  throughput: number
  statusCodes?: Record<string, number>
  errors?: string[]
}

export type RunView = {
  id: string
  profile: string
  status: string
  target?: {
    baseUrl?: string
    method?: string
    path?: string
    headers?: Record<string, string>
  }
  startedAt?: string
  endedAt?: string
  error?: string
  metrics?: RunMetrics
}

function LatencyMatrix({ m }: { m: RunMetrics }) {
  const rows = [
    { label: 'min', value: m.latencyMin ?? '—' },
    { label: 'mean', value: m.latencyMean ?? '—' },
    { label: 'p50', value: m.latencyP50 },
    { label: 'p95', value: m.latencyP95 },
    { label: 'p99', value: m.latencyP99 ?? '—' },
    { label: 'max', value: m.latencyMax ?? '—' },
  ]
  return (
    <div className="latency-matrix">
      <h4>Vegeta latency matrix</h4>
      <div className="latency-matrix__grid">
        {rows.map((r) => (
          <div key={r.label} className="latency-matrix__cell">
            <span className="latency-matrix__label">{r.label}</span>
            <strong className="latency-matrix__value">{r.value}</strong>
          </div>
        ))}
      </div>
    </div>
  )
}

function ClientResponse({ m, error }: { m?: RunMetrics; error?: string }) {
  const codes = Object.entries(m?.statusCodes ?? {}).sort(([a], [b]) => a.localeCompare(b))
  return (
    <div className="client-response">
      <h4>Client response</h4>
      {error ? <p className="error">{error}</p> : null}
      <dl className="meta-list meta-list--grid">
        <div>
          <dt>requests</dt>
          <dd>{m?.requests ?? 0}</dd>
        </div>
        <div>
          <dt>success</dt>
          <dd>{m ? `${(m.successRatio * 100).toFixed(1)}%` : '—'}</dd>
        </div>
        <div>
          <dt>throughput</dt>
          <dd>{m ? `${m.throughput.toFixed(2)} rps` : '—'}</dd>
        </div>
        <div>
          <dt>bytes in / out</dt>
          <dd>{m ? `${m.bytesIn ?? 0} / ${m.bytesOut ?? 0}` : '—'}</dd>
        </div>
      </dl>
      <div className="status-codes">
        <span className="status-codes__title">HTTP status codes</span>
        {codes.length === 0 ? (
          <p className="muted">No status codes yet.</p>
        ) : (
          <div className="status-codes__row">
            {codes.map(([code, n]) => (
              <span
                key={code}
                className={`status-code status-code--${code.startsWith('2') ? 'ok' : code.startsWith('4') || code.startsWith('5') ? 'bad' : 'other'}`}
              >
                {code}: <strong>{n}</strong>
              </span>
            ))}
          </div>
        )}
      </div>
      {m?.errors && m.errors.length > 0 ? (
        <div className="run-errors">
          <span className="status-codes__title">Sample errors</span>
          <ul>
            {m.errors.slice(0, 5).map((e) => (
              <li key={e}>
                <code>{e}</code>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  )
}

function formatWhen(iso?: string) {
  if (!iso) return '—'
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

function RunDetailModal({
  run,
  busy,
  onClose,
  onDelete,
}: {
  run: RunView
  busy?: boolean
  onClose: () => void
  onDelete: (id: string) => void
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="modal-backdrop" role="presentation" onClick={onClose}>
      <div
        className="modal modal--wide"
        role="dialog"
        aria-modal="true"
        aria-labelledby="run-detail-title"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal__head">
          <div>
            <p className="modal__eyebrow">Test run</p>
            <h2 id="run-detail-title">
              {run.profile}{' '}
              <span className={`status status--${run.status}`}>{run.status}</span>
            </h2>
          </div>
          <button type="button" className="btn btn--ghost" onClick={onClose}>
            Close
          </button>
        </div>

        <div className="modal__body">
          <section className="modal__block">
            <h3>Run</h3>
            <dl className="meta-list meta-list--grid">
              <div>
                <dt>id</dt>
                <dd>
                  <code>{run.id}</code>
                </dd>
              </div>
              <div>
                <dt>target</dt>
                <dd>
                  <code>
                    {run.target?.method ?? 'GET'} {run.target?.baseUrl}
                    {run.target?.path}
                  </code>
                </dd>
              </div>
              <div>
                <dt>started</dt>
                <dd>{formatWhen(run.startedAt)}</dd>
              </div>
              <div>
                <dt>ended</dt>
                <dd>{formatWhen(run.endedAt)}</dd>
              </div>
              <div>
                <dt>US-API-Key</dt>
                <dd>
                  <code>{run.target?.headers?.['US-API-Key'] ?? '—'}</code>
                </dd>
              </div>
            </dl>
          </section>

          <div className="run-history__metrics">
            <ClientResponse m={run.metrics} error={run.error} />
            {run.metrics ? (
              <LatencyMatrix m={run.metrics} />
            ) : (
              <p className="muted">Latency matrix available when the run has samples.</p>
            )}
          </div>
        </div>

        <div className="modal__foot">
          <button
            type="button"
            className="btn btn--danger"
            disabled={busy}
            onClick={() => onDelete(run.id)}
          >
            Delete run
          </button>
          <button type="button" className="btn btn--ghost" onClick={onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  )
}

export function RunHistory({
  runs,
  openId,
  busy,
  onOpen,
  onClose,
  onRefresh,
  onDelete,
  onClear,
}: {
  runs: RunView[]
  openId?: string | null
  busy?: boolean
  onOpen: (run: RunView) => void
  onClose: () => void
  onRefresh: () => void
  onDelete: (id: string) => void
  onClear: () => void
}) {
  const ordered = runs.slice().sort((a, b) => {
    const at = a.startedAt ?? a.endedAt ?? ''
    const bt = b.startedAt ?? b.endedAt ?? ''
    return bt.localeCompare(at)
  })

  const openRun = openId ? ordered.find((r) => r.id === openId) ?? null : null

  return (
    <section className="panel run-history">
      <div className="panel__toolbar">
        <h2>Test run history</h2>
        <div className="btn-row">
          <button type="button" className="btn btn--ghost" onClick={onRefresh} disabled={busy}>
            Refresh
          </button>
          <button
            type="button"
            className="btn btn--ghost"
            disabled={busy || ordered.length === 0}
            onClick={onClear}
          >
            Clear all
          </button>
        </div>
      </div>
      <p className="muted" style={{ marginBottom: '0.85rem' }}>
        Select a row to open client response and Vegeta latency matrix.
      </p>

      {ordered.length === 0 ? (
        <p className="muted">No runs yet. Start a scenario or custom profile.</p>
      ) : (
        <div className="table-wrap">
          <table className="data-table">
            <thead>
              <tr>
                <th>Status</th>
                <th>Profile</th>
                <th>Target</th>
                <th>Requests</th>
                <th>Success</th>
                <th>p50</th>
                <th>p95</th>
                <th>Throughput</th>
                <th>Started</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {ordered.map((r) => {
                const target = `${r.target?.method ?? 'GET'} ${r.target?.path ?? '—'}`
                return (
                  <tr
                    key={r.id}
                    className={`data-table__row${openId === r.id ? ' data-table__row--active' : ''}`}
                    onClick={() => onOpen(r)}
                  >
                    <td>
                      <span className={`status status--${r.status}`}>{r.status}</span>
                    </td>
                    <td>
                      <strong>{r.profile}</strong>
                      <div className="muted data-table__sub">
                        <code>{r.id.slice(0, 8)}</code>
                      </div>
                    </td>
                    <td>
                      <code className="data-table__target">{target}</code>
                    </td>
                    <td>{r.metrics?.requests ?? '—'}</td>
                    <td>
                      {r.metrics ? `${(r.metrics.successRatio * 100).toFixed(0)}%` : '—'}
                    </td>
                    <td>{r.metrics?.latencyP50 ?? '—'}</td>
                    <td>{r.metrics?.latencyP95 ?? '—'}</td>
                    <td>
                      {r.metrics ? `${r.metrics.throughput.toFixed(1)} rps` : '—'}
                    </td>
                    <td>{formatWhen(r.startedAt)}</td>
                    <td className="data-table__actions" onClick={(e) => e.stopPropagation()}>
                      <button
                        type="button"
                        className="btn btn--ghost btn--tiny"
                        onClick={() => onOpen(r)}
                      >
                        View
                      </button>
                      <button
                        type="button"
                        className="btn btn--ghost btn--tiny"
                        disabled={busy}
                        onClick={() => onDelete(r.id)}
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {openRun ? (
        <RunDetailModal
          run={openRun}
          busy={busy}
          onClose={onClose}
          onDelete={(id) => {
            onDelete(id)
            onClose()
          }}
        />
      ) : null}
    </section>
  )
}
