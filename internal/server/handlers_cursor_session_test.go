package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// getSessionDetail reads one session detail document exactly as the dashboard
// does, so the assertions below describe the wire contract rather than an
// internal struct.
func getSessionDetail(
	t *testing.T,
	fixture *cursorDirectFixture,
	sessionID string,
) (int, map[string]any, string) {
	t.Helper()
	return getSessionDetailAt(t, fixture.http.URL, fixture.http.Client(), sessionID)
}

func getSessionDetailAt(
	t *testing.T,
	baseURL string,
	client *http.Client,
	sessionID string,
) (int, map[string]any, string) {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodGet,
		baseURL+"/api/sessions/"+sessionID,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if response.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode session detail: %v (%s)", err, raw)
		}
	}
	return response.StatusCode, body, string(raw)
}

func seedCursorSession(
	t *testing.T,
	fixture *cursorDirectFixture,
	sessionID string,
	mutate func(*store.CursorSessionState),
) {
	t.Helper()
	ctx := context.Background()
	if err := fixture.db.CreateSession(ctx, &store.Session{
		ID: sessionID, Title: "Cursor session",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	state := &store.CursorSessionState{
		SessionID:          sessionID,
		TargetActive:       true,
		ReuseValid:         true,
		ModelID:            "gpt-5.6-sol",
		ModelParams:        `[{"id":"cyber","value":"false"},{"id":"reasoning","value":"max"}]`,
		RepositoryURL:      "https://github.com/acme/repo",
		StartingRef:        "main",
		Mode:               "plan",
		AutoCreatePR:       true,
		AgentID:            "bc-secret-agent",
		RunID:              "run-secret",
		RemoteStatus:       "RUNNING",
		OperationState:     store.CursorOperationRunInFlight,
		PartialText:        "half of a private answer",
		UserMessageID:      "msg-user-internal",
		AssistantMessageID: "msg-assistant-internal",
	}
	if mutate != nil {
		mutate(state)
	}
	if err := fixture.db.PutCursorSessionState(ctx, state); err != nil {
		t.Fatalf("put cursor state: %v", err)
	}
}

func cursorStateOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	raw, ok := body["cursor_state"]
	if !ok {
		t.Fatal("session detail has no cursor_state field")
	}
	if raw == nil {
		t.Fatal("cursor_state is null, want a projection")
	}
	state, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("cursor_state = %T, want an object", raw)
	}
	return state
}

func TestSessionDetailReportsNoCursorStateAsNull(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	if err := fixture.db.CreateSession(context.Background(), &store.Session{
		ID: "ses-plain", Title: "Plain chat",
	}); err != nil {
		t.Fatal(err)
	}

	status, body, raw := getSessionDetail(t, fixture, "ses-plain")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	value, ok := body["cursor_state"]
	if !ok {
		t.Fatalf("cursor_state is absent for a session without Cursor state: %s", raw)
	}
	if value != nil {
		t.Fatalf("cursor_state = %#v, want null", value)
	}
}

func TestSessionDetailProjectsExactCursorSelection(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	seedCursorSession(t, fixture, "ses-active", func(state *store.CursorSessionState) {
		state.GitState = `{"branches":[{"repoUrl":"https://github.com/acme/repo","branch":"cursor/x","prUrl":"https://github.com/acme/repo/pull/7"}]}`
	})

	status, body, raw := getSessionDetail(t, fixture, "ses-active")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	state := cursorStateOf(t, body)

	if state["target_active"] != true || state["reuse_valid"] != true {
		t.Fatalf("target/reuse = %#v/%#v", state["target_active"], state["reuse_valid"])
	}
	if state["model_id"] != "gpt-5.6-sol" {
		t.Fatalf("model_id = %#v", state["model_id"])
	}
	// The whole stored selection, in order, including a param the catalogue
	// never lists as a user-facing dimension.
	wantParams := []any{
		map[string]any{"id": "cyber", "value": "false"},
		map[string]any{"id": "reasoning", "value": "max"},
	}
	gotParams, _ := state["model_params"].([]any)
	if len(gotParams) != len(wantParams) {
		t.Fatalf("model_params = %#v, want %#v", state["model_params"], wantParams)
	}
	for i := range wantParams {
		got, _ := gotParams[i].(map[string]any)
		want, _ := wantParams[i].(map[string]any)
		if got["id"] != want["id"] || got["value"] != want["value"] {
			t.Fatalf("model_params[%d] = %#v, want %#v", i, gotParams[i], wantParams[i])
		}
	}
	if state["repository_url"] != "https://github.com/acme/repo" ||
		state["starting_ref"] != "main" || state["mode"] != "plan" ||
		state["auto_create_pr"] != true {
		t.Fatalf("repository projection = %#v", state)
	}
	if state["remote_status"] != "RUNNING" ||
		state["operation_state"] != store.CursorOperationRunInFlight {
		t.Fatalf("status projection = %#v", state)
	}
	git, _ := state["git"].(map[string]any)
	branches, _ := git["branches"].([]any)
	if len(branches) != 1 {
		t.Fatalf("git projection = %#v", state["git"])
	}
	branch, _ := branches[0].(map[string]any)
	if branch["repo_url"] != "https://github.com/acme/repo" ||
		branch["branch"] != "cursor/x" ||
		branch["pr_url"] != "https://github.com/acme/repo/pull/7" {
		t.Fatalf("branch projection = %#v", branch)
	}

	// Nothing internal, recoverable-only, or partially generated may travel to
	// the browser with the composer's restore data.
	for _, forbidden := range []string{
		"revision", "partial_text", "partial_reasoning", "agent_id", "run_id",
		"user_message_id", "assistant_message_id", "last_event_id",
		"bc-secret-agent", "run-secret", "half of a private answer",
		"msg-user-internal", "msg-assistant-internal",
	} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("session detail leaked %q: %s", forbidden, raw)
		}
	}
}

