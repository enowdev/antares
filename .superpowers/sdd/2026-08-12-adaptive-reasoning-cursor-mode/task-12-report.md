# Task 12 Report: Direct Cursor SSE Coordinator and Recovery

Date: 2026-08-13

## Status

Implemented and verified. The direct Cursor path now uses the existing
`cursorrun.Runner`, instance-owned `agent.Agent` approval gate, durable
`CursorSessionState` CAS operations, exact model validation, strict attachment
decoding, and idempotent assistant commit. It does not introduce another Cursor
client, approval gate, or persistence model.

## Implementation

### Request preparation and approval

- Added `POST /api/chat/cursor`.
- Requires dashboard-password authentication before the route-specific
  105 MiB body decoder or image allocation.
- Strictly validates the requested model and exact parameter selection, mode,
  images, project directory, normalized GitHub repository, starting ref, and
  auto-PR compatibility.
- Preserves the distinction between auto-discovery and explicit no-repository.
- Deep-copies model params and images into a private turn plan.
- Computes agent reuse only when `ReuseValid`, `TargetActive`, `AgentID`, model,
  exact params, repository identity, starting ref, and auto-PR all match. Mode
  is intentionally excluded from reuse identity.
- Atomically reserves one live run before publishing approval. The durable CAS
  remains the arbiter across server revisions/processes.
- Creates or hydrates the Antares session without changing its active Antares
  provider, appends the user message, and records `awaiting_approval`.
- Publishes `EventSession` before the approval request.
- Uses a precomputed immutable approval projection containing operation kind,
  exact model params, repository/ref/source, mode, auto-PR, a credential-redacted
  UTF-8-safe 240-rune prompt preview, and image count.
- Added `Agent.AwaitOperationApproval`, a narrow public adapter over the
  existing instance-owned gate. `deny` refuses immediately; `auto`, `prompt`,
  and unknown-safe modes require a human decision through the existing
  pending/resolve API.

### Approved execution and streaming

- CASes to `create_in_flight` or `run_in_flight` before any mutating Cursor POST.
- Calls `CreateAgent` for a changed identity and `CreateRun` for an eligible
  follow-up.
- Persists returned agent/run IDs before opening Cursor SSE.
- Treats context/transport uncertainty, HTTP 408, and 5xx create outcomes
  without IDs as ambiguous and never auto-retries them.
- Persists Last-Event-ID, remote status, partial reasoning, and partial answer
  before publishing each corresponding live event.
- Treats result text as a canonical whole value, not an appended delta.
- Keeps remote tool activity live-only.
- Bounds and redacts upstream status, text, reasoning, progress, errors, and Git
  fields before publishing or persistence. Prompt image bytes are never stored
  or emitted.
- Handles stream reset by durably clearing Last-Event-ID and both accumulators,
  then clearing/replaying the in-memory presentation.

### Finalization and recovery

- Reconciles the terminal snapshot's whole result, reasoning, status, and
  bounded Git state.
- Uses `CommitCursorAssistant` to atomically append one assistant message and
  mark the matching state committed.
- Reuses the winning terminal revision when finalizers race, so the store's
  transaction remains the exactly-once arbiter.
- Treats durable `terminal` state as unfinished for new turns/history edits
  until the assistant commit is complete.
- `GET /api/chat/attach` first replays the in-memory log at the browser cursor.
- If memory is absent, one watcher is reserved, a reset plus durable partials
  are replayed from cursor zero, and the stream resumes with persisted
  Last-Event-ID.
- A stale cursor from the lost process is never applied to the fresh recovery
  log.
- Recovery fetches/finalizes terminal snapshots without opening SSE, resumes
  active runs, marks create-without-IDs ambiguous, and never recreates a lost
  approval after restart.
- No recoverable state retains ordinary attach behavior and emits `done`.

### Interrupt, cancellation, and history mutation

- HTTP disconnect only detaches that follower.
- `/api/chat/interrupt` invokes the local watcher cancel hook for direct Cursor
  runs and never calls `CancelRun`; the durable run remains recoverable.
