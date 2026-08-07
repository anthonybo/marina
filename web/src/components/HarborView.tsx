import { memo, useEffect, useMemo, useState, type CSSProperties } from 'react'
import type { Ashore, Service } from '../lib/types'
import { LoadMeter } from './LoadMeter'
import {
  distress,
  distressLabel,
  loadLevel,
  type AppHealth,
  type HealthState,
} from '../lib/useHealth'
import {
  clusterServices,
  isToolPort,
  primaryName,
  secondaryName,
  uptime,
  type Cluster,
} from '../lib/format'
import { RootsEditor } from './RootsEditor'

/**
 * The harbour: the same data as the manifest, read as a place.
 *
 * Every visual choice here carries a fact rather than decorating one. A boat is
 * out on the water because its port answers HTTP; it sits at the pier because it
 * is listening but not serving yet; it has a wake because it started seconds
 * ago. Infrastructure is built on the shore because that is what infrastructure
 * is. Nothing is encoded by motion alone, so the scene still reads with
 * animations off.
 */

/**
 * Fleet colours — one per project, chosen deterministically from the name so a
 * project keeps its colour between reloads.
 *
 * Amber is deliberately absent: it is reserved for "just started" and "pinned",
 * and a fleet that happened to hash to amber would make those meaningless.
 */
const FLEET = [
  '#3fe0c8', // aqua
  '#a78bfa', // orchid
  '#68d5ff', // sky
  '#b8e986', // sea grass
  '#ff9edb', // rose quartz
  '#7dd3fc', // pale sky
  '#c4b5fd', // pale orchid
  '#5eead4', // pale aqua
]

function hash(value: string): number {
  let h = 0
  for (let i = 0; i < value.length; i++) h = (h * 31 + value.charCodeAt(i)) | 0
  return Math.abs(h)
}

const fleetColor = (name: string) => FLEET[hash(name) % FLEET.length]

/**
 * Reserved for a boat in distress, and used nowhere else in the harbour.
 *
 * Amber already means "just started" and "pinned", and the fleet palette is
 * deliberately cool, so a coral hull cannot be mistaken for a project that merely
 * hashed to a warm colour.
 */
const DISTRESS = '#ff6b81'

interface HarborViewProps {
  services: Service[]
  ashore: Ashore[]
  ashoreSkipped: number
  now: number
  onPin: (key: string, pinned: boolean) => void
  onStart: (path: string) => void
  /** Stops a running app. Every boat needs this, not just the manifest rows. */
  onStop: (target: { port: number; withServices?: boolean }) => Promise<string | null> | void
  /** Live per-app load, so a boat can show what it is costing. */
  health: HealthState
}