func TestSessionDetailKeepsInactiveCursorTargetInactive(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	seedCursorSession(t, fixture, "ses-inactive", func(state *store.CursorSessionState) {
		state.TargetActive = false
		state.ReuseValid = false
		state.OperationState = store.CursorOperationCommitted
	})

	_, body, raw := getSessionDetail(t, fixture, "ses-inactive")
	state := cursorStateOf(t, body)
	if state["target_active"] != false || state["reuse_valid"] != false {
		t.Fatalf("inactive projection = %#v (%s)", state, raw)
	}
	if state["operation_state"] != store.CursorOperationCommitted {
		t.Fatalf("operation_state = %#v", state["operation_state"])
	}
}

func TestSessionDetailNeverExposesAutoNoRepositorySentinel(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	seedCursorSession(t, fixture, "ses-auto", func(state *store.CursorSessionState) {
		state.RepositoryURL = cursorAutoNoRepositoryIdentity
		state.StartingRef = ""
	})

	_, body, raw := getSessionDetail(t, fixture, "ses-auto")
	state := cursorStateOf(t, body)
	value, ok := state["repository_url"]
	if !ok {
		t.Fatal("repository_url is absent")
	}
	// null means "discover it again", which is the identity this run used; an
	// empty string would mean the user explicitly chose no repository.
	if value != nil {
		t.Fatalf("repository_url = %#v, want null for auto-discovery", value)
	}
	if strings.Contains(raw, "antares://") {
		t.Fatalf("session detail leaked the auto-discovery sentinel: %s", raw)
	}
}

func TestSessionDetailDropsUndecodableCursorSelection(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	// The store guarantees a JSON array, not that every element is a parameter.
	seedCursorSession(t, fixture, "ses-broken", func(state *store.CursorSessionState) {
		state.ModelParams = `[{"id":"reasoning","value":42}]`
	})

	_, body, raw := getSessionDetail(t, fixture, "ses-broken")
	state := cursorStateOf(t, body)
	if state["model_id"] != "" {
		t.Fatalf("model_id = %#v, want no selection when its params cannot be decoded (%s)",
			state["model_id"], raw)
	}
	params, _ := state["model_params"].([]any)
	if len(params) != 0 {
		t.Fatalf("model_params = %#v, want empty", state["model_params"])
	}
	if state["target_active"] != true {
		t.Fatalf("an undecodable selection must not hide the durable state: %#v", state)
	}
}

func TestSessionDetailCopiesCatalogueValuesExactly(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	// Catalogue identifiers are opaque and case-sensitive: a projection that
	// rewrites them would resolve to a different variant, or to none at all.
	seedCursorSession(t, fixture, "ses-opaque", func(state *store.CursorSessionState) {
		state.ModelID = "GPT-5.6-Sol_Max"
		state.ModelParams = `[{"id":"Reasoning","value":"MAX"},{"id":"context","value":"1M"}]`
		state.StartingRef = "Feature/Big-Change"
		state.RepositoryURL = "https://github.com/Acme/Repo"
	})

	_, body, raw := getSessionDetail(t, fixture, "ses-opaque")
	state := cursorStateOf(t, body)
	if state["model_id"] != "GPT-5.6-Sol_Max" {
		t.Fatalf("model_id = %#v, want the stored value unchanged (%s)", state["model_id"], raw)
	}
	if state["starting_ref"] != "Feature/Big-Change" {
		t.Fatalf("starting_ref = %#v, want the stored value unchanged", state["starting_ref"])
	}
	if state["repository_url"] != "https://github.com/Acme/Repo" {
		t.Fatalf("repository_url = %#v, want the stored value unchanged", state["repository_url"])
	}
	params, _ := state["model_params"].([]any)
	if len(params) != 2 {
		t.Fatalf("model_params = %#v", state["model_params"])
	}
	first, _ := params[0].(map[string]any)
	second, _ := params[1].(map[string]any)
	if first["id"] != "Reasoning" || first["value"] != "MAX" ||
		second["id"] != "context" || second["value"] != "1M" {
		t.Fatalf("model_params = %#v, want the stored values unchanged", params)
	}
}

