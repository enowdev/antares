package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/enowdev/antares/internal/config"
)

const cursorToolTestKey = "synthetic-key"

func cursorToolTestConfig(baseURL string) *config.Config {
	cfg := config.Default()
	p := cfg.Providers["cursor"]
	p.APIKey = cursorToolTestKey
	p.BaseURL = baseURL
	cfg.Providers["cursor"] = p
	return cfg
}

func cursorToolTestInput(cfg *config.Config, args string, progress *[]Progress) Input {
	return Input{
		Args: []byte(args),
		Deps: &Deps{Config: cfg},
		Emit: func(p Progress) {
			if progress != nil {
				*progress = append(*progress, p)
			}
		},
	}
}

func writeCursorSSE(w io.Writer, event string, payload any) {
	raw, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, raw)
}

func TestCursorToolSchemasAndApprovalClassification(t *testing.T) {
	if !NeedsApproval(cursorAgentTool{}) {
		t.Fatal("cursor_agent must require approval")
	}
	if NeedsApproval(cursorAgentStatusTool{}) {
		t.Fatal("cursor_agent_status must be read-only")
	}

	agentProps := cursorAgentTool{}.Schema()["properties"].(map[string]any)
	action := agentProps["action"].(map[string]any)
	gotActions := action["enum"].([]string)
	if strings.Join(gotActions, ",") != "start,follow_up,cancel" {
		t.Fatalf("cursor_agent action enum = %v", gotActions)
	}
	if got := agentProps["wait"].(map[string]any)["default"]; got != true {
		t.Fatalf("cursor_agent wait default = %#v, want true", got)
	}
	if got := agentProps["skip_reviewer_request"].(map[string]any)["default"]; got != true {
		t.Fatalf("skip_reviewer_request default = %#v, want true", got)
	}
	if got := agentProps["auto_create_pr"].(map[string]any)["default"]; got != false {
		t.Fatalf("auto_create_pr default = %#v, want false", got)
	}

	statusSchema := cursorAgentStatusTool{}.Schema()
	statusProps := statusSchema["properties"].(map[string]any)
	if got := statusProps["wait"].(map[string]any)["default"]; got != false {
		t.Fatalf("cursor_agent_status wait default = %#v, want false", got)
	}
	required := statusSchema["required"].([]string)
	if len(required) != 1 || required[0] != "agent_id" {
		t.Fatalf("cursor_agent_status required = %v", required)
	}
}

func TestCursorAgentApprovalProjectionIsBoundedAndRedacted(t *testing.T) {
	const secret = "must-not-appear-in-approval"
	raw, err := json.Marshal(map[string]any{
		"action":                "start",
		"prompt":                "Fix the issue using " + secret,
		"model":                 "composer-2",
		"repository_url":        "https://github.com/acme/repo",
		"starting_ref":          "main",
		"pull_request_url":      "https://github.com/acme/repo/pull/7",
		"mode":                  "plan",
		"auto_create_pr":        true,
		"skip_reviewer_request": false,
		"wait":                  false,
		"images":                []string{"data:image/png;base64," + secret},
		"api_key":               secret,
		"untrusted":             map[string]any{"instructions": secret},
	})
	if err != nil {
		t.Fatal(err)
	}

	op, err := (cursorAgentTool{}).ApprovalOperation(raw, "ses-one")
	if err != nil {
		t.Fatalf("ApprovalOperation: %v", err)
	}
	if op.SessionID != "ses-one" || op.Tool != "cursor_agent" {
		t.Fatalf("operation identity = %+v", op)
	}
	if op.Message != "Start Cursor Cloud Agent run" {
		t.Fatalf("operation message = %q", op.Message)
	}
	if len(op.Arguments) > 4096 {
		t.Fatalf("approval projection has %d bytes, want at most 4096", len(op.Arguments))
	}
	for _, forbidden := range []string{secret, "prompt", "images", "api_key", "untrusted", "instructions"} {
		if strings.Contains(op.Arguments, forbidden) {
			t.Errorf("approval projection contains forbidden %q: %s", forbidden, op.Arguments)
		}
	}

	var projection map[string]any
	if err := json.Unmarshal([]byte(op.Arguments), &projection); err != nil {
		t.Fatalf("approval projection is not JSON: %v", err)
	}
	want := map[string]any{
		"action":                "start",
		"model":                 "composer-2",
		"repository_url":        "https://github.com/acme/repo",
		"starting_ref":          "main",
		"pull_request_url":      "https://github.com/acme/repo/pull/7",
		"mode":                  "plan",
		"auto_create_pr":        true,
		"skip_reviewer_request": false,
		"wait":                  false,
	}
	for key, value := range want {
		if got := projection[key]; got != value {
			t.Errorf("projection[%q] = %#v, want %#v", key, got, value)
		}
	}

	longValue := strings.Repeat("界", 10_000)
	longRaw, _ := json.Marshal(map[string]any{
		"action":         "start",
		"prompt":         secret,
		"model":          longValue,
		"repository_url": "https://github.com/acme/repo",
		"starting_ref":   longValue,
	})
	longOp, err := (cursorAgentTool{}).ApprovalOperation(longRaw, "ses-long")
	if err != nil {
		t.Fatalf("long ApprovalOperation: %v", err)
	}
	if len(longOp.Arguments) > 4096 {
		t.Fatalf("long approval projection has %d bytes, want at most 4096", len(longOp.Arguments))
	}
	if strings.Contains(longOp.Arguments, longValue) {
		t.Fatal("long approval field was not bounded")
	}
}

