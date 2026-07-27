import { useCallback, useEffect, useRef, useState } from 'react'

/** One captured terminal, as listed by the daemon. */
export interface LogEntry {
  name: string
  size: number
  modTime: string
  path: string
  running: boolean
  /** 'launch' = Marina started it. 'process' = its output happens to be a file. */
  source: 'launch' | 'process'
  /** Set for 'process' sources; how the client asks for its content. */
  port?: number
}

/** A running app whose output cannot be read, and why. */
export interface Unreachable {
  project: string
  port: number
  kind: 'pipe' | 'tty' | 'discarded' | 'unknown' | 'file'
  path?: string
}

interface Chunk {
  name: string
  offset: number
  next: number
  size: number
  data: string
  rotated: boolean
}

/** Polls the list of captured terminals. */
export function useLogList(intervalMs = 4000) {
  const [logs, setLogs] = useState<LogEntry[] | null>(null)
  const [unreachable, setUnreachable] = useState<Unreachable[]>([])
  const [dir, setDir] = useState('')

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      try {
        const res = await fetch('/api/logs')
        if (!res.ok) throw new Error(await res.text())
        const body = (await res.json()) as {
          logs: LogEntry[] | null
          unreachable: Unreachable[] | null
          dir: string
        }
        if (cancelled) return
        setLogs(body.logs ?? [])
        setUnreachable(body.unreachable ?? [])
        setDir(body.dir)
      } catch {
        if (!cancelled) setLogs([])
      }
    }

    load()
    const id = setInterval(load, intervalMs)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [intervalMs])

  return { logs, unreachable, dir }
}

/** Removes a finished terminal from the list. Marina only deletes logs it wrote. */
export async function dismissLog(name: string): Promise<string | null> {
  try {
    const res = await fetch('/api/logs/dismiss', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name }),
    })
    if (res.ok) return null
    const text = await res.text()
    try {
      return (JSON.parse(text) as { error?: string }).error ?? text
    } catch {
      return text
    }
  } catch (err) {
    return err instanceof Error ? err.message : 'request failed'
  }
}

/**
 * Follows one log, transferring only what is new.
 *
 * The daemon returns the offset to continue from, so a quiet log costs one small
 * request per interval rather than the whole file. `maxBytes` bounds what is held
 * in memory: a dev server left running for a day can produce a lot of output, and
 * a terminal pane only ever shows the tail of it.
 */
export function useLogTail(
  target: { name: string; port?: number } | null,
  intervalMs = 1500,
  maxBytes = 400_000,
) {
  const name = target?.name ?? null
  const port = target?.port
  const [text, setText] = useState('')
  const [missing, setMissing] = useState(false)
  const offset = useRef<number>(-1)
  const currentName = useRef<string | null>(null)

  // Starting on a different log resets the buffer before the first fetch lands.
  if (currentName.current !== name) {
    currentName.current = name
    offset.current = -1
  }

  useEffect(() => {
    if (!name) return
    let cancelled = false
    setText('')
    setMissing(false)
    offset.current = -1

    const poll = async () => {
      try {
        // A port addresses a live process's own log; the daemon resolves it to a
        // path, so no path is ever sent from here.
        const query =
          port !== undefined
            ? `port=${port}`
            : `name=${encodeURIComponent(name)}`
        const res = await fetch(`/api/logs/content?${query}&offset=${offset.current}`)
        if (res.status === 404) {
          if (!cancelled) setMissing(true)
          return
        }
        if (!res.ok) return
        const chunk = (await res.json()) as Chunk
        if (cancelled) return

        setMissing(false)
        offset.current = chunk.next
        if (chunk.rotated) {
          // A relaunch truncated the file; start the pane over.
          setText(chunk.data)
          return
        }
        if (!chunk.data) return
        setText((prev) => {
          const joined = prev + chunk.data
          return joined.length > maxBytes ? joined.slice(joined.length - maxBytes) : joined
        })
      } catch {
        // A failed poll is not worth surfacing; the next one will succeed or the
        // daemon-unreachable banner will explain it.
      }
    }

    poll()
    const id = setInterval(poll, intervalMs)
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [name, port, intervalMs, maxBytes])

  const refresh = useCallback(() => {
    offset.current = -1
    setText('')
  }, [])

  return { text, missing, refresh }
}
