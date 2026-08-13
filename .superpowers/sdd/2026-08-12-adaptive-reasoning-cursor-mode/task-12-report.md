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

## Fix Round 1 (2026-08-13)

### Status

Addressed all nine lifecycle/security review findings with focused
red-green tests. Ambiguous create/cancel outcomes remain non-retryable, but
are now explicitly described and locally deletable without issuing any remote
retry or cancellation.

### Implementation

1. Split direct-run local control into explicit approval, create, and watch
   phases. Stop cancels approval before the POST boundary, records detachment
   without cancelling a non-idempotent create POST, and installs/cancels the
   watcher only after returned IDs are durable.
2. Restored ordinary-chat supersede semantics: under the session lifecycle
   lock, ordinary chat rejects unfinished Cursor state, invalidates reuse, and
   replaces the hub entry with `put`.
3. Classified cancellation outcomes:
   - context/transport uncertainty, API status zero, HTTP 408, and 5xx become
     `ANTARES_CANCEL_OUTCOME_AMBIGUOUS`;
   - definitive 4xx restores the pre-POST status and permits a new approved
     attempt;
   - 404 records `ANTARES_CANCEL_NO_ACTIVE_RUN`, invalidates reuse, and returns
     success;
   - a durable cancel-in-flight marker without a matching process-local
     reservation is reconciled to cancel-ambiguous and never re-submitted.
4. Marked live logs as ordinary, direct Cursor, or Cursor recovery. Attach now
   requires dashboard protection before selecting any credential-using direct
   or recovery path and before any runner call; ordinary live/done attach stays
   compatible.
5. Replaced the global lifecycle mutex with reference-counted keyed session
   locks. Bulk deletion acquires unique IDs in sorted order. Direct/ordinary
   reservation, attach recovery reservation, terminal finalization, edit,
   single delete, and bulk delete use the same per-session lock.
6. Added immutable approval fields for dirty worktree, local-only commit count,
   remote-ref-known, and fixed bounded/redacted warnings. The warnings explain
   that local dirty/local-only work is absent from the Cursor cloud VM and that
   an unknown remote ref prevents verification. Reuse identity is unchanged.
7. Cursor reset is now derived from both initial absence and the selected live
   log kind. A newly selected recovery log, including another recovery winner,
   resets to zero; concurrent ordinary/direct logs and existing reconnect logs
   preserve the browser cursor.
8. Cancellation's in-memory reservation is session-wide rather than run-ID
   specific.
9. Added exported `store.ErrCursorRevisionConflict`; server conflict handling
   uses `errors.Is` and preserves the existing safe user-facing behavior.

Ambiguous create state and cancel-ambiguous state are accepted by both single
and bulk local deletion. New turns/cancel retries explain that deletion is the
operator reconciliation escape. Deletion only removes local data and never
calls `CreateAgent`, `CreateRun`, or `CancelRun`.

### Files

New:

- `internal/server/cursor_lifecycle.go`
- `internal/server/cursor_lifecycle_test.go`
- `internal/server/handlers_cursor_fix_test.go`

Modified:

- `internal/server/cursor_events.go`
- `internal/server/handlers_chat.go`
- `internal/server/handlers_cursor.go`
- `internal/server/handlers_cursor_test.go`
- `internal/server/livechat.go`
- `internal/server/server.go`
- `internal/store/cursor_sessions.go`
- `internal/store/cursor_sessions_test.go`
- `internal/store/sql.go`
- `.superpowers/sdd/2026-08-12-adaptive-reasoning-cursor-mode/task-12-report.md`

Explicitly excluded from staging:

- `web/tsconfig.tsbuildinfo`
- pre-existing untracked controller plan/design documents

### Focused TDD evidence

RED was established before each production change.

Lifecycle/cancel/projection matrix:

