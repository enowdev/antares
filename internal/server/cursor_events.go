package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/approval"
	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/store"
)

var (
	errCursorStateChanged    = errors.New("cursor session state changed")
	errCursorAlreadyTerminal = errors.New("cursor session is already terminal")
)

const (
	cursorCancelInFlight  = "ANTARES_CANCEL_IN_FLIGHT"
	cursorCancelRequested = "ANTARES_CANCEL_REQUESTED"
	cursorCancelAmbiguous = "ANTARES_CANCEL_OUTCOME_AMBIGUOUS"
	cursorCancelNoActive  = "ANTARES_CANCEL_NO_ACTIVE_RUN"
)

func cursorCancelState(status string) bool {
	switch status {
	case cursorCancelInFlight, cursorCancelRequested, cursorCancelAmbiguous:
		return true
	default:
		return false
	}
}

func (s *Server) reconcileCursorCancelCrashMarker(
	ctx context.Context,
	state *store.CursorSessionState,
) (*store.CursorSessionState, error) {
	if state == nil || state.OperationState != store.CursorOperationRunInFlight ||
		state.RemoteStatus != cursorCancelInFlight {
		return state, nil
	}
	s.cursorCancelMu.Lock()
	_, locallyInFlight := s.cursorCancels[state.SessionID]
	s.cursorCancelMu.Unlock()
	if locallyInFlight {
		return state, nil
	}
	next, err := s.mutateCursorState(ctx, state.SessionID,
		func(current *store.CursorSessionState) error {
			if current.OperationState != store.CursorOperationRunInFlight ||
				current.AgentID != state.AgentID || current.RunID != state.RunID ||
				current.RemoteStatus != cursorCancelInFlight {
				return errCursorStateChanged
			}
			current.RemoteStatus = cursorCancelAmbiguous
			return nil
		})
	if errors.Is(err, errCursorStateChanged) {
		return s.db.GetCursorSessionState(ctx, state.SessionID)
	}
	return next, err
}

// mutateCursorState is the coordinator's only durable update primitive. Every
// write reloads and CASes the current revision so stream persistence, edits,
// cancellation, and recovery cannot silently overwrite one another.
func (s *Server) mutateCursorState(
	ctx context.Context,
	sessionID string,
	mutate func(*store.CursorSessionState) error,
) (*store.CursorSessionState, error) {
	for attempt := 0; attempt < 32; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current, err := s.db.GetCursorSessionState(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		next := *current
		if err := mutate(&next); err != nil {
			return nil, err
		}
		swapped, err := s.db.CompareAndSwapCursorSessionState(
			ctx, &next, current.Revision,
		)
		if err != nil {
			return nil, err
		}
		if swapped {
			return &next, nil
		}
	}
	return nil, errors.New("cursor session state remained contended")
}

