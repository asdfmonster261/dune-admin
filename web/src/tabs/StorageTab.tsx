import { useEffect, useState } from 'react'
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

export default function StorageTab() {
  const [rows, setRows] = useState<StorageRow[]>([])
  const [filter, setFilter] = useState('')
  const [ownerType, setOwnerType] = useState<OwnerType>('')
  const [selected, setSelected] = useState<number | null>(null)
  const [err, setErr] = useState<string | null>(null)

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
          {rows.map((row) => (
            <button
              key={row.id}
              className={`split-row ${selected === row.id ? 'active' : ''}`}
              onClick={() => setSelected(row.id)}
            >
              <span className="split-row-name">{labelForRow(row)}</span>
              <span className="split-row-meta mono" title={metaTooltip(row)}>
                {metaSummary(row)}
              </span>
            </button>
          ))}
          {rows.length === 0 && <div className="hint" style={{ padding: 8 }}>no inventories match.</div>}
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

// Compact meta string for the row: '<label> · <count>/<max>'.
// Falls back to 't<type> · <count>/<max>' for unknown / low-confidence
// types. Unlimited inventories (max_item_count = -1) render as ∞.
function metaSummary(row: StorageRow): string {
  const t = typeLabel(row.inventory_type)
  const max =
    row.max_item_count === null
      ? '?'
      : row.max_item_count < 0
        ? '∞'
        : String(row.max_item_count)
  return `${t.text} · ${row.item_count}/${max}`
}

function metaTooltip(row: StorageRow): string {
  const parts = [
    `inventory_type=${row.inventory_type ?? '?'}`,
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
              inventory id {inv.id} · type {inv.inventory_type ?? '?'} ·
              max items {inv.max_item_count ?? '∞'} ·
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