func TestCursorApprovalFieldNormalizesBeforeWholeFieldTokenRedaction(t *testing.T) {
	value := "visible-prefix-" + string([]byte{0xff}) +
		"-crsr_synthetic_key_123.tail-must-not-leak"

	if got := boundCursorApprovalField(value); got != "[REDACTED]" {
		t.Fatalf("key-like approval field = %q, want whole-field redaction", got)
	}
}

func TestCursorAgentApprovalProjectionIncludesFollowUpAndCancelIDs(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantMessage string
		want        map[string]any
	}{
		{
			name:        "follow up",
			raw:         `{"action":"follow_up","agent_id":"bc-one","prompt":"continue privately","mode":"agent","wait":false}`,
			wantMessage: "Continue Cursor Cloud Agent run",
			want: map[string]any{
				"action":   "follow_up",
				"agent_id": "bc-one",
				"mode":     "agent",
				"wait":     false,
			},
		},
		{
			name:        "cancel",
			raw:         `{"action":"cancel","agent_id":"bc-one","run_id":"run-one"}`,
			wantMessage: "Cancel Cursor Cloud Agent run",
			want: map[string]any{
				"action":   "cancel",
				"agent_id": "bc-one",
				"run_id":   "run-one",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, err := (cursorAgentTool{}).ApprovalOperation(json.RawMessage(tt.raw), "ses-one")
			if err != nil {
				t.Fatalf("ApprovalOperation: %v", err)
			}
			if op.Message != tt.wantMessage {
				t.Fatalf("message = %q, want %q", op.Message, tt.wantMessage)
			}
			if strings.Contains(op.Arguments, "continue privately") ||
				strings.Contains(op.Arguments, "prompt") {
				t.Fatalf("projection leaked prompt: %s", op.Arguments)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(op.Arguments), &got); err != nil {
				t.Fatal(err)
			}
			for key, want := range tt.want {
				if got[key] != want {
					t.Errorf("projection[%q] = %#v, want %#v", key, got[key], want)
				}
			}
		})
	}
}

func TestCursorAgentApprovalRejectsInvalidOperation(t *testing.T) {
	if _, err := (cursorAgentTool{}).ApprovalOperation(
		json.RawMessage(`{"action":"follow_up","agent_id":"bc-one"}`),
		"ses-one",
	); err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("invalid operation error = %v", err)
	}
}

func TestCursorAgentRejectsMissingConfigAndInvalidRepository(t *testing.T) {
	in := cursorToolTestInput(config.Default(), `{"action":"start","prompt":"fix it"}`, nil)
	result := (cursorAgentTool{}).Execute(context.Background(), in)
	if !result.IsError || !strings.Contains(result.Content, "CURSOR_API_KEY") {
		t.Fatalf("missing-key result = %+v", result)
	}

	in.Deps = nil
	result = (cursorAgentTool{}).Execute(context.Background(), in)
	if !result.IsError || !strings.Contains(result.Content, "unavailable") {
		t.Fatalf("missing-runtime result = %+v", result)
	}

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "validation should happen before network I/O", http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := cursorToolTestConfig(srv.URL)

	for _, rawURL := range []string{
		"http://github.com/acme/repo",
		"https://user@github.com/acme/repo",
		"https://github.com:443/acme/repo",
		"https://example.com/acme/repo",
	} {
		t.Run(rawURL, func(t *testing.T) {
			args := fmt.Sprintf(`{"action":"start","prompt":"fix it","repository_url":%q}`, rawURL)
			got := (cursorAgentTool{}).Execute(context.Background(), cursorToolTestInput(cfg, args, nil))
			if !got.IsError || !strings.Contains(got.Content, "HTTPS GitHub") {
				t.Fatalf("invalid repository result = %+v", got)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid repository made %d network calls", calls.Load())
	}
}

func TestCursorAgentRejectsStartingRefWithoutRepositoryBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "validation should happen before network I/O", http.StatusInternalServerError)
	}))
	defer srv.Close()

	args := `{"action":"start","prompt":"fix it","starting_ref":"main","wait":false}`
	got := (cursorAgentTool{}).Execute(
		context.Background(),
		cursorToolTestInput(cursorToolTestConfig(srv.URL), args, nil),
	)
	if !got.IsError || !strings.Contains(got.Content, "repository_url is required when starting_ref is set") {
		t.Fatalf("result = %+v, want actionable starting_ref validation error", got)
	}
	if calls.Load() != 0 {
		t.Fatalf("starting_ref without repository_url made %d network request(s)", calls.Load())
	}
}

