import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'

// Phase 10 — Map tab (Hagga Basin + Deep Desert).
//
// Base image is the stitched 8192² gaming.tools texture, served from
// /maps/hagga.webp or /maps/dd_layout_NN.webp by the embedded SPA. Live
// player positions come from /api/v1/map/players and project onto the
// Hagga texture via the affine constants the backend ships in the same
// payload. DD uses fixed world bounds (±1,219,395 cm) and doesn't
// surface live data — the underlying gameplay world is the same engine
// instance as Hagga, so live overlays would project onto the wrong
// texture if shown while in DD mode.
//
// POI data is gaming.tools — Hagga's 3,425 deduped placements baked
// into /maps/hagga_pois.json, plus 12 per-layout DD bundles at
// /maps/dd_layout_NN_pois.json. Icons mirrored to /maps/icons/<iconId>
// .webp so the page never depends on their CDN being up. Sidebar mimics
// gaming.tools' filter UX: one row per icon type with the count + a
// checkbox-style toggle.
//
// DD layout selection: auto-tracks current week via the
// `coriolis_world_seed` field from storm_tailer.go; can be manually
// overridden via the layout dropdown to inspect any of the 12 layouts.

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

type Respawn = {
  actor_id: number
  group_type: string
  world_x: number
  world_y: number
  owners: string[] | null
}

type MapBundle = {
  players: Player[]
  deaths: Death[]
  buildings: Building[]
  vehicles: Vehicle[]
  respawns: Respawn[]
  storms: StormSnapshot
  projection: Projection
}

// Layer toggle ids for the live overlay rows. Stored separately from
// the POI hidden-set so "hide all" on the POI side doesn't sweep them
// away (and vice versa).
type LayerId = 'players' | 'deaths' | 'buildings' | 'vehicles' | 'storms' | 'respawns'

type IconType = { id: string; label: string; count: number }
type Group = { id: string; label: string; icons: IconType[] }
type Poi = { x: number; y: number; name: string; icon: string }
type PoiBundle = { groups: Group[]; pois: Poi[] }

// DD resource bundles (gaming.tools' spawner dumps, ~30k entries per
// layout). Lazy-loaded — see resourceBundleUrl + the conditional fetch.
type ResourceFamily = {
  id: string
  label: string
  color: string
  count: number
  types: { id: string; count: number }[]
}
type ResourceNode = { x: number; y: number; f: string; t: string }
type ResourceBundle = { families: ResourceFamily[]; nodes: ResourceNode[] }

type MapMode = 'hagga' | 'dd'

// DD resource heatmap layers. Per-layout files at
// /maps/dd_layout_NN_heat_<id>.png cover the 1024² density field
// stretched to the same gameBounds as the base layout.
type DdHeatId = 'T6ResourceA' | 'T6ResourceB'
const DD_HEAT_LAYERS: { id: DdHeatId; label: string; color: string }[] = [
  { id: 'T6ResourceA', label: 'Titanium',   color: '#dca53c' },
  { id: 'T6ResourceB', label: 'Stravidium', color: '#aadc82' },
]

// Fixed projection for Deep Desert. The 12 layouts share the same world
// frame and texture size — only POI placements + terrain change per
// seed. Bounds come from gaming.tools' own map config (their
// `deepdesert_1` entry in chunks/w9533npS.js): asymmetric extents with
// the playable grid offset roughly 50k cm toward -x/-y of centre.
//
// flip_y stays false here even though gaming.tools sets
// transformType:"flipVertical" — their flip lives downstream of a
// Mercator projection, which (after both transforms) lands world y_max
// at the SOUTH (bottom) of the screen. We don't run the Mercator step,
// so a plain affine without a flip reproduces the same orientation:
// high world y goes to high texture y. The empirical check is the
// shield wall + the named "Wreck of the *" markers at y≈+1,000,000;
// both must land at the bottom of the texture and they do without a
// flip but not with one.
const DD_PROJECTION: Projection = {
  world_x_min: -1_270_000,
  world_x_max: 1_168_400,
  world_y_min: -1_270_000,
  world_y_max: 1_168_400,
  texture_size: 8192,
  flip_y: false,
}

// 9×9 sector grid (A1..I9) spans the entire DD texture, matching
// gaming.tools' grid which uses the full gameBounds rather than the
// in-game ±1.08M playable grid. Using gameBounds here keeps every POI
// inside a labelled sector instead of letting sand-border markers spill
// outside the grid lines.
const DD_GRID_X_MIN = DD_PROJECTION.world_x_min
const DD_GRID_X_MAX = DD_PROJECTION.world_x_max
const DD_GRID_Y_MIN = DD_PROJECTION.world_y_min
const DD_GRID_Y_MAX = DD_PROJECTION.world_y_max

