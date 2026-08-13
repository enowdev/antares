import { describe, expect, test } from 'bun:test'
import {
  chatTargetFromModel,
  composerTargetKey,
  cursorCatalogueState,
  cursorChatRequest,
  cursorTargetFromModel,
  isCursorTarget,
  searchComposerTargets,
  startsNewCursorAgent,
} from './composerTargets.ts'

const chatModels = [
  {
    id: 'gpt-5.6',
    name: 'GPT 5.6',
    provider: 'openai',
    provider_label: 'OpenAI',
    reasoning_capability: { values: [], mandatory: false, can_disable: false, source: 'live' },
  },
  {
    id: 'claude-opus-4-6',
    name: 'Claude Opus 4.6',
    provider: 'anthropic',
    provider_label: 'Anthropic',
  },
]

const cursorModels = [
  {
    id: 'gpt-5.6-sol',
    name: 'GPT 5.6 Sol',
    aliases: ['sol'],
    parameters: [{ id: 'reasoning', values: [{ value: 'low' }, { value: 'max' }] }],
    variants: [
      {
        params: [
          { id: 'reasoning', value: 'low' },
          { id: 'internal', value: 'off' },
        ],
        displayName: 'GPT 5.6 Sol',
        isDefault: true,
      },
      {
        params: [
          { id: 'reasoning', value: 'max' },
          { id: 'internal', value: 'off' },
        ],
        displayName: 'GPT 5.6 Sol (max)',
      },
    ],
  },
  {
    id: 'auto-smart',
    name: 'Auto (smart)',
    aliases: ['auto'],
    parameters: [],
    variants: [],
  },
]

describe('composer targets', () => {
  test('a chat target carries provider metadata and reasoning capability', () => {
    const target = chatTargetFromModel(chatModels[0])
    expect(target).toEqual({
      kind: 'chat',
      provider: 'openai',
      model: 'gpt-5.6',
      name: 'GPT 5.6',
      providerLabel: 'OpenAI',
      reasoningCapability: chatModels[0].reasoning_capability,
    })
    expect(isCursorTarget(target)).toBe(false)
  })

  test('a Cursor target starts from the upstream default variant', () => {
    const target = cursorTargetFromModel(cursorModels[0])
    expect(target.kind).toBe('cursor')
    expect(target.variant).toBe(cursorModels[0].variants[0])
    expect(isCursorTarget(target)).toBe(true)
  })

  test('a model with no upstream variant cannot become a target', () => {
    // cursorModels[1] is the catalogue's variant-less entry.
    expect(cursorTargetFromModel(cursorModels[1])).toBeNull()
  })

  test('target keys separate the two execution surfaces', () => {
    expect(composerTargetKey(chatTargetFromModel(chatModels[0]))).toBe('chat:openai/gpt-5.6')
    expect(composerTargetKey(cursorTargetFromModel(cursorModels[0]))).toBe('cursor:gpt-5.6-sol')
  })
})

describe('grouped target search', () => {
  test('searches chat id, name, and provider label', () => {
    expect(searchComposerTargets({ chatModels, cursorModels, query: 'anthropic' }).chat).toHaveLength(1)
    expect(searchComposerTargets({ chatModels, cursorModels, query: 'opus' }).chat[0].model).toBe(
      'claude-opus-4-6',
    )
    expect(searchComposerTargets({ chatModels, cursorModels, query: 'gpt-5.6' }).chat[0].model).toBe(
      'gpt-5.6',
    )
  })

  test('searches Cursor id, name, and alias without mixing the groups', () => {
    const bySlug = searchComposerTargets({ chatModels, cursorModels, query: 'sol' })
    expect(bySlug.cursor.map((t) => t.model.id)).toEqual(['gpt-5.6-sol'])
    expect(bySlug.chat).toHaveLength(0)

    const byAlias = searchComposerTargets({ chatModels, cursorModels, query: 'auto' })
    expect(byAlias.cursor.map((t) => t.model.id)).toEqual(['auto-smart'])
  })

  test('the Cursor group answers a Cursor provider search', () => {
    const found = searchComposerTargets({ chatModels, cursorModels, query: 'cursor' })
    expect(found.cursor).toHaveLength(2)
    expect(found.chat).toHaveLength(0)
  })

  test('a model with no upstream variant is listed but has no target to select', () => {
    const found = searchComposerTargets({ chatModels, cursorModels, query: 'auto' })
    expect(found.cursor.map((row) => row.model.id)).toEqual(['auto-smart'])
    expect(found.cursor[0].target).toBeNull()
    const usable = searchComposerTargets({ chatModels, cursorModels, query: 'sol' })
    expect(usable.cursor[0].target?.variant).toBe(cursorModels[0].variants[0])
  })

  test('an empty query keeps both catalogues intact', () => {
    const all = searchComposerTargets({ chatModels, cursorModels, query: '' })
    expect(all.chat).toHaveLength(2)
    expect(all.cursor).toHaveLength(2)
  })
})

