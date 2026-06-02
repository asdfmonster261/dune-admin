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
  kind: 'player' | 'npc' | 'exchange' | 'item' | 'vehicle' | 'orphan'
  name: string
  sub?: string
  rows: StorageRow[]
}

const KIND_ORDER: Record<Group['kind'], number> = {
  player: 0, npc: 1, exchange: 2, vehicle: 3, item: 4, orphan: 5,
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
      sub = shortClass(r.owner_actor_class) || undefined
    } else if (r.exchange_id) {
      key = `e:${r.exchange_id}`
      kind = 'exchange'
      name = `Exchange #${r.exchange_id}`
    } else if (r.item_id) {
      key = `i:${r.item_id}`
      kind = 'item'
      name = r.owner_item_template || `Container item #${r.item_id}`
      sub = `item ${r.item_id}`
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
  }
  return [...byKey.values()].sort((a, b) => {
    if (KIND_ORDER[a.kind] !== KIND_ORDER[b.kind]) return KIND_ORDER[a.kind] - KIND_ORDER[b.kind]
    return a.name.localeCompare(b.name)
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

  const groups = useMemo(() => buildGroups(rows), [rows])

  // Active search expands every group (so matches are visible immediately).
  // The group containing the currently-selected row also stays open.
  const isExpanded = (g: Group) =>
    expanded.has(g.key) || filter.trim() !== '' || g.rows.some((r) => r.id === selected)

  // Clicking a group header toggles its expanded state. If the group
  // contains the currently-selected row, also clear the selection — that
  // way one click on the owner name fully backs out of the group rather
  // than getting stuck in a "force-expanded by selection" state.
  const onGroupClick = (g: Group) => {
    const open = isExpanded(g)
    if (open && g.rows.some((r) => r.id === selected)) {
      setSelected(null)
    }
    setExpanded((prev) => {
      const next = new Set(prev)
      if (open) next.delete(g.key)
      else next.add(g.key)
      return next
    })
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
          {groups.map((g) => {
            const single = g.rows.length === 1
            const open = isExpanded(g)
            const groupHasSelected = g.rows.some((r) => r.id === selected)
            // For 1-slot groups, render flat (no expand toggle). Clicking
            // selects the only row directly.
            if (single) {
              const r = g.rows[0]
              return (
                <button
                  key={g.key}
                  className={`split-row ${selected === r.id ? 'active' : ''}`}
                  onClick={() => setSelected(r.id)}
                  title={metaTooltip(r)}
                >
                  <span className="split-row-name">
                    {g.name}
                    {g.sub && <span style={{ opacity: 0.6 }}> · {g.sub}</span>}
                    <span style={{ opacity: 0.7 }}> · {r.component_name || typeLabel(r.inventory_type).text}</span>
                  </span>
                  <span className="split-row-meta mono">
                    {r.item_count}/{cap(r.max_item_count)}
                  </span>
                </button>
              )
            }
            return (
              <div key={g.key}>
                <button
                  className={`split-row ${groupHasSelected ? 'active' : ''}`}
                  onClick={() => onGroupClick(g)}
                  style={{ fontWeight: 600 }}
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
                {open && g.rows.map((r) => (
                  <button
                    key={r.id}
                    className={`split-row ${selected === r.id ? 'active' : ''}`}
                    onClick={() => setSelected(r.id)}
                    title={metaTooltip(r)}
                    style={{ paddingLeft: 28 }}
                  >
                    <span className="split-row-name">
                      {r.component_name || typeLabel(r.inventory_type).text}
                    </span>
                    <span className="split-row-meta mono">
                      {r.item_count}/{cap(r.max_item_count)}
                    </span>
                  </button>
                ))}
              </div>
            )
          })}
          {groups.length === 0 && <div className="hint" style={{ padding: 8 }}>no inventories match.</div>}
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
