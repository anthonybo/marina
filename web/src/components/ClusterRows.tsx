import { Berth } from './Berth'
import { portRange, type Cluster } from '../lib/format'
import type { Trend } from '../lib/useHealth'

/**
 * Renders a project's front door with its supporting services folded underneath.
 *
 * Collapsed by default: a dozen workers are real, but they are not a dozen apps,
 * and listing them at the same weight as the UI is what made the manifest read as
 * far busier than the machine actually is. The disclosure states exactly what is
 * hidden — how many, and over which ports — so nothing is quietly dropped.
 */
interface ClusterRowsProps {
  cluster: Cluster
  now: number
  grouped: boolean
  expanded: boolean
  onToggle: (key: string) => void
  onPin: (key: string, pinned: boolean) => void
  onRename: (key: string, nickname: string) => void
  onStop?: (target: { port: number; withServices?: boolean }) => Promise<string | null> | void
  /** The primary's current CPU cost, if measured. */
  cpu?: number
  /** Where the primary's memory is heading, so a leak shows here too. */
  trend?: Trend
  cores?: number
}

export function ClusterRows({
  cluster,
  now,
  grouped,
  expanded,
  onToggle,
  onPin,
  onRename,
  onStop,
  cpu,
  trend,
  cores,
}: ClusterRowsProps) {
  const { primary, services } = cluster
  const count = services.length

  return (
    <>
      <Berth
        service={primary}
        now={now}
        grouped={grouped}
        serviceCount={count}
        onPin={onPin}
        onRename={onRename}
        onStop={onStop}
        cpu={cpu}
        trend={trend}
        cores={cores}
      />

      {count > 0 && (
        <li>
          <button
            type="button"
            onClick={() => onToggle(primary.key)}
            aria-expanded={expanded}
            className="flex w-full items-center gap-2 rounded-lg px-4 py-1.5 text-left font-mono text-[0.7rem] text-foam-400 transition-colors hover:bg-harbor-900 hover:text-foam-100"
          >
            <span
              aria-hidden
              className={`inline-block transition-transform duration-200 ${expanded ? 'rotate-90' : ''}`}
            >
              ▸
            </span>
            <span>
              {count} {count === 1 ? 'service' : 'services'}
            </span>
            <span className="text-harbor-600">·</span>
            <span className="tnum">{portRange(services)}</span>
            <span className="ml-auto text-foam-400/70">{expanded ? 'hide' : 'show'}</span>
          </button>
        </li>
      )}

      {expanded &&
        services.map((service) => (
          <li key={service.key} className="ml-4 border-l border-harbor-800 pl-3">
            <ul>
              <Berth
                service={service}
                now={now}
                grouped
                nested
                onPin={onPin}
                onRename={onRename}
                onStop={onStop}
              />
            </ul>
          </li>
        ))}
    </>
  )
}
