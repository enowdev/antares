package cursor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMeAndModelsUseBearerAndDecodeCatalog(t *testing.T) {
	const key = "synthetic-cursor-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+key {
			t.Fatalf("Authorization = %q", got)
		}
		switch r.URL.Path {
		case "/v1/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"apiKeyName": "test key", "createdAt": "2026-08-12T00:00:00Z",
			})
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{
				map[string]any{
					"id": "composer-2", "displayName": "Composer 2",
					"parameters": []any{map[string]any{
						"id": "fast", "values": []any{map[string]any{"value": "true"}},
					}},
				},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := New(Options{BaseURL: srv.URL, APIKey: key, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	me, err := client.Me(context.Background())
	if err != nil || me.APIKeyName != "test key" {
		t.Fatalf("Me = %+v, %v", me, err)
	}
	models, err := client.Models(context.Background())
	if err != nil || len(models.Items) != 1 || models.Items[0].ID != "composer-2" {
		t.Fatalf("Models = %+v, %v", models, err)
	}
}

func TestAPIErrorNeverLeaksAPIKey(t *testing.T) {
	const key = "synthetic-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"rejected synthetic-secret"}}`)
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: key, HTTPClient: srv.Client()})
	_, err := client.Me(context.Background())
	if err == nil || !IsAuthError(err) {
		t.Fatalf("expected auth error, got %v", err)
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("error leaked key: %v", err)
	}
}

func TestAPIErrorClassificationAndRetryAfter(t *testing.T) {
	for _, status := range []int{400, 404, 409, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"code":"synthetic","message":"request failed"}`)
			}))
			defer srv.Close()
			client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
			_, err := client.Me(context.Background())
			if !IsStatus(err, status) {
				t.Fatalf("status %d classified as %v", status, err)
			}
			if status == 429 {
				if !IsRateLimit(err) {
					t.Fatalf("429 not classified as rate limit: %v", err)
				}
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.RetryAfter != 7*time.Second {
					t.Fatalf("RetryAfter = %v, want 7s", apiErr)
				}
			}
		})
	}
}

func TestCreateAgentRepoAndFollowUpPayloads(t *testing.T) {
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		seen = append(seen, body)
		switch r.URL.Path {
		case "/v1/agents":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"agent": map[string]any{
					"id": "bc-agent", "status": "ACTIVE",
					"url":         "https://cursor.com/agents/bc-agent",
					"latestRunId": "run-one",
				},
				"run": map[string]any{
					"id": "run-one", "agentId": "bc-agent", "status": "CREATING",
				},
			})
		case "/v1/agents/bc-agent/runs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run": map[string]any{
					"id": "run-two", "agentId": "bc-agent", "status": "CREATING",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	created, err := client.CreateAgent(context.Background(), CreateAgentRequest{
		Prompt:       Prompt{Text: "fix it"},
		Model:        &ModelSelection{ID: "composer-2"},
		Repos:        []Repository{{URL: "https://github.com/acme/repo", StartingRef: "main"}},
		AutoCreatePR: true,
	})
	if err != nil || created.Agent.ID != "bc-agent" || created.Run.ID != "run-one" {
		t.Fatalf("CreateAgent = %+v, %v", created, err)
	}
	run, err := client.CreateRun(context.Background(), "bc-agent", CreateRunRequest{
		Prompt: Prompt{Text: "add tests"}, Mode: "agent",
	})
	if err != nil || run.ID != "run-two" {
		t.Fatalf("CreateRun = %+v, %v", run, err)
	}
	if seen[0]["autoCreatePR"] != true {
		t.Fatalf("create payload = %#v", seen[0])
	}
}

// The Cursor Cloud Agents API accepts a stable envelope of hidden variant
// params alongside optional prompt images; both must round-trip byte-exact
// so a chosen model variant and any attachments are never silently altered.
func TestCreateAgentEncodesPromptImagesAndExactModelParams(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agent": map[string]any{
				"id": "a1", "status": "ACTIVE", "url": "https://cursor.com/agents/a1", "latestRunId": "r1",
			},
			"run": map[string]any{"id": "r1", "agentId": "a1", "status": "CREATING"},
		})
	}))
	defer srv.Close()

	want := CreateAgentRequest{
		Prompt: Prompt{
			Text:   "inspect this",
			Images: []PromptImage{{Data: "aGVsbG8=", MimeType: "image/png"}},
		},
		Model: &ModelSelection{
			ID: "gpt-5.6-sol",
			Params: []ModelParameterSelection{
				{ID: "context", Value: "1m"},
				{ID: "reasoning", Value: "max"},
				{ID: "fast", Value: "true"},
			},
		},
	}

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	if _, err := client.CreateAgent(context.Background(), want); err != nil {
		t.Fatalf("CreateAgent error = %v", err)
	}

	var got CreateAgentRequest
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("decode request body: %v (body=%s)", err, gotBody)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request body = %+v, want %+v", got, want)
	}
}

func TestCreateAgentOmitsOptionalFieldsWhenUnset(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"agent": map[string]any{"id": "a1", "status": "ACTIVE", "url": "https://cursor.com/agents/a1", "latestRunId": "r1"},
			"run":   map[string]any{"id": "r1", "agentId": "a1", "status": "CREATING"},
		})
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	if _, err := client.CreateAgent(context.Background(), CreateAgentRequest{Prompt: Prompt{Text: "fix it"}}); err != nil {
		t.Fatalf("CreateAgent error = %v", err)
	}
	for _, field := range []string{"repos", "model", "name", "workOnCurrentBranch", "autoCreatePR", "skipReviewerRequest", "mode"} {
		if _, ok := body[field]; ok {
			t.Fatalf("expected %q omitted, got %#v", field, body[field])
		}
	}
}

func TestGetAgentDecodesFullEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/agents/bc-agent" {
			t.Fatalf("method/path = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "bc-agent", "name": "fix bug", "status": "FINISHED",
			"url": "https://cursor.com/agents/bc-agent", "latestRunId": "run-one",
			"git": map[string]any{"branches": []any{
				map[string]any{"repoUrl": "https://github.com/acme/repo", "branch": "cursor/fix-bug"},
			}},
		})
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	agent, err := client.GetAgent(context.Background(), "bc-agent")
	if err != nil || agent.ID != "bc-agent" || agent.Status != "FINISHED" ||
		agent.Git == nil || len(agent.Git.Branches) != 1 || agent.Git.Branches[0].Branch != "cursor/fix-bug" {
		t.Fatalf("GetAgent = %+v, %v", agent, err)
	}
}

func TestGetRunDecodesFullEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/agents/bc-agent/runs/run-one" {
			t.Fatalf("method/path = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "run-one", "agentId": "bc-agent", "status": "FINISHED",
			"createdAt": "2026-08-12T00:00:00Z", "updatedAt": "2026-08-12T00:05:00Z",
			"durationMs": 5000, "result": "done",
		})
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	run, err := client.GetRun(context.Background(), "bc-agent", "run-one")
	if err != nil || run.ID != "run-one" || run.Status != "FINISHED" ||
		run.DurationMS != 5000 || run.Result != "done" {
		t.Fatalf("GetRun = %+v, %v", run, err)
	}
}

func TestCancelRunPostsToCancelPath(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	if err := client.CancelRun(context.Background(), "bc-agent", "run-one"); err != nil {
		t.Fatalf("CancelRun error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/agents/bc-agent/runs/run-one/cancel" {
		t.Fatalf("method/path = %s %s", gotMethod, gotPath)
	}
}

func TestLifecycleEscapesIDsInRequestPath(t *testing.T) {
	var gotURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "r", "agentId": "a", "status": "CREATING"})
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	if _, err := client.GetRun(context.Background(), "a/b", "r/1"); err != nil {
		t.Fatalf("GetRun error = %v", err)
	}
	if want := "/v1/agents/a%2Fb/runs/r%2F1"; gotURI != want {
		t.Fatalf("request URI = %q, want %q", gotURI, want)
	}
}

func TestCreateAgentDoesNotRetryOnTransientError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"code":"unavailable","message":"try again"}`)
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	if _, err := client.CreateAgent(context.Background(), CreateAgentRequest{Prompt: Prompt{Text: "fix it"}}); err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want exactly 1 (CreateAgent must not auto-retry)", calls.Load())
	}
}

