import { memo, useEffect, useState } from 'react'
import { portEvidence } from '../lib/format'
import type { Ashore } from '../lib/types'
import { RootsEditor } from './RootsEditor'

/**
 * Projects Marina found on disk that aren't running.
 *
 * Rendered deliberately quieter than the live list, and with no port number,
 * because there is no port yet — that absence is the whole distinction.
 *
 * Laid out as aligned columns: this list grows with the projects folder, and past
 * roughly a dozen entries the limiting factor is not how small each row is but
 * whether they line up. See AshoreChip for what a row does and does not carry.
 */
interface AshoreListProps {
  projects: Ashore[]
  skipped: number
  onStart: (path: string) => void
  /** Opens the terminal for a project, so a failure links to its own output. */
  onOpenLog: (name: string) => void
}

export const AshoreList = memo(function AshoreList({
  projects,
  skipped,
  onStart,
  onOpenLog,
}: AshoreListProps) {
  const [query, setQuery] = useState('')

  const q = query.trim().toLowerCase()
  const shown = q
    ? projects.filter(
        (p) =>
          p.name.toLowerCase().includes(q) ||
          (p.framework ?? '').toLowerCase().includes(q) ||
          p.manager.toLowerCase().includes(q),
      )
    : projects

  return (
    <section className="mb-7">
      <div className="mb-2.5 flex items-baseline gap-3">
        <h2 className="stencil shrink-0 text-foam-300">Ashore · not running</h2>
        <span className="h-px flex-1 bg-harbor-800" aria-hidden />
        {projects.length > 6 && (
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && setQuery('')}
            placeholder="filter"
            aria-label="Filter projects that aren't running"
            className="w-28 rounded border border-harbor-800 bg-harbor-950 px-2 py-0.5 font-mono text-[0.66rem] text-foam-100 placeholder:text-foam-400/70 focus:border-lit-400/50 focus:outline-none"
          />
        )}
        <span className="tnum font-mono text-[0.7rem] text-foam-400">{shown.length}</span>
      </div>

      {/* Aligned columns rather than wrapped chips: variable-width chips leave every
          row ragged, and twenty-plus unaligned items read as clutter however small
          each one is. */}
      <ul
        className="grid gap-x-7 gap-y-px"
        style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(14rem, 1fr))' }}
      >
        {shown.map((project) => (
          <AshoreChip
            key={project.path}
            project={project}
            onStart={onStart}
            onOpenLog={onOpenLog}
          />
        ))}
      </ul>

      {shown.length === 0 && (
        <p className="rounded-lg border border-dashed border-harbor-800 px-4 py-3 font-mono text-[0.7rem] text-foam-400">
          {q
            ? `Nothing matches “${query}”.`
            : 'No projects found. Check which directories are being scanned below.'}
        </p>
      )}

      {/* Say what was left out rather than implying the folder holds nothing else. */}
      {skipped > 0 && (
        <p className="mt-2 font-mono text-[0.66rem] text-foam-400">
          {skipped} more {skipped === 1 ? 'directory' : 'directories'} found with no start command
          Marina recognises.
        </p>
      )}

      {/* Where this list comes from, and how to point it somewhere else. */}
      <RootsEditor />
    </section>
  )
})

/**
 * One hauled-out project, as a row in an aligned column.
 *
 * Started as a full-width row with the hull, name, framework badge, whole command
 * and a start button — about 2.7rem each, so twenty-two of them took thirty rem of
 * dashboard for things that are not running. Shrinking them to chips fixed the
 * height but not the noise: variable widths meant no two rows aligned. Columns fix
 * that, and dropping the per-item hull removes twenty-two identical marks that
 * never distinguished one project from another.
 *
 * The command is not printed inline any more, but the rule that you can read what
 * will run *before* running it still holds: the row is the button, and the command
 * is its tooltip, so nothing is clickable without hovering first.
 */
const AshoreChip = memo(function AshoreChip({
  project,
  onStart,
  onOpenLog,
}: {
  project: Ashore
  onStart: (path: string) => void
  onOpenLog: (name: string) => void
}) {
  const { starting, failed } = project
  const conflicts = project.conflicts ?? []
  // The strongest piece of evidence is enough for a row this dense; the rest is
  // in the tooltip.
  const port = (project.expect ?? [])[0]
  const clash = port ? conflicts.find((c) => c.port === port.port) : undefined

  // A conflict does not block starting — a dev server may fall back to the next
  // free port — but it should cost a second, deliberate click.
  const [armed, setArmed] = useState(false)
  useEffect(() => {
    if (!armed) return
    const id = setTimeout(() => setArmed(false), 4000)
    return () => clearTimeout(id)
  }, [armed])

  const click = () => {
    if (conflicts.length > 0 && !armed) {
      setArmed(true)
      return
    }
    setArmed(false)
    onStart(project.path)
  }

  const title = starting
    ? `Starting: ${project.command}`
    : armed
      ? `:${conflicts[0].port} is already in use by ${conflicts[0].heldBy} — click again to start anyway`
      : [
          `Run: ${project.command}`,
          port && `Expects :${port.port} — ${portEvidence[port.source]}`,
          clash && `Already in use by ${clash.heldBy}`,
          failed && project.error && `Last attempt failed: ${project.error}`,
        ]
          .filter(Boolean)
          .join('\n')

  const nameColor = failed ? '#ff9aa6' : starting ? '#ffc978' : clash ? '#ffc978' : '#dcecf1'

  return (
    <li>
      <button
        type="button"
        onClick={click}
        disabled={starting}
        title={title}
        className={[
          'group flex w-full items-baseline gap-2 rounded px-1.5 py-[0.22rem] text-left',
          'transition-colors duration-100',
          starting
            ? 'cursor-default bg-lantern-400/10'
            : armed
              ? 'cursor-pointer bg-coral-400/20'
              : failed
                ? 'cursor-pointer bg-coral-400/8 hover:bg-coral-400/16'
                : 'cursor-pointer hover:bg-harbor-900/70',
        ].join(' ')}
      >
        <span
          className="truncate font-mono text-[0.76rem] leading-snug"
          style={{ color: nameColor }}
        >
          {project.name}
        </span>

        {/* The port it will try to take. Shown quietly when it's only a framework
            guess, and in warning colour when something already holds it. */}
        {port && !armed && (
          <span
            className={[
              'tnum shrink-0 font-mono text-[0.58rem] leading-snug',
              clash
                ? 'text-coral-300'
                : port.source === 'default'
                  ? 'text-foam-400/45'
                  : 'text-foam-400/75',
            ].join(' ')}
          >
            :{port.port}
            {clash && ' ⚠'}
          </span>
        )}

        <span
          className={[
            'ml-auto shrink-0 font-mono text-[0.58rem] leading-snug',
            starting
              ? 'text-lantern-300'
              : armed
                ? 'text-coral-300'
                : failed
                  ? 'text-coral-300'
                  : 'text-foam-400/60',
          ].join(' ')}
        >
          {starting
            ? 'starting…'
            : armed
              ? 'start anyway?'
              : failed
                ? 'failed'
                : project.framework || project.manager}
        </span>
      </button>

      {/* A failure names itself and links to its own output. */}
      {failed && project.error && (
        <p className="flex items-start gap-1.5 px-1.5 pb-1 text-[0.62rem] leading-snug text-coral-300">
          <span className="min-w-0 flex-1 truncate" title={project.error}>
            {project.error}
          </span>
          <button
            type="button"
            onClick={() => onOpenLog(project.name)}
            className="shrink-0 underline decoration-dotted hover:text-coral-200"
          >
            log
          </button>
        </p>
      )}
    </li>
  )
})
