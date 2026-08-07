import { useEffect, useMemo, useRef, useState } from 'react'
import { AshoreList } from './components/AshoreList'
import { Dock } from './components/Dock'
import { LogsView } from './components/LogsView'
import { Berth } from './components/Berth'
import { ClusterRows } from './components/ClusterRows'
import { HarborView } from './components/HarborView'
import { HealthView } from './components/HealthView'
import { TopBar, type Filter, type Pane, type View } from './components/TopBar'
import {
  clusterPinned,
  clusterServices,
  clusterSize,
  groupClusters,
  score,
} from './lib/format'
import type { Cluster } from './lib/format'
import { useClock, useMarina } from './lib/useMarina'
import { watchAttention } from './lib/calm'
import { useLogList } from './lib/useLogs'
import { useHealth } from './lib/useHealth'
import { useRoute } from './lib/useRoute'

const VIEW_KEY = 'marina:view'

export default function App() {
  const { snapshot, connection, setPinned, setNickname, start, stop } = useMarina()
  const now = useClock()
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('app')
  const [view, setView] = useState<View>(
    () => (localStorage.getItem(VIEW_KEY) as View | null) ?? 'manifest',
  )
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const searchRef = useRef<HTMLInputElement>(null)

  // Whether this page is being read on the machine Marina runs on. A visitor from
  // another device cannot reach loopback-bound apps and cannot change anything, so
  // it is served the phone page instead of controls that would not work.
  const viewedLocally = /^(localhost|127(\.\d+){3}|\[?::1\]?)$/.test(window.location.hostname)

  // Two routes, so the terminals view is linkable and the back button works.
  const { route, navigate } = useRoute()
  const pane: Pane =
    route.name === 'logs' ? 'logs' : route.name === 'health' ? 'health' : 'dashboard'
  // Live load per app, for the meters on the boats and rows. Polled only while the
  // dashboard is on screen; the health view fetches its own copy with history.
  const health = useHealth(pane === 'dashboard', false)
  const { logs } = useLogList(route.name === 'logs' ? 4000 : 15000)

  const setPane = (next: Pane) =>
    navigate(next === 'logs' ? '/logs' : next === 'health' ? '/health' : '/')
  const selectLog = (name: string | null) => navigate(name ? `/logs/${encodeURIComponent(name)}` : '/logs')

  // How many running apps would actually answer on the LAN address. A server on
  // loopback ignores it, so the header can say so instead of implying otherwise.
  const lanReach = useMemo(() => {
    const apps = (snapshot?.services ?? []).filter(
      (s) => s.kind === 'app' || s.kind === 'unknown',
    )
    return { apps: apps.length, reachable: apps.filter((s) => s.wildcard).length }
  }, [snapshot?.services])

  useEffect(() => {
    localStorage.setItem(VIEW_KEY, view)
  }, [view])

  // Hold the scene still whenever the window isn't being looked at.
  useEffect(watchAttention, [])

  const toggleCluster = (key: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      next.has(key) ? next.delete(key) : next.add(key)
      return next
    })

  // ⌘K or / focuses search; typing a port and pressing Enter opens that app.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typingElsewhere =
        e.target instanceof HTMLElement && /^(INPUT|TEXTAREA)$/.test(e.target.tagName)

      if ((e.key === 'k' && (e.metaKey || e.ctrlKey)) || (e.key === '/' && !typingElsewhere)) {
        e.preventDefault()
        searchRef.current?.focus()
        searchRef.current?.select()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const services = snapshot?.services ?? []

  const visible = useMemo(() => {
    // The harbour is a picture of the whole machine, so the kind filter — which
    // exists to tame a long list — doesn't apply there.
    const byKind =
      filter === 'all' || view === 'harbor'
        ? services
        : services.filter((s) => s.kind === filter)
    if (!query.trim()) return byKind

    return byKind
      .map((s) => ({ s, rank: score(s, query) }))
      .filter(({ rank }) => rank >= 0)
      .sort((a, b) => b.rank - a.rank || a.s.port - b.s.port)
      .map(({ s }) => s)
  }, [services, filter, query, view])

  // Searching flattens the list — ranked order is more useful than grouping.
  const searching = query.trim().length > 0

  // Cluster first, then split out pins, so a pinned front door always takes its
  // services with it instead of orphaning them in their old group.
  const clusters = useMemo(() => clusterServices(visible), [visible])
  const pinnedClusters = useMemo(() => clusters.filter(clusterPinned), [clusters])
  const groups = useMemo(
    () => groupClusters(clusters.filter((c) => !clusterPinned(c))),
    [clusters],
  )

  // Enter in the search box opens the single best match.
  useEffect(() => {
    const onEnter = (e: KeyboardEvent) => {
      if (e.key !== 'Enter' || document.activeElement !== searchRef.current) return
      const target = visible.find((s) => s.url)
      if (target?.url) window.open(target.url, '_blank', 'noreferrer')
    }
    window.addEventListener('keydown', onEnter)
    return () => window.removeEventListener('keydown', onEnter)
  }, [visible])

  // The phone page stands alone: no top bar, no filters, nothing to administer.
  if (route.name === 'dock' || (!viewedLocally && route.name === 'dashboard')) {
    return (
      <Dock
        services={snapshot?.services ?? []}
        net={snapshot?.net}
        now={now}
        local={viewedLocally}
      />
    )
  }

  return (
    <div className="min-h-screen">
      <TopBar
        ref={searchRef}
        counts={snapshot?.counts}
        connection={connection}
        net={snapshot?.net}
        netReachable={lanReach.reachable}
        netApps={lanReach.apps}
        store={snapshot?.store}
        scanMs={snapshot?.scanMs}
        query={query}
        onQuery={setQuery}
        filter={filter}
        onFilter={setFilter}
        view={view}
        onView={setView}
        pane={pane}
        onPane={setPane}
        logCount={logs?.length ?? 0}
        matches={visible.length}
      />

      <main className="mx-auto max-w-5xl px-5 pb-24 pt-6">
        {pane === 'logs' && (
          <LogsView selected={route.param} onSelect={selectLog} onStop={stop} />
        )}

        {pane === 'health' && (
          <HealthView now={now} onStop={stop} onOpenLog={selectLog} />
        )}

        {pane === 'dashboard' && !snapshot && connection !== 'lost' && <Loading />}
        {pane === 'dashboard' && connection === 'lost' && <DaemonDown />}

        {pane === 'dashboard' && snapshot && visible.length === 0 && <Empty query={query} filter={filter} />}

        {pane === 'dashboard' && snapshot && view === 'harbor' && visible.length > 0 && (
          <HarborView
            services={visible}
            ashore={snapshot.ashore}
            ashoreSkipped={snapshot.ashoreSkipped}
            now={now}
            onPin={setPinned}
            onStart={start}
            onStop={stop}
            health={health}
          />
        )}

        {pane === 'dashboard' && view === 'manifest' && !searching && pinnedClusters.length > 0 && (
          <Section
            heading="Pinned"
            count={pinnedClusters.reduce((n, c) => n + clusterSize(c), 0)}
          >
            {pinnedClusters.map((cluster) => (
              <ClusterRows
                key={cluster.primary.key}
                cluster={cluster}
                now={now}
                grouped={false}
                // If the pinned thing is a service rather than the front door,
                // open the cluster so you can actually see what you pinned.
                expanded={
                  expanded.has(cluster.primary.key) ||
                  (!cluster.primary.meta.pinned && cluster.services.some((s) => s.meta.pinned))
                }
                onToggle={toggleCluster}
                onPin={setPinned}
                onRename={setNickname}
                onStop={stop}
                cpu={health.byKey.get(cluster.primary.key)?.sample.cpu}
                trend={health.byKey.get(cluster.primary.key)?.trend}
                cores={health.machine.cores}
              />
            ))}
          </Section>
        )}

        {pane === 'dashboard' && view === 'manifest' && searching && (
          <Section heading={`${visible.length} matches`}>
            {visible.map((s) => (
              <Berth
                key={s.key}
                service={s}
                now={now}
                onPin={setPinned}
                onRename={setNickname}
                onStop={s.kind === 'app' ? stop : undefined}
              />
            ))}
          </Section>
        )}

        {pane === 'dashboard' &&
          view === 'manifest' &&
          !searching &&
          groups.map((group) => (
            <Section
              key={group.name}
              heading={group.name}
              count={group.clusters.reduce((n, c) => n + clusterSize(c), 0)}
              subtitle={shapeOf(group.clusters)}
            >
              {group.clusters.map((cluster) => (
                <ClusterRows
                  key={cluster.primary.key}
                  cluster={cluster}
                  now={now}
                  grouped
                  expanded={expanded.has(cluster.primary.key)}
                  onToggle={toggleCluster}
                  onPin={setPinned}
                  onRename={setNickname}
                  // Trouble only. This listing deliberately doesn't carry the CPU
                  // meter — every row would grow a bar and the manifest is meant to
                  // be scanned — but an app eating the machine has to be visible
                  // from whichever tab you happen to have open.
                  trend={health.byKey.get(cluster.primary.key)?.trend}
                />
              ))}
            </Section>
          ))}

        {/* What could be running, but isn't. Only in the manifest: the harbour
            draws these in its boatyard instead.
            Rendered even when empty, because this section holds the control for
            which directories are scanned — hiding it when nothing was found would
            hide the one thing that fixes nothing being found. */}
        {pane === 'dashboard' && snapshot && view === 'manifest' && !searching && (
          <AshoreList
            projects={snapshot.ashore}
            skipped={snapshot.ashoreSkipped}
            onStart={start}
            onOpenLog={selectLog}
          />
        )}

        {pane === 'dashboard' && snapshot && (
          <p className="mt-10 text-center font-mono text-[0.68rem] text-foam-400">
            marina {snapshot.version} · watching since{' '}
            {new Date(snapshot.daemonStartedAt).toLocaleString()}
          </p>
        )}
      </main>
    </div>
  )
}