export function HarborView({
  services,
  ashore,
  ashoreSkipped,
  now,
  onPin,
  onStart,
  onStop,
  health,
}: HarborViewProps) {
  const { fishing, moored, shore, shoreCount, buoys, netted } = useMemo(() => {
    const apps = services.filter((s) => s.kind === 'app' || s.kind === 'unknown')
    const infra = services.filter((s) => s.kind === 'infra')
    // One boat per project front door. Its workers ride on its net rather than
    // crowding the water as if they were apps of their own.
    // A boat is a server somebody chose to run on a port somebody chose. A cluster
    // whose front door is an OS-assigned port is a tool, not a project, and drawing
    // it made the scene flicker: see isToolPort.
    const clusters = clusterServices(apps).filter((c) => !isToolPort(c.primary.port))
    return {
      fishing: clusters.filter((c) => c.primary.probe.http),
      moored: clusters.filter((c) => !c.primary.probe.http),
      shore: groupShore(infra),
      // Buildings are grouped by service, so the headline count has to come from
      // the services themselves or it would under-report the harbour.
      shoreCount: infra.length,
      netted: clusters.reduce((n, c) => n + c.services.length, 0),
      buoys: services.filter((s) => s.kind === 'system'),
    }
  }, [services])

  return (
    <div className="overflow-hidden rounded-2xl border border-harbor-800 bg-harbor-975">
      {/* ── Sky ─────────────────────────────────────────────────────────── */}
      <div className="relative h-20 overflow-hidden bg-gradient-to-b from-[#04121a] to-[#0a2836]">
        <Stars />
        <Moon />
        <p className="absolute bottom-2 right-5 font-mono text-[0.66rem] text-foam-400">
          {fishing.length} out fishing
          {netted > 0 && ` · ${netted} on their nets`}
          {moored.length > 0 && ` · ${moored.length} moored`} · {shoreCount} on shore
          {ashore.length > 0 && ` · ${ashore.length} in the boatyard`}
        </p>
        <Lighthouse />
      </div>

      {/* ── The sea. A real body of water, so the boats sit on something. ── */}
      <div className="relative">
        <Horizon />
        <div className="relative bg-gradient-to-b from-[#0c3446] via-[#082935] to-[#051c27]">
          {fishing.length === 0 ? (
            <p className="px-5 py-12 text-center font-mono text-[0.78rem] text-foam-300">
              Nothing out on the water. Start a dev server and it will set sail here.
            </p>
          ) : (
            <div className="flex flex-wrap items-start justify-center gap-x-4 gap-y-6 px-4 pb-4 pt-5">
              {fishing.map((cluster) => (
                <Flotilla
                  key={cluster.id}
                  cluster={cluster}
                  now={now}
                  onPin={onPin}
                  onStop={onStop}
                  health={health}
                />
              ))}
            </div>
          )}
          {/* Open water below the fleet. In flow rather than absolute, so a tall
              flotilla can never have a swell drawn across its caption. */}
          <div className="flex flex-col gap-2 pb-3">
            <Swell offset={0} />
            <Swell offset={1} />
          </div>
        </div>
      </div>

      {/* ── The pier, where boats that aren't serving yet stay tied up ─────
          Always here, empty or not, and always the same height.

          It used to mount only when something was moored, so a dev server
          starting inserted a whole section and shoved every row below it down
          the page — including the boatyard, which is where you read a project's
          tooltip to find out which port it wants. A layout that moves while you
          are reading it is worse than one that shows an empty pier, and a real
          harbour has a pier whether or not anything is tied to it.

          The height is reserved rather than merely reactive: one row of boats
          fits in it, so the common case of one or two things compiling changes
          what is drawn without changing where anything sits. */}
      <section className="relative min-h-[10rem] border-t-2 border-[#143c4f] bg-[#08222e] px-4 pb-5 pt-4">
        <SectionLabel>
          {moored.length > 0
            ? 'At the pier · listening, not answering HTTP yet'
            : 'At the pier · nothing tied up'}
        </SectionLabel>
        {moored.length === 0 ? (
          <p className="pt-3 font-mono text-[0.66rem] text-foam-400/70">
            A server that is listening but still compiling waits here until it answers.
          </p>
        ) : (
          <div className="flex flex-wrap items-start gap-x-3 gap-y-4">
            {moored.map((cluster) => (
              <Flotilla
                key={cluster.id}
                cluster={cluster}
                now={now}
                onPin={onPin}
                onStop={onStop}
                health={health}
                moored
              />
            ))}
          </div>
        )}
        <Planks />
      </section>

      {/* ── Boatyard: yours, hauled out, ready to put back in the water ────
          Shown even when empty: this is where the scanned-directory control lives,
          and an empty boatyard is exactly when you need it. */}
      <section className="relative border-t border-harbor-700/60 bg-[#071e28] px-4 pb-4 pt-4">
        <SectionLabel>
          <LegendCradle />{' '}
          {ashore.length > 0
            ? `In the boatyard · ${ashore.length} not running · click to launch`
            : 'In the boatyard · nothing hauled out'}
          {/* Which directories this section is drawn from. */}
          <RootsEditor />
        </SectionLabel>
        {/* Aligned columns, not wrapped chips. Variable-width chips produced ragged
            rows with nothing lining up vertically, so twenty-two calm items read as
            clutter. Fixed columns give the eye an edge to run down, which is what
            makes a long list scannable. */}
        <ul
          className="grid gap-x-7 gap-y-px"
          style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(13.5rem, 1fr))' }}
        >
          {ashore.map((project) => (
            <Cradled key={project.path} project={project} onStart={onStart} />
          ))}
        </ul>
        {ashoreSkipped > 0 && (
          <p className="mt-3 font-mono text-[0.62rem] text-foam-400">
            {ashoreSkipped} more {ashoreSkipped === 1 ? 'directory' : 'directories'} with no start
            command Marina recognises.
          </p>
        )}
      </section>

      {/* ── Shore: the infrastructure everything else depends on ─────────── */}
      {shore.length > 0 && (
        <section className="border-t border-harbor-700/60 bg-[#061c26] px-4 py-4">
          <SectionLabel>On shore · databases and services</SectionLabel>
          <div className="flex flex-wrap items-end gap-2">
            {shore.map((group) => (
              <Building key={group.label} group={group} now={now} />
            ))}
          </div>
        </section>
      )}

      {/* ── Buoys: the machine's own port-holders, present but not yours ─── */}
      {buoys.length > 0 && (
        <section className="border-t border-harbor-800 px-5 py-3">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <span className="stencil text-foam-400">Buoys</span>
            {buoys.map((service) => (
              <span
                key={service.key}
                title={`${service.label} on port ${service.port}`}
                className="flex items-center gap-1.5 font-mono text-[0.68rem] text-foam-400"
              >
                <svg viewBox="0 0 12 16" className="h-3.5 w-2.5" aria-hidden>
                  <path d="M6 1v6" stroke="#2a7089" strokeWidth="1.4" strokeLinecap="round" />
                  <circle cx="6" cy="10.5" r="3.4" fill="#143c4f" stroke="#2a7089" strokeWidth="1.2" />
                  <circle cx="6" cy="1.5" r="1.3" fill="#8fb8c8" />
                </svg>
                {service.port}
              </span>
            ))}
          </div>
        </section>
      )}

      <Legend />
    </div>
  )
}

