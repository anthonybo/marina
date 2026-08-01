import { useState } from 'react'
import type { NetInfo } from '../lib/types'

/**
 * The address another device on this network uses to reach this Mac.
 *
 * Kept in the header, at a size you can read across a desk, because the job it
 * does is being glanced at: you started a server here and want it on a phone, and
 * the router changes the address whenever it feels like it. Reading it off the
 * screen has to be faster than opening a terminal, or nobody will use it.
 *
 * The .local name is offered beside it deliberately. It is the better answer to
 * the same question — it survives a new lease, so a bookmark made with it keeps
 * working — but it is longer to type, so the IP stays the headline and the name is
 * the thing you learn about by being here.
 */
export function LanAddress({
  net,
  reachable,
  apps,
}: {
  net: NetInfo | undefined
  /** Apps bound to all interfaces, so they will answer on this address. */
  reachable: number
  /** Apps in total, to say how many will not. */
  apps: number
}) {
  const [copied, setCopied] = useState<'ip' | 'host' | null>(null)

  const copy = async (text: string, which: 'ip' | 'host') => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(which)
      setTimeout(() => setCopied(null), 1400)
    } catch {
      // Clipboard access can be refused; the address is on screen either way.
    }
  }

  // No address is a real state — an unplugged cable, a dropped Wi-Fi connection —
  // and it has to look like one. Showing the last address we saw would send
  // someone to a machine that is no longer there.
  if (!net?.ip) {
    return (
      <span
        title="No network interface has an address, so nothing on the network can reach this Mac right now."
        className="flex items-center gap-2 rounded-full border border-harbor-800 bg-harbor-900 py-1 pl-2.5 pr-3 font-mono text-[0.75rem] text-foam-400"
      >
        <Signal off />
        no network
      </span>
    )
  }

  // Prefer the short published name when there is one: it is the same on every
  // machine and short enough to type on a phone. It is only ever set once it
  // actually resolves, so showing it cannot send anyone to a dead name.
  const name = net.alias ?? net.host
  const others = net.others ?? []
  // An address alone is half an answer. A server bound to loopback will never
  // answer here however correct the address is — Vite binds IPv6 loopback by
  // default — so say how many of them actually will.
  const shut = Math.max(0, apps - reachable)
  const detail = [
    `Other devices on this network reach this Mac at ${net.ip}${net.iface ? ` (${net.iface})` : ''}.`,
    `Append the port: http://${net.ip}:3000`,
    name && `Or use ${name}, which keeps working when the address changes.`,
    net.alias && net.host && `This Mac's own name, ${net.host}, works too.`,
    others.length > 0 &&
      `Also reachable at ${others.map((o) => `${o.ip} (${o.iface})`).join(', ')}.`,
    apps > 0 &&
      `${reachable} of ${apps} running apps listen on all interfaces and will answer here.` +
        (shut > 0
          ? ` The other ${shut} are bound to localhost only — start those with --host (Vite, Astro) or 0.0.0.0 to reach them.`
          : ''),
    'Click to copy.',
  ]
    .filter(Boolean)
    .join('\n')

  return (
    <span className="flex items-center gap-2 rounded-full border border-harbor-700 bg-harbor-900 py-1 pl-2.5 pr-1.5">
      <Signal />
      <button
        type="button"
        onClick={() => copy(net.ip!, 'ip')}
        title={detail}
        aria-label={`Copy this Mac's network address, ${net.ip}`}
        className="tnum font-mono text-[0.9rem] font-semibold leading-none text-foam-50 transition-colors hover:text-lit-400"
      >
        {copied === 'ip' ? 'copied' : net.ip}
      </button>

      {/* The durable alternative, quiet enough not to compete with the address. */}
      {name && (
        <button
          type="button"
          onClick={() => copy(name, 'host')}
          title={`Copy ${name} — this name keeps working after the router hands out a new address.`}
          aria-label={`Copy this Mac's network name, ${name}`}
          className="max-w-[10rem] truncate rounded-full px-1.5 py-0.5 font-mono text-[0.62rem] text-foam-400 transition-colors hover:bg-harbor-800 hover:text-foam-100"
        >
          {copied === 'host' ? 'copied' : name.replace(/\.local$/, '')}
        </button>
      )}
    </span>
  )
}

/** Concentric arcs: the same shorthand a Mac uses for "on a network". */
function Signal({ off = false }: { off?: boolean }) {
  const tint = off ? '#5b7b88' : '#3fe0c8'
  return (
    <svg viewBox="0 0 16 16" className="size-3 shrink-0" aria-hidden fill="none">
      <circle cx="8" cy="12.5" r="1.4" fill={tint} />
      <path d="M4.6 9.4a4.8 4.8 0 0 1 6.8 0" stroke={tint} strokeWidth="1.5" strokeLinecap="round" />
      <path
        d="M2.2 6.6a8.2 8.2 0 0 1 11.6 0"
        stroke={tint}
        strokeWidth="1.5"
        strokeLinecap="round"
        opacity={off ? 0.3 : 0.75}
      />
    </svg>
  )
}