func (s *Server) watchCursorRun(
	ctx context.Context,
	initial *store.CursorSessionState,
	live *liveRun,
) error {
	if initial == nil || initial.AgentID == "" || initial.RunID == "" {
		return errors.New("Cursor recovery IDs are missing")
	}
	agentID, runID := initial.AgentID, initial.RunID
	partialText := truncateCursorRunes(
		s.redactCursorString(initial.PartialText), maxCursorPartialRunes,
	)
	partialReasoning := truncateCursorRunes(
		s.redactCursorString(initial.PartialReasoning), maxCursorPartialRunes,
	)

	onReset := func() error {
		_, err := s.mutateCursorState(context.Background(), initial.SessionID,
			func(state *store.CursorSessionState) error {
				if state.AgentID != agentID || state.RunID != runID ||
					state.OperationState != store.CursorOperationRunInFlight {
					return errCursorStateChanged
				}
				state.LastEventID = ""
				state.PartialText = ""
				state.PartialReasoning = ""
				return nil
			})
		if err != nil {
			return err
		}
		// Clear memory only after the durable reset succeeded and before Cursor
		// replays from the beginning.
		partialText = ""
		partialReasoning = ""
		live.publish(agent.Event{Type: agent.EventReset})
		return nil
	}

	emit := func(event cursor.StreamEvent) error {
		event.ID = truncateCursorRunes(event.ID, maxCursorIdentifierRunes)
		event.Status = truncateCursorRunes(
			s.redactCursorString(event.Status), maxCursorRemoteStatusRunes,
		)
		event.Text = truncateCursorRunes(
			s.redactCursorString(event.Text), maxCursorPartialRunes,
		)
		previousText := partialText
		previousReasoning := partialReasoning
		nextText, nextReasoning := partialText, partialReasoning
		switch event.Type {
		case "assistant":
			nextText = truncateCursorRunes(
				s.redactCursorString(nextText+event.Text), maxCursorPartialRunes,
			)
		case "thinking":
			nextReasoning = truncateCursorRunes(
				s.redactCursorString(nextReasoning+event.Text), maxCursorPartialRunes,
			)
		case "result":
			// Result text is a whole canonical answer, not one more delta.
			if event.Text != "" {
				nextText = event.Text
			}
		}

		_, err := s.mutateCursorState(context.Background(), initial.SessionID,
			func(state *store.CursorSessionState) error {
				if state.AgentID != agentID || state.RunID != runID ||
					state.OperationState != store.CursorOperationRunInFlight {
					return errCursorStateChanged
				}
				if event.ID != "" {
					state.LastEventID = event.ID
				}
				state.PartialText = nextText
				state.PartialReasoning = nextReasoning
				if event.Status != "" && !cursorCancelState(state.RemoteStatus) {
					state.RemoteStatus = event.Status
				}
				return nil
			})
		if err != nil {
			return err
		}
		partialText, partialReasoning = nextText, nextReasoning

		// Every corresponding durable field above is committed before its live
		// event is visible. Tool activity deliberately remains live-only.
		switch event.Type {
		case "assistant":
			if nextText != previousText {
				if strings.HasPrefix(nextText, previousText) {
					live.publish(agent.Event{
						Type:  agent.EventText,
						Delta: strings.TrimPrefix(nextText, previousText),
					})
				} else {
					live.publish(agent.Event{Type: agent.EventReset})
					if nextReasoning != "" {
						live.publish(agent.Event{
							Type: agent.EventReasoning, Delta: nextReasoning,
						})
					}
					if nextText != "" {
						live.publish(agent.Event{Type: agent.EventText, Delta: nextText})
					}
				}
			}
		case "thinking":
			if nextReasoning != previousReasoning {
				if strings.HasPrefix(nextReasoning, previousReasoning) {
					live.publish(agent.Event{
						Type:  agent.EventReasoning,
						Delta: strings.TrimPrefix(nextReasoning, previousReasoning),
					})
				} else {
					live.publish(agent.Event{Type: agent.EventReset})
					if nextReasoning != "" {
						live.publish(agent.Event{
							Type: agent.EventReasoning, Delta: nextReasoning,
						})
					}
					if nextText != "" {
						live.publish(agent.Event{Type: agent.EventText, Delta: nextText})
					}
				}
			}
		case "status":
			progress := s.cursorRunner.Progress(event)
			live.publish(agent.Event{
				Type: agent.EventToolProgress,
				ID: truncateCursorRunes(
					s.redactCursorString(event.ID), maxCursorIdentifierRunes,
				),
				Name:    "cursor",
				Message: truncateCursorRunes(s.redactCursorString(progress.Message), 4096),
				Chunk:   truncateCursorRunes(s.redactCursorString(progress.Chunk), 4096),
			})
		case "tool_call":
			progress := s.cursorRunner.Progress(event)
			live.publish(agent.Event{
				Type: agent.EventToolProgress,
				ID: truncateCursorRunes(
					s.redactCursorString(event.CallID), maxCursorIdentifierRunes,
				),
				Name: truncateCursorRunes(
					s.redactCursorString(event.ToolName), maxCursorIdentifierRunes,
				),
				Message: truncateCursorRunes(s.redactCursorString(progress.Message), 4096),
				Chunk:   truncateCursorRunes(s.redactCursorString(progress.Chunk), 4096),
			})
		case "result":
			if event.Text != "" && event.Text != previousText {
				if previousText != "" {
					live.publish(agent.Event{Type: agent.EventReset})
					if nextReasoning != "" {
						live.publish(agent.Event{
							Type: agent.EventReasoning, Delta: nextReasoning,
						})
					}
				}
				live.publish(agent.Event{Type: agent.EventText, Delta: event.Text})
			}
		}
		return nil
	}

	terminal, err := s.cursorRunner.StreamRun(
		ctx, agentID, runID, initial.LastEventID, onReset, emit,
	)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A terminal metadata snapshot wins over a later stream failure.
		snapshot, snapshotErr := s.cursorRunner.GetRun(
			context.Background(), agentID, runID,
		)
		if snapshotErr == nil && snapshot != nil && cursorRunTerminal(snapshot.Status) {
			return s.finalizeCursorRun(
				context.Background(), initial.SessionID, snapshot, live,
			)
		}
		return err
	}
	if terminal == nil {
		return errors.New("Cursor stream returned no run snapshot")
	}
	if !cursorRunTerminal(terminal.Status) {
		_, _ = s.mutateCursorState(context.Background(), initial.SessionID,
			func(state *store.CursorSessionState) error {
				if state.AgentID == agentID && state.RunID == runID {
					state.RemoteStatus = truncateCursorRunes(
						s.redactCursorString(terminal.Status), maxCursorRemoteStatusRunes,
					)
				}
				return nil
			})
		return errors.New("Cursor stream ended before the run became terminal")
	}
	return s.finalizeCursorRun(
		context.Background(), initial.SessionID, terminal, live,
	)
}

