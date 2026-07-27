import { useEffect, useRef, useState } from 'react'
import { useRoots } from '../lib/useRoots'

/**
 * Which directories Marina looks in for projects you could start.
 *
 * Collapsed to a single line until asked for, because it is settings rather than
 * status: you touch it when a machine is new and then never again. Opening it
 * loads the list; leaving it shut costs nothing.
 *
 * It states the two things that otherwise fail silently. A root is scanned one
 * level deep, so projects grouped into subfolders are invisible and the honest
 * cue is a count of zero. And a directory that has been moved or renamed is
 * skipped without complaint, so it is called out rather than left looking fine.
 */
export function RootsEditor() {
  const [open, setOpen] = useState(false)
  const { roots, file, loading, error, add, remove, clearError } = useRoots(open)
  const [draft, setDraft] = useState('')
  /** Path awaiting a confirmed removal, if any. */
  const [confirming, setConfirming] = useState<string | null>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (open) inputRef.current?.focus()
    else setConfirming(null)
  }, [open])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const path = draft.trim()
    if (!path) return
    if (await add(path)) setDraft('')
  }

  const total = roots.reduce((n, r) => n + r.projects, 0)

  return (
    <div className="mt-2">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="font-mono text-[0.66rem] text-foam-400 underline decoration-dotted underline-offset-2 transition-colors hover:text-foam-100"
      >
        {open ? 'hide' : 'scanned'} directories
        {!open && roots.length > 0 && ` (${roots.length})`}
      </button>

      {open && (
        <div className="mt-2 rounded-lg border border-harbor-800 bg-harbor-950 p-3">
          {/* A titled panel with its own way out. Without these, the only
              close-looking thing was the per-row button, which is destructive. */}
          <div className="mb-2 flex items-baseline gap-3">
            <h3 className="stencil shrink-0 text-foam-300">
              Directories scanned for projects
            </h3>
            <span className="h-px flex-1 bg-harbor-800" aria-hidden />
            <button
              type="button"
              onClick={() => setOpen(false)}
              className="shrink-0 rounded border border-harbor-700 px-2 py-0.5 font-mono text-[0.62rem] text-foam-100 transition-colors hover:border-lit-400/50 hover:text-lit-400"
            >
              done
            </button>
          </div>

          <ul className="mb-2.5 flex flex-col gap-1">
            {roots.map((root) => (
              <li key={root.path} className="flex items-center gap-2.5 font-mono text-[0.72rem]">
                {/* Truncated rather than allowed to crowd the row: most paths are
                    short, but a deep one would otherwise squeeze out the count. */}
                <span
                  title={root.path}
                  className={`max-w-[60%] truncate ${root.exists ? 'text-foam-100' : 'text-coral-300'}`}
                >
                  {root.display}
                </span>

                {/* The count is the visible consequence of scanning one level deep. */}
                {root.exists && root.readable && (
                  <span className="tnum shrink-0 text-[0.66rem] text-foam-400">
                    {root.projects} {root.projects === 1 ? 'project' : 'projects'}
                  </span>
                )}
                {!root.exists && (
                  <span className="shrink-0 text-[0.66rem] text-coral-300">
                    missing — nothing is being scanned here
                  </span>
                )}
                {root.exists && !root.readable && (
                  <span className="shrink-0 text-[0.66rem] text-lantern-300">
                    not readable — check its permissions
                  </span>
                )}

                <span className="h-px flex-1 bg-harbor-800/70" aria-hidden />

                {/* Deliberately a word, not an ×. An × at the right-hand edge of a
                    panel reads as "close the panel" — it was misread exactly that
                    way, and the click silently removed the only scanned directory.
                    Two steps as well, because there is no undo for it. */}
                {confirming === root.path ? (
                  <span className="flex shrink-0 items-center gap-1.5">
                    <span className="text-[0.62rem] text-foam-400">
                      {roots.length === 1 ? 'scan nothing at all?' : 'stop scanning it?'}
                    </span>
                    <button
                      type="button"
                      onClick={() => {
                        setConfirming(null)
                        remove(root.path)
                      }}
                      disabled={loading}
                      className="rounded border border-coral-400/60 px-1.5 py-0.5 text-[0.62rem] text-coral-300 transition-colors hover:bg-coral-400/10 disabled:opacity-40"
                    >
                      yes
                    </button>
                    <button
                      type="button"
                      onClick={() => setConfirming(null)}
                      className="rounded border border-harbor-700 px-1.5 py-0.5 text-[0.62rem] text-foam-100 transition-colors hover:border-lit-400/50"
                    >
                      keep it
                    </button>
                  </span>
                ) : (
                  <button
                    type="button"
                    onClick={() => setConfirming(root.path)}
                    disabled={loading}
                    className="shrink-0 rounded px-1.5 py-0.5 font-mono text-[0.62rem] text-foam-400 transition-colors hover:text-coral-300 disabled:opacity-40"
                  >
                    stop scanning
                  </button>
                )}
              </li>
            ))}
            {roots.length === 0 && !loading && (
              <li className="flex flex-wrap items-center gap-2 font-mono text-[0.7rem] text-foam-400">
                <span>No directories are being scanned, so the boatyard stays empty.</span>
                {/* The way back, without having to remember the default. */}
                <button
                  type="button"
                  onClick={() => add('~/projects')}
                  className="rounded border border-harbor-700 px-2 py-0.5 text-[0.66rem] text-foam-100 transition-colors hover:border-lit-400/50 hover:text-lit-400"
                >
                  scan ~/projects
                </button>
              </li>
            )}
          </ul>

          <form onSubmit={submit} className="flex items-center gap-2">
            <input
              ref={inputRef}
              value={draft}
              onChange={(e) => {
                setDraft(e.target.value)
                if (error) clearError()
              }}
              onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}
              placeholder="~/git"
              spellCheck={false}
              autoCapitalize="off"
              autoComplete="off"
              aria-label="Directory to scan for projects"
              className="min-w-0 flex-1 rounded border border-harbor-800 bg-harbor-975 px-2 py-1 font-mono text-[0.72rem] text-foam-100 placeholder:text-foam-400/70 focus:border-lit-400/50 focus:outline-none"
            />
            <button
              type="submit"
              disabled={loading || !draft.trim()}
              className="shrink-0 rounded border border-harbor-700 px-2.5 py-1 font-mono text-[0.7rem] text-foam-100 transition-colors hover:border-lit-400/50 hover:text-lit-400 disabled:opacity-40"
            >
              add
            </button>
          </form>

          {error && (
            <p role="alert" className="mt-2 font-mono text-[0.68rem] text-coral-300">
              {error}
            </p>
          )}

          <p className="mt-2 font-mono text-[0.64rem] leading-relaxed text-foam-400">
            Each directory is read one level deep, so <span className="text-foam-300">~/git/app</span>{' '}
            is found but <span className="text-foam-300">~/git/clients/app</span> is not — add the
            subfolder itself for those. {total} project{total === 1 ? '' : 's'} found in total.
            {file && (
              <>
                {' '}
                Saved in <span className="text-foam-300">{file}</span>.
              </>
            )}
          </p>
        </div>
      )}
    </div>
  )
}
