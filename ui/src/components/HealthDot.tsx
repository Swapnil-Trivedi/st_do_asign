import type { HealthState } from '../lib/api'

const labels: Record<HealthState, string> = {
  healthy: 'Healthy',
  down: 'Down',
  planned: 'Planned',
  checking: 'Checking',
}

export function HealthDot({ state }: { state: HealthState }) {
  return (
    <span className={`health health--${state}`}>
      <span className="health__dot" aria-hidden />
      {labels[state]}
    </span>
  )
}
