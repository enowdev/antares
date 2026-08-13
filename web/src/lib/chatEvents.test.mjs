import { describe, expect, test } from 'bun:test'
import {
  approvalFromEvent,
  cursorSessionHydration,
  mergeApprovals,
  parseCursorApproval,
  pendingApprovalsForSession,
  shouldReconnectAttach,
  stopBehavior,
} from './chatEvents.ts'

const startArguments = JSON.stringify({
  operation: 'start',
  kind: 'new_agent',
  model: {
    id: 'gpt-5.6-sol',
    params: [
      { id: 'reasoning', value: 'max' },
      { id: 'internal', value: 'off' },
    ],
  },
  repository_url: 'https://github.com/acme/repo',
  repository_source: 'auto',
  starting_ref: 'main',
  worktree_dirty: true,
  local_only_commits: 2,
  remote_ref_known: false,
  warnings: ['Local uncommitted changes are absent from the Cursor cloud VM.'],
  mode: 'agent',
  auto_create_pr: false,
  prompt_preview: 'ship the release',
  image_count: 1,
})

describe('approval events', () => {
  test('an approval event becomes a card view', () => {
    expect(
      approvalFromEvent({
        type: 'approval',
        id: 'apr_1',
        name: 'cursor_direct',
        arguments: startArguments,
        message: 'Start Cursor Cloud Agent run',
      }),
    ).toEqual({
      id: 'apr_1',
      tool: 'cursor_direct',
      arguments: startArguments,
      message: 'Start Cursor Cloud Agent run',
    })
  })

  test('non-approval and id-less events are ignored', () => {
    expect(approvalFromEvent({ type: 'text', delta: 'hi' })).toBeNull()
    expect(approvalFromEvent({ type: 'approval', name: 'cursor_direct' })).toBeNull()
  })

  test('the same approval never appears twice and keeps its decision', () => {
    const first = { id: 'apr_1', tool: 'cursor_direct', arguments: '{}', message: 'Start' }
    const decided = mergeApprovals(
      mergeApprovals([], first).map((a) => ({ ...a, decided: 'allowed' })),
      { ...first, message: 'Start again' },
    )
    expect(decided).toHaveLength(1)
    expect(decided[0].decided).toBe('allowed')
    expect(mergeApprovals(decided, { id: 'apr_2', tool: 'cursor_direct_cancel', arguments: '{}', message: 'Cancel' })).toHaveLength(2)
  })

  test('pending approvals are scoped to the open session and de-duplicated', () => {
    const existing = [{ id: 'apr_1', tool: 'cursor_direct', arguments: '{}', message: 'Start', decided: 'allowed' }]
    const merged = pendingApprovalsForSession(
      existing,
      [
        { id: 'apr_1', session_id: 'ses_1', tool: 'cursor_direct', arguments: '{}', message: 'Start' },
        { id: 'apr_2', session_id: 'ses_1', tool: 'cursor_direct_cancel', arguments: '{}', message: 'Cancel' },
        { id: 'apr_3', session_id: 'ses_2', tool: 'terminal', arguments: '{}', message: 'Other session' },
      ],
      'ses_1',
    )
    expect(merged.map((a) => a.id)).toEqual(['apr_1', 'apr_2'])
    expect(merged[0].decided).toBe('allowed')
  })

  test('no open session shows no pending approvals', () => {
    expect(
      pendingApprovalsForSession([], [{ id: 'apr_1', session_id: 'ses_1', tool: 'x', arguments: '{}', message: '' }], undefined),
    ).toEqual([])
  })
})

