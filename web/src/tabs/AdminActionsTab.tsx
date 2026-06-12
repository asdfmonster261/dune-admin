import { useEffect, useMemo, useState } from 'react'
import { api, type Status } from '../api'

// Phase 10 D1 — live execute UI.
//
// The 27-mode "envelope preview" surface from Phase 5 was Snapetech
// speculation built before the dispatcher envelope was actually RE'd.
// Now that GMCommand dispatches through OpsBridge for real, the UI is
// just: pick a command, fill its catalog-declared params, execute.
//
// Source of truth is /api/v1/gm/v2/catalog — the Go side declares
// every command's name, tier, kind (native/synth), status, and param
// schema. UI renders dynamically from that.

type GMParamType = 'string' | 'int' | 'float' | 'player' | 'node' | 'item'

type GMParam = {
  name: string
  type: GMParamType
  required: boolean
  placeholder?: string
  min?: number
  max?: number
  help?: string
}

type GMEntry = {
  name: string
  tier: string
  kind: 'native' | 'synth'
  status: 'live' | 'needs-probe' | 'deferred'
  notes?: string
  params: GMParam[]
}

type GMPlayer = {
  name: string
  player_id: string
  id_type?: string
}

type ItemRow = {
  id: string
  name?: string
}

type ExecuteResponse = {
  ok: boolean
  command: string
  reply: unknown
}

// Display order matches the rough tier risk gradient. Anything not in
// this list falls to the end alphabetically.
const TIER_ORDER = [
  'comms',
  'safe',
  'movement',
  'inventory',
  'progression',
  'journey',
  'spawn',
  'player',
  'destructive',
  'console',
]

