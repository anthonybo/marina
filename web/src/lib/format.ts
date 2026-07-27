import type { Service } from './types'

/** Compact uptime, e.g. "1d 20h". Returns null when the start time is unknown. */
export function uptime(startedAt: number | undefined, now: number): string | null {
  if (!startedAt) return null
  const secs = Math.max(0, Math.floor(now / 1000) - startedAt)
  const d = Math.floor(secs / 86400)
  const h = Math.floor((secs % 86400) / 3600)
  const m = Math.floor((secs % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${secs % 60}s`
  return `${secs}s`
}

/** The one-line name for a service: nickname, else project, else process. */
export function primaryName(s: Service): string {
  return s.display || s.label || s.proc
}

/** The quieter second line: what distinguishes this berth from its neighbours. */
export function secondaryName(s: Service): string {
  if (s.entry) return s.entry
  if (s.subpath) return s.subpath
  if (s.probe.title) return s.probe.title
  return s.proc
}

/**
 * Ranks a service against a query. Returns -1 for no match.
 *
 * Exact port matches win outright, because typing "3005" is how you search a
 * list like this and you expect to land on it immediately.
 */
export function score(s: Service, query: string): number {
  const q = query.trim().toLowerCase()
  if (!q) return 0

  const port = String(s.port)
  if (port === q) return 1000
  if (port.startsWith(q)) return 700

  const fields: Array<[string | undefined, number]> = [
    [s.meta.nickname, 600],
    [s.project, 500],
    [s.entry, 450],
    [s.label, 400],
    [s.subpath, 300],
    [s.probe.title, 250],
    [s.framework, 200],
    [s.proc, 150],
    [s.dir, 60],
  ]

  let best = -1
  for (const [raw, weight] of fields) {
    if (!raw) continue
    const value = raw.toLowerCase()
    if (value === q) best = Math.max(best, weight + 80)
    else if (value.startsWith(q)) best = Math.max(best, weight + 40)
    else if (value.includes(q)) best = Math.max(best, weight)
  }
  return best
}

/** A project's front door together with the services that only exist to serve it. */
export interface Cluster {
  primary: Service
  services: Service[]
}

/**
 * Folds a flat list into clusters using the roles the daemon assigned.
 *
 * A project that is one UI plus a dozen workers should read as one thing with a
 * dozen parts, not as thirteen apps. Anything the daemon left as `solo` becomes
 * a cluster of one, and an orphaned service (its primary gone from the current
 * filter) is promoted rather than dropped — never silently hide a live port.
 */
export function clusterServices(services: Service[]): Cluster[] {
  const clusters: Cluster[] = []
  const byPort = new Map<string, Cluster>()

  for (const s of services) {
    if (s.role === 'service') continue
    const cluster: Cluster = { primary: s, services: [] }
    clusters.push(cluster)
    byPort.set(`${s.project ?? ''}:${s.port}`, cluster)
  }

  for (const s of services) {
    if (s.role !== 'service') continue
    const parent = byPort.get(`${s.project ?? ''}:${s.primaryPort ?? 0}`)
    if (parent) parent.services.push(s)
    else clusters.push({ primary: s, services: [] })
  }

  for (const cluster of clusters) cluster.services.sort((a, b) => a.port - b.port)
  return clusters
}

/**
 * A cluster is pinned when any part of it is. Pinning a project's front door has
 * to carry its services along, or they are left behind in their old group with
 * no primary to hang from.
 */
export function clusterPinned(cluster: Cluster): boolean {
  return cluster.primary.meta.pinned || cluster.services.some((s) => s.meta.pinned)
}

/** Groups clusters under their project heading, preserving daemon ordering. */
export function groupClusters(clusters: Cluster[]): Array<{ name: string; clusters: Cluster[] }> {
  const groups = new Map<string, Cluster[]>()
  const order: string[] = []
  for (const cluster of clusters) {
    const p = cluster.primary
    const name = p.project || p.label || p.proc
    if (!groups.has(name)) {
      groups.set(name, [])
      order.push(name)
    }
    groups.get(name)!.push(cluster)
  }
  return order.map((name) => ({ name, clusters: groups.get(name)! }))
}

/** Total ports a cluster accounts for, used for group headings. */
export function clusterSize(cluster: Cluster): number {
  return 1 + cluster.services.length
}

/** Compact port summary for a collapsed cluster, e.g. "3001–3013". */
export function portRange(services: Service[]): string {
  if (services.length === 0) return ''
  const ports = services.map((s) => s.port).sort((a, b) => a - b)
  if (ports.length === 1) return String(ports[0])
  return `${ports[0]}–${ports[ports.length - 1]}`
}

/** Groups services under a heading, preserving the daemon's ordering. */
export function groupByProject(services: Service[]): Array<{ name: string; items: Service[] }> {
  const groups = new Map<string, Service[]>()
  for (const s of services) {
    const name = s.project || s.label || s.proc
    const bucket = groups.get(name)
    if (bucket) bucket.push(s)
    else groups.set(name, [s])
  }
  return [...groups].map(([name, items]) => ({ name, items }))
}

/** How confident we are about an expected port, for the tooltip. */
export const portEvidence: Record<string, string> = {
  history: 'Marina has seen it use this port',
  config: 'declared in its config',
  script: 'named in its start command',
  default: "its framework's usual port — a guess",
}

export const kindLabel: Record<string, string> = {
  app: 'Apps',
  infra: 'Infrastructure',
  system: 'System',
  unknown: 'Unattributed',
}
