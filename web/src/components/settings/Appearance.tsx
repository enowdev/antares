import { CheckCircle, Moon, Sun, Translate } from '@phosphor-icons/react'
import { LANGUAGES, useI18n, type MessageKey } from '@/lib/i18n'
import { PALETTES, PALETTE_SWATCHES, type Palette } from '@/lib/theme'
import { useTheme } from '@/lib/theme-provider'
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
        className="h-9 w-full cursor-pointer rounded-full border border-input bg-transparent pl-9 pr-4 text-sm transition-[border-color] duration-200 hover:border-line focus-visible:border-ring focus-visible:outline-none"
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

const PALETTE_LABEL: Record<Palette, MessageKey> = {
  claude: 'theme.claude',
  facebook: 'theme.facebook',
  pinterest: 'theme.pinterest',
  supabase: 'theme.supabase',
}

/** Palette choice, shown in the current light or dark mode. */
export function PalettePicker() {
  const { theme, palette, setPalette } = useTheme()
  const { t } = useI18n()
  return (
    <div className="grid grid-cols-[repeat(auto-fit,minmax(11rem,1fr))] gap-2">
      {PALETTES.map((id) => {
        const swatch = PALETTE_SWATCHES[id][theme]
        const selected = palette === id
        return (
          <button
            key={id}
            type="button"
            onClick={() => setPalette(id)}
            aria-pressed={selected}
            className={cn(
              'flex items-center gap-3 rounded-[var(--radius-md)] border p-2.5 text-left transition-colors',
              selected ? 'border-primary ring-2 ring-primary/40' : 'border-border hover:bg-raised',
            )}
          >
            <span
              className="size-8 shrink-0 overflow-hidden rounded-[var(--radius-xs)] border border-border"
              style={{ background: `linear-gradient(135deg, ${swatch.bg} 50%, ${swatch.accent} 50%)` }}
              aria-hidden
            />
            <span className="min-w-0 flex-1 truncate text-sm font-medium">{t(PALETTE_LABEL[id])}</span>
            {selected ? <CheckCircle className="size-4 shrink-0 text-primary" weight="fill" /> : null}
          </button>
        )
      })}
    </div>
  )
}

/** Settings › Appearance: per-browser language, theme and palette. */
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
        <div className="flex flex-col gap-2 px-4 py-3.5 sm:flex-row sm:items-start sm:gap-4 sm:px-5">
          <span className="text-[13px] font-medium sm:w-[42%] sm:pt-2">{t('settings.palette')}</span>
          <div className="sm:flex-1">
            <PalettePicker />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
