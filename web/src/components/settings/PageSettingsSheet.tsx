import { lazy, Suspense } from 'react'
import { useSearchParams } from 'react-router-dom'
import { GearSix } from '@phosphor-icons/react'
import { useI18n } from '@/lib/i18n'
import { SETTINGS_PARAM, humanizeGroup } from '@/lib/configGroups'
import type { RouteManifestEntry } from '@/lib/routeManifest'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Tooltip } from '@/components/ui/primitives'
import { SkeletonList } from '@/components/ui/skeleton'

// The field editor only loads once a sheet opens, keeping it out of the shell bundle.
const ConfigGroupPanel = lazy(() =>
  import('@/components/settings/ConfigGroupPanel').then((m) => ({ default: m.ConfigGroupPanel })),
)

/**
 * Gear button for a page whose config groups moved out of Settings. Opens a
 * right-hand sheet (full screen on phones) editing just those groups. Open
 * state lives in `?settings=1`, so Settings search can link straight into it.
 */
export function PageSettingsSheet({ route }: { route: RouteManifestEntry }) {
  const { t } = useI18n()
  const [params, setParams] = useSearchParams()
  const open = params.get(SETTINGS_PARAM) === '1'
  const groups = route.configGroups ?? []

  const setOpen = (next: boolean) =>
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev)
        if (next) p.set(SETTINGS_PARAM, '1')
        else p.delete(SETTINGS_PARAM)
        return p
      },
      { replace: true },
    )

  if (groups.length === 0) return null
  const label = t('settings.pageSettings')

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Tooltip label={label}>
        <Button variant="outline" size="icon-sm" aria-label={label} onClick={() => setOpen(true)}>
          <GearSix />
        </Button>
      </Tooltip>
      <DialogContent
        className={
          // Phone: full screen. From sm up: a sheet pinned to the right edge.
          'inset-0 h-dvh max-h-none border-0 ' +
          'sm:inset-y-0 sm:left-auto sm:right-0 sm:top-0 sm:bottom-0 sm:h-dvh sm:max-h-none sm:max-w-xl ' +
          'sm:translate-x-0 sm:translate-y-0 sm:border-0 sm:border-l'
        }
      >
        <DialogHeader className="pt-[calc(env(safe-area-inset-top)+1rem)] sm:pt-5">
          <DialogTitle>{t('settings.sheetTitle', { page: t(route.tabKey ?? route.titleKey) })}</DialogTitle>
          <DialogDescription>
            {t('settings.sheetDesc', { groups: groups.map(humanizeGroup).join(', ') })}
          </DialogDescription>
        </DialogHeader>
        <Suspense
          fallback={
            <div className="p-4 sm:p-5">
              <SkeletonList count={4} />
            </div>
          }
        >
          <ConfigGroupPanel groups={groups} />
        </Suspense>
      </DialogContent>
    </Dialog>
  )
}
