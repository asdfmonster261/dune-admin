import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'

// Phase 10 — Map tab (Hagga Basin).
//
// Base image is the stitched 8192² gaming.tools texture, served from
// /maps/hagga.png by the embedded SPA. Live player positions come from
// /api/v1/map/players and project onto the texture via the affine
// constants the backend ships in the same payload.
//
// POI data is also gaming.tools — 3,425 deduped placements baked into
// /maps/hagga_pois.json at build time, with icons mirrored to
// /maps/icons/<iconId>.webp so the page never depends on their CDN
// being up. Sidebar mimics gaming.tools' filter UX: one row per icon
// type with the count + a checkbox-style toggle.

type Player = {
  actor_id: number
  account_id: number
  player_state_id: number
  character_name: string
  online_status: string
  last_login_time: string
  world_x: number
  world_y: number
  world_z: number
  partition_id: number
}

type MapTabProps = {
  // Bubbles up to App when an operator clicks a player dot. App switches
  // active tab to Players and stashes the id for PlayersTab to consume.
  onPlayerClick?: (playerStateId: number) => void
}

type Projection = {
  world_x_min: number
  world_x_max: number
  world_y_min: number
  world_y_max: number
  texture_size: number
  flip_y: boolean
}

type Death = {
  account_id: number
  character_name: string
  world_x: number
  world_y: number
  world_z: number
}

type Building = {
  actor_id: number
  partition_id: number
  world_x: number
  world_y: number
  world_z: number
  owner_player_name: string | null
  owner_account_id: number | null
}

type Vehicle = {
  actor_id: number
  partition_id: number
  class: string
  vehicle_type: string
  world_x: number
  world_y: number
  world_z: number
  owner_player_name: string | null
  owner_account_id: number | null
}

type Storm = {
  spawn_time: string
  start_x: number
  start_y: number
  end_x: number
  end_y: number
  lifetime_seconds: number
  map: string
}

type StormSnapshot = {
  active: Storm[]
  next_scheduled_at: string | null
  blackout_start: string | null
  blackout_end: string | null
  coriolis_cycle_start: string | null
  coriolis_cycle_end: string | null
  coriolis_world_seed: number | null
}

type MapBundle = {
  players: Player[]
  deaths: Death[]
  buildings: Building[]
  vehicles: Vehicle[]
  storms: StormSnapshot
  projection: Projection
}

// Layer toggle ids for the live overlay rows. Stored separately from
// the POI hidden-set so "hide all" on the POI side doesn't sweep them
// away (and vice versa).
type LayerId = 'players' | 'deaths' | 'buildings' | 'vehicles' | 'storms'

type IconType = { id: string; label: string; count: number }
type Group = { id: string; label: string; icons: IconType[] }
type Poi = { x: number; y: number; name: string; icon: string }
type PoiBundle = { groups: Group[]; pois: Poi[] }

