import { useEffect, useState } from 'react'
import { api, type Status } from './api'

export default function App() {
  const [status, setStatus] = useState<Status | null>(null)
  const [err, setErr] = useState<string | null>(null)

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

  return (
    <div className="layout">
      <header>
        <h1>dune-admin</h1>
        <div className="meta">
          {status && (
            <>
              <Badge label="docker" ok={status.docker_connected} />
              <Badge label="orchestrator" ok={status.orchestrator_connected} />
              {status.battlegroup_ns && <span className="ns">{status.battlegroup_ns}</span>}
              <span className="version">{status.version}</span>
            </>
          )}
          {err && <span className="err">{err}</span>}
        </div>
      </header>
      <main>
        <div className="placeholder">
          <p>Scaffold ready. Tabs land in the upcoming phases.</p>
          <ul>
            <li>Phase 2 — Overview</li>
            <li>Phase 3 — Players, Database</li>
            <li>Phase 4 — Logs</li>
            <li>Phase 5 — Audit, Admin Actions</li>
            <li>Phase 6 — Settings</li>
            <li>Phase 7 — Ops</li>
            <li>Phase 8 — Niche (Hagga POIs, Deep Desert, Storage/Blueprints/Bases, exchange)</li>
          </ul>
        </div>
      </main>
    </div>
  )
}

function Badge({ label, ok }: { label: string; ok: boolean }) {
  return (
    <span className={`badge ${ok ? 'ok' : 'bad'}`}>
      <span className="dot" />
      {label}
    </span>
  )
}