func cursorRunTerminal(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "FINISHED", "ERROR", "CANCELLED", "EXPIRED":
		return true
	default:
		return false
	}
}

func (s *Server) finalizeCursorRun(
	ctx context.Context,
	sessionID string,
	run *cursor.Run,
	live *liveRun,
) error {
	unlock := s.cursorLifecycles.Lock(sessionID)
	defer unlock()
	if run == nil || strings.TrimSpace(run.ID) == "" ||
		strings.TrimSpace(run.AgentID) == "" {
		return errors.New("Cursor terminal snapshot is missing IDs")
	}
	if !cursorRunTerminal(run.Status) {
		return fmt.Errorf("Cursor run is not terminal: %s", run.Status)
	}

	current, err := s.db.GetCursorSessionState(ctx, sessionID)
	if err != nil {
		return err
	}
	if current.RunID != run.ID || current.AgentID != run.AgentID {
		return errCursorStateChanged
	}
	if current.OperationState == store.CursorOperationCommitted {
		return nil
	}
	oldText := current.PartialText

	gitState := ""
	if run.Git != nil {
		gitState, err = s.marshalCursorGitState(run.Git)
		if err != nil {
			return err
		}
	}
	canonicalText := truncateCursorRunes(
		s.redactCursorString(current.PartialText), maxCursorPartialRunes,
	)
	if run.Result != "" {
		canonicalText = truncateCursorRunes(
			s.redactCursorString(run.Result), maxCursorPartialRunes,
		)
	}
	canonicalReasoning := truncateCursorRunes(
		s.redactCursorString(current.PartialReasoning), maxCursorPartialRunes,
	)
	assistantID := current.AssistantMessageID
	if assistantID == "" {
		assistantID = deterministicCursorAssistantID(current.SessionID, run.ID)
	}

	terminalState := current
	if current.OperationState != store.CursorOperationTerminal {
		terminalState, err = s.mutateCursorState(ctx, current.SessionID,
			func(state *store.CursorSessionState) error {
				if state.AgentID != run.AgentID || state.RunID != run.ID {
					return errCursorStateChanged
				}
				if state.OperationState == store.CursorOperationCommitted ||
					state.OperationState == store.CursorOperationTerminal {
					// Another finalizer won the terminal CAS. Reuse its
					// revision so CommitCursorAssistant arbitrates exactly once.
					return errCursorAlreadyTerminal
				}
				if state.OperationState != store.CursorOperationRunInFlight {
					return errCursorStateChanged
				}
				state.RemoteStatus = truncateCursorRunes(
					s.redactCursorString(run.Status), maxCursorRemoteStatusRunes,
				)
				state.PartialText = canonicalText
				state.PartialReasoning = canonicalReasoning
				state.GitState = gitState
				state.AssistantMessageID = assistantID
				state.OperationState = store.CursorOperationTerminal
				state.ReuseValid = state.AgentID != ""
				return nil
			})
		if errors.Is(err, errCursorAlreadyTerminal) {
			terminalState, err = s.db.GetCursorSessionState(ctx, current.SessionID)
		}
		if err != nil {
			return err
		}
	}
	if terminalState.OperationState == store.CursorOperationCommitted {
		return nil
	}

	message := &store.Message{
		ID: terminalState.AssistantMessageID, SessionID: terminalState.SessionID,
		Role: store.RoleAssistant, Content: terminalState.PartialText,
		Reasoning: terminalState.PartialReasoning, Model: terminalState.ModelID,
		Meta: store.Meta{
			"cursor_remote_status": terminalState.RemoteStatus,
			"cursor_git_state":     terminalState.GitState,
		},
	}
	if err := s.db.CommitCursorAssistant(ctx, terminalState, message); err != nil {
		latest, getErr := s.db.GetCursorSessionState(ctx, terminalState.SessionID)
		if getErr == nil && latest.OperationState == store.CursorOperationCommitted &&
			latest.RunID == terminalState.RunID &&
			latest.AssistantMessageID == terminalState.AssistantMessageID {
			return nil
		}
		return err
	}

	if live != nil && oldText != canonicalText {
		if oldText != "" {
			live.publish(agent.Event{Type: agent.EventReset})
			if terminalState.PartialReasoning != "" {
				live.publish(agent.Event{
					Type: agent.EventReasoning, Delta: terminalState.PartialReasoning,
				})
			}
		}
		if canonicalText != "" {
			live.publish(agent.Event{Type: agent.EventText, Delta: canonicalText})
		}
	}
	if live != nil && strings.ToUpper(strings.TrimSpace(run.Status)) != "FINISHED" {
		live.publish(agent.Event{
			Type: agent.EventError,
			Err: "Cursor run ended with status " +
				truncateCursorRunes(s.redactCursorString(run.Status), 120),
		})
	}
	return nil
}