func TestLifecycleValidatesInputsBeforeNetworkIO(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})

	if _, err := client.CreateAgent(context.Background(), CreateAgentRequest{}); err == nil {
		t.Fatal("expected error for empty CreateAgent prompt")
	}
	if _, err := client.CreateRun(context.Background(), "bc-agent", CreateRunRequest{}); err == nil {
		t.Fatal("expected error for empty CreateRun prompt")
	}
	if _, err := client.CreateRun(context.Background(), "", CreateRunRequest{Prompt: Prompt{Text: "x"}}); err == nil {
		t.Fatal("expected error for empty CreateRun agentID")
	}
	if _, err := client.GetAgent(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty GetAgent agentID")
	}
	if _, err := client.GetRun(context.Background(), "bc-agent", ""); err == nil {
		t.Fatal("expected error for empty GetRun runID")
	}
	if err := client.CancelRun(context.Background(), "bc-agent", ""); err == nil {
		t.Fatal("expected error for empty CancelRun runID")
	}
	if called {
		t.Fatal("expected no network calls for invalid input")
	}
}

func TestAPIErrorMessageTruncatedOnRuneBoundary(t *testing.T) {
	longMsg := strings.Repeat("a", 239) + "é" + strings.Repeat("é", 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		body, _ := json.Marshal(map[string]string{"message": longMsg})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	client, _ := New(Options{BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client()})
	_, err := client.Me(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !utf8.ValidString(msg) {
		t.Fatalf("invalid UTF-8: %q", msg)
	}
	if got := utf8.RuneCountInString(msg); got != 240 {
		t.Fatalf("rune count = %d, want 240", got)
	}
	want := strings.Repeat("a", 239) + "é"
	if msg != want {
		t.Fatalf("message = %q, want %q", msg, want)
	}
}
