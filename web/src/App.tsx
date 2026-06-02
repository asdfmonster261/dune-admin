import { useEffect, useState } from 'react'
import { api, type Status } from './api'

type TabId =
  | 'overview'
  | 'players'
  | 'database'
  | 'logs'
  | 'audit'
  | 'admin'
  | 'settings'
  | 'ops'
  | 'niche'

type Tab = { id: TabId; label: string; phase: number; ready: boolean }

const TABS: Tab[] = [
  { id: 'overview', label: 'Overview', phase: 2, ready: false },
  { id: 'players', label: 'Players', phase: 3, ready: false },
  { id: 'database', label: 'Database', phase: 3, ready: false },
  { id: 'logs', label: 'Logs', phase: 4, ready: false },
  { id: 'audit', label: 'Audit', phase: 5, ready: false },
  { id: 'admin', label: 'Admin Actions', phase: 5, ready: false },
  { id: 'settings', label: 'Settings', phase: 6, ready: false },
  { id: 'ops', label: 'Ops', phase: 7, ready: false },
  { id: 'niche', label: 'Niche', phase: 8, ready: false },
]

export default function App() {
  const [status, setStatus] = useState<Status | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [active, setActive] = useState<TabId>('overview')

  useEffect(() => {
    let live = true
    const tick = async () => {
      try {
        const s = await api.get<Status>('/status')
        if (live) {
          setStatus(s)
          setErr(null)
        }
      } catch (e) {
        if (live) setErr((e as Error).message)
      }
    }
    tick()
    const id = setInterval(tick, 5000)
    return () => {
      live = false
      clearInterval(id)
    }
  }, [])

  const tab = TABS.find((t) => t.id === active)!

  return (
    <div className="layout">
      <header className="topbar">
        <div className="brand">
          <span className="name">dune-admin</span>
          {status?.battlegroup_ns && <span className="subtle">{status.battlegroup_ns}</span>}
        </div>
        <div className="metaRow">
          <Pill label="docker" ok={status?.docker_connected} />
          <Pill label="orchestrator" ok={status?.orchestrator_connected} />
          {status && <span className="mono">{status.version}</span>}
          {err && <span className="pill bad">{err}</span>}
        </div>
      </header>

      <div className="shell">
        <nav className="sidenav">
          <h3>Sections</h3>
          {TABS.map((t) => (
            <button
              key={t.id}
              className={`tab ${t.id === active ? 'active' : ''}`}
              onClick={() => setActive(t.id)}
            >
              {t.label}
              <span className="stage">P{t.phase}</span>
            </button>
          ))}
        </nav>

        <main className="content">
          <div className="card">
            <h3 style={{ marginTop: 0, color: 'var(--text)', textTransform: 'none', letterSpacing: 0 }}>
              {tab.label}
            </h3>
            <p className="placeholder">
              Lands in <code>Phase {tab.phase}</code>. Phase 1 is the scaffold — the badges above
              show that the backend can talk to docker and the orchestrator. Other tabs come
              online as their phases ship.
            </p>
          </div>
        </main>
      </div>
    </div>
  )
}

function Pill({ label, ok }: { label: string; ok: boolean | undefined }) {
  const cls = ok === undefined ? '' : ok ? 'ok' : 'bad'
  return (
    <span className={`pill ${cls}`}>
      <span className="dot" />
      {label}
    </span>
  )
}
