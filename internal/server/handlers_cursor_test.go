package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/cursor"
	"github.com/enowdev/antares/internal/cursorrun"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

type cursorStreamCall struct {
	AgentID     string
	RunID       string
	LastEventID string
}

type fakeCursorRunner struct {
	mu sync.Mutex

	validateErr error
	validated   *cursor.ModelSelection

	createAgentRequests []cursor.CreateAgentRequest
	createRunAgentIDs   []string
	createRunRequests   []cursor.CreateRunRequest
	cancelCalls         [][2]string
	streamCalls         []cursorStreamCall

	createAgentBlock chan struct{}
	createAgentErr   error
	createRunErr     error

	streamStarted chan struct{}
	streamOnce    sync.Once
	streamRelease chan struct{}
	streamEvents  []cursor.StreamEvent
	streamErr     error
	afterEmit     func(cursor.StreamEvent)
	invokeReset   bool
	terminal      cursor.Run
	getRun        cursor.Run
	cancelHook    func()
}

func newFakeCursorRunner() *fakeCursorRunner {
	return &fakeCursorRunner{
		streamStarted: make(chan struct{}),
		streamEvents: []cursor.StreamEvent{
			{ID: "evt-status", Type: "status", Status: "RUNNING"},
			{ID: "evt-reasoning", Type: "thinking", Text: "reasoning"},
			{ID: "evt-text", Type: "assistant", Text: "final answer"},
		},
		terminal: cursor.Run{
			ID: "run-test-1", AgentID: "bc-test-1", Status: "FINISHED",
			Result: "final answer",
			Git: &cursor.GitState{Branches: []cursor.GitBranch{{
				RepoURL: "https://github.com/acme/repo",
				Branch:  "cursor/task",
				PRURL:   "https://github.com/acme/repo/pull/1",
			}}},
		},
		getRun: cursor.Run{ID: "run-recovery", AgentID: "bc-recovery", Status: "RUNNING"},
	}
}

func (f *fakeCursorRunner) Catalog(context.Context, bool) (*cursor.ModelCatalog, error) {
	return &cursor.ModelCatalog{}, nil
}

func (f *fakeCursorRunner) InvalidateCatalog() {}

func (f *fakeCursorRunner) ValidateModel(
	_ context.Context,
	selection *cursor.ModelSelection,
	_ cursorrun.SelectionPolicy,
) (*cursor.ModelSelection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	if selection == nil || strings.TrimSpace(selection.ID) == "" {
		return nil, errors.New("cursor model is required")
	}
	validated := &cursor.ModelSelection{
		ID:     selection.ID,
		Params: append([]cursor.ModelParameterSelection(nil), selection.Params...),
	}
	if selection.Params != nil && validated.Params == nil {
		validated.Params = []cursor.ModelParameterSelection{}
	}
	f.validated = validated
	return validated, nil
}

func (f *fakeCursorRunner) CreateAgent(
	ctx context.Context,
	request cursor.CreateAgentRequest,
) (*cursor.CreateAgentResponse, error) {
	f.mu.Lock()
	f.createAgentRequests = append(f.createAgentRequests, cloneCreateAgentRequest(request))
	index := len(f.createAgentRequests)
	block := f.createAgentBlock
	createErr := f.createAgentErr
	f.mu.Unlock()

	if block != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-block:
		}
	}
	if createErr != nil {
		return nil, createErr
	}
	agentID := "bc-test-" + string(rune('0'+index))
	runID := "run-test-" + string(rune('0'+index))
	return &cursor.CreateAgentResponse{
		Agent: cursor.Agent{ID: agentID, Status: "RUNNING", LatestRunID: runID},
		Run:   cursor.Run{ID: runID, AgentID: agentID, Status: "CREATING"},
	}, nil
}

func (f *fakeCursorRunner) CreateRun(
	_ context.Context,
	agentID string,
	request cursor.CreateRunRequest,
) (*cursor.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createRunAgentIDs = append(f.createRunAgentIDs, agentID)
	f.createRunRequests = append(f.createRunRequests, cloneCreateRunRequest(request))
	if f.createRunErr != nil {
		return nil, f.createRunErr
	}
	index := len(f.createAgentRequests) + len(f.createRunRequests)
	return &cursor.Run{
		ID: "run-test-" + string(rune('0'+index)), AgentID: agentID, Status: "CREATING",
	}, nil
}

func (f *fakeCursorRunner) GetAgent(_ context.Context, agentID string) (*cursor.Agent, error) {
	return &cursor.Agent{ID: agentID, Status: "RUNNING"}, nil
}

func (f *fakeCursorRunner) GetRun(
	_ context.Context,
	agentID string,
	runID string,
) (*cursor.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	run := f.getRun
	run.AgentID = agentID
	run.ID = runID
	return cloneRun(run), nil
}

func (f *fakeCursorRunner) CancelRun(
	_ context.Context,
	agentID string,
	runID string,
) error {
	f.mu.Lock()
	f.cancelCalls = append(f.cancelCalls, [2]string{agentID, runID})
	hook := f.cancelHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (f *fakeCursorRunner) StreamRun(
	ctx context.Context,
	agentID string,
	runID string,
	lastEventID string,
	onReset func() error,
	emit func(cursor.StreamEvent) error,
) (*cursor.Run, error) {
	f.mu.Lock()
	f.streamCalls = append(f.streamCalls, cursorStreamCall{
		AgentID: agentID, RunID: runID, LastEventID: lastEventID,
	})
	invokeReset := f.invokeReset
	events := append([]cursor.StreamEvent(nil), f.streamEvents...)
	release := f.streamRelease
	terminal := f.terminal
	streamErr := f.streamErr
	afterEmit := f.afterEmit
	f.streamOnce.Do(func() { close(f.streamStarted) })
	f.mu.Unlock()

	if invokeReset && onReset != nil {
		if err := onReset(); err != nil {
			return nil, err
		}
	}
	for _, event := range events {
		if err := emit(event); err != nil {
			return nil, err
		}
		if afterEmit != nil {
			afterEmit(event)
		}
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
	}
	if streamErr != nil {
		return nil, streamErr
	}
	terminal.AgentID = agentID
	terminal.ID = runID
	return cloneRun(terminal), nil
}

func (f *fakeCursorRunner) Progress(event cursor.StreamEvent) cursorrun.Progress {
	return cursorrun.Progress{
		Message: "Cursor " + event.Type + " " + event.Status,
		Chunk:   event.Text,
	}
}

func (f *fakeCursorRunner) holdStream() {
	f.mu.Lock()
	f.streamRelease = make(chan struct{})
	f.streamStarted = make(chan struct{})
	f.streamOnce = sync.Once{}
	f.mu.Unlock()
}

func (f *fakeCursorRunner) releaseStream() {
	f.mu.Lock()
	release := f.streamRelease
	f.streamRelease = nil
	f.mu.Unlock()
	if release != nil {
		close(release)
	}
}

func (f *fakeCursorRunner) CreateAgentCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.createAgentRequests)
}