func TestSessionDetailRejectsUnsafeSelectionInsteadOfRewritingIt(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*store.CursorSessionState)
	}{
		{
			name: "credential-like model id",
			apply: func(state *store.CursorSessionState) {
				state.ModelID = "sk-live_abcdef123456"
			},
		},
		{
			name: "credential-like parameter value",
			apply: func(state *store.CursorSessionState) {
				state.ModelParams = `[{"id":"reasoning","value":"authorization: Bearer abcdef123456"}]`
			},
		},
		{
			name: "control characters in a parameter id",
			apply: func(state *store.CursorSessionState) {
				state.ModelParams = "[{\"id\":\"reason\\ning\",\"value\":\"max\"}]"
			},
		},
		{
			name: "repository carrying credentials",
			apply: func(state *store.CursorSessionState) {
				state.RepositoryURL = "https://user:secret@github.com/acme/repo"
			},
		},
		{
			name: "starting ref with control characters",
			apply: func(state *store.CursorSessionState) {
				state.StartingRef = "main\nrm -rf"
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newCursorDirectTestServer(t)
			seedCursorSession(t, fixture, "ses-unsafe", tt.apply)

			_, body, raw := getSessionDetail(t, fixture, "ses-unsafe")
			state := cursorStateOf(t, body)
			if state["model_id"] != "" {
				t.Fatalf("model_id = %#v, want the whole selection rejected (%s)",
					state["model_id"], raw)
			}
			params, _ := state["model_params"].([]any)
			if len(params) != 0 {
				t.Fatalf("model_params = %#v, want empty", state["model_params"])
			}
			for _, forbidden := range []string{
				"sk-live_abcdef123456", "Bearer abcdef123456", "user:secret",
				"rm -rf", "REDACTED",
			} {
				if strings.Contains(raw, forbidden) {
					t.Fatalf("projection kept or rewrote an unsafe value %q: %s", forbidden, raw)
				}
			}
		})
	}
}

func TestSessionDetailRedactsCursorProjectionStrings(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	seedCursorSession(t, fixture, "ses-secret", func(state *store.CursorSessionState) {
		state.RemoteStatus = "ERROR: authorization: Bearer test-token"
		state.GitState = `{"branches":[{"repoUrl":"https://user:test-token@github.com/acme/repo","branch":"main","prUrl":""}]}`
	})

	_, _, raw := getSessionDetail(t, fixture, "ses-secret")
	if strings.Contains(raw, "test-token") {
		t.Fatalf("session detail leaked a credential: %s", raw)
	}
	if !strings.Contains(raw, "REDACTED") {
		t.Fatalf("session detail did not redact the projection: %s", raw)
	}
}

type cursorStateErrorStore struct {
	store.Store
	err error
}

func (s cursorStateErrorStore) GetCursorSessionState(
	context.Context, string,
) (*store.CursorSessionState, error) {
	return nil, s.err
}

func TestSessionDetailStoreFailureIsNotReportedAsNoCursorState(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	if err := fixture.db.CreateSession(context.Background(), &store.Session{
		ID: "ses-store-error", Title: "Cursor session",
	}); err != nil {
		t.Fatal(err)
	}
	failing := cursorStateErrorStore{
		Store: fixture.db,
		err:   errors.New("cursor state read failed for test-token"),
	}
	broken := New(Options{
		Config: fixture.cfg,
		Agent:  agent.New(fixture.cfg, failing, tools.NewRegistry(), nil, nil),
		Store:  failing,
		Cursor: fixture.runner,
	})
	brokenHTTP := httptest.NewServer(broken.Handler())
	defer brokenHTTP.Close()

	status, _, raw := getSessionDetailAt(
		t, brokenHTTP.URL, brokenHTTP.Client(), "ses-store-error",
	)
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want a reported failure rather than no state", status, raw)
	}
	if strings.Contains(raw, "test-token") {
		t.Fatalf("store failure leaked a credential: %s", raw)
	}
}
