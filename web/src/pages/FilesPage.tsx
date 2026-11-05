import { useEffect, useState } from 'react'
import {
  ArrowUUpLeft,
  CaretRight,
  DownloadSimple,
  File as FileIcon,
  FileArchive,
  FileAudio,
  FileCode,
  FileImage,
  FilePdf,
  FileText,
  FileVideo,
  Folder,
  House,
} from '@phosphor-icons/react'
import { authedUrl, downloadFile, get } from '@/lib/api'
import { useApi } from '@/lib/hooks'
import { formatBytes } from '@/lib/utils'
import { PageLayout } from '@/components/layout/PageLayout'
import { Pagination } from '@/components/ui/Pagination'
import { Button } from '@/components/ui/button'
import { Card, EmptyState } from '@/components/ui/primitives'
import { Skeleton, SkeletonList } from '@/components/ui/skeleton'
import { useI18n, useTimeAgo, type TFunc } from '@/lib/i18n'
import { usePageActions } from '@/components/layout/PageChrome'
import { SensitiveGate } from '@/components/ui/SensitiveGate'
import { cn } from '@/lib/utils'
import { Markdown } from '@/components/chat/Markdown'

interface Entry {
  name: string
  path: string
  is_dir: boolean
  size: number
  modified: string
}

type Kind =
  | 'dir'
  | 'image'
  | 'video'
  | 'audio'
  | 'pdf'
  | 'code'
  | 'text'
  | 'markdown'
  | 'archive'
  | 'binary'