func (f *fakeCursorRunner) CreateRunCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.createRunRequests)
}

func (f *fakeCursorRunner) CancelCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.cancelCalls)
}

func (f *fakeCursorRunner) StreamCalls() []cursorStreamCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cursorStreamCall(nil), f.streamCalls...)
}

func cloneCreateAgentRequest(request cursor.CreateAgentRequest) cursor.CreateAgentRequest {
	cloned := request
	cloned.Prompt.Images = append([]cursor.PromptImage(nil), request.Prompt.Images...)
	cloned.Repos = append([]cursor.Repository(nil), request.Repos...)
	if request.Model != nil {
		cloned.Model = &cursor.ModelSelection{
			ID: request.Model.ID,
			Params: append(
				[]cursor.ModelParameterSelection(nil),
				request.Model.Params...,
			),
		}
	}
	return cloned
}

func cloneCreateRunRequest(request cursor.CreateRunRequest) cursor.CreateRunRequest {
	cloned := request
	cloned.Prompt.Images = append([]cursor.PromptImage(nil), request.Prompt.Images...)
	return cloned
}

func cloneRun(run cursor.Run) *cursor.Run {
	cloned := run
	if run.Git != nil {
		cloned.Git = &cursor.GitState{
			Branches: append([]cursor.GitBranch(nil), run.Git.Branches...),
		}
	}
	return &cloned
}

type cursorDirectFixture struct {
	server *Server
	runner *fakeCursorRunner
	db     store.Store
	http   *httptest.Server
	cfg    *config.Config
}

func newCursorDirectTestServer(t *testing.T) *cursorDirectFixture {
	t.Helper()
	return newCursorDirectTestServerWithConfig(t, nil)
}

func newCursorDirectTestServerWithConfig(
	t *testing.T,
	mutate func(*config.Config),
) *cursorDirectFixture {
	t.Helper()
	t.Setenv("ANTARES_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Server.AuthToken = "test-token"
	cfg.Tools.ApprovalMode = "auto"
	if mutate != nil {
		mutate(cfg)
	}
	db, err := store.Open(context.Background(), "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a := agent.New(cfg, db, tools.NewRegistry(), nil, nil)
	runner := newFakeCursorRunner()
	s := New(Options{Config: cfg, Agent: a, Store: db, Cursor: runner})
	httpServer := httptest.NewServer(s.Handler())
	t.Cleanup(httpServer.Close)
	return &cursorDirectFixture{
		server: s, runner: runner, db: db, http: httpServer, cfg: cfg,
	}
}

type sseTestStream struct {
	t        *testing.T
	response *http.Response
	scanner  *bufio.Scanner
}

func postCursorChat(
	t *testing.T,
	fixture *cursorDirectFixture,
	request cursorChatRequest,
) *sseTestStream {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest, err := http.NewRequest(
		http.MethodPost,
		fixture.http.URL+"/api/chat/cursor",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Authorization", "Bearer test-token")
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := fixture.http.Client().Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("cursor chat status=%d body=%s", response.StatusCode, raw)
	}
	return &sseTestStream{
		t: t, response: response, scanner: bufio.NewScanner(response.Body),
	}
}

func postCursorChatStatus(
	t *testing.T,
	fixture *cursorDirectFixture,
	request cursorChatRequest,
) (int, string) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest, err := http.NewRequest(
		http.MethodPost,
		fixture.http.URL+"/api/chat/cursor",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Authorization", "Bearer test-token")
	response, err := fixture.http.Client().Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(raw)
}

func (s *sseTestStream) Next(t *testing.T) agent.Event {
	t.Helper()
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event agent.Event
		if err := json.Unmarshal(
			[]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))),
			&event,
		); err != nil {
			t.Fatalf("decode SSE %q: %v", line, err)
		}
		return event
	}
	if err := s.scanner.Err(); err != nil {
		t.Fatalf("read SSE: %v", err)
	}
	t.Fatal("SSE stream ended before the next event")
	return agent.Event{}
}

func (s *sseTestStream) NextType(t *testing.T, eventType agent.EventType) agent.Event {
	t.Helper()
	for {
		event := s.Next(t)
		if event.Type == eventType {
			return event
		}
	}
}

func (s *sseTestStream) Close() {
	_ = s.response.Body.Close()
}

func resolveApproval(
	t *testing.T,
	fixture *cursorDirectFixture,
	approvalID string,
	allow bool,
) {
	t.Helper()
	raw := `{"allow":false}`
	if allow {
		raw = `{"allow":true}`
	}
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.http.URL+"/api/approvals/"+approvalID,
		strings.NewReader(raw),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("resolve approval status=%d body=%s", response.StatusCode, body)
	}
}

func approvedCursorTurn(
	t *testing.T,
	fixture *cursorDirectFixture,
	request cursorChatRequest,
) string {
	t.Helper()
	stream := postCursorChat(t, fixture, request)
	defer stream.Close()
	session := stream.NextType(t, agent.EventSession)
	approvalEvent := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, approvalEvent.ID, true)
	stream.NextType(t, agent.EventDone)
	return session.ID
}

