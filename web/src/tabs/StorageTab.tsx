import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'

type StorageRow = {
  id: number
  inventory_type: number | null
  max_item_count: number | null
  max_item_volume: number | null
  actor_id: number | null
  exchange_id: number | null
  item_id: number | null
  vehicle_module_id: number | null
  component_name_hash: number | null
  component_name: string
  item_count: number
  owner_actor_class: string | null
  owner_player_name: string | null
  owner_item_template: string | null
  // Set when this inventory is a sub-inventory of an item that lives in a
  // player's slot (e.g. a MiningTool inside Bing's BackpackInventory). The
  // backend chains one hop up the parent chain; owner_player_name is
  // already COALESCEd with this so the row groups under the right player.
  root_player_name: string | null
}

type StorageDetail = {
  inventory: StorageRow
  items: Record<string, unknown>[]
}

type OwnerType = '' | 'actor' | 'exchange' | 'item' | 'vmodule' | 'orphan'

const OWNER_LABELS: Record<OwnerType, string> = {
  '': 'any',
  actor: 'actor (player / NPC)',
  exchange: 'exchange',
  item: 'container item',
  vmodule: 'vehicle module',
  orphan: 'orphan (no owner)',
}

// One group in the sidebar tree. Each group is one logical "owner"
// (a player, an NPC actor, an exchange terminal, a container item, a
// vehicle module, or the catch-all orphan bucket). The group's rows
// are the inventory slots that owner has.
type Group = {
  key: string
  kind: 'player' | 'npc' | 'exchange' | 'vehicle' | 'dropped' | 'item' | 'orphan'
  name: string
  sub?: string
  rows: StorageRow[]
}

// A class-coalesced bucket of multiple Groups that all share the same
// kind + display name (e.g. all 13 DuneChoamExchangeTerminal_C actors).
// Buckets only appear in the sidebar when there are 2+ groups to collapse.
type Bucket = {
  key: string
  name: string
  kind: Group['kind']
  groups: Group[]
}

type SidebarItem =
  | { kind: 'single'; group: Group }
  | { kind: 'bucket'; bucket: Bucket }

const KIND_ORDER: Record<Group['kind'], number> = {
  player: 0, npc: 1, exchange: 2, vehicle: 3, dropped: 4, item: 5, orphan: 6,
}

// EInventoryType integer for PlayerDroppedLoot — what the game stamps on
// the inventory inside a world-spawned LootContainer actor when a player
// dies. Carving these out of the NPC bucket into their own group makes
// the sidebar easier to scan; player-PLACED containers (where the chain
// resolves to a player owner) still group under that player.
const PLAYER_DROPPED_LOOT_TYPE = 22

// Kinds where multiple instances of the same actor class are common
// (CHOAM terminals, NPCs, fleets of vehicles, scattered loot bags). For
// these we collapse matching name+kind into a single Bucket so the
// sidebar stays tidy.
const COALESCE_KINDS = new Set<Group['kind']>(['npc', 'exchange', 'vehicle', 'dropped'])

function buildSidebar(groups: Group[]): SidebarItem[] {
  const out: SidebarItem[] = []
  const slotIdx = new Map<string, number>()
  for (const g of groups) {
    if (!COALESCE_KINDS.has(g.kind)) {
      out.push({ kind: 'single', group: g })
      continue
    }
    const k = `b:${g.kind}:${g.name}`
    const i = slotIdx.get(k)
    if (i === undefined) {
      slotIdx.set(k, out.length)
      out.push({ kind: 'single', group: g })
    } else {
      const existing = out[i]
      if (existing.kind === 'single') {
        out[i] = {
          kind: 'bucket',
          bucket: { key: k, name: g.name, kind: g.kind, groups: [existing.group, g] },
        }
      } else {
        existing.bucket.groups.push(g)
      }
    }
  }
  return out
}

