import { useEffect, useState } from 'react'
import { api } from '../api'

type Announcement = {
  id: string
  message: string
  run_at: string
  mode: string
  routing: string
  status: string
  created_at: string
  updated_at: string
  error?: string
}

type RestartJob = {
  id: string
  run_at: string
  warn_mins: number
  services: string[]
  status: string
  created_at: string
  updated_at: string
  stopped_at?: string
  started_at?: string
  finished_at?: string
  error?: string
}

export default function OpsTab() {
  const [ann, setAnn] = useState<Announcement[]>([])
  const [rst, setRst] = useState<RestartJob[]>([])
  const [err, setErr] = useState<string | null>(null)

  const reload = () => {
    Promise.all([
      api.get<Announcement[]>('/ops/announcements'),
      api.get<RestartJob[]>('/ops/restarts'),
    ])
      .then(([a, r]) => {
        setAnn(a || [])
        setRst(r || [])
        setErr(null)
      })
      .catch((e) => setErr((e as Error).message))
  }

  useEffect(() => {
    reload()
    const id = setInterval(reload, 8000)
    return () => clearInterval(id)
  }, [])

  return (
    <>
      {err && <div className="alert">{err}</div>}

      <div className="card warn-card">
        <div className="card-title">Announcements: preview only</div>
        <p className="hint">
          The in-game broadcast RMQ payload contract is unverified (same as GM commands). Scheduled
          announcements still appear in the queue and the worker fires at the scheduled time, but
          it audits the would-be envelope instead of publishing.
        </p>
      </div>

      <div className="two-col">
        <AnnouncementsCard
          jobs={ann}
          onCreate={reload}
          onCancel={(id) => api.del(`/ops/announcements/${id}`).then(reload)}
        />
        <RestartsCard
          jobs={rst}
          onCreate={reload}
          onCancel={(id) => api.del(`/ops/restarts/${id}`).then(reload)}
        />
      </div>
    </>
  )
}

function AnnouncementsCard({
  jobs,
  onCreate,
  onCancel,
}: {
  jobs: Announcement[]
  onCreate: () => void
  onCancel: (id: string) => void
}) {
  const [message, setMessage] = useState('')
  const [runAt, setRunAt] = useState(defaultRunAtLocal(15))
  const [submitting, setSubmitting] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    setErr(null)
    try {
      await api.post('/ops/announcements', {
        message,
        run_at: new Date(runAt).toISOString(),
        mode: 'service-message',
        routing: '#',
      })
      setMessage('')
      onCreate()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section className="card">
      <h3 className="card-title">
        Announcements <span className="card-title-count">{jobs.length}</span>
      </h3>
      <form className="ops-form" onSubmit={submit}>
        <label className="field-label">Message</label>
        <input
          className="input wide"
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder="Server restarting in 5 min"
        />
        <label className="field-label">Run at (local)</label>
        <input
          className="input wide"
          type="datetime-local"
          value={runAt}
          onChange={(e) => setRunAt(e.target.value)}
        />
        <div className="actions-row">
          <button className="btn primary" type="submit" disabled={!message || submitting}>
            {submitting ? 'Queuing…' : 'Queue'}
          </button>
        </div>
        {err && <div className="alert">{err}</div>}
      </form>
      <JobList
        jobs={jobs.map((j) => ({
          id: j.id,
          title: j.message,
          subtitle: `→ ${fmtDate(j.run_at)} · ${j.status}`,
          status: j.status,
          error: j.error,
        }))}
        onCancel={onCancel}
      />
    </section>
  )
}

function RestartsCard({
  jobs,
  onCreate,
  onCancel,
}: {
  jobs: RestartJob[]
  onCreate: () => void
  onCancel: (id: string) => void
}) {
  const [runAt, setRunAt] = useState(defaultRunAtLocal(15))
  const [warnMins, setWarnMins] = useState('5')
  const [servicesRaw, setServicesRaw] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSubmitting(true)
    setErr(null)
    try {
      await api.post('/ops/restarts', {
        run_at: new Date(runAt).toISOString(),
        warn_mins: Number(warnMins) || 0,
        services: servicesRaw
          .split(',')
          .map((s) => s.trim())
          .filter(Boolean),
      })
      setServicesRaw('')
      onCreate()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <section className="card">
      <h3 className="card-title">
        Restarts <span className="card-title-count">{jobs.length}</span>
      </h3>
      <form className="ops-form" onSubmit={submit}>
        <label className="field-label">Run at (local)</label>
        <input
          className="input wide"
          type="datetime-local"
          value={runAt}
          onChange={(e) => setRunAt(e.target.value)}
        />
        <label className="field-label">Warn (min before)</label>
        <input
          className="input wide"
          type="number"
          min="0"
          max="60"
          value={warnMins}
          onChange={(e) => setWarnMins(e.target.value)}
        />
        <label className="field-label">Services (comma-separated, blank = all game-server-*)</label>
        <input
          className="input wide"
          value={servicesRaw}
          onChange={(e) => setServicesRaw(e.target.value)}
          placeholder="game-server-survival, game-server-overmap"
        />
        <div className="actions-row">
          <button className="btn primary" type="submit" disabled={submitting}>
            {submitting ? 'Scheduling…' : 'Schedule'}
          </button>
        </div>
        {err && <div className="alert">{err}</div>}
      </form>
      <JobList
        jobs={jobs.map((j) => ({
          id: j.id,
          title: j.services.length ? j.services.join(', ') : 'all game-servers',
          subtitle: `→ ${fmtDate(j.run_at)} · ${j.status}${
            j.warn_mins ? ` · warn ${j.warn_mins}m` : ''
          }`,
          status: j.status,
          error: j.error,
        }))}
        onCancel={onCancel}
      />
    </section>
  )
}

function JobList({
  jobs,
  onCancel,
}: {
  jobs: { id: string; title: string; subtitle: string; status: string; error?: string }[]
  onCancel: (id: string) => void
}) {
  if (jobs.length === 0) {
    return <div className="hint" style={{ marginTop: 12 }}>No scheduled jobs.</div>
  }
  return (
    <div className="job-list">
      {jobs.map((j) => (
        <div key={j.id} className={`job-row status-${j.status}`}>
          <div>
            <div className="job-title">{j.title}</div>
            <div className="job-sub mono">{j.subtitle}</div>
            {j.error && <div className="err-text mono">{j.error}</div>}
          </div>
          {(j.status === 'pending' || j.status === 'warning') && (
            <button className="btn" onClick={() => onCancel(j.id)}>
              Cancel
            </button>
          )}
        </div>
      ))}
    </div>
  )
}

function defaultRunAtLocal(minsAhead: number): string {
  const d = new Date(Date.now() + minsAhead * 60 * 1000)
  // datetime-local needs YYYY-MM-DDTHH:mm without seconds and in local time.
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function fmtDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString(undefined, {
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return iso
  }
}
