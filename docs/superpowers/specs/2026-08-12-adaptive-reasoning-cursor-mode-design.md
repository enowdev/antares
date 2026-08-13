# Adaptive Reasoning and Direct Cursor Mode

Date: 2026-08-12  
Status: Approved design

## Summary

Antares will make reasoning controls model-aware and make Cursor Cloud Agent
models directly usable from the web composer.

The composer model search will show two clearly separated execution targets:

- chat models, which continue through Antares' `llm.Client` abstraction; and
- Cursor Cloud Agent models, which execute through a dedicated, approval-gated
  Cursor run path.

Cursor remains an agent integration rather than an active chat provider.
Selecting a Cursor model must not change `model.provider`, `model.default`, or
the model used by ordinary Antares turns.

Reasoning options will no longer come from the global
`none|low|medium|high` list. Antares will expose the exact values supported by
the selected provider and model. Cursor's live model catalogue is authoritative
for Cursor models, including all preset variant parameters.

## Evidence and Root Cause

The connected Cursor account currently returns 34 models from `GET /v1/models`.
Their controls are not uniform:

- Grok models use `effort`, including `xhigh` on some models.
- GPT models use `reasoning`, with model-dependent values including `none`,
  `extra-high`, `xhigh`, and `max`.
- Claude models combine `thinking`, `context`, `effort`, and sometimes `fast`.
- Gemini 3.6 Flash exposes `minimal|low|medium|high`.
- Some models expose no reasoning control.
- Cursor variants may contain required parameters that are not listed as
  user-facing parameters, such as `cyber=false`.

Antares currently loses or bypasses this information in four places:

1. `/api/providers/cursor/models` drops aliases and variants.
2. Cursor models are deliberately excluded from `/api/model/list-all`, but no
   separate usable Cursor picker was added.
3. `cursor_agent` accepts a model ID but not `model.params`.
4. The composer reasoning picker and config schema use a fixed global enum.

The existing provider adapters also do not implement current model semantics
consistently:

- Anthropic uses fixed token budgets for every model instead of modern adaptive
  thinking where supported.
- OpenRouter omits `none`, which can leave reasoning enabled at the model's
  default.
- Gemini maps `none` to minimal thinking but labels it as off and does not
  accept the explicit `minimal` value.

The fix therefore requires an end-to-end capability contract. Adding more
hard-coded picker entries would leave the underlying requests incorrect.

## Authoritative References

- Cursor Cloud Agent API:
  <https://cursor.com/docs/cloud-agent/api/endpoints>
- Cursor SDK model selection:
  <https://cursor.com/docs/sdk/typescript>
- Cursor Router:
  <https://cursor.com/docs/cursor-router>
- Anthropic adaptive thinking and effort:
  <https://docs.anthropic.com/en/docs/build-with-claude/thinking>
  and <https://docs.anthropic.com/en/docs/build-with-claude/effort>
- Gemini thinking:
  <https://ai.google.dev/gemini-api/docs/thinking>
- OpenRouter reasoning metadata:
  <https://openrouter.ai/docs/guides/best-practices/reasoning-tokens>

## Goals

1. Make every model available to the connected Cursor key searchable and
   selectable from the composer.
2. Run a selected Cursor model directly, without asking an Antares LLM to
   manufacture a `cursor_agent` tool call.
3. Preserve Cursor's exact model variant, including hidden parameters.
4. Reuse the same Cursor agent for consecutive Cursor-mode follow-ups.
5. Require an explicit approval before every paid or mutating Cursor action.
6. Derive chat reasoning choices from the selected provider and model.
7. Correct provider request bodies so every displayed choice has the advertised
   effect.
8. Preserve existing configurations and keep Cursor isolated from active chat
   provider selection.

## Non-Goals

- Implementing Cursor as an `llm.Client`.
- Sending Antares' local tool registry to Cursor.
- Inventing a task-complexity classifier in Antares.
- Guessing unsupported reasoning values for unknown providers.
- Treating local uncommitted files as if they existed in a Cursor cloud VM.
- Automatically retrying non-idempotent create-agent or create-run requests.
- Replacing the existing `cursor_agent` and `cursor_agent_status` tools.

## Core Concepts

### Execution target

The composer tracks an execution target:

```text
chat:
  provider
  model
  reasoning override

cursor:
  model
  exact variant params
  conversation mode
  repository and starting ref
  auto-create-PR setting
```

This is a UI and request-level distinction. It does not add Cursor to
`model.provider`.

### Reasoning capability

Chat model metadata receives a structured capability:

