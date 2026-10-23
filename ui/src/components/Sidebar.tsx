import { NavLink } from 'react-router-dom'
import { PlaneTag } from './PlaneTag'

const links: {
  to: string
  label: string
  hint: string
  plane: null | 'control' | 'data'
}[] = [
  { to: '/', label: 'Overview', hint: 'Compose topology', plane: null },
  { to: '/rate-limiter', label: 'Rate Limiter', hint: 'Live obs · policies', plane: 'control' },
  { to: '/testing', label: 'Testing', hint: 'CP + DP suites', plane: null },
  { to: '/e2e', label: 'E2E', hint: 'Stack · Vegeta · concurrency', plane: 'data' },
  { to: '/mock-backend', label: 'Mock Backend', hint: 'Logs & API docs', plane: 'data' },
  { to: '/vegeta', label: 'Vegeta Client', hint: 'Load profiles', plane: 'data' },
]

export function Sidebar() {
  return (
    <aside className="sidebar">
      <div className="sidebar__brand">
        <div className="sidebar__title">Rate Limiter</div>
        <div className="sidebar__sub">Console</div>
      </div>

      <nav className="sidebar__nav">
        {links.map((l) => (
          <NavLink
            key={l.to}
            to={l.to}
            end={l.to === '/'}
            className={({ isActive }) =>
              `sidebar__link${isActive ? ' sidebar__link--active' : ''}`
            }
          >
            <div className="sidebar__link-row">
              <span>{l.label}</span>
              {l.plane ? <PlaneTag plane={l.plane} /> : null}
            </div>
            <small>{l.hint}</small>
          </NavLink>
        ))}
      </nav>

      <div className="sidebar__foot">
        <span className="plane-tag plane-tag--control">Control</span>
        <span className="plane-tag plane-tag--data">Data</span>
      </div>
    </aside>
  )
}