function buildGroups(rows: StorageRow[]): Group[] {
  const byKey = new Map<string, Group>()
  for (const r of rows) {
    let key: string, kind: Group['kind'], name: string
    let sub: string | undefined
    if (r.owner_player_name) {
      key = `p:${r.owner_player_name}`
      kind = 'player'
      name = r.owner_player_name
      // Leave sub undefined for now; we'll backfill it below from
      // whichever row in the group corresponds to the actual
      // PlayerCharacter actor (otherwise the first row, sorted DESC
      // by id, can be a totem / vehicle inv and the subtitle ends up
      // saying 'TotemSmall').
    } else if (r.exchange_id) {
      key = `e:${r.exchange_id}`
      kind = 'exchange'
      name = `Exchange #${r.exchange_id}`
    } else if (r.item_id) {
      key = `i:${r.item_id}`
      kind = 'item'
      name = r.owner_item_template || `Container item #${r.item_id}`
      sub = `item ${r.item_id}`
    } else if (r.actor_id && r.inventory_type === PLAYER_DROPPED_LOOT_TYPE) {
      // World-spawned LootContainer for player-dropped items. Coalesces
      // on the friendly bucket name so multiple instances become a
      // single "Dropped loot × N" header rather than a list of
      // identically-named LootContainer rows.
      key = 'd:player-dropped-loot'
      kind = 'dropped'
      name = 'Player dropped loot'
      sub = `#${r.actor_id}`
    } else if (r.actor_id) {
      key = `n:${r.actor_id}`
      kind = 'npc'
      name = shortClass(r.owner_actor_class) || `Actor #${r.actor_id}`
      sub = `#${r.actor_id}`
    } else if (r.vehicle_module_id) {
      key = `v:${r.vehicle_module_id}`
      kind = 'vehicle'
      name = `Vehicle module #${r.vehicle_module_id}`
    } else {
      key = 'o:orphan'
      kind = 'orphan'
      name = 'Orphans'
    }
    let g = byKey.get(key)
    if (!g) {
      g = { key, kind, name, sub, rows: [] }
      byKey.set(key, g)
    }
    g.rows.push(r)
  }
  for (const g of byKey.values()) {
    g.rows.sort((a, b) => {
      const an = a.component_name || `t${a.inventory_type ?? '?'}`
      const bn = b.component_name || `t${b.inventory_type ?? '?'}`
      if (an !== bn) return an < bn ? -1 : 1
      return a.id - b.id
    })
    // For player groups, set sub to the player-character class. Pick from
    // a row whose owner_actor_class shortens to 'PlayerCharacter'; fall
    // back to the first row's class if none match.
    if (g.kind === 'player') {
      const playerRow = g.rows.find(
        (r) => shortClass(r.owner_actor_class) === 'PlayerCharacter',
      )
      g.sub = shortClass((playerRow ?? g.rows[0])?.owner_actor_class ?? null) || undefined
    }
  }
  return [...byKey.values()].sort((a, b) => {
    if (KIND_ORDER[a.kind] !== KIND_ORDER[b.kind]) return KIND_ORDER[a.kind] - KIND_ORDER[b.kind]
    return a.name.localeCompare(b.name)
  })
}

// Sub-group rows within a player's group by their actor. Each actor
// (the player's character, a vehicle they own, a totem they placed)
// becomes its own sub-section with a small header above its rows.
// Container items (rows with no actor_id but an item_id) collapse
// into one 'Container items' sub-section. With ≤1 sub-group the
// renderer falls back to a flat list.
type SubGroup = {
  key: string
  label: string
  rows: StorageRow[]
}

function buildSubGroups(rows: StorageRow[]): SubGroup[] {
  const byKey = new Map<string, SubGroup>()
  for (const r of rows) {
    let key: string, label: string
    if (r.actor_id) {
      key = `a:${r.actor_id}`
      label = shortClass(r.owner_actor_class) || `Actor #${r.actor_id}`
    } else if (r.item_id) {
      key = 'i:items'
      label = 'Container items'
    } else {
      key = 'o:other'
      label = 'Other'
    }
    let sg = byKey.get(key)
    if (!sg) {
      sg = { key, label, rows: [] }
      byKey.set(key, sg)
    }
    sg.rows.push(r)
  }
  return [...byKey.values()].sort((a, b) => {
    // Player character first, container items + other last, rest alpha.
    const aPlayer = a.label === 'PlayerCharacter'
    const bPlayer = b.label === 'PlayerCharacter'
    if (aPlayer !== bPlayer) return aPlayer ? -1 : 1
    const aLast = a.label === 'Container items' || a.label === 'Other'
    const bLast = b.label === 'Container items' || b.label === 'Other'
    if (aLast !== bLast) return aLast ? 1 : -1
    return a.label.localeCompare(b.label)
  })
}

