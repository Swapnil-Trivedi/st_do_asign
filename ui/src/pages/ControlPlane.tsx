import { PlaneTag } from '../components/PlaneTag'

export function ControlPlane() {
  return (
    <div className="page">
      <header className="page__header">
        <div>
          <div className="page__eyebrow">
            <PlaneTag plane="control" />
          </div>
          <h1>Control Plane</h1>
          <p className="muted">
            Key, path, and upstream registration plus rate-limit policies will land here (MySQL-backed).
          </p>
        </div>
      </header>
      <section className="panel">
        <h2>Coming next</h2>
        <ul className="bullet">
          <li>Register API keys per tenant</li>
          <li>Map paths → upstreams</li>
          <li>CRUD rate-limit rules (burst / sustained, algorithm)</li>
        </ul>
      </section>
    </div>
  )
}
