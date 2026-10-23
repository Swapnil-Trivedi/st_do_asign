import { useMemo, useState } from 'react'
import {
  CP_API_INFO,
  CP_CURL_EXAMPLES,
  CP_ENDPOINTS,
  CP_OPENAPI_YAML,
  type CpEndpoint,
} from '../data/controlPlaneApiDocs'

type FocusGroup = CpEndpoint['group'] | 'all'

export function ControlPlaneDocsPanel() {
  const [group, setGroup] = useState<FocusGroup>('all')

  const endpoints = useMemo(() => {
    if (group === 'all') return CP_ENDPOINTS
    return CP_ENDPOINTS.filter((e) => e.group === group)
  }, [group])

  return (
    <div className="docs">
      <section className="panel">
        <h2>
          {CP_API_INFO.title}{' '}
          <span className="docs__ver">v{CP_API_INFO.version}</span>
        </h2>
        <p className="muted">{CP_API_INFO.description}</p>
        <dl className="meta-list meta-list--grid" style={{ marginTop: '0.85rem' }}>
          <div>
            <dt>Control plane</dt>
            <dd>
              <code>{CP_API_INFO.baseUrlHost}</code>
            </dd>
          </div>
          <div>
            <dt>Control st_net</dt>
            <dd>
              <code>{CP_API_INFO.baseUrlNetwork}</code>
            </dd>
          </div>
          <div>
            <dt>Proxy (LB)</dt>
            <dd>
              <code>{CP_API_INFO.proxyHost}</code>
            </dd>
          </div>
          <div>
            <dt>Proxy st_net</dt>
            <dd>
              <code>{CP_API_INFO.proxyNetwork}</code>
            </dd>
          </div>
        </dl>
      </section>

      <section className="panel">
        <div className="panel__toolbar">
          <h2>Endpoints</h2>
          <div className="cp-docs__filters">
            {(
              [
                'all',
                'quota',
                'proxy',
                'configs',
                'namespaces',
                'rules',
                'algorithms',
                'tests',
              ] as FocusGroup[]
            ).map((g) => (
              <button
                key={g}
                type="button"
                className={`btn btn--ghost btn--tiny${group === g ? ' cp-docs__filter--on' : ''}`}
                onClick={() => setGroup(g)}
              >
                {g}
              </button>
            ))}
          </div>
        </div>
        <div className="docs__endpoints">
          {endpoints.map((ep) => (
            <article key={`${ep.plane}-${ep.method}-${ep.path}`} className="docs__ep">
              <div className="docs__ep-top">
                <span className={`docs__method docs__method--${ep.method === '*' ? 'ANY' : ep.method}`}>
                  {ep.method === '*' ? 'ANY' : ep.method}
                </span>
                <code className="docs__path">{ep.path}</code>
                <span className="docs__badge">{ep.group}</span>
                <span className="docs__badge">{ep.plane}</span>
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
                <ul className="docs__resp-list">
                  {ep.responses.map((r) => (
                    <li key={r.status} className="docs__resp">
                      <div className="docs__resp-top">
                        <strong>{r.status}</strong> — <code>{r.detail}</code>
                      </div>
                      {r.example ? (
                        <pre className="docs__pre docs__pre--compact">{r.example}</pre>
                      ) : null}
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
        <h2>Swagger (OpenAPI 3)</h2>
        <p className="muted">
          Hardcoded OpenAPI stub — control plane + data-plane{' '}
          <code>GET /api/v1/quota</code>.
        </p>
        <pre className="docs__pre cp-docs__yaml">{CP_OPENAPI_YAML}</pre>
      </section>

      <section className="panel">
        <h2>Examples</h2>
        <div className="docs__examples">
          {CP_CURL_EXAMPLES.map((ex) => (
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