```text
$ go test ./internal/server -run 'TestCursor(ChatStopDuringCreate|ChatAmbiguousStates|OrdinaryChatSecond|Cancel|ApprovalCarries)' -count=1 -v
TestCursorChatAmbiguousStatesCanBeDeletedLocally:
  body={"error":"a turn is already active for this session"}, want local-delete escape
TestCursorCancelDefinitiveFailuresRestoreStatusAndAllowRetry:
  HTTP 400 returned 502; other 4xx left ANTARES_CANCEL_OUTCOME_AMBIGUOUS
TestCursorCancelNotFoundReconcilesNoActiveRun:
  status=404, want 200
TestCursorCancelInFlightRecoveryBecomesAmbiguousWithoutResubmission:
  durable status remained ANTARES_CANCEL_IN_FLIGHT
TestCursorCancelReservationCoversWholeSession:
  different run bypassed the reservation
TestCursorApprovalCarriesRepositoryPreflightWarnings:
  dirty=false local-only=0 remote-known=false warnings=[]
TestCursorChatStopDuringCreatePersistsIDsAndDefersWatching:
  state became ambiguous with context canceled instead of persisting IDs
FAIL
```

Ordinary supersede:

```text
$ go test ./internal/server -run TestOrdinaryChatSecondTurnSupersedesLiveRun -count=1 -v
second ordinary turn lost supersede compatibility: cursor session is active
FAIL
```

Attach, terminal race, and unrelated-session isolation:

```text
$ go test ./internal/server -run 'TestCursorAttach|TestCursorTerminalRecovery|TestCursorLifecycleSlowSession' -count=1 -v
unprotected Cursor recovery attach status=200, want 428
unprotected direct live attach status=200, want 428
delete racing terminal recovery status=200, want 409
slow session lifecycle blocked an unrelated session
FAIL
```

New lock, cursor-reset, and store-sentinel surfaces:

```text
$ go test ./internal/server -run TestSessionLocker -count=1
undefined: sessionLocker
FAIL

$ go test ./internal/server -run TestCursorAttachCursorResetTargetsFreshRecoveryLogOnly -count=1
undefined: cursorAttachShouldReset
FAIL

$ go test ./internal/store -run 'TestCursorSessionPutFailuresDoNotMutateCaller/revision_conflict' -count=1
undefined: ErrCursorRevisionConflict
FAIL
```

Additional crash-marker and remote-ref cases:

```text
TestCursorCancelInFlightMarkerAfterRestartIsLocallyDeletable:
  delete status=409, want 200

TestCursorApprovalWarnsWhenRemoteRefCannotBeVerified:
  remote-ref-known=false warnings=[]

TestCursorCancelUncertainFailuresRemainAmbiguousAndDeletable/api_transport:
  status restored to RUNNING instead of cancel-ambiguous
```

Final focused GREEN:

```text
$ go test ./internal/server -run 'Test(CursorChatStopDuringCreatePersistsIDsAndDefersWatching|CursorChatAmbiguousStatesCanBeDeletedLocally|OrdinaryChatSecondTurnSupersedesLiveRun|CursorCancel|CursorAttach|CursorTerminalRecovery|CursorLifecycleSlowSession|SessionLocker|CursorApproval)' -count=1 && \
  go test ./internal/store -run 'TestCursorSessionPutFailuresDoNotMutateCaller/revision_conflict' -count=1
ok  	github.com/enowdev/antares/internal/server	0.550s
ok  	github.com/enowdev/antares/internal/store	0.018s
```

### Full verification

Fresh hermetic full repository suite:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u AZURE_OPENAI_ENDPOINT \
  -u AZURE_OPENAI_KEY -u AZURE_OPENAI_DEPLOYMENT \
  -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN go test ./... -count=1
...
ok  	github.com/enowdev/antares/internal/server	3.532s
ok  	github.com/enowdev/antares/internal/store	1.285s
...
```

All repository packages passed.

Fresh affected-package race suite:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u AZURE_OPENAI_ENDPOINT \
  -u AZURE_OPENAI_KEY -u AZURE_OPENAI_DEPLOYMENT \
  -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN \
  go test -race ./internal/server ./internal/store ./internal/cursorrun \
    ./internal/approval ./internal/agent -count=1
ok  	github.com/enowdev/antares/internal/server	16.533s
ok  	github.com/enowdev/antares/internal/store	5.405s
ok  	github.com/enowdev/antares/internal/cursorrun	3.367s
ok  	github.com/enowdev/antares/internal/approval	1.049s
ok  	github.com/enowdev/antares/internal/agent	2.624s
```

