package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/approval"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/cursorrun"
	"github.com/enowdev/antares/internal/store"
)

const (
	cursorAutoNoRepositoryIdentity = "antares://cursor/auto-discovery/no-repository"
	maxCursorPromptPreviewRunes    = 240
	maxCursorServerErrorRunes      = 4096
	maxCursorStartingRefRunes      = 1024
	maxCursorPartialRunes          = 1 << 20
	maxCursorIdentifierRunes       = 1024
	maxCursorRemoteStatusRunes     = 16 << 10
	maxCursorRepositoryRunes       = 16 << 10
	maxCursorGitStateRunes         = 256 << 10
	maxCursorApprovalWarningRunes  = 240
	maxCursorApprovalWarnings      = 4
)

type cursorChatRequest struct {
	SessionID     string                `json:"session_id"`
	Message       string                `json:"message"`
	Images        []string              `json:"images"`
	Model         cursor.ModelSelection `json:"model"`
	Mode          string                `json:"mode"`
	ProjectDir    string                `json:"project_dir,omitempty"`
	RepositoryURL *string               `json:"repository_url,omitempty"`
	StartingRef   *string               `json:"starting_ref,omitempty"`
	AutoCreatePR  bool                  `json:"auto_create_pr"`
}

type cursorTurnPlan struct {
	sessionID          string
	sessionTitle       string
	message            string
	images             []cursor.PromptImage
	model              cursor.ModelSelection
	modelParamsJSON    string
	mode               string
	repositoryURL      string
	repositoryIdentity string
	repositorySource   string
	startingRef        string
	worktreeDirty      bool
	localOnlyCommits   int
	remoteRefKnown     bool
	repositoryWarnings []string
	autoCreatePR       bool
	reuse              bool
	agentID            string
	userMessageID      string
	assistantMessageID string
	approvalArguments  string
}

type cursorRepositoryPlan struct {
	url              string
	identity         string
	ref              string
	source           string
	dirty            bool
	localOnlyCommits int
	remoteRefKnown   bool
	warnings         []string
}

type cursorApprovalModel struct {
	ID     string                           `json:"id"`
	Params []cursor.ModelParameterSelection `json:"params"`
}

type cursorDirectApprovalProjection struct {
	Operation     string              `json:"operation"`
	Kind          string              `json:"kind"`
	Model         cursorApprovalModel `json:"model"`
	RepositoryURL string              `json:"repository_url"`
	Repository    string              `json:"repository_source"`
	StartingRef   string              `json:"starting_ref"`
	WorktreeDirty bool                `json:"worktree_dirty"`
	LocalOnly     int                 `json:"local_only_commits"`
	RemoteKnown   bool                `json:"remote_ref_known"`
	Warnings      []string            `json:"warnings"`
	Mode          string              `json:"mode"`
	AutoCreatePR  bool                `json:"auto_create_pr"`
	PromptPreview string              `json:"prompt_preview"`
	ImageCount    int                 `json:"image_count"`
}

