import { useEffect, useRef, useState } from 'react'

/** One entry in an app's recent CPU/memory series. */
export interface HealthPoint {
  cpu: number
  rss: number
}

export interface HealthSample {
  /** Percent of a single core. 250 means two and a half cores busy. */
  cpu: number
  /** Resident memory in bytes, across the app's whole process group. */
  rss: number
  processes: number
  at: string
}

export interface AppHealth {
  key: string
  port: number
  project: string
  display: string
  sample: HealthSample
  /** Where its memory is heading — the part a single number never conveyed. */
  trend?: Trend
  history?: HealthPoint[]
  services: number
  startedAt?: number
}

export interface MachineHealth {
  cores: number
  totalRss: number
  totalCpu: number
}

export interface HealthState {
  apps: AppHealth[]
  machine: MachineHealth
  ready: boolean
  byKey: Map<string, AppHealth>
}

const empty: HealthState = {
  apps: [],
  machine: { cores: 1, totalRss: 0, totalCpu: 0 },
  ready: false,
  byKey: new Map(),
}

/**
 * Polls per-app CPU and memory.
 *
 * Deliberately polled rather than pushed. CPU changes on every sample, so putting
 * it on the live stream would mean the daemon broadcasting continuously and losing
 * the property that a quiet machine produces no traffic. Polling also lets the
 * page stop asking entirely when it isn't visible, which a push cannot do.
 *
 * `withHistory` is off for the dashboard, where only the current number is drawn,
 * and on for the health view, which needs the series for sparklines.
 */
export function useHealth(enabled: boolean, withHistory = false) {
  const [state, setState] = useState<HealthState>(empty)
  const interval = useRef(3000)

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    let timer: number | undefined

    const poll = async () => {
      // Nothing is looking at it; don't ask, and don't reschedule until it is.
      if (document.hidden) {
        timer = window.setTimeout(poll, 2000)
        return
      }
      try {
        const res = await fetch(`/api/health${withHistory ? '' : '?history=0'}`)
        if (res.ok) {
          const body = (await res.json()) as {
            apps: AppHealth[] | null
            machine: MachineHealth
            ready: boolean
            intervalMs?: number
          }
          if (!cancelled) {
            const apps = body.apps ?? []
            // Never poll faster than the daemon samples — extra requests would
            // return the same numbers.
            interval.current = Math.max(1500, body.intervalMs ?? 3000)
            setState({
              apps,
              machine: body.machine,
              ready: body.ready,
              byKey: new Map(apps.map((a) => [a.key, a])),
            })
          }
        }
      } catch {
        // A failed poll is not worth surfacing; the next one will succeed or the
        // daemon-unreachable banner already explains it.
      }
      if (!cancelled) timer = window.setTimeout(poll, interval.current)
    }

    poll()
    return () => {
      cancelled = true
      if (timer) clearTimeout(timer)
    }
  }, [enabled, withHistory])

  return state
}

/** Formats bytes the way a developer reads them. */
export function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0'
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)}K`
  if (bytes < 1024 * 1024 * 1024) return `${Math.round(bytes / 1024 / 1024)}M`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(1)}G`
}

/**
 * Buckets load into something to colour by.
 *
 * The scale is cores, not a percentage of the machine: one core fully busy is
 * normal for a dev server building, while three cores pinned is what actually
 * makes a laptop lag.
 */
export type LoadLevel = 'idle' | 'busy' | 'heavy' | 'hot'

/** Where an app's memory is heading. Empty severity means nothing is wrong. */
export interface Trend {
  growthPerMin: number
  climbing: boolean
  /** 0 = fine, approaching 1 = about to take the machine with it. */
  severity: number
  why?: string
}

/**
 * How a boat in trouble sits in the water.
 *
 * Deliberately a static pose rather than an animation. A sinking boat that
 * *animates* would be a perpetual animation, and one of those costs ~15% of a CPU
 * core here — an alarm about resource use has no business being the second-biggest
 * consumer on the page. Depth and list are computed once from severity and simply
 * rendered.
 */
/**
 * The same fact as `trend.why`, short enough for a caption 7rem wide.
 *
 * "memory climbing 240 MB/min" rendered as "memory climbing 2…", which tells you
 * a boat is in trouble and nothing about why. The full sentence stays on the
 * tooltip; the caption carries the number, which is the part that decides whether
 * you act now or after lunch.
 */
export function distressLabel(trend: Trend): string {
  if (trend.climbing) return `+${formatBytes(trend.growthPerMin)}/min`
  return trend.why ?? ''
}

export function distress(trend?: Trend) {
  const s = Math.max(0, Math.min(1, trend?.severity ?? 0))
  return {
    /** How far below the waterline, in pixels. */
    sink: s * 14,
    /** How far over it has heeled, in degrees. Only once it is clearly in trouble. */
    list: s < 0.4 ? 0 : (s - 0.4) * 18,
    /**
     * Whether it reads as foundering rather than merely heavy.
     *
     * `climbing` is taken at its word rather than re-derived from severity. The
     * daemon has already cleared three gates to set it, and severity keeps moving
     * underneath — a page reclaim landing at the end of the window drops a live
     * 240 MB/min leak from 0.80 to 0.66. Comparing that against a bare threshold
     * would put the boat back on the surface for one poll, which is the exact
     * blinking the daemon-side window was widened to stop.
     */
    foundering: trend?.climbing === true || s >= 0.6,
  }
}

export function loadLevel(cpu: number): LoadLevel {
  if (cpu < 25) return 'idle'
  if (cpu < 100) return 'busy'
  if (cpu < 250) return 'heavy'
  return 'hot'
}

export const loadColor: Record<LoadLevel, string> = {
  idle: '#3fe0c8',
  busy: '#6ff0dc',
  heavy: '#ffb454',
  hot: '#ff7a8a',
}

export const loadLabel: Record<LoadLevel, string> = {
  idle: 'quiet',
  busy: 'working',
  heavy: 'heavy',
  hot: 'pinning cores',
}
