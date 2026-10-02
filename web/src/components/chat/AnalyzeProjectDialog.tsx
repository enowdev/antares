import { useState } from 'react'
import { ChatCircle, Database, MagnifyingGlass } from '@phosphor-icons/react'
import { useI18n } from '@/lib/i18n'
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

/**
 * Shown right after a folder is bound as a project. It offers to have the agent
 * analyze the whole project before the first message (Yes) or to skip straight
 * to chatting (No). Either choice starts the project session; only the first
 * turn differs.
 */
export function AnalyzeProjectDialog({
  open,
  projectDir,
  onChoose,
}: {
  open: boolean
  projectDir: string
  // analyze=true kicks off an automatic analysis turn; indexRag opts the project
  // into RAG indexing + retrieval for the session.
  onChoose: (analyze: boolean, indexRag: boolean) => void
}) {
  const { t } = useI18n()
  const name = projectDir.split('/').filter(Boolean).pop() || projectDir
  const [indexRag, setIndexRag] = useState(true)

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onChoose(false, false)}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('project.analyzeTitle')}</DialogTitle>
          <DialogDescription>
            {t('project.analyzeDesc', { name })}
          </DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-3">
          <label className="flex items-start gap-2 rounded-[var(--radius-md)] border border-border p-3">
            <input
              type="checkbox"
              checked={indexRag}
              onChange={(e) => setIndexRag(e.target.checked)}
              className="mt-0.5"
            />
            <span className="min-w-0">
              <span className="flex items-center gap-1.5 text-sm font-medium">
                <Database className="size-4 text-muted-foreground" />
                {t('project.indexRag')}
              </span>
              <span className="block text-[11px] leading-relaxed text-muted-foreground">
                {t('project.indexRagDesc')}
              </span>
            </span>
          </label>

          <div className="grid gap-2 sm:grid-cols-2">
            <button
              onClick={() => onChoose(true, indexRag)}
              className="flex flex-col items-start gap-1.5 rounded-[var(--radius-md)] border border-foreground/60 bg-raised p-3 text-left transition-[border-color,background-color] duration-200 hover:border-foreground"
            >
              <MagnifyingGlass className="size-5 text-foreground" />
              <span className="text-sm font-medium">{t('project.analyzeYes')}</span>
              <span className="text-[11px] leading-relaxed text-muted-foreground">
                {t('project.analyzeYesDesc')}
              </span>
            </button>
            <button
              onClick={() => onChoose(false, indexRag)}
              className="flex flex-col items-start gap-1.5 rounded-[var(--radius-md)] border border-border p-3 text-left transition-[border-color,background-color] duration-200 hover:border-line hover:bg-raised"
            >
              <ChatCircle className="size-5 text-muted-foreground" />
              <span className="text-sm font-medium">{t('project.analyzeNo')}</span>
              <span className="text-[11px] leading-relaxed text-muted-foreground">
                {t('project.analyzeNoDesc')}
              </span>
            </button>
          </div>
        </DialogBody>
      </DialogContent>
    </Dialog>
  )
}
