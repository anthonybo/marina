import { useCallback, useEffect, useRef, useState } from 'react'
import type { Connection, Snapshot } from './types'

/**
 * Subscribes to the daemon's Server-Sent Events stream.
 *
 * EventSource reconnects on its own, but it cannot tell us the difference
 * between "reconnecting" and "the daemon is gone", so we track that here to
 * report the connection state honestly in the UI.
 */
export function useMarina() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const [connection, setConnection] = useState<Connection>('connecting')
  const sourceRef = useRef<EventSource | null>(null)

  useEffect(() => {
    let cancelled = false

    const connect = () => {
      if (cancelled) return
      const source = new EventSource('/api/stream')
      sourceRef.current = source

      source.addEventListener('state', (event) => {
        if (cancelled) return
        try {
          setSnapshot(JSON.parse((event as MessageEvent).data) as Snapshot)
          setConnection('live')
        } catch {
          // A malformed frame is not worth tearing the stream down for.
        }
      })

      source.onopen = () => !cancelled && setConnection('live')
      source.onerror = () => {
        if (cancelled) return
        // EventSource retries by itself; surface the gap without racing it.
        setConnection((prev) => (prev === 'live' ? 'lost' : 'connecting'))
      }
    }

    connect()
    return () => {
      cancelled = true
      sourceRef.current?.close()
    }
  }, [])

  /** Posts to the daemon, returning the error message when it refuses. A stop
   *  can legitimately be refused — infrastructure, Marina itself — and the caller
   *  needs to be able to say so rather than appearing to do nothing. */
  const mutate = useCallback(async (path: string, body: unknown): Promise<string | null> => {
    try {
      const res = await fetch(`/api/${path}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
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
  }, [])

  const setPinned = useCallback(
    (key: string, pinned: boolean) => {
      // Update locally first so the row reorders the instant you click it.
      setSnapshot((prev) =>
        prev
          ? {
              ...prev,
              services: prev.services.map((s) =>
                s.key === key ? { ...s, meta: { ...s.meta, pinned } } : s,
              ),
            }
          : prev,
      )
      return mutate('pin', { key, pinned })
    },
    [mutate],
  )

  const setNickname = useCallback(
    (key: string, nickname: string) => {
      setSnapshot((prev) =>
        prev
          ? {
              ...prev,
              services: prev.services.map((s) =>
                s.key === key
                  ? { ...s, display: nickname || s.label, meta: { ...s.meta, nickname } }
                  : s,
              ),
            }
          : prev,
      )
      return mutate('nickname', { key, nickname })
    },
    [mutate],
  )

  const start = useCallback(
    (path: string) => {
      // Mark it starting straight away so the button responds on click; the
      // daemon confirms on its next sweep.
      setSnapshot((prev) =>
        prev
          ? {
              ...prev,
              ashore: prev.ashore.map((p) => (p.path === path ? { ...p, starting: true } : p)),
            }
          : prev,
      )
      return mutate('launch', { path })
    },
    [mutate],
  )

  /** Stops an app. `withServices` also stops the workers behind it. */
  const stop = useCallback(
    (target: { port?: number; path?: string; withServices?: boolean }) =>
      mutate('stop', target),
    [mutate],
  )

  return { snapshot, connection, setPinned, setNickname, start, stop }
}

/** Ticks once a second so uptime counters stay current without server traffic. */
export function useClock(intervalMs = 1000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return now
}
