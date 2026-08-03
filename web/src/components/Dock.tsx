import { useEffect, useMemo, useRef, useState } from 'react'
import type { NetInfo, Service } from '../lib/types'
import { clusterServices, primaryName, uptime } from '../lib/format'

/**
 * The page you get on a phone, at marina.local.
 *
 * A different job from the dashboard, so a different page. There is nothing to
 * administer here — starting and stopping apps is refused from the network on
 * purpose — so this is one question answered large: what is running, and take me
 * to it. Boats are thumb-sized, the port is the biggest thing on each, and the
 * whole scene fits a portrait screen without scrolling for the common case.
 *
 * # Motion marks events, it does not idle
 *
 * Boats sail in when an app starts and sail out when it stops, and are otherwise
 * still. That is the opposite of ambient animation, deliberately: measured
 * earlier in this project, a single perpetual CSS animation costs ~15% of a CPU
 * core because it stops the browser ever going idle, and this page is meant to be
 * left open on a phone. Motion that only happens when something happens is both
 * cheaper and more informative — if a boat moves, something really did change.
 *
 * # Reachability is not decoration here
 *
 * An app bound to loopback cannot be opened from the device reading this page,
 * however correct the address is — Vite and Astro bind localhost by default. Those
 * are drawn moored and are not tappable, because a link that cannot work is worse
 * than an absent one.
 */
interface DockProps {
  services: Service[]
  net: NetInfo | undefined
  now: number
  /** True when viewed on the machine Marina runs on, which can reach loopback. */
  local: boolean
}

/** How long a departing boat stays on screen to sail out. */
const DEPART_MS = 1100

interface Boat {
  key: string
  port: number
  name: string
  detail: string
  framework: string
  /** Reachable from the device reading this page. */
  open: boolean
  /** http or https — a TLS dev server linked as http just fails to connect. */
  scheme: string
  /** Absent when the process's start time could not be read. */
  startedAt: number | undefined
  /** Set while the boat is leaving, so it animates out before being dropped. */
  departing?: boolean
}

export function Dock({ services, net, now, local }: DockProps) {
  const host = window.location.hostname

  const present = useMemo<Boat[]>(() => {
    const apps = services.filter(
      (s) =>
        (s.kind === 'app' || s.kind === 'unknown') &&
        // Marina is a running app like any other, but a boat that reloads the page
        // you are already reading is a joke at the reader's expense.
        s.project !== 'Marina',
    )
    return clusterServices(apps)
      .filter((c) => c.primary.probe.http)
      .map((c) => {
        const s = c.primary
        return {
          key: s.key,
          port: s.port,
          name: primaryName(s),
          detail: c.services.length > 0 ? `+${c.services.length} behind it` : (s.subpath ?? ''),
          framework: s.framework ?? '',
          // Loopback-bound apps are unreachable from another device. On the
          // machine itself everything is reachable, so the rule relaxes.
          open: local || s.wildcard,
          scheme: s.probe.scheme || 'http',
          startedAt: s.startedAt,
        }
      })
      .sort((a, b) => a.port - b.port)
  }, [services, local])

  // Keep boats that have just gone so they can leave rather than blink out.
  const [shown, setShown] = useState<Boat[]>(present)
  const timers = useRef(new Map<string, number>())

  useEffect(() => {
    setShown((prev) => {
      const live = new Map(present.map((b) => [b.key, b]))
      const gone = prev.filter((b) => !b.departing && !live.has(b.key))

      for (const boat of gone) {
        if (timers.current.has(boat.key)) continue
        const id = window.setTimeout(() => {
          timers.current.delete(boat.key)
          setShown((cur) => cur.filter((b) => b.key !== boat.key))
        }, DEPART_MS)
        timers.current.set(boat.key, id)
      }

      const departing = gone.map((b) => ({ ...b, departing: true }))
      const kept = prev.filter((b) => b.departing && live.has(b.key) === false && !gone.includes(b))
      return [...present, ...departing, ...kept].sort((a, b) => a.port - b.port)
    })
  }, [present])

  useEffect(() => {
    const running = timers.current
    return () => running.forEach((id) => window.clearTimeout(id))
  }, [])

  const reachable = shown.filter((b) => b.open && !b.departing).length

  return (
    // A column that fills the viewport: sky, then water all the way down. Without
    // this the harbour stopped an inch below the boats and a phone showed a band
    // of scenery floating in a void.
    <div className="flex min-h-screen flex-col bg-[#04121a]">
      <Sky net={net} count={reachable} />

      {/* The water. Grows to take whatever height is left, so the scene reaches the
          bottom of the screen on any device. */}
      <main className="relative flex flex-1 flex-col bg-gradient-to-b from-[#0c3446] via-[#082935] to-[#03141c]">
        <div aria-hidden className="h-px w-full bg-lit-400/25" />

        <div className="mx-auto w-full max-w-3xl px-4">
          {shown.length === 0 ? (
            <p className="pt-14 text-center font-mono text-[0.88rem] leading-relaxed text-foam-300">
              Nothing is out on the water.
              <br />
              <span className="text-foam-400">
                Start a server on that Mac and it will sail in here.
              </span>
            </p>
          ) : (
            <ul className="flex flex-wrap justify-center gap-x-3 gap-y-5 pt-7">
              {shown.map((boat) => (
                <BoatCard key={boat.key} boat={boat} host={host} now={now} />
              ))}
            </ul>
          )}

          {shown.some((b) => !b.open) && (
            <p className="mx-auto mt-7 max-w-md rounded-xl border border-harbor-800 bg-harbor-950/60 px-4 py-3 font-mono text-[0.68rem] leading-relaxed text-foam-400">
              Boats at the pier listen only on that Mac, so this device cannot open
              them. Their dev server needs <span className="text-foam-200">--host</span> to
              answer on the network.
            </p>
          )}
        </div>

        {/* Open water below the fleet, filling the rest of the screen. */}
        <Waterline />
      </main>
    </div>
  )
}

