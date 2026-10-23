import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { checkHealth, type HealthState } from '../lib/api'
import {
  getConsoleJob,
  pipelineBuild,
  pipelineDown,
  pipelineUp,
  serviceBuild,
  serviceDown,
  serviceUp,
  type ConsoleJob,
} from '../lib/consoleApi'
import { SERVICES, type ServiceDef } from '../lib/config'

const SERVICE_LIST = Object.values(SERVICES) as ServiceDef[]
const JOB_STORAGE_KEY = 'st-do-asign.pipeline.activeJobIds'

type PipelineContextValue = {
  health: Record<string, HealthState>
  refreshHealth: () => Promise<void>
  jobs: ConsoleJob[]
  activeJobs: ConsoleJob[]
  busyTargets: Set<string>
  lastError: string | null
  clearError: () => void
  runPipelineUp: (rebuild?: boolean) => Promise<void>
  runPipelineDown: () => Promise<void>
  runPipelineBuild: () => Promise<void>
  runServiceUp: (name: string, rebuild?: boolean) => Promise<void>
  runServiceDown: (name: string) => Promise<void>
  runServiceBuild: (name: string) => Promise<void>
  isManaged: (id: string) => boolean
  isBusy: (target: string) => boolean
}

const PipelineContext = createContext<PipelineContextValue | null>(null)

function loadStoredJobIds(): string[] {
  try {
    const raw = sessionStorage.getItem(JOB_STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    return Array.isArray(parsed) ? parsed.filter((x) => typeof x === 'string') : []
  } catch {
    return []
  }
}

function saveStoredJobIds(ids: string[]) {
  sessionStorage.setItem(JOB_STORAGE_KEY, JSON.stringify(ids))
}

export function PipelineProvider({ children }: { children: ReactNode }) {
  const [health, setHealth] = useState<Record<string, HealthState>>({})
  const [jobsById, setJobsById] = useState<Record<string, ConsoleJob>>({})
  const [watching, setWatching] = useState<string[]>(() => loadStoredJobIds())
  const [lastError, setLastError] = useState<string | null>(null)
  const pollRef = useRef<number | null>(null)

  const refreshHealth = useCallback(async () => {
    const next: Record<string, HealthState> = {}
    await Promise.all(
      SERVICE_LIST.map(async (s) => {
        next[s.id] = await checkHealth(
          s.hostUrl,
          s.healthPath,
          'planned' in s && !!s.planned,
        )
      }),
    )
    setHealth(next)
  }, [])

  const upsertJob = useCallback((job: ConsoleJob) => {
    setJobsById((prev) => ({ ...prev, [job.id]: job }))
  }, [])

  const trackJob = useCallback(
    (job: ConsoleJob) => {
      upsertJob(job)
      setWatching((prev) => {
        const next = prev.includes(job.id) ? prev : [...prev, job.id]
        saveStoredJobIds(next)
        return next
      })
    },
    [upsertJob],
  )

  const runAction = useCallback(
    async (fn: () => Promise<ConsoleJob>) => {
      setLastError(null)
      try {
        const job = await fn()
        trackJob(job)
      } catch (e) {
        setLastError(e instanceof Error ? e.message : 'action failed')
      }
    },
    [trackJob],
  )

  // Poll active jobs while any are queued/running — survives route changes.
  useEffect(() => {
    const tick = async () => {
      if (watching.length === 0) return
      const still: string[] = []
      await Promise.all(
        watching.map(async (id) => {
          try {
            const job = await getConsoleJob(id)
            upsertJob(job)
            if (job.status === 'queued' || job.status === 'running') {
              still.push(id)
            }
          } catch {
            // drop missing jobs
          }
        }),
      )
      setWatching((prev) => {
        // keep ids that are still active; drop finished
        const next = prev.filter((id) => still.includes(id))
        if (next.length !== prev.length) saveStoredJobIds(next)
        return next
      })
      // refresh health when something finishes
      if (still.length < watching.length) {
        void refreshHealth()
      }
    }

    void tick()
    pollRef.current = window.setInterval(() => void tick(), 1500)
    return () => {
      if (pollRef.current) window.clearInterval(pollRef.current)
    }
  }, [watching, upsertJob, refreshHealth])

  // Health polling for the whole app (not just Overview).
  useEffect(() => {
    void refreshHealth()
    const id = window.setInterval(() => void refreshHealth(), 5000)
    return () => window.clearInterval(id)
  }, [refreshHealth])

  const jobs = useMemo(
    () => Object.values(jobsById).sort((a, b) => b.startedAt.localeCompare(a.startedAt)),
    [jobsById],
  )

  const activeJobs = useMemo(
    () => jobs.filter((j) => j.status === 'queued' || j.status === 'running'),
    [jobs],
  )

  const busyTargets = useMemo(() => {
    const set = new Set<string>()
    for (const j of activeJobs) {
      set.add(j.target)
      set.add(`${j.action}:${j.target}`)
    }
    return set
  }, [activeJobs])

  const value = useMemo<PipelineContextValue>(
    () => ({
      health,
      refreshHealth,
      jobs,
      activeJobs,
      busyTargets,
      lastError,
      clearError: () => setLastError(null),
      runPipelineUp: (rebuild = true) => runAction(() => pipelineUp(rebuild)),
      runPipelineDown: () => runAction(() => pipelineDown()),
      runPipelineBuild: () => runAction(() => pipelineBuild()),
      runServiceUp: (name, rebuild = false) => runAction(() => serviceUp(name, rebuild)),
      runServiceDown: (name) => runAction(() => serviceDown(name)),
      runServiceBuild: (name) => runAction(() => serviceBuild(name)),
      isManaged: (id) => {
        const s = SERVICE_LIST.find((x) => x.id === id)
        return !!(s && 'managed' in s && s.managed)
      },
      isBusy: (target) =>
        busyTargets.has(target) ||
        busyTargets.has('all') ||
        activeJobs.some((j) => j.target === target || j.target === 'all'),
    }),
    [
      health,
      refreshHealth,
      jobs,
      activeJobs,
      busyTargets,
      lastError,
      runAction,
    ],
  )

  return <PipelineContext.Provider value={value}>{children}</PipelineContext.Provider>
}

export function usePipeline() {
  const ctx = useContext(PipelineContext)
  if (!ctx) throw new Error('usePipeline must be used within PipelineProvider')
  return ctx
}
