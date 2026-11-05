import { useEffect, useRef, useState } from 'react'
import { ArrowUp, CaretDown, Check, FolderOpen, House, X } from '@phosphor-icons/react'
import { get } from '@/lib/api'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'

interface BrowseResp {
  path: string
  parent: string
  home: string
  entries: { name: string; path: string }[]
}

/**
 * Bind a chat to a project folder. A project session can write only inside the
 * chosen folder (plus the antares workspace) while reading anywhere on the
 * machine, and it loads the project's AGENTS.md / skills / structure into the
 * agent's context.
 *
 * The picker offers two ways to choose: browse the machine's directories, or
 * paste/type an absolute path with directory autocomplete. It is disabled once a
 * session has started — a session's project binding is fixed on its first turn.
 */
export function ProjectPicker({
  value,
  onChange,
  locked = false,
}: {
  // Currently bound project directory, or '' for an ordinary session.
  value: string
  onChange: (dir: string) => void
  // locked shows the binding read-only (an existing session cannot be rebound).
  locked?: boolean
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  const [cwd, setCwd] = useState('')
  const [parent, setParent] = useState('')
  const [entries, setEntries] = useState<{ name: string; path: string }[]>([])
  const [home, setHome] = useState('')
  const [typed, setTyped] = useState('')
  const [error, setError] = useState('')

  const browse = (path: string) => {
    get<BrowseResp>(`/project/browse?path=${encodeURIComponent(path)}`)
      .then((d) => {
        setCwd(d.path)
        setParent(d.parent)
        setEntries(d.entries)
        setHome(d.home)
        setTyped(d.path)
        setError('')
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
  }

  // Load the starting directory (home) the first time the picker opens.
  useEffect(() => {
    if (open && !cwd) browse('')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // Close when clicking away.
  useEffect(() => {
    if (!open) return
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [open])

  // Typing a path that ends in "/" navigates into that directory, so the folder
  // browser below just refreshes — no separate autocomplete dropdown to overlap
  // the list. Debounced so it follows the user rather than fighting them.
  useEffect(() => {
    if (!open || !typed || typed === cwd) return
    if (!typed.endsWith('/')) return
    const id = setTimeout(() => browse(typed), 250)
    return () => clearTimeout(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [typed, open, cwd])

  // A partial name after the current dir filters the browser list in place.
  const typedPrefix = (() => {
    if (!typed || typed === cwd) return ''
    const slash = typed.lastIndexOf('/')
    const dir = slash >= 0 ? typed.slice(0, slash) : ''
    // Only filter when the typed directory matches what the browser is showing.
    if (dir && cwd && dir !== cwd.replace(/\/$/, '')) return ''
    return typed.slice(slash + 1).toLowerCase()
  })()
  const shownEntries = typedPrefix
    ? entries.filter((e) => e.name.toLowerCase().startsWith(typedPrefix))
    : entries

  const choose = (dir: string) => {
    onChange(dir)
    setOpen(false)
  }

  const label = value ? value.split('/').filter(Boolean).pop() || value : t('project.none')

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => !locked && setOpen((v) => !v)}
        disabled={locked}
        title={value || undefined}
        aria-expanded={open && !locked}
        aria-label={label}
        className={cn(
          'flex h-8 min-w-8 items-center justify-center gap-1.5 rounded-full border px-2 text-xs sm:px-3 transition-[border-color,background-color,color] duration-200 aria-expanded:bg-nav-active aria-expanded:text-foreground',
          value
            ? 'border-foreground/40 text-foreground'
            : 'border-border text-muted-foreground hover:border-line hover:bg-raised hover:text-foreground',
          locked && 'cursor-default opacity-80 hover:border-border hover:bg-transparent',
        )}
      >
        <FolderOpen className="size-3.5 shrink-0" />
        <span className={cn('hidden max-w-32 truncate sm:inline', value && 'font-mono text-[11px]')}>{label}</span>
        {value && !locked ? (
          <X
            className="size-3 shrink-0 text-muted-foreground hover:text-foreground"
            onClick={(e) => {
              e.stopPropagation()
              onChange('')
            }}
          />
        ) : !locked ? (
          <CaretDown className="hidden size-3 shrink-0 text-muted-foreground sm:block" />
        ) : null}
      </button>

      {open && !locked ? (
        <div className="m-open absolute bottom-full left-0 z-30 mb-2 w-96 max-w-[calc(100vw-2rem)] overflow-hidden rounded-[var(--radius-lg)] border border-border bg-popover shadow-[0_10px_28px_-14px_#00000080]">
          <div className="border-b border-border px-3 py-2">
            <div className="text-xs font-medium">{t('project.pick')}</div>
            <div className="mt-0.5 text-[11px] text-muted-foreground">{t('project.pickHint')}</div>
          </div>

          {/* Path input with autocomplete. Enter selects the typed path. */}
          <div className="relative border-b border-border p-2">
            <div className="flex items-center gap-1.5">
              <input
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    if (typed.trim()) choose(typed.trim())
                  }
                }}
                placeholder="/path/to/project"
                spellCheck={false}
                className="h-8 min-w-0 flex-1 rounded-full border border-border bg-transparent px-3.5 font-mono text-[11px] outline-none transition-colors focus:border-foreground/60"
              />
              <button
                onClick={() => browse(typed || '')}
                className="h-8 shrink-0 rounded-full border border-border px-3.5 font-mono text-[11px] lowercase transition-colors hover:border-line hover:bg-raised"
              >
                {t('project.go')}
              </button>
            </div>
          </div>

          {/* Browser: current dir path, up/home nav, and subdirectories. */}
          <div className="flex items-center gap-1 border-b border-border px-2 py-1.5">
            <button
              onClick={() => browse('')}
              title={t('project.home')}
              className="grid size-6 place-items-center rounded-full transition-colors hover:bg-raised"
            >
              <House className="size-3.5 text-muted-foreground" />
            </button>
            <button
              onClick={() => parent && browse(parent)}
              disabled={!parent}
              title={t('project.up')}
              className="grid size-6 place-items-center rounded-full transition-colors hover:bg-raised disabled:opacity-40"
            >
              <ArrowUp className="size-3.5 text-muted-foreground" />
            </button>
            <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground" title={cwd}>
              {home && cwd.startsWith(home) ? '~' + cwd.slice(home.length) : cwd}
            </span>
          </div>

          <div className="max-h-56 overflow-y-auto p-1">
            {error ? (
              <div className="px-2 py-3 text-[11px] text-[var(--destructive)]">{error}</div>
            ) : shownEntries.length === 0 ? (
              <div className="px-2 py-3 text-[11px] text-muted-foreground">{t('project.empty')}</div>
            ) : (
              shownEntries.map((e) => (
                <div
                  key={e.path}
                  className="group flex items-center gap-1.5 rounded-[var(--radius-sm)] px-2 py-1 transition-colors hover:bg-raised"
                >
                  <button
                    onClick={() => browse(e.path)}
                    className="flex min-w-0 flex-1 items-center gap-1.5 text-left"
                  >
                    <FolderOpen className="size-3.5 shrink-0 text-muted-foreground" />
                    <span className="truncate text-xs">{e.name}</span>
                  </button>
                  <button
                    onClick={() => choose(e.path)}
                    className="hidden shrink-0 items-center gap-1 rounded-full border border-foreground/40 px-2 py-0.5 font-mono text-[10px] lowercase text-foreground focus-visible:flex group-hover:flex"
                  >
                    <Check className="size-3" />
                    {t('project.select')}
                  </button>
                </div>
              ))
            )}
          </div>

          {/* Select the currently-open directory itself as the project. */}
          <div className="border-t border-border p-2">
            <button
              onClick={() => cwd && choose(cwd)}
              className="tp-btn tp-btn-solid w-full rounded-full bg-primary px-3.5 py-1.5 font-mono text-xs lowercase text-primary-foreground transition-opacity hover:opacity-90"
            >
              {t('project.useThis')}
            </button>
          </div>
        </div>
      ) : null}
    </div>
  )
}
