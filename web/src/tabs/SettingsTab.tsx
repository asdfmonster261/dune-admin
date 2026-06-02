import { useEffect, useState } from 'react'
import { api } from '../api'

type SettingDef = {
  id: string
  backend: 'env' | 'ini'
  group: string
  label: string
  kind: 'string' | 'int' | 'float' | 'bool' | 'enum'
  enum_values?: string[]
  min?: number
  max?: number
  readonly: boolean
  secret: boolean
  needs_restart?: string
  description?: string
}

type SettingRow = {
  def: SettingDef
  value: string | number | boolean | null
}

type ListResp = {
  settings: SettingRow[]
  env_error?: string
  ini_error?: string
}

export default function SettingsTab() {
  const [rows, setRows] = useState<SettingRow[]>([])
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [err, setErr] = useState<string | null>(null)
  const [envErr, setEnvErr] = useState<string | null>(null)
  const [iniErr, setIniErr] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [savedMsg, setSavedMsg] = useState<string | null>(null)

  const load = () => {
    api
      .get<ListResp>('/settings')
      .then((r) => {
        setRows(r.settings || [])
        setDrafts({})
        setEnvErr(r.env_error ?? null)
        setIniErr(r.ini_error ?? null)
        setErr(null)
      })
      .catch((e) => setErr((e as Error).message))
  }

  useEffect(() => {
    load()
  }, [])

  const groups = Array.from(new Set(rows.map((r) => r.def.group))).filter(
    (g) => g !== '_secrets',
  )

  const dirty = Object.keys(drafts).length

  const save = async () => {
    if (!dirty) return
    setSaving(true)
    setSavedMsg(null)
    try {
      const resp = await api.post<{ applied: string[] }>('/settings', { updates: drafts })
      setSavedMsg(`Saved ${resp.applied.length} setting(s)`)
      load()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setSaving(false)
      setTimeout(() => setSavedMsg(null), 4000)
    }
  }

  return (
    <>
      {err && <div className="alert">{err}</div>}
      {envErr && <div className="alert">.env: {envErr}</div>}
      {iniErr && <div className="alert">UserEngine.ini: {iniErr}</div>}

      <div className="logs-bar">
        <span className="hint">
          Edits write to <code>/host/.env</code> and <code>/host/UserEngine.ini</code> on the dune-server volume. Each setting's needs-restart hint
          tells you whether you need to recreate game-servers or other services for the change to take effect.
        </span>
        <button className="btn primary" disabled={!dirty || saving} onClick={save}>
          {saving ? 'Saving…' : dirty ? `Save (${dirty})` : 'Save'}
        </button>
        {savedMsg && <span className="hint" style={{ color: 'var(--ok)' }}>{savedMsg}</span>}
      </div>

      {groups.map((g) => (
        <section className="card" key={g}>
          <h3 className="card-title">{g}</h3>
          <div className="settings-grid">
            {rows
              .filter((r) => r.def.group === g)
              .map((r) => (
                <SettingField
                  key={r.def.id}
                  row={r}
                  draft={drafts[r.def.id]}
                  onChange={(v) => {
                    if (v === stringify(r.value)) {
                      const { [r.def.id]: _ignored, ...rest } = drafts
                      void _ignored
                      setDrafts(rest)
                    } else {
                      setDrafts({ ...drafts, [r.def.id]: v })
                    }
                  }}
                />
              ))}
          </div>
        </section>
      ))}

      {/* Secrets section: read-only masked */}
      {rows.some((r) => r.def.group === '_secrets') && (
        <section className="card">
          <h3 className="card-title">Secrets <span className="card-title-count">read-only</span></h3>
          <div className="settings-grid">
            {rows
              .filter((r) => r.def.group === '_secrets')
              .map((r) => (
                <div key={r.def.id} className="setting-row">
                  <div className="setting-label">{r.def.label}</div>
                  <input
                    className="input wide"
                    value={String(r.value ?? '')}
                    readOnly
                  />
                </div>
              ))}
          </div>
        </section>
      )}
    </>
  )
}

function SettingField({
  row,
  draft,
  onChange,
}: {
  row: SettingRow
  draft: string | undefined
  onChange: (v: string) => void
}) {
  const value = draft ?? stringify(row.value)
  const isDirty = draft !== undefined

  return (
    <div className={`setting-row ${isDirty ? 'dirty' : ''}`}>
      <div className="setting-label">
        {row.def.label}
        {row.def.needs_restart && (
          <span className="needs-restart">restart {row.def.needs_restart}</span>
        )}
      </div>
      {renderInput(row.def, value, onChange)}
      {row.def.description && <div className="setting-desc">{row.def.description}</div>}
    </div>
  )
}

function renderInput(
  def: SettingDef,
  value: string,
  onChange: (v: string) => void,
) {
  const disabled = def.readonly

  if (def.kind === 'enum' || (def.enum_values && def.enum_values.length > 0)) {
    return (
      <select
        className="input wide"
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
      >
        {def.enum_values?.map((v) => (
          <option key={v}>{v}</option>
        ))}
      </select>
    )
  }
  if (def.kind === 'bool') {
    return (
      <select
        className="input wide"
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="true">true</option>
        <option value="false">false</option>
      </select>
    )
  }
  return (
    <input
      className="input wide"
      value={value}
      readOnly={disabled}
      onChange={(e) => onChange(e.target.value)}
      placeholder={
        def.kind === 'float' || def.kind === 'int'
          ? def.min != null && def.max != null
            ? `${def.min}–${def.max}`
            : 'number'
          : ''
      }
    />
  )
}

function stringify(v: string | number | boolean | null): string {
  if (v === null || v === undefined) return ''
  return String(v)
}
