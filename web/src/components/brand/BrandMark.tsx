import { cn } from '@/lib/utils'

/** Antares's mark: the red star. It fades and settles in when it first
 * appears (.brand-mark in motion.css). */
export function BrandMark({ className, size = 28 }: { className?: string; size?: number }) {
  return (
    <img
      src="/antares-192.png"
      alt=""
      aria-hidden
      width={size}
      height={size}
      draggable={false}
      className={cn('brand-mark shrink-0 select-none object-contain', className)}
    />
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