describe('Cursor catalogue state', () => {
  test('a missing key asks for the Connect action instead of an error', () => {
    expect(cursorCatalogueState({ needs_key: true, models: [] })).toBe('connect')
  })

  test('a catalogue error is reported as an error', () => {
    expect(cursorCatalogueState({ models: [], error: 'Cursor API key expired' })).toBe('error')
    expect(cursorCatalogueState(undefined, new Error('Network unavailable'))).toBe('error')
  })

  test('a connected but empty catalogue is empty, not disconnected', () => {
    expect(cursorCatalogueState({ models: [] })).toBe('empty')
    expect(cursorCatalogueState({ models: cursorModels })).toBe('ready')
  })
})

describe('Cursor run identity', () => {
  const base = {
    model: cursorModels[0],
    variant: cursorModels[0].variants[0],
    mode: 'agent',
    repositoryUrl: 'https://github.com/acme/repo',
    startingRef: 'main',
    autoCreatePR: false,
  }
  const reusable = { options: base, reuseValid: true }

  test('mode-only changes continue the same agent', () => {
    expect(startsNewCursorAgent(reusable, { ...base, mode: 'plan' })).toBe(false)
  })

  test('model, variant, repository, ref, and auto-PR changes start a new agent', () => {
    expect(
      startsNewCursorAgent(reusable, { ...base, variant: cursorModels[0].variants[1] }),
    ).toBe(true)
    expect(
      startsNewCursorAgent(reusable, { ...base, model: cursorModels[1], variant: { params: [] } }),
    ).toBe(true)
    expect(startsNewCursorAgent(reusable, { ...base, repositoryUrl: '' })).toBe(true)
    expect(startsNewCursorAgent(reusable, { ...base, repositoryUrl: null })).toBe(true)
    expect(startsNewCursorAgent(reusable, { ...base, startingRef: 'release' })).toBe(true)
    expect(startsNewCursorAgent(reusable, { ...base, autoCreatePR: true })).toBe(true)
  })

  test('an identical selection whose reuse was invalidated still starts a new agent', () => {
    expect(startsNewCursorAgent({ options: base, reuseValid: false }, { ...base })).toBe(true)
  })

  test('no previous run never warns about a new agent', () => {
    expect(startsNewCursorAgent(null, base)).toBe(false)
  })
})

describe('Cursor chat request', () => {
  const value = {
    model: cursorModels[0],
    variant: cursorModels[0].variants[1],
    mode: 'plan',
    repositoryUrl: null,
    startingRef: null,
    autoCreatePR: false,
  }

  test('sends the exact upstream variant params', () => {
    const request = cursorChatRequest(value, {
      sessionId: 'ses_1',
      message: 'ship it',
      images: ['data:image/png;base64,AAAA'],
      projectDir: '/home/me/project',
    })
    expect(request.model).toEqual({
      id: 'gpt-5.6-sol',
      params: [
        { id: 'reasoning', value: 'max' },
        { id: 'internal', value: 'off' },
      ],
    })
    expect(request.mode).toBe('plan')
    expect(request.session_id).toBe('ses_1')
    expect(request.images).toEqual(['data:image/png;base64,AAAA'])
    expect(request.project_dir).toBe('/home/me/project')
    expect(request.auto_create_pr).toBe(false)
  })

  test('omits repository overrides so the server discovers the project repo', () => {
    const request = cursorChatRequest(value, { sessionId: '', message: 'hi' })
    expect('repository_url' in request).toBe(false)
    expect('starting_ref' in request).toBe(false)
    expect('project_dir' in request).toBe(false)
  })

  test('an edited repository and ref are sent verbatim, including a cleared repository', () => {
    const edited = cursorChatRequest(
      { ...value, repositoryUrl: 'https://github.com/acme/repo', startingRef: 'main' },
      { sessionId: 'ses_1', message: 'hi' },
    )
    expect(edited.repository_url).toBe('https://github.com/acme/repo')
    expect(edited.starting_ref).toBe('main')

    const cleared = cursorChatRequest(
      { ...value, repositoryUrl: '', startingRef: '' },
      { sessionId: 'ses_1', message: 'hi' },
    )
    expect(cleared.repository_url).toBe('')
    expect(cleared.starting_ref).toBe('')
  })

  test('the request never carries composer-only fields', () => {
    const request = cursorChatRequest(value, { sessionId: 'ses_1', message: 'hi' })
    expect(Object.keys(request).sort()).toEqual([
      'auto_create_pr',
      'images',
      'message',
      'mode',
      'model',
      'session_id',
    ])
  })
})
