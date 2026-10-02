import { useEffect, useRef, useState } from 'react'
import { CaretDown, Check, UsersThree, Warning } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'

interface Role {
  name: string
  title: string
  summary: string
  category: string
  danger?: boolean
  subrole?: boolean
}

const CATEGORY_LABEL: Record<string, string> = {
  general: 'General',
  engineering: 'Engineering',
  research: 'Research',
  writing: 'Writing',
  security: 'Security',
}

/**
 * Choose the specialist a conversation runs as, from the header — so the role
 * is a click, not a slash command. The selection rides on the next message and
 * is remembered by the server, so it sticks across turns.
 */
export function RolePicker({
  value,
  onChange,
  compact = false,
}: {
  value: string
  onChange: (role: string) => void
  // compact renders a short chip for the composer's control row, instead of the
  // tall standalone box.
  compact?: boolean
}) {
  const { t } = useI18n()
  const [roles, setRoles] = useState<Role[]>([])
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    get<{ roles: Role[] }>('/roles')
      // Subroles are reached only through their master role, never selected
      // directly — so they stay out of the picker.
      .then((r) => setRoles((r.roles ?? []).filter((x) => !x.subrole)))
      .catch(() => setRoles([]))
  }, [])

  // Close when clicking away.
  useEffect(() => {
    if (!open) return
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [open])

  // Empty value means "the default agent", which is the Orchestrator (the
  // `assistant` role). Resolve it so the button and the checkmark reflect that,
  // rather than showing a separate "no role" state.
  const selected = value || 'assistant'
  const current = roles.find((r) => r.name === selected)

  // Group by category, preserving the server's order.
  const groups: { category: string; roles: Role[] }[] = []
  const index = new Map<string, number>()
  for (const r of roles) {
    if (!index.has(r.category)) {
      index.set(r.category, groups.length)
      groups.push({ category: r.category, roles: [] })
    }
    groups[index.get(r.category)!].roles.push(r)
  }

  const pick = (name: string) => {
    onChange(name)
    setOpen(false)
  }

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={current ? current.title : t('roles.orchestrator')}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          'flex items-center gap-1.5 rounded-full border border-border bg-transparent text-muted-foreground transition-[border-color,background-color,color] duration-200 hover:border-line hover:bg-raised hover:text-foreground aria-expanded:border-line aria-expanded:bg-nav-active aria-expanded:text-foreground',
          compact ? 'h-8 min-w-8 justify-center rounded-full px-2 text-xs sm:px-3' : 'h-[3.25rem] px-3 text-sm',
        )}
      >
        <UsersThree className={cn('shrink-0', compact ? 'size-3.5' : 'size-4')} />
        <span className="hidden max-w-28 truncate sm:inline">
          {current ? current.title : t('roles.orchestrator')}
        </span>
        <CaretDown className="hidden size-3 shrink-0 text-muted-foreground sm:block" />
      </button>

      {open ? (
        <div className="m-open absolute bottom-full left-0 z-30 mb-2 max-h-72 w-72 max-w-[calc(100vw-2rem)] overflow-y-auto rounded-[var(--radius-lg)] border border-border bg-popover p-1 shadow-[0_10px_28px_-14px_#00000080]">
          {groups.map((g) => (
            <div key={g.category}>
              <div className="eyebrow px-2.5 pb-1 pt-2 !text-[10px]">
                {CATEGORY_LABEL[g.category] ?? g.category}
              </div>
              {g.roles.map((r) => (
                <button
                  key={r.name}
                  // Selecting the Orchestrator (the default) clears the role back
                  // to empty, so the default stays represented as "no explicit
                  // role" rather than a pinned name.
                  onClick={() => pick(r.name === 'assistant' ? '' : r.name)}
                  className={cn(
                    'flex w-full items-start gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left transition-colors hover:bg-raised',
                    selected === r.name && 'bg-nav-active',
                  )}
                >
                  {selected === r.name ? (
                    <Check className="mt-0.5 size-3.5 shrink-0 text-foreground" />
                  ) : (
                    <span className="w-3.5 shrink-0" />
                  )}
                  <span className="min-w-0">
                    <span className="flex items-center gap-1.5 text-xs font-medium">
                      {r.title}
                      {r.danger ? <Warning className="size-3 text-[var(--warning)]" weight="fill" /> : null}
                    </span>
                    <span className="block truncate text-[11px] text-muted-foreground">{r.summary}</span>
                  </span>
                </button>
              ))}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  )
}
