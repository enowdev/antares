import { memo, useMemo, useState } from 'react'
import {
  CaretDown,
  CaretRight,
  CircleNotch,
  Database,
  FilePlus,
  FileText,
  Globe,
  Image,
  ListChecks,
  MagnifyingGlass,
  PencilSimple,
  Terminal,
  Wrench,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import type { ToolCallView } from '@/lib/chatTranscript'
import { useI18n } from '@/lib/i18n'
import { isIncompleteToolCall } from '@/lib/toolCallState'

type IconType = React.ComponentType<{ className?: string; weight?: 'regular' | 'fill' }>
type Meta = { label: string; Icon: IconType }

// Verb + icon per tool, so a call reads as an action ("edit foo.ts") rather than
// a raw function name. The verb is implied by the icon/diff, mirroring how a
// clean IDE chat presents its tools.
const TOOL_META: Record<string, Meta> = {
  read_file: { label: 'read', Icon: FileText },
  write_file: { label: 'create', Icon: FilePlus },
  edit_file: { label: 'edit', Icon: PencilSimple },
  list_files: { label: 'list', Icon: FileText },
  glob: { label: 'find', Icon: MagnifyingGlass },
  grep: { label: 'search', Icon: MagnifyingGlass },
  terminal: { label: 'run', Icon: Terminal },
  web_search: { label: 'search', Icon: Globe },
  web_fetch: { label: 'fetch', Icon: Globe },
  memory: { label: 'memory', Icon: Database },
  rag_search: { label: 'recall', Icon: Database },
  rag_index: { label: 'index', Icon: Database },
  session_search: { label: 'search', Icon: MagnifyingGlass },
  todo: { label: 'tasks', Icon: ListChecks },
  view_image: { label: 'view image', Icon: Image },
}

type DiffRow = { type: 'ctx' | 'add' | 'del'; text: string }
type Diff = { rows: DiffRow[]; added: number; removed: number }

// Line-based diff (LCS) between old and new source, for the expanded edit view.
function lineDiff(oldText: string, newText: string): Diff {
  if (!oldText && !newText) return { rows: [], added: 0, removed: 0 }
  const a = oldText.replace(/\n$/, '').split('\n')
  const b = newText.replace(/\n$/, '').split('\n')
  if (!oldText) {
    return { rows: b.map((text) => ({ type: 'add', text })), added: b.length, removed: 0 }
  }
  const n = a.length
  const m = b.length
  const dp = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const rows: DiffRow[] = []
  let i = 0
  let j = 0
  let added = 0
  let removed = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      rows.push({ type: 'ctx', text: a[i] })
      i++
      j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      rows.push({ type: 'del', text: a[i] })
      i++
      removed++
    } else {
      rows.push({ type: 'add', text: b[j] })
      j++
      added++
    }
  }
  while (i < n) {
    rows.push({ type: 'del', text: a[i] })
    i++
    removed++
  }
  while (j < m) {
    rows.push({ type: 'add', text: b[j] })
    j++
    added++
  }
  return { rows, added, removed }
}

/** One-line human summary of a tool call, derived from its arguments. */
function summarize(name: string, args: Record<string, unknown>): string {
  const s = (k: string) => (typeof args[k] === 'string' ? (args[k] as string) : '')
  switch (name) {
    case 'terminal':
      return s('command')
    case 'grep':
    case 'glob':
      return s('pattern')
    case 'web_search':
    case 'rag_search':
    case 'session_search':
      return s('query')
    case 'web_fetch':
      return s('url')
    case 'view_image': {
      const p = s('path')
      return p ? p.split('/').pop() || p : ''
    }
    case 'memory':
      return s('action') + (s('key') ? `: ${s('key')}` : '')
    case 'todo':
      return s('action')
    default:
      return Object.entries(args)
        .slice(0, 2)
        .map(([k, v]) => `${k}=${String(v).slice(0, 40)}`)
        .join(' ')
  }
}