func TestCursorAgentValidatesActionSpecificArguments(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "validation should happen before network I/O", http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := cursorToolTestConfig(srv.URL)

	tests := []struct {
		name string
		args string
		want string
	}{
		{"missing action", `{}`, "action is required"},
		{"unknown action", `{"action":"launch"}`, "action must be"},
		{"start missing prompt", `{"action":"start","wait":false}`, "prompt is required"},
		{"start rejects agent id", `{"action":"start","prompt":"x","agent_id":"bc-one"}`, "agent_id is not allowed for start"},
		{"start rejects run id", `{"action":"start","prompt":"x","run_id":"run-one"}`, "run_id is not allowed for start"},
		{"start rejects mode", `{"action":"start","prompt":"x","mode":"ask"}`, "mode must be"},
		{"pull request needs repository", `{"action":"start","prompt":"x","pull_request_url":"https://github.com/acme/repo/pull/7"}`, "repository_url is required"},
		{"auto PR needs repository", `{"action":"start","prompt":"x","auto_create_pr":true}`, "repository_url is required"},
		{"pull request path", `{"action":"start","prompt":"x","repository_url":"https://github.com/acme/repo","pull_request_url":"https://github.com/acme/repo/issues/7"}`, "pull_request_url must be"},
		{"follow-up missing agent", `{"action":"follow_up","prompt":"x","wait":false}`, "agent_id is required"},
		{"follow-up bad agent prefix", `{"action":"follow_up","agent_id":"agent-one","prompt":"x","wait":false}`, "bc-"},
		{"follow-up missing prompt", `{"action":"follow_up","agent_id":"bc-one","wait":false}`, "prompt is required"},
		{"follow-up rejects run id", `{"action":"follow_up","agent_id":"bc-one","run_id":"run-one","prompt":"x","wait":false}`, "run_id is not allowed for follow_up"},
		{"follow-up rejects model", `{"action":"follow_up","agent_id":"bc-one","prompt":"x","model":"composer-2","wait":false}`, "model is not allowed for follow_up"},
		{"follow-up rejects repository", `{"action":"follow_up","agent_id":"bc-one","prompt":"x","repository_url":"https://github.com/acme/repo","wait":false}`, "repository_url is not allowed for follow_up"},
		{"follow-up rejects ref", `{"action":"follow_up","agent_id":"bc-one","prompt":"x","starting_ref":"main","wait":false}`, "starting_ref is not allowed for follow_up"},
		{"follow-up rejects pull request", `{"action":"follow_up","agent_id":"bc-one","prompt":"x","pull_request_url":"https://github.com/acme/repo/pull/7","wait":false}`, "pull_request_url is not allowed for follow_up"},
		{"follow-up rejects auto PR", `{"action":"follow_up","agent_id":"bc-one","prompt":"x","auto_create_pr":true,"wait":false}`, "auto_create_pr is not allowed for follow_up"},
		{"follow-up rejects reviewer option", `{"action":"follow_up","agent_id":"bc-one","prompt":"x","skip_reviewer_request":false,"wait":false}`, "skip_reviewer_request is not allowed for follow_up"},
		{"follow-up rejects mode", `{"action":"follow_up","agent_id":"bc-one","prompt":"x","mode":"ask","wait":false}`, "mode must be"},
		{"cancel missing agent", `{"action":"cancel","run_id":"run-one"}`, "agent_id is required"},
		{"cancel missing run", `{"action":"cancel","agent_id":"bc-one"}`, "run_id is required"},
		{"cancel bad agent prefix", `{"action":"cancel","agent_id":"agent-one","run_id":"run-one"}`, "bc-"},
		{"cancel bad run prefix", `{"action":"cancel","agent_id":"bc-one","run_id":"job-one"}`, "run-"},
		{"cancel rejects prompt", `{"action":"cancel","agent_id":"bc-one","run_id":"run-one","prompt":"x"}`, "prompt is not allowed for cancel"},
		{"cancel rejects mode", `{"action":"cancel","agent_id":"bc-one","run_id":"run-one","mode":"agent"}`, "mode is not allowed for cancel"},
		{"cancel rejects wait", `{"action":"cancel","agent_id":"bc-one","run_id":"run-one","wait":false}`, "wait is not allowed for cancel"},
		{"cancel rejects repository", `{"action":"cancel","agent_id":"bc-one","run_id":"run-one","repository_url":"https://github.com/acme/repo"}`, "repository_url is not allowed for cancel"},
		{"cancel rejects reviewer option", `{"action":"cancel","agent_id":"bc-one","run_id":"run-one","skip_reviewer_request":true}`, "skip_reviewer_request is not allowed for cancel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (cursorAgentTool{}).Execute(context.Background(), cursorToolTestInput(cfg, tt.args, nil))
			if !got.IsError || !strings.Contains(got.Content, tt.want) {
				t.Fatalf("result = %+v, want error containing %q", got, tt.want)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid arguments made %d network calls", calls.Load())
	}
}

func TestCursorAgentStatusValidatesIDs(t *testing.T) {
	cfg := cursorToolTestConfig("http://127.0.0.1:1")
	tests := []struct {
		args string
		want string
	}{
		{`{}`, "agent_id is required"},
		{`{"agent_id":"agent-one"}`, "bc-"},
		{`{"agent_id":"bc-one","run_id":"job-one"}`, "run-"},
	}
	for _, tt := range tests {
		got := (cursorAgentStatusTool{}).Execute(context.Background(), cursorToolTestInput(cfg, tt.args, nil))
		if !got.IsError || !strings.Contains(got.Content, tt.want) {
			t.Fatalf("args %s: result = %+v, want %q", tt.args, got, tt.want)
		}
	}
}

func TestCursorAgentStartPostsExpectedPayloadAndReturnsImmediately(t *testing.T) {
	var calls atomic.Int32
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/agents" {
			t.Errorf("method/path = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+cursorToolTestKey {
			t.Errorf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agent": map[string]any{
				"id": "bc-one", "status": "ACTIVE",
				"url": "https://cursor.com/agents/bc-one", "latestRunId": "run-one",
			},
			"run": map[string]any{
				"id": "run-one", "agentId": "bc-one", "status": "CREATING",
			},
		})
	}))
	defer srv.Close()

	args := `{
		"action":"start",
		"prompt":"fix it",
		"model":"composer-2",
		"repository_url":"https://github.com/acme/repo",
		"starting_ref":"main",
		"pull_request_url":"https://github.com/acme/repo/pull/7",
		"mode":"plan",
		"auto_create_pr":true,
		"wait":false
	}`
	got := (cursorAgentTool{}).Execute(context.Background(), cursorToolTestInput(cursorToolTestConfig(srv.URL), args, nil))
	if got.IsError {
		t.Fatalf("start result = %+v", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want exactly one create request", calls.Load())
	}
	prompt := body["prompt"].(map[string]any)
	if prompt["text"] != "fix it" || body["mode"] != "plan" {
		t.Fatalf("create body = %#v", body)
	}
	model := body["model"].(map[string]any)
	if model["id"] != "composer-2" {
		t.Fatalf("model = %#v", model)
	}
	repos := body["repos"].([]any)
	repo := repos[0].(map[string]any)
	if repo["url"] != "https://github.com/acme/repo" ||
		repo["startingRef"] != "main" ||
		repo["prUrl"] != "https://github.com/acme/repo/pull/7" {
		t.Fatalf("repo = %#v", repo)
	}
	if body["autoCreatePR"] != true || body["skipReviewerRequest"] != true {
		t.Fatalf("pointer defaults not applied: %#v", body)
	}
	if got.Meta["agent_id"] != "bc-one" || got.Meta["run_id"] != "run-one" ||
		got.Meta["cursor_url"] != "https://cursor.com/agents/bc-one" {
		t.Fatalf("meta = %#v", got.Meta)
	}
	if !strings.Contains(got.Content, "Do not busy-poll") {
		t.Fatalf("immediate result lacks polling guidance: %q", got.Content)
	}
}