### Crash-window and exactly-once self-review

1. Stop before the POST boundary cancels approval or rolls the durable
   in-flight marker back to idle without a remote mutation.
2. Once create enters its non-idempotent phase, its context is independent of
   local Stop. Returned IDs are CAS-persisted before detachment can skip or
   cancel a watcher.
3. A process crash or true transport uncertainty before IDs remains
   non-retryable. Local delete is the explicit escape and performs no remote
   operation.
4. The cancel marker is durable before `CancelRun`. A locally reserved request
   is distinguished from a marker recovered after restart; only the latter is
   converted to ambiguous. No ambiguous request is automatically submitted.
5. Definitive cancel rejection restores the exact status captured immediately
   before the marker. If that restore itself cannot commit, the marker remains
   conservative and becomes ambiguous rather than permitting a duplicate.
6. Per-session locking orders recovery reservation, terminal commit, history
   mutation, and deletion. A terminal recovery live log blocks deletion/edit
   until finalization; unrelated sessions do not share a lock.
7. Bulk deletion acquires sorted unique session IDs before checking any state,
   retaining all-or-nothing precondition checks without deadlock.
8. Approval warnings are generated from booleans/counts, not raw Git output,
   then redacted and capped at four entries of 240 runes. Prompt/repository reuse
   identity is unchanged.
9. Recovery auth is checked before `GetRun` or `StreamRun`. Ordinary live/done
   attach does not enter the protected path.

### Concerns

- The intentionally deferred full-accumulator O(n²) persistence concern remains
  outside Fix Round 1.
- A true create/cancel transport ambiguity cannot establish remote truth. It is
  deliberately never retried; operators may delete only the local session and
  reconcile any remote Cursor work separately.
- No live API calls or credentials were used. No remaining functional or
  race-detector failures are known.

## Fix Round 2 (2026-08-13)

### Status

Closed the recovery-log cursor reset gap and protected the two remaining
session cleanup escape paths. Ambiguous cancellation failures now return one
bounded non-retryable response, and the deletion predicate no longer performs
durable reconciliation as a side effect.

### Implementation

1. Every attach whose selected live log is `liveRunCursorRecovery` starts at
   cursor zero. This applies to sequential followers and followers of an
   already-reserved concurrent recovery winner. Ordinary and direct live logs
   continue from the caller cursor.
2. `handleDeleteEmpty` and `handlePruneSessions` now enumerate all candidate
   sessions in 500-entry pages, acquire the sorted keyed session locks, and
   precheck every candidate before any cleanup mutation. Existing active remote
   state returns HTTP 409.
3. Store cleanup SQL now excludes Cursor states in `awaiting_approval`,
   `create_in_flight`, and ordinary `run_in_flight` states at mutation time.
   This closes the enumeration-to-delete race with foreign keys both enabled
   and disabled.
4. Explicitly deletable states remain deletable: create-ambiguous,
   cancel-requested, cancel-ambiguous, and stale cancel-in-flight markers.
   Counts remain the number of sessions actually deleted.
5. `cursorSessionHasActiveRemoteState` is now read-only. A process-local cancel
   reservation makes a current cancel-in-flight marker active; the same durable
   marker without that reservation is a stale local-reconciliation candidate.
   Durable crash-marker reconciliation remains in direct-turn preparation,
   explicit cancellation, and recovery.
6. Context/transport uncertainty, HTTP 408, and 5xx cancellation failures now
   always return HTTP 502 with a fixed bounded message stating that the outcome
   is ambiguous and will not be retried. No upstream error body or retry hint is
   forwarded. Definitive 4xx and 404 behavior is unchanged.
7. Removed the write-only `cursorLivePhase` type and `phase` field. The live
   state machine is represented only by `done`, `detached`, and the currently
   installed local stop function.

CAS backoff and full-accumulator persistence remain deferred.

### Files

New:

- `internal/server/handlers_cursor_cleanup_test.go`

Modified:

