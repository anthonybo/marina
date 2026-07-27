import { memo } from 'react'
import { loadColor, loadLabel, loadLevel } from '../lib/useHealth'

/**
 * How hard one app is working, as a bar.
 *
 * The scale is cores rather than a share of the machine: one core fully busy is
 * ordinary for a dev server mid-rebuild, while three cores pinned is what actually
 * makes the laptop lag. The bar fills against a four-core reference so the common
 * case sits in the lower quarter and a genuine problem is unmistakable.
 */
interface LoadMeterProps {
  cpu: number
  cores: number
  className?: string
  /** Show the number beside the bar. */
  showValue?: boolean
}

export const LoadMeter = memo(function LoadMeter({
  cpu,
  cores,
  className = '',
  showValue = false,
}: LoadMeterProps) {
  const level = loadLevel(cpu)
  const color = loadColor[level]
  // Four cores, or the machine's total if it has fewer.
  const ceiling = Math.min(cores, 4) * 100
  const fill = Math.max(cpu > 0 ? 3 : 0, Math.min(100, (cpu / ceiling) * 100))

  return (
    <span
      className={`flex items-center gap-1.5 ${className}`}
      title={`${cpu.toFixed(1)}% of one core — ${loadLabel[level]}`}
    >
      <span className="relative h-[3px] w-full min-w-6 overflow-hidden rounded-full bg-harbor-700">
        <span
          className="absolute inset-y-0 left-0 rounded-full transition-[width] duration-500"
          style={{ width: `${fill}%`, backgroundColor: color }}
        />
      </span>
      {showValue && (
        <span className="tnum shrink-0 font-mono text-[0.58rem]" style={{ color }}>
          {cpu < 10 ? cpu.toFixed(1) : Math.round(cpu)}%
        </span>
      )}
    </span>
  )
})
