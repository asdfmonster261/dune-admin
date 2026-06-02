import { useEffect, useState } from 'react'
import { api } from '../api'

type AuditEvent = {
  ts: string
  action: string
  ok: boolean
  actor?: string
  error?: string
  fields?: Record<string, unknown>
}

export default function AuditTab() {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [q, setQ] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const reload = (filter = q) => {
    setLoading(true)
    const qs = filter ? `?q=${encodeURIComponent(filter)}&limit=500` : '?limit=500'
    api
      .get<AuditEvent[]>(`/audit${qs}`)
      .then((rows) => {
        setEvents(rows || [])
        setErr(null)
      })
      .catch((e) => setErr((e as Error).message))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    reload('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <>
      <div className="logs-bar">
        <input
          className="search log-filter"
          placeholder="filter on action / actor / error / fields"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && reload()}
        />
        <button className="btn" onClick={() => reload()} disabled={loading}>
          {loading ? 'Loading…' : 'Refresh'}
        </button>
        <span className="hint">{events.length} events</span>
      </div>

      {err && <div className="alert">{err}</div>}

      <div className="grid-wrap">
        <table className="grid compact">
          <thead>
            <tr>
              <th style={{ width: 180 }}>when</th>
              <th>action</th>
              <th>actor</th>
              <th>fields</th>
              <th style={{ width: 50, textAlign: 'center' }}>ok</th>
            </tr>
          </thead>
          <tbody>
            {events.map((e, i) => (
              <tr key={i} className={e.ok ? '' : 'row-bad'}>
                <td className="mono">{formatTime(e.ts)}</td>
                <td className="mono">{e.action}</td>
                <td className="mono dim">{e.actor || ''}</td>
                <td className="mono small wrap">
                  {e.error ? <span className="err-text">{e.error}</span> : null}
                  {e.error && e.fields ? ' · ' : null}
                  {e.fields ? renderFields(e.fields) : null}
                </td>
                <td style={{ textAlign: 'center' }}>
                  <span className={`pill ${e.ok ? 'ok' : 'bad'}`}>
                    <span className="dot" />
                    {e.ok ? 'ok' : 'err'}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </>
  )
}

function renderFields(f: Record<string, unknown>): string {
  return Object.entries(f)
    .map(([k, v]) => `${k}=${typeof v === 'object' ? JSON.stringify(v) : String(v)}`)
    .join(' ')
}

function formatTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString(undefined, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    })
  } catch {
    return iso
  }
}