func defaultCursorChatRequest() cursorChatRequest {
	return cursorChatRequest{
		Message: "fix it",
		Model: cursor.ModelSelection{
			ID: "gpt-5.6-sol",
			Params: []cursor.ModelParameterSelection{{
				ID: "reasoning", Value: "max",
			}},
		},
		Mode: "agent",
	}
}

func waitCursorState(
	t *testing.T,
	db store.Store,
	sessionID string,
	predicate func(*store.CursorSessionState) bool,
) *store.CursorSessionState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, err := db.GetCursorSessionState(context.Background(), sessionID)
		if err == nil && predicate(state) {
			return state
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, err := db.GetCursorSessionState(context.Background(), sessionID)
	t.Fatalf("cursor state did not reach condition: state=%+v err=%v", state, err)
	return nil
}

func TestCursorChatAuthenticatesBeforeReadingLargeBody(t *testing.T) {
	cfg := config.Default()
	s := New(Options{Config: cfg})
	body := &readTrackingBody{}
	request := httptest.NewRequest(http.MethodPost, "/api/chat/cursor", body)
	response := httptest.NewRecorder()

	s.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusPreconditionRequired {
		t.Fatalf("status=%d body=%s, want 428", response.Code, response.Body.String())
	}
	if body.read {
		t.Fatal("unauthenticated Cursor route read the request body")
	}
}

type readTrackingBody struct {
	read bool
}

func (b *readTrackingBody) Read([]byte) (int, error) {
	b.read = true
	return 0, io.EOF
}

func (b *readTrackingBody) Close() error { return nil }