/* ── Flotillas ─────────────────────────────────────────────────────────── */

interface FlotillaProps {
  cluster: Cluster
  now: number
  onPin: (key: string, pinned: boolean) => void
  onStop: (target: { port: number; withServices?: boolean }) => Promise<string | null> | void
  /** Live per-app load, so a boat can show what it is costing. */
  health: HealthState
  moored?: boolean
}

/**
 * A project as one unit: the boat you'd actually open, and the workers that only
 * exist to serve it strung out behind on its net.
 *
 * This is the whole point of clustering. Thirteen boats on the water implied
 * thirteen apps; one boat trailing twelve floats says what is actually true —
 * one app, twelve services — while keeping every port individually clickable.
 */
const Flotilla = memo(function Flotilla({
  cluster,
  now,
  onPin,
  onStop,
  health,
  moored = false,
}: FlotillaProps) {
  const { primary, services } = cluster
  const color = fleetColor(primary.project || primary.label)
  const hasNet = services.length > 0

  return (
    <div
      className={[
        'flex flex-col items-center rounded-xl',
        // A netted flotilla needs room for its floats; a lone boat does not.
        hasNet ? 'w-full max-w-[19rem] px-1 sm:w-[19rem]' : 'w-[7rem]',
      ].join(' ')}
    >
      <Boat
        service={primary}
        now={now}
        onPin={onPin}
        onStop={onStop}
        serviceCount={services.length}
        load={health.byKey.get(primary.key)}
        cores={health.machine.cores}
        moored={moored}
      />

      {hasNet && (
        <>
          {/* The net line the floats hang from. */}
          <svg
            viewBox="0 0 240 10"
            preserveAspectRatio="none"
            className="mt-1 h-2.5 w-full"
            aria-hidden
          >
            <path
              d="M6 1 Q120 11 234 1"
              fill="none"
              stroke={color}
              strokeWidth="1.2"
              strokeDasharray="3 3"
              opacity="0.5"
            />
          </svg>

          <ul className="flex flex-wrap justify-center gap-x-1.5 gap-y-1">
            {services.map((service) => (
              <Float key={service.key} service={service} color={color} now={now} />
            ))}
          </ul>

          <p className="mt-1.5 font-mono text-[0.58rem] text-foam-400">
            {services.length} {services.length === 1 ? 'service' : 'services'} on the net
          </p>
        </>
      )}
    </div>
  )
})

/**
 * One supporting service, as a float on the net. Small on purpose — it is real
 * and reachable, but it is not an app — while still carrying the port number,
 * which is how you actually identify it.
 */
const Float = memo(function Float({
  service,
  color,
  now,
}: {
  service: Service
  color: string
  now: number
}) {
  const clickable = Boolean(service.url)
  const up = uptime(service.startedAt, now)
  const tint = service.fresh ? '#ffb454' : color

  const Wrapper = clickable ? 'a' : 'div'
  const props = clickable ? { href: service.url, target: '_blank', rel: 'noreferrer' } : {}

  return (
    <li>
      <Wrapper
        {...props}
        title={`${primaryName(service)}${
          service.subpath || service.entry ? ` · ${service.subpath || service.entry}` : ''
        } · port ${service.port}${up ? ` · up ${up}` : ''}${
          clickable ? '' : ' · not answering HTTP'
        }`}
        className={[
          'flex w-[2.7rem] flex-col items-center rounded transition-transform duration-150',
          clickable ? 'cursor-pointer hover:-translate-y-0.5' : 'cursor-default opacity-60',
        ].join(' ')}
      >
        {/* The bob rides on the <svg> itself, never on a child: a transform on an
            SVG child is not compositable, so it would relayout every frame. */}
        <svg
          viewBox="0 0 12 14"
          className={`h-3.5 w-3 ${service.fresh ? 'animate-bobber' : ''}`}
          aria-hidden
        >
          <path d="M6 0.5v3.5" stroke={tint} strokeWidth="1.1" strokeLinecap="round" opacity="0.7" />
          <circle cx="6" cy="8.5" r="3.4" fill="#0a2431" stroke={tint} strokeWidth="1.5" />
        </svg>
        <span className="tnum font-mono text-[0.55rem] leading-none" style={{ color: tint }}>
          {service.port}
        </span>
      </Wrapper>
    </li>
  )
})