func TestCursorAgentStartSupportsNoRepository(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if _, exists := body["repos"]; exists {
			t.Errorf("no-repo create unexpectedly sent repos: %#v", body["repos"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agent": map[string]any{
				"id": "bc-norepo", "status": "ACTIVE",
				"url": "https://cursor.com/agents/bc-norepo", "latestRunId": "run-norepo",
			},
			"run": map[string]any{
				"id": "run-norepo", "agentId": "bc-norepo", "status": "CREATING",
			},
		})
	}))
	defer srv.Close()

	args := `{"action":"start","prompt":"research this","wait":false}`
	got := (cursorAgentTool{}).Execute(context.Background(), cursorToolTestInput(cursorToolTestConfig(srv.URL), args, nil))
	if got.IsError || got.Meta["agent_id"] != "bc-norepo" {
		t.Fatalf("no-repo result = %+v", got)
	}
}

func TestCursorAgentFollowUpFetchesAgentAndPreservesURL(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/v1/agents/bc-one" {
				t.Errorf("first request = %s %s", r.Method, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "bc-one", "status": "FINISHED",
				"url": "https://cursor.com/agents/bc-one", "latestRunId": "run-one",
			})
		case 2:
			if r.Method != http.MethodPost || r.URL.Path != "/v1/agents/bc-one/runs" {
				t.Errorf("second request = %s %s", r.Method, r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			if body["mode"] != "agent" || body["prompt"].(map[string]any)["text"] != "add tests" {
				t.Errorf("follow-up body = %#v", body)
			}
			for _, forbidden := range []string{"model", "repos", "autoCreatePR", "skipReviewerRequest"} {
				if _, ok := body[forbidden]; ok {
					t.Errorf("follow-up body included %s: %#v", forbidden, body)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run": map[string]any{
					"id": "run-two", "agentId": "bc-one", "status": "CREATING",
				},
			})
		default:
			t.Errorf("unexpected request %d: %s %s", calls.Load(), r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	args := `{"action":"follow_up","agent_id":"bc-one","prompt":"add tests","mode":"agent","wait":false}`
	got := (cursorAgentTool{}).Execute(context.Background(), cursorToolTestInput(cursorToolTestConfig(srv.URL), args, nil))
	if got.IsError {
		t.Fatalf("follow-up result = %+v", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want agent read plus run create", calls.Load())
	}
	if got.Meta["cursor_url"] != "https://cursor.com/agents/bc-one" ||
		got.Meta["run_id"] != "run-two" {
		t.Fatalf("follow-up meta = %#v", got.Meta)
	}
}

func TestCursorAgentCancelPostsOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/agents/bc-one/runs/run-one/cancel" {
			t.Errorf("cancel request = %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	args := `{"action":"cancel","agent_id":"bc-one","run_id":"run-one"}`
	got := (cursorAgentTool{}).Execute(context.Background(), cursorToolTestInput(cursorToolTestConfig(srv.URL), args, nil))
	if got.IsError || calls.Load() != 1 {
		t.Fatalf("cancel result = %+v, calls = %d", got, calls.Load())
	}
	if got.Meta["agent_id"] != "bc-one" || got.Meta["run_id"] != "run-one" {
		t.Fatalf("cancel meta = %#v", got.Meta)
	}
}

// A cancel 404 can mean either the run or the whole agent is gone, and only
// Cursor's typed code says which.
func TestCursorAgentCancelNotFoundUsesTypedCode(t *testing.T) {
	for _, tc := range []struct {
		code string
		want string
	}{
		{code: "agent_not_found", want: "Cursor agent not found"},
		{code: "run_not_found", want: "Cursor run not found"},
		{code: "", want: "Cursor run not found"},
	} {
		t.Run("code="+tc.code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": tc.code, "message": "missing " + cursorToolTestKey,
				})
			}))
			defer srv.Close()

			got := (cursorAgentTool{}).Execute(
				context.Background(),
				cursorToolTestInput(
					cursorToolTestConfig(srv.URL),
					`{"action":"cancel","agent_id":"bc-one","run_id":"run-one"}`,
					nil,
				),
			)
			if !got.IsError || !strings.HasPrefix(got.Content, tc.want) {
				t.Fatalf("cancel 404 result = %+v, want %q", got, tc.want)
			}
			if strings.Contains(got.Content, cursorToolTestKey) {
				t.Fatalf("cancel 404 leaked the API key: %s", got.Content)
			}
		})
	}
}