- `internal/server/cursor_events.go`
- `internal/server/handlers_chat.go`
- `internal/server/handlers_cursor_fix_test.go`
- `internal/server/handlers_cursor_test.go`
- `internal/server/livechat.go`
- `internal/store/cursor_sessions_test.go`
- `internal/store/sessions.go`
- `.superpowers/sdd/2026-08-12-adaptive-reasoning-cursor-mode/task-12-report.md`

Explicitly excluded:

- `web/tsconfig.tsbuildinfo`
- pre-existing untracked controller plan/design documents

### Focused TDD evidence

Recovery-log reset RED:

```text
$ go test ./internal/server -run 'TestCursorAttach(EverySequentialRecoveryFollowerStartsAtZero|ConcurrentFollowersResetToRecoveryWinner|CursorResetTargetsFreshRecoveryLogOnly)' -count=1 -v
existing_recovery_reconnect: cursor reset=false, want true
TestCursorAttachEverySequentialRecoveryFollowerStartsAtZero:
  SSE stream ended before the next event
TestCursorAttachConcurrentFollowersResetToRecoveryWinner:
  SSE stream ended before the next event
FAIL
```

Cleanup/predicate RED:

```text
$ go test ./internal/server -run 'TestCursor(CleanupHandlersPrecheckActiveStateAcrossPagination|ActiveRemotePredicateIsPureForCancelCrashMarker)' -count=1 -v
empty cleanup status=200, want 409
prune cleanup status=200, want 409
predicate mutated crash marker to "ANTARES_CANCEL_OUTCOME_AMBIGUOUS"
FAIL

$ go test ./internal/store -run TestCursorCleanupSkipsActiveStatesWithAndWithoutForeignKeys -count=1 -v
foreign_keys_on/delete_empty: deleted=9, want 5
foreign_keys_on/prune: deleted=9, want 5
foreign_keys_off/delete_empty: deleted=9, want 5
foreign_keys_off/prune: deleted=9, want 5
FAIL
```

Ambiguous cancellation response RED:

```text
$ go test ./internal/server -run TestCursorCancelAmbiguousResponseIsBoundedAndNonRetryable -count=1 -v
request_timeout: status=408, want 502
server: status=503, want 502
context: response did not state ambiguous/non-retryable
transport: response forwarded the bounded upstream error instead of a fixed message
FAIL
```

Final focused GREEN:

```text
$ go test ./internal/server -run 'TestCursor(AttachEverySequentialRecoveryFollowerStartsAtZero|AttachConcurrentFollowersResetToRecoveryWinner|CleanupHandlersPrecheckActiveStateAcrossPagination|ActiveRemotePredicateIsPureForCancelCrashMarker|CancelAmbiguousResponseIsBoundedAndNonRetryable)' -count=1 && \
  go test ./internal/store -run 'TestCursor(CleanupSkipsActiveStatesWithAndWithoutForeignKeys|SessionDeleteEmptySessionsRemovesStateWithoutForeignKeys|SessionBulkDeleteRollsBackOnChildFailure|SessionPruneSessionsRemovesStateWithoutForeignKeys)' -count=1
ok  	github.com/enowdev/antares/internal/server	0.162s
ok  	github.com/enowdev/antares/internal/store	0.097s
```

### Affected and race verification

Fresh affected-package suite:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u AZURE_OPENAI_ENDPOINT \
  -u AZURE_OPENAI_KEY -u AZURE_OPENAI_DEPLOYMENT \
  -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN \
  go test ./internal/server ./internal/store ./internal/cursorrun \
    ./internal/approval ./internal/agent -count=1
ok  	github.com/enowdev/antares/internal/server	2.951s
ok  	github.com/enowdev/antares/internal/store	1.069s
ok  	github.com/enowdev/antares/internal/cursorrun	1.186s
ok  	github.com/enowdev/antares/internal/approval	0.031s
ok  	github.com/enowdev/antares/internal/agent	0.332s
```

Fresh affected-package race suite:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u AZURE_OPENAI_ENDPOINT \
  -u AZURE_OPENAI_KEY -u AZURE_OPENAI_DEPLOYMENT \
  -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN \
  go test -race ./internal/server ./internal/store ./internal/cursorrun \
    ./internal/approval ./internal/agent -count=1
ok  	github.com/enowdev/antares/internal/server	25.524s
ok  	github.com/enowdev/antares/internal/store	9.513s
ok  	github.com/enowdev/antares/internal/cursorrun	4.227s
ok  	github.com/enowdev/antares/internal/approval	1.049s
ok  	github.com/enowdev/antares/internal/agent	3.004s
```