/* ── Boats ─────────────────────────────────────────────────────────────── */

interface BoatProps {
  service: Service
  now: number
  onPin: (key: string, pinned: boolean) => void
  onStop?: (target: { port: number; withServices?: boolean }) => Promise<string | null> | void
  serviceCount?: number
  /** This app's current cost, if measured yet, and where it is heading. */
  load?: AppHealth
  cores?: number
  moored?: boolean
}

const Boat = memo(function Boat({
  service,
  now,
  onPin,
  onStop,
  serviceCount = 0,
  load,
  cores = 1,
  moored = false,
}: BoatProps) {
  // Stop arms on the first click, like everywhere else this appears.
  const [armed, setArmed] = useState(false)
  const [stopError, setStopError] = useState<string | null>(null)

  useEffect(() => {
    if (!armed) return
    const id = setTimeout(() => setArmed(false), 4000)
    return () => clearTimeout(id)
  }, [armed])
  const fleet = fleetColor(service.project || service.label)
  const delay = (hash(service.key) % 2600) / 1000
  const up = uptime(service.startedAt, now)
  const clickable = Boolean(service.url)

  // Load, once it has been measured. A boat carrying more work sits lower in the
  // water — three pixels at most, so it reads as weight rather than as motion, and
  // the meter below carries the actual number.
  const cpu = load?.sample.cpu ?? 0
  const level = loadLevel(cpu)
  const weightPx = Math.min(3, (cpu / 400) * 3)

  // Trouble is drawn, not announced. Marina watched an app climb from 0.4 GB to
  // 4.6 GB and said nothing louder than a slightly longer meter bar, and the first
  // anyone knew of it was a frozen machine. A boat going under is impossible to
  // read as normal, which is the entire point.
  const trouble = distress(load?.trend)
  const sinkPx = weightPx + trouble.sink
  // A boat in trouble stops flying its own colours. Nothing else in the harbour is
  // coral, so the change is legible from across the page without reading a word.
  const color = trouble.foundering ? DISTRESS : fleet

  // The project name is the identity and must always appear. This used to prefer
  // `entry`, so the media backend rendered as "server.js" — a filename dozens of
  // projects share — and the project name was demoted to the line below, which then
  // lost it to the uptime because that line was `up ?? detail`. Net effect: a boat
  // labelled with nothing you could act on.
  const label = primaryName(service)
  // `subpath` ahead of `entry`: "backend" says what this is, "server.js" only says
  // how it boots.
  const qualifier = service.subpath || service.entry
  // Both facts, not one or the other.
  const detail = [qualifier, up].filter(Boolean).join(' · ') || secondaryName(service)

  const Wrapper = clickable ? 'a' : 'div'
  const wrapperProps = clickable
    ? { href: service.url, target: '_blank', rel: 'noreferrer' }
    : {}

  return (
    <Wrapper
      {...wrapperProps}
      title={`${service.project ?? service.label}${qualifier ? ` · ${qualifier}` : ''}${
        service.entry && service.entry !== qualifier ? ` · ${service.entry}` : ''
      } · port ${service.port}${up ? ` · up ${up}` : ''}${
        clickable ? '' : ' · not answering HTTP'
      }${load?.trend?.why ? ` · ${load.trend.why}` : ''}`}
      className={[
        'group relative flex w-[7rem] shrink-0 flex-col items-center rounded-lg px-1 pt-1.5',
        'transition-transform duration-200',
        clickable ? 'cursor-pointer hover:-translate-y-1.5' : 'cursor-default',
      ].join(' ')}
    >
      <div
        className={`relative ${moored || trouble.foundering ? '' : 'animate-bob'}`}
        style={{
          animationDelay: `-${delay}s`,
          transform:
            sinkPx || trouble.list
              ? `translateY(${sinkPx}px) rotate(${trouble.list}deg)`
              : undefined,
          transformOrigin: '50% 90%',
          transition: 'transform 700ms ease-out',
        }}
      >
        <svg viewBox="0 0 68 58" className="h-[3.1rem] w-[3.6rem] overflow-visible" aria-hidden>
          {service.meta.pinned && <path d="M34 5 L45 8.5 L34 12 Z" fill="#ffb454" />}
          <path d="M34 6v28" stroke="#cfe4ec" strokeWidth="1.5" strokeLinecap="round" />
          {/* Mainsail and jib, in the fleet's colour. */}
          <path d="M32 10 L32 34 L14 34 Z" fill={color} opacity="0.9" />
          <path d="M36 13 L36 34 L48 34 Z" fill={color} opacity="0.5" />
          {/* Hull: dark, with only a rim of fleet colour so a large fleet doesn't
              flood the scene with one hue. */}
          <path
            d="M9 35 H59 L52.5 46 Q34 50 15.5 46 Z"
            fill="#0a2431"
            stroke={color}
            strokeWidth="1.4"
            strokeLinejoin="round"
            opacity="0.95"
          />
          <path d="M12 40.5 H56" stroke={color} strokeWidth="0.9" opacity="0.3" />

          {/* The cast line — only for boats actually serving. Its float is a DOM
              element just below, not a <circle> here: Blink cannot composite a
              transform on an SVG child, so animating one relayouts the whole
              scene every frame (measured: 60 layouts/s vs 7). */}
          {!moored && service.probe.http && (
            <path d="M59 37 L65.5 45" stroke="#8fb8c8" strokeWidth="0.9" opacity="0.7" />
          )}
        </svg>

        {/* The float, sitting at the end of the cast line. Positioned as a share
            of the boat's box so it tracks the viewBox coordinates above
            (65.5,46.5 of 68x58) at any font size. */}
        {!moored && service.probe.http && (
          <span
            aria-hidden
            className="animate-bobber absolute rounded-full bg-lantern-400"
            style={{
              left: '96.3%',
              top: '80.2%',
              width: '5.9%',
              aspectRatio: '1',
              translate: '-50% -50%',
              animationDelay: `-${delay / 2}s`,
            }}
          />
        )}
      </div>

      {trouble.foundering && (
        <svg
          aria-hidden
          viewBox="0 0 68 14"
          className="pointer-events-none absolute left-1/2 top-[2.1rem] h-[0.8rem] w-[3.6rem] -translate-x-1/2"
        >
          <path
            d="M2 8 Q11 2 20 8 T38 8 T56 8 T74 8 V14 H2 Z"
            fill="#0d4a5f"
            opacity="0.85"
          />
          <path
            d="M2 8 Q11 2 20 8 T38 8 T56 8 T74 8"
            stroke="#9fe8ff"
            strokeWidth="1.1"
            fill="none"
            opacity="0.75"
          />
        </svg>
      )}

      {service.fresh && (
        <span
          aria-hidden
          className="animate-wake pointer-events-none absolute bottom-[2.3rem] h-[2px] w-9 rounded-full bg-lantern-400/70"
        />
      )}

      <span
        className="tnum font-mono text-[0.84rem] font-semibold leading-none"
        style={{ color }}
      >
        {service.port}
      </span>
      <span className="mt-1 w-full truncate text-center font-mono text-[0.62rem] leading-tight text-foam-100">
        {label}
      </span>
      <span
        className="w-full truncate text-center font-mono text-[0.57rem] leading-tight"
        style={{ color: trouble.foundering ? DISTRESS : undefined }}
        title={trouble.foundering ? load?.trend?.why : undefined}
      >
        {trouble.foundering && load?.trend ? distressLabel(load.trend) : detail}
      </span>

      {/* What this app is costing right now. Absent until the first two samples
          land, rather than showing a misleading 0%. The number only appears once
          it is doing something, so a quiet harbour stays quiet. */}
      {load && (
        <LoadMeter
          cpu={cpu}
          cores={cores}
          showValue={level !== 'idle'}
          className="mt-1.5 w-full px-1"
        />
      )}

      {onStop && (
        <button
          type="button"
          aria-label={armed ? 'Confirm stop' : 'Stop'}
          title={
            armed
              ? 'Click again to stop'
              : serviceCount > 0
                ? `Stop ${primaryName(service)} and its ${serviceCount} services`
                : `Stop ${primaryName(service)}`
          }
          onClick={async (e) => {
            e.preventDefault()
            e.stopPropagation()
            if (!armed) {
              setArmed(true)
              return
            }
            setArmed(false)
            const error = await onStop({ port: service.port, withServices: serviceCount > 0 })
            setStopError(error ?? null)
          }}
          className={[
            'absolute left-0 top-0 z-10 grid place-items-center rounded text-[0.68rem]',
            'transition-opacity hover:bg-coral-400/20',
            armed
              ? 'h-5 w-auto bg-coral-400/25 px-1 font-mono text-[0.55rem] text-coral-300 opacity-100'
              : 'size-5 text-foam-400 opacity-0 hover:text-coral-300 group-hover:opacity-100 focus-visible:opacity-100',
          ].join(' ')}
        >
          {armed ? 'stop?' : '⏻'}
        </button>
      )}

      {stopError && (
        <span className="absolute -bottom-4 left-0 right-0 z-10 text-center text-[0.55rem] leading-tight text-coral-300">
          {stopError}
        </span>
      )}

      <button
        type="button"
        aria-label={service.meta.pinned ? 'Unpin' : 'Pin to top'}
        title={service.meta.pinned ? 'Unpin' : 'Pin to top'}
        onClick={(e) => {
          e.preventDefault()
          e.stopPropagation()
          onPin(service.key, !service.meta.pinned)
        }}
        className={[
          'absolute right-0 top-0 z-10 grid size-5 place-items-center rounded text-[0.7rem]',
          'transition-opacity hover:bg-harbor-700',
          service.meta.pinned
            ? 'text-lantern-400 opacity-100'
            : 'text-foam-400 opacity-0 group-hover:opacity-100 focus-visible:opacity-100',
        ].join(' ')}
      >
        ★
      </button>
    </Wrapper>
  )
})