func (s *Server) marshalCursorGitState(git *cursor.GitState) (string, error) {
	if git == nil {
		return "", nil
	}
	safe := cursor.GitState{Branches: []cursor.GitBranch{}}
	for _, branch := range git.Branches {
		candidate := cursor.GitBranch{
			RepoURL: truncateCursorRunes(
				s.redactCursorString(branch.RepoURL), maxCursorRepositoryRunes,
			),
			Branch: truncateCursorRunes(
				s.redactCursorString(branch.Branch), maxCursorStartingRefRunes,
			),
			PRURL: truncateCursorRunes(
				s.redactCursorString(branch.PRURL), maxCursorRepositoryRunes,
			),
		}
		safe.Branches = append(safe.Branches, candidate)
		raw, err := json.Marshal(safe)
		if err != nil {
			return "", err
		}
		if utf8.RuneCount(raw) > maxCursorGitStateRunes {
			safe.Branches = safe.Branches[:len(safe.Branches)-1]
			break
		}
	}
	raw, err := json.Marshal(safe)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func deterministicCursorAssistantID(sessionID, runID string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + runID))
	return "msg_cursor_" + hex.EncodeToString(sum[:12])
}

// cursorRecoveryRun first returns an in-memory run. If none exists, it
// atomically reserves one watcher for durable Cursor state.
func (s *Server) cursorRecoveryRun(sessionID string) *liveRun {
	if sessionID == "" || s.db == nil || s.cursorRunner == nil {
		return nil
	}
	unlock := s.cursorLifecycles.Lock(sessionID)
	defer unlock()
	if live := s.hub.get(sessionID); live != nil {
		return live
	}
	state, err := s.db.GetCursorSessionState(context.Background(), sessionID)
	if err != nil || !cursorStateNeedsRecovery(state) {
		return nil
	}

	ctx, stop := context.WithCancel(context.Background())
	live := newCursorLiveRun(liveRunCursorRecovery)
	live.beginCursorWatch(stop)
	if !s.hub.putIfAbsent(sessionID, live) {
		stop()
		return s.hub.get(sessionID)
	}
	// Rehydrate durable partials before resuming after Last-Event-ID. This closes
	// the crash window where an event was committed but the process died before
	// publishing it to the old in-memory run.
	live.publish(agent.Event{Type: agent.EventReset})
	if state.PartialReasoning != "" {
		live.publish(agent.Event{
			Type: agent.EventReasoning,
			Delta: truncateCursorRunes(
				s.redactCursorString(state.PartialReasoning), maxCursorPartialRunes,
			),
		})
	}
	if state.PartialText != "" {
		live.publish(agent.Event{
			Type: agent.EventText,
			Delta: truncateCursorRunes(
				s.redactCursorString(state.PartialText), maxCursorPartialRunes,
			),
		})
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				live.publish(agent.Event{
					Type: agent.EventError, Err: "Cursor recovery failed",
				})
			}
			live.publish(agent.Event{Type: agent.EventDone})
			live.finish()
			stop()
			s.hub.remove(sessionID, live)
		}()
		if err := s.recoverCursorSession(ctx, sessionID, live); err != nil {
			if errors.Is(err, context.Canceled) {
				live.publish(agent.Event{
					Type:    agent.EventNotice,
					Message: "stopped watching Cursor; the remote run may still be active",
				})
				return
			}
			live.publish(agent.Event{Type: agent.EventError, Err: s.cursorEventError(err)})
		}
	}()
	return live
}