### Safety self-review

1. Recovery-log cursor zero is derived solely from immutable live-log kind.
   Replaying its leading reset and durable snapshots is idempotent for every
   follower; a stale cursor from the pre-crash process cannot skip them.
2. Cleanup prechecks hold every enumerated session lock through mutation.
   Direct/ordinary reservation, recovery, terminal finalization, edit, and
   explicit deletion therefore cannot cross the check/mutation boundary for
   those sessions.
3. The store-side `NOT EXISTS` predicate independently skips newly visible
   active Cursor states at the delete statement, including states inserted
   after server enumeration. FK-off child cleanup remains transactional.
4. Cleanup prechecking is all-or-nothing and read-only. Encountering a later
   active candidate cannot mutate an earlier stale cancel marker.
5. A current cancel POST is protected by the process-local reservation. After a
   restart, the same marker is locally deletable without a write during
   precheck; recovery/direct/cancel paths still reconcile it durably before
   remote lifecycle work.
6. Ambiguous cancellation never exposes the upstream error, never returns its
   408/5xx status, and retains the durable no-resubmit marker.

### Concerns

- The requested affected and race suites pass.
- A parallel full-repository attempt stalled for more than four minutes while
  entering the pre-existing MCP test region and was terminated; the known
  `internal/mcp.TestStdioRoundTrip` passed immediately in isolation.
- A serial full-repository attempt passed MCP but hit the unrelated flaky
  `TestModelSetConcurrentWithConfigReads` async-save assertion; that test passed
  immediately in isolation.
- No live API calls or credentials were used. CAS backoff and full-accumulator
  persistence remain intentionally deferred.

## Fix Round 3 (2026-08-13)

### Status

Closed the compacted recovery replay gap, preserved terminal-but-uncommitted
work from automatic cleanup, made category deletion enumerate the full stable
session listing, and classified local missing Cursor configuration as a
definitive cancellation failure.

### Implementation

1. `liveRun` now folds text and reasoning from evicted original events into a
   bounded replay checkpoint. A follower whose absolute cursor is behind the
   retained window receives `EventReset`, the complete checkpoint reasoning and
   text snapshots, then the retained post-checkpoint events.
2. The checkpoint does not consume original absolute cursor positions.
   Followers already at or ahead of `base` continue without a reset. A
   checkpoint advances the reconnect cursor to `base` only on its final frame;
   a disconnect between reset/reasoning/text therefore repeats the full anchor
   instead of skipping a partially delivered snapshot.
3. The retained original-event window remains capped at `maxLiveEvents`.
   Reasoning and text checkpoints are independently capped at
   `maxCursorPartialRunes`, are UTF-8 safe, and reset with `EventReset`.
   Existing short-run event ordering is unchanged. The generic implementation
   also makes compacted ordinary and direct logs safer.
4. Automatic empty/prune prechecks now treat
   `CursorOperationTerminal` as protected unfinished work even without an
   in-memory watcher. The store-side cleanup predicate independently excludes
   terminal state at mutation time for both foreign-key modes.
5. Explicit single and category deletion still use the operator policy:
   terminal state without a live watcher, create ambiguity, cancel ambiguity,
   and stale cancellation markers remain locally deletable. Active state and a
   process-local cancellation reservation still return HTTP 409.
6. Category delete-all now enumerates the complete session list in stable
   500-entry pages before applying `chat`/`project`/`all` filtering. It acquires
   sorted keyed locks, prechecks every selected session, and submits the full
   selected ID set only after all prechecks succeed.
7. `ListSessions` now adds `id ASC` as the final ordering key, making offset
   pages deterministic when pinned and timestamp/order values tie.
