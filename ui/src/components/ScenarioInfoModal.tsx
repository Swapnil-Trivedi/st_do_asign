import type { VegetaScenario } from '../lib/api'

type Props = {
  scenario: VegetaScenario
  onClose: () => void
  onRun: () => void
  busy?: boolean
}

export function ScenarioInfoModal({ scenario, onClose, onRun, busy }: Props) {
  const req = scenario.request ?? {}
  const target = (req as { target?: Record<string, unknown> }).target ?? {}
  const profile = (req as { profile?: string }).profile ?? '—'
  const steady = (req as { steady?: Record<string, unknown> }).steady
  const ramp = (req as { ramp?: Record<string, unknown> }).ramp
  const burst = (req as { burst?: Record<string, unknown> }).burst

  return (
    <div className="modal-backdrop" role="presentation" onClick={onClose}>
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="scenario-info-title"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal__head">
          <div>
            <p className="modal__eyebrow">Scenario info</p>
            <h2 id="scenario-info-title">{scenario.name}</h2>
          </div>
          <button type="button" className="btn btn--ghost" onClick={onClose}>
            Close
          </button>
        </div>

        <div className="modal__body">
          <section className="modal__block modal__block--intent">
            <h3>Test intent</h3>
            <p>{scenario.intent || scenario.description}</p>
          </section>

          <section className="modal__block">
            <h3>Summary</h3>
            <p className="muted">{scenario.description}</p>
            <dl className="meta-list meta-list--grid" style={{ marginTop: '0.65rem' }}>
              <div>
                <dt>id</dt>
                <dd>
                  <code>{scenario.id}</code>
                </dd>
              </div>
              <div>
                <dt>profile</dt>
                <dd>
                  <code>{profile}</code>
                </dd>
              </div>
              <div>
                <dt>duration</dt>
                <dd>{scenario.durationHint}</dd>
              </div>
              <div>
                <dt>tags</dt>
                <dd>{scenario.tags.join(', ')}</dd>
              </div>
            </dl>
          </section>

          <section className="modal__block">
            <h3>Target</h3>
            <dl className="meta-list">
              <div>
                <dt>baseUrl</dt>
                <dd>
                  <code>{String(target.baseUrl ?? '—')}</code>
                </dd>
              </div>
              <div>
                <dt>method / path</dt>
                <dd>
                  <code>
                    {String(target.method ?? 'GET')} {String(target.path ?? '/')}
                  </code>
                </dd>
              </div>
              <div>
                <dt>headers</dt>
                <dd>
                  <pre className="docs__pre docs__pre--compact">
                    {JSON.stringify(target.headers ?? {}, null, 2)}
                  </pre>
                </dd>
              </div>
              {target.body ? (
                <div>
                  <dt>body</dt>
                  <dd>
                    <pre className="docs__pre docs__pre--compact">{String(target.body)}</pre>
                  </dd>
                </div>
              ) : null}
            </dl>
          </section>

          <section className="modal__block">
            <h3>Load config</h3>
            <pre className="docs__pre">
              {JSON.stringify(steady ?? ramp ?? burst ?? {}, null, 2)}
            </pre>
          </section>

          <section className="modal__block">
            <h3>Full request payload</h3>
            <pre className="docs__pre">{JSON.stringify(scenario.request, null, 2)}</pre>
          </section>
        </div>

        <div className="modal__foot">
          <button type="button" className="btn btn--ghost" onClick={onClose}>
            Close
          </button>
          <button type="button" className="btn" disabled={busy} onClick={onRun}>
            Run this scenario
          </button>
        </div>
      </div>
    </div>
  )
}
