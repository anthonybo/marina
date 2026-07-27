/**
 * Minimal ANSI renderer for dev-server output.
 *
 * Dev servers colour their logs, and stripping that away loses real information —
 * Vite's error output is red for a reason. This handles SGR colour and weight,
 * and discards the sequences a scrolling log view can't act on (cursor moves,
 * line erases from spinners) rather than printing them as mojibake.
 *
 * The palette is harbour-tinted and deliberately avoids near-black greys: a
 * terminal colour that renders as unreadable dim text is worse than no colour.
 */

export interface Span {
  text: string
  color?: string
  background?: string
  bold?: boolean
  italic?: boolean
  underline?: boolean
}

const FG: Record<number, string> = {
  30: '#7fa3b0', // "black" lifted to something readable on a dark background
  31: '#ff7a8a',
  32: '#5fe3b3',
  33: '#ffc978',
  34: '#7dd3fc',
  35: '#c4b5fd',
  36: '#5eead4',
  37: '#dcecf1',
  90: '#8fb8c8', // bright black, also lifted
  91: '#ff9aa6',
  92: '#6ff0dc',
  93: '#ffd93f',
  94: '#a5dfff',
  95: '#d8caff',
  96: '#7ff3e0',
  97: '#ffffff',
}

const BG: Record<number, string> = {
  40: '#143c4f',
  41: '#7f2b36',
  42: '#1c6b57',
  43: '#7a5a1e',
  44: '#1f4f73',
  45: '#4d3f7a',
  46: '#1c6b66',
  47: '#3a4e57',
  100: '#1d5570',
  101: '#96343f',
  102: '#23806a',
  103: '#8f6b23',
  104: '#265e87',
  105: '#5b4a90',
  106: '#23807a',
  107: '#4a6b78',
}

/** Matches any CSI sequence; the final byte tells us what it was. */
const CSI = /\x1b\[([0-9;?]*)([A-Za-z])/g
/** OSC sequences (window titles) terminated by BEL or ST. */
const OSC = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g

interface State {
  color?: string
  background?: string
  bold?: boolean
  italic?: boolean
  underline?: boolean
}

function applySGR(state: State, params: string): State {
  const codes = params === '' ? [0] : params.split(';').map((p) => Number(p) || 0)
  const next: State = { ...state }

  for (let i = 0; i < codes.length; i++) {
    const code = codes[i]
    if (code === 0) {
      next.color = undefined
      next.background = undefined
      next.bold = false
      next.italic = false
      next.underline = false
    } else if (code === 1) next.bold = true
    else if (code === 2) next.bold = false // dim: render as normal, never faint
    else if (code === 3) next.italic = true
    else if (code === 4) next.underline = true
    else if (code === 22) next.bold = false
    else if (code === 23) next.italic = false
    else if (code === 24) next.underline = false
    else if (code === 39) next.color = undefined
    else if (code === 49) next.background = undefined
    else if (FG[code]) next.color = FG[code]
    else if (BG[code]) next.background = BG[code]
    else if (code === 38 || code === 48) {
      // Extended colour: 5;n (256) or 2;r;g;b (truecolour).
      const mode = codes[i + 1]
      if (mode === 5) {
        const value = xterm256(codes[i + 2])
        if (code === 38) next.color = value
        else next.background = value
        i += 2
      } else if (mode === 2) {
        const value = `rgb(${codes[i + 2] ?? 0}, ${codes[i + 3] ?? 0}, ${codes[i + 4] ?? 0})`
        if (code === 38) next.color = value
        else next.background = value
        i += 4
      }
    }
  }
  return next
}

/** Approximates the xterm 256-colour cube, keeping the greys readable. */
function xterm256(n: number): string {
  if (n < 16) return FG[n < 8 ? 30 + n : 90 + (n - 8)] ?? '#dcecf1'
  if (n < 232) {
    const i = n - 16
    const step = (v: number) => (v === 0 ? 0 : 55 + v * 40)
    return `rgb(${step(Math.floor(i / 36))}, ${step(Math.floor((i % 36) / 6))}, ${step(i % 6)})`
  }
  // Greyscale ramp, floored so the darkest greys stay legible.
  const level = Math.max(90, 8 + (n - 232) * 10)
  return `rgb(${level}, ${level + 12}, ${level + 18})`
}

/**
 * Parses a chunk of terminal output into styled spans, one array per line.
 * Carriage returns are honoured the way a terminal would: a `\r` without a
 * newline rewrites the current line, which is how progress spinners behave.
 */
export function parseAnsi(input: string): Span[][] {
  // Drop sequences a log view cannot represent, but keep SGR for the parser.
  const cleaned = input.replace(OSC, '')

  const lines: Span[][] = []
  let current: Span[] = []
  let state: State = {}

  // Appends text to the line being built. Newlines never reach here — emit()
  // splits on them — so this only has to honour carriage returns, where anything
  // before the last \r was overwritten in a real terminal.
  const pushText = (text: string) => {
    if (!text) return
    const chunks = text.split('\r')
    if (chunks.length > 1) current = []
    const visible = chunks[chunks.length - 1]
    if (!visible) return
    current.push({
      text: visible,
      color: state.color,
      background: state.background,
      bold: state.bold,
      italic: state.italic,
      underline: state.underline,
    })
  }

  let lastIndex = 0
  CSI.lastIndex = 0
  let match: RegExpExecArray | null

  const emit = (text: string) => {
    let buffer = ''
    for (const char of text) {
      if (char === '\n') {
        pushText(buffer)
        buffer = ''
        lines.push(current)
        current = []
      } else {
        buffer += char
      }
    }
    pushText(buffer)
  }

  while ((match = CSI.exec(cleaned)) !== null) {
    emit(cleaned.slice(lastIndex, match.index))
    if (match[2] === 'm') state = applySGR(state, match[1])
    // Every other CSI (cursor motion, erase) is dropped: a scrolling view has
    // nowhere to move a cursor to.
    lastIndex = match.index + match[0].length
  }
  emit(cleaned.slice(lastIndex))

  if (current.length > 0) lines.push(current)
  return lines
}

/** Strips all escape sequences, for copying or measuring. */
export function stripAnsi(input: string): string {
  return input.replace(OSC, '').replace(CSI, '')
}
