import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'

type Table = { name: string; approx_rows: number | null }

export default function DatabaseTab() {
  const [tables, setTables] = useState<Table[]>([])
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const [view, setView] = useState<'sample' | 'describe' | 'sql'>('sample')
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    api
      .get<Table[]>('/database/tables')
      .then((rows) => {
        setTables(rows || [])
        setErr(null)
      })
      .catch((e) => setErr((e as Error).message))
  }, [])

  const filtered = useMemo(
    () => tables.filter((t) => t.name.toLowerCase().includes(filter.toLowerCase())),
    [tables, filter],
  )

  return (
    <div className="split">
      <aside className="split-side">
        <input
          className="search"
          placeholder={`search ${tables.length} tables`}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <div className="split-list">
          {filtered.map((t) => (
            <button
              key={t.name}
              className={`split-row ${selected === t.name ? 'active' : ''}`}
              onClick={() => {
                setSelected(t.name)
                setView('sample')
              }}
            >
              <span className="split-row-name">{t.name}</span>
              <span className="split-row-meta">{t.approx_rows ?? '?'}</span>
            </button>
          ))}
        </div>
      </aside>

      <main className="split-main">
        {err && <div className="alert">{err}</div>}
        {selected ? (
          <>
            <div className="subtabs">
              <button
                className={`subtab ${view === 'sample' ? 'active' : ''}`}
                onClick={() => setView('sample')}
              >
                Sample
              </button>
              <button
                className={`subtab ${view === 'describe' ? 'active' : ''}`}
                onClick={() => setView('describe')}
              >
                Describe
              </button>
              <button
                className={`subtab ${view === 'sql' ? 'active' : ''}`}
                onClick={() => setView('sql')}
              >
                SQL
              </button>
              <span className="subtab-name mono">{selected}</span>
            </div>
            {view === 'sample' && <SampleView name={selected} />}
            {view === 'describe' && <DescribeView name={selected} />}
            {view === 'sql' && <SQLView name={selected} />}
          </>
        ) : view === 'sql' ? (
          <SQLView name={undefined} />
        ) : (
          <div className="placeholder">
            <p>Pick a table on the left, or jump straight to the SQL runner.</p>
            <button className="btn" onClick={() => setView('sql')}>
              Open SQL runner
            </button>
          </div>
        )}
      </main>
    </div>
  )
}

function SampleView({ name }: { name: string }) {
  const [rows, setRows] = useState<Record<string, unknown>[]>([])
  const [err, setErr] = useState<string | null>(null)
  useEffect(() => {
    api
      .get<Record<string, unknown>[]>(`/database/sample?name=${encodeURIComponent(name)}`)
      .then((r) => {
        setRows(r || [])
        setErr(null)
      })
      .catch((e) => setErr((e as Error).message))
  }, [name])
  if (err) return <div className="alert">{err}</div>
  return <ResultGrid rows={rows} />
}

function DescribeView({ name }: { name: string }) {
  const [rows, setRows] = useState<Record<string, unknown>[]>([])
  const [err, setErr] = useState<string | null>(null)
  useEffect(() => {
    api
      .get<Record<string, unknown>[]>(`/database/describe?name=${encodeURIComponent(name)}`)
      .then((r) => {
        setRows(r || [])
        setErr(null)
      })
      .catch((e) => setErr((e as Error).message))
  }, [name])
  if (err) return <div className="alert">{err}</div>
  return <ResultGrid rows={rows} />
}

function SQLView({ name }: { name: string | undefined }) {
  const [sql, setSql] = useState<string>(name ? `SELECT * FROM dune."${name}" LIMIT 100` : '')
  const [result, setResult] = useState<{ columns: string[]; rows: Record<string, unknown>[] } | null>(
    null,
  )
  const [err, setErr] = useState<string | null>(null)
  const [running, setRunning] = useState(false)

  const run = async () => {
    setRunning(true)
    setErr(null)
    try {
      const r = await api.post<{ columns: string[]; rows: Record<string, unknown>[] }>(
        '/database/sql',
        { sql },
      )
      setResult(r)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setRunning(false)
    }
  }

  return (
    <>
      <textarea
        className="sql-input"
        value={sql}
        onChange={(e) => setSql(e.target.value)}
        spellCheck={false}
        rows={6}
      />
      <div className="actions-row">
        <button className="btn primary" disabled={running} onClick={run}>
          {running ? 'Running…' : 'Run'}
        </button>
        <span className="hint">Read-only: only SELECT / WITH / EXPLAIN / SHOW.</span>
      </div>
      {err && <div className="alert">{err}</div>}
      {result && <ResultGrid rows={result.rows} columns={result.columns} />}
    </>
  )
}

function ResultGrid({
  rows,
  columns,
}: {
  rows: Record<string, unknown>[]
  columns?: string[]
}) {
  if (!rows || rows.length === 0) {
    return <div className="placeholder">No rows.</div>
  }
  const cols = columns ?? Object.keys(rows[0])
  return (
    <div className="grid-wrap">
      <table className="grid">
        <thead>
          <tr>
            {cols.map((c) => (
              <th key={c}>{c}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={i}>
              {cols.map((c) => (
                <td key={c}>{renderCell(row[c])}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function renderCell(v: unknown): string {
  if (v === null || v === undefined) return '∅'
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}
