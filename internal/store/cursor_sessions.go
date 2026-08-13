package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const cursorSessionCols = `session_id,target_active,reuse_valid,model_id,model_params,repository_url,starting_ref,mode,auto_create_pr,agent_id,run_id,remote_status,last_event_id,partial_text,partial_reasoning,git_state,operation_state,user_message_id,assistant_message_id,revision,updated_at`

const cursorSessionUpdateAssignments = `target_active=?,
	reuse_valid=?,
	model_id=?,
	model_params=?,
	repository_url=?,
	starting_ref=?,
	mode=?,
	auto_create_pr=?,
	agent_id=?,
	run_id=?,
	remote_status=?,
	last_event_id=?,
	partial_text=?,
	partial_reasoning=?,
	git_state=?,
	operation_state=?,
	user_message_id=?,
	assistant_message_id=?,
	revision=?,
	updated_at=?`

func validCursorOperationState(operation string) bool {
	switch operation {
	case CursorOperationIdle,
		CursorOperationAwaitingApproval,
		CursorOperationCreateInFlight,
		CursorOperationRunInFlight,
		CursorOperationTerminal,
		CursorOperationCommitted,
		CursorOperationAmbiguous:
		return true
	default:
		return false
	}
}

func canonicalCursorModelParams(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "[]", nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("cursor model params: %w", err)
	}
	if _, ok := value.([]any); !ok {
		return "", errors.New("cursor model params must be a JSON array")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", errors.New("cursor model params contain multiple JSON values")
		}
		return "", fmt.Errorf("cursor model params: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("cursor model params: %w", err)
	}
	return string(canonical), nil
}

func prepareCursorSessionState(state *CursorSessionState) error {
	if state == nil {
		return errors.New("cursor session state is required")
	}
	if strings.TrimSpace(state.SessionID) == "" {
		return errors.New("cursor session id is required")
	}
	if state.Revision < 0 {
		return errors.New("cursor session revision cannot be negative")
	}
	if !validCursorOperationState(state.OperationState) {
		return fmt.Errorf("invalid cursor operation state %q", state.OperationState)
	}
	params, err := canonicalCursorModelParams(state.ModelParams)
	if err != nil {
		return err
	}
	state.ModelParams = params
	if state.Revision <= 0 {
		state.Revision = 1
	}
	state.UpdatedAt = fromMS(ms(time.Now()))
	return nil
}

func cursorSessionArgs(state *CursorSessionState) []any {
	return []any{
		state.SessionID,
		state.TargetActive,
		state.ReuseValid,
		state.ModelID,
		state.ModelParams,
		state.RepositoryURL,
		state.StartingRef,
		state.Mode,
		state.AutoCreatePR,
		state.AgentID,
		state.RunID,
		state.RemoteStatus,
		state.LastEventID,
		state.PartialText,
		state.PartialReasoning,
		state.GitState,
		state.OperationState,
		state.UserMessageID,
		state.AssistantMessageID,
		state.Revision,
		ms(state.UpdatedAt),
	}
}

func cursorSessionUpdateArgs(state *CursorSessionState) []any {
	return cursorSessionArgs(state)[1:]
}