describe('Cursor approval details', () => {
  test('parses the immutable Cursor projection', () => {
    const details = parseCursorApproval({
      id: 'apr_1',
      tool: 'cursor_direct',
      arguments: startArguments,
      message: 'Start Cursor Cloud Agent run',
    })
    expect(details).toEqual({
      operation: 'start',
      newAgent: true,
      model: 'gpt-5.6-sol',
      params: [
        { id: 'reasoning', value: 'max' },
        { id: 'internal', value: 'off' },
      ],
      repositoryUrl: 'https://github.com/acme/repo',
      repositorySource: 'auto',
      startingRef: 'main',
      worktreeDirty: true,
      localOnlyCommits: 2,
      remoteRefKnown: false,
      warnings: ['Local uncommitted changes are absent from the Cursor cloud VM.'],
      mode: 'agent',
      autoCreatePR: false,
      promptPreview: 'ship the release',
      imageCount: 1,
      agentId: '',
      runId: '',
    })
  })

  test('a follow-up is not a new agent', () => {
    const details = parseCursorApproval({
      id: 'apr_2',
      tool: 'cursor_direct',
      arguments: JSON.stringify({ operation: 'follow_up', kind: 'follow_up', model: { id: 'x', params: [] } }),
      message: 'Continue',
    })
    expect(details?.newAgent).toBe(false)
    expect(details?.operation).toBe('follow_up')
  })

  test('a cancellation names the remote run and carries no model params', () => {
    const details = parseCursorApproval({
      id: 'apr_3',
      tool: 'cursor_direct_cancel',
      arguments: JSON.stringify({ operation: 'cancel', agent_id: 'bc-1', run_id: 'run-1' }),
      message: 'Cancel Cursor Cloud Agent run',
    })
    expect(details?.operation).toBe('cancel')
    expect(details?.params).toEqual([])
    expect(details?.agentId).toBe('bc-1')
    expect(details?.runId).toBe('run-1')
  })

  test('other tools and malformed payloads have no Cursor details', () => {
    expect(parseCursorApproval({ id: 'a', tool: 'terminal', arguments: startArguments, message: '' })).toBeNull()
    expect(parseCursorApproval({ id: 'a', tool: 'cursor_direct', arguments: 'not json', message: '' })).toBeNull()
  })
})

describe('stream lifecycle', () => {
  test('Cursor Stop detaches locally while chat Stop interrupts the turn', () => {
    expect(stopBehavior('cursor')).toEqual({ interrupt: false, detach: true })
    expect(stopBehavior('chat')).toEqual({ interrupt: true, detach: false })
  })

  test('an intentional detach stops the standing attach loop from reconnecting', () => {
    expect(shouldReconnectAttach({ alive: true, detached: false })).toBe(true)
    expect(shouldReconnectAttach({ alive: true, detached: true })).toBe(false)
    expect(shouldReconnectAttach({ alive: false, detached: false })).toBe(false)
  })
})

describe('Cursor session hydration', () => {
  test('restores the Cursor target, status, and branches from persisted messages', () => {
    const state = cursorSessionHydration([
      { id: 'm1', role: 'user', content: 'hi', meta: { cursor_image_count: 1 } },
      {
        id: 'm2',
        role: 'assistant',
        content: 'done',
        model: 'gpt-5.6-sol',
        meta: {
          cursor_remote_status: 'FINISHED',
          cursor_git_state: JSON.stringify({
            branches: [
              { repoUrl: 'https://github.com/acme/repo', branch: 'cursor/x', prUrl: 'https://github.com/acme/repo/pull/7' },
            ],
          }),
        },
      },
    ])
    expect(state).toEqual({
      active: true,
      modelId: 'gpt-5.6-sol',
      remoteStatus: 'FINISHED',
      branches: [
        {
          repoUrl: 'https://github.com/acme/repo',
          branch: 'cursor/x',
          prUrl: 'https://github.com/acme/repo/pull/7',
        },
      ],
    })
  })

  test('an ordinary chat session is not a Cursor session', () => {
    expect(
      cursorSessionHydration([{ id: 'm1', role: 'assistant', content: 'hi', model: 'gpt-5.6' }]),
    ).toEqual({ active: false, branches: [] })
  })

  test('malformed Cursor git state never breaks hydration', () => {
    expect(
      cursorSessionHydration([
        { id: 'm1', role: 'assistant', content: 'x', model: 'sol', meta: { cursor_remote_status: 'ERROR', cursor_git_state: '{' } },
      ]),
    ).toEqual({ active: true, modelId: 'sol', remoteStatus: 'ERROR', branches: [] })
  })
})