func TestCursorChatSendsNoUpstreamRequestBeforeApproval(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer stream.Close()

	sessionEvent := stream.Next(t)
	if sessionEvent.Type != agent.EventSession || sessionEvent.ID == "" {
		t.Fatalf("first event = %+v, want session", sessionEvent)
	}
	approvalEvent := stream.Next(t)
	if approvalEvent.Type != agent.EventApproval {
		t.Fatalf("second event = %+v, want approval", approvalEvent)
	}
	if got := fixture.runner.CreateAgentCalls(); got != 0 {
		t.Fatalf("CreateAgent calls before approval = %d", got)
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), sessionEvent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.OperationState != store.CursorOperationAwaitingApproval {
		t.Fatalf("operation state=%q, want awaiting_approval", state.OperationState)
	}
	messages, err := fixture.db.ListMessages(context.Background(), sessionEvent.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Role != store.RoleUser ||
		messages[0].Content != "fix it" {
		t.Fatalf("messages before approval = %+v", messages)
	}

	resolveApproval(t, fixture, approvalEvent.ID, true)
	stream.NextType(t, agent.EventToolProgress)
	if got := fixture.runner.CreateAgentCalls(); got != 1 {
		t.Fatalf("CreateAgent calls after approval = %d", got)
	}
	stream.NextType(t, agent.EventDone)

	session, err := fixture.db.GetSession(context.Background(), sessionEvent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if session.Provider != fixture.cfg.Model.Provider {
		t.Fatalf("session provider=%q, want active chat provider %q",
			session.Provider, fixture.cfg.Model.Provider)
	}
}

func TestCursorChatApprovalIsBoundedRedactedAndImmutable(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	secret := "crsr_super_secret_value"
	prompt := "use " + secret + " " + strings.Repeat("界", 300)
	request := defaultCursorChatRequest()
	request.Message = prompt
	request.Images = []string{
		cursorImageDataURL("image/png", cursorImageSignature("image/png")),
	}
	stream := postCursorChat(t, fixture, request)
	defer stream.Close()
	sessionEvent := stream.NextType(t, agent.EventSession)
	if strings.Contains(sessionEvent.Title, secret) {
		t.Fatalf("session event leaked prompt credential in title: %q", sessionEvent.Title)
	}
	approvalEvent := stream.NextType(t, agent.EventApproval)

	if strings.Contains(approvalEvent.Arguments, secret) ||
		strings.Contains(approvalEvent.Content, secret) ||
		strings.Contains(approvalEvent.Arguments, request.Images[0]) {
		t.Fatalf("approval leaked prompt/image credential: %+v", approvalEvent)
	}
	var projection struct {
		Operation     string                `json:"operation"`
		Kind          string                `json:"kind"`
		Model         cursor.ModelSelection `json:"model"`
		PromptPreview string                `json:"prompt_preview"`
		ImageCount    int                   `json:"image_count"`
	}
	if err := json.Unmarshal([]byte(approvalEvent.Arguments), &projection); err != nil {
		t.Fatalf("approval projection: %v (%s)", err, approvalEvent.Arguments)
	}
	if projection.Operation != "start" || projection.Kind != "new_agent" ||
		projection.ImageCount != 1 || projection.Model.ID != request.Model.ID {
		t.Fatalf("approval projection = %+v", projection)
	}
	if utf8.RuneCountInString(projection.PromptPreview) > 240 {
		t.Fatalf("prompt preview has %d runes, want <=240",
			utf8.RuneCountInString(projection.PromptPreview))
	}

	fixture.runner.mu.Lock()
	fixture.runner.validated.ID = "mutated-model"
	fixture.runner.validated.Params[0].Value = "mutated"
	fixture.runner.mu.Unlock()
	resolveApproval(t, fixture, approvalEvent.ID, true)
	stream.NextType(t, agent.EventDone)

	fixture.runner.mu.Lock()
	created := fixture.runner.createAgentRequests[0]
	fixture.runner.mu.Unlock()
	if created.Model == nil || created.Model.ID != request.Model.ID ||
		created.Model.Params[0].Value != "max" {
		t.Fatalf("approved immutable model became %+v", created.Model)
	}
	if created.Prompt.Text != prompt || len(created.Prompt.Images) != 1 {
		t.Fatalf("approved immutable prompt/images were not executed exactly")
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), projectionSessionID(
		t, fixture.db, prompt,
	))
	if err != nil {
		t.Fatal(err)
	}
	rawState, _ := json.Marshal(state)
	if strings.Contains(string(rawState), secret) ||
		strings.Contains(string(rawState), request.Images[0]) {
		t.Fatalf("cursor state leaked prompt/image data: %s", rawState)
	}
}

func projectionSessionID(t *testing.T, db store.Store, prompt string) string {
	t.Helper()
	sessions, _, err := db.ListSessions(context.Background(), store.SessionFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		messages, err := db.ListMessages(context.Background(), session.ID, 0, 0)
		if err == nil && len(messages) > 0 && messages[0].Content == prompt {
			return session.ID
		}
	}
	t.Fatal("session for prompt not found")
	return ""
}

func TestCursorChatDenyModeRefusesWithoutPendingApprovalOrUpstream(t *testing.T) {
	fixture := newCursorDirectTestServerWithConfig(t, func(cfg *config.Config) {
		cfg.Tools.ApprovalMode = "deny"
	})
	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer stream.Close()
	stream.NextType(t, agent.EventSession)
	event := stream.Next(t)
	if event.Type != agent.EventError {
		t.Fatalf("event after session=%+v, want immediate error", event)
	}
	stream.NextType(t, agent.EventDone)
	if fixture.runner.CreateAgentCalls() != 0 ||
		len(fixture.server.agent.PendingApprovals()) != 0 {
		t.Fatal("deny mode created approval or upstream request")
	}
}

func TestCursorChatRejectsInvalidPlanBeforeMutation(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	fixture.runner.validateErr = errors.New("selection is stale")
	status, body := postCursorChatStatus(t, fixture, defaultCursorChatRequest())
	if status != http.StatusBadRequest || !strings.Contains(body, "stale") {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if fixture.runner.CreateAgentCalls() != 0 {
		t.Fatal("invalid plan reached CreateAgent")
	}
	sessions, total, err := fixture.db.ListSessions(
		context.Background(), store.SessionFilter{Limit: 10},
	)
	if err != nil || total != 0 || len(sessions) != 0 {
		t.Fatalf("invalid plan persisted sessions=%+v total=%d err=%v", sessions, total, err)
	}
}

func TestCursorChatIdentityControlsAgentReuse(t *testing.T) {
	repo := "https://github.com/acme/repo"
	ref := "main"
	base := defaultCursorChatRequest()
	base.RepositoryURL = &repo
	base.StartingRef = &ref

	tests := []struct {
		name        string
		change      func(*cursorChatRequest)
		wantCreates int
		wantRuns    int
	}{
		{
			name: "same identity mode-only change reuses",
			change: func(request *cursorChatRequest) {
				request.Mode = "plan"
			},
			wantCreates: 1, wantRuns: 1,
		},
		{
			name: "model change creates agent",
			change: func(request *cursorChatRequest) {
				request.Model.ID = "claude-4.6-opus"
			},
			wantCreates: 2,
		},
		{
			name: "variant change creates agent",
			change: func(request *cursorChatRequest) {
				request.Model.Params[0].Value = "high"
			},
			wantCreates: 2,
		},
		{
			name: "repository change creates agent",
			change: func(request *cursorChatRequest) {
				other := "https://github.com/acme/other"
				request.RepositoryURL = &other
			},
			wantCreates: 2,
		},
		{
			name: "ref change creates agent",
			change: func(request *cursorChatRequest) {
				other := "develop"
				request.StartingRef = &other
			},
			wantCreates: 2,
		},
		{
			name: "auto PR change creates agent",
			change: func(request *cursorChatRequest) {
				request.AutoCreatePR = true
			},
			wantCreates: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCursorDirectTestServer(t)
			first := base
			first.Model.Params = append(
				[]cursor.ModelParameterSelection(nil), base.Model.Params...,
			)
			sessionID := approvedCursorTurn(t, fixture, first)

			second := first
			second.SessionID = sessionID
			second.Message = "continue"
			second.Model.Params = append(
				[]cursor.ModelParameterSelection(nil), first.Model.Params...,
			)
			test.change(&second)
			approvedCursorTurn(t, fixture, second)

			if got := fixture.runner.CreateAgentCalls(); got != test.wantCreates {
				t.Fatalf("CreateAgent calls=%d, want %d", got, test.wantCreates)
			}
			if got := fixture.runner.CreateRunCalls(); got != test.wantRuns {
				t.Fatalf("CreateRun calls=%d, want %d", got, test.wantRuns)
			}
			if test.wantRuns == 1 {
				fixture.runner.mu.Lock()
				mode := fixture.runner.createRunRequests[0].Mode
				agentID := fixture.runner.createRunAgentIDs[0]
				fixture.runner.mu.Unlock()
				if mode != "plan" || agentID != "bc-test-1" {
					t.Fatalf("follow-up mode=%q agent=%q", mode, agentID)
				}
			}
		})
	}
}

func TestCursorChatExplicitNoRepositoryDiffersFromAutoDiscovery(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	first := defaultCursorChatRequest()
	sessionID := approvedCursorTurn(t, fixture, first)

	noRepository := ""
	second := defaultCursorChatRequest()
	second.SessionID = sessionID
	second.Message = "continue without a repository"
	second.RepositoryURL = &noRepository
	approvedCursorTurn(t, fixture, second)

	if fixture.runner.CreateAgentCalls() != 2 || fixture.runner.CreateRunCalls() != 0 {
		t.Fatalf("create agent/run calls=%d/%d, want 2/0",
			fixture.runner.CreateAgentCalls(), fixture.runner.CreateRunCalls())
	}
}

func TestCursorChatReservationRejectsConcurrentTurnBeforeApproval(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	first := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer first.Close()
	session := first.NextType(t, agent.EventSession)
	approvalEvent := first.NextType(t, agent.EventApproval)

	secondRequest := defaultCursorChatRequest()
	secondRequest.SessionID = session.ID
	secondRequest.Message = "competing turn"
	status, body := postCursorChatStatus(t, fixture, secondRequest)
	if status != http.StatusConflict || !strings.Contains(body, "already") {
		t.Fatalf("status=%d body=%s, want 409", status, body)
	}
	resolveApproval(t, fixture, approvalEvent.ID, false)
	first.NextType(t, agent.EventDone)
	if fixture.runner.CreateAgentCalls() != 0 {
		t.Fatal("concurrent request or refused request reached upstream")
	}
}

func TestCursorChatDisconnectDetachesAndAttachReplaysMemory(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	fixture.runner.holdStream()
	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	session := stream.NextType(t, agent.EventSession)
	approvalEvent := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, approvalEvent.ID, true)
	stream.NextType(t, agent.EventText)
	stream.Close()

	select {
	case <-fixture.runner.streamStarted:
	case <-time.After(time.Second):
		t.Fatal("Cursor stream did not start")
	}
	if fixture.runner.CancelCalls() != 0 {
		t.Fatal("HTTP disconnect cancelled the remote run")
	}

	attach := getCursorAttach(t, fixture, session.ID)
	defer attach.Close()
	replayed := attach.NextType(t, agent.EventText)
	if replayed.Delta != "final answer" {
		t.Fatalf("replayed text=%q", replayed.Delta)
	}
	fixture.runner.releaseStream()
	attach.NextType(t, agent.EventDone)
	if fixture.runner.CancelCalls() != 0 {
		t.Fatal("reattach path cancelled the remote run")
	}
}

