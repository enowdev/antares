import { useMemo, useState } from 'react'
import { Check, Copy, Image as ImageIcon } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { copyText } from '@/lib/clipboard'
import { useI18n } from '@/lib/i18n'
import { getToken } from '@/lib/api'

/**
 * A small, dependency-free Markdown renderer covering what agent replies
 * actually use: fenced code, headings, lists, tables, quotes, images, and inline marks.
 */
export function Markdown({ content, className }: { content: string; className?: string }) {
  const blocks = useMemo(() => parseBlocks(content), [content])
  return (
    <div className={cn('space-y-2.5', className)}>
      {blocks.map((block, i) =>
        block.type === 'code' ? (
          <CodeBlock key={i} code={block.code} lang={block.lang} />
        ) : (
          <RichBlock key={i} block={block} />
        ),
      )}
    </div>
  )
}

type Block =
  | { type: 'code'; code: string; lang: string }
  | { type: 'heading'; level: number; text: string }
  | { type: 'list'; ordered: boolean; items: string[] }
  | { type: 'quote'; text: string }
  | { type: 'table'; header: string[]; rows: string[][] }
  | { type: 'rule' }
  | { type: 'image'; alt: string; url: string }
  | { type: 'paragraph'; text: string }

function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n/g, '\n').split('\n')
  const blocks: Block[] = []
  let i = 0

  while (i < lines.length) {
    const line = lines[i]

    // Fenced code
    const fence = /^\s*```(\S*)\s*$/.exec(line)
    if (fence) {
      const lang = fence[1] ?? ''
      const body: string[] = []
      i++
      while (i < lines.length && !/^\s*```\s*$/.test(lines[i])) {
        body.push(lines[i])
        i++
      }
      i++ // closing fence
      blocks.push({ type: 'code', code: body.join('\n'), lang })
      continue
    }

    if (!line.trim()) {
      i++
      continue
    }

    // Standalone image: ![alt](url)
    const imgBlock = /^\s*!\[([^\]]*)\]\(([^)]+)\)\s*$/.exec(line)
    if (imgBlock) {
      blocks.push({ type: 'image', alt: imgBlock[1], url: imgBlock[2] })
      i++
      continue
    }

    if (/^\s*(---|\*\*\*|___)\s*$/.test(line)) {
      blocks.push({ type: 'rule' })
      i++
      continue
    }

    const heading = /^(#{1,6})\s+(.*)$/.exec(line)
    if (heading) {
      blocks.push({ type: 'heading', level: heading[1].length, text: heading[2] })
      i++
      continue
    }

    // Table: header row followed by a separator row
    if (line.includes('|') && i + 1 < lines.length && /^\s*\|?[\s:|-]+\|[\s:|-]*$/.test(lines[i + 1])) {
      const header = splitRow(line)
      i += 2
      const rows: string[][] = []
      while (i < lines.length && lines[i].includes('|') && lines[i].trim()) {
        rows.push(splitRow(lines[i]))
        i++
      }
      blocks.push({ type: 'table', header, rows })
      continue
    }

    if (/^\s*>/.test(line)) {
      const body: string[] = []
      while (i < lines.length && /^\s*>/.test(lines[i])) {
        body.push(lines[i].replace(/^\s*>\s?/, ''))
        i++
      }
      blocks.push({ type: 'quote', text: body.join('\n') })
      continue
    }

    const bullet = /^\s*([-*+]|\d+[.)])\s+/.exec(line)
    if (bullet) {
      const ordered = /\d/.test(bullet[1])
      const items: string[] = []
      while (i < lines.length) {
        const m = /^\s*([-*+]|\d+[.)])\s+(.*)$/.exec(lines[i])
        if (!m) break
        items.push(m[2])
        i++
      }
      blocks.push({ type: 'list', ordered, items })
      continue
    }

    const para: string[] = []
    while (i < lines.length && lines[i].trim() && !/^\s*(```|#{1,6}\s|>|[-*+]\s|\d+[.)]\s|!\[)/.test(lines[i])) {
      para.push(lines[i])
      i++
    }
    blocks.push({ type: 'paragraph', text: para.join('\n') })
  }

  return blocks
}

function splitRow(line: string): string[] {
  return line
    .replace(/^\s*\|/, '')
    .replace(/\|\s*$/, '')
    .split('|')
    .map((c) => c.trim())
}

function resolveImageUrl(url: string): string {
  // Relative paths like /api/files/raw?path=... are already correct.
  // Data URIs are already valid.
  // Absolute URLs are already correct.
  // Bare file paths get prefixed with /api/.
  if (url.startsWith('http') || url.startsWith('data:') || url.startsWith('blob:')) {
    return url
  }
  if (url.startsWith('/api/')) {
    // Append auth token for API image endpoints (files/raw, social/image).
    const token = getToken()
    if (token) {
      const sep = url.includes('?') ? '&' : '?'
      return url + sep + 'token=' + encodeURIComponent(token)
    }
    return url
  }
  if (url.startsWith('/')) return url
  return '/api/files/raw?path=' + encodeURIComponent(url)
}

function EmbeddedImage({ alt, url }: { alt: string; url: string }) {
  const [expanded, setExpanded] = useState(false)
  const [failed, setFailed] = useState(false)
  const src = resolveImageUrl(url)

  if (failed) {
    return (
      <span className="inline-flex items-center gap-1 border border-border px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
        <ImageIcon className="size-3" /> {alt || 'image'}
      </span>
    )
  }

  return (
    <img
      src={src}
      alt={alt}
      loading="lazy"
      onClick={() => setExpanded(!expanded)}
      onError={() => setFailed(true)}
      className={cn(
        'cursor-zoom-in border border-border object-contain transition-all',
        expanded ? 'max-w-full' : 'max-h-48 max-w-xs',
      )}
    />
  )
}

function RichBlock({ block }: { block: Block }) {
  switch (block.type) {
    case 'heading': {
      const sizes = ['text-lg', 'text-base', 'text-sm', 'text-sm', 'text-sm', 'text-sm']
      return (
        <p className={cn('font-medium tracking-tight', sizes[block.level - 1])}>
          <Inline text={block.text} />
        </p>
      )
    }
    case 'list':
      return block.ordered ? (
        <ol className="list-decimal space-y-1 pl-5">
          {block.items.map((it, i) => (
            <li key={i}>
              <Inline text={it} />
            </li>
          ))}
        </ol>
      ) : (
        <ul className="list-disc space-y-1 pl-5">
          {block.items.map((it, i) => (
            <li key={i}>
              <Inline text={it} />
            </li>
          ))}
        </ul>
      )
    case 'quote':
      return (
        <blockquote className="border-l-2 border-foreground/40 pl-3 text-muted-foreground">
          <Inline text={block.text} />
        </blockquote>
      )
    case 'rule':
      return <hr className="border-line" />
    case 'image':
      return (
        <div className="py-1">
          <EmbeddedImage alt={block.alt} url={block.url} />
        </div>
      )
    case 'table':
      return (
        <div className="overflow-x-auto border border-border bg-card">
          <table className="w-full text-xs">
            <thead>
              <tr>
                {block.header.map((h, i) => (
                  <th key={i} className="border-b border-border px-3 py-2 text-left font-mono text-[11px] font-normal lowercase tracking-[0.04em] whitespace-nowrap text-dim">
                    <Inline text={h} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {block.rows.map((row, r) => (
                <tr key={r} className="border-t border-border first:border-t-0 hover:bg-raised">
                  {row.map((c, i) => (
                    <td key={i} className="px-3 py-2 align-top">
                      <Inline text={c} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )
    case 'paragraph':
      return (
        <p className="whitespace-pre-wrap break-words">
          <Inline text={block.text} />
        </p>
      )
    default:
      return null
  }
}

/** Renders inline code, bold, italics, links, and inline images. */
function Inline({ text }: { text: string }) {
  const nodes: React.ReactNode[] = []
  const pattern = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*]+\*)|(!\[[^\]]*\]\([^)]+\))|(\[[^\]]+\]\([^)]+\))|(https?:\/\/\S+)/g
  let last = 0
  let m: RegExpExecArray | null
  let key = 0

  while ((m = pattern.exec(text)) !== null) {
    if (m.index > last) nodes.push(text.slice(last, m.index))
    const token = m[0]
    if (token.startsWith('`')) {
      nodes.push(
        <code
          key={key++}
          className="bg-raised px-1 py-0.5 font-mono text-[0.85em] text-foreground"
        >
          {token.slice(1, -1)}
        </code>,
      )
    } else if (token.startsWith('![')) {
      // Inline image: ![alt](url)
      const imgMatch = /^!\[([^\]]*)\]\(([^)]+)\)$/.exec(token)!
      nodes.push(
        <EmbeddedImage key={key++} alt={imgMatch[1]} url={imgMatch[2]} />,
      )
    } else if (token.startsWith('**')) {
      nodes.push(<strong key={key++}>{token.slice(2, -2)}</strong>)
    } else if (token.startsWith('[')) {
      const link = /\[([^\]]+)\]\(([^)]+)\)/.exec(token)!
      nodes.push(
        <a
          key={key++}
          href={link[2]}
          target="_blank"
          rel="noreferrer noopener"
          className="text-foreground underline decoration-line underline-offset-4 transition-colors hover:decoration-foreground"
        >
          <Inline text={link[1]} />
        </a>,
      )
    } else if (token.startsWith('http')) {
      // Bare URL — check if it looks like an image URL
      if (/\.(jpg|jpeg|png|gif|webp|svg|avif)(\?|$)/i.test(token)) {
        nodes.push(<EmbeddedImage key={key++} alt="" url={token} />)
      } else {
        nodes.push(
          <a
            key={key++}
            href={token}
            target="_blank"
            rel="noreferrer noopener"
            className="break-all text-foreground underline decoration-line underline-offset-4 transition-colors hover:decoration-foreground"
          >
            {token}
          </a>,
        )
      }
    } else {
      nodes.push(<em key={key++}>{token.slice(1, -1)}</em>)
    }
    last = m.index + token.length
  }
  if (last < text.length) nodes.push(text.slice(last))
  return <>{nodes}</>
}

function CodeBlock({ code, lang }: { code: string; lang: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    if (await copyText(code)) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }
  return (
    <div className="group relative overflow-hidden border border-border bg-card">
      <div className="flex items-center justify-between border-b border-border px-3 py-1.5">
        <span className="font-mono text-[10px] lowercase tracking-[0.04em] text-dim">
          {lang || t('chat.codeText')}
        </span>
        <button
          onClick={copy}
          className="text-muted-foreground transition-colors hover:text-foreground"
          aria-label={t('chat.copyCode')}
        >
          {copied ? <Check className="size-3.5 text-[var(--success)]" /> : <Copy className="size-3.5" />}
        </button>
      </div>
      <pre className="overflow-x-auto p-3">
        <code className="font-mono text-xs leading-relaxed">{code}</code>
      </pre>
    </div>
  )
}