export default function MapTab({ onPlayerClick }: MapTabProps = {}) {
  // Hagga | DD toggle. Pan/zoom/filter UX is shared; what changes is the
  // base texture, POI bundle, projection bounds, and whether live data
  // overlays paint at all.
  const [mode, setMode] = useState<MapMode>('hagga')
  // Manual DD layout override. null = follow the current Coriolis cycle's
  // seed from the storm tailer.
  const [ddSeedManual, setDdSeedManual] = useState<number | null>(null)
  // Which DD resource heatmaps are visible. Default off — the overlay
  // covers a lot of the map so we don't surprise operators with it.
  const [ddHeatOn, setDdHeatOn] = useState<Set<DdHeatId>>(() => new Set())

  // DD resource node bundle (lazy). Visibility is tracked per TYPE
  // (subspawner class — e.g. SpiceField Small vs SpiceField Medium)
  // rather than per family so the sidebar matches gaming.tools' filter
  // granularity. Family header click flips every type in the family;
  // a single row click flips just that type.
  const [resources, setResources] = useState<ResourceBundle | null>(null)
  const [visibleResTypes, setVisibleResTypes] = useState<Set<string>>(
    () => new Set(),
  )

  const [data, setData] = useState<MapBundle | null>(null)
  const [pois, setPois] = useState<PoiBundle | null>(null)
  // Set of icon ids whose POIs should be HIDDEN. Empty = all visible.
  const [hidden, setHidden] = useState<Set<string>>(new Set())
  // Player / death / building layers — separate from POI icons so the
  // existing "hide all POIs" action doesn't sweep them away. Respawns
  // start hidden because they overlap with totems/vehicles/beacons
  // visually and only matter when an operator is auditing spawn coverage.
  const [hiddenLayers, setHiddenLayers] = useState<Set<LayerId>>(
    () => new Set(['respawns'] as LayerId[]),
  )
  // Search filters. POI search highlights matches and dims non-matches;
  // player search recenters the map onto the selected character.
  const [poiSearch, setPoiSearch] = useState('')
  const [playerSearch, setPlayerSearch] = useState('')
  // Click-to-copy world coords toast — null when no recent copy.
  const [coordToast, setCoordToast] = useState<string | null>(null)
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
  // Canvas overlay for the DD resource layer (~30k dots) — SVG circles
  // can't keep up with drag at that scale.
  const resourceCanvasRef = useRef<HTMLCanvasElement | null>(null)
  const [viewportSize, setViewportSize] = useState({ w: 0, h: 0 })

  // Poll players every 3 s. The URL flips when mode flips: Hagga is the
  // default; DD passes the partition name so the backend swaps the actor
  // filters and projection. The effect's mode dep guarantees an immediate
  // re-fetch on toggle so we never linger more than one round-trip on the
  // previous map's data.
  useEffect(() => {
    let cancelled = false
    const path = mode === 'dd'
      ? '/map/players?map=DeepDesert_1'
      : '/map/players'
    const tick = () => {
      api
        .get<MapBundle>(path)
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
  }, [mode])

  // 1 s wall-clock tick so the storm wall slides smoothly between polls.
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(id)
  }, [])

  // Track the viewport pixel size so the resource canvas can resize.
  useEffect(() => {
    if (!viewportEl) return
    const update = () => {
      const r = viewportEl.getBoundingClientRect()
      setViewportSize({ w: r.width, h: r.height })
    }
    update()
    const ro = new ResizeObserver(update)
    ro.observe(viewportEl)
    return () => ro.disconnect()
  }, [viewportEl])

  // Effective DD seed: manual override wins, otherwise track the cycle.
  // Falls back to null (no resolved seed) so DD asset URLs can refuse to
  // render until the storm tailer reports the current Coriolis seed.
  const ddSeed = useMemo<number | null>(() => {
    if (mode !== 'dd') return null
    if (ddSeedManual !== null) return ddSeedManual
    return data?.storms?.coriolis_world_seed ?? null
  }, [mode, ddSeedManual, data])

  // POI bundle URL — re-fetched whenever mode or DD seed flips. Hagga
  // uses the single bundle; DD picks the seed-keyed file (1-indexed
  // filenames, 0-indexed seed, hence +1).
  const poiBundleUrl = useMemo(() => {
    if (mode === 'hagga') return '/maps/hagga_pois.json'
    const layoutNum = (ddSeed ?? 0) + 1
    return `/maps/dd_layout_${String(layoutNum).padStart(2, '0')}_pois.json`
  }, [mode, ddSeed])

  // (Re)load static POIs whenever the bundle URL changes. Clearing the
  // current pois on URL change prevents a flash of stale icons under the
  // new base texture.
  useEffect(() => {
    let cancelled = false
    setPois(null)
    fetch(poiBundleUrl)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.statusText))))
      .then((p: PoiBundle) => {
        if (!cancelled) setPois(p)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [poiBundleUrl])

  // DD resource bundle URL. Same NN/seed convention as POIs + heatmaps.
  const resourceBundleUrl = useMemo(() => {
    if (mode !== 'dd' || ddSeed === null) return null
    const layoutNum = ddSeed + 1
    return `/maps/dd_layout_${String(layoutNum).padStart(2, '0')}_resources.json`
  }, [mode, ddSeed])

  // Fetch the resource bundle whenever the URL changes (mode flip or
  // layout change). Always fetched on DD mode entry — the bundle is
  // ~1.7 MB which the browser caches per-layout, and pre-fetching means
  // the sidebar can show real per-type filter rows immediately instead
  // of a placeholder dance. Hagga doesn't get a fetch (URL is null).
  useEffect(() => {
    setResources(null)
    if (!resourceBundleUrl) return
    let cancelled = false
    fetch(resourceBundleUrl)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.statusText))))
      .then((b: ResourceBundle) => { if (!cancelled) setResources(b) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [resourceBundleUrl])

  // Active projection. Hagga uses the backend-supplied affine; DD uses
  // the fixed constants we baked into the WebP tile pyramid.
  const projection = useMemo<Projection | null>(() => {
    if (mode === 'dd') return DD_PROJECTION
    return data?.projection ?? null
  }, [mode, data])

  // Refit when the active map mode changes — Hagga and DD use different
  // base textures so the pan/zoom that worked for one is wrong for the
  // other. The ref tracks "fitted for this mode" rather than "fitted
  // ever" so manual pan/zoom inside a mode is still respected.
  const fittedModeRef = useRef<MapMode | null>(null)
  useEffect(() => {
    if (fittedModeRef.current === mode) return
    if (!viewportEl || !projection || !pois) return
    fitToViewport(viewportEl, projection.texture_size, setScale, setPan)
    fittedModeRef.current = mode
  }, [viewportEl, projection, pois, mode])

  // Visible world bounds (cm). The viewport shows a texture-pixel
  // window whose top-left in texture space is (-pan / scale) and whose
  // size is (viewportSize / scale). Translating that back through the
  // projection gives a world-coord AABB used to cull dense layers (DD
  // resource nodes — 30k+ entries per layout) down to whatever sits
  // under the user's eye. We add a small margin so dots don't pop in
  // at the edge of a pan.
  const visibleWorldBounds = useMemo(() => {
    if (!projection || !viewportEl) return null
    const r = viewportEl.getBoundingClientRect()
    const xMinTex = -pan.x / scale
    const yMinTex = -pan.y / scale
    const xMaxTex = xMinTex + r.width / scale
    const yMaxTex = yMinTex + r.height / scale
    const texToWorldX = (tx: number) =>
      projection.world_x_min +
      (tx / projection.texture_size) *
        (projection.world_x_max - projection.world_x_min)
    const texToWorldY = (ty: number) => {
      const tyEff = projection.flip_y ? projection.texture_size - ty : ty
      return (
        projection.world_y_min +
        (tyEff / projection.texture_size) *
          (projection.world_y_max - projection.world_y_min)
      )
    }
    const xs = [texToWorldX(xMinTex), texToWorldX(xMaxTex)]
    const ys = [texToWorldY(yMinTex), texToWorldY(yMaxTex)]
    const marginX = (Math.max(...xs) - Math.min(...xs)) * 0.05
    const marginY = (Math.max(...ys) - Math.min(...ys)) * 0.05
    return {
      xMin: Math.min(...xs) - marginX,
      xMax: Math.max(...xs) + marginX,
      yMin: Math.min(...ys) - marginY,
      yMax: Math.max(...ys) + marginY,
    }
  }, [projection, viewportEl, pan, scale])

  // Map family-id to color for the renderer. Tiny but called per-cluster.
  const resFamilyColor = useMemo(() => {
    const m = new Map<string, string>()
    if (resources) for (const f of resources.families) m.set(f.id, f.color)
    return m
  }, [resources])

  // Cluster the visible-type resource nodes into world-coordinate cells.
  // Cell size is the clusterRadius (60 viewport px) translated back into
  // world units at the current scale, so cell boundaries are anchored in
  // world space — panning never changes which cell a node falls into, so
  // clusters don't morph during drag. Cells reform only when scale flips
  // (zoom changes the world units per pixel), which matches gaming.tools'
  // supercluster behaviour of break-apart-on-zoom-in.
  //
  // Buckets are computed across the WHOLE filtered set (not just nodes
  // inside the viewport AABB) so a cluster at the edge of the screen
  // shows its full member count + centroid, not a partial set that
  // would jump as you pan into it.
  type ResCluster = { wx: number; wy: number; count: number; color: string }
  const resourceClusters = useMemo(() => {
    if (!resources || !projection || visibleResTypes.size === 0) {
      return [] as ResCluster[]
    }
    const CLUSTER_RADIUS_PX = 60
    const wxRange = projection.world_x_max - projection.world_x_min
    const wyRange = projection.world_y_max - projection.world_y_min
    const cellW = (CLUSTER_RADIUS_PX / scale) * (wxRange / projection.texture_size)
    const cellH = (CLUSTER_RADIUS_PX / scale) * (wyRange / projection.texture_size)
    const buckets = new Map<string, ResCluster & { sumX: number; sumY: number }>()
    for (const n of resources.nodes) {
      if (!visibleResTypes.has(n.t)) continue
      const cx = Math.floor(n.x / cellW)
      const cy = Math.floor(n.y / cellH)
      const key = `${n.t}|${cx},${cy}`
      const b = buckets.get(key)
      if (b) {
        b.sumX += n.x
        b.sumY += n.y
        b.count++
      } else {
        buckets.set(key, {
          sumX: n.x,
          sumY: n.y,
          count: 1,
          color: resFamilyColor.get(n.f) ?? '#fff',
          wx: 0,
          wy: 0,
        })
      }
    }
    const out: ResCluster[] = []
    for (const b of buckets.values()) {
      b.wx = b.sumX / b.count
      b.wy = b.sumY / b.count
      out.push(b)
    }
    return out
  }, [resources, projection, visibleResTypes, scale, resFamilyColor])

  // Count of nodes currently being drawn (for the legend). Sum of all
  // cluster point_counts intersected with the viewport AABB.
  const visibleResourceCount = useMemo(() => {
    if (!visibleWorldBounds) return 0
    const { xMin, xMax, yMin, yMax } = visibleWorldBounds
    let n = 0
    for (const c of resourceClusters) {
      if (c.wx < xMin || c.wx > xMax || c.wy < yMin || c.wy > yMax) continue
      n += c.count
    }
    return n
  }, [resourceClusters, visibleWorldBounds])

  // Canvas-based resource layer draw. SVG was fine at the POI volume
  // (~3k circles) but blows up at ~30k. Canvas runs in viewport pixel
  // space outside the CSS-transformed map layer, so we project each
  // node through (world -> texture -> viewport-pixel) ourselves and
  // redraw the whole layer per pan/zoom tick.
  useEffect(() => {
    const cnv = resourceCanvasRef.current
    if (!cnv || !projection) return
    const dpr = window.devicePixelRatio || 1
    const { w, h } = viewportSize
    if (w === 0 || h === 0) return
    if (cnv.width !== w * dpr) cnv.width = w * dpr
    if (cnv.height !== h * dpr) cnv.height = h * dpr
    cnv.style.width = `${w}px`
    cnv.style.height = `${h}px`
    const ctx = cnv.getContext('2d')
    if (!ctx) return
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)
    if (mode !== 'dd' || resourceClusters.length === 0) return

    const tex = projection.texture_size
    const wxMin = projection.world_x_min
    const wxRange = projection.world_x_max - projection.world_x_min
    const wyMin = projection.world_y_min
    const wyRange = projection.world_y_max - projection.world_y_min
    const flipY = projection.flip_y
    // gaming.tools' size formula: count=1 -> 0.8× base; count>=140 -> 2.0×.
    // BASE_HALF is the on-screen half-width at the base scale.
    const BASE_HALF = 10
    const SCALE_AT_1 = 0.8
    const SCALE_AT_140 = 2.0

    // Group clusters by color so we issue one fill + stroke per family
    // color rather than per cluster — bounded by family count regardless
    // of how many clusters are on screen.
    const byColor = new Map<string, ResCluster[]>()
    for (const c of resourceClusters) {
      const arr = byColor.get(c.color)
      if (arr) arr.push(c)
      else byColor.set(c.color, [c])
    }

    ctx.lineWidth = 0.75
    ctx.strokeStyle = 'rgba(0,0,0,0.65)'
    for (const [color, list] of byColor) {
      ctx.beginPath()
      for (const c of list) {
        const tx = ((c.wx - wxMin) / wxRange) * tex
        let ty = ((c.wy - wyMin) / wyRange) * tex
        if (flipY) ty = tex - ty
        const sx = tx * scale + pan.x
        const sy = ty * scale + pan.y
        const t = Math.min(1, Math.max(0, (c.count - 1) / 139))
        const sizeMul = SCALE_AT_1 + (SCALE_AT_140 - SCALE_AT_1) * t
        const half = BASE_HALF * sizeMul
        if (sx + half < 0 || sy + half < 0 ||
            sx - half > w || sy - half > h) continue
        ctx.moveTo(sx, sy - half)
        ctx.lineTo(sx + half, sy)
        ctx.lineTo(sx, sy + half)
        ctx.lineTo(sx - half, sy)
        ctx.closePath()
      }
      ctx.fillStyle = color
      ctx.fill()
      ctx.stroke()
    }
  }, [resourceClusters, scale, pan, viewportSize, projection, mode])

  // Project world coords to texture pixel coords. Shared between players
  // and POIs — both come from the same UE5 world frame.
  const project = useMemo(() => {
    return (wx: number, wy: number) => {
      if (!projection) return null
      const tx =
        ((wx - projection.world_x_min) /
          (projection.world_x_max - projection.world_x_min)) *
        projection.texture_size
      let ty =
        ((wy - projection.world_y_min) /
          (projection.world_y_max - projection.world_y_min)) *
        projection.texture_size
      if (projection.flip_y) ty = projection.texture_size - ty
      return { x: tx, y: ty }
    }
  }, [projection])

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

  // Track the mouse-down origin so we can tell drags from clicks: any
  // movement >4 px between down and up disqualifies the gesture from
  // being treated as a coord-copy click.
  const downRef = useRef<{ x: number; y: number; t: number } | null>(null)
  const onMouseDown = (e: React.MouseEvent<HTMLDivElement>) => {
    dragRef.current = { x: e.clientX - pan.x, y: e.clientY - pan.y }
    downRef.current = { x: e.clientX, y: e.clientY, t: Date.now() }
  }
  const onMouseMove = (e: React.MouseEvent<HTMLDivElement>) => {
    if (!dragRef.current) return
    setPan({ x: e.clientX - dragRef.current.x, y: e.clientY - dragRef.current.y })
  }
  const onMouseUp = (e?: React.MouseEvent<HTMLDivElement>) => {
    const wasClick =
      e && downRef.current &&
      Math.abs(e.clientX - downRef.current.x) < 4 &&
      Math.abs(e.clientY - downRef.current.y) < 4 &&
      Date.now() - downRef.current.t < 400
    dragRef.current = null
    downRef.current = null
    // Click-to-copy world coords. Only fires for genuine clicks (no
    // drag), and we inverse-project the cursor through the same affine
    // mapping the SVG uses going the other direction.
    if (wasClick && e && viewportEl && projection) {
      const rect = viewportEl.getBoundingClientRect()
      const cx = e.clientX - rect.left
      const cy = e.clientY - rect.top
      const tx = (cx - pan.x) / scale
      const ty = (cy - pan.y) / scale
      const proj = projection
      const wx =
        (tx / proj.texture_size) * (proj.world_x_max - proj.world_x_min) +
        proj.world_x_min
      const tyUE = proj.flip_y ? proj.texture_size - ty : ty
      const wy =
        (tyUE / proj.texture_size) * (proj.world_y_max - proj.world_y_min) +
        proj.world_y_min
      // Only copy when the click landed inside the map texture bounds.
      if (tx >= 0 && tx <= proj.texture_size && ty >= 0 && ty <= proj.texture_size) {
        const txt = `${Math.round(wx)} ${Math.round(wy)}`
        copyToClipboard(txt).then((ok) => {
          setCoordToast(ok ? txt : 'copy failed (need HTTPS)')
          window.setTimeout(() => setCoordToast(null), 1800)
        })
      }
    }
  }

  // Recenter the map so the given world point lands at the centre of
  // the viewport, keeping the current zoom. Used by player-jump.
  const jumpTo = (worldX: number, worldY: number) => {
    if (!viewportEl || !projection) return
    const proj = projection
    const tx =
      ((worldX - proj.world_x_min) / (proj.world_x_max - proj.world_x_min)) *
      proj.texture_size
    let ty =
      ((worldY - proj.world_y_min) / (proj.world_y_max - proj.world_y_min)) *
      proj.texture_size
    if (proj.flip_y) ty = proj.texture_size - ty
    setPan({
      x: viewportEl.clientWidth / 2 - tx * scale,
      y: viewportEl.clientHeight / 2 - ty * scale,
    })
  }

  // POI icons sized so they're roughly constant on screen — 16 px display
  // by default. Scaled with the SVG transform via the same /scale trick
  // we use for the player dots.
  const POI_PX = 22

  // Pre-bucket visible POIs once per render so the SVG mapping stays
  // simple and we don't recompute the hidden-set check inside the JSX.
  // POI search highlights matches and dims non-matches but doesn't
  // remove anything — operators want to see the whole map context.
  const poiSearchNorm = poiSearch.trim().toLowerCase()
  const visiblePois = useMemo(() => {
    if (!pois) return []
    return pois.pois.filter((p) => !hidden.has(p.icon))
  }, [pois, hidden])
  const poiMatches = useMemo(() => {
    if (!poiSearchNorm) return null
    const m = new Set<number>()
    visiblePois.forEach((p, i) => {
      if (p.name.toLowerCase().includes(poiSearchNorm)) m.add(i)
    })
    return m
  }, [visiblePois, poiSearchNorm])

  // Filtered player list for the player-search/jump widget. Empty query
  // shows nothing (otherwise the dropdown is huge); 1+ chars shows matches.
  const playerSearchNorm = playerSearch.trim().toLowerCase()
  const playerMatches = useMemo(() => {
    if (!data || !playerSearchNorm) return []
    return data.players.filter((p) =>
      p.character_name?.toLowerCase().includes(playerSearchNorm),
    )
  }, [data, playerSearchNorm])

  // Flat list of icon ids so the global counter / show-all / hide-all
  // can operate without walking the group tree each time. MUST live
  // above the early-return guards below — calling a hook conditionally
  // breaks the rules of hooks and React renders nothing.
  const allIconIds = useMemo(
    () => (pois ? pois.groups.flatMap((g) => g.icons.map((i) => i.id)) : []),
    [pois],
  )

  if (err) return <div className="alert">{err}</div>
  if (!data || !projection) return <div className="placeholder">loading…</div>

  const tex = projection.texture_size
  const baseImageSrc =
    mode === 'hagga'
      ? '/maps/hagga.webp'
      : `/maps/dd_layout_${String((ddSeed ?? 0) + 1).padStart(2, '0')}.webp`
  const baseImageAlt = mode === 'hagga' ? 'Hagga Basin' : `Deep Desert Layout ${(ddSeed ?? 0) + 1}`
  const allHidden = allIconIds.length > 0 && hidden.size === allIconIds.length
  const showAll = () => {
    setHidden(new Set())
    if (resources) {
      const allTypes = resources.families.flatMap((f) => f.types.map((t) => t.id))
      setVisibleResTypes(new Set(allTypes))
    }
  }
  const hideAll = () => {
    setHidden(new Set(allIconIds))
    setVisibleResTypes(new Set())
  }

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
            {/* Hagga / Deep Desert mode toggle. Tab-bar styling reuses
                the existing .map-link buttons with an explicit
                `is-active` modifier the host CSS bolds. */}
            <div className="map-mode-tabs">
              <button
                type="button"
                className={`map-link ${mode === 'hagga' ? 'is-active' : ''}`}
                onClick={() => setMode('hagga')}
              >
                Hagga
              </button>
              <span className="hint">·</span>
              <button
                type="button"
                className={`map-link ${mode === 'dd' ? 'is-active' : ''}`}
                onClick={() => setMode('dd')}
              >
                Deep Desert
              </button>
            </div>
            {mode === 'dd' && (
              <div className="map-layout-picker">
                <label className="hint">Layout</label>
                <select
                  className="map-search"
                  value={ddSeedManual ?? ''}
                  onChange={(e) =>
                    setDdSeedManual(e.target.value === '' ? null : Number(e.target.value))
                  }
                >
                  <option value="">
                    Current week
                    {data?.storms?.coriolis_world_seed != null
                      ? ` (Layout ${(data.storms.coriolis_world_seed) + 1})`
                      : ''}
                  </option>
                  {Array.from({ length: 12 }, (_, i) => (
                    <option key={i} value={i}>
                      Layout {i + 1}
                    </option>
                  ))}
                </select>
              </div>
            )}
            <div className="map-sidebar-actions">
              <button type="button" className="map-link" onClick={showAll}>
                show all
              </button>
              <span className="hint">·</span>
              <button type="button" className="map-link" onClick={hideAll}>
                hide all
              </button>
            </div>
            <input
              type="search"
              className="map-search"
              placeholder="search POIs…"
              value={poiSearch}
              onChange={(e) => setPoiSearch(e.target.value)}
            />
            {mode === 'hagga' && (
              <div className="map-jump-wrap">
                <input
                  type="search"
                  className="map-search"
                  placeholder="jump to player…"
                  value={playerSearch}
                  onChange={(e) => setPlayerSearch(e.target.value)}
                />
                {playerSearchNorm && playerMatches.length > 0 && (
                  <ul className="map-jump-list">
                    {playerMatches.slice(0, 8).map((p) => (
                      <li key={p.actor_id}>
                        <button
                          type="button"
                          className="map-jump-item"
                          onClick={() => {
                            jumpTo(p.world_x, p.world_y)
                            setPlayerSearch('')
                          }}
                        >
                          {p.character_name}
                          <span className="hint">
                            {p.online_status?.toLowerCase() === 'online' ? '·online' : ''}
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )}
            <div className="map-icon-list">
              {/* Live layers — kept above POIs since they're admin-
                  oriented and tend to be looked at first. Hidden in DD
                  mode because the live coords project onto Hagga's
                  texture and would render in the wrong spots over a DD
                  layout. */}
              {mode === 'hagga' && (
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
                      { id: 'respawns', label: 'Respawn points', count: data.respawns?.length ?? 0, dot: '#10b981' },
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
              )}
              {mode === 'dd' && (
                <section className="map-group">
                  <header className="map-group-header" style={{ cursor: 'default' }}>
                    <span className="map-group-label">Resource heatmap</span>
                  </header>
                  <ul className="map-group-rows">
                    {DD_HEAT_LAYERS.map((h) => {
                      const off = !ddHeatOn.has(h.id)
                      return (
                        <li
                          key={h.id}
                          className={`map-icon-row ${off ? 'is-off' : ''}`}
                          onClick={() =>
                            setDdHeatOn((prev) => {
                              const next = new Set(prev)
                              if (next.has(h.id)) next.delete(h.id)
                              else next.add(h.id)
                              return next
                            })
                          }
                          title={off ? 'show' : 'hide'}
                        >
                          <span
                            className="map-layer-dot"
                            style={{ background: h.color }}
                          />
                          <span className="map-icon-label">{h.label}</span>
                        </li>
                      )
                    })}
                  </ul>
                </section>
              )}
              {/* DD resource node families with per-subtype filters
                  (e.g. SpiceField Small vs Medium) matching gaming.tools'
                  filter granularity. Family is opt-in because the bundle
                  is large (~30k nodes per layout). Family header click
                  flips every type in that family at once. Until the
                  bundle has loaded we show family rows as placeholders
                  so the first click can trigger the fetch. */}
              {mode === 'dd' && resourceBundleUrl && resources && (
                <>
                  {resources.families.map((fa) => {
                    const typeIds = fa.types.map((t) => t.id)
                    const onCount = typeIds.filter((id) =>
                      visibleResTypes.has(id),
                    ).length
                    const allOn = onCount === typeIds.length && typeIds.length > 0
                    const someOn = onCount > 0
                    return (
                      <section
                        key={fa.id}
                        className={`map-group ${!someOn ? 'is-off' : ''}`}
                      >
                        <header
                          className="map-group-header"
                          onClick={() =>
                            setVisibleResTypes((prev) => {
                              const next = new Set(prev)
                              if (allOn) typeIds.forEach((id) => next.delete(id))
                              else typeIds.forEach((id) => next.add(id))
                              return next
                            })
                          }
                          title={allOn ? 'hide all' : 'show all'}
                        >
                          <span
                            className="map-layer-dot"
                            style={{ background: fa.color }}
                          />
                          <span className="map-group-label">{fa.label}</span>
                          <span className="map-group-count">
                            {someOn && !allOn ? '· ' : ''}
                            {fa.count}
                          </span>
                        </header>
                        <ul className="map-group-rows">
                          {fa.types.map((t) => {
                            const off = !visibleResTypes.has(t.id)
                            return (
                              <li
                                key={t.id}
                                className={`map-icon-row ${off ? 'is-off' : ''}`}
                                onClick={(e) => {
                                  e.stopPropagation()
                                  setVisibleResTypes((prev) => {
                                    const next = new Set(prev)
                                    if (next.has(t.id)) next.delete(t.id)
                                    else next.add(t.id)
                                    return next
                                  })
                                }}
                                title={off ? 'show' : 'hide'}
                              >
                                <span
                                  className="map-layer-dot"
                                  style={{ background: fa.color }}
                                />
                                <span className="map-icon-label">{t.id}</span>
                                <span className="map-icon-count">{t.count}</span>
                              </li>
                            )
                          })}
                        </ul>
                      </section>
                    )
                  })}
                </>
              )}
              {mode === 'dd' && resourceBundleUrl && !resources && (
                <section className="map-group is-off">
                  <header
                    className="map-group-header"
                    style={{ cursor: 'default' }}
                  >
                    <span className="map-group-label">Resource nodes</span>
                    <span className="map-group-count">loading…</span>
                  </header>
                </section>
              )}
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
          <div className="map-controls">
            <button
              type="button"
              className="map-control-btn"
              onClick={() => fitToViewport(viewportEl, tex, setScale, setPan)}
              title="Fit map to viewport"
            >
              Fit
            </button>
          </div>
          {coordToast && (
            <div className="map-coord-toast">copied {coordToast}</div>
          )}
          {/* Two transformed layers with the same pan/scale so the
              base map texture and the SVG markers stay aligned. The
              canvas resource overlay sits between them in DOM order so
              its dots paint over the base map but under the SVG —
              meaning POI icons / players / totems / etc. stay legible
              on top of the resource diamonds. */}
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
              src={baseImageSrc}
              width={tex}
              height={tex}
              draggable={false}
              alt={baseImageAlt}
            />
          </div>
          {/* Canvas sits between the base layer and the SVG overlay so
              POI icons / players / totems / etc. paint above the
              resource diamonds. */}
          <canvas
            ref={resourceCanvasRef}
            className="map-resource-canvas"
            style={{
              position: 'absolute',
              top: 0,
              left: 0,
              pointerEvents: 'none',
            }}
          />
          <div
            className="map-layer"
            style={{
              width: tex,
              height: tex,
              transform: `translate(${pan.x}px, ${pan.y}px) scale(${scale})`,
              transformOrigin: '0 0',
            }}
          >
            <svg
              width={tex}
              height={tex}
              className="map-overlay"
              viewBox={`0 0 ${tex} ${tex}`}
            >
              {/* Layer paint order, bottom → top:
                    1. DD sector grid (DD mode only — drawn first so it
                       sits beneath every marker layer)
                    2. POI icons (static, lowest priority)
                    3. Sandstorm path + active wall (amber sweep)
                    4. Land-claim totems (purple diamond)
                    5. Vehicles (cyan triangle)
                    6. Recent death markers (red X)
                    7. Live players (always on top so you can find them) */}
              {mode === 'dd' && (
                <DdSectorGrid project={project} scale={scale} />
              )}
              {/* DD resource heatmaps — one image per visible resource,
                  stretched to the full texture so the 1024² density map
                  lines up with gameBounds. Painted before POIs so icons
                  + labels stay legible on top. */}
              {mode === 'dd' &&
                DD_HEAT_LAYERS.filter((h) => ddHeatOn.has(h.id)).map((h) => {
                  const layoutNum = (ddSeed ?? 0) + 1
                  const src = `/maps/dd_layout_${String(layoutNum).padStart(2, '0')}_heat_${h.id}.png`
                  return (
                    <image
                      key={`heat-${h.id}`}
                      href={src}
                      x={0}
                      y={0}
                      width={tex}
                      height={tex}
                      style={{ pointerEvents: 'none' }}
                    />
                  )
                })}
              {/* DD resource nodes render on a separate <canvas> overlay
                  outside this SVG (see the resourceCanvas effect) — SVG
                  circles can't keep up with drag at ~30k volume. */}
              {visiblePois.map((poi, i) => {
                const pos = project(poi.x, poi.y)
                if (!pos) return null
                const matched = poiMatches?.has(i) ?? false
                const dimmed = poiMatches != null && !matched
                const size = (POI_PX * (matched ? 1.6 : 1)) / scale
                return (
                  <g key={`poi-${i}`} opacity={dimmed ? 0.18 : 1}>
                    {matched && (
                      <circle
                        cx={pos.x}
                        cy={pos.y}
                        r={size * 0.75}
                        fill="none"
                        stroke="#fbbf24"
                        strokeWidth={3 / scale}
                        opacity={0.9}
                      />
                    )}
                    <image
                      href={`/maps/icons/${poi.icon}.webp`}
                      x={pos.x - size / 2}
                      y={pos.y - size / 2}
                      width={size}
                      height={size}
                    >
                      <title>{poi.name}</title>
                    </image>
                  </g>
                )
              })}
              {mode === 'hagga' && !hiddenLayers.has('storms') &&
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
                        r={8 / scale}
                        fill="#fff"
                        stroke="#92400e"
                        strokeWidth={1 / scale}
                      />
                    </g>
                  )
                })}
              {!hiddenLayers.has('respawns') &&
                data.respawns?.map((r) => {
                  const pos = project(r.world_x, r.world_y)
                  if (!pos) return null
                  const radius = 7 / scale
                  return (
                    <g key={`rs-${r.actor_id}`} transform={`translate(${pos.x}, ${pos.y})`}>
                      <circle
                        r={radius}
                        fill="none"
                        stroke="#10b981"
                        strokeWidth={2 / scale}
                        opacity={0.9}
                      />
                      <circle r={radius * 0.35} fill="#10b981" opacity={0.9}>
                        <title>
                          {r.group_type}
                          {r.owners?.length ? ` — ${r.owners.join(', ')}` : ''}
                        </title>
                      </circle>
                    </g>
                  )
                })}
              {!hiddenLayers.has('buildings') &&
                data.buildings.map((b) => {
                  const pos = project(b.world_x, b.world_y)
                  if (!pos) return null
                  const r = 9 / scale
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
                  const r = 9 / scale
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
                  const r = 10 / scale
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
                        r={11 / scale}
                        fill={online ? '#22c55e' : '#94a3b8'}
                        stroke="#000"
                        strokeWidth={1.5 / scale}
                        opacity={online ? 0.95 : 0.55}
                      />
                      <text
                        y={-(14 / scale)}
                        textAnchor="middle"
                        fontSize={14 / scale}
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
          {`${data.players.length} players · `}
          {mode === 'dd' && `Layout ${(ddSeed ?? 0) + 1} · `}
          {visiblePois.length} of {pois?.pois.length ?? 0} POIs visible
          {mode === 'dd' && visibleResTypes.size > 0 &&
            ` · ${visibleResourceCount} resources visible`}
          {allHidden && ' (all categories hidden)'}
        </span>
        <span className="hint">scroll to zoom · drag to pan · click a row to toggle</span>
      </div>
    </div>
  )
}

function clamp(n: number, lo: number, hi: number) {
  return Math.max(lo, Math.min(hi, n))
}

// 9×9 sector grid for Deep Desert (A1..I9). Columns A..I run W→E,
// rows 1..9 run N→S, matching the gridCell strings in the gaming.tools
// data. We draw the grid in world coordinates and let the SVG transform
// scale lines down with zoom so they stay one screen pixel thin.
function DdSectorGrid({
  project,
  scale,
}: {
  project: (wx: number, wy: number) => { x: number; y: number } | null
  scale: number
}) {
  const lines: React.ReactNode[] = []
  const labels: React.ReactNode[] = []
  const stepX = (DD_GRID_X_MAX - DD_GRID_X_MIN) / 9
  const stepY = (DD_GRID_Y_MAX - DD_GRID_Y_MIN) / 9
  // 10 line positions enclose 9 cells.
  for (let i = 0; i <= 9; i++) {
    const vx = DD_GRID_X_MIN + i * stepX
    const vy = DD_GRID_Y_MIN + i * stepY
    const xLineTop = project(vx, DD_GRID_Y_MAX)
    const xLineBot = project(vx, DD_GRID_Y_MIN)
    const yLineLeft = project(DD_GRID_X_MIN, vy)
    const yLineRight = project(DD_GRID_X_MAX, vy)
    if (xLineTop && xLineBot) {
      lines.push(
        <line
          key={`gx-${i}`}
          x1={xLineTop.x}
          y1={xLineTop.y}
          x2={xLineBot.x}
          y2={xLineBot.y}
          stroke="#000"
          strokeOpacity={0.35}
          strokeWidth={1 / scale}
        />,
      )
    }
    if (yLineLeft && yLineRight) {
      lines.push(
        <line
          key={`gy-${i}`}
          x1={yLineLeft.x}
          y1={yLineLeft.y}
          x2={yLineRight.x}
          y2={yLineRight.y}
          stroke="#000"
          strokeOpacity={0.35}
          strokeWidth={1 / scale}
        />,
      )
    }
  }
  // Cell labels at the centre of each cell. In-game convention: rows
  // are letters A..I going south→north (A = southernmost), columns are
  // numbers 1..9 going west→east. So bottom-left cell is "A1", top-right
  // is "I9". `row` iterates from row 0 at the lowest world y (which lands
  // at the TOP of the screen with no flip — north — so the row letter
  // counts DOWN: row 0 → "I", row 8 → "A").
  for (let row = 0; row < 9; row++) {
    for (let col = 0; col < 9; col++) {
      const wx = DD_GRID_X_MIN + (col + 0.5) * stepX
      const wy = DD_GRID_Y_MIN + (row + 0.5) * stepY
      const pos = project(wx, wy)
      if (!pos) continue
      labels.push(
        <text
          key={`gl-${row}-${col}`}
          x={pos.x}
          y={pos.y}
          fill="#000"
          fillOpacity={0.55}
          fontSize={48 / scale}
          textAnchor="middle"
          dominantBaseline="central"
          style={{ pointerEvents: 'none' }}
        >
          {String.fromCharCode(0x41 + (8 - row))}
          {col + 1}
        </text>,
      )
    }
  }
  return (
    <g style={{ pointerEvents: 'none' }}>
      {lines}
      {labels}
    </g>
  )
}

// Copy text to the OS clipboard. Tries the modern async Clipboard API
// first (only available in secure contexts — HTTPS or localhost), then
// falls back to the legacy execCommand('copy') via a hidden textarea
// so the LAN HTTP deployment can still copy world coords. Returns true
// when one of the two paths succeeded.
async function copyToClipboard(text: string): Promise<boolean> {
  // Secure-context path. window.isSecureContext is true on HTTPS and
  // on http://localhost; everywhere else navigator.clipboard.writeText
  // rejects with NotAllowedError.
  if (typeof window !== 'undefined' && window.isSecureContext && navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // fall through to legacy
    }
  }
  // Legacy path. Off-screen textarea so the user doesn't see a flash.
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    ta.style.pointerEvents = 'none'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

// fitToViewport zooms the map so the full 8192² texture exactly fits the
// available viewport, then centres it. Used by the "Fit" button and on
// initial viewport mount so the first paint isn't off-screen if the
// browser window is smaller than the default 0.15× scale assumes.
function fitToViewport(
  viewport: HTMLDivElement | null,
  tex: number,
  setScale: (s: number) => void,
  setPan: (p: { x: number; y: number }) => void,
) {
  if (!viewport) return
  const w = viewport.clientWidth
  const h = viewport.clientHeight
  if (w === 0 || h === 0) return
  const s = Math.min(w / tex, h / tex)
  setScale(s)
  setPan({ x: (w - tex * s) / 2, y: (h - tex * s) / 2 })
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