func TestCursorAgentStatusResolvesLatestRunWithoutStreaming(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			if r.URL.Path != "/v1/agents/bc-one" {
				t.Errorf("agent path = %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "bc-one", "status": "FINISHED",
				"url": "https://cursor.com/agents/bc-one", "latestRunId": "run-latest",
			})
		case 2:
			if r.URL.Path != "/v1/agents/bc-one/runs/run-latest" {
				t.Errorf("run path = %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-latest", "agentId": "bc-one", "status": "FINISHED",
				"durationMs": 4200, "result": "all done",
				"git": map[string]any{"branches": []any{
					map[string]any{
						"repoUrl": "https://github.com/acme/repo",
						"branch":  "cursor/fix",
						"prUrl":   "https://github.com/acme/repo/pull/8",
					},
				}},
			})
		default:
			t.Errorf("unexpected request %d: %s", calls.Load(), r.URL.Path)
		}
	}))
	defer srv.Close()

	got := (cursorAgentStatusTool{}).Execute(
		context.Background(),
		cursorToolTestInput(cursorToolTestConfig(srv.URL), `{"agent_id":"bc-one"}`, nil),
	)
	if got.IsError || calls.Load() != 2 {
		t.Fatalf("status result = %+v, calls = %d", got, calls.Load())
	}
	for _, text := range []string{
		"bc-one", "run-latest", "FINISHED", "all done",
		"cursor/fix", "https://github.com/acme/repo/pull/8", "Do not busy-poll",
	} {
		if !strings.Contains(got.Content, text) {
			t.Errorf("status content missing %q: %s", text, got.Content)
		}
	}
	metaJSON, _ := json.Marshal(got.Meta)
	for _, text := range []string{"all done", "cursor/fix", "https://github.com/acme/repo/pull/8"} {
		if !strings.Contains(string(metaJSON), text) {
			t.Errorf("status meta missing %q: %s", text, metaJSON)
		}
	}
}