/** The name of the machine you are looking at, and how many boats are out. */
function Sky({ net, count }: { net: NetInfo | undefined; count: number }) {
  const name = net?.alias ?? net?.host ?? window.location.hostname
  return (
    <header className="relative overflow-hidden px-4 pb-6 pt-9">
      <Stars />
      <p className="stencil relative text-center text-lit-400">Marina</p>
      <h1 className="relative mt-1 text-center text-[1.35rem] font-bold tracking-[-0.02em] text-foam-50">
        {name.replace(/\.local$/, '')}
      </h1>
      <p className="relative mt-1.5 text-center font-mono text-[0.74rem] text-foam-400">
        {count === 0 ? 'nothing serving' : `${count} ready to open`}
        {net?.ip && <span className="text-harbor-600"> · </span>}
        {net?.ip}
      </p>
    </header>
  )
}

/**
 * One app, as a boat you can tap.
 *
 * The port is the largest element because it is the thing that identifies an app
 * to someone who runs several: the name tells you which project, the port tells
 * you which of its servers.
 */
function BoatCard({ boat, host, now }: { boat: Boat; host: string; now: number }) {
  const up = boat.startedAt ? uptime(boat.startedAt, now) : ''
  const href = `${boat.scheme}://${host}:${boat.port}`

  const inner = (
    <>
      <Hull moored={!boat.open} />
      <span className="tnum mt-1 font-mono text-[1.15rem] font-bold leading-none text-foam-50">
        {boat.port}
      </span>
      <span className="mt-1 max-w-[7.5rem] truncate text-center text-[0.78rem] font-semibold leading-tight text-foam-100">
        {boat.name}
      </span>
      <span className="mt-0.5 max-w-[7.5rem] truncate text-center font-mono text-[0.6rem] leading-tight text-foam-400">
        {boat.framework || boat.detail}
      </span>
      {up && (
        <span className="mt-0.5 font-mono text-[0.58rem] text-foam-400/70">up {up}</span>
      )}
    </>
  )

  // A 44px-plus target either way, because this is used with a thumb.
  const shell =
    'flex w-[7.9rem] flex-col items-center rounded-2xl px-2 py-3 transition-transform'

  return (
    <li className={boat.departing ? 'animate-sail-out' : 'animate-sail-in'}>
      {boat.open ? (
        <a
          href={href}
          className={`${shell} border border-harbor-700 bg-harbor-950/80 active:scale-[0.97]`}
        >
          {inner}
        </a>
      ) : (
        <span
          title="Listening only on that Mac — its dev server needs --host"
          className={`${shell} cursor-default border border-dashed border-harbor-800 bg-harbor-950/40 opacity-70`}
        >
          {inner}
        </span>
      )}
    </li>
  )
}

/** A boat, drawn the same way the harbour draws them so the two views agree. */
function Hull({ moored }: { moored: boolean }) {
  const tint = moored ? '#5b7b88' : '#3fe0c8'
  return (
    <svg viewBox="0 0 68 58" className="h-[2.9rem] w-[3.4rem]" aria-hidden>
      <path d="M34 6v28" stroke="#cfe4ec" strokeWidth="1.5" strokeLinecap="round" />
      <path d="M32 10 L32 34 L14 34 Z" fill={tint} opacity="0.9" />
      <path d="M36 13 L36 34 L48 34 Z" fill={tint} opacity="0.5" />
      <path
        d="M9 35 H59 L52.5 46 Q34 50 15.5 46 Z"
        fill="#0a2431"
        stroke={tint}
        strokeWidth="1.4"
        strokeLinejoin="round"
      />
    </svg>
  )
}

/**
 * Open water. Still, not animated — see the note at the top of this file.
 *
 * Takes the remaining height so the harbour reaches the bottom of the screen, with
 * the rows thinning as they recede.
 */
function Waterline() {
  const rows = [0, 1, 2, 3, 4, 5]
  return (
    <div aria-hidden className="mt-8 flex flex-1 flex-col justify-start gap-6 pb-10">
      {rows.map((row) => (
        <div
          key={row}
          className="h-[2px] w-full rounded-full"
          style={{
            background:
              'repeating-linear-gradient(90deg, transparent 0 22px, rgba(143,184,200,0.55) 22px 52px, transparent 52px 78px)',
            marginLeft: `${(row % 3) * 11}px`,
            // Fading with distance, so the water reads as depth rather than stripes.
            opacity: 0.34 - row * 0.045,
          }}
        />
      ))}
    </div>
  )
}

function Stars() {
  // Fixed positions: a random field would move on every render, which is both
  // distracting and a re-render nobody asked for.
  const stars = [
    [8, 22], [17, 46], [26, 14], [38, 38], [52, 20], [61, 44], [73, 16], [84, 36], [93, 26],
  ]
  return (
    <div aria-hidden className="pointer-events-none absolute inset-0">
      {stars.map(([left, top], i) => (
        <span
          key={i}
          className="absolute size-[2px] rounded-full bg-foam-100"
          style={{ left: `${left}%`, top: `${top}%`, opacity: i % 3 === 0 ? 0.5 : 0.28 }}
        />
      ))}
    </div>
  )
}
