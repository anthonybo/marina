import { forwardRef } from 'react'
import type { Connection, Counts, StoreHealth } from '../lib/types'

export type Filter = 'all' | 'app' | 'infra' | 'system'
export type View = 'manifest' | 'harbor'
export type Pane = 'dashboard' | 'logs' | 'health'

interface TopBarProps {
  counts: Counts | undefined
  connection: Connection
  store: StoreHealth | undefined
  scanMs: number | undefined
  query: string
  onQuery: (value: string) => void
  filter: Filter
  onFilter: (value: Filter) => void
  view: View
  onView: (value: View) => void
  pane: Pane
  onPane: (value: Pane) => void
  logCount: number
  matches: number
}

export const TopBar = forwardRef<HTMLInputElement, TopBarProps>(function TopBar(
  {
    counts,
    connection,
    store,
    scanMs,
    query,
    onQuery,
    filter,
    onFilter,
    view,
    onView,
    pane,
    onPane,
    logCount,
    matches,
  },
  searchRef,
) {
  // Apps counts front doors, not ports. A project that is one UI plus a dozen
  // workers is one app; the workers are reported separately rather than inflating
  // the headline number.
  const tabs: Array<{ id: Filter; label: string; count?: number; extra?: number; hint?: string }> = [
    {
      id: 'app',
      label: 'Apps',
      count: counts?.primary,
      extra: counts?.services,
      hint: counts
        ? `${counts.primary} apps, plus ${counts.services} supporting services behind them`
        : undefined,
    },
    { id: 'infra', label: 'Infra', count: counts?.infra },
    { id: 'system', label: 'System', count: counts?.system },
    { id: 'all', label: 'Everything', count: counts?.total, hint: 'Every listening port' },
  ]

  return (
    <header className="sticky top-0 z-30 border-b border-harbor-800 bg-harbor-950/85 backdrop-blur-xl">
      <div className="mx-auto flex max-w-5xl flex-wrap items-center gap-x-5 gap-y-3 px-5 py-3.5">
        {/* Wordmark: a mooring post and its waterline. */}
        <div className="flex items-center gap-2.5">
          <svg viewBox="0 0 32 32" className="size-6" aria-hidden>
            <path d="M16 4.5v16" stroke="#3fe0c8" strokeWidth="2.5" strokeLinecap="round" />
            <path
              d="M7 19.5c2.6 4.2 6 6.3 9 6.3s6.4-2.1 9-6.3"
              stroke="#3fe0c8"
              strokeWidth="2.5"
              strokeLinecap="round"
              fill="none"
            />
            <circle cx="16" cy="5" r="2.6" fill="#ffb454" />
          </svg>
          <h1 className="text-[1.05rem] font-bold tracking-[-0.03em] text-foam-50">Marina</h1>
        </div>

        <ConnectionPill connection={connection} counts={counts} />

        <div className="flex-1" />

        {/* Search and the kind filters belong to the dashboard; the terminals
            pane has its own controls. */}
        <div
          className={`relative order-last w-full sm:order-none sm:w-72 ${
            pane !== 'dashboard' ? 'pointer-events-none opacity-40' : ''
          }`}
        >
          <input
            ref={searchRef}
            value={query}
            onChange={(e) => onQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && onQuery('')}
            placeholder="Search port or project"
            aria-label="Search services"
            className="w-full rounded-lg border border-harbor-700 bg-harbor-900 py-1.5 pl-3 pr-16 font-mono text-[0.8rem] text-foam-50 placeholder:text-foam-400/80 focus:border-lit-400/60 focus:outline-none"
          />
          <span className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 font-mono text-[0.65rem] text-foam-400">
            {query ? `${matches}` : '⌘K'}
          </span>
        </div>

        <nav
          className={`flex items-center gap-1 ${pane !== 'dashboard' ? 'hidden lg:flex lg:opacity-40' : ''}`}
          aria-label="Filter by type"
        >
          {tabs.map((tab) => (
            <button
              key={tab.id}
              type="button"
              onClick={() => onFilter(tab.id)}
              aria-pressed={filter === tab.id}
              disabled={view === 'harbor' || pane !== 'dashboard'}
              title={view === 'harbor' ? 'The harbour shows everything at once' : tab.hint}
              className={[
                'rounded-lg px-2.5 py-1.5 text-[0.78rem] font-medium transition-colors',
                filter === tab.id && view === 'manifest'
                  ? 'bg-harbor-700 text-foam-50'
                  : 'text-foam-300 hover:bg-harbor-800 hover:text-foam-50',
                view === 'harbor' ? 'cursor-not-allowed opacity-40 hover:bg-transparent' : '',
              ].join(' ')}
            >
              {tab.label}
              {tab.count !== undefined && (
                <span className="tnum ml-1.5 font-mono text-[0.7rem] text-foam-400">{tab.count}</span>
              )}
              {tab.extra !== undefined && tab.extra > 0 && (
                <span className="tnum ml-1 font-mono text-[0.62rem] text-foam-400/70">
                  +{tab.extra}
                </span>
              )}
            </button>
          ))}
        </nav>

        <ViewSwitch
          view={view}
          onView={onView}
          pane={pane}
          onPane={onPane}
          logCount={logCount}
        />
      </div>

      {/* A single honest status line, including whether history is being saved. */}
      <div className="mx-auto flex max-w-5xl flex-wrap items-center gap-x-4 gap-y-1 px-5 pb-2.5 font-mono text-[0.68rem] text-foam-400">
        {scanMs !== undefined && <span>swept in {scanMs}ms</span>}
        <span className="text-harbor-600">·</span>
        <span className={store?.connected ? 'text-foam-300' : 'text-lantern-300'}>
          {store?.connected
            ? 'postgres connected — pins and history saved'
            : `postgres offline — pins held in memory${store?.pending ? ` (${store.pending} queued)` : ''}`}
        </span>
      </div>
    </header>
  )
})