- Added `POST /api/chat/cursor/cancel`.
- Cancellation has its own immutable approval operation and shared gate.
- A per-run in-memory reservation serializes approval through the durable
  pre-POST marker.
- CASes `ANTARES_CANCEL_IN_FLIGHT` before `CancelRun`; success records
  `ANTARES_CANCEL_REQUESTED`, and uncertain failure records an ambiguous cancel
  outcome. Repeated calls cannot issue a second cancellation POST.
- Single and category/bulk deletion inspect every selected session before any
  mutation and return 409 for active remote state. Deletion is allowed after an
  approved cancellation request or terminal completion and stops only the
  local watcher.
- Message edit/retry history mutation atomically rejects unfinished Cursor
  state and invalidates reuse before rollback/deletion.
- Ordinary chat and direct Cursor turns now share the per-session reservation,
  preventing a cross-mode race; ordinary chat invalidates the Cursor target
  before running.

## Files

New:

- `internal/server/handlers_cursor.go`
- `internal/server/cursor_events.go`
- `internal/server/handlers_cursor_test.go`
- `.superpowers/sdd/2026-08-12-adaptive-reasoning-cursor-mode/task-12-report.md`

Modified:

- `internal/agent/approval.go`
- `internal/agent/approval_test.go`
- `internal/server/handlers_chat.go`
- `internal/server/livechat.go`
- `internal/server/routes.go`
- `internal/server/server.go`

Explicitly excluded:

- `web/tsconfig.tsbuildinfo`
- pre-existing untracked controller plan/design documents

## TDD Evidence

### Baseline

The affected server/store/runner/approval/agent packages passed before Task 12
changes.

### RED

The initial coordinator matrix was introduced before the production handlers.

Command:

```text
go test ./internal/server -run 'TestCursorChat' -count=1 -v
```

Observed result: build failed because the new `cursorChatRequest`,
`handleCursorChat`, and `handleCursorCancel` production surface did not exist.

The shared public approval adapter was also introduced test-first.

Command:

```text
go test ./internal/agent -run TestAwaitOperationApprovalSharesExplicitCursorPolicy -count=1 -v
```

Observed result:

```text
a.AwaitOperationApproval undefined
FAIL
```

Focused RED iterations then exposed and drove these lifecycle fixes:

```text
TestCursorChatReservationRejectsConcurrentTurnBeforeApproval:
status=400, want 409

TestCursorChatApprovedCancelCallsUpstreamExactlyOnce:
delete after approved cancellation status=409, want 200

TestCursorChatApprovalIsBoundedRedactedAndImmutable:
session event leaked prompt credential in title

TestCursorChatAttachReplaysPersistedPartialsBeforeResuming:
persisted partials were not replayed before resumed events
```

The final restart-cursor regression was also demonstrated independently.

Command:

```text
go test ./internal/server -run TestCursorChatRecoveryIgnoresCursorFromLostInMemoryRun -count=1 -v
```

Output:

```text
=== RUN   TestCursorChatRecoveryIgnoresCursorFromLostInMemoryRun
    handlers_cursor_test.go:1211: SSE stream ended before the next event
--- FAIL: TestCursorChatRecoveryIgnoresCursorFromLostInMemoryRun (0.01s)
FAIL
FAIL	github.com/enowdev/antares/internal/server	0.041s
```

After resetting the cursor only for the absent-memory recovery path:

```text
=== RUN   TestCursorChatRecoveryIgnoresCursorFromLostInMemoryRun
--- PASS: TestCursorChatRecoveryIgnoresCursorFromLostInMemoryRun (0.01s)
PASS
ok  	github.com/enowdev/antares/internal/server	0.038s
```

### GREEN

Brief-specified focused matrix:

```text
$ go test ./internal/server -run 'TestCursorChat|TestChatAttach' -count=1
ok  	github.com/enowdev/antares/internal/server	0.332s
```

Fresh affected-package suite, with all opt-in live credentials removed:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u AZURE_OPENAI_ENDPOINT \
  -u AZURE_OPENAI_KEY -u AZURE_OPENAI_DEPLOYMENT \
  -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN \
  go test ./internal/server ./internal/store ./internal/cursorrun \
    ./internal/approval ./internal/agent -count=1
