import { useEffect, useState } from 'react'
import { api } from '../api'

// Phase 9 — Building tab.
//
// Read-only overview of player-built / player-stored content that's
// off the live world: saved vehicles, recovered (destroyed-but-
// salvageable) vehicles, base backups, building blueprints, building
// favorites, and a per-player rollup of how many things they have
// placed in the world right now.
//
// Single bundle endpoint; sections are rendered as plain tables so an
// operator can scan everything that exists at a glance. Restore /
// recovery actions are deferred until the RMQ envelope contract is
// reverse-engineered (see [[dune-gm-commands]] memory).

type Row = Record<string, unknown>

type Bundle = {
  vehicle_backups: Row[]
  recovered_vehicles: Row[]
  base_backups: Row[]
  building_blueprints: Row[]
  building_favorites: Row[]
  live_builds: Row[]
}

export default function BuildingTab() {
  const [data, setData] = useState<Bundle | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    api
      .get<Bundle>('/building')
      .then((d) => {
        setData(d)
        setErr(null)
      })
      .catch((e) => setErr((e as Error).message))
  }, [])

  if (err) return <div className="alert">{err}</div>
  if (!data) return <div className="placeholder">loading…</div>

  return (
    <div className="stack">
      <Section
        title="Vehicle backups"
        rows={data.vehicle_backups}
        cols={[
          ['owner_player_name', 'Player'],
          ['vehicle_class', 'Vehicle', shortClass],
          ['vehicle_id', 'Vehicle ID'],
          ['account_id', 'Account'],
          ['customization_id', 'Customization'],
        ]}
        empty="No vehicle backups saved."
      />
      <Section
        title="Recovered vehicles"
        rows={data.recovered_vehicles}
        cols={[
          ['owner_player_name', 'Player'],
          ['vehicle_name', 'Name'],
          ['vehicle_class', 'Class', shortClass],
          ['vehicle_id', 'ID'],
          ['chassis_durability', 'Chassis %'],
          ['time_stored', 'Stored at'],
          ['migrated', 'Migrated'],
        ]}
        empty="No vehicles in recovery."
      />
      <Section
        title="Base backups"
        rows={data.base_backups}
        cols={[
          ['owner_player_name', 'Player'],
          ['base_backup_name', 'Name'],
          ['linked_actor_count', 'Actors'],
          ['id', 'ID'],
          ['player_id', 'Owner actor'],
        ]}
        empty="No base backups saved."
      />
      <Section
        title="Building blueprints"
        rows={data.building_blueprints}
        cols={[
          ['owner_player_name', 'Player'],
          ['building_blueprint_map', 'Map'],
          ['instance_count', 'Instances'],
          ['placeable_count', 'Placeables'],
          ['id', 'ID'],
          ['item_id', 'Item ID'],
        ]}
        empty="No saved blueprints."
      />
      <Section
        title="Live builds (placed in the world)"
        rows={data.live_builds}
        cols={[
          ['owner_player_name', 'Player'],
          ['account_id', 'Account'],
          ['building_count', 'Buildings'],
          ['piece_count', 'Pieces'],
          ['placeable_count', 'Free placeables'],
        ]}
        empty="No live buildings or placeables on the server right now."
      />
      <Section
        title="Building favorites (per account)"
        rows={data.building_favorites}
        cols={[
          ['owner_player_name', 'Player'],
          ['account_id', 'Account'],
          ['building_types', 'Favorites', (v) => Array.isArray(v) ? v.join(', ') : String(v ?? '')],
        ]}
        empty="No favorites set."
      />
    </div>
  )
}

type Col = [string, string] | [string, string, (v: unknown) => string]

function Section({
  title,
  rows,
  cols,
  empty,
}: {
  title: string
  rows: Row[]
  cols: Col[]
  empty: string
}) {
  return (
    <div className="card">
      <h3 className="card-title">
        {title} <span className="card-title-count">{rows.length}</span>
      </h3>
      {rows.length === 0 ? (
        <div className="hint">{empty}</div>
      ) : (
        <div className="grid-wrap">
          <table className="grid compact">
            <thead>
              <tr>{cols.map(([, label]) => <th key={label}>{label}</th>)}</tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={i}>
                  {cols.map(([key, label, fmt]) => {
                    const raw = r[key]
                    const text = fmt ? fmt(raw) : raw === null || raw === undefined ? '∅' : String(raw)
                    return (
                      <td key={label} className="mono">
                        {text}
                      </td>
                    )
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// Trim Unreal class paths down for display. Mirrors the helper in
// StorageTab.tsx — duplicated here to keep tab files independent.
function shortClass(v: unknown): string {
  if (typeof v !== 'string' || !v) return '∅'
  let out = v
  const slash = out.lastIndexOf('/')
  if (slash >= 0) out = out.slice(slash + 1)
  const dot = out.indexOf('.')
  if (dot >= 0) out = out.slice(dot + 1)
  out = out.replace(/_C$/, '')
  out = out.replace(/^BP_Dune/, '')
  out = out.replace(/^BP_/, '')
  return out
}
