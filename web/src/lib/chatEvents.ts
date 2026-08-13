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

/** The durable Cursor state `GET /api/sessions/{id}` projects for the composer. */
export interface CursorStateProjection {
  target_active: boolean
  reuse_valid: boolean
  model_id: string
  model_params: Array<{ id: string; value: string }>
  /** null when the run discovered its repository (or ran without one). */
  repository_url: string | null
  starting_ref: string
  mode: string
  auto_create_pr: boolean
  remote_status: string
  operation_state: string
  git?: { branches?: Array<{ repo_url: string; branch: string; pr_url: string }> }
}

export interface CursorHydration {
  /** Whether this conversation's execution target is still Cursor. */
  active: boolean
  modelId?: string
  params?: Array<{ id: string; value: string }>
  mode?: 'agent' | 'plan'
  repositoryUrl?: string | null
  startingRef?: string
  autoCreatePR?: boolean
  reuseValid?: boolean
  remoteStatus?: string
  operationState?: string
  /** A remote run that has not reached a terminal state yet. */
  running?: boolean
  branches: CursorBranch[]
}

/** Operation states in which Cursor still owns unfinished remote work. */
const CURSOR_RUNNING_OPERATIONS = [
  'awaiting_approval',
  'create_in_flight',
  'run_in_flight',
]

/**
 * Restore the Cursor half of a session. The durable projection is
 * authoritative: when the server reports no state, or a target that is no
 * longer Cursor, old transcript metadata must not resurrect Cursor mode.
 * Transcript parsing survives only for a server that predates the projection,
 * which is the one case where the field is absent rather than null.
 */
export function cursorHydrationFromDetail(detail: {
  cursor_state?: CursorStateProjection | null
  messages: HydrationMessage[]
}): CursorHydration {
  if (detail.cursor_state === undefined) {
    const legacy = cursorSessionHydration(detail.messages)
    return {
      active: legacy.active,
      modelId: legacy.modelId,
      remoteStatus: legacy.remoteStatus,
      // A transcript proves neither the exact variant nor that a follow-up
      // would reuse the same agent.
      reuseValid: false,
      running: false,
      branches: legacy.branches,
    }
  }

  const state = detail.cursor_state
  if (!state) return { active: false, branches: [] }

  const branches: CursorBranch[] = (state.git?.branches ?? []).map((branch) => ({
    repoUrl: String(branch.repo_url ?? ''),
    branch: String(branch.branch ?? ''),
    prUrl: String(branch.pr_url ?? ''),
  }))
  const remoteStatus = state.remote_status || undefined
  const operationState = state.operation_state || undefined

  if (!state.target_active) {
    return {
      active: false,
      reuseValid: false,
      running: false,
      remoteStatus,
      operationState,
      branches,
    }
  }

  const hydration: CursorHydration = {
    active: true,
    mode: state.mode === 'agent' || state.mode === 'plan' ? state.mode : undefined,
    repositoryUrl: state.repository_url ?? null,
    startingRef: typeof state.starting_ref === 'string' ? state.starting_ref : '',
    autoCreatePR: state.auto_create_pr === true,
    reuseValid: state.reuse_valid === true,
    remoteStatus,
    operationState,
    running: CURSOR_RUNNING_OPERATIONS.includes(state.operation_state),
    branches,
  }
  // The server drops both halves of a selection it could not decode exactly.
  if (state.model_id) {
    hydration.modelId = state.model_id
    hydration.params = (state.model_params ?? []).map((param) => ({
      id: String(param.id ?? ''),
      value: String(param.value ?? ''),
    }))
  }
  return hydration
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