/** Picks what you're looking at: two readings of the live state, plus the
 *  captured terminals. Labelled rather than icon-only — there are only three. */
function ViewSwitch({
  view,
  onView,
  pane,
  onPane,
  logCount,
}: {
  view: View
  onView: (v: View) => void
  pane: Pane
  onPane: (p: Pane) => void
  logCount: number
}) {
  const options = [
    {
      id: 'manifest' as const,
      label: 'Manifest',
      hint: 'Dense list, grouped by project',
      active: pane === 'dashboard' && view === 'manifest',
      onClick: () => {
        onPane('dashboard')
        onView('manifest')
      },
    },
    {
      id: 'harbor' as const,
      label: 'Harbour',
      hint: 'The same servers as a working harbour',
      active: pane === 'dashboard' && view === 'harbor',
      onClick: () => {
        onPane('dashboard')
        onView('harbor')
      },
    },
    {
      id: 'logs' as const,
      label: 'Terminals',
      hint: 'Output from apps Marina started',
      active: pane === 'logs',
      onClick: () => onPane('logs'),
    },
    {
      id: 'health' as const,
      label: 'Health',
      hint: 'What each running app is costing the machine',
      active: pane === 'health',
      onClick: () => onPane('health'),
    },
  ]

  return (
    <div
      className="flex items-center gap-0.5 rounded-lg border border-harbor-700 bg-harbor-900 p-0.5"
      role="group"
      aria-label="View"
    >
      {options.map((option) => (
        <button
          key={option.id}
          type="button"
          onClick={option.onClick}
          aria-pressed={option.active}
          title={option.hint}
          className={[
            'rounded-md px-2.5 py-1 text-[0.74rem] font-medium transition-colors',
            option.active ? 'bg-lit-600/25 text-lit-300' : 'text-foam-400 hover:text-foam-50',
          ].join(' ')}
        >
          {option.label}
          {option.id === 'logs' && logCount > 0 && (
            <span className="tnum ml-1.5 font-mono text-[0.66rem] text-foam-400">{logCount}</span>
          )}
        </button>
      ))}
    </div>
  )
}

function ConnectionPill({
  connection,
  counts,
}: {
  connection: Connection
  counts: Counts | undefined
}) {
  const config = {
    live: { dot: 'bg-lit-400', text: 'text-foam-100', label: 'docked' },
    connecting: { dot: 'bg-lantern-400', text: 'text-lantern-300', label: 'connecting' },
    lost: { dot: 'bg-coral-400', text: 'text-coral-300', label: 'daemon unreachable' },
  }[connection]

  return (
    <div className="flex items-center gap-2 rounded-full border border-harbor-700 bg-harbor-900 py-1 pl-2.5 pr-3">
      {/* Steady when connected, pulsing when not. The reverse used to be true, and
          that one dot cost ~15% of a CPU core on every route: a running animation
          stops the browser going idle, so it produced frames forever to say
          "fine". Motion now marks the state you can act on, which is also the
          only state worth spending frames on. */}
      <span
        className={`size-2 rounded-full ${config.dot} ${connection === 'live' ? '' : 'animate-buoy'}`}
        aria-hidden
      />
      <span className={`tnum font-mono text-[0.75rem] ${config.text}`}>
        {connection === 'live' ? `${counts?.total ?? 0} ${config.label}` : config.label}
      </span>
    </div>
  )
}
