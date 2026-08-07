import { useEffect, useMemo, useState } from 'react'
import { Terminal } from './Terminal'
import { stripAnsi } from '../lib/ansi'
import { dismissLog, useLogList, useLogTail, type LogEntry, type Unreachable } from '../lib/useLogs'

/**
 * Every terminal Marina captured, in one place.
 *
 * The honest boundary of this view: Marina owns the output of processes it
 * launched, because that is when it holds the pipe. A server you started in your
 * own terminal writes to that terminal, and nothing can retroactively attach to
 * it — so instead of showing an empty pane that implies something is broken, this
 * says which running apps have no captured output and why.
 */
interface LogsViewProps {
  /** null shows the grid; a name shows that one terminal. */
  selected: string | null
  onSelect: (name: string | null) => void
  /** Stops an app. Looking at its output is the natural place to shut it down. */
  onStop: (target: { port?: number; path?: string }) => Promise<string | null> | void
}

export function LogsView({ selected, onSelect, onStop }: LogsViewProps) {
  const { logs, unreachable, dir } = useLogList()

  const blocked = useMemo(
    // Marina's own daemon log is listed as a terminal, so it is never "missing".
    () => unreachable.filter((u) => u.project.toLowerCase() !== 'marina'),
    [unreachable],
  )

  // The terminals Marina may clear away, by exactly the rule the per-card dismiss
  // uses: finished, and Marina's own launch log. A file belonging to someone else's
  // process is not ours to delete however many of them there are.
  const finished = useMemo(
    () => (logs ?? []).filter((log) => !log.running && log.source === 'launch'),
    [logs],
  )

  if (selected) {
    return (
      <SingleTerminal
        name={selected}
        logs={logs}
        onBack={() => onSelect(null)}
        onStop={onStop}
      />
    )
  }

  return (
    <>
      {logs === null && (
        <p className="font-mono text-[0.75rem] text-foam-400">Loading terminals…</p>
      )}

      {logs !== null && logs.length === 0 && <NothingCaptured unreachable={blocked} />}

      {logs !== null && logs.length > 0 && (
        <>
          <div className="mb-3 flex flex-wrap items-baseline gap-3">
            <h2 className="stencil shrink-0 text-foam-300">Captured terminals</h2>
            <span className="h-px flex-1 bg-harbor-800" aria-hidden />
            <span className="tnum font-mono text-[0.7rem] text-foam-400">{logs.length}</span>
            {/* Worth its own control past one: a list that is mostly finished
                sessions takes as many clicks to clear as it has cards. */}
            {finished.length > 1 && <DismissFinished logs={finished} />}
          </div>

          <ul className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            {logs.map((log) => (
              <TerminalCard
                key={log.name}
                log={log}
                onOpen={() => onSelect(log.name)}
                onStop={onStop}
              />
            ))}
          </ul>

          {blocked.length > 0 && <Unreachable items={blocked} />}

          {dir && (
            <p className="mt-4 font-mono text-[0.64rem] text-foam-400">
              Logs are files on disk: <span className="text-foam-300">{dir}</span>
            </p>
          )}
        </>
      )}
    </>
  )
}

/** A small live preview of one terminal, click to open it fully. */
function TerminalCard({
  log,
  onOpen,
  onStop,
}: {
  log: LogEntry
  onOpen: () => void
  onStop: (target: { port?: number; path?: string }) => Promise<string | null> | void
}) {
  const { text } = useLogTail({ name: log.name, port: log.port }, 3000, 40_000)
  const [dismissError, setDismissError] = useState<string | null>(null)

  return (
    <li className="overflow-hidden rounded-xl border border-harbor-800 bg-harbor-900/50">
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full items-center gap-2.5 px-3.5 py-2.5 text-left transition-colors hover:bg-harbor-850"
      >
        {/* Lit, not pulsing: "running" is a steady state, and one perpetual
            animation per row would keep the browser rendering frames for as long
            as this list is open. The word beside it already says which it is. */}
        <span
          aria-hidden
          className={[
            'size-2 shrink-0 rounded-full',
            log.running ? 'bg-lit-400' : 'bg-harbor-600',
          ].join(' ')}
        />
        <span className="truncate text-[0.9rem] font-semibold text-foam-50">{log.name}</span>
        <span className="stencil shrink-0 rounded border border-harbor-700 px-1.5 py-0.5 text-foam-400">
          {log.running ? 'running' : 'stopped'}
        </span>
        <span
          title={
            log.source === 'launch'
              ? 'Marina started this, so it owns the output'
              : 'Started elsewhere, but its output goes to a file Marina can read'
          }
          className="stencil shrink-0 rounded border border-harbor-800 px-1.5 py-0.5 text-foam-400"
        >
          {log.source === 'launch' ? 'launched' : 'file'}
        </span>
        <span className="ml-auto shrink-0 font-mono text-[0.66rem] text-foam-400">
          {formatSize(log.size)}
        </span>
        <span aria-hidden className="shrink-0 font-mono text-[0.7rem] text-foam-400">
          ↗
        </span>
      </button>

      <Terminal
        text={text}
        compact
        className="h-44 rounded-none border-0 border-t border-harbor-800"
        empty={
          <span className="font-mono text-[0.66rem] text-foam-400">
            Waiting for output…
          </span>
        }
      />

      <div className="flex items-center gap-2 border-t border-harbor-800 px-3 py-1.5">
        {log.running ? (
          <StopButton name={log.name} onStop={() => onStop({ port: log.port })} />
        ) : (
          <span className="font-mono text-[0.64rem] text-foam-400">not running</span>
        )}

        {/* A finished terminal can be cleared away. Only Marina's own launch logs:
            a file belonging to someone else's process is not ours to delete. */}
        {!log.running && log.source === 'launch' && (
          <button
            type="button"
            title={`Remove ${log.name}'s terminal from this list (deletes ${log.path})`}
            onClick={async () => {
              const error = await dismissLog(log.name)
              if (error) setDismissError(error)
            }}
            className="ml-auto rounded-md px-2 py-0.5 font-mono text-[0.64rem] text-foam-400 transition-colors hover:bg-harbor-700 hover:text-foam-100"
          >
            dismiss
          </button>
        )}
        {dismissError && <span className="text-[0.64rem] text-coral-300">{dismissError}</span>}
      </div>
    </li>
  )
}

