import { useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../api'

type LogPod = {
  name: string
  service: string
  state: string
  status: string
}

type LogLine = { stream: 'stdout' | 'stderr' | 'error'; text: string }

const MAX_LINES = 5000

export default function LogsTab() {
  const [pods, setPods] = useState<LogPod[]>([])
  const [filter, setFilter] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    const tick = () =>
      api
        .get<LogPod[]>('/logs/pods')
        .then((rows) => {
          setPods(rows || [])
          setErr(null)
        })
        .catch((e) => setErr((e as Error).message))
    tick()
    const id = setInterval(tick, 8000)
    return () => clearInterval(id)
  }, [])

  const filtered = useMemo(
    () => pods.filter((p) => p.service.toLowerCase().includes(filter.toLowerCase())),
    [pods, filter],
  )

  return (
    <div className="split">
      <aside className="split-side">
        <input
          className="search"
          placeholder={`search ${pods.length} containers`}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        {err && <div className="alert">{err}</div>}
        <div className="split-list">
          {filtered.map((p) => (
            <button
              key={p.name}
              className={`split-row ${selected === p.name ? 'active' : ''}`}
              onClick={() => setSelected(p.name)}
            >
              <span className="split-row-name">
                {p.service}
                {p.state === 'running' && <span className="online-dot" />}
              </span>
              <span className="split-row-meta mono">{p.state}</span>
            </button>
          ))}
        </div>
      </aside>

      <main className="split-main">
        {selected ? <LogStream key={selected} container={selected} /> : (
          <div className="placeholder">
            <p>Pick a container on the left to start tailing its logs.</p>
          </div>
        )}
      </main>
    </div>
  )
}

function LogStream({ container }: { container: string }) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [connected, setConnected] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [filter, setFilter] = useState('')
  const wsRef = useRef<WebSocket | null>(null)
  const tailRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    setLines([])
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const url = `${proto}://${location.host}/api/v1/logs/stream?name=${encodeURIComponent(container)}`
    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => setConnected(true)
    ws.onclose = () => setConnected(false)
    ws.onerror = () => setConnected(false)
    ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data) as LogLine
        setLines((prev) => {
          const next = prev.length >= MAX_LINES ? prev.slice(prev.length - MAX_LINES + 1) : prev
          return [...next, msg]
        })
      } catch {
        setLines((prev) => [...prev, { stream: 'error', text: String(ev.data) }])
      }
    }
    return () => {
      ws.close()
    }
  }, [container])

  useEffect(() => {
    if (autoScroll && tailRef.current) {
      tailRef.current.scrollTop = tailRef.current.scrollHeight
    }
  }, [lines, autoScroll])

  const filtered = useMemo(() => {
    if (!filter) return lines
    const f = filter.toLowerCase()
    return lines.filter((l) => l.text.toLowerCase().includes(f))
  }, [lines, filter])

  return (
    <>
      <div className="logs-bar">
        <span className={`pill ${connected ? 'ok' : 'bad'}`}>
          <span className="dot" />
          {connected ? 'streaming' : 'disconnected'}
        </span>
        <span className="mono dim">{container}</span>
        <input
          className="search log-filter"
          placeholder="filter shown lines"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <label className="toggle">
          <input
            type="checkbox"
            checked={autoScroll}
            onChange={(e) => setAutoScroll(e.target.checked)}
          />
          autoscroll
        </label>
        <button
          className="btn"
          onClick={() => setLines([])}
          disabled={lines.length === 0}
        >
          Clear
        </button>
        <span className="hint">
          {filtered.length}/{lines.length} lines · cap {MAX_LINES}
        </span>
      </div>

      <div className="logs-pane" ref={tailRef}>
        {filtered.map((l, i) => (
          <div key={i} className={`log-line ${l.stream}`}>
            <span className="log-stream">{l.stream === 'stderr' ? '!' : ' '}</span>
            <span className="log-text">{l.text}</span>
          </div>
        ))}
      </div>
    </>
  )
}