// handleCursorChat prepares one immutable direct Cursor operation and follows
// the shared live-run log. The coordinator itself uses a background context, so
// losing this HTTP follower never cancels or retries a remote mutation.
func (s *Server) handleCursorChat(w http.ResponseWriter, r *http.Request) {
	// This check deliberately precedes the 105 MiB decoder and image allocation.
	if s.requireDashboardPassword(w, r) {
		return
	}
	if s.agent == nil || s.db == nil || s.cursorRunner == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("Cursor chat is unavailable"))
		return
	}

	var request cursorChatRequest
	if err := decodeCursorChatBody(w, r, &request); err != nil {
		status := http.StatusBadRequest
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			status = http.StatusRequestEntityTooLarge
		}
		writeError(w, status, s.cursorSafeError(err))
		return
	}

	session, newSession, projectDir, err := s.cursorSessionCandidate(r.Context(), request)
	if err != nil {
		s.writeCursorPreparationError(w, err)
		return
	}
	plan, previous, err := s.prepareCursorTurn(r.Context(), request, session, projectDir)
	if err != nil {
		s.writeCursorPreparationError(w, err)
		return
	}

	approvalCtx, stopApproval := context.WithCancel(context.Background())
	live := newCursorLiveRun(liveRunCursorDirect)
	live.beginCursorApproval(stopApproval)
	unlockLifecycle := s.cursorLifecycles.Lock(plan.sessionID)
	if !s.hub.putIfAbsent(plan.sessionID, live) {
		unlockLifecycle()
		stopApproval()
		writeError(w, http.StatusConflict, errors.New("a turn is already active for this session"))
		return
	}

	if err := s.persistCursorAwaiting(
		r.Context(), session, newSession, previous, plan,
	); err != nil {
		unlockLifecycle()
		stopApproval()
		live.finish()
		s.hub.remove(plan.sessionID, live)
		if errors.Is(err, errCursorSessionBusy) {
			writeError(w, http.StatusConflict, errors.New("a turn is already active for this session"))
			return
		}
		writeError(w, http.StatusInternalServerError, s.cursorSafeError(err))
		return
	}
	unlockLifecycle()

	sse, err := newSSE(w)
	if err != nil {
		stopApproval()
		live.finish()
		s.hub.remove(plan.sessionID, live)
		_ = s.abandonCursorApproval(context.Background(), plan, "local stream unavailable")
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	// This is in the replay log before the coordinator can publish approval.
	live.publish(agent.Event{
		Type: agent.EventSession, ID: plan.sessionID, Title: plan.sessionTitle,
	})

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				live.publish(agent.Event{
					Type: agent.EventError,
					Err:  s.cursorSafeError(fmt.Errorf("Cursor coordinator failed")).Error(),
				})
			}
			live.publish(agent.Event{Type: agent.EventDone})
			live.finish()
			stopApproval()
			s.hub.remove(plan.sessionID, live)
		}()
		s.coordinateCursorTurn(approvalCtx, plan, live)
	}()

	ctx := r.Context()
	stopPing := make(chan struct{})
	defer close(stopPing)
	go cursorKeepalive(ctx, stopPing, sse)
	_ = live.follow(ctx, 0, func(event agent.Event, _ int) error {
		return sse.send(event)
	})
}

func cursorKeepalive(ctx context.Context, stop <-chan struct{}, sse *sseWriter) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			sse.comment("keepalive")
		}
	}
}

func (s *Server) cursorSessionCandidate(
	ctx context.Context,
	request cursorChatRequest,
) (*store.Session, bool, string, error) {
	if strings.TrimSpace(request.SessionID) != "" {
		session, err := s.db.GetSession(ctx, strings.TrimSpace(request.SessionID))
		if err == nil {
			projectDir, _ := session.Meta["project_dir"].(string)
			return session, false, strings.TrimSpace(projectDir), nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, false, "", err
		}
	}

	projectDir, err := validateCursorProjectDir(request.ProjectDir)
	if err != nil {
		return nil, false, "", err
	}
	cfg := s.config()
	session := &store.Session{
		ID:        newID("ses"),
		Title:     cursorSessionTitle(s.redactCursorString(request.Message)),
		Platform:  "web",
		Model:     cfg.Model.Default,
		Provider:  cfg.Model.Provider,
		Workspace: cfg.Agent.Workspace,
		Meta:      store.Meta{},
	}
	if projectDir != "" {
		session.Workspace = projectDir
		session.Meta["project_dir"] = projectDir
	}
	return session, true, projectDir, nil
}

func validateCursorProjectDir(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	dir := filepath.Clean(config.Expand(raw))
	if !filepath.IsAbs(dir) {
		return "", errors.New("project_dir must be an absolute path")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", errors.New("project_dir is not a directory")
	}
	return dir, nil
}

func cursorSessionTitle(message string) string {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\n", " "))
	if message == "" {
		return "Percakapan baru"
	}
	runes := []rune(message)
	if len(runes) > 60 {
		return string(runes[:60]) + "…"
	}
	return message
}