func TestCursorAgentWaitStreamsBoundedProgressAndFinalMetadata(t *testing.T) {
	longChunk := strings.Repeat("界", 2001)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/bc-one":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "bc-one", "status": "RUNNING",
				"url": "https://cursor.com/agents/bc-one", "latestRunId": "run-one",
			})
		case "/v1/agents/bc-one/runs/run-one/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			writeCursorSSE(w, "status", map[string]any{"runId": "run-one", "status": "RUNNING"})
			writeCursorSSE(w, "assistant", map[string]any{"text": longChunk})
			writeCursorSSE(w, "thinking", map[string]any{"text": "checking tests"})
			writeCursorSSE(w, "tool_call", map[string]any{"name": "grep", "status": "running"})
			writeCursorSSE(w, "result", map[string]any{
				"runId": "run-one", "status": "FINISHED", "text": "fixed",
				"durationMs": 9001,
				"git": map[string]any{"branches": []any{
					map[string]any{
						"repoUrl": "https://github.com/acme/repo",
						"branch":  "cursor/fixed",
						"prUrl":   "https://github.com/acme/repo/pull/9",
					},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var progress []Progress
	got := (cursorAgentStatusTool{}).Execute(
		context.Background(),
		cursorToolTestInput(cursorToolTestConfig(srv.URL), `{"agent_id":"bc-one","wait":true}`, &progress),
	)
	if got.IsError {
		t.Fatalf("wait result = %+v", got)
	}
	if len(progress) != 5 {
		t.Fatalf("progress = %#v, want status/assistant/thinking/tool/result", progress)
	}
	if progress[0].Message != "Cursor status" {
		t.Fatalf("status progress = %+v", progress[0])
	}
	if progress[1].Message != "Cursor assistant" ||
		!utf8.ValidString(progress[1].Chunk) ||
		utf8.RuneCountInString(progress[1].Chunk) != 2001 ||
		!strings.HasSuffix(progress[1].Chunk, "…") {
		t.Fatalf("bounded assistant progress = message %q, runes %d, valid=%v",
			progress[1].Message, utf8.RuneCountInString(progress[1].Chunk), utf8.ValidString(progress[1].Chunk))
	}
	if progress[2].Message != "Cursor thinking" || progress[2].Chunk != "checking tests" {
		t.Fatalf("thinking progress = %+v", progress[2])
	}
	if progress[3].Message != "Cursor tool grep running" {
		t.Fatalf("tool progress = %+v", progress[3])
	}
	for _, p := range progress {
		if p.Tool != "cursor_agent" {
			t.Errorf("progress tool = %q, want cursor_agent", p.Tool)
		}
	}
	for _, text := range []string{"fixed", "cursor/fixed", "https://github.com/acme/repo/pull/9"} {
		if !strings.Contains(got.Content, text) {
			t.Errorf("final content missing %q: %s", text, got.Content)
		}
	}
	if got.Meta["duration_ms"] != int64(9001) {
		t.Fatalf("duration meta = %#v", got.Meta["duration_ms"])
	}
	metaJSON, _ := json.Marshal(got.Meta)
	if !strings.Contains(string(metaJSON), "cursor/fixed") ||
		!strings.Contains(string(metaJSON), "https://github.com/acme/repo/pull/9") {
		t.Fatalf("git meta = %s", metaJSON)
	}
}

func TestCursorAgentWaitBoundsAndNormalizesEveryProgressField(t *testing.T) {
	invalidAndLong := strings.Repeat("界", 10) + string([]byte{0xff}) + strings.Repeat("界", 2100)
	longToolName := strings.Repeat("tool", 600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/bc-one":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "bc-one", "status": "RUNNING",
				"url": "https://cursor.com/agents/bc-one", "latestRunId": "run-one",
			})
		case "/v1/agents/bc-one/runs/run-one/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: ")
			_, _ = w.Write([]byte(invalidAndLong))
			_, _ = io.WriteString(w, "\ndata: {}\n\n")
			_, _ = io.WriteString(w, "event: assistant\ndata: {\"text\":\"")
			_, _ = w.Write([]byte(invalidAndLong))
			_, _ = io.WriteString(w, "\"}\n\n")
			writeCursorSSE(w, "tool_call", map[string]any{"name": longToolName, "status": "running"})
			writeCursorSSE(w, "result", map[string]any{
				"runId": "run-one", "status": "FINISHED", "text": "fixed",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var progress []Progress
	got := (cursorAgentStatusTool{}).Execute(
		context.Background(),
		cursorToolTestInput(cursorToolTestConfig(srv.URL), `{"agent_id":"bc-one","wait":true}`, &progress),
	)
	if got.IsError {
		t.Fatalf("wait result = %+v", got)
	}
	if len(progress) != 4 {
		t.Fatalf("progress = %#v, want event/assistant/tool/result", progress)
	}

	var boundedMessages, boundedChunks int
	for i, update := range progress {
		for field, value := range map[string]string{
			"message": update.Message,
			"chunk":   update.Chunk,
		} {
			if !utf8.ValidString(value) {
				t.Errorf("progress[%d].%s is invalid UTF-8", i, field)
			}
			if gotRunes := utf8.RuneCountInString(value); gotRunes > 2001 {
				t.Errorf("progress[%d].%s has %d runes, want at most 2001", i, field, gotRunes)
			}
			if strings.HasSuffix(value, "…") {
				if gotRunes := utf8.RuneCountInString(value); gotRunes != 2001 {
					t.Errorf("progress[%d].%s has %d bounded runes, want 2001", i, field, gotRunes)
				}
				if field == "message" {
					boundedMessages++
				} else {
					boundedChunks++
				}
			}
		}
	}
	if boundedMessages != 2 {
		t.Errorf("bounded messages = %d, want oversized event and tool messages", boundedMessages)
	}
	if boundedChunks != 1 {
		t.Errorf("bounded chunks = %d, want oversized assistant chunk", boundedChunks)
	}
	if !strings.Contains(progress[0].Message, "\uFFFD") ||
		!strings.Contains(progress[1].Chunk, "\uFFFD") {
		t.Fatal("invalid UTF-8 was not normalized with replacement runes")
	}
}

func TestCursorAgentTimeoutPreservesRecoverableIDsWithoutCancel(t *testing.T) {
	var cancelCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"agent": map[string]any{
					"id": "bc-one", "status": "ACTIVE",
					"url": "https://cursor.com/agents/bc-one", "latestRunId": "run-one",
				},
				"run": map[string]any{
					"id": "run-one", "agentId": "bc-one", "status": "CREATING",
				},
			})
		case "/v1/agents/bc-one/runs/run-one/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		case "/v1/agents/bc-one/runs/run-one/cancel":
			cancelCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	got := (cursorAgentTool{}).Execute(
		ctx,
		cursorToolTestInput(cursorToolTestConfig(srv.URL), `{"action":"start","prompt":"fix it"}`, nil),
	)
	if !got.IsError || !strings.Contains(got.Content, "remote Cursor run may still be active") {
		t.Fatalf("timeout result = %+v", got)
	}
	if got.Meta["agent_id"] != "bc-one" || got.Meta["run_id"] != "run-one" {
		t.Fatalf("timeout lost recovery ids: %#v", got.Meta)
	}
	if cancelCalls.Load() != 0 {
		t.Fatalf("timeout remotely canceled the run %d time(s)", cancelCalls.Load())
	}
}

func TestCursorAgentErrorsAreClassifiedAndSecretSafe(t *testing.T) {
	t.Run("server error is redacted and create is not retried", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"message":"rejected synthetic-key"}`)
		}))
		defer srv.Close()

		var progress []Progress
		got := (cursorAgentTool{}).Execute(
			context.Background(),
			cursorToolTestInput(cursorToolTestConfig(srv.URL), `{"action":"start","prompt":"fix it","wait":false}`, &progress),
		)
		if !got.IsError || calls.Load() != 1 {
			t.Fatalf("server error result = %+v, calls = %d", got, calls.Load())
		}
		metaJSON, _ := json.Marshal(got.Meta)
		combined := got.Content + got.Display + string(metaJSON)
		for _, p := range progress {
			combined += p.Message + p.Chunk
		}
		if strings.Contains(combined, cursorToolTestKey) {
			t.Fatalf("result leaked synthetic key: %s", combined)
		}
	})

	t.Run("rate limit includes retry after", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"message":"slow down"}`)
		}))
		defer srv.Close()
		got := (cursorAgentTool{}).Execute(
			context.Background(),
			cursorToolTestInput(cursorToolTestConfig(srv.URL), `{"action":"start","prompt":"fix it","wait":false}`, nil),
		)
		if !got.IsError || !strings.Contains(got.Content, "rate limit") ||
			got.Meta["retry_after_seconds"] != 7 {
			t.Fatalf("rate-limit result = %+v", got)
		}
	})

	t.Run("agent and run 404s are distinguished", func(t *testing.T) {
		agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"missing"}`)
		}))
		defer agentServer.Close()
		got := (cursorAgentStatusTool{}).Execute(
			context.Background(),
			cursorToolTestInput(cursorToolTestConfig(agentServer.URL), `{"agent_id":"bc-missing"}`, nil),
		)
		if !got.IsError || !strings.HasPrefix(got.Content, "Cursor agent not found") {
			t.Fatalf("agent 404 result = %+v", got)
		}

		runServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/agents/bc-one" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "bc-one", "url": "https://cursor.com/agents/bc-one", "latestRunId": "run-missing",
				})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"missing"}`)
		}))
		defer runServer.Close()
		got = (cursorAgentStatusTool{}).Execute(
			context.Background(),
			cursorToolTestInput(cursorToolTestConfig(runServer.URL), `{"agent_id":"bc-one"}`, nil),
		)
		if !got.IsError || !strings.HasPrefix(got.Content, "Cursor run not found") {
			t.Fatalf("run 404 result = %+v", got)
		}
	})

	t.Run("conflict text is preserved without retry", func(t *testing.T) {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"message":"agent already active"}`)
		}))
		defer srv.Close()
		got := (cursorAgentTool{}).Execute(
			context.Background(),
			cursorToolTestInput(cursorToolTestConfig(srv.URL), `{"action":"start","prompt":"fix it","wait":false}`, nil),
		)
		if !got.IsError || got.Content != "agent already active" || calls.Load() != 1 {
			t.Fatalf("conflict result = %+v, calls = %d", got, calls.Load())
		}
	})
}

func TestCursorAgentRedactsSecretFromStreamProgressAndResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/bc-one":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "bc-one", "status": "RUNNING",
				"url":         "https://cursor.com/agents/bc-one?token=" + cursorToolTestKey,
				"latestRunId": "run-one",
			})
		case "/v1/agents/bc-one/runs/run-one/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			writeCursorSSE(w, "assistant", map[string]any{"text": "saw " + cursorToolTestKey})
			writeCursorSSE(w, "tool_call", map[string]any{"name": cursorToolTestKey, "status": "running"})
			writeCursorSSE(w, "result", map[string]any{
				"runId": "run-one", "status": "FINISHED",
				"text": "finished with " + cursorToolTestKey,
				"git": map[string]any{"branches": []any{
					map[string]any{
						"repoUrl": "https://github.com/acme/" + cursorToolTestKey,
						"branch":  "cursor/" + cursorToolTestKey,
						"prUrl":   "https://github.com/acme/repo/pull/1?token=" + cursorToolTestKey,
					},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var progress []Progress
	got := (cursorAgentStatusTool{}).Execute(
		context.Background(),
		cursorToolTestInput(cursorToolTestConfig(srv.URL), `{"agent_id":"bc-one","wait":true}`, &progress),
	)
	metaJSON, _ := json.Marshal(got.Meta)
	combined := got.Content + got.Display + string(metaJSON)
	for _, p := range progress {
		combined += p.Message + p.Chunk
	}
	if strings.Contains(combined, cursorToolTestKey) {
		t.Fatalf("stream output leaked synthetic key: %s", combined)
	}
}

func TestCursorAgentRedactsTrimmedConfiguredSecretFromSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/bc-one":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "bc-one", "status": "RUNNING",
				"url":         "https://cursor.com/agents/bc-one?token=" + cursorToolTestKey,
				"latestRunId": "run-one",
			})
		case "/v1/agents/bc-one/runs/run-one":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "run-one", "agentId": "bc-one", "status": "FINISHED",
				"result": "finished with " + cursorToolTestKey,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := cursorToolTestConfig(srv.URL)
	provider := cfg.Providers["cursor"]
	provider.APIKey = "  " + cursorToolTestKey + "  "
	cfg.Providers["cursor"] = provider
	got := (cursorAgentStatusTool{}).Execute(
		context.Background(),
		cursorToolTestInput(cfg, `{"agent_id":"bc-one"}`, nil),
	)
	metaJSON, _ := json.Marshal(got.Meta)
	if combined := got.Content + got.Display + string(metaJSON); strings.Contains(combined, cursorToolTestKey) {
		t.Fatalf("snapshot output leaked trimmed synthetic key: %s", combined)
	}
}

func TestCursorAgentRedactsSecretFromInvalidLatestRunMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "bc-one", "status": "RUNNING",
			"url":         "https://cursor.com/agents/bc-one",
			"latestRunId": "invalid-" + cursorToolTestKey,
		})
	}))
	defer srv.Close()

	got := (cursorAgentStatusTool{}).Execute(
		context.Background(),
		cursorToolTestInput(cursorToolTestConfig(srv.URL), `{"agent_id":"bc-one"}`, nil),
	)
	metaJSON, _ := json.Marshal(got.Meta)
	if combined := got.Content + got.Display + string(metaJSON); strings.Contains(combined, cursorToolTestKey) {
		t.Fatalf("invalid latest-run result leaked synthetic key: %s", combined)
	}
}

func TestCursorAgentToolsRegisteredInOrdinaryAgentToolsets(t *testing.T) {
	for _, name := range []string{"cursor_agent", "cursor_agent_status"} {
		if _, ok := Default().Get(name); !ok {
			t.Errorf("%s is not registered", name)
		}
		for _, set := range []string{"coding", "vibecoder", "default"} {
			if !containsTool(ExpandToolset(set), name) {
				t.Errorf("toolset %q does not contain %s", set, name)
			}
		}
		for _, set := range []string{"minimal", "research", "browser", "social", "security", "osint", "reverse", "intercept"} {
			if containsTool(ExpandToolset(set), name) {
				t.Errorf("toolset %q unexpectedly contains %s", set, name)
			}
		}
	}
	if !containsTool(ExpandToolset("default"), "read_file") ||
		!containsTool(ExpandToolset("social"), "temp_mail") {
		t.Fatal("adding Cursor tools replaced ordinary toolset members")
	}
}
