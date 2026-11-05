import { CaretLeft, CaretRight } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { useI18n } from '@/lib/i18n'

/**
 * Always-visible page controls. Meant to live in a PageLayout footer so it
 * stays put while the list scrolls above it. Shows the current range and
 * prev/next; hidden entirely only when there is a single page of results.
 */
export function Pagination({
  offset,
  limit,
  total,
  onChange,
}: {
  offset: number
  limit: number
  total: number
  onChange: (offset: number) => void
}) {
  const { t } = useI18n()
  const from = total === 0 ? 0 : offset + 1
  const to = Math.min(offset + limit, total)
  const canPrev = offset > 0
  const canNext = offset + limit < total

  // Nothing to page through — a single short list needs no bar.
  if (total <= limit && offset === 0) return null

  return (
    <div className="flex items-center justify-between gap-3 font-mono text-[11px] text-muted-foreground">
      <span className="tabular-nums">
        {t('common.showingRange', { from, to, total })}
      </span>
      <div className="flex gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={!canPrev}
          onClick={() => onChange(Math.max(0, offset - limit))}
          className="gap-1 px-2.5"
        >
          <CaretLeft className="size-3.5" />
          <span className="hidden sm:inline">{t('common.previous')}</span>
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={!canNext}
          onClick={() => onChange(offset + limit)}
          className="gap-1 px-2.5"
        >
          <span className="hidden sm:inline">{t('common.next')}</span>
          <CaretRight className="size-3.5" />
        </Button>
      </div>
    </div>
  )
}