/**
 * Stop, armed on the first click.
 *
 * A dev server is cheap to restart, so a modal would be heavy-handed — but an
 * unarmed button sitting beside "copy" is easy to hit by accident, and this is the
 * one control in Marina that ends a process.
 */
function StopButton({
  name,
  onStop,
}: {
  name: string
  onStop: () => Promise<string | null> | void
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
        title={armed ? 'Click again to stop' : `Stop ${name}`}
        onClick={async () => {
          if (!armed) {
            setArmed(true)
            return
          }
          setArmed(false)
          setError((await onStop()) ?? null)
        }}
        className={[
          'rounded-md px-2 py-0.5 font-mono text-[0.66rem] transition-colors',
          armed
            ? 'bg-coral-400/25 text-coral-300'
            : 'text-foam-400 hover:bg-coral-400/15 hover:text-coral-300',
        ].join(' ')}
      >
        {armed ? `stop ${name}?` : '⏻ stop'}
      </button>
      {error && <span className="ml-2 text-[0.68rem] text-coral-300">{error}</span>}
    </>
  )
}

/**
 * Clears every finished terminal at once.
 *
 * Armed on the first click and explicit about the count on the second, because
 * this deletes files. Marina has form here: an unlabelled × in the boatyard once
 * read as "close this panel" and removed the only scanned directory instead. A
 * control that destroys something says so in words and says how much.
 */
function DismissFinished({ logs }: { logs: LogEntry[] }) {
  const [armed, setArmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!armed) return
    const id = setTimeout(() => setArmed(false), 4000)
    return () => clearTimeout(id)
  }, [armed])

  const count = logs.length

  return (
    <>
      <button
        type="button"
        disabled={busy}
        title={
          armed
            ? `Click again to delete ${count} log files`
            : `Remove the ${count} finished terminals from this list, deleting their log files`
        }
        onClick={async () => {
          if (!armed) {
            setArmed(true)
            return
          }
          setArmed(false)
          setBusy(true)
          // Sequential, not parallel: the daemon deletes files, and a failure
          // partway through should leave a list you can read rather than a race.
          // Report the first thing that went wrong and stop guessing after that.
          let failed: string | null = null
          for (const log of logs) {
            const err = await dismissLog(log.name)
            if (err) {
              failed = `${log.name}: ${err}`
              break
            }
          }
          setError(failed)
          setBusy(false)
        }}
        className={[
          'rounded-md px-2 py-0.5 font-mono text-[0.66rem] transition-colors',
          busy
            ? 'cursor-default text-foam-400'
            : armed
              ? 'bg-coral-400/25 text-coral-300'
              : 'text-foam-400 hover:bg-coral-400/15 hover:text-coral-300',
        ].join(' ')}
      >
        {busy ? 'clearing…' : armed ? `delete ${count} log files?` : `dismiss ${count} finished`}
      </button>
      {error && <span className="font-mono text-[0.66rem] text-coral-300">{error}</span>}
    </>
  )
}

