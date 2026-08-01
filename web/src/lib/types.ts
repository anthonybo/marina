export type Kind = 'app' | 'infra' | 'system' | 'unknown'

export interface Probe {
  http: boolean
  scheme?: string
  status?: number
  title?: string
  server?: string
  favicon?: string
}

export interface Meta {
  nickname?: string
  pinned: boolean
  firstSeen?: string
  lastSeen?: string
}

export interface Service {
  key: string
  port: number
  pid: number
  proc: string
  kind: Kind
  project?: string
  subpath?: string
  label: string
  display: string
  dir?: string
  repo?: string
  framework?: string
  language?: string
  entry?: string
  cmd?: string
  wildcard: boolean
  v6Only?: boolean
  /** Unix seconds. Uptime is derived from this client-side so the daemon can
   *  stay quiet when nothing has actually changed. */
  startedAt?: number
  probe: Probe
  meta: Meta
  url?: string
  fresh: boolean
  /** How this service relates to the rest of its project. */
  role: 'primary' | 'service' | 'solo'
  /** True when this port was excluded from HTTP probing via --no-probe. */
  probeSkipped?: boolean
  /** Set on a primary: how many services belong to it. */
  serviceCount?: number
  /** Set on a service: the port of the primary it belongs to. */
  primaryPort?: number
}

export interface Counts {
  total: number
  apps: number
  infra: number
  system: number
  http: number
  /** Apps you would actually open — project front doors plus standalone apps. */
  primary: number
  /** Supporting workers behind those front doors. */
  services: number
}

/** A port a project is expected to bind, and the evidence for it. */
export interface ExpectedPort {
  port: number
  /** history = Marina watched it use this port; then config, script, default. */
  source: 'history' | 'config' | 'script' | 'default'
  detail?: string
}

/** An expected port that something else already holds. */
export interface PortConflict {
  port: number
  heldBy: string
  kind: string
  source: string
}

/** A project found on disk that isn't currently running. */
export interface Ashore {
  name: string
  path: string
  manager: string
  script?: string
  /** Exactly what will run. Always shown before it can be started. */
  command: string
  framework?: string
  language?: string
  hasGit: boolean
  /** True between a launch and its ports appearing. */
  starting: boolean
  /** True when the last launch attempt died. */
  failed: boolean
  /** What went wrong, in terms you can act on. */
  error?: string
  lastSeen?: string
  logPath?: string
  /** Ports it is likely to bind, strongest evidence first. */
  expect?: ExpectedPort[]
  /** Expected ports already in use. Starting is still allowed. */
  conflicts?: PortConflict[]
}

export interface StoreHealth {
  connected: boolean
  dsn: string
  error?: string
  pending: number
}

/** One address this machine answers on. */
export interface NetAddr {
  ip: string
  /** en0 is Wi-Fi on most Macs; en1 or a bridge is usually wired. */
  iface: string
}

/** How to reach this machine from another device on the same network. */
export interface NetInfo {
  /** Absent when no interface has an address — an unplugged cable, dropped Wi-Fi. */
  ip?: string
  iface?: string
  /** The mDNS name, which survives the router handing out a new address. */
  host?: string
  /** A shorter name Marina publishes, present only once it actually resolves. */
  alias?: string
  /** Further addresses, for a machine on Wi-Fi and Ethernet at once. */
  others?: NetAddr[]
}

export interface Snapshot {
  rev: number
  at: string
  services: Service[]
  counts: Counts
  store: StoreHealth
  daemonStartedAt: string
  version: string
  scanMs: number
  ashore: Ashore[]
  /** Directories that look like projects but have no start command we know. */
  ashoreSkipped: number
  net: NetInfo
}

export type Connection = 'connecting' | 'live' | 'lost'
