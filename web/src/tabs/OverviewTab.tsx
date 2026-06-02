import { useEffect, useState } from 'react'
import { api } from '../api'

type HostSummary = {
  name: string
  operating_system: string
  kernel_version: string
  docker_version: string
  ncpu: number
  mem_total: number
}

type ContainerEntry = {
  service: string
  name: string
  state: string
  status: string
  is_game: boolean
  is_core: boolean
  is_on_demand: boolean
}

type PlayerSummary = {
  online: number
  by_map: Record<string, number>
  servers_up: number
}

type PlayerPeakSummary = {
  session_max: number
  session_at?: string
}

type Snapshot = {
  host: HostSummary
  containers: ContainerEntry[]
  players: PlayerSummary
  peak: PlayerPeakSummary
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}

export default function OverviewTab() {
  const [snap, setSnap] = useState<Snapshot | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    const tick = async () => {
      try {
        const s = await api.get<Snapshot>('/overview/snapshot')
        if (live) {
          setSnap(s)
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

  if (err && !snap) {
    return <div className="placeholder">overview failed: {err}</div>
  }
  if (!snap) {
    return <div className="placeholder">loading…</div>
  }

  const containersRunning = snap.containers.filter((c) => c.state === 'running').length
  const containersTotal = snap.containers.length
  const gameRunning = snap.containers.filter((c) => c.is_game && c.state === 'running').length

  const containerGroups = [
    {
      title: 'Core services',
      filter: (c: ContainerEntry) => c.is_core,
    },
    {
      title: 'Always-on game servers',
      filter: (c: ContainerEntry) => c.is_game && !c.is_on_demand,
    },
    {
      title: 'On-demand maps',
      filter: (c: ContainerEntry) => c.is_game && c.is_on_demand,
    },
    {
      title: 'Other',
      filter: (c: ContainerEntry) => !c.is_core && !c.is_game,
    },
  ]

  return (
    <>
      <section className="metric-grid">
        <Metric
          label="Players online"
          value={snap.players.online}
          sub={`peak ${snap.peak.session_max} this session`}
        />
        <Metric
          label="Game servers up"
          value={`${gameRunning}`}
          sub={`${snap.players.servers_up} reporting ready`}
        />
        <Metric
          label="Containers running"
          value={`${containersRunning}/${containersTotal}`}
          sub={`${snap.host.ncpu} cpu`}
        />
        <Metric
          label="Host memory"
          value={formatBytes(snap.host.mem_total)}
          sub={snap.host.kernel_version}
          subMono
        />
      </section>

      {Object.keys(snap.players.by_map).length > 0 && (
        <section className="card">
          <h3 className="card-title">Players by map</h3>
          <div className="kv-grid">
            {Object.entries(snap.players.by_map)
              .sort(([, a], [, b]) => b - a)
              .map(([map, n]) => (
                <div key={map} className="kv">
                  <span className="kv-key">{map}</span>
                  <span className="kv-val">{n}</span>
                </div>
              ))}
          </div>
        </section>
      )}

      {containerGroups.map((group) => {
        const items = snap.containers.filter(group.filter)
        if (items.length === 0) return null
        return (
          <section key={group.title} className="card">
            <h3 className="card-title">
              {group.title} <span className="card-title-count">{items.length}</span>
            </h3>
            <div className="container-grid">
              {items.map((c) => (
                <ContainerCell key={c.name} entry={c} />
              ))}
            </div>
          </section>
        )
      })}
    </>
  )
}

function Metric({
  label,
  value,
  sub,
  subMono,
}: {
  label: string
  value: number | string
  sub?: string
  subMono?: boolean
}) {
  return (
    <div className="metric">
      <div className="metric-label">{label}</div>
      <div className="metric-value">{value}</div>
      {sub && <div className={`metric-sub ${subMono ? 'mono' : ''}`}>{sub}</div>}
    </div>
  )
}

function ContainerCell({ entry }: { entry: ContainerEntry }) {
  const running = entry.state === 'running'
  return (
    <div className={`container-cell ${running ? 'running' : 'stopped'}`} title={entry.status}>
      <div className="container-dot" />
      <div className="container-svc">{entry.service}</div>
      <div className="container-state">{entry.state}</div>
    </div>
  )
}