```json
{
  "reasoning_capability": {
    "values": [
      { "value": "none", "label": "Off", "kind": "disable" },
      { "value": "low", "label": "Low" },
      { "value": "medium", "label": "Medium" }
    ],
    "default": "medium",
    "mandatory": false,
    "can_disable": true,
    "source": "live"
  }
}
```

`values` are opaque provider values. Antares must not normalize
`extra-high` into `xhigh`, or otherwise assume that similar labels share a wire
format. The optional `kind: "disable"` marker, rather than the value's spelling,
determines whether the UI renders an Off choice. `can_disable` must agree with
the presence of exactly one marked disable value; mandatory capabilities have
neither.

An absent capability means the UI offers only Auto. Auto is represented by no
override and lets the provider apply its model default or native adaptive
reasoning.

### Cursor model selection

Cursor model selections use the existing upstream shape:

```json
{
  "id": "gpt-5.6-sol",
  "params": [
    { "id": "context", "value": "1m" },
    { "id": "reasoning", "value": "max" },
    { "id": "fast", "value": "true" }
  ]
}
```

Antares selects one concrete variant returned by Cursor and copies its complete
`params` array. It never synthesizes a Cartesian product from
`model.parameters`.

## Architecture

### 1. Separate catalogues, unified search

The backend continues to keep chat and agent providers separate:

- `GET /api/model/list-all` returns chat models and their reasoning capability.
- `GET /api/providers/cursor/models` returns Cursor model IDs, names,
  descriptions, aliases, parameters, and variants.

The frontend merges these responses only for search and presentation. Results
are grouped as `Chat models` and `Cursor Cloud Agents`.

The existing `/api/model/set` endpoint continues to reject Cursor. A Cursor
result is never marked as the globally active chat model.

Cursor catalogue responses are cached for five minutes per resolved provider
configuration. Connecting a new key or changing Cursor settings invalidates the
cache. A selection rejected as stale causes one catalogue refresh before
Antares returns an actionable reselect error.

### 2. Shared Cursor run service

Remote Cursor lifecycle logic moves behind a service shared by:

- the existing `cursor_agent` tool;
- the existing `cursor_agent_status` tool where applicable; and
- the direct web Cursor coordinator.

The service owns:

- model and variant validation;
- request encoding;
- agent creation and follow-up runs;
- cancellation and status reads;
- resumable SSE handling;
- bounded progress conversion;
- typed error classification; and
- key redaction.

The tool and web adapters remain responsible for their own input shape,
approval presentation, and result rendering.

### 3. Direct Cursor chat route

`POST /api/chat/cursor` accepts a Cursor turn and returns the same Antares SSE
event envelope used by `/api/chat`.

The request contains:

- session ID and prompt;
- up to five supported images;
- selected model ID and exact variant params;
- Cursor mode (`agent` or `plan`);
- project directory for repository discovery;
- optional edited repository URL and starting ref; and
- auto-create-PR preference.

The route starts a background coordinator and publishes through the existing
live-run hub. Browser disconnects therefore detach the viewer without
terminating the Cursor run. `/api/chat/attach` can replay events from the
in-memory run.

If the daemon restarted and no live run exists, attach checks persisted Cursor
run metadata. A non-terminal remote run starts a recovery watcher using the
stored `agent_id` and `run_id`; a terminal run hydrates its final state.

The route persists ordinary user and assistant transcript messages. Cursor
status, reasoning summaries, tool progress, final text, branches, and pull
requests use existing event and message segments where possible.

### 4. Approval gate

The approval mechanism is extracted from tool-call-specific code into a shared
operation gate. Mutating Cursor operations use it from both direct mode and the
tool adapter.

Cursor start, follow-up, and cancel always require an explicit human decision,
even when the general tool approval mode is `auto`.

The approval payload is immutable and includes:

- operation;
- a bounded prompt preview and attachment count;
- model ID and every parameter;
- repository and starting ref;
- Cursor conversation mode;
- auto-create-PR setting; and
- whether the operation creates a new agent or follows up.

The server retains the exact pending request behind an opaque approval ID. The
card is a display projection, not a second client-submitted request. Approval
executes the retained request exactly; editing a composer selection after the
card appears cannot change the pending operation.

No Cursor create or cancel request is sent before approval.

### 5. Session lifecycle

A Cursor-mode session stores:

- current model ID and variant params;
- repository and starting ref;
- Cursor mode;
- `agent_id` and latest `run_id`;
- remote status; and
- whether the last target transition invalidated follow-up reuse.

Consecutive Cursor turns with the same model, variant, repository, starting
ref, and auto-create-PR setting call Create Run on the existing agent. Cursor
mode may change per follow-up because Create Run supports a mode override.

Changing model, variant, repository, starting ref, or auto-create-PR starts a
new Cursor agent after approval. Starting a new Antares chat also starts a new
Cursor agent.