ok  	github.com/enowdev/antares/internal/server	2.037s
ok  	github.com/enowdev/antares/internal/store	0.622s
ok  	github.com/enowdev/antares/internal/cursorrun	1.112s
ok  	github.com/enowdev/antares/internal/approval	0.030s
ok  	github.com/enowdev/antares/internal/agent	0.351s
```

Full hermetic repository suite:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u AZURE_OPENAI_ENDPOINT \
  -u AZURE_OPENAI_KEY -u AZURE_OPENAI_DEPLOYMENT \
  -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN go test ./...
...
ok  	github.com/enowdev/antares/internal/server	2.117s
...
```

All repository packages passed. Packages without tests reported
`[no test files]`.

Fresh affected-package race suite:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u AZURE_OPENAI_ENDPOINT \
  -u AZURE_OPENAI_KEY -u AZURE_OPENAI_DEPLOYMENT \
  -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN \
  go test -race ./internal/server ./internal/store ./internal/cursorrun \
    ./internal/approval ./internal/agent -count=1
ok  	github.com/enowdev/antares/internal/server	14.654s
ok  	github.com/enowdev/antares/internal/store	6.133s
ok  	github.com/enowdev/antares/internal/cursorrun	3.427s
ok  	github.com/enowdev/antares/internal/approval	1.051s
ok  	github.com/enowdev/antares/internal/agent	2.637s
```

One uncached full-suite attempt saw the unrelated
`internal/mcp.TestStdioRoundTrip` exceed its 10-second deadline under parallel
load. It passed immediately in isolation:

```text
$ go test ./internal/mcp -run TestStdioRoundTrip -count=1 -v
=== RUN   TestStdioRoundTrip
--- PASS: TestStdioRoundTrip (0.03s)
PASS
ok  	github.com/enowdev/antares/internal/mcp	0.055s
```

The subsequent complete hermetic suite passed as shown above.

## Crash-Window and Exactly-Once Review

1. **Before approval:** only Antares session/message/state writes occur; no
   mutating Cursor request is possible.
2. **Approval to POST:** durable state changes to `create_in_flight` or
   `run_in_flight` before the POST.
3. **Crash during create without IDs:** restart sees an in-flight state with no
   recoverable IDs, marks it ambiguous, and never retries.
4. **Create response before ID persistence:** this remains conservatively
   ambiguous after a crash; duplicate paid work is preferred against.
5. **After ID persistence:** recovery uses those exact IDs and Last-Event-ID;
   it does not create another agent/run.
6. **Event persistence before publication:** a crash can cause replay, but
   cannot expose a live partial that lacks its durable counterpart.
7. **Reset:** durable cursor and accumulators are cleared before in-memory
   reset/replay.
8. **Terminal CAS before assistant commit:** `terminal` remains recoverable and
   blocks the next turn/history mutation. `CommitCursorAssistant` transactionally
   appends and marks committed; competing finalizers reuse the winning revision.
9. **Cancellation:** the durable cancel-in-flight marker precedes `CancelRun`.
   A lost response becomes ambiguous/requested and is never submitted twice.
10. **Follower detach/local stop:** neither changes remote state; persisted IDs
    retain the recovery path.
11. **Deletion/edit races:** local lifecycle locking and durable CAS ensure a
    delete/edit cannot pass its precondition and then race a newly reserved
    direct turn.

## Concerns

- The first un-sanitized `go test ./...` invocation inherited opt-in live-test
  credentials from the environment. It performed read-only Cursor metadata
  requests and rejected OpenAI smoke-test requests. It did not invoke Cursor
  create, run, or cancel. Every subsequent full/race command explicitly removed
  all live-test credential variables.
- Ambiguous create and ambiguous cancellation states intentionally remain
  sticky. This prevents duplicate paid/mutating operations but requires future
  explicit operator reconciliation rather than automatic retry.
- No remaining Task 12 functional or race-detector failures are known.
