/**
 * Pure stream-lifecycle and approval helpers shared by the chat page.
 *
 * The approval payload the server publishes is an immutable display
 * projection: the pending operation itself stays on the server behind an
 * opaque id, so nothing parsed here can change what a decision executes.
 */

export interface ApprovalView {
  id: string
  tool: string
  arguments: string
  message: string
  /** Set once answered, so the card shows the outcome instead of buttons. */
  decided?: 'allowed' | 'refused' | 'expired'
}

export interface PendingApproval {
  id: string
  session_id: string
  tool: string
  arguments: string
  message?: string
}

export const CURSOR_APPROVAL_TOOLS = ['cursor_direct', 'cursor_direct_cancel'] as const

/** An `approval` stream event as a card view, or null for anything else. */
export function approvalFromEvent(
  event: Record<string, unknown>,
): ApprovalView | null {
  if (event.type !== 'approval') return null
  const id = typeof event.id === 'string' ? event.id : ''
  if (!id) return null
  return {
    id,
    tool: String(event.name ?? ''),
    arguments: String(event.arguments ?? ''),
    message: String(event.message ?? ''),
  }
}

/** Add an approval once. A decision already shown to the user is never reset. */
export function mergeApprovals(
  current: ApprovalView[],
  incoming: ApprovalView,
): ApprovalView[] {
  if (current.some((approval) => approval.id === incoming.id)) return current
  return [...current, incoming]
}

/**
 * The approvals waiting on this session, merged into what is already on screen.
 * Used after (re)opening a session, where the `approval` event that announced a
 * still-pending decision was published before this page attached.
 */
export function pendingApprovalsForSession(
  current: ApprovalView[],
  pending: PendingApproval[],
  sessionId: string | undefined,
): ApprovalView[] {
  if (!sessionId) return current
  let merged = current
  for (const request of pending ?? []) {
    if (request.session_id !== sessionId) continue
    merged = mergeApprovals(merged, {
      id: request.id,
      tool: request.tool,
      arguments: request.arguments,
      message: request.message ?? '',
    })
  }
  return merged
}

export interface CursorApprovalDetails {
  operation: string
  newAgent: boolean
  model: string
  params: Array<{ id: string; value: string }>
  repositoryUrl: string
  repositorySource: string
  startingRef: string
  worktreeDirty: boolean
  localOnlyCommits: number
  remoteRefKnown: boolean
  warnings: string[]
  mode: string
  autoCreatePR: boolean
  promptPreview: string
  imageCount: number
  /** Populated for a cancellation, which names the run it would stop. */
  agentId: string
  runId: string
}

/** The Cursor projection behind an approval, or null for any other tool. */
export function parseCursorApproval(
  approval: Pick<ApprovalView, 'tool' | 'arguments'>,
): CursorApprovalDetails | null {
  if (!CURSOR_APPROVAL_TOOLS.includes(approval.tool as (typeof CURSOR_APPROVAL_TOOLS)[number])) {
    return null
  }
  let parsed: Record<string, unknown>
  try {
    const decoded: unknown = JSON.parse(approval.arguments)
    if (typeof decoded !== 'object' || decoded === null) return null
    parsed = decoded as Record<string, unknown>
  } catch {
    return null
  }

  const model = (parsed.model ?? {}) as { id?: unknown; params?: unknown }
  const params = Array.isArray(model.params)
    ? (model.params as Array<Record<string, unknown>>).map((param) => ({
        id: String(param.id ?? ''),
        value: String(param.value ?? ''),
      }))
    : []
  return {
    operation: String(parsed.operation ?? ''),
    newAgent: parsed.kind === 'new_agent',
    model: String(model.id ?? ''),
    params,
    repositoryUrl: String(parsed.repository_url ?? ''),
    repositorySource: String(parsed.repository_source ?? ''),
    startingRef: String(parsed.starting_ref ?? ''),
    worktreeDirty: parsed.worktree_dirty === true,
    localOnlyCommits: Number(parsed.local_only_commits ?? 0),
    remoteRefKnown: parsed.remote_ref_known === true,
    warnings: Array.isArray(parsed.warnings) ? parsed.warnings.map(String) : [],
    mode: String(parsed.mode ?? ''),
    autoCreatePR: parsed.auto_create_pr === true,
    promptPreview: String(parsed.prompt_preview ?? ''),
    imageCount: Number(parsed.image_count ?? 0),
    agentId: String(parsed.agent_id ?? ''),
    runId: String(parsed.run_id ?? ''),
  }
}

/**
 * What the composer's Stop button does. A Cursor run lives on Cursor's side, so
 * Stop only closes this browser's stream; cancelling it remotely is a separate,
 * approved action.
 */
export function stopBehavior(kind: 'chat' | 'cursor'): {
  interrupt: boolean
  detach: boolean
} {
  return kind === 'cursor'
    ? { interrupt: false, detach: true }
    : { interrupt: true, detach: false }
}

/**
 * Whether the standing attach loop may reconnect. After an intentional detach
 * it must not, or Stop would immediately re-follow the run it just left.
 */
export function shouldReconnectAttach(state: {
  alive: boolean
  detached: boolean
}): boolean {
  return state.alive && !state.detached
}

export interface CursorBranch {
  repoUrl: string
  branch: string
  prUrl: string
}

export interface CursorSessionHydration {
  active: boolean
  modelId?: string
  remoteStatus?: string
  branches: CursorBranch[]
}

interface HydrationMessage {
  role: string
  model?: string
  meta?: Record<string, unknown> | null
}

/**
 * Recover the Cursor side of a persisted session: only a Cursor turn records a
 * remote status, so the newest one identifies the model, its outcome, and the
 * branches or pull requests the run produced.
 */
export function cursorSessionHydration(
  messages: HydrationMessage[],
): CursorSessionHydration {
  for (let i = (messages ?? []).length - 1; i >= 0; i--) {
    const message = messages[i]
    if (message.role !== 'assistant') continue
    const status = message.meta?.cursor_remote_status
    if (typeof status !== 'string' || !status) continue
    return {
      active: true,
      modelId: message.model || undefined,
      remoteStatus: status,
      branches: parseCursorBranches(message.meta?.cursor_git_state),
    }
  }
  return { active: false, branches: [] }
}

function parseCursorBranches(raw: unknown): CursorBranch[] {
  if (typeof raw !== 'string' || !raw) return []
  try {
    const parsed: unknown = JSON.parse(raw)
    const branches = (parsed as { branches?: unknown })?.branches
    if (!Array.isArray(branches)) return []
    return branches.map((branch: Record<string, unknown>) => ({
      repoUrl: String(branch.repoUrl ?? ''),
      branch: String(branch.branch ?? ''),
      prUrl: String(branch.prUrl ?? ''),
    }))
  } catch {
    return []
  }
}
