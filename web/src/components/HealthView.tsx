import { memo, useEffect, useState } from 'react'
import { LoadMeter } from './LoadMeter'
import { Sparkline } from './Sparkline'
import { uptime } from '../lib/format'
import {
  formatBytes,
  loadColor,
  loadLabel,
  loadLevel,
  useHealth,
  type AppHealth,
} from '../lib/useHealth'

/**
 * What each running app is costing the machine.
 *
 * The question this answers is "which of these is making my laptop lag", so apps
 * are ordered by CPU and the scale is cores rather than a share of the total: one
 * core busy is ordinary for a dev server, three cores pinned is a problem. Memory
 * is shown as the app's own figure and as a share of what is resident, because
 * 2.4GB means something different on a machine holding 8GB than one holding 64.
 */
interface HealthViewProps {
  now: number
  onStop: (target: { port: number; withServices?: boolean }) => Promise<string | null> | void
  onOpenLog: (name: string) => void
}

export function HealthView({ now, onStop, onOpenLog }: HealthViewProps) {
  const { apps, machine, ready } = useHealth(true, true)

  const busiest = apps.length > 0 ? apps[0].sample.cpu : 0
  const totalAppCPU = apps.reduce((sum, a) => sum + a.sample.cpu, 0)
  const totalAppRSS = apps.reduce((sum, a) => sum + a.sample.rss, 0)

  return (
    <>
      <div className="mb-4 flex flex-wrap items-baseline gap-x-5 gap-y-1">
        <h2 className="stencil shrink-0 text-foam-300">Health</h2>
        <span className="h-px flex-1 bg-harbor-800" aria-hidden />
        <span className="font-mono text-[0.68rem] text-foam-400">
          your apps: {(totalAppCPU / 100).toFixed(1)} of {machine.cores} cores ·{' '}
          {formatBytes(totalAppRSS)}
        </span>
        <span className="font-mono text-[0.68rem] text-foam-400">
          machine: {(machine.totalCpu / 100).toFixed(1)} cores · {formatBytes(machine.totalRss)}{' '}
          resident
        </span>
      </div>

      {!ready && (
        <p className="mb-4 rounded-lg border border-harbor-800 bg-harbor-900/50 px-4 py-2.5 font-mono text-[0.7rem] text-foam-400">
          Measuring… CPU is a rate, so the first figure needs two samples.
        </p>
      )}

      {ready && apps.length === 0 && (
        <div className="rounded-xl border border-harbor-800 bg-harbor-900/60 p-6">
          <h3 className="text-base font-semibold text-foam-50">No apps running</h3>
          <p className="mt-1.5 text-[0.88rem] text-foam-300">
            Start something from Ashore and its CPU and memory appear here.
          </p>
        </div>
      )}

      <ul className="flex flex-col gap-2">
        {apps.map((app) => (
          <HealthRow
            key={app.key}
            app={app}
            cores={machine.cores}
            totalRSS={machine.totalRss}
            busiest={busiest}
            now={now}
            onStop={onStop}
            onOpenLog={onOpenLog}
          />
        ))}
      </ul>

      {apps.length > 0 && (
        <p className="mt-4 font-mono text-[0.64rem] leading-relaxed text-foam-400">
          CPU is percent of one core, measured over the last sample and summed across
          each app's whole process group — so a project's workers count towards it.
          The dotted line on each trace marks one full core.
        </p>
      )}
    </>
  )
}

