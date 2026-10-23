import { useCallback, useEffect, useState } from 'react'
import { PlaneTag } from '../components/PlaneTag'
import {
  MOCK_API_INFO,
  MOCK_CURL_EXAMPLES,
  MOCK_ENDPOINTS,
  MOCK_HEADERS,
  MOCK_TENANTS,
} from '../data/mockApiDocs'
import { fetchMockLogs } from '../lib/api'
import { SERVICES } from '../lib/config'

type Tab = 'logs' | 'docs'

export function MockBackend() {
  const [tab, setTab] = useState<Tab>('logs')
  const [logs, setLogs] = useState<{ ts: string; level: string; message: string }[]>([])
  const [error, setError] = useState<string | null>(null)

  const loadLogs = useCallback(async () => {
    try {
      const data = await fetchMockLogs(200)
      setLogs([...data.logs].reverse())
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'failed to load logs')
    }
  }, [])

  useEffect(() => {
    if (tab !== 'logs') return
    void loadLogs()
    const id = setInterval(() => void loadLogs(), 2000)
    return () => clearInterval(id)
  }, [tab, loadLogs])

  return (
    <div className="page">
      <header className="page__header">
        <div>
          <div className="page__eyebrow">
            <PlaneTag plane="data" />
          </div>
          <h1>Mock Backend</h1>
          <p className="muted">
            Upstream on <code>{SERVICES.mockServer.networkUrl}</code> · host{' '}
            <code>{SERVICES.mockServer.hostUrl}</code>
          </p>
        </div>
      </header>

      <div className="tabs">
        <button
          type="button"
          className={`tabs__btn${tab === 'logs' ? ' tabs__btn--active' : ''}`}
          onClick={() => setTab('logs')}
        >
          Logs
        </button>
        <button
          type="button"
          className={`tabs__btn${tab === 'docs' ? ' tabs__btn--active' : ''}`}
          onClick={() => setTab('docs')}
        >
          API Docs
        </button>
      </div>

      {tab === 'logs' ? (
        <section className="panel">
          <div className="panel__toolbar">
            <h2>Access &amp; upstream logs</h2>
            <button type="button" className="btn btn--ghost" onClick={() => void loadLogs()}>
              Refresh
            </button>
          </div>
          {error ? <p className="error">{error} — is mock-server up?</p> : null}
          <div className="log-view">
            {logs.length === 0 && !error ? <p className="muted">No log lines yet.</p> : null}
            {logs.map((l, i) => (
              <div key={`${l.ts}-${i}`} className="log-line">
                <span className="log-line__ts">{new Date(l.ts).toLocaleTimeString()}</span>
                <span className={`log-line__lvl log-line__lvl--${l.level}`}>{l.level}</span>
                <span className="log-line__msg">{l.message}</span>
              </div>
            ))}
          </div>
        </section>
      ) : (
        <MockApiDocsPanel />
      )}
    </div>
  )
}

function MockApiDocsPanel() {
  return (
    <div className="docs">
      <section className="panel">
        <h2>
          {MOCK_API_INFO.title}{' '}
          <span className="docs__ver">v{MOCK_API_INFO.version}</span>
        </h2>
        <p className="muted">{MOCK_API_INFO.description}</p>
        <dl className="meta-list meta-list--grid" style={{ marginTop: '0.85rem' }}>
          <div>
            <dt>Host</dt>
            <dd>
              <code>{MOCK_API_INFO.baseUrlHost}</code>
            </dd>
          </div>
          <div>
            <dt>st_net</dt>
            <dd>
              <code>{MOCK_API_INFO.baseUrlNetwork}</code>
            </dd>
          </div>
        </dl>
      </section>

      <section className="panel">
        <h2>Observability headers</h2>
        <div className="docs__table-wrap">
          <table className="docs__table">
            <thead>
              <tr>
                <th>Header</th>
                <th>Role</th>
              </tr>
            </thead>
            <tbody>
              {MOCK_HEADERS.map((h) => (
                <tr key={h.name}>
                  <td>
                    <code>{h.name}</code>
                  </td>
                  <td>{h.role}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <h2>Mock tenant → API keys</h2>
        <div className="docs__table-wrap">
          <table className="docs__table">
            <thead>
              <tr>
                <th>Tenant</th>
                <th>Keys</th>
              </tr>
            </thead>
            <tbody>
              {MOCK_TENANTS.map((t) => (
                <tr key={t.tenantId}>
                  <td>
                    <code>{t.tenantId}</code>
                  </td>
                  <td>
                    {t.keys.map((k) => (
                      <code key={k} className="docs__chip">
                        {k}
                      </code>
                    ))}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <h2>Endpoints</h2>
        <div className="docs__endpoints">
          {MOCK_ENDPOINTS.map((ep) => (
            <article key={`${ep.method}-${ep.path}`} className="docs__ep">
              <div className="docs__ep-top">
                <span className={`docs__method docs__method--${ep.method}`}>{ep.method}</span>
                <code className="docs__path">{ep.path}</code>
                {ep.delayMs ? <span className="docs__badge">{ep.delayMs}ms delay</span> : null}
              </div>
              <p className="muted">{ep.summary}</p>

              {ep.params && ep.params.length > 0 ? (
                <div className="docs__block">
                  <h3>Parameters</h3>
                  <ul>
                    {ep.params.map((p) => (
                      <li key={`${p.in}-${p.name}`}>
                        <code>{p.name}</code> <span className="docs__in">{p.in}</span>
                        {p.required ? <span className="docs__req">required</span> : null} — {p.detail}
                      </li>
                    ))}
                  </ul>
                </div>
              ) : null}

              {ep.body ? (
                <div className="docs__block">
                  <h3>Body</h3>
                  <pre className="docs__pre">{ep.body}</pre>
                </div>
              ) : null}

              <div className="docs__block">
                <h3>Responses</h3>
                <ul>
                  {ep.responses.map((r) => (
                    <li key={r.status}>
                      <strong>{r.status}</strong> — <code>{r.detail}</code>
                    </li>
                  ))}
                </ul>
              </div>

              {ep.responseHeaders && ep.responseHeaders.length > 0 ? (
                <div className="docs__block">
                  <h3>Response headers</h3>
                  <p className="docs__hdrs">
                    {ep.responseHeaders.map((h) => (
                      <code key={h} className="docs__chip">
                        {h}
                      </code>
                    ))}
                  </p>
                </div>
              ) : null}
            </article>
          ))}
        </div>
      </section>

      <section className="panel">
        <h2>Examples</h2>
        <div className="docs__examples">
          {MOCK_CURL_EXAMPLES.map((ex) => (
            <div key={ex.title} className="docs__ex">
              <h3>{ex.title}</h3>
              <pre className="docs__pre">{ex.cmd}</pre>
            </div>
          ))}
        </div>
      </section>
    </div>
  )
}