8. `cursorrun.ErrNotConfigured`, including wrapped instances, is definitive:
   cancellation restores the exact prior remote status and returns HTTP 428
   with the actionable configuration error. Context, transport, HTTP 408, 5xx,
   definitive API 4xx, and 404 classifications are otherwise unchanged.

CAS backoff and the full persisted-accumulator rewrite remain deferred.

### Files

Modified:

- `internal/server/cursor_events.go`
- `internal/server/handlers_chat.go`
- `internal/server/handlers_cursor.go`
- `internal/server/handlers_cursor_cleanup_test.go`
- `internal/server/handlers_cursor_fix_test.go`
- `internal/server/livechat.go`
- `internal/server/livechat_test.go`
- `internal/store/cursor_sessions_test.go`
- `internal/store/sessions.go`
- `.superpowers/sdd/2026-08-12-adaptive-reasoning-cursor-mode/task-12-report.md`

Explicitly excluded:

- `web/tsconfig.tsbuildinfo`
- pre-existing untracked controller plan/design documents

### Focused TDD evidence

Compacted replay RED:

```text
$ go test ./internal/server -run 'TestLiveRun_(CoalescesBacklogAndReportsAbsoluteCursor|CompactionRetainsCanonicalReplayCheckpoint)$' -count=1
TestLiveRun_CoalescesBacklogAndReportsAbsoluteCursor:
  4001 backlog events produced 2 frames, want 4
TestLiveRun_CompactionRetainsCanonicalReplayCheckpoint:
  reconnect 1 canonical mismatch: text=11994/12408 reasoning=12000/12414
FAIL
```

Mid-anchor reconnect RED found while verifying absolute cursor behavior:

```text
$ go test ./internal/server -run TestLiveRun_CompactedCheckpointSurvivesMidAnchorReconnect -count=1
TestLiveRun_CompactedCheckpointSurvivesMidAnchorReconnect:
  disconnect 1 lost checkpoint: text="" reasoning="" cursor=3
FAIL
```

Terminal cleanup and category pagination RED:

```text
$ go test ./internal/store -run TestCursorCleanupSkipsActiveStatesWithAndWithoutForeignKeys -count=1
foreign_keys_on/delete_empty: deleted=6, want 5
foreign_keys_on/prune: deleted=6, want 5
foreign_keys_off/delete_empty: deleted=6, want 5
foreign_keys_off/prune: deleted=6, want 5
FAIL

$ go test ./internal/server -run 'Test(CursorAutomaticCleanupBlocksTerminalWithoutLiveWatcher|DeleteAllSessionsPaginatesBeforeCategoryFiltering|DeleteAllSessionsPrechecksActiveStateOnLaterPage)$' -count=1
empty: terminal cleanup status=200, want 409
prune: terminal cleanup status=200, want 409
category deletion listed 1 page(s), want at least 3
late-page active category deletion status=200, want 409
FAIL
```

Missing-configuration cancellation RED:

```text
$ go test ./internal/server -run TestCursorCancelNotConfiguredRestoresStatusAndReturnsActionableError -count=1
not-configured cancellation status=502, want 428
FAIL
```

Final focused GREEN:

```text
$ go test ./internal/server -run 'Test(LiveRun_|CursorAutomaticCleanupBlocksTerminalWithoutLiveWatcher|DeleteAllSessionsPaginatesBeforeCategoryFiltering|DeleteAllSessionsPrechecksActiveStateOnLaterPage|CursorCancelNotConfiguredRestoresStatusAndReturnsActionableError|CursorChatAmbiguousStatesCanBeDeletedLocally|CursorChatDeleteRejectsActiveRemoteState)' -count=1
ok  	github.com/enowdev/antares/internal/server	0.403s

$ go test ./internal/store -run 'Test(CursorCleanupSkipsActiveStatesWithAndWithoutForeignKeys|ListSessions)' -count=1
ok  	github.com/enowdev/antares/internal/store	0.184s

$ go test ./internal/server -run 'TestLiveRun_' -count=1
ok  	github.com/enowdev/antares/internal/server	0.185s
```

### Affected, race, and full verification