func cursorStateNeedsRecovery(state *store.CursorSessionState) bool {
	if state == nil {
		return false
	}
	switch state.OperationState {
	case store.CursorOperationAwaitingApproval,
		store.CursorOperationCreateInFlight,
		store.CursorOperationRunInFlight,
		store.CursorOperationTerminal,
		store.CursorOperationAmbiguous:
		return true
	default:
		return false
	}
}

func (s *Server) recoverCursorSession(
	ctx context.Context,
	sessionID string,
	live *liveRun,
) error {
	state, err := s.db.GetCursorSessionState(ctx, sessionID)
	if err != nil {
		return err
	}
	if state.OperationState == store.CursorOperationRunInFlight &&
		state.RemoteStatus == cursorCancelInFlight {
		state, err = s.reconcileCursorCancelCrashMarker(
			context.Background(), state,
		)
		if err != nil {
			return err
		}
		if live != nil {
			live.publish(agent.Event{
				Type:    agent.EventNotice,
				Message: "Cursor cancellation outcome is ambiguous after restart; it will not be retried. Delete this local session to discard it.",
			})
		}
	}
	switch state.OperationState {
	case store.CursorOperationAwaitingApproval:
		_, err := s.mutateCursorState(context.Background(), sessionID,
			func(current *store.CursorSessionState) error {
				if current.OperationState != store.CursorOperationAwaitingApproval {
					return errCursorStateChanged
				}
				current.OperationState = store.CursorOperationIdle
				current.ReuseValid = false
				current.RemoteStatus = "APPROVAL_LOST_AFTER_RESTART"
				return nil
			})
		if err != nil {
			return err
		}
		return errors.New("Cursor approval was interrupted by a server restart; no remote request was sent")

	case store.CursorOperationAmbiguous:
		return errors.New(
			"Cursor create outcome is ambiguous and will not be retried automatically; delete this local session to discard it without retrying or cancelling remote work",
		)

	case store.CursorOperationCreateInFlight, store.CursorOperationRunInFlight:
		if state.AgentID == "" || state.RunID == "" {
			_, updateErr := s.mutateCursorState(context.Background(), sessionID,
				func(current *store.CursorSessionState) error {
					if current.OperationState != state.OperationState {
						return errCursorStateChanged
					}
					current.OperationState = store.CursorOperationAmbiguous
					current.ReuseValid = false
					current.RemoteStatus = "AMBIGUOUS_CREATE_OUTCOME"
					return nil
				})
			if updateErr != nil {
				return updateErr
			}
			return errors.New(
				"Cursor create outcome is ambiguous and will not be retried automatically; delete this local session to discard it without retrying or cancelling remote work",
			)
		}
		if state.OperationState == store.CursorOperationCreateInFlight {
			state, err = s.mutateCursorState(context.Background(), sessionID,
				func(current *store.CursorSessionState) error {
					if current.OperationState != store.CursorOperationCreateInFlight ||
						current.AgentID != state.AgentID || current.RunID != state.RunID {
						return errCursorStateChanged
					}
					current.OperationState = store.CursorOperationRunInFlight
					return nil
				})
			if err != nil {
				return err
			}
		}
		snapshot, snapshotErr := s.cursorRunner.GetRun(
			ctx, state.AgentID, state.RunID,
		)
		if snapshotErr == nil && snapshot != nil && cursorRunTerminal(snapshot.Status) {
			return s.finalizeCursorRun(
				context.Background(), sessionID, snapshot, live,
			)
		}
		if snapshotErr != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if snapshotErr == nil && snapshot != nil && snapshot.Status != "" {
			updated, updateErr := s.mutateCursorState(context.Background(), sessionID,
				func(current *store.CursorSessionState) error {
					if current.AgentID == state.AgentID && current.RunID == state.RunID {
						if !cursorCancelState(current.RemoteStatus) {
							current.RemoteStatus = truncateCursorRunes(
								s.redactCursorString(snapshot.Status),
								maxCursorRemoteStatusRunes,
							)
						}
					}
					return nil
				})
			if updateErr != nil {
				return updateErr
			}
			state = updated
		}
		return s.watchCursorRun(ctx, state, live)

	case store.CursorOperationTerminal:
		if state.AgentID != "" && state.RunID != "" {
			snapshot, snapshotErr := s.cursorRunner.GetRun(
				ctx, state.AgentID, state.RunID,
			)
			if snapshotErr == nil && snapshot != nil && cursorRunTerminal(snapshot.Status) {
				return s.finalizeCursorRun(
					context.Background(), sessionID, snapshot, live,
				)
			}
		}
		status := state.RemoteStatus
		if !cursorRunTerminal(status) {
			status = "FINISHED"
		}
		return s.finalizeCursorRun(
			context.Background(),
			sessionID,
			&cursor.Run{
				ID: state.RunID, AgentID: state.AgentID,
				Status: status, Result: state.PartialText,
			},
			live,
		)
	}
	return nil
}

