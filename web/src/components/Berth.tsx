import { memo, useEffect, useRef, useState } from 'react'
import type { Service } from '../lib/types'
import { primaryName, secondaryName, uptime } from '../lib/format'
import { LoadMeter } from './LoadMeter'

/**
 * One berth in the harbour: a single listening service.
 *
 * The port number is the typographic anchor, because a port is how you actually
 * identify a local server. The bar down the left encodes state at a glance, and
 * when the service speaks HTTP the entire row is one click target.
 */
interface BerthProps {
  service: Service
  now: number
  /** True when this row sits under its project's heading, in which case the
   *  project name is already established and the row leads with whatever
   *  actually distinguishes it from its siblings. */
  grouped?: boolean
  /** Set on a project's front door: how many services it fronts. Shown as a
   *  badge so the relationship is visible even while they're collapsed. */
  serviceCount?: number
  /** Stops this app. Absent for anything Marina refuses to stop. */
  onStop?: (target: { port: number; withServices?: boolean }) => Promise<string | null> | void
  /** This app's current CPU cost, if measured. */
  cpu?: number
  cores?: number
  /** True for a row rendered inside an expanded cluster — quieter, since its
   *  project and role are already established by what it sits under. */
  nested?: boolean
  onPin: (key: string, pinned: boolean) => void
  onRename: (key: string, nickname: string) => void
}

/** The two lines of text for a row, chosen so neither restates the other. */
function lines(s: Service, grouped: boolean): { primary: string; secondary: string } {
  const named = Boolean(s.meta.nickname)

  // Under a project heading, "stormwire" on every row says nothing. The entry
  // script is the real identity of a worker among thirteen siblings.
  if (grouped && !named && s.entry) {
    return { primary: s.entry, secondary: s.subpath || s.probe.title || s.proc }
  }
  if (grouped && !named && s.subpath) {
    return { primary: s.subpath, secondary: s.probe.title || s.framework || s.proc }
  }
  return { primary: primaryName(s), secondary: secondaryName(s) }
}

/** Maps a service's state to the colour of its mooring bar and port number. */
function tone(s: Service) {
  if (s.fresh) return { bar: 'bg-lantern-400', port: 'text-lantern-300' }
  if (s.probe.http) return { bar: 'bg-lit-400', port: 'text-lit-300' }
  if (s.kind === 'infra') return { bar: 'bg-orchid-400', port: 'text-orchid-300' }
  if (s.kind === 'app') return { bar: 'bg-coral-400', port: 'text-coral-300' }
  return { bar: 'bg-harbor-600', port: 'text-foam-400' }
}