export default function StorageTab() {
  const [rows, setRows] = useState<StorageRow[]>([])
  const [filter, setFilter] = useState('')
  const [ownerType, setOwnerType] = useState<OwnerType>('')
  const [selected, setSelected] = useState<number | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  useEffect(() => {
    const t = setTimeout(() => {
      const params = new URLSearchParams()
      if (filter) params.set('q', filter)
      if (ownerType) params.set('type', ownerType)
      api
        .get<StorageRow[]>(`/storage${params.toString() ? '?' + params : ''}`)
        .then((r) => {
          setRows(r || [])
          setErr(null)
        })
        .catch((e) => setErr((e as Error).message))
    }, 200)
    return () => clearTimeout(t)
  }, [filter, ownerType])

  const sidebar = useMemo(() => buildSidebar(buildGroups(rows)), [rows])

  const containsSelected = (g: Group) => g.rows.some((r) => r.id === selected)
  const bucketContainsSelected = (b: Bucket) => b.groups.some(containsSelected)

  // Active search expands every group + bucket (so matches are visible
  // immediately). Containers of the selected row also stay open.
  const isGroupExpanded = (g: Group) =>
    expanded.has(g.key) || filter.trim() !== '' || containsSelected(g)
  const isBucketExpanded = (b: Bucket) =>
    expanded.has(b.key) || filter.trim() !== '' || bucketContainsSelected(b)

  // Clicking a header toggles its expanded state. If we're collapsing and
  // the selection is inside, clear the selection first — otherwise the
  // 'force-expanded by selection' rule would override the collapse.
  const toggleAndMaybeDeselect = (key: string, open: boolean, hasSelected: boolean) => {
    if (open && hasSelected) setSelected(null)
    setExpanded((prev) => {
      const next = new Set(prev)
      if (open) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const renderGroup = (g: Group, indent: number) => {
    const single = g.rows.length === 1
    const open = isGroupExpanded(g)
    const hasSel = containsSelected(g)
    if (single) {
      const r = g.rows[0]
      return (
        <button
          key={g.key}
          className={`split-row ${selected === r.id ? 'active' : ''}`}
          onClick={() => setSelected(r.id)}
          title={metaTooltip(r)}
          style={indent ? { paddingLeft: indent } : undefined}
        >
          <span className="split-row-name">
            {g.name}
            {g.sub && <span style={{ opacity: 0.6 }}> · {g.sub}</span>}
            <span style={{ opacity: 0.7 }}> · {r.component_name || r.owner_item_template || typeLabel(r.inventory_type).text}</span>
          </span>
          <span className="split-row-meta mono">
            {r.item_count}/{cap(r.max_item_count)}
          </span>
        </button>
      )
    }
    const headerStyle: React.CSSProperties = { fontWeight: 600 }
    if (indent) headerStyle.paddingLeft = indent
    // Player groups with rows across multiple actors get sub-headers
    // (PlayerCharacter / LightOrnithopter / TotemSmall etc.) so the
    // operator can see at a glance which inventories belong to the
    // character vs to vehicles/totems the player owns.
    const subGroups = g.kind === 'player' ? buildSubGroups(g.rows) : null
    const useSubGroups = subGroups && subGroups.length > 1
    return (
      <div key={g.key}>
        <button
          className={`split-row ${hasSel ? 'active' : ''}`}
          onClick={() => toggleAndMaybeDeselect(g.key, open, hasSel)}
          style={headerStyle}
        >
          <span className="split-row-name">
            <span style={{ opacity: 0.6, width: 10, display: 'inline-block' }}>
              {open ? '▼' : '▶'}
            </span>
            {g.name}
            {g.sub && <span style={{ opacity: 0.6, fontWeight: 400 }}> · {g.sub}</span>}
          </span>
          <span className="split-row-meta mono">{g.rows.length} slots</span>
        </button>
        {open && (useSubGroups
          ? subGroups!.flatMap((sg) => [
              <div
                key={`sg-h:${sg.key}`}
                style={{
                  paddingLeft: indent + 28,
                  paddingTop: 6,
                  paddingBottom: 2,
                  fontSize: 11,
                  fontStyle: 'italic',
                  opacity: 0.65,
                }}
              >
                {sg.label}
              </div>,
              ...sg.rows.map((r) => renderSlotRow(r, indent + 40)),
            ])
          : g.rows.map((r) => renderSlotRow(r, indent + 28)))}
      </div>
    )
  }

  const renderSlotRow = (r: StorageRow, leftPad: number) => (
    <button
      key={r.id}
      className={`split-row ${selected === r.id ? 'active' : ''}`}
      onClick={() => setSelected(r.id)}
      title={metaTooltip(r)}
      style={{ paddingLeft: leftPad }}
    >
      <span className="split-row-name">
        {r.component_name || r.owner_item_template || typeLabel(r.inventory_type).text}
      </span>
      <span className="split-row-meta mono">
        {r.item_count}/{cap(r.max_item_count)}
      </span>
    </button>
  )

  const renderBucket = (b: Bucket) => {
    const open = isBucketExpanded(b)
    const hasSel = bucketContainsSelected(b)
    return (
      <div key={b.key}>
        <button
          className={`split-row ${hasSel ? 'active' : ''}`}
          onClick={() => toggleAndMaybeDeselect(b.key, open, hasSel)}
          style={{ fontWeight: 600 }}
        >
          <span className="split-row-name">
            <span style={{ opacity: 0.6, width: 10, display: 'inline-block' }}>
              {open ? '▼' : '▶'}
            </span>
            {b.name}
          </span>
          <span className="split-row-meta mono">× {b.groups.length}</span>
        </button>
        {open && b.groups.map((g) => renderGroup(g, 28))}
      </div>
    )
  }

  return (
    <div className="split">
      <aside className="split-side">
        <input
          className="search"
          placeholder="search by id, player name, container template"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <select
          className="input wide"
          value={ownerType}
          onChange={(e) => setOwnerType(e.target.value as OwnerType)}
        >
          {(Object.keys(OWNER_LABELS) as OwnerType[]).map((k) => (
            <option key={k} value={k}>
              {OWNER_LABELS[k]}
            </option>
          ))}
        </select>
        {err && <div className="alert">{err}</div>}
        <div className="split-list">
          {sidebar.map((item) =>
            item.kind === 'single' ? renderGroup(item.group, 0) : renderBucket(item.bucket),
          )}
          {sidebar.length === 0 && <div className="hint" style={{ padding: 8 }}>no inventories match.</div>}
        </div>
      </aside>

      <main className="split-main">
        {selected ? (
          <StorageDetail id={selected} />
        ) : (
          <div className="placeholder">
            <p>Pick an inventory on the left to see its contents.</p>
          </div>
        )}
      </main>
    </div>
  )
}

// Render a max-item-count for the sidebar meta cell: '?' / '∞' / N.
function cap(n: number | null): string {
  if (n === null) return '?'
  if (n < 0) return '∞'
  return String(n)
}

function labelForRow(row: StorageRow): string {
  if (row.owner_player_name) {
    const cls = shortClass(row.owner_actor_class)
    return cls
      ? `${row.owner_player_name} · ${cls} (#${row.id})`
      : `${row.owner_player_name} (#${row.id})`
  }
  if (row.owner_item_template) return `${row.owner_item_template} (#${row.id})`
  if (row.exchange_id) return `exchange #${row.exchange_id} (inv ${row.id})`
  if (row.vehicle_module_id) return `vmodule #${row.vehicle_module_id} (inv ${row.id})`
  if (row.owner_actor_class) {
    const cls = shortClass(row.owner_actor_class)
    return `${cls} #${row.actor_id} (inv ${row.id})`
  }
  if (row.actor_id) return `actor #${row.actor_id} (inv ${row.id})`
  return `orphan inv #${row.id}`
}

// inventory_type → EInventoryType enum name, reversed from the
// game-server binary. The enum's 24 named values were extracted from
// the UEnum reflection metadata table at .data.rel.ro 0x14ec0080;
// every observed DB type lines up with a name once the table's
// off-by-one value-storage layout is accounted for. Two SQL comments
// in the schema files (`inventory_type 0 is backpack`, `inventory_type
// = 29 -- EInventoryType::ContractsInventory = 29`) anchor the
// mapping. Content checks confirm the rest:
//   14 = Spellbook (11 Emote_* items, cap 100)
//   15 = RadialMenuShortcuts (8 quick tools — knife, mining tool, …)
//   22 = PlayerDroppedLoot (PlantFiber, ScrapMetal, Stone)
//   27 = EmoteRadialMenuShortcuts (8 Thumper / Watershipper emotes)
//   30 = PlayerBank (cap 500)
const TYPE_LABELS: Record<number, string> = {
  0: 'Backpack',
  1: 'Equipment',
  3: 'PlaceableInventory',
  4: 'DedicatedStorageInventory',
  12: 'CraftingIngredientsInventory',
  14: 'Spellbook',
  15: 'RadialMenuShortcuts',
  17: 'VehicleAbilities',
  18: 'VehicleAbilityShortcuts',
  19: 'VehicleAmmunition',
  20: 'P2pTradingInventory',
  21: 'LootContainer',
  22: 'PlayerDroppedLoot',
  23: 'PersonalLootContainer',
  24: 'WeaponModsInventory',
  25: 'InfluenceInventory',
  27: 'EmoteRadialMenuShortcuts',
  28: 'NpcLootInventory',
  29: 'ContractsInventory',
  30: 'PlayerBank',
  31: 'TransactionalInventory',
  32: 'DeliveryInventory',
  33: 'PlayerInboxInventory',
  255: 'Invalid',
}

function typeLabel(t: number | null): { text: string; confident: boolean } {
  if (t === null) return { text: '?', confident: false }
  const e = TYPE_LABELS[t]
  if (e) return { text: e, confident: true }
  return { text: `t${t}`, confident: false }
}

function metaTooltip(row: StorageRow): string {
  const parts = [
    `inventory_type=${row.inventory_type ?? '?'}`,
    `component_name=${row.component_name || '?'}`,
    `component_name_hash=${row.component_name_hash ?? '?'}`,
    `items=${row.item_count}`,
    `max_item_count=${row.max_item_count ?? '?'}`,
    `max_item_volume=${row.max_item_volume ?? '?'}`,
  ]
  return parts.join(' · ')
}

// Trim Unreal class paths like
// /Game/Dune/Characters/Player/BP_DunePlayerCharacter.BP_DunePlayerCharacter_C
// down to a short readable form like 'PlayerCharacter'.
function shortClass(s: string | null): string {
  if (!s) return ''
  let out = s
  const slash = out.lastIndexOf('/')
  if (slash >= 0) out = out.slice(slash + 1)
  const dot = out.indexOf('.')
  if (dot >= 0) out = out.slice(dot + 1)
  out = out.replace(/_C$/, '')
  out = out.replace(/^BP_Dune/, '')
  out = out.replace(/^BP_/, '')
  return out
}

function StorageDetail({ id }: { id: number }) {
  const [d, setD] = useState<StorageDetail | null>(null)
  const [err, setErr] = useState<string | null>(null)

  const reload = () =>
    api
      .get<StorageDetail>(`/storage/${id}`)
      .then((data) => {
        setD(data)
        setErr(null)
      })
      .catch((e) => setErr((e as Error).message))

  useEffect(() => {
    reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  if (err) return <div className="alert">{err}</div>
  if (!d) return <div className="placeholder">loading…</div>

  const inv = d.inventory

  return (
    <>
      <div className="card">
        <div className="player-header">
          <div>
            <div className="player-name">{labelForRow(inv)}</div>
            <div className="player-meta mono">
              inventory id {inv.id} ·
              slot {inv.component_name || `hash ${inv.component_name_hash ?? '?'}`} ·
              type {typeLabel(inv.inventory_type).text} ·
              max items {inv.max_item_count === null ? '?' : inv.max_item_count < 0 ? '∞' : inv.max_item_count} ·
              max volume {inv.max_item_volume ?? '∞'}
            </div>
          </div>
        </div>
      </div>

      <div className="card">
        <h3 className="card-title">
          Items <span className="card-title-count">{d.items.length}</span>
        </h3>
        {d.items.length === 0 ? (
          <div className="hint">empty inventory</div>
        ) : (
          <div className="grid-wrap">
            <table className="grid compact">
              <thead>
                <tr>
                  {Object.keys(d.items[0]).map((c) => (
                    <th key={c}>{c}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {d.items.map((it, i) => (
                  <tr key={i}>
                    {Object.keys(d.items[0]).map((c) => (
                      <td key={c} className="mono">
                        {it[c] === null || it[c] === undefined ? '∅' : String(it[c])}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <GiveItemForm inventoryId={inv.id} onDone={reload} />
      </div>
    </>
  )
}

function GiveItemForm({ inventoryId, onDone }: { inventoryId: number; onDone: () => void }) {
  const [templateId, setTemplateId] = useState('')
  const [stack, setStack] = useState('1')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api.post(`/storage/${inventoryId}/give-item`, {
        template_id: templateId,
        stack_size: Number(stack),
      })
      onDone()
      setTemplateId('')
      setStack('1')
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="action-row" onSubmit={submit}>
      <input
        className="input"
        placeholder="template id (e.g. SolarisCoin)"
        value={templateId}
        onChange={(e) => setTemplateId(e.target.value)}
      />
      <input
        className="input small"
        placeholder="stack"
        value={stack}
        onChange={(e) => setStack(e.target.value)}
      />
      <button className="btn" type="submit" disabled={busy || !templateId}>
        Give item
      </button>
      {err && <span className="alert inline">{err}</span>}
    </form>
  )
}