func (s *Server) prepareCursorTurn(
	ctx context.Context,
	request cursorChatRequest,
	session *store.Session,
	projectDir string,
) (cursorTurnPlan, *store.CursorSessionState, error) {
	var plan cursorTurnPlan
	if strings.TrimSpace(request.Message) == "" {
		return plan, nil, errors.New("message is required")
	}
	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = "agent"
	}
	if mode != "agent" && mode != "plan" {
		return plan, nil, errors.New("mode must be agent or plan")
	}

	validated, err := s.cursorRunner.ValidateModel(
		ctx,
		&cursor.ModelSelection{
			ID: strings.TrimSpace(request.Model.ID),
			Params: append(
				[]cursor.ModelParameterSelection(nil), request.Model.Params...,
			),
		},
		cursorrun.RequireExactVariant,
	)
	if err != nil {
		return plan, nil, err
	}
	if validated == nil {
		return plan, nil, errors.New("cursor model is required")
	}
	model := cloneCursorModelSelection(*validated)
	params := append([]cursor.ModelParameterSelection(nil), model.Params...)
	if params == nil {
		params = []cursor.ModelParameterSelection{}
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return plan, nil, fmt.Errorf("encode Cursor model parameters: %w", err)
	}

	images, err := decodeCursorImages(append([]string(nil), request.Images...))
	if err != nil {
		return plan, nil, err
	}
	repository, err := s.resolveCursorRepository(
		ctx, request.RepositoryURL, request.StartingRef, request.AutoCreatePR, projectDir,
	)
	if err != nil {
		return plan, nil, err
	}

	var previous *store.CursorSessionState
	previous, err = s.db.GetCursorSessionState(ctx, session.ID)
	if errors.Is(err, store.ErrNotFound) {
		previous = nil
	} else if err != nil {
		return plan, nil, err
	}
	if previous != nil {
		previous, err = s.reconcileCursorCancelCrashMarker(ctx, previous)
		if err != nil {
			return plan, nil, err
		}
	}
	if previous != nil {
		switch {
		case previous.OperationState == store.CursorOperationAmbiguous:
			return plan, previous, errCursorAmbiguousCreate
		case previous.OperationState == store.CursorOperationRunInFlight &&
			previous.RemoteStatus == cursorCancelAmbiguous:
			return plan, previous, errCursorAmbiguousCancel
		case cursorOperationUnfinished(previous.OperationState):
			return plan, previous, errCursorSessionBusy
		}
	}

	reuse := previous != nil &&
		previous.TargetActive &&
		previous.ReuseValid &&
		strings.TrimSpace(previous.AgentID) != "" &&
		previous.ModelID == model.ID &&
		previous.ModelParams == string(paramsJSON) &&
		previous.RepositoryURL == repository.identity &&
		previous.StartingRef == repository.ref &&
		previous.AutoCreatePR == request.AutoCreatePR

	plan = cursorTurnPlan{
		sessionID:          session.ID,
		sessionTitle:       truncateCursorRunes(s.redactCursorString(session.Title), 240),
		message:            strings.Clone(request.Message),
		images:             append([]cursor.PromptImage(nil), images...),
		model:              model,
		modelParamsJSON:    string(paramsJSON),
		mode:               mode,
		repositoryURL:      repository.url,
		repositoryIdentity: repository.identity,
		repositorySource:   repository.source,
		startingRef:        repository.ref,
		worktreeDirty:      repository.dirty,
		localOnlyCommits:   repository.localOnlyCommits,
		remoteRefKnown:     repository.remoteRefKnown,
		repositoryWarnings: append([]string(nil), repository.warnings...),
		autoCreatePR:       request.AutoCreatePR,
		reuse:              reuse,
		userMessageID:      newID("msg"),
		assistantMessageID: newID("msg"),
	}
	if reuse {
		plan.agentID = previous.AgentID
	}
	plan.approvalArguments, err = s.cursorTurnApprovalArguments(plan)
	if err != nil {
		return cursorTurnPlan{}, previous, err
	}
	return plan, previous, nil
}

func cloneCursorModelSelection(selection cursor.ModelSelection) cursor.ModelSelection {
	cloned := cursor.ModelSelection{
		ID: strings.Clone(selection.ID),
		Params: append(
			[]cursor.ModelParameterSelection(nil), selection.Params...,
		),
	}
	if selection.Params != nil && cloned.Params == nil {
		cloned.Params = []cursor.ModelParameterSelection{}
	}
	for i := range cloned.Params {
		cloned.Params[i].ID = strings.Clone(cloned.Params[i].ID)
		cloned.Params[i].Value = strings.Clone(cloned.Params[i].Value)
	}
	return cloned
}

