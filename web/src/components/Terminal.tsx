import { memo, useEffect, useMemo, useRef } from 'react'
import { parseAnsi } from '../lib/ansi'

/**
 * Renders captured terminal output with its colours intact.
 *
 * Sticks to the bottom while you're already at the bottom, and stops following
 * the moment you scroll up — a log that yanks you back down mid-read is useless
 * for the thing you'd actually open it for.
 */
interface TerminalProps {
  text: string
  /** Compact mode is the small preview shown in the grid. */
  compact?: boolean
  /** Rendered when there is no output at all. */
  empty?: React.ReactNode
  className?: string
}

export const Terminal = memo(function Terminal({
  text,
  compact = false,
  empty,
  className = '',
}: TerminalProps) {
  const scroller = useRef<HTMLDivElement>(null)
  const pinnedToBottom = useRef(true)

  const lines = useMemo(() => {
    const parsed = parseAnsi(text)
    // The grid preview only has room for the tail.
    return compact ? parsed.slice(-14) : parsed
  }, [text, compact])

  useEffect(() => {
    const el = scroller.current
    if (!el || !pinnedToBottom.current) return
    el.scrollTop = el.scrollHeight
  }, [lines])

  const onScroll = () => {
    const el = scroller.current
    if (!el) return
    // A small tolerance, so "close enough to the bottom" still counts.
    pinnedToBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
  }

  if (!text) {
    return (
      <div
        className={`flex items-center justify-center rounded-lg border border-harbor-800 bg-harbor-975 p-4 ${className}`}
      >
        {empty ?? <span className="font-mono text-[0.7rem] text-foam-400">No output yet.</span>}
      </div>
    )
  }

  return (
    <div
      ref={scroller}
      onScroll={onScroll}
      className={[
        'overflow-auto rounded-lg border border-harbor-800 bg-harbor-975 font-mono',
        compact ? 'px-3 py-2 text-[0.62rem] leading-[1.45]' : 'px-4 py-3 text-[0.76rem] leading-[1.55]',
        className,
      ].join(' ')}
    >
      {lines.map((spans, i) => (
        <div key={i} className="whitespace-pre-wrap break-words text-foam-100">
          {spans.length === 0 ? (
            ' '
          ) : (
            spans.map((span, j) => (
              <span
                key={j}
                style={{
                  color: span.color,
                  backgroundColor: span.background,
                  fontWeight: span.bold ? 700 : undefined,
                  fontStyle: span.italic ? 'italic' : undefined,
                  textDecoration: span.underline ? 'underline' : undefined,
                }}
              >
                {span.text}
              </span>
            ))
          )}
        </div>
      ))}
    </div>
  )
})
