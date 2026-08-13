package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/store"
)

const (
	maxCursorProjectionStatusRunes = 512
	maxCursorProjectionBranches    = 16
	maxCursorProjectionParams      = 64
)

type cursorBranchProjection struct {
	RepoURL string `json:"repo_url"`
	Branch  string `json:"branch"`
	PRURL   string `json:"pr_url"`
}

type cursorGitProjection struct {
	Branches []cursorBranchProjection `json:"branches"`
}

// cursorSessionProjection is the composer-facing view of durable Cursor state:
// enough to restore the execution target and describe the run's outcome, and
// nothing else. Revision, partial prompt and answer text, recovery identifiers
// (agent, run, last event), and internal message IDs deliberately stay on the
// server — they are recovery machinery, not composer state, and some of them
// carry user content.
type cursorSessionProjection struct {
	TargetActive bool `json:"target_active"`
	ReuseValid   bool `json:"reuse_valid"`
	// ModelID and ModelParams are populated together or not at all: a partially
	// understood selection must never become a different one.
	ModelID     string                           `json:"model_id"`
	ModelParams []cursor.ModelParameterSelection `json:"model_params"`
	// RepositoryURL is null when the run discovered its repository (or ran with
	// none) rather than being given one, so restoring it reproduces the same
	// run identity instead of pinning an explicit empty repository.
	RepositoryURL  *string             `json:"repository_url"`
	StartingRef    string              `json:"starting_ref"`
	Mode           string              `json:"mode"`
	AutoCreatePR   bool                `json:"auto_create_pr"`
	RemoteStatus   string              `json:"remote_status"`
	OperationState string              `json:"operation_state"`
	Git            cursorGitProjection `json:"git"`
}

// cursorSessionView loads the durable Cursor state for one session. A session
// that never ran Cursor yields nil; a store failure is returned as an error, so
// a read problem can never be presented as "this session has no Cursor state".
func (s *Server) cursorSessionView(
	ctx context.Context,
	sessionID string,
) (*cursorSessionProjection, error) {
	if s.db == nil {
		return nil, nil
	}
	state, err := s.db.GetCursorSessionState(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.projectCursorState(state), nil
}

func (s *Server) projectCursorState(
	state *store.CursorSessionState,
) *cursorSessionProjection {
	if state == nil {
		return nil
	}
	view := &cursorSessionProjection{
		TargetActive: state.TargetActive,
		ReuseValid:   state.ReuseValid,
		ModelParams:  []cursor.ModelParameterSelection{},
		StartingRef: truncateCursorRunes(
			s.redactCursorString(state.StartingRef), maxCursorStartingRefRunes,
		),
		AutoCreatePR: state.AutoCreatePR,
		RemoteStatus: truncateCursorRunes(
			s.redactCursorString(state.RemoteStatus), maxCursorProjectionStatusRunes,
		),
		OperationState: state.OperationState,
		Git: cursorGitProjection{
			Branches: s.projectCursorGitState(state.GitState),
		},
	}
	// Only the two modes a turn can be prepared with are meaningful to the
	// composer; anything else is reported as no mode at all.
	if state.Mode == "agent" || state.Mode == "plan" {
		view.Mode = state.Mode
	}
	// The auto-discovery identity is an internal marker, not a repository.
	if state.RepositoryURL != cursorAutoNoRepositoryIdentity {
		repository := truncateCursorRunes(
			s.redactCursorString(state.RepositoryURL), maxCursorRepositoryRunes,
		)
		view.RepositoryURL = &repository
	}
	if params, ok := s.decodeCursorProjectionParams(state.ModelParams); ok {
		view.ModelID = truncateCursorRunes(
			s.redactCursorString(state.ModelID), maxCursorIdentifierRunes,
		)
		view.ModelParams = params
	}
	return view
}

// decodeCursorProjectionParams reads a stored selection back into its exact
// ordered parameters. The store only guarantees a JSON array, so a value that
// is not a well-formed, unambiguous parameter list is reported as undecodable
// rather than partially restored.
func (s *Server) decodeCursorProjectionParams(
	raw string,
) ([]cursor.ModelParameterSelection, bool) {
	params := []cursor.ModelParameterSelection{}
	if strings.TrimSpace(raw) == "" {
		return params, true
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return nil, false
	}
	if len(params) > maxCursorProjectionParams {
		return nil, false
	}
	seen := make(map[string]struct{}, len(params))
	for i := range params {
		id := truncateCursorRunes(
			s.redactCursorString(params[i].ID), maxCursorIdentifierRunes,
		)
		if id == "" {
			return nil, false
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, false
		}
		seen[id] = struct{}{}
		params[i].ID = id
		params[i].Value = truncateCursorRunes(
			s.redactCursorString(params[i].Value), maxCursorIdentifierRunes,
		)
	}
	return params, true
}

func (s *Server) projectCursorGitState(raw string) []cursorBranchProjection {
	branches := []cursorBranchProjection{}
	if strings.TrimSpace(raw) == "" {
		return branches
	}
	var git cursor.GitState
	if err := json.Unmarshal([]byte(raw), &git); err != nil {
		return branches
	}
	for _, branch := range git.Branches {
		if len(branches) >= maxCursorProjectionBranches {
			break
		}
		branches = append(branches, cursorBranchProjection{
			RepoURL: truncateCursorRunes(
				s.redactCursorString(branch.RepoURL), maxCursorRepositoryRunes,
			),
			Branch: truncateCursorRunes(
				s.redactCursorString(branch.Branch), maxCursorStartingRefRunes,
			),
			PRURL: truncateCursorRunes(
				s.redactCursorString(branch.PRURL), maxCursorRepositoryRunes,
			),
		})
	}
	return branches
}
