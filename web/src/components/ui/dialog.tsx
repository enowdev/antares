import * as React from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import { X } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

export const Dialog = DialogPrimitive.Root
export const DialogTrigger = DialogPrimitive.Trigger
export const DialogClose = DialogPrimitive.Close

/**
 * Modal surface. On phones it rises from the bottom like a sheet — thumbs reach
 * the bottom of the screen, not the middle — and becomes a centred dialog from
 * the small breakpoint up.
 */
export function DialogContent({
  className,
  children,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Content>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay
        className={cn(
          'm-dialog-overlay fixed inset-0 z-50 bg-[#04050699] backdrop-blur-[4px]',
        )}
      />
      <DialogPrimitive.Content
        className={cn(
          'm-dialog tp-panel fixed z-50 flex flex-col bg-card text-card-foreground shadow-[0_28px_100px_#0009]',
          // Phone: full-width sheet pinned to the bottom, capped so the list
          // behind stays partly visible.
          'safe-bottom inset-x-0 bottom-0 max-h-[88dvh] border-t border-border',
          // Desktop: a centred panel.
          'sm:inset-x-auto sm:bottom-auto sm:left-1/2 sm:top-1/2 sm:max-h-[85dvh] sm:w-full sm:max-w-lg',
          'sm:-translate-x-1/2 sm:-translate-y-1/2 sm:border',
          className,
        )}
        {...props}
      >
        {children}
        <DialogPrimitive.Close
          className="absolute right-3 top-3 rounded-full p-2 text-muted-foreground transition-colors hover:bg-raised hover:text-foreground"
          aria-label="Close"
        >
          <X className="size-4" />
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

export function DialogHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn('shrink-0 space-y-1.5 border-b border-border p-4 pr-12 sm:p-5 sm:pr-12', className)}
      {...props}
    />
  )
}

export function DialogTitle({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      className={cn('text-xl font-medium tracking-[-0.5px]', className)}
      {...props}
    />
  )
}

export function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      className={cn('text-xs leading-relaxed text-muted-foreground sm:text-sm', className)}
      {...props}
    />
  )
}

/** Scrolling middle section, so long forms never push the footer off-screen. */
export function DialogBody({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('min-h-0 flex-1 space-y-4 overflow-y-auto p-4 sm:p-5', className)} {...props} />
}

export function DialogFooter({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        'flex shrink-0 flex-col-reverse gap-2 border-t border-border p-4 sm:flex-row sm:justify-end sm:p-5',
        className,
      )}
      {...props}
    />
  )
}