/** One terminal, full height, following its tail. */
function SingleTerminal({
  name,
  logs,
  onBack,
  onStop,
}: {
  name: string
  logs: LogEntry[] | null
  onBack: () => void
  onStop: (target: { port?: number; path?: string }) => Promise<string | null> | void
}) {
  const log = logs?.find((l) => l.name === name)
  const { text, missing, refresh } = useLogTail(
    { name, port: log?.port },
    1200,
    600_000,
  )

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(stripAnsi(text))
    } catch {
      // Clipboard access can be refused; the text is on screen either way.
    }
  }

  return (
    <>
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <button
          type="button"
          onClick={onBack}
          className="rounded-lg border border-harbor-700 px-2.5 py-1 font-mono text-[0.72rem] text-foam-300 transition-colors hover:bg-harbor-800 hover:text-foam-50"
        >
          ← all terminals
        </button>

        <h2 className="text-[1rem] font-semibold text-foam-50">{name}</h2>

        {log && (
          <span
            className={[
              'stencil rounded border px-1.5 py-0.5',
              log.running
                ? 'border-lit-400/40 bg-lit-600/15 text-lit-300'
                : 'border-harbor-700 text-foam-400',
            ].join(' ')}
          >
            {log.running ? 'running' : 'stopped'}
          </span>
        )}

        <span className="flex-1" />

        <button
          type="button"
          onClick={refresh}
          className="rounded-lg border border-harbor-700 px-2.5 py-1 font-mono text-[0.72rem] text-foam-300 transition-colors hover:bg-harbor-800 hover:text-foam-50"
        >
          reload
        </button>
        <button
          type="button"
          onClick={copy}
          className="rounded-lg border border-harbor-700 px-2.5 py-1 font-mono text-[0.72rem] text-foam-300 transition-colors hover:bg-harbor-800 hover:text-foam-50"
        >
          copy
        </button>
        {log?.running && (
          <div className="rounded-lg border border-harbor-700 px-1 py-0.5">
            <StopButton name={name} onStop={() => onStop({ port: log.port })} />
          </div>
        )}
      </div>

      {missing ? (
        <div className="rounded-xl border border-coral-400/30 bg-harbor-900 p-6">
          <h3 className="text-base font-semibold text-foam-50">No captured log for “{name}”</h3>
          <p className="mt-1.5 text-[0.88rem] text-foam-300">
            Marina only records output for apps it started. Launch this one from the dashboard and
            its terminal will appear here.
          </p>
        </div>
      ) : (
        <Terminal text={text} className="h-[calc(100vh-15rem)] min-h-80" />
      )}

      {log && (
        <p className="mt-3 font-mono text-[0.64rem] text-foam-400">
          {log.path} · {formatSize(log.size)} · following the tail
        </p>
      )}
    </>
  )
}

function NothingCaptured({ unreachable }: { unreachable: Unreachable[] }) {
  return (
    <div className="rounded-xl border border-harbor-800 bg-harbor-900/60 p-6">
      <h2 className="text-base font-semibold text-foam-50">No terminals to show yet</h2>
      <p className="mt-1.5 max-w-2xl text-[0.88rem] text-foam-300">
        Marina can show a terminal in two cases: an app it started itself, or an app whose output
        goes to a file. Launch something from the <span className="text-foam-100">Ashore</span> list
        and its terminal appears here.
      </p>
      {unreachable.length > 0 && <ReasonList items={unreachable} className="mt-4" />}
    </div>
  )
}

function Unreachable({ items }: { items: Unreachable[] }) {
  return (
    <div className="mt-5 rounded-xl border border-dashed border-harbor-800 px-4 py-3">
      <p className="stencil mb-2 text-foam-400">Running, output not reachable</p>
      <ReasonList items={items} />
    </div>
  )
}

/**
 * Names each app whose output can't be shown, and why — a pipe held by a terminal
 * is point-to-point, so there is genuinely nothing for a third process to read.
 * Saying that is more useful than an empty pane.
 */
function ReasonList({ items, className = '' }: { items: Unreachable[]; className?: string }) {
  const reason = (item: Unreachable) => {
    switch (item.kind) {
      case 'pipe':
        return 'writes to a pipe held by the terminal that started it — nothing else can read it'
      case 'tty':
        return `writes straight to ${item.path ?? 'a terminal'}`
      case 'discarded':
        return 'output is discarded (/dev/null)'
      case 'file':
        // A process keeps its descriptor open after the file is unlinked, so the
        // path is still reported but there is nothing left to read.
        return `wrote to ${item.path ?? 'a file'}, which has since been deleted — restart it to get a fresh log`
      default:
        return 'output destination could not be determined'
    }
  }

  return (
    <div className={className}>
      <ul className="flex flex-col gap-1.5">
        {items.map((item) => (
          <li key={`${item.project}:${item.port}`} className="text-[0.8rem] text-foam-300">
            <span className="font-mono text-[0.74rem] text-foam-400">:{item.port}</span>{' '}
            <span className="font-semibold text-foam-100">{item.project}</span>{' '}
            <span className="text-foam-400">{reason(item)}</span>
          </li>
        ))}
      </ul>
      <p className="mt-2.5 max-w-2xl text-[0.78rem] text-foam-400">
        To see one of these here, either start it from Marina, or redirect its output to a file when
        you start it — <span className="font-mono text-foam-300">npm run dev &gt; dev.log 2&gt;&amp;1</span>.
        Piping to <span className="font-mono">tee</span> won't work: that's still a pipe.
      </p>
    </div>
  )
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