/**
 * One hauled-out project, as a row in an aligned column.
 *
 * Three things were making twenty-two of these read as clutter, and all three are
 * gone here:
 *
 *   1. The hull glyph was drawn once per project. Twenty-two identical marks in
 *      identical states carry no information — they are pure repetition. One mark
 *      now sits in the section label, where it still explains the metaphor.
 *   2. Chips were variable-width and wrapped, so no two rows lined up. Fixed grid
 *      columns give the eye a left edge to run down.
 *   3. The framework sat immediately beside the name at near-equal weight. Pushed
 *      to the right edge of its column and dimmed, it becomes a second alignment
 *      edge instead of noise next to the thing you are actually reading.
 */
const Cradled = memo(function Cradled({
  project,
  onStart,
}: {
  project: Ashore
  onStart: (path: string) => void
}) {
  const { starting, failed } = project
  const conflicts = project.conflicts ?? []
  const port = (project.expect ?? [])[0]

  // A failed launch has to look failed. Reporting "starting" forever while the
  // log already said the command was not found is the bug this state exists for.
  const nameColor = failed
    ? '#ff9aa6'
    : starting
      ? '#ffc978'
      : conflicts.length > 0
        ? '#ffc978'
        : '#c4dde8'
  const status = starting ? 'starting…' : failed ? 'failed' : project.framework || project.manager

  return (
    <li>
      <button
        type="button"
        onClick={() => onStart(project.path)}
        disabled={starting}
        title={
          failed
            ? `Failed: ${project.error ?? 'the launch did not survive'} — click to retry ${project.command}`
            : starting
              ? `Starting: ${project.command}`
              : conflicts.length > 0
                ? `${project.command} — but :${conflicts[0].port} is already in use by ${conflicts[0].heldBy}`
                : `Run: ${project.command}`
        }
        className={[
          'group flex w-full items-baseline gap-2 rounded px-1.5 py-[0.18rem] text-left',
          'transition-colors duration-100',
          starting
            ? 'cursor-default bg-lantern-400/10'
            : failed
              ? 'cursor-pointer bg-coral-400/10 hover:bg-coral-400/20'
              : 'cursor-pointer hover:bg-harbor-800/70',
        ].join(' ')}
      >
        <span
          className="truncate font-mono text-[0.7rem] leading-snug"
          style={{ color: nameColor }}
        >
          {project.name}
        </span>
        {port && (
          <span
            className={[
              'tnum shrink-0 font-mono text-[0.58rem] leading-snug',
              conflicts.some((c) => c.port === port.port)
                ? 'text-coral-300'
                : port.source === 'default'
                  ? 'text-foam-400/50'
                  : 'text-foam-400/80',
            ].join(' ')}
          >
            :{port.port}
            {conflicts.some((c) => c.port === port.port) && '⚠'}
          </span>
        )}
        <span
          className={[
            'ml-auto shrink-0 font-mono text-[0.55rem] leading-snug',
            starting ? 'text-lantern-300' : failed ? 'text-coral-300' : 'text-foam-400/60',
          ].join(' ')}
        >
          {status}
        </span>
      </button>
    </li>
  )
})

