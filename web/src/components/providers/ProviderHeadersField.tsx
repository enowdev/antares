import { useI18n } from '@/lib/i18n'
import { Label, Textarea } from '@/components/ui/primitives'

export function ProviderHeadersField({
  id,
  value,
  onChange,
}: {
  id: string
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useI18n()
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{t('providers.headers')}</Label>
      <Textarea
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={t('providers.headersPlaceholder')}
        className="min-h-[60px] resize-y font-mono text-xs"
        autoComplete="off"
        spellCheck={false}
      />
    </div>
  )
}