Only one direct turn may run per Antares session. Selection changes can be
prepared while idle but cannot start a competing run in the same session.

Switching from Cursor mode to a chat model ends automatic reuse for that Cursor
chain. Switching back creates a new agent, because intervening Antares messages
were not part of the remote Cursor conversation.

Stopping local streaming does not cancel the remote run. Remote cancellation is
an explicit approved action.

After Create Agent returns, Antares persists the agent and run IDs before it
starts remote streaming. A process failure after Cursor accepts a create
request must not cause Antares to retry that create request.

## Repository Discovery

For a project-bound chat, the backend inspects the project repository:

1. Read the `origin` URL.
2. Normalize GitHub SSH forms such as `git@github.com:owner/repo.git` to
   `https://github.com/owner/repo`.
3. Resolve the current branch or detached commit as the proposed starting ref.
4. Report whether the worktree is dirty or has commits not present on the
   selected remote ref.

The composer preflight displays this information in Cursor options. The user
may edit the repository or ref before sending. The server independently
normalizes and validates the submitted values again.

No-project chats default to a no-repository run. Local file paths, credentials
embedded in remote URLs, non-GitHub repositories, and non-HTTPS normalized
destinations are rejected.

The approval card warns that dirty files and local-only commits are not present
in the cloud VM.

## User Experience

### Model search

The composer model search:

- searches ID, display name, aliases, and provider;
- labels every Cursor result as `Cursor Cloud Agent`;
- shows a Connect Cursor action when the provider has no resolved key;
- preserves the active chat model while Cursor is selected; and
- remembers the last target per session.

The Providers page remains the credential-management surface. Its Cursor model
section gains search and displays parameter/variant summaries, but selection
for execution happens in the composer.

### Cursor options

Selecting a Cursor model chooses the `isDefault` variant. If no variant is
marked default, Antares chooses the first returned variant. A model with no
variants uses an empty parameter list.

The main chip shows `Cursor · <model>`. A Cursor options popover exposes:

- the reasoning-like axis from `reasoning`, `effort`, or `thinking`;
- other dimensions such as Context and Fast;
- Agent or Plan mode;
- repository and starting ref; and
- auto-create-PR.

Controls act as filters over concrete variants. A choice is committed only when
one exact variant matches. Values and labels come from Cursor's catalogue.

`auto-smart` and its `optimize_for` choices appear only when returned for the
connected account. Antares does not hard-code team-entitled models.

### Chat reasoning picker

For a chat target:

- Auto is always available and sends no override.
- Only capability values for the selected provider/model are shown.
- Off appears only when the capability can actually disable reasoning.
- Mandatory reasoning models do not show Off.
- The selection is stored by `provider/model`.
- Changing models restores that model's prior valid value or Auto.

The same capability source drives the Roles editor and configuration UI.
Role values are validated against the role's explicit model, or against the
inherited active model when no model is specified.

### Attachments

Cursor mode supports up to five image inputs accepted by the Cursor API.
Unsupported MIME types and oversized images fail before approval.

Local non-image attachments are rejected in Cursor mode with a clear
explanation. They are not silently dropped, and local paths are never sent as
if the cloud VM could read them.

## Provider-Specific Reasoning

### Cursor

The live catalogue is the sole source of model params and variants. Antares
does not translate generic chat reasoning into Cursor params.

### OpenRouter

When present, model metadata fields `reasoning.supported_efforts`,
`default_effort`, `default_enabled`, `mandatory`, and `supports_max_tokens` are
authoritative.

The adapter sends an explicit disable value when supported. It does not omit a
user-selected `none` and accidentally fall back to enabled reasoning.

### Anthropic

The resolver distinguishes modern adaptive-thinking models from legacy
extended-thinking models.

Modern supported models use:

```json
{
  "thinking": { "type": "adaptive" },
  "output_config": { "effort": "<model-supported value>" }
}
```

Model-specific ladders follow Anthropic's published capability table. Legacy
models retain fixed-budget behavior only where the upstream API supports it.
Unsupported values are never silently mapped to a nearby value.

### Gemini

The resolver exposes each model's published thinking levels. Gemini 3 models
use `thinkingLevel`; `minimal` is represented as Minimal rather than Off.
Models that cannot disable dynamic thinking do not offer Off.

Legacy budget configuration remains only for models whose API contract still
requires it.

### OpenAI and Codex

Known model families receive their documented effort ladder. OpenAI chat-style
requests use their supported reasoning-effort field; Responses/Codex requests
use the nested reasoning object.

When the provider model endpoint supplies richer live metadata, live metadata
wins. Unknown models fall back to Auto rather than inheriting a guessed global
ladder.

