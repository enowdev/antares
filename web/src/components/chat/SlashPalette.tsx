import { useEffect, useMemo, useState } from 'react'
import { get } from '@/lib/api'
import { cn } from '@/lib/utils'

export interface CommandSpec {
  name: string
  summary: string
  args?: string
  client?: boolean
}

/**
 * The command catalogue, fetched once per page. It is small and changes only
 * when the binary does, so refetching per keystroke would be pure noise.
 */
export function useCommands(): CommandSpec[] {
  const [list, setList] = useState<CommandSpec[]>([])
  useEffect(() => {
    get<{ commands: CommandSpec[] }>('/commands?surface=web')
      .then((r) => setList(r.commands ?? []))
      // A missing catalogue only costs the palette; typing still works.
      .catch(() => setList([]))
  }, [])
  return list
}

/**
 * Which commands match what is being typed. Returns nothing unless the buffer
 * is a single line that starts with "/" and has no argument yet — once someone
 * is typing arguments, suggestions are in the way.
 */
export function useMatches(input: string, commands: CommandSpec[]): CommandSpec[] {
  return useMemo(() => {
    if (!input.startsWith('/') || input.includes('\n') || input.includes(' ')) return []
    const word = input.slice(1).toLowerCase()
    return commands.filter((c) => c.name.startsWith(word))
  }, [input, commands])
}

/**
 * The completion list, anchored above the composer. It is a listbox rather than
 * a set of buttons so a keyboard can drive it without the focus leaving the
 * textarea.
 */
export function SlashPalette({
  matches,
  selected,
  onPick,
}: {
  matches: CommandSpec[]
  selected: number
  onPick: (c: CommandSpec) => void
}) {
  if (matches.length === 0) return null
  return (
    <div
      role="listbox"
      aria-label="Commands"
      className="m-open mb-2 max-h-64 overflow-y-auto rounded-[var(--radius-lg)] border border-border bg-popover p-1 shadow-[0_10px_28px_-14px_#00000080]"
    >
      {matches.map((c, i) => (
        <button
          key={c.name}
          role="option"
          aria-selected={i === selected}
          // Pointer-down rather than click: the textarea must not lose focus
          // between the two events, or the composer height jumps.
          onMouseDown={(e) => {
            e.preventDefault()
            onPick(c)
          }}
          className={cn(
            'flex w-full items-baseline gap-2 rounded-[var(--radius-sm)] px-2.5 py-1.5 text-left transition-colors',
            i === selected ? 'bg-nav-active text-foreground' : 'hover:bg-raised',
          )}
        >
          <span className="shrink-0 font-mono text-xs text-foreground">
            /{c.name}
            {c.args ? <span className="text-muted-foreground"> {c.args}</span> : null}
          </span>
          <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">
            {c.summary}
          </span>
        </button>
      ))}
    </div>
  )
}