// Memoised on the call object. During a streaming turn every tool call lives on
// ONE assistant message, so each new call replaces that message and re-renders
// the whole bubble. Without this, all N already-finished cards re-render on
// every one of the N calls — O(N²) work (and JSON.parse + summarize each time)
// on a single message that a runaway loop can grow to hundreds of segments,
// which is what pushed the tab to an out-of-memory crash. Finished calls keep a
// stable `call` reference, so memo skips them and only the live card re-renders.
export const ToolCallCard = memo(function ToolCallCard({ call }: { call: ToolCallView }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)

  const args = useMemo<Record<string, unknown>>(() => {
    try {
      return JSON.parse(call.args || '{}') as Record<string, unknown>
    } catch {
      return {}
    }
  }, [call.args])

  const meta = TOOL_META[call.name] ?? { label: call.name, Icon: Wrench }
  const isEdit = call.name === 'edit_file'
  const isCreate = call.name === 'write_file'
  const isDiff = isEdit || isCreate
  const isRead = call.name === 'read_file'
  const output = call.result ?? call.progress ?? ''
  const incomplete = isIncompleteToolCall(call)

  // Only the file tools render as filename-over-directory. Other tools may also
  // carry a `path` arg (e.g. view_image with a URL), but for them that is just
  // an argument — they get the name-header + summary layout like everything else.
  const FILE_TOOLS = new Set(['read_file', 'write_file', 'edit_file', 'list_files'])
  const path = FILE_TOOLS.has(call.name) && typeof args.path === 'string' ? (args.path as string) : ''
  const fileName = path ? path.split('/').pop() || path : ''
  const parentPath = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''
  const summary = path ? '' : summarize(call.name, args)

  const diff = useMemo<Diff>(() => {
    if (isEdit) return lineDiff(String(args.old_string ?? ''), String(args.new_string ?? ''))
    if (isCreate) return lineDiff('', String(args.content ?? ''))
    return { rows: [], added: 0, removed: 0 }
  }, [isEdit, isCreate, args.old_string, args.new_string, args.content])

  // Lines read, for the collapsed read summary.
  const readLines = isRead && output.trim() ? output.trim().split('\n').length : 0

  const canExpand = call.isError
    ? !!output
    : incomplete
      ? true
      : isDiff
        ? diff.rows.length > 0
        : (call.args && call.args !== '{}') || !!output

  // Right-side status: a diff tally for edits, "N lines" for reads, a spinner
  // while running, else a short result echo.
  const right = call.isError ? (
    <span className="flex max-w-48 items-center gap-1.5 font-mono text-[10px] text-destructive" title={output}>
      <span className="size-1.5 shrink-0 rounded-full bg-destructive" />
      <span className="truncate">{output.trim().split('\n')[0] || t('chat.toolError')}</span>
    </span>
  ) : incomplete ? (
    <span className="flex items-center gap-1.5 font-mono text-[10px] text-[var(--warning)]" title={t('chat.toolIncomplete')}>
      <span className="size-1.5 shrink-0 rounded-full bg-[var(--warning)]" />
      {t('chat.toolIncomplete')}
    </span>
  ) : isDiff ? (
    <span className="flex items-center gap-1.5 font-mono text-[10px] tabular-nums">
      {call.running ? <CircleNotch className="size-3 animate-spin text-muted-foreground" /> : null}
      {diff.added > 0 ? <span className="text-[var(--success)]">+{diff.added}</span> : null}
      {diff.removed > 0 ? <span className="text-destructive">-{diff.removed}</span> : null}
      {!call.running && diff.added === 0 && diff.removed === 0 ? (
        <span className="size-1.5 rounded-full bg-[var(--success)]" />
      ) : null}
    </span>
  ) : call.running ? (
    <CircleNotch className="size-3.5 animate-spin text-muted-foreground" />
  ) : isRead && readLines ? (
    <span className="font-mono text-[10px] tabular-nums text-dim">{t('chat.nLines', { n: readLines })}</span>
  ) : call.result !== undefined ? (
    output ? (
      <span className="block max-w-64 truncate font-mono text-[10px] text-dim">
        {output.trim().split('\n')[0]}
      </span>
    ) : (
      <span className="mt-1 block size-1.5 rounded-full bg-[var(--success)]" />
    )
  ) : null

  return (
    <div
      className={cn(
        'overflow-hidden rounded-[var(--radius-md)] border bg-card transition-[border-color] duration-200',
        call.isError
          ? 'border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))]'
          : incomplete
            ? 'border-[color-mix(in_oklch,var(--warning)_45%,var(--border))]'
            : 'border-border hover:border-line',
      )}
    >
      <button
        type="button"
        aria-expanded={canExpand ? open : undefined}
        onClick={() => canExpand && setOpen((v) => !v)}
        className={cn(
          'flex w-full items-start gap-2 px-3 py-2 text-left transition-colors duration-200',
          canExpand ? 'cursor-pointer hover:bg-raised' : 'cursor-default',
        )}
      >
        <span className="mt-0.5 shrink-0 text-muted-foreground">
          {canExpand ? (
            open ? (
              <CaretDown className="size-3.5" />
            ) : (
              <CaretRight className="size-3.5" />
            )
          ) : (
            <meta.Icon className="size-3.5" />
          )}
        </span>

        {path ? (
          <span className="flex min-w-0 flex-1 flex-col leading-tight" title={path}>
            <span
              className={cn(
                'truncate font-mono text-[11px]',
                'text-foreground',
                isDiff && 'underline decoration-line underline-offset-4',
              )}
            >
              {fileName}
            </span>
            {parentPath ? (
              <span className="truncate font-mono text-[10px] text-dim">
                {parentPath}/
              </span>
            ) : null}
          </span>
        ) : (
          // Name as a header, with the argument summary on its own line beneath
          // — so a long summary (e.g. a fetched URL or a notice) never crowds
          // the name or gets squeezed on one line.
          <span className="flex min-w-0 flex-1 flex-col gap-0.5 leading-tight">
            <span className="truncate font-mono text-[11px] lowercase text-foreground">
              {meta.label}
            </span>
            {summary ? (
              <span className="truncate font-mono text-[10px] text-muted-foreground">
                {summary}
              </span>
            ) : null}
          </span>
        )}

        <span className="mt-0.5 shrink-0">{right}</span>
      </button>

      {open && canExpand ? (
        isDiff && !call.isError ? (
          <div className="m-fade border-t border-border bg-background/40">
            <div className="max-h-64 overflow-auto py-1 font-mono text-[11px] leading-[1.5]">
              {diff.rows.map((r, ri) => (
                <div
                  key={ri}
                  className={cn(
                    'flex gap-2 px-3',
                    r.type === 'add'
                      ? 'bg-[color-mix(in_oklch,var(--success)_12%,transparent)] text-[var(--success)]'
                      : r.type === 'del'
                        ? 'bg-destructive/10 text-destructive'
                        : 'text-muted-foreground/70',
                  )}
                >
                  <span
                    className={cn(
                      'select-none',
                      r.type === 'add'
                        ? 'text-[var(--success)]'
                        : r.type === 'del'
                          ? 'text-destructive'
                          : 'text-transparent',
                    )}
                  >
                    {r.type === 'add' ? '+' : r.type === 'del' ? '-' : ' '}
                  </span>
                  <span className="whitespace-pre">{r.text || ' '}</span>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <div className="m-fade space-y-2.5 border-t border-border px-3 py-2.5">
            {call.args && call.args !== '{}' ? (
              <div>
                <p className="mb-1 font-mono text-[10px] lowercase tracking-[0.04em] text-dim">
                  {t('chat.toolArgs')}
                </p>
                <pre className="max-h-48 overflow-auto rounded-[var(--radius-sm)] border border-border bg-background/40 p-2 font-mono text-[11px]">
                  {prettyJSON(call.args)}
                </pre>
              </div>
            ) : null}
            {output || incomplete ? (
              <div>
                <p className="mb-1 font-mono text-[10px] lowercase tracking-[0.04em] text-dim">
                  {t('chat.toolResult')}
                </p>
                <pre
                  className={cn(
                    'max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-[var(--radius-sm)] border border-border bg-background/40 p-2 font-mono text-[11px]',
                    call.isError && 'text-destructive',
                    incomplete && 'text-[var(--warning)]',
                  )}
                >
                  {output || t('chat.toolIncompleteDetail')}
                </pre>
              </div>
            ) : null}
          </div>
        )
      ) : null}
    </div>
  )
})

function prettyJSON(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}
