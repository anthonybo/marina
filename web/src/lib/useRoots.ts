import { useCallback, useEffect, useState } from 'react'

/** One directory Marina scans for projects it could start. */
export interface Root {
  path: string
  /** The path with $HOME collapsed to ~, which is how you recognise your own. */
  display: string
  exists: boolean
  readable: boolean
  /** Startable projects found here, running or not. Zero is a real signal. */
  projects: number
}

interface RootsState {
  roots: Root[]
  /** Where the list is persisted, so the UI can name the file. */
  file: string
  loading: boolean
  /** The last failure, phrased by the daemon for a person to read. */
  error: string | null
}

/**
 * Reads and edits the scanned directories.
 *
 * Fetched on demand rather than folded into the live snapshot: this changes when
 * you change it, perhaps twice in a machine's lifetime, so putting it on the
 * event stream would mean sending it forever to describe something static.
 */
export function useRoots(enabled: boolean) {
  const [state, setState] = useState<RootsState>({
    roots: [],
    file: '',
    loading: false,
    error: null,
  })

  const load = useCallback(async () => {
    setState((prev) => ({ ...prev, loading: true }))
    try {
      const res = await fetch('/api/roots')
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const body = await res.json()
      setState({ roots: body.roots ?? [], file: body.file ?? '', loading: false, error: null })
    } catch {
      setState((prev) => ({ ...prev, loading: false, error: 'Could not read the directory list.' }))
    }
  }, [])

  useEffect(() => {
    if (enabled) load()
  }, [enabled, load])

  // Both edits return the new list, so one round trip covers the write and the
  // refresh — and the reply already reflects the rescan the daemon just did.
  const mutate = useCallback(async (action: 'add' | 'remove', path: string) => {
    setState((prev) => ({ ...prev, loading: true, error: null }))
    try {
      const res = await fetch(`/api/roots/${action}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path }),
      })
      const body = await res.json().catch(() => null)
      if (!res.ok) {
        setState((prev) => ({
          ...prev,
          loading: false,
          error: body?.error ?? `Could not ${action} that directory.`,
        }))
        return false
      }
      setState({ roots: body.roots ?? [], file: body.file ?? '', loading: false, error: null })
      return true
    } catch {
      setState((prev) => ({ ...prev, loading: false, error: 'The daemon did not answer.' }))
      return false
    }
  }, [])

  const clearError = useCallback(() => setState((prev) => ({ ...prev, error: null })), [])

  return { ...state, add: (p: string) => mutate('add', p), remove: (p: string) => mutate('remove', p), reload: load, clearError }
}
