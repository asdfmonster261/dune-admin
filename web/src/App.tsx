import { useEffect, useState } from 'react'
import { api, type Status } from './api'
import OverviewTab from './tabs/OverviewTab'
import PlayersTab from './tabs/PlayersTab'
import DatabaseTab from './tabs/DatabaseTab'
import LogsTab from './tabs/LogsTab'
import AuditTab from './tabs/AuditTab'
import AdminActionsTab from './tabs/AdminActionsTab'
import SettingsTab from './tabs/SettingsTab'
import OpsTab from './tabs/OpsTab'
import StorageTab from './tabs/StorageTab'
import BuildingTab from './tabs/BuildingTab'
import MapTab from './tabs/MapTab'
import ExchangeTab from './tabs/ExchangeTab'

type TabId =
  | 'overview'
  | 'players'
  | 'database'
  | 'logs'
  | 'audit'
  | 'admin'
  | 'settings'
  | 'ops'
  | 'storage'
  | 'building'
  | 'map'
  | 'exchange'

type Tab = { id: TabId; label: string; phase: number; description: string }

const TABS: Tab[] = [
  {
    id: 'overview',
    label: 'Overview',
    phase: 2,
    description: 'System health, docker container state, online players, daily peak tracking.',
  },
  {
    id: 'players',
    label: 'Players',
    phase: 3,
    description:
      'Browse characters, view inventory, edit currency / faction / XP, teleport, journey unlocks.',
  },
  {
    id: 'database',
    label: 'Database',
    phase: 3,
    description: 'Browse tables, run read-only SQL, inspect schemas.',
  },
  {
    id: 'logs',
    label: 'Logs',
    phase: 4,
    description: 'Live stream container logs over WebSocket. Cheat-detection feed.',
  },
  {
    id: 'audit',
    label: 'Audit',
    phase: 5,
    description: 'Append-only log of every admin action taken through this panel.',
  },
  {
    id: 'admin',
    label: 'Admin Actions',
    phase: 5,
    description: 'GM command catalog with payload preview + execute.',
  },
  {
    id: 'settings',
    label: 'Settings',
    phase: 6,
    description: '.env edits, INI knobs, director transfer + player-online-state tuning.',
  },
  {
    id: 'ops',
    label: 'Ops',
    phase: 7,
    description: 'Schedule announcements and restarts with player handoff.',
  },
  {
    id: 'storage',
    label: 'Storage',
    phase: 8,
    description: 'Browse server-side storage containers (Spicefield depots, exchanges, caches); give items.',
  },
  {
    id: 'building',
    label: 'Building',
    phase: 9,
    description: 'Player-built content: vehicle backups, base backups, blueprint export/import.',
  },
  {
    id: 'map',
    label: 'Map',
    phase: 10,
    description: 'Hagga Basin POI map + Deep Desert observation overlay.',
  },
  {
    id: 'exchange',
    label: 'Exchange',
    phase: 11,
    description: 'Browse exchange orders, manage artificial economy bots (buyer/seller/populator).',
  },
]

export default function App() {
  const [status, setStatus] = useState<Status | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [active, setActive] = useState<TabId>('overview')
  // Cross-tab navigation: when MapTab is clicked through to a player,
  // App stashes the target player_state_id here and switches tabs.
  // PlayersTab consumes it on mount (and clears it via onConsumed) so
  // re-entering the Players tab later doesn't keep re-selecting the
  // same character.
  const [pendingPlayerId, setPendingPlayerId] = useState<number | null>(null)
  const navigateToPlayer = (playerStateId: number) => {
    setPendingPlayerId(playerStateId)
    setActive('players')
  }

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
    <>
      <header className="topbar">
        <div className="brand">
          <span className="brand-mark" />
          <span className="brand-name">dune-admin</span>
          {status?.battlegroup_ns && <span className="brand-meta">{status.battlegroup_ns}</span>}
        </div>
        <div className="topbar-meta">
          <Pill label="docker" ok={status?.docker_connected} />
          <Pill label="orchestrator" ok={status?.orchestrator_connected} />
          <Pill label="opsbridge" ok={status?.opsbridge_connected} />
          {status && <span className="version">{status.version}</span>}
          {err && <span className="pill bad">{err}</span>}
        </div>
      </header>

      <nav className="tabs">
        {TABS.map((t) => (
          <button
            key={t.id}
            className={`tab ${t.id === active ? 'active' : ''}`}
            onClick={() => setActive(t.id)}
          >
            {t.label}
            <span className="tab-stage">P{t.phase}</span>
          </button>
        ))}
      </nav>

      <main className="content">
        <h1 className="section-title">{tab.label}</h1>
        <p className="section-sub">{tab.description}</p>

        {active === 'overview' && <OverviewTab />}
        {active === 'players' && (
          <PlayersTab
            initialSelectedId={pendingPlayerId}
            onConsumed={() => setPendingPlayerId(null)}
          />
        )}
        {active === 'database' && <DatabaseTab />}
        {active === 'logs' && <LogsTab />}
        {active === 'audit' && <AuditTab />}
        {active === 'admin' && <AdminActionsTab />}
        {active === 'settings' && <SettingsTab />}
        {active === 'ops' && <OpsTab />}
        {active === 'storage' && <StorageTab />}
        {active === 'building' && <BuildingTab />}
        {active === 'map' && <MapTab onPlayerClick={navigateToPlayer} />}
        {active === 'exchange' && <ExchangeTab />}
      </main>
    </>
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