/* ── Shore buildings ───────────────────────────────────────────────────── */

interface ShoreGroup {
  label: string
  ports: number[]
  /** The port to open, if any of this service's ports speak HTTP. */
  url?: string
  startedAt?: number
}

/**
 * One service, one building — even when it holds six ports. Jaeger alone
 * occupies four, and six identical sheds in a row is noise rather than
 * information.
 */
function groupShore(services: Service[]): ShoreGroup[] {
  const groups = new Map<string, ShoreGroup>()
  for (const s of services) {
    const existing = groups.get(s.label)
    if (existing) {
      existing.ports.push(s.port)
      if (!existing.url && s.url) existing.url = s.url
      continue
    }
    groups.set(s.label, {
      label: s.label,
      ports: [s.port],
      url: s.url,
      startedAt: s.startedAt,
    })
  }
  return [...groups.values()].map((g) => ({ ...g, ports: g.ports.sort((a, b) => a - b) }))
}

const Building = memo(function Building({ group, now }: { group: ShoreGroup; now: number }) {
  const color = fleetColor(group.label)
  const up = uptime(group.startedAt, now)
  const clickable = Boolean(group.url)
  const extra = group.ports.length - 1

  const Wrapper = clickable ? 'a' : 'div'
  const props = clickable ? { href: group.url, target: '_blank', rel: 'noreferrer' } : {}

  return (
    <Wrapper
      {...props}
      title={`${group.label} · port${group.ports.length > 1 ? 's' : ''} ${group.ports.join(', ')}${
        up ? ` · up ${up}` : ''
      }${clickable ? '' : ' · speaks its own protocol, not HTTP'}`}
      className={[
        'relative flex w-[5.8rem] flex-col items-center rounded-lg px-1 py-1 transition-transform duration-200',
        clickable ? 'cursor-pointer hover:-translate-y-0.5' : 'cursor-default',
      ].join(' ')}
    >
      <svg viewBox="0 0 52 42" className="h-11 w-[3.1rem]" aria-hidden>
        <path d="M5 19 L26 6 L47 19 Z" fill={color} opacity="0.75" />
        <rect x="9" y="19" width="34" height="19" rx="1.5" fill="#0a2431" stroke={color} strokeWidth="1.3" />
        <rect x="21" y="27" width="10" height="11" rx="0.8" fill={color} opacity="0.3" />
        <circle cx="26" cy="23" r="1.5" fill={color} className="animate-lamp" />
      </svg>
      <span className="tnum font-mono text-[0.7rem] font-semibold" style={{ color }}>
        {group.ports[0]}
        {extra > 0 && <span className="ml-1 text-[0.58rem] text-foam-400">+{extra}</span>}
      </span>
      <span className="w-full truncate text-center font-mono text-[0.58rem] leading-tight text-foam-300">
        {group.label}
      </span>
    </Wrapper>
  )
})

