import { cn } from '@/lib/utils'

/** The mark's 5 by 5 grid: a star of lit cells — the cross, and the four
 * cells beside the centre on the diagonals — drawn from the centre out, with
 * the red core Antares is named for. Cells of 3 with gaps of 1 on a 20 unit
 * square, so at 20px every edge falls on a whole pixel. */
const MARK_CELLS = Array.from({ length: 25 }, (_, i) => {
  const row = Math.floor(i / 5)
  const col = i % 5
  const ring = Math.max(Math.abs(row - 2), Math.abs(col - 2))
  const lit = row === 2 || col === 2 || (ring === 1 && Math.abs(row - 2) === Math.abs(col - 2))
  return { x: 1 + col * 4, y: 1 + row * 4, lit, ring }
})

export function BrandMark({ className, size = 28 }: { className?: string; size?: number }) {
  return (
    <svg
      className={cn('brand-mark shrink-0 text-foreground', className)}
      viewBox="0 0 20 20"
      width={size}
      height={size}
      aria-hidden="true"
    >
      {MARK_CELLS.map(({ x, y, lit, ring }) => (
        <rect
          key={`${x}-${y}`}
          className={lit ? cn('mark-cell', `mark-ring-${ring}`, ring === 0 && 'mark-core') : 'mark-dim'}
          x={x}
          y={y}
          width={3}
          height={3}
        />
      ))}
    </svg>
  )
}

/** The mark and the wordmark, as the site header draws them. */
export function Brand({ className, size = 28, label }: { className?: string; size?: number; label?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-2.5', className)}>
      <BrandMark size={size} />
      <span className="text-[22px] font-[350] leading-none tracking-[-0.8px]">antares</span>
      {label ? (
        <span className="ml-1 rounded-full bg-raised px-2 py-0.5 text-[10.5px] font-medium leading-4 text-muted-foreground">
          {label}
        </span>
      ) : null}
    </span>
  )
}
