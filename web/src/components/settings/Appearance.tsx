import { Moon, Sun, Translate } from '@phosphor-icons/react'
import { LANGUAGES, useI18n } from '@/lib/i18n'
import { useTheme } from '@/lib/theme'
import { cn } from '@/lib/utils'
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Label } from '@/components/ui/primitives'

/** Dashboard language select. Stored per browser. */
export function LanguagePicker({ id, className }: { id?: string; className?: string }) {
  const { lang, setLang, t } = useI18n()
  return (
    <div className={cn('relative', className)}>
      <Translate className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <select
        id={id}
        value={lang}
        onChange={(e) => setLang(e.target.value as typeof lang)}
        className="h-9 w-full cursor-pointer border border-input bg-transparent pl-9 pr-3 text-sm transition-[border-color] duration-200 hover:border-line focus-visible:border-ring focus-visible:outline-none"
        aria-label={t('nav.language')}
      >
        {LANGUAGES.map((l) => (
          <option key={l.code} value={l.code} className="bg-popover text-popover-foreground">
            {l.label}
          </option>
        ))}
      </select>
    </div>
  )
}

/** Light / dark segmented toggle. */
export function ThemeToggle() {
  const { theme, setTheme } = useTheme()
  const { t } = useI18n()
  const options = [
    { id: 'light' as const, label: t('settings.themeLight'), Icon: Sun },
    { id: 'dark' as const, label: t('settings.themeDark'), Icon: Moon },
  ]
  return (
    <div role="radiogroup" aria-label={t('settings.theme')} className="inline-flex rounded-full border border-border bg-card p-1">
      {options.map(({ id, label, Icon }) => {
        const active = theme === id
        return (
          <button
            key={id}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => setTheme(id)}
            className={cn(
              'flex h-7 items-center gap-1.5 rounded-full px-3.5 text-xs transition-[background-color,color] duration-200',
              active ? 'bg-nav-active text-foreground' : 'text-muted-foreground hover:text-foreground',
            )}
          >
            <Icon className="size-4" weight={active ? 'fill' : 'regular'} />
            {label}
          </button>
        )
      })}
    </div>
  )
}

/** Settings › Appearance: per-browser language and theme. */
export function AppearanceCard() {
  const { t } = useI18n()
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('settings.appearance')}</CardTitle>
        <CardDescription>{t('settings.appearanceDesc')}</CardDescription>
      </CardHeader>
      <CardContent className="divide-y divide-border border-t border-border p-0 sm:p-0">
        <div className="flex flex-col gap-2 px-4 py-3.5 sm:flex-row sm:items-center sm:gap-4 sm:px-5">
          <Label htmlFor="appearance-language" className="sm:w-[42%]">
            {t('settings.language')}
          </Label>
          <LanguagePicker id="appearance-language" className="sm:flex-1" />
        </div>
        <div className="flex flex-col gap-2 px-4 py-3.5 sm:flex-row sm:items-center sm:gap-4 sm:px-5">
          <span className="text-[13px] font-medium sm:w-[42%]">{t('settings.theme')}</span>
          <div className="sm:flex-1">
            <ThemeToggle />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
