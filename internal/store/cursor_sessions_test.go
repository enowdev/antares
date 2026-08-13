package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestCursorSessionDatabaseCheckRejectsInvalidOperationState(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t).(*sqlStore)
	if err := s.CreateSession(ctx, &Session{ID: "cursor-invalid-sql-state"}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, err := s.exec(ctx, `INSERT INTO cursor_session_states
		(session_id,operation_state,updated_at) VALUES (?,?,?)`,
		"cursor-invalid-sql-state", "launching", ms(time.Now()))
	if err == nil {
		t.Fatal("database accepted an invalid cursor operation state")
	}
	if _, err := s.GetCursorSessionState(ctx, "cursor-invalid-sql-state"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid SQL state was persisted: %v", err)
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

func TestCursorSessionPutAdvancesRevisionAndRejectsStaleSnapshot(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	current := putCursorTestState(t, s, "cursor-put-revision", CursorOperationRunInFlight)
	stale := *current

	current.AgentID = "bc-current"
	current.RunID = "run-current"
	current.PartialText = "first current snapshot"
	if err := s.PutCursorSessionState(ctx, current); err != nil {
		t.Fatalf("put current snapshot: %v", err)
	}
	if current.Revision != 2 {
		t.Fatalf("first update revision = %d, want 2", current.Revision)
	}
	current.PartialText = "second current snapshot"
	if err := s.PutCursorSessionState(ctx, current); err != nil {
		t.Fatalf("put second current snapshot: %v", err)
	}
	if current.Revision != 3 {
		t.Fatalf("second update revision = %d, want 3", current.Revision)
	}

	stale.ModelParams = `[ { "id": "reasoning", "value": "max" } ]`
	staleBefore := stale
	if err := s.PutCursorSessionState(ctx, &stale); err == nil {
		t.Fatal("stale full snapshot overwrote newer cursor state")
	}
	if !reflect.DeepEqual(stale, staleBefore) {
		t.Fatalf("conflicted put mutated caller:\n got: %+v\nwant: %+v", stale, staleBefore)
	}
	fresh := *current
	fresh.Revision = 0
	fresh.RunID = "run-zero-revision-bypass"
	freshBefore := fresh
	if err := s.PutCursorSessionState(ctx, &fresh); err == nil {
		t.Fatal("zero-revision create overwrote an existing cursor state")
	}
	if !reflect.DeepEqual(fresh, freshBefore) {
		t.Fatalf("insert conflict mutated caller:\n got: %+v\nwant: %+v", fresh, freshBefore)
	}

	persisted, err := s.GetCursorSessionState(ctx, current.SessionID)
	if err != nil {
		t.Fatalf("get current state: %v", err)
	}
	if persisted.Revision != 3 ||
		persisted.AgentID != current.AgentID ||
		persisted.RunID != current.RunID ||
		persisted.PartialText != current.PartialText {
		t.Fatalf("stale put changed current state: %+v", persisted)
	}
}

func TestCursorSessionStalePutCannotRestoreCASOwnership(t *testing.T) {
	ctx := context.Background()

	t.Run("old finalizer and CAS stay rejected", func(t *testing.T) {
		s := newTestStore(t)
		initial := putCursorTestState(t, s, "cursor-stale-put-cas", CursorOperationTerminal)
		stalePut := *initial

		current := *initial
		current.AgentID = "bc-new-owner"
		current.RunID = "run-new-owner"
		current.AssistantMessageID = "assistant-new-owner"
		swapped, err := s.CompareAndSwapCursorSessionState(ctx, &current, initial.Revision)
		if err != nil || !swapped {
			t.Fatalf("advance current owner: swapped=%v err=%v", swapped, err)
		}

		if err := s.PutCursorSessionState(ctx, &stalePut); err == nil {
			t.Error("stale put restored the old revision after CAS")
		}
		oldFinalizer := *initial
		if err := s.CommitCursorAssistant(ctx, &oldFinalizer, &Message{Content: "stale result"}); err == nil {
			t.Error("old finalizer won after stale put")
		}
		oldCAS := *initial
		oldCAS.PartialText = "stale update"
		swapped, err = s.CompareAndSwapCursorSessionState(ctx, &oldCAS, initial.Revision)
		if err != nil {
			t.Fatalf("old CAS: %v", err)
		}
		if swapped {
			t.Error("old CAS won after stale put")
		}

		persisted, err := s.GetCursorSessionState(ctx, initial.SessionID)
		if err != nil {
			t.Fatalf("get current owner: %v", err)
		}
		if persisted.Revision != current.Revision ||
			persisted.AgentID != current.AgentID ||
			persisted.RunID != current.RunID {
			t.Fatalf("old ownership was restored: %+v", persisted)
		}
		messages, err := s.ListMessages(ctx, initial.SessionID, 0, 0)
		if err != nil {
			t.Fatalf("list messages: %v", err)
		}
		if len(messages) != 0 {
			t.Fatalf("old finalizer appended messages: %+v", messages)
		}
	})

	t.Run("invalidation remains monotonic", func(t *testing.T) {
		s := newTestStore(t)
		initial := putCursorTestState(t, s, "cursor-stale-put-invalidate", CursorOperationRunInFlight)
		stalePut := *initial
		if err := s.InvalidateCursorReuse(ctx, initial.SessionID); err != nil {
			t.Fatalf("invalidate reuse: %v", err)
		}

		if err := s.PutCursorSessionState(ctx, &stalePut); err == nil {
			t.Fatal("stale put undid reuse invalidation")
		}
		persisted, err := s.GetCursorSessionState(ctx, initial.SessionID)
		if err != nil {
			t.Fatalf("get invalidated state: %v", err)
		}
		if persisted.Revision != initial.Revision+1 || persisted.ReuseValid {
			t.Fatalf("invalidation was regressed: %+v", persisted)
		}
		if persisted.AgentID != initial.AgentID || persisted.RunID != initial.RunID {
			t.Fatalf("invalidation changed remote IDs: %+v", persisted)
		}
	})
}

func TestCursorSessionPutFailuresDoNotMutateCaller(t *testing.T) {
	ctx := context.Background()

	t.Run("validation", func(t *testing.T) {
		s := newTestStore(t)
		state := CursorSessionState{
			SessionID:      "cursor-put-validation",
			ModelParams:    `[ { "id": "reasoning", "value": "max" } ]`,
			OperationState: "launching",
		}
		before := state
		if err := s.PutCursorSessionState(ctx, &state); err == nil {
			t.Fatal("invalid state was accepted")
		}
		if !reflect.DeepEqual(state, before) {
			t.Fatalf("validation failure mutated caller:\n got: %+v\nwant: %+v", state, before)
		}
	})

	t.Run("database", func(t *testing.T) {
		s := newTestStore(t)
		state := CursorSessionState{
			SessionID:      "missing-parent",
			ModelParams:    `[ { "id": "reasoning", "value": "max" } ]`,
			OperationState: CursorOperationIdle,
		}
		before := state
		if err := s.PutCursorSessionState(ctx, &state); err == nil {
			t.Fatal("state without a parent session was accepted")
		}
		if !reflect.DeepEqual(state, before) {
			t.Fatalf("database failure mutated caller:\n got: %+v\nwant: %+v", state, before)
		}
	})

	t.Run("revision conflict", func(t *testing.T) {
		s := newTestStore(t)
		current := putCursorTestState(t, s, "cursor-put-conflict-copy", CursorOperationRunInFlight)
		stale := *current
		current.PartialText = "new owner"
		swapped, err := s.CompareAndSwapCursorSessionState(ctx, current, current.Revision)
		if err != nil || !swapped {
			t.Fatalf("advance owner: swapped=%v err=%v", swapped, err)
		}
		stale.ModelParams = `[ { "id": "reasoning", "value": "max" } ]`
		before := stale
		err = s.PutCursorSessionState(ctx, &stale)
		if !errors.Is(err, ErrCursorRevisionConflict) {
			t.Fatalf("stale state error=%v, want ErrCursorRevisionConflict", err)
		}
		if !reflect.DeepEqual(stale, before) {
			t.Fatalf("conflict failure mutated caller:\n got: %+v\nwant: %+v", stale, before)
		}
	})
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

func newCursorForeignKeyOffStore(t *testing.T) *sqlStore {
	t.Helper()
	raw, err := Open(context.Background(), "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatalf("open foreign-key-off store: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	s := raw.(*sqlStore)
	var enabled int
	if err := s.row(context.Background(), `PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if enabled != 0 {
		t.Fatalf("foreign_keys = %d, want disabled test store", enabled)
	}
	return s
}

func TestCursorSessionDeleteEmptySessionsRemovesStateWithoutForeignKeys(t *testing.T) {
	ctx := context.Background()
	s := newCursorForeignKeyOffStore(t)
	removed := putCursorTestState(t, s, "cursor-empty-remove", CursorOperationIdle)
	kept := putCursorTestState(t, s, "cursor-empty-keep", CursorOperationRunInFlight)
	if err := s.AppendMessage(ctx, &Message{
		ID:        "keep-message",
		SessionID: kept.SessionID,
		Role:      RoleUser,
		Content:   "keep",
	}); err != nil {
		t.Fatalf("append keep message: %v", err)
	}

	count, err := s.DeleteEmptySessions(ctx)
	if err != nil {
		t.Fatalf("delete empty sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("deleted empty sessions = %d, want 1", count)
	}
	if _, err := s.GetCursorSessionState(ctx, removed.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty-session cursor state survived: %v", err)
	}
	if _, err := s.GetSession(ctx, removed.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty session survived: %v", err)
	}
	if _, err := s.GetCursorSessionState(ctx, kept.SessionID); err != nil {
		t.Fatalf("non-empty cursor state was deleted: %v", err)
	}
}

func TestCursorSessionBulkDeleteRollsBackOnChildFailure(t *testing.T) {
	ctx := context.Background()
	s := newCursorForeignKeyOffStore(t)
	state := putCursorTestState(t, s, "cursor-bulk-rollback", CursorOperationIdle)
	if _, err := s.exec(ctx, `INSERT INTO messages (id,session_id,seq,role,created_at)
		VALUES (?,?,?,?,?)`, "rollback-message", state.SessionID, 1, RoleUser, ms(time.Now())); err != nil {
		t.Fatalf("insert rollback message: %v", err)
	}
	if _, err := s.exec(ctx, `CREATE TRIGGER fail_cursor_bulk_message_delete
		BEFORE DELETE ON messages
		WHEN OLD.session_id = 'cursor-bulk-rollback'
		BEGIN
			SELECT RAISE(ABORT, 'blocked child delete');
		END`); err != nil {
		t.Fatalf("create failing delete trigger: %v", err)
	}

	count, err := s.DeleteEmptySessions(ctx)
	if err == nil {
		t.Fatal("bulk deletion unexpectedly succeeded")
	}
	if count != 0 {
		t.Fatalf("failed bulk deletion count = %d, want 0", count)
	}
	if _, err := s.GetSession(ctx, state.SessionID); err != nil {
		t.Fatalf("session was not rolled back: %v", err)
	}
	if _, err := s.GetCursorSessionState(ctx, state.SessionID); err != nil {
		t.Fatalf("cursor state was not rolled back: %v", err)
	}
	messages, err := s.ListMessages(ctx, state.SessionID, 0, 0)
	if err != nil {
		t.Fatalf("list rolled-back messages: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != "rollback-message" {
		t.Fatalf("messages were not rolled back: %+v", messages)
	}
}

func TestCursorDeleteSessionsRollsBackEveryIDOnLaterFailure(t *testing.T) {
	ctx := context.Background()
	s := newCursorForeignKeyOffStore(t)
	sessionIDs := []string{"cursor-delete-many-first", "cursor-delete-many-second"}
	for _, sessionID := range sessionIDs {
		seedCursorSessionDeleteGraph(t, s, sessionID)
	}
	if _, err := s.exec(ctx, `CREATE TRIGGER fail_later_exact_bulk_message_delete
		BEFORE DELETE ON messages
		WHEN OLD.session_id = 'cursor-delete-many-second'
		BEGIN
			SELECT RAISE(ABORT, 'blocked later child delete');
		END`); err != nil {
		t.Fatalf("create failing later delete trigger: %v", err)
	}

	count, err := s.DeleteSessions(ctx, sessionIDs)
	if err == nil {
		t.Fatal("multi-session deletion unexpectedly succeeded")
	}
	if count != 0 {
		t.Fatalf("failed atomic deletion count=%d, want 0", count)
	}
	for _, sessionID := range sessionIDs {
		assertCursorSessionDeleteGraphPresent(t, s, sessionID)
	}
}

func TestCursorDeleteSessionsDeletesExactSetAndReturnsCount(t *testing.T) {
	ctx := context.Background()
	s := newCursorForeignKeyOffStore(t)
	deleted := []string{"cursor-delete-exact-first", "cursor-delete-exact-second"}
	for _, sessionID := range append(deleted, "cursor-delete-exact-kept") {
		seedCursorSessionDeleteGraph(t, s, sessionID)
	}

	count, err := s.DeleteSessions(ctx, deleted)
	if err != nil {
		t.Fatalf("delete exact session set: %v", err)
	}
	if count != int64(len(deleted)) {
		t.Fatalf("deleted=%d, want %d", count, len(deleted))
	}
	for _, sessionID := range deleted {
		if _, err := s.GetSession(ctx, sessionID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted session %q survived: %v", sessionID, err)
		}
		if _, err := s.GetCursorSessionState(ctx, sessionID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted cursor state %q survived: %v", sessionID, err)
		}
		messages, err := s.ListMessages(ctx, sessionID, 0, 0)
		if err != nil || len(messages) != 0 {
			t.Fatalf("deleted messages for %q=%+v err=%v", sessionID, messages, err)
		}
		memories, err := s.ListMemories(ctx, "session", sessionID, 10)
		if err != nil || len(memories) != 0 {
			t.Fatalf("deleted memories for %q=%+v err=%v", sessionID, memories, err)
		}
	}
	assertCursorSessionDeleteGraphPresent(t, s, "cursor-delete-exact-kept")
}

func seedCursorSessionDeleteGraph(t *testing.T, s *sqlStore, sessionID string) {
	t.Helper()
	ctx := context.Background()
	putCursorTestState(t, s, sessionID, CursorOperationIdle)
	if err := s.AppendMessage(ctx, &Message{
		ID: "message-" + sessionID, SessionID: sessionID,
		Role: RoleUser, Content: "keep",
	}); err != nil {
		t.Fatalf("append message for %q: %v", sessionID, err)
	}
	if err := s.PutMemory(ctx, &Memory{
		ID: "memory-" + sessionID, Scope: "session", ScopeKey: sessionID,
		Content: "keep",
	}); err != nil {
		t.Fatalf("put memory for %q: %v", sessionID, err)
	}
}

func assertCursorSessionDeleteGraphPresent(t *testing.T, s *sqlStore, sessionID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.GetSession(ctx, sessionID); err != nil {
		t.Fatalf("session %q was not rolled back: %v", sessionID, err)
	}
	if _, err := s.GetCursorSessionState(ctx, sessionID); err != nil {
		t.Fatalf("cursor state %q was not rolled back: %v", sessionID, err)
	}
	messages, err := s.ListMessages(ctx, sessionID, 0, 0)
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages for %q=%+v err=%v, want one", sessionID, messages, err)
	}
	memories, err := s.ListMemories(ctx, "session", sessionID, 10)
	if err != nil || len(memories) != 1 {
		t.Fatalf("memories for %q=%+v err=%v, want one", sessionID, memories, err)
	}
}

func TestCursorSessionPruneSessionsRemovesStateWithoutForeignKeys(t *testing.T) {
	ctx := context.Background()
	s := newCursorForeignKeyOffStore(t)
	removed := putCursorTestState(t, s, "cursor-prune-remove", CursorOperationIdle)
	kept := putCursorTestState(t, s, "cursor-prune-keep", CursorOperationRunInFlight)
	if err := s.AppendMessage(ctx, &Message{
		ID:        "prune-message",
		SessionID: removed.SessionID,
		Role:      RoleUser,
		Content:   "remove",
	}); err != nil {
		t.Fatalf("append pruned message: %v", err)
	}
	if _, err := s.exec(ctx, `UPDATE sessions SET updated_at=? WHERE id=?`,
		ms(time.Now().Add(-2*time.Hour)), removed.SessionID); err != nil {
		t.Fatalf("age pruned session: %v", err)
	}

	count, err := s.PruneSessions(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("prune sessions: %v", err)
	}
	if count != 1 {
		t.Fatalf("pruned sessions = %d, want 1", count)
	}
	if _, err := s.GetCursorSessionState(ctx, removed.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned cursor state survived: %v", err)
	}
	if _, err := s.GetSession(ctx, removed.SessionID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned session survived: %v", err)
	}
	messages, err := s.ListMessages(ctx, removed.SessionID, 0, 0)
	if err != nil {
		t.Fatalf("list pruned messages: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("pruned messages survived: %+v", messages)
	}
	if _, err := s.GetCursorSessionState(ctx, kept.SessionID); err != nil {
		t.Fatalf("fresh cursor state was deleted: %v", err)
	}
}

func TestCursorCleanupSkipsActiveStatesWithAndWithoutForeignKeys(t *testing.T) {
	const (
		cancelInFlight  = "ANTARES_CANCEL_IN_FLIGHT"
		cancelRequested = "ANTARES_CANCEL_REQUESTED"
		cancelAmbiguous = "ANTARES_CANCEL_OUTCOME_AMBIGUOUS"
	)
	stores := []struct {
		name string
		open func(*testing.T) *sqlStore
	}{
		{
			name: "foreign keys on",
			open: func(t *testing.T) *sqlStore {
				return newTestStore(t).(*sqlStore)
			},
		},
		{name: "foreign keys off", open: newCursorForeignKeyOffStore},
	}
	cleanups := []struct {
		name string
		run  func(context.Context, *sqlStore) (int64, error)
	}{
		{
			name: "delete empty",
			run: func(ctx context.Context, s *sqlStore) (int64, error) {
				return s.DeleteEmptySessions(ctx)
			},
		},
		{
			name: "prune",
			run: func(ctx context.Context, s *sqlStore) (int64, error) {
				return s.PruneSessions(ctx, time.Now().Add(-time.Hour))
			},
		},
	}

	for _, storeCase := range stores {
		for _, cleanup := range cleanups {
			t.Run(storeCase.name+"/"+cleanup.name, func(t *testing.T) {
				ctx := context.Background()
				s := storeCase.open(t)
				ordinaryID := "cleanup-ordinary"
				if err := s.CreateSession(ctx, &Session{ID: ordinaryID}); err != nil {
					t.Fatal(err)
				}

				activeIDs := []string{
					putCursorTestState(t, s, "cleanup-awaiting", CursorOperationAwaitingApproval).SessionID,
					putCursorTestState(t, s, "cleanup-create", CursorOperationCreateInFlight).SessionID,
					putCursorTestState(t, s, "cleanup-run", CursorOperationRunInFlight).SessionID,
					putCursorTestState(t, s, "cleanup-terminal", CursorOperationTerminal).SessionID,
				}
				deletableIDs := []string{
					ordinaryID,
					putCursorTestState(t, s, "cleanup-ambiguous", CursorOperationAmbiguous).SessionID,
				}
				for name, status := range map[string]string{
					"requested": cancelRequested,
					"ambiguous": cancelAmbiguous,
					"stale":     cancelInFlight,
				} {
					state := putCursorTestState(
						t, s, "cleanup-cancel-"+name, CursorOperationRunInFlight,
					)
					state.RemoteStatus = status
					if err := s.PutCursorSessionState(ctx, state); err != nil {
						t.Fatal(err)
					}
					deletableIDs = append(deletableIDs, state.SessionID)
				}

				// This state appears after the cleanup caller's enumeration.
				// The store-side predicate must still preserve it.
				racedID := "cleanup-raced-run"
				if err := s.CreateSession(ctx, &Session{ID: racedID}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := s.ListSessions(ctx, SessionFilter{Limit: 500}); err != nil {
					t.Fatal(err)
				}
				if err := s.PutCursorSessionState(ctx, &CursorSessionState{
					SessionID:      racedID,
					ModelParams:    `[]`,
					AgentID:        "bc-" + racedID,
					RunID:          "run-" + racedID,
					RemoteStatus:   "RUNNING",
					OperationState: CursorOperationRunInFlight,
				}); err != nil {
					t.Fatal(err)
				}
				activeIDs = append(activeIDs, racedID)

				if cleanup.name == "prune" {
					if _, err := s.exec(ctx,
						`UPDATE sessions SET updated_at=?`,
						ms(time.Now().Add(-2*time.Hour)),
					); err != nil {
						t.Fatal(err)
					}
				}

				count, err := cleanup.run(ctx, s)
				if err != nil {
					t.Fatalf("cleanup: %v", err)
				}
				if count != int64(len(deletableIDs)) {
					t.Fatalf("deleted=%d, want %d", count, len(deletableIDs))
				}
				for _, id := range activeIDs {
					if _, err := s.GetSession(ctx, id); err != nil {
						t.Fatalf("active session %q was deleted: %v", id, err)
					}
					if _, err := s.GetCursorSessionState(ctx, id); err != nil {
						t.Fatalf("active cursor state %q was deleted: %v", id, err)
					}
				}
				for _, id := range deletableIDs {
					if _, err := s.GetSession(ctx, id); !errors.Is(err, ErrNotFound) {
						t.Fatalf("deletable session %q survived: %v", id, err)
					}
					if id != ordinaryID {
						if _, err := s.GetCursorSessionState(ctx, id); !errors.Is(err, ErrNotFound) {
							t.Fatalf("deletable cursor state %q survived: %v", id, err)
						}
					}
				}
			})
		}
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