### Other OpenAI-compatible providers

A provider may expose a reasoning capability through model metadata. Without
that metadata or a tested provider-specific resolver, Antares offers only Auto.

## Validation and Error Handling

Both frontend and backend validate selections, but the backend is authoritative.

The backend returns specific errors for:

- missing Cursor credentials;
- model or variant no longer available;
- unsupported reasoning value;
- malformed or non-GitHub repository;
- image count, type, or size violations;
- agent busy conflicts;
- authentication and authorization failures;
- rate limits, including retry-after metadata;
- local wait cancellation while the remote run may still be active; and
- terminal Cursor failures.

Create-agent and create-run requests are never automatically retried.
Idempotent metadata reads and SSE reconnections retain their existing bounded
retry behavior.

No upstream error, approval payload, log, event, or persisted metadata may
contain an API key.

## Interrupt and Recovery Semantics

- Browser navigation or network loss detaches from the local event stream.
- Stop interrupts local waiting and reports that the remote run may continue.
- Cancel requests remote cancellation and requires approval.
- Reattach first uses the in-memory live-run replay buffer.
- After a daemon restart, persisted IDs allow status recovery and a new remote
  stream watcher.
- A completed run always wins over a later retryable stream read error.

## Compatibility and Migration

Existing YAML remains valid. `reasoning_effort` remains a string so existing
automation does not require a format migration.

The static config schema enum is replaced by model-aware validation. Existing
values are accepted when supported by the selected model. Unsupported stored
values resolve to Auto and surface a one-time UI notice rather than being sent
upstream.

This compatibility rule applies while loading old persisted configuration.
An explicit new API request carrying an unsupported value returns a validation
error; it is not silently converted to Auto.

The old browser key `antares:reasoning` is migrated once:

- copy it into the active `provider/model` preference if valid;
- otherwise select Auto; and
- remove the old global key.

Existing Cursor tool callers that send only `model` remain valid. The tool gains
optional model params; omitting them preserves Cursor's model default behavior.

## Testing Strategy

### Unit tests

- Reasoning capability resolution for each supported provider and representative
  model family.
- Exact Anthropic, OpenAI, Codex, OpenRouter, and Gemini request bodies.
- Auto omission, explicit disable, mandatory reasoning, and invalid values.
- Cursor model and variant validation, including params not listed in the
  user-facing parameter definitions.
- Repository normalization, dirty/ahead detection, and rejection cases.
- Approval immutability and no upstream request before approval.

### Server and service tests

- Full Cursor catalogue response, including aliases, params, and variants.
- Five-minute cache and invalidation behavior with an injected clock.
- Direct start, follow-up reuse, and new-agent behavior after target changes.
- Browser detach, in-memory attach, daemon-style recovery from persisted IDs,
  local stop, and approved remote cancel.
- Auth, 409, 429, stale model, stream reconnect, terminal error, and key
  redaction paths.
- Regression that `/api/model/set`, CLI model selection, TUI provider selection,
  and `llm.New` still reject Cursor as a chat provider.

### Frontend tests

- Grouped search over chat and Cursor models, including aliases.
- Cursor connection and catalogue errors.
- Per-model reasoning persistence and migration from the global key.
- Hidden/unsupported reasoning controls.
- Variant filtering that always resolves to an upstream variant.
- Approval contents and immutable pending selection.
- Repo/ref warnings, unsupported local files, and model-change new-agent notice.

### Integration and verification

- End-to-end direct mode against a fake Cursor server.
- Existing Go package tests and race tests.
- Frontend unit tests, typecheck, and production build.
- Full Antares build.
- Local daemon restart and browser smoke test.
- Optional live Cursor metadata test only; automated verification must not
  create a paid remote run.

## Acceptance Criteria

1. A connected Cursor account's complete model catalogue appears in composer
   search without changing the active Antares chat provider.
2. Choosing a Cursor model and variant results in the exact selected
   `model.id` and `model.params` after explicit approval.
3. A consecutive Cursor turn reuses the same agent; changing model, variant,
   repo/ref, or auto-create-PR creates a new one.
4. Cursor runs survive browser detachment and can be recovered after daemon
   restart from persisted IDs.
5. Chat reasoning choices match the selected model and provider, including
   values beyond low/medium/high where supported.
6. Auto sends no override and defers to native provider/model behavior.
7. A displayed Off choice actually disables reasoning; otherwise Off is absent.
8. Stale or unsupported values fail before an upstream model request.
9. Cursor never becomes the active chat provider through web, CLI, TUI, config,
   or direct `llm.New` paths.
10. No secret is exposed and no paid Cursor mutation occurs before approval.
