import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'

type GMCommand = {
  name: string
  tier: string
  status: string
  syntax: string
  chat?: string
  notes?: string
}

type Catalog = {
  commands: GMCommand[]
  modes: string[]
  execution: { default: string; reason: string }
}

type Preview = {
  mode: string
  envelope: unknown
}

export default function AdminActionsTab() {
  const [catalog, setCatalog] = useState<Catalog | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [commandText, setCommandText] = useState('')
  const [mode, setMode] = useState('service-message')
  const [targetPlayer, setTargetPlayer] = useState('')
  const [adminPlayer, setAdminPlayer] = useState('')
  const [preview, setPreview] = useState<Preview | null>(null)
  const [previewErr, setPreviewErr] = useState<string | null>(null)
  const [filter, setFilter] = useState('')

  useEffect(() => {
    api
      .get<Catalog>('/gm/catalog')
      .then(setCatalog)
      .catch((e) => setErr((e as Error).message))
  }, [])

  const filtered = useMemo(() => {
    if (!catalog) return []
    const f = filter.toLowerCase()
    return catalog.commands.filter(
      (c) =>
        c.name.toLowerCase().includes(f) ||
        c.tier.toLowerCase().includes(f) ||
        c.status.toLowerCase().includes(f),
    )
  }, [catalog, filter])

  const renderPreview = async () => {
    if (!commandText.trim()) {
      setPreview(null)
      setPreviewErr('command text is required')
      return
    }
    try {
      const p = await api.post<Preview>('/gm/preview', {
        command_text: commandText,
        mode,
        target_player: targetPlayer,
        admin_player: adminPlayer,
      })
      setPreview(p)
      setPreviewErr(null)
    } catch (e) {
      setPreview(null)
      setPreviewErr((e as Error).message)
    }
  }

  return (
    <>
      {err && <div className="alert">{err}</div>}

      <div className="card warn-card">
        <div className="card-title">Preview only</div>
        <p className="hint">
          {catalog?.execution?.reason ??
            'The live RabbitMQ payload contract is unverified. Preview shows what would be published; execution is intentionally not wired.'}
        </p>
      </div>

      <div className="split">
        <aside className="split-side">
          <input
            className="search"
            placeholder={`search ${catalog?.commands.length ?? 0} commands`}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <div className="split-list">
            {filtered.map((c) => (
              <button
                key={c.name}
                className={`split-row ${selected === c.name ? 'active' : ''}`}
                onClick={() => {
                  setSelected(c.name)
                  setCommandText(c.name)
                }}
              >
                <span className="split-row-name">{c.name}</span>
                <span className="split-row-meta mono">{c.tier}</span>
              </button>
            ))}
          </div>
        </aside>

        <main className="split-main">
          {selected && catalog && (
            <div className="card">
              {(() => {
                const c = catalog.commands.find((x) => x.name === selected)!
                return (
                  <>
                    <h3 className="card-title">
                      {c.name}
                      <span className={`pill ${statusPill(c.status)}`}>
                        <span className="dot" />
                        {c.status}
                      </span>
                    </h3>
                    <div className="kv-grid">
                      <div className="kv">
                        <span className="kv-key">tier</span>
                        <span className="kv-val">{c.tier}</span>
                      </div>
                      <div className="kv">
                        <span className="kv-key">syntax</span>
                        <span className="kv-val mono">{c.syntax}</span>
                      </div>
                      {c.chat && (
                        <div className="kv">
                          <span className="kv-key">chat</span>
                          <span className="kv-val mono">{c.chat}</span>
                        </div>
                      )}
                    </div>
                    {c.notes && <p className="hint">{c.notes}</p>}
                  </>
                )
              })()}
            </div>
          )}

          <div className="card">
            <h3 className="card-title">Envelope preview</h3>
            <label className="field-label">command text</label>
            <input
              className="input wide"
              value={commandText}
              onChange={(e) => setCommandText(e.target.value)}
              placeholder="PrintPos"
            />
            <div className="form-row">
              <div>
                <label className="field-label">mode</label>
                <select
                  className="input wide"
                  value={mode}
                  onChange={(e) => setMode(e.target.value)}
                >
                  {catalog?.modes.map((m) => (
                    <option key={m}>{m}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="field-label">target player</label>
                <input
                  className="input wide"
                  value={targetPlayer}
                  onChange={(e) => setTargetPlayer(e.target.value)}
                  placeholder="(optional)"
                />
              </div>
              <div>
                <label className="field-label">admin player</label>
                <input
                  className="input wide"
                  value={adminPlayer}
                  onChange={(e) => setAdminPlayer(e.target.value)}
                  placeholder="(optional)"
                />
              </div>
            </div>
            <div className="actions-row">
              <button className="btn primary" onClick={renderPreview}>
                Render preview
              </button>
              <span className="hint">No publish. Just shows the envelope shape.</span>
            </div>
            {previewErr && <div className="alert">{previewErr}</div>}
            {preview && (
              <pre className="json-preview">
                {JSON.stringify(preview.envelope, null, 2)}
              </pre>
            )}
          </div>
        </main>
      </div>
    </>
  )
}

function statusPill(status: string): string {
  switch (status) {
    case 'wired-preview':
    case 'gated-preview':
      return ''
    case 'blocked':
      return 'bad'
    default:
      return ''
  }
}