func (s *Server) resolveCursorRepository(
	ctx context.Context,
	repositoryURL *string,
	startingRef *string,
	autoCreatePR bool,
	projectDir string,
) (cursorRepositoryPlan, error) {
	var result cursorRepositoryPlan
	requestedRef := ""
	if startingRef != nil {
		requestedRef = strings.TrimSpace(*startingRef)
		if err := validateCursorStartingRef(requestedRef); err != nil {
			return result, err
		}
	}

	if repositoryURL != nil {
		result.source = "explicit"
		raw := strings.TrimSpace(*repositoryURL)
		if raw == "" {
			if requestedRef != "" {
				return result, errors.New("repository_url is required when starting_ref is set")
			}
			if autoCreatePR {
				return result, errors.New("repository_url is required when auto_create_pr is true")
			}
			return result, nil
		}
		normalized, err := cursorrun.NormalizeGitHubRepository(raw)
		if err != nil {
			return result, err
		}
		if utf8.RuneCountInString(normalized) > maxCursorRepositoryRunes {
			return result, errors.New("repository_url is too long")
		}
		result.url = normalized
		result.identity = normalized
		result.ref = requestedRef
		return result, nil
	}

	result.source = "auto"
	if projectDir == "" {
		if requestedRef != "" {
			return result, errors.New("repository_url is required when starting_ref is set")
		}
		if autoCreatePR {
			return result, errors.New("repository_url is required when auto_create_pr is true")
		}
		result.identity = cursorAutoNoRepositoryIdentity
		return result, nil
	}
	info, err := cursorrun.InspectRepository(ctx, projectDir)
	if err != nil {
		return result, err
	}
	if !info.Repository {
		if requestedRef != "" {
			return result, errors.New("project_dir is not a repository for starting_ref")
		}
		if autoCreatePR {
			return result, errors.New("repository_url is required when auto_create_pr is true")
		}
		result.identity = cursorAutoNoRepositoryIdentity
		return result, nil
	}
	if info.URL == "" {
		return result, errors.New(
			"project origin is not a supported credential-free GitHub repository; choose an explicit repository or no repository",
		)
	}
	if utf8.RuneCountInString(info.URL) > maxCursorRepositoryRunes {
		return result, errors.New("discovered repository URL is too long")
	}
	result.url = info.URL
	result.identity = info.URL
	result.ref = info.StartingRef
	result.dirty = info.Dirty
	result.localOnlyCommits = max(0, info.LocalOnlyCommits)
	result.remoteRefKnown = info.RemoteRefKnown
	if !info.RemoteRefKnown {
		result.warnings = append(result.warnings,
			"The remote-tracking ref is unavailable, so Antares cannot verify which local commits are present in the Cursor cloud VM.")
	}
	if info.Dirty {
		result.warnings = append(result.warnings,
			"Local uncommitted changes are absent from the Cursor cloud VM.")
	}
	if info.LocalOnlyCommits > 0 {
		result.warnings = append(result.warnings,
			"Local-only commits not present on the remote ref are absent from the Cursor cloud VM.")
	}
	if startingRef != nil {
		result.ref = requestedRef
	}
	return result, nil
}

