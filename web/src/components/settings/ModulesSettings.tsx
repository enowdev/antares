import { useState } from 'react'
import { SquaresFour } from '@phosphor-icons/react'
import { useI18n, type MessageKey } from '@/lib/i18n'
import { CUSTOM_PRESET, MODULE_IDS, PRESET_IDS, PRESET_MODULES, type ModuleId, type PresetId } from '@/lib/modules'
import { hubsOfModule, withModule } from '@/lib/moduleNav'
import { HUB_MANIFEST, tabsOf } from '@/lib/routeManifest'
import { saveModules, useModules } from '@/lib/useModules'
import { Card, CardContent, CardDescription, CardHeader, CardTitle, Label, Switch } from '@/components/ui/primitives'

/**
 * Whether a Settings search should surface the Modules card: its own title,
 * the preset names, and every module's hubs and tabs, so searching "vps"
 * after choosing General finds the switch that brings it back. `query` is
 * already trimmed and lower-cased.
 */
export function modulesMatchQuery(t: (key: MessageKey) => string, query: string): boolean {
  const terms = ['modules', 'preset', t('modules.title'), t('modules.preset'), t('preset.custom')]
  for (const id of PRESET_IDS) terms.push(t(`preset.${id}` as MessageKey))
  for (const m of MODULE_IDS) {
    terms.push(m)
    for (const h of hubsOfModule(HUB_MANIFEST, m)) {
      terms.push(t(h.titleKey))
      for (const r of tabsOf(h.id)) terms.push(t(r.tabKey ?? r.titleKey))
    }
  }
  return terms.some((s) => s.toLowerCase().includes(query))
}

/**
 * Settings → Modules: which optional hubs the sidebar lists. Every change
 * saves at once; the shared store updates the sidebar without a reload. The
 * controls read the store, so a failed save leaves them where they were.
 */
export function ModulesSettings() {
  const { t } = useI18n()
  const { active, preset, loaded } = useModules()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string>()
  const disabled = !loaded || pending

  const save = async (next: ReadonlySet<ModuleId>) => {
    setPending(true)
    setError(undefined)
    try {
      await saveModules(next)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setPending(false)
    }
  }

  return (
    <Card aria-busy={pending || undefined}>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <SquaresFour className="size-4 text-primary" weight="fill" />
          {t('modules.title')}
        </CardTitle>
        <CardDescription>{t('modules.desc')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="modules-preset">{t('modules.preset')}</Label>
          <select
            id="modules-preset"
            value={preset}
            disabled={disabled}
            onChange={(e) => {
              const id = e.target.value as PresetId
              if (PRESET_IDS.includes(id)) void save(new Set(PRESET_MODULES[id]))
            }}
            className="block h-9 w-full rounded-[var(--radius-sm)] border border-input bg-background px-3 text-sm disabled:opacity-60 sm:max-w-xs"
          >
            {PRESET_IDS.map((id) => (
              <option key={id} value={id}>
                {t(`preset.${id}` as MessageKey)}
              </option>
            ))}
            {preset === CUSTOM_PRESET ? (
              <option value={CUSTOM_PRESET} disabled>
                {t('preset.custom')}
              </option>
            ) : null}
          </select>
        </div>

        <ul className="divide-y divide-border rounded-[var(--radius-sm)] border border-border">
          {MODULE_IDS.map((m) => {
            const hubs = hubsOfModule(HUB_MANIFEST, m)
            const id = `module-${m}`
            return (
              <li key={m} className="flex items-center gap-3 px-3 py-2.5">
                <div className="min-w-0 flex-1">
                  <label htmlFor={id} className="text-sm font-medium">
                    {hubs.map((h) => t(h.titleKey)).join(', ')}
                  </label>
                  <p className="truncate text-xs text-muted-foreground">
                    {hubs
                      .flatMap((h) => tabsOf(h.id))
                      .map((r) => t(r.tabKey ?? r.titleKey))
                      .join(' · ')}
                  </p>
                </div>
                <Switch
                  id={id}
                  checked={active.has(m)}
                  disabled={disabled}
                  onCheckedChange={(on) => void save(withModule(active, m, on))}
                />
              </li>
            )
          })}
        </ul>

        {error ? <p className="text-xs text-[var(--destructive)]">{error}</p> : null}
      </CardContent>
    </Card>
  )
}