type cursorCancelRequest struct {
	SessionID string `json:"session_id"`
}

func (s *Server) handleCursorCancel(w http.ResponseWriter, r *http.Request) {
	if s.requireDashboardPassword(w, r) {
		return
	}
	if s.agent == nil || s.db == nil || s.cursorRunner == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("Cursor cancellation is unavailable"))
		return
	}
	var request cursorCancelRequest
	if err := decodeBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, s.cursorSafeError(err))
		return
	}
	request.SessionID = strings.TrimSpace(request.SessionID)
	if request.SessionID == "" {
		writeError(w, http.StatusBadRequest, errors.New("session_id is required"))
		return
	}
	state, err := s.db.GetCursorSessionState(r.Context(), request.SessionID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, s.cursorSafeError(err))
		return
	}
	state, err = s.reconcileCursorCancelCrashMarker(r.Context(), state)
	if err != nil {
		writeError(w, http.StatusInternalServerError, s.cursorSafeError(err))
		return
	}
	if state.OperationState != store.CursorOperationRunInFlight ||
		state.AgentID == "" || state.RunID == "" {
		writeError(w, http.StatusConflict, errors.New("there is no active Cursor run to cancel"))
		return
	}
	if state.RemoteStatus == cursorCancelAmbiguous {
		writeError(w, http.StatusConflict, errors.New(
			"Cursor cancellation outcome is ambiguous and will not be retried; delete this local session to discard it without another remote request",
		))
		return
	}
	if cursorCancelState(state.RemoteStatus) {
		writeError(w, http.StatusConflict, errors.New("Cursor cancellation was already requested"))
		return
	}
	if !s.reserveCursorCancel(state.SessionID, state.RunID) {
		writeError(w, http.StatusConflict, errors.New("Cursor cancellation was already requested"))
		return
	}
	defer s.releaseCursorCancel(state.SessionID, state.RunID)

	live := s.hub.get(state.SessionID)
	if live == nil {
		live = s.cursorRecoveryRun(state.SessionID)
	}
	display, _ := json.Marshal(map[string]string{
		"operation": "cancel",
		"agent_id":  truncateCursorRunes(s.redactCursorString(state.AgentID), 128),
		"run_id":    truncateCursorRunes(s.redactCursorString(state.RunID), 128),
	})
	allowed, err := s.agent.AwaitOperationApproval(
		context.Background(),
		approval.Operation{
			SessionID: state.SessionID,
			Tool:      "cursor_direct_cancel",
			Arguments: string(display),
			Message:   "Cancel Cursor Cloud Agent run",
			Reason:    "remote cancellation changes Cursor state",
		},
		func(event agent.Event) error {
			if live != nil {
				live.publish(event)
			}
			return nil
		},
	)
	if err != nil {
		writeError(w, http.StatusRequestTimeout, s.cursorSafeError(err))
		return
	}
	if !allowed {
		writeError(w, http.StatusForbidden, errors.New("Cursor cancellation was refused"))
		return
	}

	latest, err := s.db.GetCursorSessionState(context.Background(), state.SessionID)
	if err != nil || latest.OperationState != store.CursorOperationRunInFlight ||
		latest.AgentID != state.AgentID || latest.RunID != state.RunID {
		writeError(w, http.StatusConflict, errors.New("Cursor run changed before cancellation"))
		return
	}
	priorStatus := state.RemoteStatus
	_, err = s.mutateCursorState(context.Background(), state.SessionID,
		func(current *store.CursorSessionState) error {
			if current.OperationState != store.CursorOperationRunInFlight ||
				current.AgentID != state.AgentID || current.RunID != state.RunID ||
				cursorCancelState(current.RemoteStatus) {
				return errCursorStateChanged
			}
			// This durable marker closes the crash window before the
			// non-idempotent cancellation POST.
			priorStatus = current.RemoteStatus
			current.RemoteStatus = cursorCancelInFlight
			return nil
		})
	if err != nil {
		writeError(w, http.StatusConflict, errors.New("Cursor run changed before cancellation"))
		return
	}
	if err := s.cursorRunner.CancelRun(
		context.Background(), state.AgentID, state.RunID,
	); err != nil {
		if cursorCancelNotFound(err) {
			_, updateErr := s.mutateCursorState(context.Background(), state.SessionID,
				func(current *store.CursorSessionState) error {
					if current.AgentID != state.AgentID || current.RunID != state.RunID ||
						current.RemoteStatus != cursorCancelInFlight {
						return errCursorStateChanged
					}
					current.RemoteStatus = cursorCancelNoActive
					current.OperationState = store.CursorOperationIdle
					current.ReuseValid = false
					return nil
				})
			if updateErr != nil && !errors.Is(updateErr, errCursorStateChanged) {
				writeError(w, http.StatusInternalServerError, s.cursorSafeError(updateErr))
				return
			}
			if live != nil {
				live.publish(agent.Event{
					Type: agent.EventToolProgress, Name: "cursor",
					Message: "Cursor run is no longer active",
				})
			}
			writeJSON(w, http.StatusOK, map[string]bool{
				"cancel_requested": false,
				"no_active_run":    true,
			})
			return
		}
		nextStatus := priorStatus
		if cursorCancelCouldBeAmbiguous(err) {
			nextStatus = cursorCancelAmbiguous
		}
		_, updateErr := s.mutateCursorState(context.Background(), state.SessionID,
			func(current *store.CursorSessionState) error {
				if current.AgentID != state.AgentID || current.RunID != state.RunID ||
					current.RemoteStatus != cursorCancelInFlight {
					return errCursorStateChanged
				}
				current.RemoteStatus = nextStatus
				return nil
			})
		if updateErr != nil && !errors.Is(updateErr, errCursorStateChanged) {
			writeError(w, http.StatusInternalServerError, s.cursorSafeError(updateErr))
			return
		}
		s.writeCursorUpstreamError(w, err, http.StatusBadGateway)
		return
	}
	_, _ = s.mutateCursorState(context.Background(), state.SessionID,
		func(current *store.CursorSessionState) error {
			if current.AgentID == state.AgentID && current.RunID == state.RunID {
				current.RemoteStatus = cursorCancelRequested
			}
			return nil
		})
	if live != nil {
		live.publish(agent.Event{
			Type: agent.EventToolProgress, Name: "cursor",
			Message: "Cursor cancellation requested",
		})
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancel_requested": true})
}