Fresh affected-package suite:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u ANTHROPIC_API_KEY \
  -u GEMINI_API_KEY -u AZURE_OPENAI_ENDPOINT -u AZURE_OPENAI_KEY \
  -u AZURE_OPENAI_DEPLOYMENT -u AWS_ACCESS_KEY_ID \
  -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN \
  go test ./internal/server ./internal/store ./internal/cursorrun \
    ./internal/approval ./internal/agent -count=1
ok  	github.com/enowdev/antares/internal/server	3.770s
ok  	github.com/enowdev/antares/internal/store	3.127s
ok  	github.com/enowdev/antares/internal/cursorrun	2.332s
ok  	github.com/enowdev/antares/internal/approval	0.029s
ok  	github.com/enowdev/antares/internal/agent	0.602s
```

Fresh affected-package race suite:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u ANTHROPIC_API_KEY \
  -u GEMINI_API_KEY -u AZURE_OPENAI_ENDPOINT -u AZURE_OPENAI_KEY \
  -u AZURE_OPENAI_DEPLOYMENT -u AWS_ACCESS_KEY_ID \
  -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN \
  go test -race ./internal/server ./internal/store ./internal/cursorrun \
    ./internal/approval ./internal/agent -count=1
ok  	github.com/enowdev/antares/internal/server	34.366s
ok  	github.com/enowdev/antares/internal/store	10.707s
ok  	github.com/enowdev/antares/internal/cursorrun	5.174s
ok  	github.com/enowdev/antares/internal/approval	1.062s
ok  	github.com/enowdev/antares/internal/agent	3.570s
```

Fresh full hermetic repository suite:

```text
$ env -u CURSOR_API_KEY -u OPENAI_API_KEY -u ANTHROPIC_API_KEY \
  -u GEMINI_API_KEY -u AZURE_OPENAI_ENDPOINT -u AZURE_OPENAI_KEY \
  -u AZURE_OPENAI_DEPLOYMENT -u AWS_ACCESS_KEY_ID \
  -u AWS_SECRET_ACCESS_KEY -u AWS_SESSION_TOKEN \
  -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
  -u COPILOT_GITHUB_TOKEN go test ./... -count=1
PASS (all packages; internal/server 6.540s, internal/store 5.173s,
internal/mcp 1.353s)
```

### Safety self-review

1. `base` counts only original published events. Folding an evicted event into
   the checkpoint and incrementing `base` happen under the same live-run lock,
   so retained indexes and checkpoint contents describe one boundary.
2. A follower at `cursor >= base` never receives the synthetic anchor and
   continues from its original absolute position. A follower behind `base`
   receives reset/full snapshots before any event at `base`.
3. Intermediate anchor frames report `base-1`; only the last snapshot frame
   reports `base`. A reconnect after any proper prefix of the anchor is
   therefore still behind the boundary and replays the entire idempotent
   anchor. Concurrent compaction causes the outer loop to emit the newer
   checkpoint instead of indexing a negative retained offset.
4. Checkpoint snapshots and the original-event window are independently
   bounded. Folded `EventReset` clears both snapshots before subsequent deltas
   are accumulated.
5. Automatic cleanup holds all selected session locks across the precheck and
   mutation call. Terminal state is blocked in that server precheck and again
   in store SQL, including state that appears after enumeration.
6. Explicit deletion still calls the original active-remote predicate, so the
   new automatic terminal rule does not remove the operator reconciliation
   escape. Ambiguous and stale cancellation states remain non-mutating during
   all-or-nothing precheck.
7. Category filtering occurs while consuming every deterministic 500-entry
   page. `LockMany` sorts and deduplicates the complete selected ID set before
   any active-state query; `DeleteSessions` receives that same complete set.
8. A local configuration failure cannot represent an accepted remote request.
   Its durable in-flight marker is restored to the captured prior status before
   the actionable 428 response. Potentially accepted context/transport/API
   failures retain their existing conservative classification.

### Concerns

- No live API calls or credentials were used.
- CAS backoff and full persisted-accumulator persistence remain intentionally
  deferred as requested.
- No functional, full-suite, or race-detector failures remain in this round.