export default function MapTab({ onPlayerClick }: MapTabProps = {}) {
  const [data, setData] = useState<MapBundle | null>(null)
  const [pois, setPois] = useState<PoiBundle | null>(null)
  // Set of icon ids whose POIs should be HIDDEN. Empty = all visible.
  const [hidden, setHidden] = useState<Set<string>>(new Set())
  // Player / death / building layers — separate from POI icons so the
  // existing "hide all POIs" action doesn't sweep them away.
  const [hiddenLayers, setHiddenLayers] = useState<Set<LayerId>>(new Set())
  const [err, setErr] = useState<string | null>(null)

  // Pan + zoom state (CSS transform on the map layer)
  const [scale, setScale] = useState(0.15)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  // Wall-clock tick for smooth storm wall interpolation between 3s polls.
  // 1s cadence is plenty — a 5-min sweep across the map moves ~27 px/s
  // at the default 0.15 zoom, well within human-eye resolution.
  const [now, setNow] = useState(() => Date.now())
  const dragRef = useRef<{ x: number; y: number } | null>(null)
  const [viewportEl, setViewportEl] = useState<HTMLDivElement | null>(null)

  // Poll players every 3 s.
  useEffect(() => {
    let cancelled = false
    const tick = () => {
      api
        .get<MapBundle>('/map/players')
        .then((d) => {
          if (cancelled) return
          setData(d)
          setErr(null)
        })
        .catch((e) => {
          if (!cancelled) setErr((e as Error).message)
        })
    }
    tick()
    const id = setInterval(tick, 3000)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [])

  // 1 s wall-clock tick so the storm wall slides smoothly between polls.
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  // Load static POIs once at mount.
  useEffect(() => {
    let cancelled = false
    fetch('/maps/hagga_pois.json')
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.statusText))))
      .then((p: PoiBundle) => {
        if (!cancelled) setPois(p)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  // Project world coords to texture pixel coords. Shared between players
  // and POIs — both come from the same UE5 world frame.
  const project = useMemo(() => {
    return (wx: number, wy: number) => {
      if (!data) return null
      const proj = data.projection
      const tx =
        ((wx - proj.world_x_min) / (proj.world_x_max - proj.world_x_min)) *
        proj.texture_size
      let ty =
        ((wy - proj.world_y_min) / (proj.world_y_max - proj.world_y_min)) *
        proj.texture_size
      if (proj.flip_y) ty = proj.texture_size - ty
      return { x: tx, y: ty }
    }
  }, [data])

  // Wheel zoom anchored at cursor. Attached via useEffect with
  // { passive: false } because React's synthetic onWheel is passive
  // by default and preventDefault() would be a no-op (page would scroll
  // through us).
  useEffect(() => {
    if (!viewportEl) return
    const handler = (e: WheelEvent) => {
      e.preventDefault()
      const factor = e.deltaY < 0 ? 1.15 : 1 / 1.15
      const rect = viewportEl.getBoundingClientRect()
      const cx = e.clientX - rect.left
      const cy = e.clientY - rect.top
      setScale((oldScale) => {
        const newScale = clamp(oldScale * factor, 0.05, 4)
        const real = newScale / oldScale
        setPan((p) => ({
          x: cx - (cx - p.x) * real,
          y: cy - (cy - p.y) * real,
        }))
        return newScale
      })
    }
    viewportEl.addEventListener('wheel', handler, { passive: false })
    return () => viewportEl.removeEventListener('wheel', handler)
  }, [viewportEl])

  const onMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
    dragRef.current = { x: e.clientX - pan.x, y: e.clientY - pan.y }
  }
  const onMouseMove = (e: React.MouseEvent<HTMLDivElement>) => {
    if (!dragRef.current) return
    setPan({ x: e.clientX - dragRef.current.x, y: e.clientY - dragRef.current.y })
  }
  const onMouseUp = () => {
    dragRef.current = null
  }

  // POI icons sized so they're roughly constant on screen — 16 px display
  // by default. Scaled with the SVG transform via the same /scale trick
  // we use for the player dots.
  const POI_PX = 16

  // Pre-bucket visible POIs once per render so the SVG mapping stays
  // simple and we don't recompute the hidden-set check inside the JSX.
  const visiblePois = useMemo(() => {
    if (!pois) return []
    return pois.pois.filter((p) => !hidden.has(p.icon))
  }, [pois, hidden])

  // Flat list of icon ids so the global counter / show-all / hide-all
  // can operate without walking the group tree each time. MUST live
  // above the early-return guards below — calling a hook conditionally
  // breaks the rules of hooks and React renders nothing.
  const allIconIds = useMemo(
    () => (pois ? pois.groups.flatMap((g) => g.icons.map((i) => i.id)) : []),
    [pois],
  )

  if (err) return <div className="alert">{err}</div>
  if (!data) return <div className="placeholder">loading…</div>

  const tex = data.projection.texture_size
  const allHidden = allIconIds.length > 0 && hidden.size === allIconIds.length
  const showAll = () => setHidden(new Set())
  const hideAll = () => setHidden(new Set(allIconIds))

  // Toggle every icon in a group at once. If any in the group is
  // currently visible, hide them all; otherwise show them all.
  const toggleGroup = (g: Group) => {
    setHidden((prev) => {
      const next = new Set(prev)
      const anyVisible = g.icons.some((i) => !next.has(i.id))
      if (anyVisible) g.icons.forEach((i) => next.add(i.id))
      else g.icons.forEach((i) => next.delete(i.id))
      return next
    })
  }

  return (
    <div className="map-wrap">
      <div className="map-body">
        {/* Sidebar: server-side layers (players/deaths/buildings) on
            top, then POI groups → icon rows mirroring gaming.tools'
            Filters panel. Click a group header to toggle its whole set;
            click an individual row to toggle just that one. */}
        {pois && (
          <aside className="map-sidebar">
            <div className="map-sidebar-actions">
              <button type="button" className="map-link" onClick={showAll}>
                show all
              </button>
              <span className="hint">·</span>
              <button type="button" className="map-link" onClick={hideAll}>
                hide all
              </button>
            </div>
            <div className="map-icon-list">
              {/* Live layers — kept above POIs since they're admin-
                  oriented and tend to be looked at first. */}
              <section className="map-group">
                <header className="map-group-header" style={{ cursor: 'default' }}>
                  <span className="map-group-label">Live data</span>
                </header>
                <ul className="map-group-rows">
                  {(
                    [
                      { id: 'players', label: 'Players', count: data.players.length, dot: '#22c55e' },
                      { id: 'vehicles', label: 'Vehicles', count: data.vehicles.length, dot: '#06b6d4' },
                      { id: 'storms', label: 'Sandstorms', count: data.storms?.active.length ?? 0, dot: '#f59e0b' },
                      { id: 'deaths', label: 'Recent deaths', count: data.deaths.length, dot: '#ef4444' },
                      { id: 'buildings', label: 'Land claims', count: data.buildings.length, dot: '#8b5cf6' },
                    ] as { id: LayerId; label: string; count: number; dot: string }[]
                  ).map((it) => {
                    const off = hiddenLayers.has(it.id)
                    return (
                      <li
                        key={it.id}
                        className={`map-icon-row ${off ? 'is-off' : ''}`}
                        onClick={() => {
                          setHiddenLayers((prev) => {
                            const next = new Set(prev)
                            if (next.has(it.id)) next.delete(it.id)
                            else next.add(it.id)
                            return next
                          })
                        }}
                        title={off ? 'show' : 'hide'}
                      >
                        <span className="map-layer-dot" style={{ background: it.dot }} />
                        <span className="map-icon-label">{it.label}</span>
                        <span className="map-icon-count">{it.count}</span>
                      </li>
                    )
                  })}
                </ul>
                <StormSchedule storms={data.storms} now={now} />
              </section>
              {pois.groups.map((g) => {
                const groupVisible = g.icons.filter((i) => !hidden.has(i.id))
                const allOff = groupVisible.length === 0
                const someOff = groupVisible.length < g.icons.length
                const totalCount = g.icons.reduce((s, i) => s + i.count, 0)
                return (
                  <section
                    key={g.id}
                    className={`map-group ${allOff ? 'is-off' : ''}`}
                  >
                    <header
                      className="map-group-header"
                      onClick={() => toggleGroup(g)}
                      title={allOff ? 'show group' : 'hide group'}
                    >
                      <span className="map-group-label">{g.label}</span>
                      <span className="map-group-count">
                        {someOff && !allOff ? '·' : ''}
                        {totalCount}
                      </span>
                    </header>
                    <ul className="map-group-rows">
                      {g.icons.map((it) => {
                        if (it.count === 0) return null
                        const off = hidden.has(it.id)
                        return (
                          <li
                            key={it.id}
                            className={`map-icon-row ${off ? 'is-off' : ''}`}
                            onClick={(e) => {
                              e.stopPropagation()
                              setHidden((prev) => {
                                const next = new Set(prev)
                                if (next.has(it.id)) next.delete(it.id)
                                else next.add(it.id)
                                return next
                              })
                            }}
                            title={off ? 'show' : 'hide'}
                          >
                            <img
                              src={`/maps/icons/${it.id}.webp`}
                              width={18}
                              height={18}
                              alt=""
                            />
                            <span className="map-icon-label">{it.label}</span>
                            <span className="map-icon-count">{it.count}</span>
                          </li>
                        )
                      })}
                    </ul>
                  </section>
                )
              })}
            </div>
          </aside>
        )}

        {/* Map */}
        <div
          ref={setViewportEl}
          className="map-viewport"
          onMouseDown={onMouseDown}
          onMouseMove={onMouseMove}
          onMouseUp={onMouseUp}
          onMouseLeave={onMouseUp}
        >
          <div
            className="map-layer"
            style={{
              width: tex,
              height: tex,
              transform: `translate(${pan.x}px, ${pan.y}px) scale(${scale})`,
              transformOrigin: '0 0',
            }}
          >
            <img
              src="/maps/hagga.png"
              width={tex}
              height={tex}
              draggable={false}
              alt="Hagga Basin"
            />
            <svg
              width={tex}
              height={tex}
              className="map-overlay"
              viewBox={`0 0 ${tex} ${tex}`}
            >
              {/* Layer paint order, bottom → top:
                    1. POI icons (static, lowest priority)
                    2. Sandstorm path + active wall (amber sweep)
                    3. Land-claim totems (purple diamond)
                    4. Vehicles (cyan triangle)
                    5. Recent death markers (red X)
                    6. Live players (always on top so you can find them) */}
              {visiblePois.map((poi, i) => {
                const pos = project(poi.x, poi.y)
                if (!pos) return null
                const size = POI_PX / scale
                return (
                  <image
                    key={`poi-${i}`}
                    href={`/maps/icons/${poi.icon}.webp`}
                    x={pos.x - size / 2}
                    y={pos.y - size / 2}
                    width={size}
                    height={size}
                  >
                    <title>{poi.name}</title>
                  </image>
                )
              })}
              {!hiddenLayers.has('storms') &&
                data.storms?.active.map((s, i) => {
                  const startPx = project(s.start_x, s.start_y)
                  const endPx = project(s.end_x, s.end_y)
                  if (!startPx || !endPx) return null
                  const spawnMs = Date.parse(s.spawn_time)
                  // Clamp progress so a storm whose lifetime just ticked
                  // past stays at the end position until the next poll
                  // drops it from the active list.
                  const t = clamp(
                    (now - spawnMs) / (s.lifetime_seconds * 1000),
                    0,
                    1,
                  )
                  const curX = startPx.x + (endPx.x - startPx.x) * t
                  const curY = startPx.y + (endPx.y - startPx.y) * t
                  // Perpendicular wall extending either side of the sweep
                  // line — half-length scales with the sweep length so
                  // long sweeps get a long wall, short ones stay tight.
                  const dx = endPx.x - startPx.x
                  const dy = endPx.y - startPx.y
                  const len = Math.hypot(dx, dy) || 1
                  const nx = -dy / len
                  const ny = dx / len
                  const half = len * 0.35
                  const wallX1 = curX + nx * half
                  const wallY1 = curY + ny * half
                  const wallX2 = curX - nx * half
                  const wallY2 = curY - ny * half
                  return (
                    <g key={`storm-${i}`} style={{ pointerEvents: 'none' }}>
                      {/* Faint full sweep path so the trajectory is
                          legible even after the wall slides past you. */}
                      <line
                        x1={startPx.x}
                        y1={startPx.y}
                        x2={endPx.x}
                        y2={endPx.y}
                        stroke="#f59e0b"
                        strokeWidth={4 / scale}
                        opacity={0.25}
                        strokeDasharray={`${10 / scale} ${6 / scale}`}
                      />
                      {/* Active wall: bright perpendicular bar at the
                          interpolated current position. */}
                      <line
                        x1={wallX1}
                        y1={wallY1}
                        x2={wallX2}
                        y2={wallY2}
                        stroke="#f59e0b"
                        strokeWidth={14 / scale}
                        strokeLinecap="round"
                        opacity={0.85}
                      />
                      {/* Direction indicator at the current center so the
                          viewer can tell which way the wall is heading. */}
                      <circle
                        cx={curX}
                        cy={curY}
                        r={6 / scale}
                        fill="#fff"
                        stroke="#92400e"
                        strokeWidth={1 / scale}
                      />
                    </g>
                  )
                })}
              {!hiddenLayers.has('buildings') &&
                data.buildings.map((b) => {
                  const pos = project(b.world_x, b.world_y)
                  if (!pos) return null
                  const r = 6 / scale
                  // Purple diamond per totem; rotate 45° via a polygon.
                  return (
                    <g key={`b-${b.actor_id}`} transform={`translate(${pos.x}, ${pos.y})`}>
                      <polygon
                        points={`0,${-r} ${r},0 0,${r} ${-r},0`}
                        fill="#8b5cf6"
                        stroke="#000"
                        strokeWidth={1 / scale}
                        opacity={0.9}
                      >
                        <title>
                          {b.owner_player_name
                            ? `Claim — ${b.owner_player_name}`
                            : `Claim — unowned (#${b.actor_id})`}
                        </title>
                      </polygon>
                    </g>
                  )
                })}
              {!hiddenLayers.has('vehicles') &&
                data.vehicles.map((v) => {
                  const pos = project(v.world_x, v.world_y)
                  if (!pos) return null
                  const r = 6 / scale
                  // Cyan upward-pointing triangle so vehicles read
                  // distinct from totems (diamond) and POIs (icons).
                  return (
                    <g key={`v-${v.actor_id}`} transform={`translate(${pos.x}, ${pos.y})`}>
                      <polygon
                        points={`0,${-r} ${r},${r * 0.7} ${-r},${r * 0.7}`}
                        fill="#06b6d4"
                        stroke="#000"
                        strokeWidth={1 / scale}
                        opacity={0.9}
                      >
                        <title>
                          {prettyVehicleType(v.vehicle_type)}
                          {v.owner_player_name ? ` — ${v.owner_player_name}` : ''}
                        </title>
                      </polygon>
                    </g>
                  )
                })}
              {!hiddenLayers.has('deaths') &&
                data.deaths.map((d) => {
                  const pos = project(d.world_x, d.world_y)
                  if (!pos) return null
                  const r = 7 / scale
                  // Two-stroke red X. paint-order=stroke gives the black
                  // halo without a separate shape.
                  return (
                    <g key={`d-${d.account_id}`} transform={`translate(${pos.x}, ${pos.y})`}>
                      <path
                        d={`M${-r},${-r} L${r},${r} M${-r},${r} L${r},${-r}`}
                        stroke="#ef4444"
                        strokeWidth={2.5 / scale}
                        fill="none"
                      >
                        <title>Death — {d.character_name}</title>
                      </path>
                    </g>
                  )
                })}
              {!hiddenLayers.has('players') &&
                data.players.map((p) => {
                  const pos = project(p.world_x, p.world_y)
                  if (!pos) return null
                  const online = p.online_status === 'Online'
                  const clickable = onPlayerClick != null
                  // Drag-vs-click: the viewport's onMouseDown stashes the
                  // pan-start cursor, so distinguishing a click from a
                  // drag here is unreliable. Instead we stop propagation
                  // on mouseDown so a click on a dot doesn't start a
                  // pan, and treat onClick as the navigation trigger.
                  return (
                    <g
                      key={p.actor_id}
                      transform={`translate(${pos.x}, ${pos.y})`}
                      style={{
                        cursor: clickable ? 'pointer' : undefined,
                        // Re-enable hit testing the parent SVG turns off.
                        pointerEvents: clickable ? 'auto' : undefined,
                      }}
                      onMouseDown={
                        clickable
                          ? (e) => {
                              e.stopPropagation()
                            }
                          : undefined
                      }
                      onClick={
                        clickable
                          ? (e) => {
                              e.stopPropagation()
                              onPlayerClick!(p.player_state_id)
                            }
                          : undefined
                      }
                    >
                      <circle
                        r={8 / scale}
                        fill={online ? '#22c55e' : '#94a3b8'}
                        stroke="#000"
                        strokeWidth={1.5 / scale}
                        opacity={online ? 0.95 : 0.55}
                      />
                      <text
                        y={-(12 / scale)}
                        textAnchor="middle"
                        fontSize={12 / scale}
                        fill="#fff"
                        stroke="#000"
                        strokeWidth={2 / scale}
                        paintOrder="stroke"
                      >
                        {p.character_name}
                      </text>
                    </g>
                  )
                })}
            </svg>
          </div>
        </div>
      </div>
      <div className="map-legend">
        <span>
          {data.players.length} players · {visiblePois.length} of{' '}
          {pois?.pois.length ?? 0} POIs visible
          {allHidden && ' (all categories hidden)'}
        </span>
        <span className="hint">scroll to zoom · drag to pan · ½⅔ click a row to toggle</span>
      </div>
    </div>
  )
}