func cursorCancelNotFound(err error) bool {
	var apiError *cursor.APIError
	return errors.As(err, &apiError) && apiError.Status == http.StatusNotFound
}

func cursorCancelCouldBeAmbiguous(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiError *cursor.APIError
	if errors.As(err, &apiError) {
		return apiError.Status == 0 ||
			apiError.Status == http.StatusRequestTimeout ||
			apiError.Status >= http.StatusInternalServerError
	}
	return true
}

func (s *Server) reserveCursorCancel(sessionID, runID string) bool {
	s.cursorCancelMu.Lock()
	defer s.cursorCancelMu.Unlock()
	if s.cursorCancels == nil {
		s.cursorCancels = make(map[string]string)
	}
	if _, ok := s.cursorCancels[sessionID]; ok {
		return false
	}
	s.cursorCancels[sessionID] = runID
	return true
}

func (s *Server) releaseCursorCancel(sessionID, runID string) {
	s.cursorCancelMu.Lock()
	if s.cursorCancels[sessionID] == runID {
		delete(s.cursorCancels, sessionID)
	}
	s.cursorCancelMu.Unlock()
}

func (s *Server) cursorSessionHasActiveRemoteState(
	ctx context.Context,
	sessionID string,
) (bool, error) {
	state, err := s.db.GetCursorSessionState(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	state, err = s.reconcileCursorCancelCrashMarker(ctx, state)
	if err != nil {
		return false, err
	}
	if state.OperationState == store.CursorOperationAmbiguous {
		return false, nil
	}
	if state.OperationState == store.CursorOperationRunInFlight {
		switch state.RemoteStatus {
		case cursorCancelRequested, cursorCancelAmbiguous:
			return false, nil
		}
	}
	if state.OperationState == store.CursorOperationTerminal {
		if live := s.hub.get(sessionID); live != nil && live.isCursor() {
			return true, nil
		}
	}
	return cursorOperationActive(state.OperationState), nil
}

func (s *Server) cursorSessionHasUnfinishedState(
	ctx context.Context,
	sessionID string,
) (bool, error) {
	state, err := s.db.GetCursorSessionState(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return cursorOperationUnfinished(state.OperationState), nil
}

func (s *Server) invalidateCursorTarget(
	ctx context.Context,
	sessionID string,
	targetActive bool,
) error {
	_, err := s.mutateCursorState(ctx, sessionID,
		func(state *store.CursorSessionState) error {
			state.ReuseValid = false
			state.TargetActive = targetActive
			return nil
		})
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}