export default function AdminActionsTab() {
  const [catalog, setCatalog] = useState<GMEntry[]>([])
  const [players, setPlayers] = useState<GMPlayer[]>([])
  // Journey-node autocomplete list. Fetched on first render of any
  // entry that declares a `node` param; cached for the tab's lifetime
  // (the Go-side cache TTL is 10 min, the Lua-side is the same — design
  // data doesn't change between server restarts).
  const [journeyNodes, setJourneyNodes] = useState<string[]>([])
  // Item-template autocomplete list. Static embed in dune-admin binary
  // (regenerated from server paks via /workspace/dune-pak-tools).
  // Shape: { id: "Radiation_Suit", name: "Radiation Suit Mk4" } — name
  // is optional and falls back to the id in the UI.
  const [items, setItems] = useState<ItemRow[]>([])
  const [status, setStatus] = useState<Status | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [filter, setFilter] = useState('')
  const [args, setArgs] = useState<Record<string, string>>({})
  const [result, setResult] = useState<ExecuteResponse | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    api
      .get<GMEntry[]>('/gm/v2/catalog')
      .then(setCatalog)
      .catch((e) => setErr((e as Error).message))
    const refresh = () => {
      api.get<GMPlayer[]>('/gm/v2/players').then(setPlayers).catch(() => {})
      api.get<Status>('/status').then(setStatus).catch(() => {})
    }
    refresh()
    const id = setInterval(refresh, 5000)
    return () => clearInterval(id)
  }, [])

  // Fetch journey-node list lazily — only when the user selects an entry
  // that declares a `node` param. Skip if we already have it; the server
  // caches 10 min and the list is ~1964 entries (~100 KB).
  useEffect(() => {
    if (journeyNodes.length > 0) return
    const entry = catalog.find((c) => c.name === selected)
    if (!entry) return
    if (!(entry.params ?? []).some((p) => p.type === 'node')) return
    api.get<string[]>('/gm/v2/journey/nodes').then(setJourneyNodes).catch(() => {})
  }, [selected, catalog, journeyNodes.length])

  // Same lazy-load pattern for the item-template list.
  useEffect(() => {
    if (items.length > 0) return
    const entry = catalog.find((c) => c.name === selected)
    if (!entry) return
    if (!(entry.params ?? []).some((p) => p.type === 'item')) return
    api.get<ItemRow[]>('/gm/v2/items').then(setItems).catch(() => {})
  }, [selected, catalog, items.length])

  const filtered = useMemo(() => {
    if (!filter) return catalog
    const f = filter.toLowerCase()
    return catalog.filter(
      (c) =>
        c.name.toLowerCase().includes(f) ||
        c.tier.toLowerCase().includes(f) ||
        c.status.toLowerCase().includes(f) ||
        c.kind.toLowerCase().includes(f),
    )
  }, [catalog, filter])

  const byTier = useMemo(() => {
    const groups: Record<string, GMEntry[]> = {}
    for (const e of filtered) {
      ;(groups[e.tier] ??= []).push(e)
    }
    const tiers = Object.keys(groups).sort((a, b) => {
      const ia = TIER_ORDER.indexOf(a)
      const ib = TIER_ORDER.indexOf(b)
      if (ia !== -1 && ib !== -1) return ia - ib
      if (ia !== -1) return -1
      if (ib !== -1) return 1
      return a.localeCompare(b)
    })
    return tiers.map((t) => [t, groups[t]] as const)
  }, [filtered])

  const selectedEntry = useMemo(
    () => catalog.find((c) => c.name === selected) ?? null,
    [catalog, selected],
  )

  // Reset args + result when selection changes
  useEffect(() => {
    setArgs({})
    setResult(null)
    setErr(null)
  }, [selected])

  const opsbridgeOffline = !!status && !status.opsbridge_connected

  const execute = async () => {
    if (!selectedEntry) return
    setSubmitting(true)
    setErr(null)
    setResult(null)
    try {
      const coerced: Record<string, unknown> = {}
      for (const p of selectedEntry.params ?? []) {
        const v = args[p.name]
        if (v === undefined || v === '') continue
        if (p.type === 'int') {
          const n = parseInt(v, 10)
          if (!Number.isFinite(n)) throw new Error(`${p.name}: not a valid integer`)
          coerced[p.name] = n
        } else if (p.type === 'float') {
          const n = parseFloat(v)
          if (!Number.isFinite(n)) throw new Error(`${p.name}: not a valid number`)
          coerced[p.name] = n
        } else {
          coerced[p.name] = v
        }
      }
      const r = await api.post<ExecuteResponse>('/gm/v2/execute', {
        command: selectedEntry.name,
        args: coerced,
      })
      setResult(r)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  const canExecute =
    !!selectedEntry &&
    selectedEntry.status === 'live' &&
    !opsbridgeOffline &&
    !submitting

  return (
    <>
      {opsbridgeOffline && (
        <div className="card warn-card">
          <div className="card-title">OpsBridge offline</div>
          <p className="hint">
            GM commands need a live OpsBridge connection to
            game-server-survival:9877. Execute is disabled until the
            survival container is reachable.
          </p>
        </div>
      )}

      {err && <div className="alert">{err}</div>}

      <div className="split">
        <aside className="split-side">
          <input
            className="search"
            placeholder={`search ${catalog.length} commands`}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <div className="split-list">
            {byTier.map(([tier, entries]) => (
              <div key={tier}>
                <div className="split-group-label">{tier}</div>
                {entries.map((c) => (
                  <button
                    key={c.name}
                    className={`split-row ${selected === c.name ? 'active' : ''}`}
                    onClick={() => setSelected(c.name)}
                  >
                    <span className="split-row-name">{c.name}</span>
                    <span className={`split-row-meta mono ${statusClass(c.status)}`}>
                      {c.status}
                    </span>
                  </button>
                ))}
              </div>
            ))}
            {byTier.length === 0 && (
              <div className="hint" style={{ padding: 12 }}>
                {catalog.length === 0
                  ? 'Loading catalog…'
                  : 'No commands match the filter.'}
              </div>
            )}
          </div>
        </aside>

        <main className="split-main">
          {selectedEntry ? (
            <>
              <div className="card">
                <h3 className="card-title">
                  {selectedEntry.name}
                  <span className={`pill ${statusPill(selectedEntry.status)}`}>
                    <span className="dot" />
                    {selectedEntry.status}
                  </span>
                </h3>
                <div className="kv-grid">
                  <div className="kv">
                    <span className="kv-key">tier</span>
                    <span className="kv-val">{selectedEntry.tier}</span>
                  </div>
                  <div className="kv">
                    <span className="kv-key">kind</span>
                    <span className="kv-val mono">{selectedEntry.kind}</span>
                  </div>
                </div>
                {selectedEntry.notes && (
                  <p className="hint">{selectedEntry.notes}</p>
                )}
              </div>

              <div className="card">
                <h3 className="card-title">Execute</h3>
                {(selectedEntry.params ?? []).length === 0 && (
                  <p className="hint">No parameters — Execute fires immediately.</p>
                )}
                {(selectedEntry.params ?? []).map((p) => (
                  <ParamInput
                    key={p.name}
                    param={p}
                    value={args[p.name]}
                    players={players}
                    journeyNodes={journeyNodes}
                    items={items}
                    onChange={(v) =>
                      setArgs((prev) => ({ ...prev, [p.name]: v }))
                    }
                  />
                ))}
                <div className="actions-row">
                  <button
                    className="btn primary"
                    onClick={execute}
                    disabled={!canExecute}
                  >
                    {submitting ? 'Sending…' : 'Execute'}
                  </button>
                  {selectedEntry.status !== 'live' && (
                    <span className="hint">
                      Not yet implemented (status={selectedEntry.status})
                    </span>
                  )}
                </div>
                {result && (
                  <pre className="json-preview">
                    {JSON.stringify(result, null, 2)}
                  </pre>
                )}
              </div>
            </>
          ) : (
            <div className="card">
              <p className="hint">Select a command from the sidebar to start.</p>
            </div>
          )}
        </main>
      </div>
    </>
  )
}

function ParamInput({
  param,
  value,
  players,
  journeyNodes,
  items,
  onChange,
}: {
  param: GMParam
  value: string | undefined
  players: GMPlayer[]
  journeyNodes: string[]
  items: ItemRow[]
  onChange: (v: string) => void
}) {
  if (param.type === 'player') {
    return (
      <>
        <label className="field-label">
          {param.name}
          {param.required && <span className="req">*</span>}
        </label>
        {players.length > 0 ? (
          <select
            className="input wide"
            value={value ?? ''}
            onChange={(e) => onChange(e.target.value)}
          >
            <option value="">(select a player)</option>
            {players.map((p) => (
              <option key={p.player_id} value={p.player_id}>
                {p.name} — {p.player_id}
              </option>
            ))}
          </select>
        ) : (
          <input
            className="input wide"
            value={value ?? ''}
            onChange={(e) => onChange(e.target.value)}
            placeholder={param.placeholder ?? 'FLS hex string'}
          />
        )}
        {param.help && <p className="hint">{param.help}</p>}
      </>
    )
  }
  if (param.type === 'node') {
    // Free-text input with a <datalist> for native browser autocomplete.
    // datalist tolerates a 2k-entry option list without perceptible
    // slowdown in modern browsers; the canonical FindTheFremen tree
    // alone is 46 entries, the full design set ~1964. Lower-case
    // substring match is browser-native — no client-side filter needed.
    return (
      <>
        <label className="field-label">
          {param.name}
          {param.required && <span className="req">*</span>}
        </label>
        <input
          className="input wide"
          list="gm-journey-nodes"
          value={value ?? ''}
          onChange={(e) => onChange(e.target.value)}
          placeholder={param.placeholder ?? 'DataAsset.UniqueName[.UniqueName...]'}
        />
        <datalist id="gm-journey-nodes">
          {journeyNodes.map((n) => (
            <option key={n} value={n} />
          ))}
        </datalist>
        <p className="hint">
          {journeyNodes.length > 0
            ? `${journeyNodes.length} known node IDs — start typing to filter, or paste a full path. Completing a parent cascades to descendants.`
            : 'Loading node list from game-server…'}
        </p>
        {param.help && <p className="hint">{param.help}</p>}
      </>
    )
  }
  if (param.type === 'item') {
    // Item template autocomplete — same datalist pattern as `node`,
    // backed by the static catalog regenerated from server paks.
    return (
      <>
        <label className="field-label">
          {param.name}
          {param.required && <span className="req">*</span>}
        </label>
        <input
          className="input wide"
          list="gm-item-templates"
          value={value ?? ''}
          onChange={(e) => onChange(e.target.value)}
          placeholder={param.placeholder ?? 'SalvageMetal'}
        />
        <datalist id="gm-item-templates">
          {items.map((it) => (
            // Browser datalist: `value` is what fills the input on
            // select (always the template id); `label` is what's
            // shown in the dropdown row (the display name when we
            // have one, otherwise the id alone).
            <option
              key={it.id}
              value={it.id}
              label={it.name ? `${it.name}  (${it.id})` : it.id}
            />
          ))}
        </datalist>
        <p className="hint">
          {items.length > 0
            ? `${items.length} known item templates (${items.filter((i) => i.name).length} with display names) — autocomplete is a best-effort catalog from server paks; the dispatcher accepts any valid template name even if it isn't listed.`
            : 'Loading item template list…'}
        </p>
        {param.help && <p className="hint">{param.help}</p>}
      </>
    )
  }
  const isNumeric = param.type === 'int' || param.type === 'float'
  return (
    <>
      <label className="field-label">
        {param.name}
        {param.required && <span className="req">*</span>}
      </label>
      <input
        className="input wide"
        type={isNumeric ? 'number' : 'text'}
        min={param.min}
        max={param.max}
        step={param.type === 'float' ? 'any' : 1}
        value={value ?? ''}
        onChange={(e) => onChange(e.target.value)}
        placeholder={param.placeholder}
      />
      {param.help && <p className="hint">{param.help}</p>}
    </>
  )
}

function statusClass(s: string): string {
  switch (s) {
    case 'live':
      return 'tag-ok'
    case 'needs-probe':
    case 'deferred':
      return 'tag-dim'
    default:
      return ''
  }
}

function statusPill(s: string): string {
  switch (s) {
    case 'live':
      return 'ok'
    case 'needs-probe':
    case 'deferred':
      return 'bad'
    default:
      return ''
  }
}
