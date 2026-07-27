import { useEffect, useRef, useState } from 'react'
import { useRoots } from '../lib/useRoots'

/**
 * Which directories Marina looks in for projects you could start.
 *
 * A gear beside the section heading rather than a panel of its own: this is
 * settings, touched when a machine is new and then never again, so it should cost
 * nothing to ignore. Opening it floats the editor over the page instead of
 * pushing the list down, and closing it leaves no trace but the icon.
 *
 * The destructive control is deliberately the words "stop scanning" and not an ×.
 * An × at a panel's edge reads as "close", and when it was one, it was clicked
 * that way — removing the only scanned directory and emptying the boatyard with
 * nothing to undo it. Removal now takes two clicks and names its consequence.
 */
const TIP =
  'Which directories Marina scans for projects you could start. Each is read one level deep.'

export function RootsEditor() {
  const [open, setOpen] = useState(false)
  const { roots, file, loading, error, add, remove, clearError } = useRoots(open)
  const [draft, setDraft] = useState('')
  /** Path awaiting a confirmed removal, if any. */
  const [confirming, setConfirming] = useState<string | null>(null)
  /**
   * Which edge the panel hangs from. Measured rather than fixed, because the gear
   * sits at the far right of the manifest heading but mid-row in the harbour: one
   * hard-coded side clips the panel in whichever view it wasn't chosen for, and
   * the harbour card is overflow-hidden, so the clipped part simply vanishes.
   */
  const [align, setAlign] = useState<'left' | 'right'>('right')
  const wrapRef = useRef<HTMLSpanElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!open) {
      setConfirming(null)
      return
    }
    inputRef.current?.focus()

    const rect = wrapRef.current?.getBoundingClientRect()
    if (rect) {
      const width = Math.min(34 * 16, window.innerWidth * 0.85)
      setAlign(rect.left + width + 8 <= window.innerWidth ? 'left' : 'right')
    }
  }, [open])

  // A floating panel has to close the way one is expected to: Escape, or a click
  // anywhere else on the page.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('mousedown', onDown)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('mousedown', onDown)
    }
  }, [open])

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const path = draft.trim()
    if (!path) return
    if (await add(path)) setDraft('')
  }

  return (
    <span ref={wrapRef} className="relative inline-flex shrink-0 items-center">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-label="Scanned directories"
        title={TIP}
        className={`rounded p-0.5 transition-colors ${
          open ? 'text-lit-400' : 'text-foam-400 hover:text-foam-100'
        }`}
      >
        <Gear />
      </button>

      {open && (
        <div
          // Floated, so opening it never moves the list underneath.
          // normal-case and tracking-normal because the gear lives inside a section
          // heading, and that heading's stencil style — uppercase, wide-tracked —
          // is inherited by everything in here otherwise.
          className={[
            'absolute top-full z-30 mt-1.5 w-[min(34rem,85vw)] rounded-lg border border-harbor-700',
            'bg-harbor-950 p-3 shadow-xl shadow-black/40',
            'font-normal normal-case tracking-normal',
            align === 'left' ? 'left-0' : 'right-0',
          ].join(' ')}
        >
          <p className="stencil mb-2 text-foam-300">Directories scanned for projects</p>

          <ul className="mb-2.5 flex max-h-52 flex-col gap-1 overflow-y-auto">
            {roots.map((root) => (
              <li key={root.path} className="flex items-center gap-2.5 font-mono text-[0.72rem]">
                <span
                  title={root.path}
                  className={`max-w-[55%] truncate ${root.exists ? 'text-foam-100' : 'text-coral-300'}`}
                >
                  {root.display}
                </span>

                {/* The count is the visible consequence of scanning one level deep:
                    zero means the projects are a level further down. */}
                {root.exists && root.readable && (
                  <span className="tnum shrink-0 text-[0.66rem] text-foam-400">
                    {root.projects} {root.projects === 1 ? 'project' : 'projects'}
                  </span>
                )}
                {!root.exists && (
                  <span className="shrink-0 text-[0.66rem] text-coral-300">missing</span>
                )}
                {root.exists && !root.readable && (
                  <span className="shrink-0 text-[0.66rem] text-lantern-300">not readable</span>
                )}

                <span className="h-px flex-1 bg-harbor-800/70" aria-hidden />

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
                    className="shrink-0 rounded px-1 py-0.5 font-mono text-[0.62rem] text-foam-400 transition-colors hover:text-coral-300 disabled:opacity-40"
                  >
                    stop scanning
                  </button>
                )}
              </li>
            ))}

            {roots.length === 0 && !loading && (
              <li className="flex flex-wrap items-center gap-2 font-mono text-[0.7rem] text-foam-400">
                <span>Nothing is being scanned.</span>
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

          <p className="mt-2 font-mono text-[0.62rem] leading-relaxed text-foam-400">
            Read one level deep: <span className="text-foam-300">~/git/app</span> is found,{' '}
            <span className="text-foam-300">~/git/clients/app</span> is not — add that subfolder
            instead.
            {file && (
              <>
                {' '}
                Saved in <span className="text-foam-300">{file}</span>.
              </>
            )}
          </p>
        </div>
      )}
    </span>
  )
}

function Gear() {
  return (
    <svg viewBox="0 0 16 16" className="size-3.5" aria-hidden fill="currentColor">
      <path d="M8 10.4a2.4 2.4 0 1 1 0-4.8 2.4 2.4 0 0 1 0 4.8Zm0-1.3a1.1 1.1 0 1 0 0-2.2 1.1 1.1 0 0 0 0 2.2Z" />
      <path d="M7.1.9h1.8l.26 1.62c.4.12.78.28 1.13.48l1.3-1 1.27 1.27-1 1.3c.2.35.36.73.48 1.13l1.62.26v1.8l-1.62.26c-.12.4-.28.78-.48 1.13l1 1.3-1.27 1.27-1.3-1c-.35.2-.73.36-1.13.48L8.9 15.1H7.1l-.26-1.62a4.7 4.7 0 0 1-1.13-.48l-1.3 1L3.14 12.7l1-1.3a4.7 4.7 0 0 1-.48-1.13L2.04 10V8.2l1.62-.26c.12-.4.28-.78.48-1.13l-1-1.3 1.27-1.27 1.3 1c.35-.2.73-.36 1.13-.48L7.1.9Zm.9 2.6a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9Z" />
    </svg>
  )
}
