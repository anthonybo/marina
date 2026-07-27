import { memo } from 'react'

/**
 * A CPU trace.
 *
 * Scaled to the series' own peak rather than a fixed ceiling, because the useful
 * question is "is this app doing something unusual for itself" — a worker that
 * normally idles at 2% and jumps to 30% matters, and a fixed 400% axis would flatten
 * that into a straight line.
 */
interface SparklineProps {
  points: number[]
  color: string
  className?: string
  /** Drawn as a dotted rule, for the "one core" reference. */
  reference?: number
}

export const Sparkline = memo(function Sparkline({
  points,
  color,
  className = '',
  reference,
}: SparklineProps) {
  if (points.length < 2) {
    return (
      <div className={`flex items-center justify-center ${className}`}>
        <span className="font-mono text-[0.6rem] text-foam-400">collecting…</span>
      </div>
    )
  }

  const width = 100
  const height = 28
  // Scale to the series' own peak, deliberately *not* including the reference.
  // Folding a 100% reference into the scale squashes a worker that varies between
  // 6% and 19% into a flat line at the bottom — which is exactly the variation you
  // opened the view to see. The reference is drawn only when it falls inside the
  // range the data already occupies.
  const peak = Math.max(...points, 1)
  const step = width / (points.length - 1)

  const coords = points.map((value, i) => {
    const x = i * step
    const y = height - (value / peak) * (height - 2) - 1
    return `${x.toFixed(2)},${y.toFixed(2)}`
  })

  const line = `M ${coords.join(' L ')}`
  const area = `${line} L ${width},${height} L 0,${height} Z`
  const refY = reference ? height - (reference / peak) * (height - 2) - 1 : null

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      className={className}
      aria-hidden
    >
      {refY !== null && refY > 0 && refY < height && (
        <line
          x1="0"
          x2={width}
          y1={refY}
          y2={refY}
          stroke="#8fb8c8"
          strokeWidth="0.5"
          strokeDasharray="2 2"
          opacity="0.4"
        />
      )}
      <path d={area} fill={color} opacity="0.14" />
      <path d={line} fill="none" stroke={color} strokeWidth="1.4" vectorEffect="non-scaling-stroke" />
    </svg>
  )
})
