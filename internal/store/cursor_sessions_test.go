package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCursorSessionMigrationCreatesCascadeAndOperationIndex(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t).(*sqlStore)

	var tableSQL string
	if err := s.row(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='cursor_session_states'`).
		Scan(&tableSQL); err != nil {
		if err == sql.ErrNoRows {
			t.Fatal("cursor_session_states table was not created")
		}
		t.Fatalf("read cursor session migration: %v", err)
	}
	if !strings.Contains(strings.ToUpper(tableSQL), "REFERENCES SESSIONS") ||
		!strings.Contains(strings.ToUpper(tableSQL), "ON DELETE CASCADE") {
		t.Fatalf("cursor session foreign key is not cascading:\n%s", tableSQL)
	}

	var indexName string
	if err := s.row(ctx, `SELECT name FROM sqlite_master
		WHERE type='index' AND tbl_name='cursor_session_states' AND name='idx_cursor_session_operation'`).
		Scan(&indexName); err != nil {
		t.Fatalf("cursor session operation-state index: %v", err)
	}

	if err := s.migrate(ctx); err != nil {
		t.Fatalf("cursor session migration is not idempotent: %v", err)
	}

	if err := s.CreateSession(ctx, &Session{ID: "cursor-fk-cascade"}); err != nil {
		t.Fatalf("create cascade session: %v", err)
	}
	if err := s.PutCursorSessionState(ctx, &CursorSessionState{
		SessionID:      "cursor-fk-cascade",
		OperationState: CursorOperationIdle,
	}); err != nil {
		t.Fatalf("put cascade state: %v", err)
	}
	if _, err := s.exec(ctx, `DELETE FROM sessions WHERE id=?`, "cursor-fk-cascade"); err != nil {
		t.Fatalf("delete cascade parent directly: %v", err)
	}
	if _, err := s.GetCursorSessionState(ctx, "cursor-fk-cascade"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign-key cascade left cursor state behind: %v", err)
	}
}

func TestCursorSessionRoundTripCanonicalizesParamsAndCascades(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateSession(ctx, &Session{ID: "cursor-round-trip", Title: "Cursor"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	state := &CursorSessionState{
		SessionID:          "cursor-round-trip",
		TargetActive:       true,
		ReuseValid:         true,
		ModelID:            "gpt-5.6-sol",
		ModelParams:        `[ { "value": "max", "id": "reasoning" }, { "id": "context", "value": "1m" } ]`,
		RepositoryURL:      "https://github.com/acme/repo",
		StartingRef:        "main",
		Mode:               "agent",
		AutoCreatePR:       true,
		AgentID:            "bc-agent",
		RunID:              "run-one",
		RemoteStatus:       "RUNNING",
		LastEventID:        "event-7",
		PartialText:        "partial answer",
		PartialReasoning:   "partial reasoning",
		GitState:           `{"branch":"cursor/work"}`,
		OperationState:     CursorOperationRunInFlight,
		UserMessageID:      "msg-user",
		AssistantMessageID: "msg-assistant",
	}
	if err := s.PutCursorSessionState(ctx, state); err != nil {
		t.Fatalf("put cursor state: %v", err)
	}

	const canonicalParams = `[{"id":"reasoning","value":"max"},{"id":"context","value":"1m"}]`
	if state.ModelParams != canonicalParams {
		t.Fatalf("caller model params = %q, want canonical %q", state.ModelParams, canonicalParams)
	}
	if state.Revision != 1 {
		t.Fatalf("caller revision = %d, want 1", state.Revision)
	}
	if state.UpdatedAt.IsZero() {
		t.Fatal("caller updated_at was not populated")
	}

	got, err := s.GetCursorSessionState(ctx, state.SessionID)
	if err != nil {
		t.Fatalf("get cursor state: %v", err)
	}
	if *got != *state {
		t.Fatalf("round trip mismatch:\n got: %+v\nwant: %+v", *got, *state)
	}

	if err := s.DeleteSession(ctx, state.SessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := s.GetCursorSessionState(ctx, state.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get cursor state after session delete = %v, want ErrNotFound", err)
	}
}

func TestCursorSessionDeleteFollowsManualSessionCascadeConvention(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatalf("open in-memory store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	state := putCursorTestState(t, s, "cursor-manual-cascade", CursorOperationIdle)

	if err := s.DeleteSession(ctx, state.SessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := s.GetCursorSessionState(ctx, state.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get cursor state after manual cascade = %v, want ErrNotFound", err)
	}
}

func TestCursorSessionStateSurvivesSQLiteReopen(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "durable.db")
	first, err := Open(ctx, "sqlite", dsn, 2, 5000, true)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	state := putCursorTestState(t, first, "cursor-durable", CursorOperationRunInFlight)
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	reopened, err := Open(ctx, "sqlite", dsn, 2, 5000, true)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.GetCursorSessionState(ctx, state.SessionID)
	if err != nil {
		t.Fatalf("get cursor state after reopen: %v", err)
	}
	if !reflect.DeepEqual(got, state) {
		t.Fatalf("state after reopen:\n got: %+v\nwant: %+v", got, state)
	}
}

func putCursorTestState(t *testing.T, s Store, sessionID, operation string) *CursorSessionState {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateSession(ctx, &Session{ID: sessionID, Title: sessionID}); err != nil {
		t.Fatalf("create cursor test session: %v", err)
	}
	state := &CursorSessionState{
		SessionID:          sessionID,
		TargetActive:       true,
		ReuseValid:         true,
		ModelID:            "gpt-5.6-sol",
		ModelParams:        `[{"id":"reasoning","value":"max"}]`,
		RepositoryURL:      "https://github.com/acme/repo",
		StartingRef:        "main",
		Mode:               "agent",
		AutoCreatePR:       true,
		AgentID:            "bc-" + sessionID,
		RunID:              "run-" + sessionID,
		RemoteStatus:       "RUNNING",
		OperationState:     operation,
		UserMessageID:      "user-" + sessionID,
		AssistantMessageID: "assistant-" + sessionID,
	}
	if err := s.PutCursorSessionState(ctx, state); err != nil {
		t.Fatalf("put cursor test state: %v", err)
	}
	return state
}

func TestCursorSessionCompareAndSwapRejectsCompetingTurn(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	initial := putCursorTestState(t, s, "cursor-cas", CursorOperationIdle)

	candidates := [2]CursorSessionState{*initial, *initial}
	candidates[0].OperationState = CursorOperationCreateInFlight
	candidates[0].PartialText = "first"
	candidates[1].OperationState = CursorOperationRunInFlight
	candidates[1].PartialText = "second"

	type result struct {
		index   int
		swapped bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, len(candidates))
	for i := range candidates {
		go func(index int) {
			<-start
			swapped, err := s.CompareAndSwapCursorSessionState(ctx, &candidates[index], initial.Revision)
			results <- result{index: index, swapped: swapped, err: err}
		}(i)
	}
	close(start)

	successes := 0
	winner := -1
	for range candidates {
		got := <-results
		if got.err != nil {
			t.Fatalf("compare-and-swap candidate %d: %v", got.index, got.err)
		}
		if got.swapped {
			successes++
			winner = got.index
		}
	}
	if successes != 1 {
		t.Fatalf("successful competing swaps = %d, want exactly 1", successes)
	}

	persisted, err := s.GetCursorSessionState(ctx, initial.SessionID)
	if err != nil {
		t.Fatalf("get swapped cursor state: %v", err)
	}
	if persisted.Revision != initial.Revision+1 {
		t.Fatalf("persisted revision = %d, want %d", persisted.Revision, initial.Revision+1)
	}
	if persisted.PartialText != candidates[winner].PartialText ||
		persisted.OperationState != candidates[winner].OperationState {
		t.Fatalf("persisted state = %+v, want candidate %d", persisted, winner)
	}
}

func TestCursorSessionRecoverableListExcludesCommittedTerminalWork(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putCursorTestState(t, s, "cursor-running", CursorOperationRunInFlight)
	putCursorTestState(t, s, "cursor-terminal", CursorOperationTerminal)
	putCursorTestState(t, s, "cursor-committed", CursorOperationCommitted)

	states, err := s.ListRecoverableCursorSessionStates(ctx)
	if err != nil {
		t.Fatalf("list recoverable cursor states: %v", err)
	}
	got := make(map[string]string, len(states))
	for _, state := range states {
		got[state.SessionID] = state.OperationState
	}
	want := map[string]string{
		"cursor-running":  CursorOperationRunInFlight,
		"cursor-terminal": CursorOperationTerminal,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recoverable cursor states = %v, want %v", got, want)
	}
}

func TestCursorSessionInvalidateReusePreservesRemoteIDs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	before := putCursorTestState(t, s, "cursor-invalidate", CursorOperationTerminal)

	if err := s.InvalidateCursorReuse(ctx, before.SessionID); err != nil {
		t.Fatalf("invalidate cursor reuse: %v", err)
	}
	after, err := s.GetCursorSessionState(ctx, before.SessionID)
	if err != nil {
		t.Fatalf("get invalidated cursor state: %v", err)
	}
	if after.ReuseValid {
		t.Fatal("reuse remains valid after invalidation")
	}
	if after.AgentID != before.AgentID || after.RunID != before.RunID {
		t.Fatalf("remote IDs changed: before=(%q,%q) after=(%q,%q)",
			before.AgentID, before.RunID, after.AgentID, after.RunID)
	}
	if after.Revision != before.Revision+1 {
		t.Fatalf("revision after invalidation = %d, want %d", after.Revision, before.Revision+1)
	}
}

func TestCursorSessionCommitAssistantIsAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	state := putCursorTestState(t, s, "cursor-commit", CursorOperationTerminal)
	if err := s.AppendMessage(ctx, &Message{
		ID:        state.UserMessageID,
		SessionID: state.SessionID,
		Role:      RoleUser,
		Content:   "fix it",
		TokensIn:  7,
	}); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	assistant := &Message{
		Content:   "fixed",
		Reasoning: "checked the tests",
		Model:     state.ModelID,
		TokensOut: 11,
		Meta:      Meta{"source": "cursor"},
	}
	if err := s.CommitCursorAssistant(ctx, state, assistant); err != nil {
		t.Fatalf("commit cursor assistant: %v", err)
	}
	retry := &Message{Content: "a retry must not replace the committed result"}
	if err := s.CommitCursorAssistant(ctx, state, retry); err != nil {
		t.Fatalf("repeat cursor assistant commit: %v", err)
	}
	if retry.Content != assistant.Content || retry.ID != assistant.ID {
		t.Fatalf("idempotent retry returned %+v, want persisted assistant %+v", retry, assistant)
	}

	if assistant.ID != state.AssistantMessageID ||
		assistant.SessionID != state.SessionID ||
		assistant.Role != RoleAssistant {
		t.Fatalf("assistant identity was not derived deterministically: %+v", assistant)
	}
	messages, err := s.ListMessages(ctx, state.SessionID, 0, 0)
	if err != nil {
		t.Fatalf("list committed messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages after repeated commit = %d, want 2", len(messages))
	}
	gotAssistant := messages[1]
	if gotAssistant.ID != state.AssistantMessageID || gotAssistant.Seq != 2 {
		t.Fatalf("committed assistant identity = (%q,%d), want (%q,2)",
			gotAssistant.ID, gotAssistant.Seq, state.AssistantMessageID)
	}
	if gotAssistant.Meta["cursor_agent_id"] != state.AgentID ||
		gotAssistant.Meta["cursor_run_id"] != state.RunID {
		t.Fatalf("assistant is not associated with Cursor run: meta=%v", gotAssistant.Meta)
	}

	persisted, err := s.GetCursorSessionState(ctx, state.SessionID)
	if err != nil {
		t.Fatalf("get committed cursor state: %v", err)
	}
	if persisted.OperationState != CursorOperationCommitted {
		t.Fatalf("operation state = %q, want committed", persisted.OperationState)
	}
	if persisted.Revision != 2 {
		t.Fatalf("committed revision = %d, want 2", persisted.Revision)
	}
	session, err := s.GetSession(ctx, state.SessionID)
	if err != nil {
		t.Fatalf("get committed session: %v", err)
	}
	if session.MessageCount != 2 || session.TokensIn != 7 || session.TokensOut != 11 {
		t.Fatalf("committed session counters = (%d,%d,%d), want (2,7,11)",
			session.MessageCount, session.TokensIn, session.TokensOut)
	}
}

func TestCursorSessionCommitAssistantRollsBackOnMessageConflict(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	state := putCursorTestState(t, s, "cursor-conflict", CursorOperationTerminal)
	state.AssistantMessageID = "occupied-message"
	if err := s.PutCursorSessionState(ctx, state); err != nil {
		t.Fatalf("put conflicting assistant ID: %v", err)
	}
	if err := s.AppendMessage(ctx, &Message{
		ID:        state.AssistantMessageID,
		SessionID: state.SessionID,
		Role:      RoleUser,
		Content:   "already occupied",
	}); err != nil {
		t.Fatalf("append conflicting message: %v", err)
	}

	err := s.CommitCursorAssistant(ctx, state, &Message{Content: "must not commit"})
	if err == nil {
		t.Fatal("commit with occupied deterministic message ID succeeded")
	}
	persisted, getErr := s.GetCursorSessionState(ctx, state.SessionID)
	if getErr != nil {
		t.Fatalf("get state after failed commit: %v", getErr)
	}
	if persisted.OperationState != CursorOperationTerminal {
		t.Fatalf("state after failed commit = %q, want terminal", persisted.OperationState)
	}
	messages, listErr := s.ListMessages(ctx, state.SessionID, 0, 0)
	if listErr != nil {
		t.Fatalf("list after failed commit: %v", listErr)
	}
	if len(messages) != 1 || messages[0].Content != "already occupied" {
		t.Fatalf("messages changed after failed commit: %+v", messages)
	}
}

func TestCursorSessionConcurrentAssistantCommitAppendsExactlyOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	initial := putCursorTestState(t, s, "cursor-concurrent-commit", CursorOperationTerminal)

	states := [2]CursorSessionState{*initial, *initial}
	messages := [2]Message{
		{Content: "one canonical result", TokensOut: 5},
		{Content: "one canonical result", TokensOut: 5},
	}
	start := make(chan struct{})
	results := make(chan error, len(states))
	for index := range states {
		go func() {
			<-start
			results <- s.CommitCursorAssistant(ctx, &states[index], &messages[index])
		}()
	}
	close(start)
	for range states {
		if err := <-results; err != nil {
			t.Fatalf("concurrent cursor assistant commit: %v", err)
		}
	}

	persistedMessages, err := s.ListMessages(ctx, initial.SessionID, 0, 0)
	if err != nil {
		t.Fatalf("list concurrently committed messages: %v", err)
	}
	if len(persistedMessages) != 1 ||
		persistedMessages[0].ID != initial.AssistantMessageID ||
		persistedMessages[0].Seq != 1 {
		t.Fatalf("concurrently committed messages = %+v, want one deterministic message", persistedMessages)
	}
	session, err := s.GetSession(ctx, initial.SessionID)
	if err != nil {
		t.Fatalf("get concurrently committed session: %v", err)
	}
	if session.MessageCount != 1 || session.TokensOut != 5 {
		t.Fatalf("session counters after concurrent commit = (%d,%d), want (1,5)",
			session.MessageCount, session.TokensOut)
	}
}

func TestCursorSessionCommitRejectsStaleRunAssociation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	initial := putCursorTestState(t, s, "cursor-stale-commit", CursorOperationTerminal)
	stale := *initial

	current := *initial
	current.AgentID = "bc-new"
	current.RunID = "run-new"
	current.AssistantMessageID = "assistant-new"
	swapped, err := s.CompareAndSwapCursorSessionState(ctx, &current, initial.Revision)
	if err != nil || !swapped {
		t.Fatalf("replace current run: swapped=%v err=%v", swapped, err)
	}

	err = s.CommitCursorAssistant(ctx, &stale, &Message{Content: "stale result"})
	if err == nil || !strings.Contains(err.Error(), "state changed") {
		t.Fatalf("stale run commit error = %v", err)
	}
	persisted, err := s.GetCursorSessionState(ctx, initial.SessionID)
	if err != nil {
		t.Fatalf("get state after stale commit: %v", err)
	}
	if persisted.RunID != current.RunID ||
		persisted.AssistantMessageID != current.AssistantMessageID ||
		persisted.OperationState != CursorOperationTerminal {
		t.Fatalf("stale commit changed current state: %+v", persisted)
	}
	messages, err := s.ListMessages(ctx, initial.SessionID, 0, 0)
	if err != nil {
		t.Fatalf("list messages after stale commit: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("stale commit appended messages: %+v", messages)
	}
}

func TestCursorSessionRejectsInvalidPersistedValues(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.CreateSession(ctx, &Session{ID: "cursor-invalid"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	state := &CursorSessionState{
		SessionID:      "cursor-invalid",
		ModelParams:    "[]",
		OperationState: "launching",
	}
	if err := s.PutCursorSessionState(ctx, state); err == nil ||
		!strings.Contains(err.Error(), "operation state") {
		t.Fatalf("invalid operation-state error = %v", err)
	}

	state.OperationState = CursorOperationIdle
	state.ModelParams = `{"reasoning":"max"}`
	if err := s.PutCursorSessionState(ctx, state); err == nil ||
		!strings.Contains(err.Error(), "JSON array") {
		t.Fatalf("invalid model-params error = %v", err)
	}
	if _, err := s.GetCursorSessionState(ctx, state.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid cursor state was persisted: %v", err)
	}
}