func getCursorAttach(
	t *testing.T,
	fixture *cursorDirectFixture,
	sessionID string,
) *sseTestStream {
	t.Helper()
	return getCursorAttachAt(t, fixture, sessionID, 0)
}

func getCursorAttachAt(
	t *testing.T,
	fixture *cursorDirectFixture,
	sessionID string,
	cursor int,
) *sseTestStream {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf(
			"%s/api/chat/attach?session_id=%s&cursor=%d",
			fixture.http.URL, sessionID, cursor,
		),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("attach status=%d body=%s", response.StatusCode, body)
	}
	return &sseTestStream{
		t: t, response: response, scanner: bufio.NewScanner(response.Body),
	}
}

func TestCursorChatAttachRecoversPersistedRunResetAndFinalizesOnce(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	fixture.runner.invokeReset = true
	fixture.runner.streamEvents = []cursor.StreamEvent{
		{ID: "evt-new-reasoning", Type: "thinking", Text: "new reasoning"},
		{ID: "evt-new-text", Type: "assistant", Text: "new answer"},
	}
	fixture.runner.terminal = cursor.Run{
		Status: "FINISHED", Result: "new answer",
		Git: &cursor.GitState{Branches: []cursor.GitBranch{{
			RepoURL: "https://github.com/acme/repo", Branch: "cursor/recovered",
		}}},
	}

	var streams [2]*sseTestStream
	var wg sync.WaitGroup
	for i := range streams {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			streams[index] = getCursorAttach(t, fixture, sessionID)
		}(i)
	}
	wg.Wait()
	for _, stream := range streams {
		stream.NextType(t, agent.EventDone)
		stream.Close()
	}

	calls := fixture.runner.StreamCalls()
	if len(calls) != 1 || calls[0].LastEventID != "evt-old" {
		t.Fatalf("recovery stream calls=%+v, want one resumed at evt-old", calls)
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.OperationState != store.CursorOperationCommitted ||
		state.PartialText != "new answer" ||
		state.PartialReasoning != "new reasoning" ||
		state.LastEventID != "evt-new-text" ||
		strings.Contains(state.PartialText, "old") ||
		strings.Contains(state.PartialReasoning, "old") {
		t.Fatalf("recovered state = %+v", state)
	}
	messages, err := fixture.db.ListMessages(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Content != "new answer" ||
		messages[1].Reasoning != "new reasoning" {
		t.Fatalf("recovered messages = %+v", messages)
	}

	again := getCursorAttach(t, fixture, sessionID)
	again.NextType(t, agent.EventDone)
	again.Close()
	if len(fixture.runner.StreamCalls()) != 1 {
		t.Fatal("committed attach started a duplicate recovery watcher")
	}
	messages, _ = fixture.db.ListMessages(context.Background(), sessionID, 0, 0)
	if len(messages) != 2 {
		t.Fatalf("terminal finalization appended %d messages, want 2 total", len(messages))
	}
}

func TestCursorChatPersistsEventsBeforePublishingAndUsesCanonicalFinalText(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	fixture.runner.streamEvents = []cursor.StreamEvent{
		{ID: "evt-status", Type: "status", Status: "RUNNING"},
		{ID: "evt-thinking", Type: "thinking", Text: "deep thought"},
		{ID: "evt-partial", Type: "assistant", Text: "partial"},
		{
			ID: "evt-tool", Type: "tool_call", CallID: "call-1",
			ToolName: "read_file", Status: "completed",
		},
		{
			ID: "evt-result", Type: "result", Status: "FINISHED",
			Text: "canonical whole answer",
		},
	}
	fixture.runner.terminal = cursor.Run{
		Status: "FINISHED", Result: "canonical whole answer",
		Git: &cursor.GitState{Branches: []cursor.GitBranch{{
			RepoURL: "https://github.com/acme/repo",
			Branch:  "cursor/canonical",
			PRURL:   "https://github.com/acme/repo/pull/9",
		}}},
	}

	var sessionID atomic.Value
	persistenceErrors := make(chan string, len(fixture.runner.streamEvents))
	fixture.runner.afterEmit = func(event cursor.StreamEvent) {
		id, _ := sessionID.Load().(string)
		state, err := fixture.db.GetCursorSessionState(context.Background(), id)
		if err != nil {
			persistenceErrors <- err.Error()
			return
		}
		if state.LastEventID != event.ID {
			persistenceErrors <- fmt.Sprintf(
				"event %s published with persisted LastEventID %s",
				event.ID, state.LastEventID,
			)
			return
		}
		switch event.Type {
		case "thinking":
			if state.PartialReasoning != "deep thought" {
				persistenceErrors <- "reasoning was not persisted before publish"
			}
		case "assistant":
			if state.PartialText != "partial" {
				persistenceErrors <- "text was not persisted before publish"
			}
		case "result":
			if state.PartialText != "canonical whole answer" {
				persistenceErrors <- "whole result was appended instead of reconciled"
			}
		}
	}

	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer stream.Close()
	session := stream.NextType(t, agent.EventSession)
	sessionID.Store(session.ID)
	approvalEvent := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, approvalEvent.ID, true)

	rendered := ""
	sawTool := false
	for {
		event := stream.Next(t)
		switch event.Type {
		case agent.EventReset:
			rendered = ""
		case agent.EventText:
			rendered += event.Delta
		case agent.EventToolProgress:
			if event.Name == "read_file" {
				sawTool = true
			}
		case agent.EventDone:
			goto finished
		}
	}

finished:
	close(persistenceErrors)
	for persistenceError := range persistenceErrors {
		if persistenceError != "" {
			t.Error(persistenceError)
		}
	}
	if rendered != "canonical whole answer" {
		t.Fatalf("rendered text=%q, want canonical whole answer", rendered)
	}
	if !sawTool {
		t.Fatal("Cursor tool event was not published live")
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.OperationState != store.CursorOperationCommitted ||
		state.PartialText != "canonical whole answer" ||
		state.PartialReasoning != "deep thought" ||
		!strings.Contains(state.GitState, "cursor/canonical") {
		t.Fatalf("final state=%+v", state)
	}
	messages, err := fixture.db.ListMessages(context.Background(), session.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Content != "canonical whole answer" ||
		messages[1].Reasoning != "deep thought" {
		t.Fatalf("final messages=%+v", messages)
	}
	for _, message := range messages {
		if message.Role == store.RoleTool {
			t.Fatal("live-only Cursor tool progress was persisted as chat history")
		}
	}
}

func TestCursorChatAttachFinalizesTerminalSnapshotWithoutStreaming(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	fixture.runner.getRun = cursor.Run{
		Status: "FINISHED", Result: "terminal snapshot",
		Git: &cursor.GitState{Branches: []cursor.GitBranch{{
			RepoURL: "https://github.com/acme/repo", Branch: "cursor/snapshot",
		}}},
	}

	stream := getCursorAttach(t, fixture, sessionID)
	stream.NextType(t, agent.EventDone)
	stream.Close()

	if calls := fixture.runner.StreamCalls(); len(calls) != 0 {
		t.Fatalf("terminal recovery opened %d stream(s), want 0", len(calls))
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.OperationState != store.CursorOperationCommitted ||
		state.PartialText != "terminal snapshot" {
		t.Fatalf("terminal recovery state=%+v", state)
	}
	messages, _ := fixture.db.ListMessages(context.Background(), sessionID, 0, 0)
	if len(messages) != 2 || messages[1].Content != "terminal snapshot" {
		t.Fatalf("terminal recovery messages=%+v", messages)
	}
}

func TestCursorChatAttachReplaysPersistedPartialsBeforeResuming(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	fixture.runner.streamEvents = []cursor.StreamEvent{
		{ID: "evt-next", Type: "assistant", Text: " plus"},
	}
	fixture.runner.terminal = cursor.Run{
		Status: "FINISHED", Result: "old text plus",
	}

	stream := getCursorAttach(t, fixture, sessionID)
	defer stream.Close()
	var text, reasoning string
	for {
		event := stream.Next(t)
		switch event.Type {
		case agent.EventText:
			text += event.Delta
		case agent.EventReasoning:
			reasoning += event.Delta
		case agent.EventDone:
			if text != "old text plus" || reasoning != "old reasoning" {
				t.Fatalf("recovered live partials text=%q reasoning=%q", text, reasoning)
			}
			return
		}
	}
}

func TestCursorChatRecoveryIgnoresCursorFromLostInMemoryRun(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	fixture.runner.streamEvents = nil
	fixture.runner.terminal = cursor.Run{
		Status: "FINISHED", Result: "old text",
	}

	stream := getCursorAttachAt(t, fixture, sessionID, 999)
	defer stream.Close()
	if event := stream.NextType(t, agent.EventText); event.Delta != "old text" {
		t.Fatalf("recovered text=%q, want persisted partial", event.Delta)
	}
	stream.NextType(t, agent.EventDone)
}

func TestCursorChatBoundsAndRedactsStreamErrors(t *testing.T) {
	const secret = "crsr_server_error_secret"
	fixture := newCursorDirectTestServerWithConfig(t, func(cfg *config.Config) {
		provider := cfg.Providers["cursor"]
		provider.Enabled = true
		provider.APIKey = secret
		cfg.Providers["cursor"] = provider
	})
	fixture.runner.streamEvents = nil
	fixture.runner.streamErr = errors.New(secret + strings.Repeat("界", 5000))

	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer stream.Close()
	stream.NextType(t, agent.EventSession)
	approvalEvent := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, approvalEvent.ID, true)
	errorEvent := stream.NextType(t, agent.EventError)
	stream.NextType(t, agent.EventDone)

	if strings.Contains(errorEvent.Err, secret) {
		t.Fatalf("stream error leaked credential: %q", errorEvent.Err)
	}
	if utf8.RuneCountInString(errorEvent.Err) > maxCursorServerErrorRunes {
		t.Fatalf("stream error has %d runes, want <=%d",
			utf8.RuneCountInString(errorEvent.Err), maxCursorServerErrorRunes)
	}
}

func TestCursorChatRedactsUpstreamContentBeforePersistingOrPublishing(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	secret := "supersecretvalue"
	fixture.runner.streamEvents = []cursor.StreamEvent{
		{ID: "evt-status", Type: "status", Status: "token=" + secret},
		{ID: "evt-reasoning", Type: "thinking", Text: "secret=" + secret},
		{ID: "evt-text", Type: "assistant", Text: "token=" + secret},
	}
	fixture.runner.terminal = cursor.Run{
		ID: "run-test-1", AgentID: "bc-test-1", Status: "FINISHED",
		Result: "answer token=" + secret,
		Git: &cursor.GitState{Branches: []cursor.GitBranch{{
			RepoURL: "https://github.com/acme/repo?token=" + secret,
			Branch:  "token=" + secret,
			PRURL:   "https://github.com/acme/repo/pull/1?token=" + secret,
		}}},
	}

	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer stream.Close()
	session := stream.NextType(t, agent.EventSession)
	approvalEvent := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, approvalEvent.ID, true)
	var published strings.Builder
	for {
		event := stream.Next(t)
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		published.Write(raw)
		if event.Type == agent.EventDone {
			break
		}
	}
	if strings.Contains(published.String(), secret) {
		t.Fatalf("SSE leaked upstream credential: %s", published.String())
	}

	state := waitCursorState(t, fixture.db, session.ID, func(state *store.CursorSessionState) bool {
		return state.OperationState == store.CursorOperationCommitted
	})
	stateJSON, _ := json.Marshal(state)
	messages, err := fixture.db.ListMessages(context.Background(), session.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	messageJSON, _ := json.Marshal(messages)
	if strings.Contains(string(stateJSON), secret) ||
		strings.Contains(string(messageJSON), secret) {
		t.Fatalf("persisted Cursor data leaked upstream credential: state=%s messages=%s",
			stateJSON, messageJSON)
	}
}

func TestCursorChatTerminalStateBlocksNextTurnUntilFinalization(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	sessionID := seedRecoverableCursorSession(t, fixture)
	_, err := fixture.server.mutateCursorState(context.Background(), sessionID,
		func(state *store.CursorSessionState) error {
			state.OperationState = store.CursorOperationTerminal
			state.RemoteStatus = "FINISHED"
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}

	request := defaultCursorChatRequest()
	request.SessionID = sessionID
	status, _ := postCursorChatStatus(t, fixture, request)
	if status != http.StatusConflict {
		t.Fatalf("status=%d, want 409 while terminal assistant commit is pending", status)
	}
	if fixture.runner.CreateAgentCalls() != 0 || fixture.runner.CreateRunCalls() != 0 {
		t.Fatal("unfinished terminal finalization issued a new Cursor mutation")
	}
}

func seedRecoverableCursorSession(
	t *testing.T,
	fixture *cursorDirectFixture,
) string {
	t.Helper()
	sessionID := "ses-recovery"
	if err := fixture.db.CreateSession(context.Background(), &store.Session{
		ID: sessionID, Title: "recover", Platform: "web",
		Model: fixture.cfg.Model.Default, Provider: fixture.cfg.Model.Provider,
		Meta: store.Meta{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.db.AppendMessage(context.Background(), &store.Message{
		ID: "msg-recovery-user", SessionID: sessionID,
		Role: store.RoleUser, Content: "recover me",
	}); err != nil {
		t.Fatal(err)
	}
	state := &store.CursorSessionState{
		SessionID: sessionID, TargetActive: true, ReuseValid: true,
		ModelID:     "gpt-5.6-sol",
		ModelParams: `[{"id":"reasoning","value":"max"}]`,
		AgentID:     "bc-recovery", RunID: "run-recovery",
		RemoteStatus: "RUNNING", LastEventID: "evt-old",
		PartialText: "old text", PartialReasoning: "old reasoning",
		OperationState:     store.CursorOperationRunInFlight,
		UserMessageID:      "msg-recovery-user",
		AssistantMessageID: "msg-recovery-assistant",
	}
	if err := fixture.db.PutCursorSessionState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	return sessionID
}

func TestCursorChatInterruptStopsWatcherWithoutRemoteCancel(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	fixture.runner.holdStream()
	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer stream.Close()
	session := stream.NextType(t, agent.EventSession)
	approvalEvent := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, approvalEvent.ID, true)
	waitCursorState(t, fixture.db, session.ID, func(state *store.CursorSessionState) bool {
		return state.OperationState == store.CursorOperationRunInFlight && state.RunID != ""
	})

	status := postInterrupt(t, fixture, session.ID)
	if status != http.StatusOK {
		t.Fatalf("interrupt status=%d", status)
	}
	stream.NextType(t, agent.EventDone)
	state := waitCursorState(t, fixture.db, session.ID, func(state *store.CursorSessionState) bool {
		return state.OperationState == store.CursorOperationRunInFlight
	})
	if state.RunID == "" || fixture.runner.CancelCalls() != 0 {
		t.Fatalf("interrupt state=%+v cancel calls=%d", state, fixture.runner.CancelCalls())
	}
}

func postInterrupt(t *testing.T, fixture *cursorDirectFixture, sessionID string) int {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.http.URL+"/api/chat/interrupt",
		strings.NewReader(`{"session_id":"`+sessionID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func TestCursorChatInterruptedCreateBecomesAmbiguousAndIsNeverRetried(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	fixture.runner.createAgentBlock = make(chan struct{})
	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer stream.Close()
	session := stream.NextType(t, agent.EventSession)
	approvalEvent := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, approvalEvent.ID, true)
	waitCursorState(t, fixture.db, session.ID, func(state *store.CursorSessionState) bool {
		return state.OperationState == store.CursorOperationCreateInFlight
	})

	postInterrupt(t, fixture, session.ID)
	stream.NextType(t, agent.EventDone)
	state := waitCursorState(t, fixture.db, session.ID, func(state *store.CursorSessionState) bool {
		return state.OperationState == store.CursorOperationAmbiguous
	})
	if state.AgentID != "" || state.RunID != "" {
		t.Fatalf("ambiguous state unexpectedly has IDs: %+v", state)
	}

	attach := getCursorAttach(t, fixture, session.ID)
	attach.NextType(t, agent.EventDone)
	attach.Close()
	if fixture.runner.CreateAgentCalls() != 1 || len(fixture.runner.StreamCalls()) != 0 {
		t.Fatalf("ambiguous recovery retried create/stream: creates=%d streams=%d",
			fixture.runner.CreateAgentCalls(), len(fixture.runner.StreamCalls()))
	}
}

func TestCursorChatApprovedCancelCallsUpstreamExactlyOnce(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	fixture.runner.holdStream()
	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer stream.Close()
	session := stream.NextType(t, agent.EventSession)
	startApproval := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, startApproval.ID, true)
	waitCursorState(t, fixture.db, session.ID, func(state *store.CursorSessionState) bool {
		return state.OperationState == store.CursorOperationRunInFlight && state.RunID != ""
	})
	cancelMarker := make(chan string, 1)
	fixture.runner.mu.Lock()
	fixture.runner.cancelHook = func() {
		state, err := fixture.db.GetCursorSessionState(context.Background(), session.ID)
		if err != nil {
			cancelMarker <- "error: " + err.Error()
			return
		}
		cancelMarker <- state.RemoteStatus
	}
	fixture.runner.mu.Unlock()

	cancelResult := make(chan int, 1)
	go func() {
		cancelResult <- postCursorCancel(t, fixture, session.ID)
	}()
	cancelApproval := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, cancelApproval.ID, true)
	select {
	case status := <-cancelResult:
		if status != http.StatusOK {
			t.Fatalf("cancel status=%d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("approved cancel did not return")
	}
	select {
	case marker := <-cancelMarker:
		if marker != cursorCancelInFlight {
			t.Fatalf("durable status at CancelRun=%q, want %q", marker, cursorCancelInFlight)
		}
	case <-time.After(time.Second):
		t.Fatal("CancelRun did not observe its durable in-flight marker")
	}
	// Simulate a daemon restart losing the in-memory reservation. The durable
	// pre-call marker must still prevent a duplicate mutation.
	fixture.server.cursorCancelMu.Lock()
	fixture.server.cursorCancels = map[string]string{}
	fixture.server.cursorCancelMu.Unlock()
	if status := postCursorCancel(t, fixture, session.ID); status != http.StatusConflict {
		t.Fatalf("duplicate cancel status=%d, want 409", status)
	}
	if fixture.runner.CancelCalls() != 1 {
		t.Fatalf("CancelRun calls=%d, want 1", fixture.runner.CancelCalls())
	}
	if status := deleteCursorSession(t, fixture, session.ID); status != http.StatusOK {
		t.Fatalf("delete after approved cancellation status=%d, want 200", status)
	}
	stream.NextType(t, agent.EventDone)
}

func postCursorCancel(t *testing.T, fixture *cursorDirectFixture, sessionID string) int {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.http.URL+"/api/chat/cursor/cancel",
		strings.NewReader(`{"session_id":"`+sessionID+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func TestCursorChatDeleteRejectsActiveRemoteStateBeforeAnyMutation(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	fixture.runner.holdStream()
	stream := postCursorChat(t, fixture, defaultCursorChatRequest())
	defer stream.Close()
	session := stream.NextType(t, agent.EventSession)
	approvalEvent := stream.NextType(t, agent.EventApproval)
	resolveApproval(t, fixture, approvalEvent.ID, true)
	waitCursorState(t, fixture.db, session.ID, func(state *store.CursorSessionState) bool {
		return state.OperationState == store.CursorOperationRunInFlight
	})

	if status := deleteCursorSession(t, fixture, session.ID); status != http.StatusConflict {
		t.Fatalf("active single delete status=%d, want 409", status)
	}
	if status := deleteAllCursorSessions(t, fixture); status != http.StatusConflict {
		t.Fatalf("active bulk delete status=%d, want 409", status)
	}
	if _, err := fixture.db.GetSession(context.Background(), session.ID); err != nil {
		t.Fatalf("active delete mutated session: %v", err)
	}

	fixture.runner.releaseStream()
	stream.NextType(t, agent.EventDone)
	if status := deleteCursorSession(t, fixture, session.ID); status != http.StatusOK {
		t.Fatalf("terminal single delete status=%d, want 200", status)
	}
}

func deleteCursorSession(t *testing.T, fixture *cursorDirectFixture, sessionID string) int {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodDelete,
		fixture.http.URL+"/api/sessions/"+sessionID,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func deleteAllCursorSessions(t *testing.T, fixture *cursorDirectFixture) int {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.http.URL+"/api/sessions/delete-all",
		strings.NewReader(`{"category":"all"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func TestCursorChatEditInvalidatesReuseBeforeNextRun(t *testing.T) {
	fixture := newCursorDirectTestServer(t)
	request := defaultCursorChatRequest()
	sessionID := approvedCursorTurn(t, fixture, request)
	messages, err := fixture.db.ListMessages(context.Background(), sessionID, 0, 0)
	if err != nil || len(messages) < 1 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if status := editCursorMessage(t, fixture, sessionID, messages[0].ID); status != http.StatusOK {
		t.Fatalf("edit status=%d", status)
	}
	state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ReuseValid {
		t.Fatal("editing Cursor history left reuse valid")
	}

	request.SessionID = sessionID
	request.Message = "edited request"
	approvedCursorTurn(t, fixture, request)
	if fixture.runner.CreateAgentCalls() != 2 || fixture.runner.CreateRunCalls() != 0 {
		t.Fatalf("post-edit creates/runs=%d/%d, want 2/0",
			fixture.runner.CreateAgentCalls(), fixture.runner.CreateRunCalls())
	}
}

func TestOrdinaryChatInvalidatesCursorReuseBeforeRunning(t *testing.T) {
	fixture := newCursorDirectTestServerWithConfig(t, func(cfg *config.Config) {
		// Keep the ordinary turn hermetic: it will fail locally after the reuse
		// transition instead of constructing a provider request.
		cfg.Model.Default = ""
	})
	request := defaultCursorChatRequest()
	sessionID := approvedCursorTurn(t, fixture, request)

	body, _ := json.Marshal(map[string]string{
		"session_id": sessionID,
		"message":    "switch back to ordinary chat",
	})
	httpRequest, err := http.NewRequest(
		http.MethodPost,
		fixture.http.URL+"/api/chat",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Authorization", "Bearer test-token")
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := fixture.http.Client().Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	state, err := fixture.db.GetCursorSessionState(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.TargetActive || state.ReuseValid {
		t.Fatalf("ordinary chat left Cursor target reusable: %+v", state)
	}
}

func editCursorMessage(
	t *testing.T,
	fixture *cursorDirectFixture,
	sessionID string,
	messageID string,
) int {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"message_id": messageID, "revert": false})
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.http.URL+"/api/sessions/"+sessionID+"/edit",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := fixture.http.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}