/* ── Scenery ───────────────────────────────────────────────────────────── */

function Stars() {
  // Fixed positions: a night sky that twinkles differently on every render would
  // be noise, not atmosphere.
  const stars = [
    [16, 34], [26, 66], [35, 24], [47, 52], [55, 20], [64, 58],
    [71, 30], [79, 70], [86, 40], [93, 22], [97, 62], [10, 74],
  ]
  return (
    <svg className="absolute inset-0 h-full w-full" aria-hidden>
      {stars.map(([x, y], i) => (
        <circle
          key={i}
          cx={`${x}%`}
          cy={`${y}%`}
          r={i % 3 === 0 ? 1.1 : 0.7}
          fill="#dcecf1"
          opacity={0.2 + (i % 4) * 0.13}
        />
      ))}
    </svg>
  )
}

function Moon() {
  return (
    <svg viewBox="0 0 40 40" className="absolute right-6 top-1.5 h-9 w-9" aria-hidden>
      <circle cx="20" cy="20" r="13" fill="#dcecf1" opacity="0.1" />
      <circle cx="20" cy="20" r="8" fill="#eef7f9" opacity="0.55" />
    </svg>
  )
}

/**
 * The lighthouse stands on a headland at the left of the harbour mouth. Its beam
 * is the one thing on the page that travels far enough to catch the eye, which
 * is exactly what a lighthouse is for.
 */
function Lighthouse() {
  return (
    <div className="absolute bottom-0 left-5 h-full">
      <span
        aria-hidden
        className="animate-sweep-beam absolute bottom-3 left-1/2 h-28 w-32 -translate-x-1/2"
        style={{
          background:
            'conic-gradient(from 166deg at 50% 100%, transparent 0deg, rgba(255,180,84,0.26) 10deg, transparent 21deg)',
        }}
      />
      <svg viewBox="0 0 30 62" className="relative h-full w-[1.9rem]" aria-hidden>
        {/* Headland. */}
        <path d="M0 62 Q9 52 15 52 Q23 52 30 62 Z" fill="#04121a" />
        {/* Lamp room. */}
        <circle cx="15" cy="9" r="3" fill="#ffb454" className="animate-lamp" />
        <path d="M10.5 12.5 h9 l1.2 5 h-11.4 z" fill="#143c4f" stroke="#2a7089" strokeWidth="0.9" />
        {/* Tower, tapering, with its two painted bands. */}
        <path d="M9.5 17.5 h11 l2.2 35 h-15.4 z" fill="#0e2b3a" stroke="#2a7089" strokeWidth="1" />
        <path d="M10.4 26 h9.2" stroke="#ff7a8a" strokeWidth="2.2" opacity="0.5" />
        <path d="M9.9 38 h10.2" stroke="#ff7a8a" strokeWidth="2.2" opacity="0.5" />
      </svg>
    </div>
  )
}

