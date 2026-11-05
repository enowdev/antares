import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { CircleNotch } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

/**
 * Actions are pills in the site's monospace voice, lowercase. Ghost and link
 * actions stay quiet.
 */
const buttonVariants = cva(
  'inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-full font-mono text-xs font-normal lowercase tracking-[0.02em] transition-[background-color,border-color,color] duration-150 disabled:pointer-events-none disabled:opacity-50 select-none [&_svg]:shrink-0',
  {
    variants: {
      variant: {
        default: 'tp-btn tp-btn-solid border border-primary bg-primary text-primary-foreground hover:border-foreground hover:bg-foreground',
        secondary: 'tp-btn border border-border bg-secondary text-secondary-foreground hover:border-line hover:bg-raised',
        outline: 'tp-btn border border-border bg-transparent text-foreground hover:border-line hover:bg-raised',
        ghost: 'border border-transparent text-muted-foreground hover:bg-raised hover:text-foreground',
        destructive:
          'tp-btn border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] bg-[color-mix(in_oklch,var(--destructive)_12%,var(--background))] text-destructive hover:bg-[color-mix(in_oklch,var(--destructive)_20%,var(--background))]',
        link: 'text-foreground underline underline-offset-4 decoration-line hover:decoration-foreground',
      },
      size: {
        sm: 'h-8 px-3.5 text-[11px] [&_svg]:size-3.5',
        default: 'h-9 px-4.5 [&_svg]:size-4',
        lg: 'h-11 px-5 text-[13px] [&_svg]:size-4',
        icon: 'size-9 [&_svg]:size-4',
        'icon-sm': 'size-8 [&_svg]:size-4',
      },
    },
    defaultVariants: { variant: 'default', size: 'default' },
  },
)

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {
  asChild?: boolean
  loading?: boolean
}

/** Primary interactive control. Set `loading` to show an inline spinner. */
export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, asChild = false, loading = false, children, disabled, ...props }, ref) => {
    const Comp = asChild ? Slot : 'button'
    return (
      <Comp
        ref={ref}
        className={cn(buttonVariants({ variant, size, className }))}
        disabled={disabled || loading}
        {...props}
      >
        {loading ? (
          <>
            <CircleNotch className="animate-spin" weight="bold" />
            {children}
          </>
        ) : (
          children
        )}
      </Comp>
    )
  },
)
Button.displayName = 'Button'

export { buttonVariants }
