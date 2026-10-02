import { useState } from 'react'
import { cn } from '@/lib/utils'

/**
 * Redacts inline text behind a marker bar until clicked; click again to hide.
 * The bar is a theme-neutral grey — light grey in light mode, dark grey in dark
 * mode — so it reads as a censor block against the card in either theme. The
 * text is always in the DOM; this is a shoulder-surfing guard for
 * sensitive-but-not-secret values (IPs, hostnames), not access control.
 */
export function Spoiler({ children, className }: { children: React.ReactNode; className?: string }) {
  const [shown, setShown] = useState(false)
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation()
        setShown((s) => !s)
      }}
      title={shown ? 'Hide' : 'Reveal'}
      className={cn(
        'rounded-[var(--radius-xs)] px-1 align-baseline transition-colors',
        shown
          ? 'bg-transparent text-inherit'
          // A theme-aware grey censor bar: --muted-foreground is a medium grey in
          // both light and dark, so it reads as a redaction block on the card
          // either way. Text is transparent so it stays hidden until revealed.
          : 'select-none bg-muted-foreground/70 text-transparent hover:bg-muted-foreground',
        className,
      )}
    >
      {children}
    </button>
  )
}