function clamp(n: number, lo: number, hi: number) {
  return Math.max(lo, Math.min(hi, n))
}

// Compact schedule widget under the Live data layer toggles. Shows the
// next sandstorm countdown, the active blackout window if any, and the
// current Coriolis cycle end. All values come from the game-server log
// tailer in storm_tailer.go.
function StormSchedule({ storms, now }: { storms: StormSnapshot; now: number }) {
  if (!storms) return null
  const next = storms.next_scheduled_at ? Date.parse(storms.next_scheduled_at) : null
  const cycleEnd = storms.coriolis_cycle_end ? Date.parse(storms.coriolis_cycle_end) : null
  const blackoutStart = storms.blackout_start ? Date.parse(storms.blackout_start) : null
  const blackoutEnd = storms.blackout_end ? Date.parse(storms.blackout_end) : null
  const inBlackout =
    blackoutStart != null && blackoutEnd != null && now >= blackoutStart && now < blackoutEnd

  return (
    <div className="map-storm-schedule">
      {next != null && (
        <div className="map-storm-line">
          <span className="map-storm-label">next storm</span>
          <span className="map-storm-value mono">{formatCountdown(next - now)}</span>
        </div>
      )}
      {inBlackout && (
        <div className="map-storm-line">
          <span className="map-storm-label">blackout ends</span>
          <span className="map-storm-value mono">{formatCountdown(blackoutEnd! - now)}</span>
        </div>
      )}
      {cycleEnd != null && (
        <div className="map-storm-line">
          <span className="map-storm-label">coriolis</span>
          <span className="map-storm-value mono">{formatCountdown(cycleEnd - now)}</span>
        </div>
      )}
    </div>
  )
}

function formatCountdown(ms: number): string {
  if (ms < 0) return '—'
  const total = Math.floor(ms / 1000)
  const d = Math.floor(total / 86400)
  const h = Math.floor((total % 86400) / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

// Vehicle classes ship as e.g. `BP_LightOrnithopter_Choam` — trim the
// `BP_` prefix, split CamelCase into words, and split out the faction
// trailer so the tooltip reads "Light Ornithopter (CHOAM)".
function prettyVehicleType(s: string): string {
  let v = s.replace(/^BP_/, '')
  const factions = ['Choam', 'Atreides', 'Harkonnen', 'Fremen', 'House']
  let faction = ''
  for (const f of factions) {
    const m = v.match(new RegExp(`_${f}$`))
    if (m) {
      faction = f.toUpperCase()
      v = v.slice(0, m.index)
      break
    }
  }
  // Insert a space before capital letters that follow lower-case ones.
  v = v.replace(/([a-z])([A-Z])/g, '$1 $2').replace(/_/g, ' ')
  return faction ? `${v} (${faction})` : v
}