func validateCursorStartingRef(ref string) error {
	if ref == "" {
		return nil
	}
	if utf8.RuneCountInString(ref) > maxCursorStartingRefRunes {
		return errors.New("starting_ref is too long")
	}
	if strings.IndexFunc(ref, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) >= 0 {
		return errors.New("starting_ref contains whitespace or control characters")
	}
	if cursorCredentialToken.MatchString(ref) ||
		cursorCredentialAssignment.MatchString(ref) ||
		cursorBearerCredential.MatchString(ref) ||
		privateKeyMarker(ref) >= 0 {
		return errors.New("starting_ref contains credential-like data")
	}
	if ref == "@" ||
		strings.HasPrefix(ref, "/") ||
		strings.HasSuffix(ref, "/") ||
		strings.HasSuffix(ref, ".") ||
		strings.Contains(ref, "//") ||
		strings.Contains(ref, "..") ||
		strings.Contains(ref, "@{") ||
		strings.ContainsAny(ref, `~^:?*[\`) {
		return errors.New("starting_ref is not a valid Git ref or commit")
	}
	for _, component := range strings.Split(ref, "/") {
		if strings.HasPrefix(component, ".") ||
			strings.HasSuffix(strings.ToLower(component), ".lock") {
			return errors.New("starting_ref is not a valid Git ref or commit")
		}
	}
	return nil
}

var (
	errCursorSessionBusy     = errors.New("cursor session is active")
	errCursorAmbiguousCreate = errors.New("cursor create outcome is ambiguous")
	errCursorAmbiguousCancel = errors.New("cursor cancellation outcome is ambiguous")
)

func cursorOperationActive(operation string) bool {
	switch operation {
	case store.CursorOperationAwaitingApproval,
		store.CursorOperationCreateInFlight,
		store.CursorOperationRunInFlight,
		store.CursorOperationAmbiguous:
		return true
	default:
		return false
	}
}

func cursorOperationUnfinished(operation string) bool {
	return cursorOperationActive(operation) ||
		operation == store.CursorOperationTerminal
}

func (s *Server) persistCursorAwaiting(
	ctx context.Context,
	session *store.Session,
	newSession bool,
	previous *store.CursorSessionState,
	plan cursorTurnPlan,
) error {
	if newSession {
		if err := s.db.CreateSession(ctx, session); err != nil {
			return err
		}
	}

	state := &store.CursorSessionState{
		SessionID:          session.ID,
		TargetActive:       true,
		ReuseValid:         plan.reuse,
		ModelID:            plan.model.ID,
		ModelParams:        plan.modelParamsJSON,
		RepositoryURL:      plan.repositoryIdentity,
		StartingRef:        plan.startingRef,
		Mode:               plan.mode,
		AutoCreatePR:       plan.autoCreatePR,
		AgentID:            plan.agentID,
		RemoteStatus:       "AWAITING_APPROVAL",
		OperationState:     store.CursorOperationAwaitingApproval,
		UserMessageID:      plan.userMessageID,
		AssistantMessageID: plan.assistantMessageID,
	}
	if previous == nil {
		if err := s.db.PutCursorSessionState(ctx, state); err != nil {
			if newSession {
				_ = s.db.DeleteSession(context.Background(), session.ID)
			}
			if errors.Is(err, store.ErrCursorRevisionConflict) {
				return errCursorSessionBusy
			}
			return err
		}
	} else {
		state.Revision = previous.Revision
		swapped, err := s.db.CompareAndSwapCursorSessionState(
			ctx, state, previous.Revision,
		)
		if err != nil {
			if newSession {
				_ = s.db.DeleteSession(context.Background(), session.ID)
			}
			return err
		}
		if !swapped {
			if newSession {
				_ = s.db.DeleteSession(context.Background(), session.ID)
			}
			return errCursorSessionBusy
		}
	}
	// Append only after winning the durable CAS reservation. A competing server
	// therefore cannot leave a losing user message (or inflate session counts).
	if err := s.db.AppendMessage(ctx, &store.Message{
		ID: plan.userMessageID, SessionID: session.ID, Role: store.RoleUser,
		Content: plan.message,
		Meta:    store.Meta{"cursor_image_count": len(plan.images)},
	}); err != nil {
		if newSession {
			_ = s.db.DeleteSession(context.Background(), session.ID)
		} else {
			_, _ = s.mutateCursorState(context.Background(), session.ID,
				func(current *store.CursorSessionState) error {
					if current.OperationState == store.CursorOperationAwaitingApproval &&
						current.UserMessageID == plan.userMessageID {
						current.OperationState = store.CursorOperationIdle
						current.ReuseValid = false
						current.RemoteStatus = "USER_MESSAGE_PERSIST_FAILED"
					}
					return nil
				})
		}
		return err
	}
	return nil
}

func (s *Server) coordinateCursorTurn(
	ctx context.Context,
	plan cursorTurnPlan,
	live *liveRun,
) {
	approvalMessage := "Start Cursor Cloud Agent run"
	if plan.reuse {
		approvalMessage = "Continue Cursor Cloud Agent run"
	}
	allowed, approvalErr := s.agent.AwaitOperationApproval(
		ctx,
		approval.Operation{
			SessionID: plan.sessionID,
			Tool:      "cursor_direct",
			Arguments: plan.approvalArguments,
			Message:   approvalMessage,
			Reason:    "Cursor operations are paid and change remote state",
		},
		func(event agent.Event) error {
			live.publish(event)
			return nil
		},
	)
	if approvalErr != nil {
		_ = s.abandonCursorApproval(context.Background(), plan, "approval interrupted")
		live.publish(agent.Event{
			Type: agent.EventError,
			Err:  s.cursorSafeError(approvalErr).Error(),
		})
		return
	}
	if !allowed {
		_ = s.abandonCursorApproval(context.Background(), plan, "approval refused")
		live.publish(agent.Event{
			Type: agent.EventError,
			Err:  "Cursor operation was refused and no remote request was sent",
		})
		return
	}
	if err := ctx.Err(); err != nil {
		_ = s.abandonCursorApproval(context.Background(), plan, "local watcher stopped")
		live.publish(agent.Event{Type: agent.EventNotice, Message: "stopped watching Cursor"})
		return
	}

	operation := store.CursorOperationCreateInFlight
	if plan.reuse {
		operation = store.CursorOperationRunInFlight
	}
	_, err := s.mutateCursorState(context.Background(), plan.sessionID,
		func(state *store.CursorSessionState) error {
			if state.OperationState != store.CursorOperationAwaitingApproval ||
				state.UserMessageID != plan.userMessageID {
				return errCursorStateChanged
			}
			state.OperationState = operation
			state.RunID = ""
			state.LastEventID = ""
			state.PartialText = ""
			state.PartialReasoning = ""
			state.GitState = ""
			if plan.reuse {
				state.AgentID = plan.agentID
				state.RemoteStatus = "CREATE_RUN_IN_FLIGHT"
			} else {
				state.AgentID = ""
				state.ReuseValid = false
				state.RemoteStatus = "CREATE_AGENT_IN_FLIGHT"
			}
			return nil
		})
	if err != nil {
		live.publish(agent.Event{Type: agent.EventError, Err: s.cursorSafeError(err).Error()})
		return
	}
	if !live.beginCursorCreate() {
		stopErr := errors.New("local Stop won before the Cursor create request")
		_ = s.recordCursorCreateFailure(
			context.Background(), plan, operation, stopErr, false,
		)
		live.publish(agent.Event{
			Type: agent.EventNotice, Message: "stopped before starting Cursor",
		})
		return
	}

	// Once the non-idempotent POST boundary is crossed, local Stop may only
	// record detachment. The runner's own timeout still bounds transport hangs.
	agentID, run, createErr := s.createCursorRun(context.Background(), plan)
	if createErr != nil {
		ambiguous := cursorCreateCouldBeAmbiguous(createErr)
		_ = s.recordCursorCreateFailure(
			context.Background(), plan, operation, createErr, ambiguous,
		)
		message := s.cursorEventError(createErr)
		if ambiguous {
			message = "Cursor may have accepted the create request, but no run IDs were returned; it will not be retried automatically"
		}
		live.publish(agent.Event{Type: agent.EventError, Err: message})
		return
	}
	if run == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(run.ID) == "" {
		createErr = errors.New("Cursor returned no durable agent/run IDs")
		_ = s.recordCursorCreateFailure(
			context.Background(), plan, operation, createErr, true,
		)
		live.publish(agent.Event{
			Type: agent.EventError,
			Err:  "Cursor create response did not contain durable IDs; it will not be retried automatically",
		})
		return
	}

	state, err := s.mutateCursorState(context.Background(), plan.sessionID,
		func(state *store.CursorSessionState) error {
			if state.OperationState != operation ||
				state.UserMessageID != plan.userMessageID {
				return errCursorStateChanged
			}
			state.AgentID = agentID
			state.RunID = run.ID
			state.RemoteStatus = truncateCursorRunes(
				s.redactCursorString(run.Status), maxCursorRemoteStatusRunes,
			)
			state.OperationState = store.CursorOperationRunInFlight
			state.ReuseValid = true
			return nil
		})
	if err != nil {
		live.publish(agent.Event{
			Type: agent.EventError,
			Err:  "Cursor accepted the run, but its IDs could not be persisted safely",
		})
		return
	}
	watchCtx, stopWatch := context.WithCancel(context.Background())
	if !live.beginCursorWatch(stopWatch) {
		live.publish(agent.Event{
			Type:    agent.EventNotice,
			Message: "stopped watching Cursor after its run IDs were saved; attach to recover",
		})
		return
	}
	defer stopWatch()
	if err := s.watchCursorRun(watchCtx, state, live); err != nil {
		if errors.Is(err, context.Canceled) {
			live.publish(agent.Event{
				Type:    agent.EventNotice,
				Message: "stopped watching Cursor; the remote run may still be active",
			})
			return
		}
		live.publish(agent.Event{Type: agent.EventError, Err: s.cursorEventError(err)})
	}
}

func (s *Server) cursorTurnApprovalArguments(plan cursorTurnPlan) (string, error) {
	params := append([]cursor.ModelParameterSelection(nil), plan.model.Params...)
	if params == nil {
		params = []cursor.ModelParameterSelection{}
	}
	kind := "new_agent"
	operation := "start"
	if plan.reuse {
		kind = "follow_up"
		operation = "follow_up"
	}
	warningCount := min(len(plan.repositoryWarnings), maxCursorApprovalWarnings)
	warnings := make([]string, 0, warningCount)
	for _, warning := range plan.repositoryWarnings[:warningCount] {
		warnings = append(warnings, truncateCursorRunes(
			s.redactCursorString(warning), maxCursorApprovalWarningRunes,
		))
	}
	display := cursorDirectApprovalProjection{
		Operation: operation,
		Kind:      kind,
		Model: cursorApprovalModel{
			ID: plan.model.ID, Params: params,
		},
		RepositoryURL: plan.repositoryURL,
		Repository:    plan.repositorySource,
		StartingRef:   plan.startingRef,
		WorktreeDirty: plan.worktreeDirty,
		LocalOnly:     max(0, plan.localOnlyCommits),
		RemoteKnown:   plan.remoteRefKnown,
		Warnings:      warnings,
		Mode:          plan.mode,
		AutoCreatePR:  plan.autoCreatePR,
		PromptPreview: s.cursorPromptPreview(plan.message),
		ImageCount:    len(plan.images),
	}
	raw, err := json.Marshal(display)
	if err != nil {
		return "", err
	}
	if len(raw) > 16<<10 {
		return "", errors.New("Cursor approval projection exceeds the safe display limit")
	}
	return string(raw), nil
}

func (s *Server) createCursorRun(
	ctx context.Context,
	plan cursorTurnPlan,
) (string, *cursor.Run, error) {
	prompt := cursor.Prompt{
		Text:   strings.Clone(plan.message),
		Images: append([]cursor.PromptImage(nil), plan.images...),
	}
	if plan.reuse {
		run, err := s.cursorRunner.CreateRun(ctx, plan.agentID, cursor.CreateRunRequest{
			Prompt: prompt,
			Mode:   plan.mode,
		})
		return plan.agentID, run, err
	}
	repositories := []cursor.Repository{}
	if plan.repositoryURL != "" {
		repositories = append(repositories, cursor.Repository{
			URL: plan.repositoryURL, StartingRef: plan.startingRef,
		})
	}
	model := cloneCursorModelSelection(plan.model)
	created, err := s.cursorRunner.CreateAgent(ctx, cursor.CreateAgentRequest{
		Prompt:              prompt,
		Model:               &model,
		Repos:               repositories,
		AutoCreatePR:        plan.autoCreatePR,
		SkipReviewerRequest: true,
		Mode:                plan.mode,
	})
	if err != nil {
		return "", nil, err
	}
	if created == nil {
		return "", nil, errors.New("Cursor returned an empty create response")
	}
	return created.Agent.ID, &created.Run, nil
}

func cursorCreateCouldBeAmbiguous(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, cursorrun.ErrNotConfigured) {
		// Runner option resolution happens before either create POST.
		return false
	}
	var apiError *cursor.APIError
	if errors.As(err, &apiError) {
		// A definitive client rejection cannot have created the run. Timeout
		// and server/gateway responses can arrive after the mutation committed.
		return apiError.Status == 0 ||
			apiError.Status == http.StatusRequestTimeout ||
			apiError.Status >= http.StatusInternalServerError
	}
	return true
}

func (s *Server) recordCursorCreateFailure(
	ctx context.Context,
	plan cursorTurnPlan,
	operation string,
	createErr error,
	ambiguous bool,
) error {
	_, err := s.mutateCursorState(ctx, plan.sessionID,
		func(state *store.CursorSessionState) error {
			if state.OperationState != operation ||
				state.UserMessageID != plan.userMessageID {
				return errCursorStateChanged
			}
			state.ReuseValid = false
			state.RemoteStatus = s.cursorSafeError(createErr).Error()
			if ambiguous {
				state.OperationState = store.CursorOperationAmbiguous
			} else {
				state.OperationState = store.CursorOperationIdle
			}
			return nil
		})
	return err
}

func (s *Server) abandonCursorApproval(
	ctx context.Context,
	plan cursorTurnPlan,
	status string,
) error {
	_, err := s.mutateCursorState(ctx, plan.sessionID,
		func(state *store.CursorSessionState) error {
			if state.OperationState != store.CursorOperationAwaitingApproval ||
				state.UserMessageID != plan.userMessageID {
				return nil
			}
			state.OperationState = store.CursorOperationIdle
			state.RemoteStatus = status
			state.ReuseValid = false
			return nil
		})
	return err
}

func (s *Server) writeCursorPreparationError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, errCursorAmbiguousCreate) {
		status = http.StatusConflict
		err = errors.New(
			"Cursor create outcome is ambiguous and will not be retried; delete this local session to discard it without retrying or cancelling remote work",
		)
	} else if errors.Is(err, errCursorAmbiguousCancel) {
		status = http.StatusConflict
		err = errors.New(
			"Cursor cancellation outcome is ambiguous and will not be retried; delete this local session to discard it without another remote request",
		)
	} else if errors.Is(err, errCursorSessionBusy) {
		status = http.StatusConflict
		err = errors.New("a turn is already active for this session")
	} else if errors.Is(err, cursorrun.ErrNotConfigured) {
		status = http.StatusPreconditionRequired
	} else {
		var apiError *cursor.APIError
		if errors.As(err, &apiError) {
			switch apiError.Status {
			case http.StatusUnauthorized, http.StatusForbidden,
				http.StatusTooManyRequests:
				status = apiError.Status
			}
			if apiError.RetryAfter > 0 {
				w.Header().Set("Retry-After", fmt.Sprintf(
					"%d", max(1, int(apiError.RetryAfter/time.Second)),
				))
			}
		}
	}
	writeError(w, status, s.cursorSafeError(err))
}

func (s *Server) writeCursorUpstreamError(
	w http.ResponseWriter,
	err error,
	defaultStatus int,
) {
	status := defaultStatus
	var apiError *cursor.APIError
	if errors.Is(err, cursorrun.ErrNotConfigured) {
		status = http.StatusPreconditionRequired
	} else if errors.As(err, &apiError) {
		switch apiError.Status {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
			http.StatusNotFound, http.StatusConflict,
			http.StatusRequestTimeout, http.StatusTooManyRequests:
			status = apiError.Status
		default:
			if apiError.Status >= http.StatusInternalServerError {
				status = apiError.Status
			}
		}
		if apiError.RetryAfter > 0 {
			w.Header().Set("Retry-After", fmt.Sprintf(
				"%d", max(1, int(apiError.RetryAfter/time.Second)),
			))
		}
	}
	writeError(w, status, errors.New(s.cursorEventError(err)))
}

var (
	cursorCredentialToken = regexp.MustCompile(
		`(?i)\b(?:(?:crsr|github_pat|ghp|gho|ghu|ghs|ghr|sk)[_-][a-z0-9_-]{6,}|AKIA[0-9A-Z]{16})`,
	)
	cursorCredentialAssignment = regexp.MustCompile(
		`(?i)\b(api[_-]?key|authorization|bearer|password|secret|token)\s*[:=]\s*(?:"[^"]*"|'[^']*'|[^\s,;]+)`,
	)
	cursorBearerCredential = regexp.MustCompile(
		`(?i)\bbearer\s+[a-z0-9._~+/=-]{6,}`,
	)
	cursorURLUserinfo      = regexp.MustCompile(`(?i)(https?://)[^/\s@]+@`)
	cursorPrivateKeyHeader = regexp.MustCompile(
		`(?i)-----BEGIN[^\r\n]*PRIVATE KEY[^\r\n]*-----`,
	)
)