/** The horizon: a crisp lit line where the sky stops and the water starts. */
function Horizon() {
  return (
    <>
      <div aria-hidden className="h-px w-full bg-lit-400/35" />
      {/* The moving strip is clipped by this wrapper; --drift-period must match
          the gradient's repeat length or the loop will visibly jump. */}
      <div
        aria-hidden
        className="relative h-[2px] w-full overflow-hidden opacity-45"
        style={{ '--drift-period': '50px' } as CSSProperties}
      >
        <div
          className="animate-drift"
          style={{
            background:
              'repeating-linear-gradient(90deg, transparent 0 16px, rgba(111,240,220,0.55) 16px 34px, transparent 34px 50px)',
          }}
        />
      </div>
    </>
  )
}

/** A band of moving water beneath each lane of boats. */
function Swell({ offset, className = '' }: { offset: number; className?: string }) {
  return (
    <div
      aria-hidden
      className={`pointer-events-none relative h-[3px] w-full overflow-hidden ${className}`}
      style={{ '--drift-period': '86px', opacity: 0.32 } as CSSProperties}
    >
      <div
        className="animate-drift"
        style={{
          background:
            'repeating-linear-gradient(90deg, transparent 0 26px, rgba(143,184,200,0.5) 26px 58px, transparent 58px 86px)',
          animationDuration: `${30 + offset * 7}s`,
          animationDirection: offset % 2 === 0 ? 'normal' : 'reverse',
        }}
      />
    </div>
  )
}

function Planks() {
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute bottom-0 left-0 h-2 w-full opacity-60"
      style={{ background: 'repeating-linear-gradient(90deg, #143c4f 0 20px, #0a2431 20px 23px)' }}
    />
  )
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  // A div rather than a p: a label can carry a control beside its text, and a p
  // may not contain block content — the browser would close the paragraph early
  // and reparent the rest.
  return <div className="stencil mb-2.5 flex items-center gap-1.5 text-foam-400">{children}</div>
}

function Legend() {
  const items = [
    { swatch: <LegendBoat fishing />, text: 'out fishing — answering HTTP' },
    { swatch: <LegendBoat />, text: 'moored — listening only' },
    { swatch: <span className="h-[2px] w-6 rounded-full bg-lantern-400" />, text: 'wake — just started' },
    { swatch: <LegendNet />, text: 'on the net — services behind an app' },
    { swatch: <LegendCradle />, text: 'in the boatyard — not running' },
    { swatch: <span className="text-lantern-400">★</span>, text: 'pennant — pinned' },
    { swatch: <span className="font-mono text-[0.6rem] text-foam-400">+n</span>, text: 'extra ports on one service' },
  ]
  return (
    <footer className="flex flex-wrap items-center gap-x-5 gap-y-2 border-t border-harbor-800 bg-harbor-975 px-5 py-2.5">
      {items.map((item, i) => (
        <span key={i} className="flex items-center gap-2 font-mono text-[0.64rem] text-foam-400">
          {item.swatch}
          {item.text}
        </span>
      ))}
    </footer>
  )
}

function LegendNet() {
  return (
    <svg viewBox="0 0 34 14" className="h-4 w-6" aria-hidden>
      <path d="M2 3 Q17 9 32 3" fill="none" stroke="#a78bfa" strokeWidth="1" strokeDasharray="2.5 2.5" opacity="0.6" />
      {[7, 17, 27].map((x) => (
        <g key={x}>
          <path d={`M${x} 6v2`} stroke="#a78bfa" strokeWidth="1" opacity="0.7" />
          <circle cx={x} cy="10.5" r="2.2" fill="#0a2431" stroke="#a78bfa" strokeWidth="1.2" />
        </g>
      ))}
    </svg>
  )
}

function LegendCradle() {
  // Matches the boatyard chip's stroke weight, so the legend reads as the same mark.
  return (
    <svg viewBox="0 0 34 22" className="h-3 w-[1.05rem] shrink-0" aria-hidden>
      <path d="M4 5 H30 L26.5 14 Q17 17 7.5 14 Z" fill="none" stroke="#2a7089" strokeWidth="1.8" strokeLinejoin="round" />
      <path d="M10 18 L12.5 14 M24 18 L21.5 14" stroke="#2a7089" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  )
}

function LegendBoat({ fishing = false }: { fishing?: boolean }) {
  const color = fishing ? '#3fe0c8' : '#2a7089'
  return (
    <svg viewBox="0 0 30 20" className="h-4 w-5" aria-hidden>
      <path d="M15 3v8" stroke="#cfe4ec" strokeWidth="1.2" strokeLinecap="round" />
      <path d="M14 4 L14 11 L7 11 Z" fill={color} />
      <path d="M4 12 H26 L23 17 Q15 19 7 17 Z" fill="#0a2431" stroke={color} strokeWidth="1.1" />
      {fishing && <circle cx="28" cy="16" r="1.4" fill="#ffb454" />}
    </svg>
  )
}

/* ── helpers ───────────────────────────────────────────────────────────── */