export const Berth = memo(function Berth({
  service,
  now,
  grouped = false,
  serviceCount = 0,
  nested = false,
  onPin,
  onRename,
  onStop,
  cpu,
  cores = 1,
}: BerthProps) {
  const [renaming, setRenaming] = useState(false)
  const [draft, setDraft] = useState('')
  const [copied, setCopied] = useState(false)
  // Stopping needs one deliberate confirmation. A modal for a reversible local
  // action is overkill, but an un-armed button next to "copy" invites misfires.
  const [armed, setArmed] = useState(false)
  const [stopError, setStopError] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (renaming) inputRef.current?.select()
  }, [renaming])

  const { bar, port } = tone(service)
  const up = uptime(service.startedAt, now)
  const clickable = Boolean(service.url)
  const { primary, secondary } = lines(service, grouped)

  const startRename = () => {
    setDraft(service.meta.nickname || primaryName(service))
    setRenaming(true)
  }

  const commitRename = () => {
    const next = draft.trim()
    onRename(service.key, next === service.label ? '' : next)
    setRenaming(false)
  }

  useEffect(() => {
    if (!armed) return
    const id = setTimeout(() => setArmed(false), 4000)
    return () => clearTimeout(id)
  }, [armed])

  const doStop = async () => {
    if (!onStop) return
    if (!armed) {
      setArmed(true)
      return
    }
    setArmed(false)
    const error = await onStop({ port: service.port, withServices: serviceCount > 0 })
    setStopError(error ?? null)
  }

  const copyUrl = async () => {
    const text = service.url ?? `localhost:${service.port}`
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1400)
    } catch {
      // Clipboard access can be refused; the URL is visible either way.
    }
  }

  return (
    <li
      className={[
        'group relative isolate overflow-hidden rounded-xl',
        nested
          ? 'border border-harbor-800/50 bg-harbor-950/60'
          : 'border border-harbor-800/80 bg-harbor-900/70',
        'transition-colors duration-150',
        clickable ? 'hover:border-lit-400/40 hover:bg-harbor-850' : 'hover:bg-harbor-850/60',
        service.fresh ? 'animate-dock-in ring-1 ring-lantern-400/50' : '',
      ].join(' ')}
    >
      {/* One amber sweep the first time a service shows up. */}
      {service.fresh && (
        <span
          aria-hidden
          className="animate-sweep pointer-events-none absolute inset-y-0 w-1/3 bg-gradient-to-r from-transparent via-lantern-400/12 to-transparent"
        />
      )}

      {/* Stretched link: clicking anywhere on the berth opens the app. Keeping
          this an anchor preserves cmd-click, middle-click, and copy-link. */}
      {clickable && (
        <a
          href={service.url}
          target="_blank"
          rel="noreferrer"
          className="absolute inset-0 z-0"
          aria-label={`Open ${primaryName(service)} on port ${service.port}`}
        />
      )}

      <div className="pointer-events-none relative z-10 flex items-center gap-3 px-3.5 py-2.5">
        <span aria-hidden className={`absolute left-0 top-0 h-full w-[3px] ${bar}`} />

        {/* Berth number. */}
        <div
          className="flex w-[4.1rem] shrink-0 items-baseline gap-0.5"
          title={service.wildcard ? 'Listening on all interfaces' : 'Listening on loopback only'}
        >
          <span className="font-mono text-[0.6rem] text-foam-400/70">:</span>
          <span className={`tnum font-mono text-[1.2rem] font-medium leading-none tracking-tight ${port}`}>
            {service.port}
          </span>
        </div>

        {/* The icon slot is always reserved so names stay in one column even
            when a dev server serves no favicon. */}
        <div className="grid size-4 shrink-0 place-items-center">
          {service.probe.http && (
            <img
              src={`/api/favicon?port=${service.port}`}
              alt=""
              aria-hidden
              width={16}
              height={16}
              className="size-4 rounded-[3px]"
              onError={(e) => {
                e.currentTarget.style.visibility = 'hidden'
              }}
            />
          )}
        </div>

        {/* Identity. */}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            {renaming ? (
              <input
                ref={inputRef}
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onBlur={commitRename}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') commitRename()
                  if (e.key === 'Escape') setRenaming(false)
                }}
                className="pointer-events-auto w-full max-w-64 rounded-md border border-lit-400/50 bg-harbor-950 px-2 py-0.5 text-[0.95rem] font-semibold text-foam-50 outline-none"
                aria-label="Service name"
              />
            ) : (
              <span
                className={[
                  'truncate tracking-[-0.01em] text-foam-50',
                  // A worker's script reads as code, the project name as a name.
                  grouped && primary === service.entry
                    ? 'font-mono text-[0.88rem] font-medium'
                    : 'text-[0.95rem] font-semibold',
                ].join(' ')}
              >
                {primary}
              </span>
            )}

            {serviceCount > 0 && (
              <span
                title={`Fronts ${serviceCount} supporting ${
                  serviceCount === 1 ? 'service' : 'services'
                } in this project`}
                className="stencil shrink-0 rounded border border-lit-400/30 bg-lit-600/15 px-1.5 py-0.5 text-lit-300"
              >
                +{serviceCount} svc
              </span>
            )}

            {service.meta.pinned && (
              <span aria-label="Pinned" className="shrink-0 text-lantern-400">
                ★
              </span>
            )}
          </div>

          <div className="mt-0.5 truncate font-mono text-[0.7rem] text-foam-300">{secondary}</div>
        </div>

        {/* Badges. Only what varies between rows earns a badge here. */}
        <div className="hidden shrink-0 items-center gap-2 md:flex">
          {service.framework && (
            <span className="stencil rounded border border-harbor-700 bg-harbor-800/70 px-1.5 py-0.5 text-foam-300">
              {service.framework}
            </span>
          )}
          {service.probeSkipped ? (
            <span
              title="Excluded from HTTP probing by --no-probe, so Marina never contacts it"
              className="stencil rounded border border-harbor-700 px-1.5 py-0.5 text-foam-400"
            >
              not probed
            </span>
          ) : (
            !service.probe.http &&
            service.kind === 'app' && (
              <span
                title="Listening, but not answering HTTP yet — it may still be compiling"
                className="stencil rounded border border-coral-400/40 px-1.5 py-0.5 text-coral-300"
              >
                no http
              </span>
            )
          )}
        </div>

        {/* What it is costing. Only when measured and not idle, so a quiet row
            stays quiet. */}
        {cpu !== undefined && cpu >= 1 && (
          <div className="hidden w-16 shrink-0 md:block">
            <LoadMeter cpu={cpu} cores={cores} showValue />
          </div>
        )}

        {/* Uptime. */}
        <div className="w-[4.5rem] shrink-0 text-right">
          {up && <span className="tnum font-mono text-[0.7rem] text-foam-300">{up}</span>}
        </div>

        {/* Actions become visible on hover or keyboard focus. */}
        <div className="pointer-events-auto flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
          <IconButton
            label={service.meta.pinned ? 'Unpin' : 'Pin to top'}
            onClick={() => onPin(service.key, !service.meta.pinned)}
            active={service.meta.pinned}
          >
            ★
          </IconButton>
          <IconButton label="Rename" onClick={startRename}>
            ✎
          </IconButton>
          <IconButton label={copied ? 'Copied' : 'Copy address'} onClick={copyUrl}>
            {copied ? '✓' : '⧉'}
          </IconButton>
          {onStop && (
            <button
              type="button"
              onClick={doStop}
              title={
                armed
                  ? 'Click again to stop'
                  : serviceCount > 0
                    ? `Stop this app and its ${serviceCount} services`
                    : 'Stop this app'
              }
              aria-label={armed ? 'Confirm stop' : 'Stop'}
              className={[
                'grid h-7 place-items-center rounded-md text-[0.8rem] transition-colors',
                armed
                  ? 'w-auto bg-coral-400/20 px-2 font-mono text-[0.66rem] text-coral-300'
                  : 'w-7 text-foam-400 hover:bg-coral-400/20 hover:text-coral-300',
              ].join(' ')}
            >
              {armed ? (serviceCount > 0 ? `stop all ${serviceCount + 1}?` : 'stop?') : '⏻'}
            </button>
          )}
        </div>
      </div>

      {/* A refused stop explains itself — infrastructure and Marina are off limits. */}
      {stopError && (
        <p className="relative z-10 border-t border-coral-400/25 bg-coral-400/5 px-4 py-1.5 text-[0.72rem] text-coral-300">
          {stopError}
        </p>
      )}
    </li>
  )
})

function IconButton({
  label,
  onClick,
  active,
  children,
}: {
  label: string
  onClick: () => void
  active?: boolean
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      onClick={onClick}
      className={[
        'grid size-7 place-items-center rounded-md text-[0.8rem] transition-colors',
        'hover:bg-harbor-700',
        active ? 'text-lantern-400' : 'text-foam-400 hover:text-foam-50',
      ].join(' ')}
    >
      {children}
    </button>
  )
}