func (s *Server) cursorPromptPreview(message string) string {
	return truncateCursorRunes(s.redactCursorString(message), maxCursorPromptPreviewRunes)
}

func (s *Server) cursorSafeError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(truncateCursorRunes(
		s.redactCursorString(err.Error()), maxCursorServerErrorRunes,
	))
}

func (s *Server) cursorEventError(err error) string {
	if err == nil {
		return ""
	}
	message := s.cursorSafeError(err).Error()
	var apiError *cursor.APIError
	if errors.As(err, &apiError) && apiError.RetryAfter > 0 {
		message += fmt.Sprintf(
			" (retry after %d seconds)",
			max(1, int(apiError.RetryAfter/time.Second)),
		)
	}
	return truncateCursorRunes(message, maxCursorServerErrorRunes)
}

func (s *Server) redactCursorString(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if cfg := s.config(); cfg != nil {
		_, provider := cfg.ResolveProvider("cursor")
		for _, secret := range []string{
			strings.TrimSpace(provider.APIKey),
			strings.TrimSpace(cfg.Server.AuthToken),
		} {
			if secret != "" {
				value = strings.ReplaceAll(value, secret, "[REDACTED]")
			}
		}
	}
	value = cursorURLUserinfo.ReplaceAllString(value, `${1}[REDACTED]@`)
	value = cursorCredentialAssignment.ReplaceAllString(value, `${1}=[REDACTED]`)
	value = cursorBearerCredential.ReplaceAllString(value, "Bearer [REDACTED]")
	value = cursorCredentialToken.ReplaceAllString(value, "[REDACTED]")
	if marker := privateKeyMarker(value); marker >= 0 {
		value = value[:marker] + "[REDACTED PRIVATE KEY]"
	}
	return value
}

func privateKeyMarker(value string) int {
	location := cursorPrivateKeyHeader.FindStringIndex(value)
	if location == nil {
		return -1
	}
	return location[0]
}

func truncateCursorRunes(value string, maximum int) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if maximum <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	if maximum == 1 {
		return "…"
	}
	return string(runes[:maximum-1]) + "…"
}
