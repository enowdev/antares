import { useMemo, useState } from 'react'
import { Question, ArrowLeft, ArrowRight, Check } from '@phosphor-icons/react'
import { useI18n } from '@/lib/i18n'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/primitives'
import { Markdown } from '@/components/chat/Markdown'
import type { ToolCallView } from '@/lib/chatTranscript'

interface AskQuestion {
  question: string
  header?: string
  options?: string[]
  multiSelect?: boolean
}

/** Accept both the single-question form and the questions[] array. */
function parseQuestions(raw: string): AskQuestion[] {
  try {
    const a = JSON.parse(raw) as {
      question?: string
      options?: string[]
      questions?: AskQuestion[]
    }
    if (Array.isArray(a.questions) && a.questions.length > 0) {
      return a.questions.filter((q) => q && q.question)
    }
    if (a.question) return [{ question: a.question, options: a.options }]
  } catch {
    /* fall through */
  }
  return []
}

/**
 * The agent called ask_user: it needs decisions only the person can make and
 * has stopped until they reply. Renders the questions one at a time with
 * next/previous and clickable options (plus free-text), then submits every
 * answer as one message so the run continues in a single turn.
 */
export function AskUserCard({
  call,
  disabled,
  onAnswer,
}: {
  call: ToolCallView
  disabled?: boolean
  onAnswer: (text: string) => void
}) {
  const { t } = useI18n()
  const questions = useMemo(() => parseQuestions(call.args), [call.args])
  const [idx, setIdx] = useState(0)
  const [answers, setAnswers] = useState<Record<number, string[]>>({})
  const [custom, setCustom] = useState('')
  const [submitted, setSubmitted] = useState(false)

  if (questions.length === 0) return null

  const q = questions[idx]
  const isLast = idx === questions.length - 1
  const multi = !!q.multiSelect
  const selected = answers[idx] ?? []

  const setSelected = (vals: string[]) => setAnswers((a) => ({ ...a, [idx]: vals }))

  const toggleOption = (o: string) => {
    if (multi) {
      setSelected(selected.includes(o) ? selected.filter((x) => x !== o) : [...selected, o])
    } else {
      setSelected([o])
      if (questions.length === 1) submit({ 0: [o] })
    }
  }

  // Commit the free-text input into the current question's answers.
  // Returns the updated answers record.
  const commitCustom = (base: typeof answers): typeof answers => {
    const v = custom.trim()
    if (!v) return base
    const cur = base[idx] ?? []
    if (multi) {
      return { ...base, [idx]: [...cur.filter((x) => x !== v), v] }
    }
    return { ...base, [idx]: [v] }
  }

  // Next button: attach text first, then advance to the next question.
  const handleNext = () => {
    const updated = commitCustom(answers)
    setAnswers(updated)
    setCustom('')
    setIdx((i) => Math.min(questions.length - 1, i + 1))
  }

  // Submit button: attach text first, then submit all answers.
  const handleSubmit = () => {
    const updated = commitCustom(answers)
    setAnswers(updated)
    setCustom('')
    submit(updated)
  }

  const answeredCount = questions.filter((_, i) => (answers[i]?.length ?? 0) > 0).length

  const submit = (final?: Record<number, string[]>) => {
    const all = final ?? answers
    const lines = questions.map((qq, i) => {
      const a = all[i]?.length ? all[i].join(', ') : t('ask.noAnswer')
      const label = qq.header || qq.question
      return `${label}: ${a}`
    })
    setSubmitted(true)
    onAnswer(questions.length === 1 ? (all[0]?.join(', ') ?? '') : lines.join('\n'))
  }

  if (submitted) {
    return (
      <div className="fade-up tp-panel border border-border bg-card px-4 py-3">
        <div className="flex items-start gap-2.5">
          <Check className="mt-0.5 size-4 shrink-0 text-[var(--success)]" weight="bold" />
          <div className="min-w-0 flex-1 space-y-1">
            {questions.map((qq, i) => (
              <p key={i} className="text-xs text-muted-foreground">
                <span className="font-medium text-foreground">{qq.header || qq.question}:</span>{' '}
                {answers[i]?.join(', ') || t('ask.noAnswer')}
              </p>
            ))}
          </div>
        </div>
      </div>
    )
  }

  const hasOptions = (q.options ?? []).length > 0

  return (
    <div className="fade-up tp-panel border border-border bg-card p-4">
      <div className="flex items-start gap-3">
        <Question className="mt-0.5 size-4 shrink-0 text-foreground" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            {q.header ? (
              <span className="eyebrow">
                {q.header}
              </span>
            ) : (
              <span />
            )}
            {questions.length > 1 ? (
              <span className="shrink-0 font-mono text-[11px] tabular-nums text-dim">
                {t('ask.progress', { current: idx + 1, total: questions.length })}
              </span>
            ) : null}
          </div>

          <div className="mt-1.5 text-sm font-medium">
            <Markdown content={q.question} className="prose-sm" />
          </div>

          <div className="mt-3 space-y-2">
            {hasOptions ? (
              <div className="flex flex-wrap gap-2">
                {q.options!.map((o) => {
                  const on = selected.includes(o)
                  return (
                    <button
                      key={o}
                      type="button"
                      aria-pressed={on}
                      disabled={disabled}
                      onClick={() => toggleOption(o)}
                      className={cn(
                        'inline-flex min-h-9 max-w-full items-center gap-1.5 rounded-full border px-3.5 py-1.5 text-xs transition-[border-color,background-color,color] duration-200 disabled:cursor-not-allowed disabled:opacity-50',
                        on
                          ? 'border-foreground/60 bg-nav-active text-foreground'
                          : 'border-border text-muted-foreground hover:border-line hover:bg-raised hover:text-foreground',
                      )}
                    >
                      {on && multi ? <Check className="size-3.5" /> : null}
                      <span className="truncate">{o}</span>
                    </button>
                  )
                })}
              </div>
            ) : null}

            {/* Free-text input — no send button; Next/Submit commits it. */}
            <Input
              value={custom}
              onChange={(e) => setCustom(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  if (isLast) {
                    handleSubmit()
                  } else {
                    handleNext()
                  }
                }
              }}
              placeholder={
                hasOptions ? t('ask.otherPlaceholder') : t('ask.customPlaceholder')
              }
              disabled={disabled}
            />

            {multi && selected.length > 0 ? (
              <p className="text-xs text-muted-foreground">
                {t('ask.selected', { items: selected.join(', ') })}
              </p>
            ) : null}
          </div>

          {/* Navigation. Next auto-attaches the typed text. */}
          {questions.length > 1 || multi || !hasOptions ? (
            <div className="mt-3 flex items-center justify-between gap-2">
              <Button
                size="sm"
                variant="ghost"
                disabled={disabled || idx === 0}
                onClick={() => setIdx((i) => Math.max(0, i - 1))}
                className="gap-1.5"
              >
                <ArrowLeft className="size-4" />
                {t('ask.prev')}
              </Button>
              {isLast ? (
                <Button
                  size="sm"
                  disabled={disabled || (answeredCount === 0 && !custom.trim())}
                  onClick={handleSubmit}
                  className="gap-1.5"
                >
                  <Check className="size-4" />
                  {t('ask.submit')}
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={disabled}
                  onClick={handleNext}
                  className="gap-1.5"
                >
                  {t('ask.next')}
                  <ArrowRight className="size-4" />
                </Button>
              )}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}