const EXT: Record<string, Kind> = {}
const add = (kind: Kind, exts: string[]) => exts.forEach((e) => (EXT[e] = kind))
add('image', ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif'])
add('video', ['mp4', 'webm', 'mov', 'mkv', 'avi'])
add('audio', ['mp3', 'wav', 'ogg', 'flac', 'm4a'])
add('pdf', ['pdf'])
add('archive', ['zip', 'tar', 'gz', 'tgz', 'rar', '7z', 'bz2', 'xz'])
add('markdown', ['md', 'markdown', 'mdx'])
add('code', [
  'js', 'ts', 'tsx', 'jsx', 'go', 'py', 'rs', 'c', 'cpp', 'h', 'java', 'rb', 'php', 'sh',
  'json', 'yaml', 'yml', 'toml', 'html', 'css', 'scss', 'sql', 'lua', 'kt', 'swift', 'dart',
])
add('text', ['txt', 'log', 'csv', 'env', 'gitignore', 'ini', 'conf'])

function kindOf(entry: Entry): Kind {
  if (entry.is_dir) return 'dir'
  const ext = entry.name.split('.').pop()?.toLowerCase() ?? ''
  return EXT[ext] ?? 'binary'
}

function iconFor(kind: Kind) {
  const cls = 'size-4 shrink-0'
  switch (kind) {
    case 'dir':
      return <Folder className={cn(cls, 'text-foreground')} weight="fill" />
    case 'image':
      return <FileImage className={cn(cls, 'text-muted-foreground')} />
    case 'video':
      return <FileVideo className={cn(cls, 'text-muted-foreground')} />
    case 'audio':
      return <FileAudio className={cn(cls, 'text-muted-foreground')} />
    case 'pdf':
      return <FilePdf className={cn(cls, 'text-muted-foreground')} />
    case 'code':
      return <FileCode className={cn(cls, 'text-muted-foreground')} />
    case 'markdown':
      return <FileText className={cn(cls, 'text-muted-foreground')} />
    case 'text':
      return <FileText className={cn(cls, 'text-muted-foreground')} />
    case 'archive':
      return <FileArchive className={cn(cls, 'text-muted-foreground')} />
    default:
      return <FileIcon className={cn(cls, 'text-muted-foreground')} />
  }
}

interface Preview {
  entry: Entry
  kind: Kind
  content?: string
  binary?: boolean
  error?: string
}

export default function FilesPage() {
  const { t } = useI18n()
  const timeAgo = useTimeAgo()
  const [path, setPath] = useState('.')
  const [preview, setPreview] = useState<Preview | null>(null)
  const [loadingPreview, setLoadingPreview] = useState(false)
  const [offset, setOffset] = useState(0)
  const PAGE = 50
  const { data, loading } = useApi<{ path: string; parent: string; entries: Entry[] }>(
    `/files?path=${encodeURIComponent(path)}`,
    [path],
  )

  useEffect(() => setOffset(0), [path])

  const open = async (entry: Entry) => {
    if (entry.is_dir) {
      setPath(entry.path)
      setPreview(null)
      return
    }
    const kind = kindOf(entry)
    // Media renders straight from the raw endpoint — no need to fetch bytes
    // into JS. Text/code is fetched for inline display.
    if (kind === 'image' || kind === 'video' || kind === 'audio' || kind === 'pdf') {
      setPreview({ entry, kind })
      return
    }
    setLoadingPreview(true)
    setPreview({ entry, kind })
    try {
      const r = await get<{ content?: string; binary?: boolean }>(
        `/files/read?path=${encodeURIComponent(entry.path)}`,
      )
      if (r.binary) setPreview({ entry, kind: 'binary', binary: true })
      else setPreview({ entry, kind, content: r.content ?? '' })
    } catch (e) {
      setPreview({ entry, kind, error: t('files.cannotRead', { error: (e as Error).message }) })
    } finally {
      setLoadingPreview(false)
    }
  }

  usePageActions(
    <>
      <Button variant="outline" size="sm" onClick={() => setPath('.')} aria-label={t('files.home')}>
        <House className="size-4" />
      </Button>
      {data?.parent != null && data.path !== '.' ? (
        <Button variant="outline" size="sm" onClick={() => setPath(data.parent)} className="gap-1.5">
          <ArrowUUpLeft className="size-4" />
          {t('files.up')}
        </Button>
      ) : null}
    </>,
    [data?.parent, data?.path, t],
  )

  const entries = data?.entries ?? []
  const paged = entries.slice(offset, offset + PAGE)

  const crumbs = buildCrumbs(data?.path ?? path)

  const header = (
    <nav className="flex items-center gap-1 overflow-x-auto text-xs">
      <button
        onClick={() => setPath('.')}
        className="inline-flex shrink-0 items-center gap-1 rounded-full px-2.5 py-1 font-medium text-muted-foreground transition-colors hover:bg-raised hover:text-foreground"
      >
        <House className="size-3.5" />
        {t('files.workspace')}
      </button>
      {crumbs.map((c) => (
        <span key={c.path} className="inline-flex shrink-0 items-center gap-1">
          <CaretRight className="size-3 text-dim" />
          <button
            onClick={() => setPath(c.path)}
            className="rounded-full px-2.5 py-1 font-mono text-muted-foreground transition-colors hover:bg-raised hover:text-foreground"
          >
            {c.name}
          </button>
        </span>
      ))}
    </nav>
  )

  return (
    // On desktop the list and the preview each scroll on their own, so
    // reading a long file never carries the list away (and vice versa).
    <PageLayout
      header={header}
      bodyClassName="lg:flex lg:flex-col lg:overflow-hidden lg:pr-0"
      footer={
        entries.length > PAGE ? (
          <Pagination offset={offset} limit={PAGE} total={entries.length} onChange={setOffset} />
        ) : undefined
      }
    >
      <SensitiveGate>
      <div className="grid gap-3 lg:min-h-0 lg:flex-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
        {/* file list */}
        <div className="min-w-0 lg:min-h-0 lg:overflow-y-auto lg:pr-1">
          {loading && !data ? (
            <SkeletonList count={8} />
          ) : entries.length === 0 ? (
            <EmptyState icon={<Folder className="size-8" />} title={t('files.emptyFolder')} />
          ) : (
            <Card className="overflow-hidden">
              {paged.map((e) => {
                const kind = kindOf(e)
                const active = preview?.entry.path === e.path
                return (
                  <button
                    key={e.path}
                    onClick={() => open(e)}
                    aria-current={active ? 'true' : undefined}
                    className={cn(
                      'flex w-full items-center gap-3 border-t border-border px-4 py-2.5 text-left transition-colors duration-200 first:border-t-0 hover:bg-raised',
                      active && 'bg-nav-active',
                    )}
                  >
                    {iconFor(kind)}
                    <span className={cn('min-w-0 flex-1 truncate text-sm', !e.is_dir && 'font-mono text-[13px]')}>{e.name}</span>
                    {!e.is_dir ? (
                      <span className="shrink-0 font-mono tabular-nums text-[11px] text-muted-foreground">
                        {formatBytes(e.size)}
                      </span>
                    ) : (
                      <CaretRight className="size-3.5 shrink-0 text-dim" />
                    )}
                    <span className="hidden w-20 shrink-0 text-right font-mono text-[11px] text-dim sm:inline">
                      {timeAgo(e.modified)}
                    </span>
                  </button>
                )
              })}
            </Card>
          )}
        </div>

        {/* preview pane */}
        <div className="min-w-0 lg:flex lg:min-h-0 lg:flex-col">
          {preview ? (
            <PreviewPane
              preview={preview}
              loading={loadingPreview}
              onClose={() => setPreview(null)}
              t={t}
            />
          ) : (
            <div className="hidden h-full items-center justify-center rounded-[var(--radius-lg)] border border-dashed border-line text-center lg:flex">
              <div className="p-6 text-xs text-muted-foreground">
                <FileText className="mx-auto mb-2 size-7 opacity-50" />
                {t('files.selectHint')}
              </div>
            </div>
          )}
        </div>
      </div>
      </SensitiveGate>
    </PageLayout>
  )
}

function PreviewPane({
  preview,
  loading,
  onClose,
  t,
}: {
  preview: Preview
  loading: boolean
  onClose: () => void
  t: TFunc
}) {
  const { entry, kind } = preview
  const rawUrl = authedUrl(`/files/raw?path=${encodeURIComponent(entry.path)}`)
  const [view, setView] = useState<'rendered' | 'raw'>('rendered')

  // A new file resets to the rendered view.
  useEffect(() => setView('rendered'), [entry.path])

  return (
    <Card className="flex max-h-[calc(100vh-12rem)] flex-col overflow-hidden lg:max-h-full lg:min-h-0">
      <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
        {iconFor(kind)}
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{entry.name}</span>
        {kind === 'markdown' && !preview.binary && !preview.error ? (
          <div className="flex shrink-0 rounded-full border border-border p-0.5">
            {(['rendered', 'raw'] as const).map((v) => (
              <button
                key={v}
                onClick={() => setView(v)}
                className={cn(
                  'rounded-full px-2.5 py-0.5 text-[11px] transition-colors',
                  view === v
                    ? 'bg-nav-active text-foreground'
                    : 'text-muted-foreground hover:text-foreground',
                )}
              >
                {t(v === 'rendered' ? 'files.rendered' : 'files.raw')}
              </button>
            ))}
          </div>
        ) : (
          <span className="shrink-0 font-mono tabular-nums text-[11px] text-muted-foreground">
            {formatBytes(entry.size)}
          </span>
        )}
        <button
          onClick={() => void downloadFile(`/files/raw?path=${encodeURIComponent(entry.path)}&download=1`, entry.name)}
          className="shrink-0 rounded-full p-1 text-muted-foreground transition-colors hover:bg-raised hover:text-foreground"
          aria-label={t('files.download')}
          title={t('files.download')}
        >
          <DownloadSimple className="size-4" />
        </button>
        <Button variant="ghost" size="sm" onClick={onClose}>
          {t('files.close')}
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-auto">
        {loading ? (
          <Skeleton className="m-3 h-64" />
        ) : preview.error ? (
          <p className="m-rise m-4 rounded-[var(--radius-lg)] border border-[color-mix(in_oklch,var(--destructive)_45%,var(--border))] px-4 py-3 text-sm text-destructive">{preview.error}</p>
        ) : kind === 'image' ? (
          <div className="flex items-center justify-center bg-[repeating-conic-gradient(var(--muted)_0_25%,transparent_0_50%)] bg-[length:16px_16px] p-4">
            <img src={rawUrl} alt={entry.name} className="max-h-full max-w-full rounded-[var(--radius-md)] object-contain" />
          </div>
        ) : kind === 'video' ? (
          <video src={rawUrl} controls className="max-h-[70vh] w-full rounded-[var(--radius-md)] bg-black" />
        ) : kind === 'audio' ? (
          <div className="p-4">
            <audio src={rawUrl} controls className="w-full" />
          </div>
        ) : kind === 'pdf' ? (
          <iframe src={rawUrl} title={entry.name} className="h-[70vh] w-full border-0" />
        ) : preview.binary ? (
          <div className="flex flex-col items-center gap-3 p-8 text-center">
            <FileIcon className="size-8 text-muted-foreground" />
            <p className="text-xs text-muted-foreground">{t('files.binaryHint')}</p>
            <Button
              size="sm"
              variant="outline"
              onClick={() => void downloadFile(`/files/raw?path=${encodeURIComponent(entry.path)}&download=1`, entry.name)}
              className="gap-1.5"
            >
              <DownloadSimple className="size-4" />
              {t('files.download')}
            </Button>
          </div>
        ) : kind === 'markdown' && view === 'rendered' ? (
          <Markdown content={preview.content ?? ''} className="p-4 text-sm" />
        ) : (
          <pre className="p-4 font-mono text-[11px] leading-relaxed">{preview.content}</pre>
        )}
      </div>
    </Card>
  )
}

// buildCrumbs turns "a/b/c" into clickable [{name,path}] segments.
function buildCrumbs(p: string): { name: string; path: string }[] {
  if (!p || p === '.' || p === '/') return []
  const parts = p.split('/').filter((s) => s && s !== '.')
  const out: { name: string; path: string }[] = []
  let acc = ''
  for (const part of parts) {
    acc = acc ? `${acc}/${part}` : part
    out.push({ name: part, path: acc })
  }
  return out
}
