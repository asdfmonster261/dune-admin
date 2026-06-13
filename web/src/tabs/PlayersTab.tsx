import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'

type Player = {
  id: number
  account_id: number
  name: string
  online_status: string
  life_state: string
  last_login: string | null
  platform_name: string | null
  platform_id: string | null
  funcom_id: string | null
}

type Currency = { currency_id: number; balance: number }
type Faction = { faction_id: number; name: string | null; reputation: number; scrips: number }
type InventoryItem = Record<string, unknown>

type PlayerDetailRow = Player & {
  server_id?: string
  player_controller_id?: number
  player_pawn_id?: number
  fls_id?: string
}

type Detail = {
  player: PlayerDetailRow
  currencies: Currency[]
  factions: Faction[]
  inventory: InventoryItem[]
}

type PlayersTabProps = {
  // Cross-tab navigation: when MapTab punches through to a player by
  // clicking their dot, App sets this to the player_state_id we should
  // open on mount. After we consume it we call onConsumed so it doesn't
  // re-trigger if the operator just navigates between tabs later.
  initialSelectedId?: number | null
  onConsumed?: () => void
}

export default function PlayersTab({ initialSelectedId, onConsumed }: PlayersTabProps = {}) {
  const [list, setList] = useState<Player[]>([])
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState<number | null>(null)
  const [err, setErr] = useState<string | null>(null)

  // Consume the cross-tab hint on mount. Runs once; if the operator
  // later picks a different player here, the hint stays null.
  useEffect(() => {
    if (initialSelectedId != null) {
      setSelected(initialSelectedId)
      onConsumed?.()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Initial + on-search load
  useEffect(() => {
    const t = setTimeout(() => {
      const qs = filter ? `?q=${encodeURIComponent(filter)}` : ''
      api
        .get<Player[]>(`/players${qs}`)
        .then((rows) => {
          setList(rows || [])
          setErr(null)
        })
        .catch((e) => setErr((e as Error).message))
    }, 200)
    return () => clearTimeout(t)
  }, [filter])

  const filtered = useMemo(() => list, [list])

  return (
    <div className="split">
      <aside className="split-side">
        <input
          className="search"
          placeholder="search by character or platform name"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        {err && <div className="alert">{err}</div>}
        <div className="split-list">
          {filtered.map((p) => (
            <button
              key={p.id}
              className={`split-row ${selected === p.id ? 'active' : ''}`}
              onClick={() => setSelected(p.id)}
            >
              <span className="split-row-name">
                {p.name || <em className="dim">(unnamed)</em>}
                {p.online_status?.toLowerCase() === 'online' && <span className="online-dot" />}
              </span>
              <span className="split-row-meta mono">
                {p.platform_name ?? '?'}
              </span>
            </button>
          ))}
        </div>
      </aside>

      <main className="split-main">
        {selected ? <PlayerDetail id={selected} /> : (
          <div className="placeholder">
            <p>Pick a character on the left.</p>
          </div>
        )}
      </main>
    </div>
  )
}

function PlayerDetail({ id }: { id: number }) {
  const [d, setD] = useState<Detail | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const reload = () =>
    api
      .get<Detail>(`/players/${id}`)
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

  return (
    <>
      <div className="card">
        <div className="player-header">
          <div>
            <div className="player-name">{d.player.name || '(unnamed)'}</div>
            <div className="player-meta mono">
              id {d.player.id} · account {d.player.account_id} ·{' '}
              {d.player.platform_name ?? '?'}
            </div>
          </div>
          <div className="player-status">
            <span className={`pill ${d.player.online_status?.toLowerCase() === 'online' ? 'ok' : ''}`}>
              <span className="dot" />
              {d.player.online_status || 'unknown'}
            </span>
            {d.player.last_login && (
              <span className="hint mono">last {new Date(d.player.last_login).toLocaleString()}</span>
            )}
          </div>
        </div>
      </div>

      <div className="two-col">
        <div className="card">
          <h3 className="card-title">
            Currencies <span className="card-title-count">{d.currencies.length}</span>
          </h3>
          {d.currencies.length === 0 ? (
            <div className="hint">no virtual currency balances</div>
          ) : (
            <table className="grid compact">
              <thead>
                <tr><th>id</th><th>balance</th></tr>
              </thead>
              <tbody>
                {d.currencies.map((c) => (
                  <tr key={c.currency_id}>
                    <td className="mono">{c.currency_id}</td>
                    <td className="mono num">{c.balance}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {d.player.fls_id && (
            <GiveCurrencyForm flsId={d.player.fls_id} onDone={reload} />
          )}
        </div>

        <div className="card">
          <h3 className="card-title">
            Factions <span className="card-title-count">{d.factions.length}</span>
          </h3>
          {d.factions.length === 0 ? (
            <div className="hint">no faction reputation rows</div>
          ) : (
            <table className="grid compact">
              <thead>
                <tr><th>faction</th><th>rep</th><th>scrips</th></tr>
              </thead>
              <tbody>
                {d.factions.map((f) => (
                  <tr key={f.faction_id}>
                    <td>{f.name ?? `#${f.faction_id}`}</td>
                    <td className="mono num">{f.reputation}</td>
                    <td className="mono num">{f.scrips}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {d.player.fls_id && (
            <SetFactionRepForm flsId={d.player.fls_id} onDone={reload} />
          )}
        </div>
      </div>

      <div className="card">
        <h3 className="card-title">
          Inventory <span className="card-title-count">{d.inventory.length}</span>
        </h3>
        {d.inventory.length === 0 ? (
          <div className="hint">no items</div>
        ) : (
          <div className="grid-wrap">
            <InventoryTable rows={d.inventory} />
          </div>
        )}
        {d.player.fls_id && (
          <GiveItemForm flsId={d.player.fls_id} onDone={reload} />
        )}
      </div>
    </>
  )
}

// All three write forms now route through OpsBridge → DuneCheatManager
// instead of direct DB writes, so changes take effect immediately on
// the live game-server (no player relog required — see the
// dune-db-edit-cache memory).

function GiveCurrencyForm({
  flsId,
  onDone,
}: {
  flsId: string
  onDone: () => void
}) {
  const [qty, setQty] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api.post('/players/give-currency', {
        player_id: flsId,
        quantity: Number(qty),
      })
      onDone()
      setQty('')
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="action-row" onSubmit={submit}>
      <input
        className="input small"
        placeholder="solaris to add"
        value={qty}
        onChange={(e) => setQty(e.target.value)}
      />
      <button className="btn" type="submit" disabled={busy || !qty}>
        Add solaris
      </button>
      {err && <span className="alert inline">{err}</span>}
    </form>
  )
}

function GiveItemForm({
  flsId,
  onDone,
}: {
  flsId: string
  onDone: () => void
}) {
  const [itemName, setItemName] = useState('')
  const [qty, setQty] = useState('1')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api.post('/players/give-item', {
        player_id: flsId,
        item_name: itemName,
        quantity: Number(qty),
      })
      onDone()
      setItemName('')
      setQty('1')
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
        placeholder="item name (e.g. SalvageMetal)"
        value={itemName}
        onChange={(e) => setItemName(e.target.value)}
      />
      <input
        className="input small"
        placeholder="qty"
        value={qty}
        onChange={(e) => setQty(e.target.value)}
      />
      <button className="btn" type="submit" disabled={busy || !itemName || !qty}>
        Give item
      </button>
      {err && <span className="alert inline">{err}</span>}
    </form>
  )
}

function SetFactionRepForm({
  flsId,
  onDone,
}: {
  flsId: string
  onDone: () => void
}) {
  const [faction, setFaction] = useState('Atreides')
  const [amount, setAmount] = useState('')
  const [mode, setMode] = useState<'set' | 'add'>('set')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api.post('/players/set-faction-rep', {
        player_id: flsId,
        faction_name: faction,
        amount: Number(amount),
        mode,
      })
      onDone()
      setAmount('')
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="action-row" onSubmit={submit}>
      <select
        className="input small"
        value={faction}
        onChange={(e) => setFaction(e.target.value)}
      >
        <option value="Atreides">Atreides</option>
        <option value="Harkonnen">Harkonnen</option>
        <option value="Neutral">Neutral</option>
        <option value="Smugglers">Smugglers</option>
      </select>
      <select
        className="input small"
        value={mode}
        onChange={(e) => setMode(e.target.value as 'set' | 'add')}
      >
        <option value="set">Set</option>
        <option value="add">Add</option>
      </select>
      <input
        className="input small"
        placeholder="amount"
        value={amount}
        onChange={(e) => setAmount(e.target.value)}
      />
      <button className="btn" type="submit" disabled={busy || !amount}>
        Apply
      </button>
      {err && <span className="alert inline">{err}</span>}
    </form>
  )
}

function InventoryTable({ rows }: { rows: InventoryItem[] }) {
  const cols = Object.keys(rows[0])
  return (
    <table className="grid compact">
      <thead>
        <tr>
          {cols.map((c) => (
            <th key={c}>{c}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={i}>
            {cols.map((c) => (
              <td key={c} className="mono">
                {r[c] === null || r[c] === undefined ? '∅' : String(r[c])}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  )
}