/** Describes a project group: how many apps, and how many services behind them. */
function shapeOf(clusters: Cluster[]): string {
  const apps = clusters.length
  const services = clusters.reduce((n, c) => n + c.services.length, 0)
  if (services === 0) {
    const items = clusters.map((c) => c.primary)
    const live = items.filter((s) => s.probe.http).length
    if (live === items.length) return 'all answering http'
    return `${live} of ${items.length} answering http`
  }
  return `${apps} ${apps === 1 ? 'app' : 'apps'} · ${services} ${
    services === 1 ? 'service' : 'services'
  }`
}

function Section({
  heading,
  count,
  subtitle,
  children,
}: {
  heading: string
  count?: number
  subtitle?: string
  children: React.ReactNode
}) {
  return (
    <section className="mb-7">
      <div className="mb-2.5 flex items-baseline gap-3">
        <h2 className="stencil shrink-0 text-foam-300">{heading}</h2>
        <span className="h-px flex-1 bg-harbor-800" aria-hidden />
        {subtitle && <span className="font-mono text-[0.66rem] text-foam-400">{subtitle}</span>}
        {count !== undefined && (
          <span className="tnum font-mono text-[0.7rem] text-foam-400">{count}</span>
        )}
      </div>
      <ul className="flex flex-col gap-1.5">{children}</ul>
    </section>
  )
}