const HealthRow = memo(function HealthRow({
  app,
  cores,
  totalRSS,
  busiest,
  now,
  onStop,
  onOpenLog,
}: {
  app: AppHealth
  cores: number
  totalRSS: number
  busiest: number
  now: number
  onStop: (target: { port: number; withServices?: boolean }) => Promise<string | null> | void
  onOpenLog: (name: string) => void
}) {
  const { sample } = app
  const level = loadLevel(sample.cpu)
  const color = loadColor[level]
  const up = uptime(app.startedAt, now)
  const memShare = totalRSS > 0 ? (sample.rss / totalRSS) * 100 : 0
  // Worth calling out only when it is both the busiest and actually busy.
  const isWorst = busiest > 100 && sample.cpu === busiest

  return (
    <li
      className={[
        'rounded-xl border bg-harbor-900/60 px-4 py-3',
        isWorst ? 'border-coral-400/40' : 'border-harbor-800',
      ].join(' ')}
    >
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-1 items-baseline gap-2">
          <span className="tnum font-mono text-[0.95rem] font-medium" style={{ color }}>
            {sample.cpu < 10 ? sample.cpu.toFixed(1) : Math.round(sample.cpu)}%
          </span>
          <span className="truncate text-[0.92rem] font-semibold text-foam-50">{app.display}</span>
          <span className="tnum shrink-0 font-mono text-[0.68rem] text-foam-400">:{app.port}</span>
          {isWorst && (
            <span className="stencil shrink-0 rounded border border-coral-400/40 px-1.5 py-0.5 text-coral-300">
              busiest
            </span>
          )}
        </div>

        <Sparkline
          points={(app.history ?? []).map((p) => p.cpu)}
          color={color}
          reference={100}
          className="h-7 w-28 shrink-0"
        />

        <div className="flex shrink-0 items-center gap-4 font-mono text-[0.68rem] text-foam-300">
          <span title={`${formatBytes(sample.rss)} resident — ${memShare.toFixed(1)}% of all memory in use`}>
            {formatBytes(sample.rss)}
            <span className="ml-1 text-foam-400">mem</span>
          </span>
          <span title="Processes in this app's process group">
            {sample.processes}
            <span className="ml-1 text-foam-400">proc</span>
          </span>
          {app.services > 0 && (
            <span title={`${app.services} services behind this app`} className="text-foam-400">
              +{app.services} svc
            </span>
          )}
          {up && <span className="text-foam-400">{up}</span>}
        </div>

        <div className="flex shrink-0 items-center gap-1.5">
          <button
            type="button"
            onClick={() => onOpenLog(app.project || app.display)}
            title="Open this app's terminal"
            className="rounded-md border border-harbor-700 px-2 py-0.5 font-mono text-[0.64rem] text-foam-300 transition-colors hover:bg-harbor-800 hover:text-foam-50"
          >
            logs
          </button>
          <StopControl
            port={app.port}
            withServices={app.services > 0}
            name={app.display}
            onStop={onStop}
          />
        </div>
      </div>

      <div className="mt-2 flex items-center gap-3">
        <LoadMeter cpu={sample.cpu} cores={cores} className="flex-1" />
        <span className="shrink-0 font-mono text-[0.6rem]" style={{ color }}>
          {loadLabel[level]}
        </span>
      </div>
    </li>
  )
})

/** Stop, armed on the first click — the same rule as everywhere else. */
function StopControl({
  port,
  withServices,
  name,
  onStop,
}: {
  port: number
  withServices: boolean
  name: string
  onStop: (target: { port: number; withServices?: boolean }) => Promise<string | null> | void
}) {
  return (
    <ArmedButton
      label={`Stop ${name}`}
      armedLabel="stop?"
      idleLabel="⏻ stop"
      onConfirm={() => onStop({ port, withServices })}
    />
  )
}


function ArmedButton({
  label,
  armedLabel,
  idleLabel,
  onConfirm,
}: {
  label: string
  armedLabel: string
  idleLabel: string
  onConfirm: () => Promise<string | null> | void
}) {
  const [armed, setArmed] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!armed) return
    const id = setTimeout(() => setArmed(false), 4000)
    return () => clearTimeout(id)
  }, [armed])

  return (
    <>
      <button
        type="button"
        title={armed ? 'Click again to stop' : label}
        onClick={async () => {
          if (!armed) {
            setArmed(true)
            return
          }
          setArmed(false)
          setError((await onConfirm()) ?? null)
        }}
        className={[
          'rounded-md px-2 py-0.5 font-mono text-[0.64rem] transition-colors',
          armed
            ? 'bg-coral-400/25 text-coral-300'
            : 'border border-harbor-700 text-foam-400 hover:bg-coral-400/15 hover:text-coral-300',
        ].join(' ')}
      >
        {armed ? armedLabel : idleLabel}
      </button>
      {error && <span className="text-[0.62rem] text-coral-300">{error}</span>}
    </>
  )
}
