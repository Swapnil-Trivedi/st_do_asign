import { usePipeline } from '../state/PipelineContext'

export function JobBanner() {
  const { activeJobs, jobs, lastError, clearError } = usePipeline()
  const latest = jobs[0]

  if (!lastError && activeJobs.length === 0 && !latest) return null

  return (
    <div className="job-banner">
      {lastError ? (
        <div className="job-banner__error">
          <span>{lastError}</span>
          <button type="button" className="btn btn--ghost" onClick={clearError}>
            Dismiss
          </button>
        </div>
      ) : null}

      {activeJobs.length > 0 ? (
        <div className="job-banner__active">
          {activeJobs.map((j) => (
            <span key={j.id} className="job-chip job-chip--running">
              {j.action} · {j.target} · {j.status}
            </span>
          ))}
          <span className="muted">Jobs keep running while you navigate.</span>
        </div>
      ) : latest ? (
        <div className="job-banner__idle">
          <span className={`job-chip job-chip--${latest.status}`}>
            last: {latest.action} · {latest.target} · {latest.status}
          </span>
        </div>
      ) : null}
    </div>
  )
}
