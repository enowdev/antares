/**
 * The composer's execution target. A chat target runs through `/api/chat` and
 * the active Antares provider; a Cursor target runs through `/api/chat/cursor`
 * and never becomes the active chat provider.
 */

import {
  cursorModelMatches,
  defaultCursorVariant,
  type CursorModel,
  type CursorVariant,
} from '@/lib/cursorModels'
import type { ReasoningCapability } from '@/lib/models'

export interface ChatCatalogueModel {
  id: string
  name: string
  provider: string
  provider_label: string
  reasoning_capability?: ReasoningCapability
}

export interface ChatTarget {
  kind: 'chat'
  provider: string
  model: string
  name: string
  providerLabel: string
  reasoningCapability?: ReasoningCapability
}

export interface CursorTarget {
  kind: 'cursor'
  model: CursorModel
  variant: CursorVariant
}

export type ComposerTarget = ChatTarget | CursorTarget

export type CursorMode = 'agent' | 'plan'

/**
 * Everything one Cursor turn needs. `repositoryUrl`/`startingRef` are null
 * until the user edits them, so the server keeps discovering the project's own
 * repository; an empty string is an explicit "no repository".
 */
export interface CursorOptionsValue {
  model: CursorModel
  variant: CursorVariant
  mode: CursorMode
  repositoryUrl: string | null
  startingRef: string | null
  autoCreatePR: boolean
}

export function isCursorTarget(target: ComposerTarget | null): target is CursorTarget {
  return target?.kind === 'cursor'
}

export function isChatTarget(target: ComposerTarget | null): target is ChatTarget {
  return target?.kind === 'chat'
}

export function chatTargetFromModel(model: ChatCatalogueModel): ChatTarget {
  return {
    kind: 'chat',
    provider: model.provider,
    model: model.id,
    name: model.name,
    providerLabel: model.provider_label,
    reasoningCapability: model.reasoning_capability,
  }
}

export function cursorTargetFromModel(
  model: CursorModel,
  variant: CursorVariant = defaultCursorVariant(model),
): CursorTarget {
  return { kind: 'cursor', model, variant }
}

export function composerTargetKey(target: ComposerTarget): string {
  return target.kind === 'cursor'
    ? `cursor:${target.model.id}`
    : `chat:${target.provider}/${target.model}`
}

/** The composer chip label: the chat model id, or `Cursor · <model>`. */
export function composerTargetLabel(target: ComposerTarget | null): string {
  if (!target) return ''
  return target.kind === 'cursor'
    ? `Cursor · ${target.model.name || target.model.id}`
    : target.model
}

function chatModelMatches(model: ChatCatalogueModel, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return [model.id, model.name, model.provider, model.provider_label].some((entry) =>
    entry.toLowerCase().includes(q),
  )
}

/**
 * One search over both catalogues, presented as two groups. The catalogues stay
 * separate: a Cursor hit is never offered as a chat model.
 */
export function searchComposerTargets(input: {
  chatModels: ChatCatalogueModel[]
  cursorModels: CursorModel[]
  query: string
}): { chat: ChatTarget[]; cursor: CursorTarget[] } {
  const { chatModels = [], cursorModels = [], query } = input
  return {
    chat: chatModels
      .filter((model) => chatModelMatches(model, query))
      .map(chatTargetFromModel),
    cursor: cursorModels
      .filter((model) => cursorModelMatches(model, query))
      .map((model) => cursorTargetFromModel(model)),
  }
}

export type CursorCatalogueState = 'connect' | 'error' | 'empty' | 'ready'

/**
 * What the Cursor section should show. A missing credential is an invitation to
 * connect, not a failure.
 */
export function cursorCatalogueState(
  response: { models?: CursorModel[]; needs_key?: boolean; error?: string } | undefined,
  requestError?: Error,
): CursorCatalogueState {
  if (response?.needs_key) return 'connect'
  if (response?.error || requestError) return 'error'
  if ((response?.models ?? []).length === 0) return 'empty'
  return 'ready'
}

/**
 * The identity Cursor follow-up reuse depends on. Conversation mode is absent
 * on purpose: Create Run accepts a mode override, so switching Agent/Plan
 * continues the same remote agent.
 */
export function cursorRunIdentity(value: CursorOptionsValue): string {
  return JSON.stringify({
    model: value.model.id,
    params: (value.variant.params ?? []).map((param) => [param.id, param.value]),
    repository: value.repositoryUrl,
    ref: value.startingRef,
    autoCreatePR: value.autoCreatePR,
  })
}

/**
 * The run a follow-up would continue: what it was configured with, and whether
 * the server still considers that agent reusable.
 */
export interface CursorRunBaseline {
  options: CursorOptionsValue
  reuseValid: boolean
}

/** Whether sending now would start a new Cursor agent instead of following up. */
export function startsNewCursorAgent(
  previous: CursorRunBaseline | null,
  next: CursorOptionsValue,
): boolean {
  if (!previous) return false
  // An invalidated chain always creates a new agent, even for an identical
  // selection — a failed create, a target switch, or an interrupted approval
  // all leave nothing to follow up on.
  if (!previous.reuseValid) return true
  return cursorRunIdentity(previous.options) !== cursorRunIdentity(next)
}

export interface CursorChatRequest {
  session_id: string
  message: string
  images: string[]
  model: { id: string; params: Array<{ id: string; value: string }> }
  mode: CursorMode
  auto_create_pr: boolean
  project_dir?: string
  repository_url?: string
  starting_ref?: string
}

/** The exact `POST /api/chat/cursor` body for one turn. */
export function cursorChatRequest(
  value: CursorOptionsValue,
  turn: {
    sessionId: string
    message: string
    images?: string[]
    projectDir?: string
  },
): CursorChatRequest {
  const request: CursorChatRequest = {
    session_id: turn.sessionId,
    message: turn.message,
    images: [...(turn.images ?? [])],
    model: {
      id: value.model.id,
      // The whole upstream variant, hidden params included.
      params: (value.variant.params ?? []).map((param) => ({
        id: param.id,
        value: param.value,
      })),
    },
    mode: value.mode,
    auto_create_pr: value.autoCreatePR,
  }
  if (turn.projectDir) request.project_dir = turn.projectDir
  if (value.repositoryUrl !== null) request.repository_url = value.repositoryUrl
  if (value.startingRef !== null) request.starting_ref = value.startingRef
  return request
}
