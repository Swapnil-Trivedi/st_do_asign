import { HealthDot } from '../components/HealthDot'
import { PlaneTag } from '../components/PlaneTag'
import { SERVICES, TOPOLOGY_EDGES, type ServiceDef } from '../lib/config'
import { usePipeline } from '../state/PipelineContext'

const SERVICE_LIST = Object.values(SERVICES) as ServiceDef[]

export function Overview() {
  const {
    health,
    refreshHealth,
    jobs,
    isManaged,
    isBusy,
    runPipelineUp,
    runPipelineDown,
    runPipelineBuild,
    runServiceUp,
    runServiceDown,
    runServiceBuild,
    lastError,
  } = usePipeline()

  const consoleHealth = health['console-api'] ?? 'checking'
  const consoleDown = consoleHealth === 'down'

  return (
    <div className="page">
      <header className="page__header">
        <div>
          <h1>Overview</h1>
          <p className="muted">
            Control the pipeline from here. Managed services start/stop via <code>console-api</code>{' '}
            (scratch + docker.sock). Health and jobs persist across page navigation.
          </p>
        </div>
        <button type="button" className="btn btn--ghost" onClick={() => void refreshHealth()}>
          Refresh health
        </button>
      </header>

      {consoleDown ? (
        <section className="panel panel--warn">
          <h2>console-api is down</h2>
          <p className="muted">
            Start/stop buttons call <code>http://localhost:8095</code>. Bootstrap the orchestrator once
            from the host, then use the UI for everything else:
          </p>
          <pre className="docs__pre">{`cd st_do_asign
./build/build.sh up`}</pre>
          <p className="muted">
            That starts <code>console-api</code>, <code>mongo</code>, <code>redis</code>,{' '}
            <code>mock-server</code>, and <code>vegeta-client</code> on <code>st_net</code>.
          </p>
        </section>
      ) : null}

      <section className="panel">
        <div className="panel__toolbar">
          <h2>Full pipeline</h2>
          <HealthDot state={consoleHealth} />
        </div>
        <p className="muted" style={{ marginBottom: '0.75rem' }}>
          Managed: <code>mongo</code>, <code>redis</code>, <code>mock-server</code>,{' '}
          <code>vegeta-client</code>, <code>control-plane</code>. Does not stop{' '}
          <code>console-api</code>.
        </p>
        <div className="btn-row">
          <button
            type="button"
            className="btn"
            disabled={consoleDown || isBusy('all')}
            onClick={() => void runPipelineUp(true)}
          >
            Build &amp; start all
          </button>
          <button
            type="button"
            className="btn btn--ghost"
            disabled={consoleDown || isBusy('all')}
            onClick={() => void runPipelineUp(false)}
          >
            Start all
          </button>
          <button
            type="button"
            className="btn btn--ghost"
            disabled={consoleDown || isBusy('all')}
            onClick={() => void runPipelineBuild()}
          >
            Build images
          </button>
          <button
            type="button"
            className="btn btn--danger"
            disabled={consoleDown || isBusy('all')}
            onClick={() => void runPipelineDown()}
          >
            Stop all
          </button>
        </div>
        {lastError ? <p className="error" style={{ marginTop: '0.75rem' }}>{lastError}</p> : null}
      </section>

      <section className="panel">
        <h2>Containers</h2>
        <div className="svc-grid">
          {SERVICE_LIST.map((s) => {
            const managed = isManaged(s.id)
            const infra = 'infra' in s && !!s.infra
            const busy = isBusy(s.id) || isBusy('all')
            const h = health[s.id] ?? 'checking'
            const running = h === 'healthy' || h === 'checking'
            return (
              <article key={s.id} className="svc-card">
                <div className="svc-card__top">
                  <strong>{s.name}</strong>
                  <PlaneTag plane={s.plane} />
                </div>
                <p className="muted">{s.role}</p>
                <HealthDot state={h} />
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

                {managed ? (
                  <div className="svc-card__actions">
                    <button
                      type="button"
                      className="btn"
                      disabled={consoleDown || busy}
                      onClick={() => void runServiceUp(s.id, false)}
                      title="Start or restart container"
                    >
                      {running ? 'Restart' : 'Start'}
                    </button>
                    {infra ? (
                      <button
                        type="button"
                        className="btn btn--ghost"
                        disabled={consoleDown || busy}
                        onClick={() => void runServiceUp(s.id, true)}
                        title="Pull image and start"
                      >
                        Pull &amp; start
                      </button>
                    ) : (
                      <>
                        <button
                          type="button"
                          className="btn btn--ghost"
                          disabled={consoleDown || busy}
                          onClick={() => void runServiceUp(s.id, true)}
                        >
                          Rebuild &amp; start
                        </button>
                        <button
                          type="button"
                          className="btn btn--ghost"
                          disabled={consoleDown || busy}
                          onClick={() => void runServiceBuild(s.id)}
                        >
                          Build
                        </button>
                      </>
                    )}
                    <button
                      type="button"
                      className="btn btn--danger"
                      disabled={consoleDown || busy || h === 'down'}
                      onClick={() => void runServiceDown(s.id)}
                    >
                      Stop
                    </button>
                  </div>
                ) : (
                  <p className="svc-card__note muted">
                    {'planned' in s && s.planned
                      ? 'Planned — not managed yet'
                      : s.id === 'console-api'
                        ? 'Keep running — orchestrates siblings'
                        : 'Not managed from UI'}
                  </p>
                )}
              </article>
            )
          })}
        </div>
      </section>

      <section className="panel">
        <h2>Connectivity</h2>
        <div className="topo">
          {TOPOLOGY_EDGES.map((e) => {
            const fromH = health[e.from] ?? 'checking'
            const toH = health[e.to] ?? 'checking'
            const edgeClass =
              fromH === 'planned' || toH === 'planned'
                ? 'topo__edge--planned'
                : fromH === 'healthy' && toH === 'healthy'
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

      <section className="panel">
        <h2>Recent jobs</h2>
        {jobs.length === 0 ? (
          <p className="muted">No jobs yet. Start/stop a service to see output here.</p>
        ) : (
          <div className="job-list">
            {jobs.slice(0, 8).map((j) => (
              <details key={j.id} className="job-item">
                <summary>
                  <span className={`job-chip job-chip--${j.status}`}>{j.status}</span>
                  <strong>
                    {j.action} · {j.target}
                  </strong>
                  <code>{j.id.slice(0, 8)}</code>
                </summary>
                {j.error ? <p className="error">{j.error}</p> : null}
                <pre className="docs__pre">{j.output || '(no output yet)'}</pre>
              </details>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}