// PutCursorSessionState creates a snapshot at revision one or replaces the
// currently owned revision and advances it atomically. A zero-revision state is
// insert-only; it can never overwrite an existing row.
func (s *sqlStore) PutCursorSessionState(ctx context.Context, state *CursorSessionState) error {
	if state == nil {
		return errors.New("cursor session state is required")
	}
	next := *state
	expectedRevision := next.Revision
	if err := prepareCursorSessionState(&next); err != nil {
		return err
	}
	args := append(cursorSessionArgs(&next), expectedRevision)
	var (
		revision  int64
		updatedAt int64
	)
	err := s.row(ctx, `INSERT INTO cursor_session_states (`+cursorSessionCols+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET
			target_active=EXCLUDED.target_active,
			reuse_valid=EXCLUDED.reuse_valid,
			model_id=EXCLUDED.model_id,
			model_params=EXCLUDED.model_params,
			repository_url=EXCLUDED.repository_url,
			starting_ref=EXCLUDED.starting_ref,
			mode=EXCLUDED.mode,
			auto_create_pr=EXCLUDED.auto_create_pr,
			agent_id=EXCLUDED.agent_id,
			run_id=EXCLUDED.run_id,
			remote_status=EXCLUDED.remote_status,
			last_event_id=EXCLUDED.last_event_id,
			partial_text=EXCLUDED.partial_text,
			partial_reasoning=EXCLUDED.partial_reasoning,
			git_state=EXCLUDED.git_state,
			operation_state=EXCLUDED.operation_state,
			user_message_id=EXCLUDED.user_message_id,
			assistant_message_id=EXCLUDED.assistant_message_id,
			revision=cursor_session_states.revision+1,
			updated_at=EXCLUDED.updated_at
		WHERE cursor_session_states.revision=EXCLUDED.revision AND ? > 0
		RETURNING revision,updated_at`,
		args...,
	).Scan(&revision, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCursorRevisionConflict
	}
	if err != nil {
		return err
	}
	next.Revision = revision
	next.UpdatedAt = fromMS(updatedAt)
	*state = next
	return nil
}

func scanCursorSessionState(scanner interface{ Scan(...any) error }) (*CursorSessionState, error) {
	var state CursorSessionState
	var updatedAt int64
	err := scanner.Scan(
		&state.SessionID,
		&state.TargetActive,
		&state.ReuseValid,
		&state.ModelID,
		&state.ModelParams,
		&state.RepositoryURL,
		&state.StartingRef,
		&state.Mode,
		&state.AutoCreatePR,
		&state.AgentID,
		&state.RunID,
		&state.RemoteStatus,
		&state.LastEventID,
		&state.PartialText,
		&state.PartialReasoning,
		&state.GitState,
		&state.OperationState,
		&state.UserMessageID,
		&state.AssistantMessageID,
		&state.Revision,
		&updatedAt,
	)
	if err != nil {
		return nil, err
	}
	state.UpdatedAt = fromMS(updatedAt)
	return &state, nil
}

func (s *sqlStore) GetCursorSessionState(ctx context.Context, sessionID string) (*CursorSessionState, error) {
	state, err := scanCursorSessionState(s.row(ctx,
		`SELECT `+cursorSessionCols+` FROM cursor_session_states WHERE session_id=?`,
		sessionID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return state, err
}

// ListRecoverableCursorSessionStates returns interrupted operations in stable
// order. Idle targets need no recovery, and committed work is already durable.
func (s *sqlStore) ListRecoverableCursorSessionStates(ctx context.Context) ([]CursorSessionState, error) {
	rows, err := s.query(ctx, `SELECT `+cursorSessionCols+` FROM cursor_session_states
		WHERE operation_state NOT IN (?,?)
		ORDER BY updated_at ASC, session_id ASC`,
		CursorOperationIdle,
		CursorOperationCommitted,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	states := []CursorSessionState{}
	for rows.Next() {
		state, err := scanCursorSessionState(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, *state)
	}
	return states, rows.Err()
}

// CompareAndSwapCursorSessionState advances the revision only when the caller
// still owns the expected snapshot.
func (s *sqlStore) CompareAndSwapCursorSessionState(
	ctx context.Context,
	state *CursorSessionState,
	expectedRevision int64,
) (bool, error) {
	if expectedRevision < 1 {
		return false, errors.New("cursor expected revision must be positive")
	}
	if state == nil {
		return false, errors.New("cursor session state is required")
	}
	next := *state
	if err := prepareCursorSessionState(&next); err != nil {
		return false, err
	}
	nextRevision := expectedRevision + 1
	next.Revision = nextRevision
	args := append(cursorSessionUpdateArgs(&next), next.SessionID, expectedRevision)
	result, err := s.exec(ctx, `UPDATE cursor_session_states SET `+cursorSessionUpdateAssignments+`
		WHERE session_id=? AND revision=?`, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed == 0 {
		return false, nil
	}
	*state = next
	return true, nil
}

// InvalidateCursorReuse prevents another turn from reusing the remote agent
// without discarding the IDs needed to observe or recover its current run.
func (s *sqlStore) InvalidateCursorReuse(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("cursor session id is required")
	}
	result, err := s.exec(ctx, `UPDATE cursor_session_states
		SET reuse_valid=FALSE, revision=revision+1, updated_at=?
		WHERE session_id=?`,
		ms(time.Now()),
		sessionID,
	)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return ErrNotFound
	}
	return nil
}

func prepareCursorAssistantMessage(state *CursorSessionState, message *Message) (*Message, error) {
	if message == nil {
		return nil, errors.New("cursor assistant message is required")
	}
	if strings.TrimSpace(state.AssistantMessageID) == "" {
		return nil, errors.New("cursor assistant message id is required")
	}
	if strings.TrimSpace(state.RunID) == "" {
		return nil, errors.New("cursor run id is required")
	}

	prepared := *message
	switch {
	case prepared.ID == "":
		prepared.ID = state.AssistantMessageID
	case prepared.ID != state.AssistantMessageID:
		return nil, errors.New("cursor assistant message id does not match session state")
	}
	switch {
	case prepared.SessionID == "":
		prepared.SessionID = state.SessionID
	case prepared.SessionID != state.SessionID:
		return nil, errors.New("cursor assistant session id does not match session state")
	}
	switch {
	case prepared.Role == "":
		prepared.Role = RoleAssistant
	case prepared.Role != RoleAssistant:
		return nil, errors.New("cursor final message must have the assistant role")
	}
	if attachments := strings.TrimSpace(prepared.Attachments); attachments != "" && attachments != "[]" {
		return nil, errors.New("cursor final assistant message cannot persist attachments")
	}
	prepared.Attachments = ""
	if prepared.CreatedAt.IsZero() {
		prepared.CreatedAt = fromMS(ms(time.Now()))
	}
	prepared.Meta = cloneMeta(prepared.Meta)
	prepared.Meta["cursor_agent_id"] = state.AgentID
	prepared.Meta["cursor_run_id"] = state.RunID
	return &prepared, nil
}

func cloneMeta(meta Meta) Meta {
	cloned := make(Meta, len(meta)+2)
	for key, value := range meta {
		cloned[key] = value
	}
	return cloned
}

// CommitCursorAssistant atomically appends the deterministic final message,
// rolls up session counters, and marks the matching remote run committed.
func (s *sqlStore) CommitCursorAssistant(
	ctx context.Context,
	state *CursorSessionState,
	message *Message,
) error {
	if state == nil {
		return errors.New("cursor session state is required")
	}
	if state.OperationState != CursorOperationTerminal &&
		state.OperationState != CursorOperationCommitted {
		return fmt.Errorf("cursor assistant commit requires terminal state, got %q", state.OperationState)
	}

	next := *state
	if err := prepareCursorSessionState(&next); err != nil {
		return err
	}
	preparedMessage, err := prepareCursorAssistantMessage(&next, message)
	if err != nil {
		return err
	}
	expectedRevision := next.Revision
	next.OperationState = CursorOperationCommitted
	next.Revision = expectedRevision + 1

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	updateArgs := append(cursorSessionUpdateArgs(&next),
		next.SessionID,
		expectedRevision,
		next.RunID,
		next.AssistantMessageID,
		CursorOperationTerminal,
	)
	result, err := tx.ExecContext(ctx, s.rebind(
		`UPDATE cursor_session_states SET `+cursorSessionUpdateAssignments+`
		 WHERE session_id=? AND revision=? AND run_id=? AND assistant_message_id=? AND operation_state=?`,
	), updateArgs...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		current, err := scanCursorSessionState(tx.QueryRowContext(ctx, s.rebind(
			`SELECT `+cursorSessionCols+` FROM cursor_session_states WHERE session_id=?`,
		), next.SessionID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if current.OperationState != CursorOperationCommitted ||
			current.RunID != next.RunID ||
			current.AssistantMessageID != next.AssistantMessageID {
			return errors.New("cursor session state changed before assistant commit")
		}
		currentMessage, err := scanMessage(tx.QueryRowContext(ctx, s.rebind(
			`SELECT `+messageCols+` FROM messages WHERE id=?`,
		), next.AssistantMessageID))
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("committed cursor assistant message is missing")
		}
		if err != nil {
			return err
		}
		if currentMessage.SessionID != current.SessionID ||
			currentMessage.Role != RoleAssistant ||
			currentMessage.Meta["cursor_agent_id"] != current.AgentID ||
			currentMessage.Meta["cursor_run_id"] != current.RunID {
			return errors.New("committed cursor assistant message has a different run association")
		}
		*state = *current
		*message = *currentMessage
		return nil
	}

	var maxSeq sql.NullInt64
	if err := tx.QueryRowContext(ctx, s.rebind(
		`SELECT MAX(seq) FROM messages WHERE session_id=?`,
	), preparedMessage.SessionID).Scan(&maxSeq); err != nil {
		return err
	}
	preparedMessage.Seq = maxSeq.Int64 + 1
	meta, err := preparedMessage.Meta.Value()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.rebind(
		`INSERT INTO messages (`+messageCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
	), preparedMessage.ID,
		preparedMessage.SessionID,
		preparedMessage.Seq,
		preparedMessage.Role,
		preparedMessage.Content,
		preparedMessage.Reasoning,
		preparedMessage.ToolCalls,
		preparedMessage.ToolCallID,
		preparedMessage.ToolName,
		preparedMessage.Attachments,
		preparedMessage.Model,
		preparedMessage.TokensIn,
		preparedMessage.TokensOut,
		preparedMessage.Hidden,
		preparedMessage.Compacted,
		ms(preparedMessage.CreatedAt),
		meta,
	); err != nil {
		return err
	}
	sessionResult, err := tx.ExecContext(ctx, s.rebind(
		`UPDATE sessions SET
			message_count=message_count+1,
			tokens_in=tokens_in+?,
			tokens_out=tokens_out+?,
			updated_at=?
		 WHERE id=?`,
	), preparedMessage.TokensIn, preparedMessage.TokensOut, ms(time.Now()), preparedMessage.SessionID)
	if err != nil {
		return err
	}
	sessionChanged, err := sessionResult.RowsAffected()
	if err != nil {
		return err
	}
	if sessionChanged != 1 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	*state = next
	*message = *preparedMessage
	return nil
}