function Loading() {
  return (
    <div className="flex flex-col gap-1.5" aria-busy>
      {Array.from({ length: 6 }, (_, i) => (
        <div
          key={i}
          className="h-[62px] animate-pulse rounded-xl border border-harbor-800/60 bg-harbor-900/40"
          style={{ animationDelay: `${i * 70}ms` }}
        />
      ))}
    </div>
  )
}

function DaemonDown() {
  return (
    <div className="rounded-xl border border-coral-400/30 bg-harbor-900 p-6">
      <h2 className="text-base font-semibold text-foam-50">The Marina daemon isn't answering</h2>
      <p className="mt-1.5 text-[0.88rem] text-foam-300">
        The dashboard is open but nothing is watching your ports. Start it again:
      </p>
      <code className="mt-3 block rounded-lg border border-harbor-700 bg-harbor-950 px-3 py-2 font-mono text-[0.8rem] text-lit-300">
        marina status
      </code>
      <p className="mt-3 font-mono text-[0.72rem] text-foam-400">
        If that reports it isn't running: launchctl kickstart -k gui/$(id -u)/tech.bocchino.marina
      </p>
    </div>
  )
}

function Empty({ query, filter }: { query: string; filter: Filter }) {
  if (query) {
    return (
      <div className="rounded-xl border border-harbor-800 bg-harbor-900/60 p-6">
        <h2 className="text-base font-semibold text-foam-50">Nothing matches “{query}”</h2>
        <p className="mt-1.5 text-[0.88rem] text-foam-300">
          Try a port number, a project name, or the script that starts it. Press Escape to clear.
        </p>
      </div>
    )
  }
  return (
    <div className="rounded-xl border border-harbor-800 bg-harbor-900/60 p-6">
      <h2 className="text-base font-semibold text-foam-50">No {filter === 'all' ? '' : filter} services listening</h2>
      <p className="mt-1.5 text-[0.88rem] text-foam-300">
        Start a dev server and it will appear here within a couple of seconds — no refresh needed.
      </p>
    </div>
  )
}
