package cursorrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/cursor"
)

func TestValidateModelAcceptsHiddenVariantParams(t *testing.T) {
	model := cursor.Model{
		ID:         "claude-opus-5",
		Parameters: []cursor.ModelParameter{{ID: "effort"}},
		Variants: []cursor.ModelVariant{{
			Params: []cursor.ModelParameterSelection{
				{ID: "cyber", Value: "false"},
				{ID: "effort", Value: "max"},
			},
			IsDefault: true,
		}},
	}
	runner := newTestRunner(t, cursor.ModelCatalog{Items: []cursor.Model{model}})
	got, err := runner.ValidateModel(context.Background(), &cursor.ModelSelection{
		ID: model.ID,
		Params: []cursor.ModelParameterSelection{
			{ID: "effort", Value: "max"},
			{ID: "cyber", Value: "false"},
		},
	}, RequireExactVariant)
	if err != nil || !reflect.DeepEqual(got.Params, model.Variants[0].Params) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestCatalogCachesForFiveMinutes(t *testing.T) {
	var nowMu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeCatalog(t, w, cursor.ModelCatalog{Items: []cursor.Model{{ID: "composer-2"}}})
	}))
	t.Cleanup(srv.Close)
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{BaseURL: " " + srv.URL + "/ ", APIKey: "synthetic-key", HTTPClient: srv.Client()}, nil
		},
		Now: clock, CatalogTTL: 5 * time.Minute,
	})

	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	advance(5*time.Minute - time.Nanosecond)
	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("catalog requests before expiry = %d, want 1", got)
	}

	advance(time.Nanosecond)
	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("catalog requests at expiry = %d, want 2", got)
	}
}

func TestCatalogCacheIsolatesCredentialFingerprints(t *testing.T) {
	var calls atomic.Int32
	var keysMu sync.Mutex
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		keysMu.Lock()
		keys = append(keys, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		keysMu.Unlock()
		writeCatalog(t, w, cursor.ModelCatalog{Items: []cursor.Model{{ID: "composer-2"}}})
	}))
	t.Cleanup(srv.Close)

	key := "credential-one"
	baseURL := srv.URL + "/"
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{BaseURL: baseURL, APIKey: key, HTTPClient: srv.Client()}, nil
		},
		Now: time.Now, CatalogTTL: 5 * time.Minute,
	})

	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	baseURL = " " + strings.TrimSuffix(srv.URL, "/") + " "
	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("normalized base URL split the cache: requests=%d", got)
	}

	key = "credential-two"
	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("changed credential reused old catalogue: requests=%d", got)
	}
	keysMu.Lock()
	defer keysMu.Unlock()
	if !reflect.DeepEqual(keys, []string{"credential-one", "credential-two"}) {
		t.Fatalf("authorization keys = %q", keys)
	}
}

func TestInvalidateCatalogForcesNextFetch(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeCatalog(t, w, cursor.ModelCatalog{})
	}))
	t.Cleanup(srv.Close)
	runner := testRunnerForServer(srv)

	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	runner.InvalidateCatalog()
	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("catalog requests = %d, want 2", got)
	}
}

func TestValidateModelRejectsDuplicateParamIDs(t *testing.T) {
	runner := newTestRunner(t, cursor.ModelCatalog{Items: []cursor.Model{{
		ID: "composer-2",
		Variants: []cursor.ModelVariant{{Params: []cursor.ModelParameterSelection{
			{ID: "effort", Value: "high"},
		}}},
	}}})
	_, err := runner.ValidateModel(context.Background(), &cursor.ModelSelection{
		ID: "composer-2",
		Params: []cursor.ModelParameterSelection{
			{ID: "effort", Value: "high"},
			{ID: "effort", Value: "low"},
		},
	}, RequireExactVariant)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err=%v, want duplicate-param rejection", err)
	}
}

func TestValidateModelDoesNotSynthesizeVariantCombinations(t *testing.T) {
	runner := newTestRunner(t, cursor.ModelCatalog{Items: []cursor.Model{{
		ID: "composer-2",
		Variants: []cursor.ModelVariant{
			{Params: []cursor.ModelParameterSelection{
				{ID: "effort", Value: "high"},
				{ID: "speed", Value: "slow"},
			}},
			{Params: []cursor.ModelParameterSelection{
				{ID: "effort", Value: "low"},
				{ID: "speed", Value: "fast"},
			}},
		},
	}}})
	_, err := runner.ValidateModel(context.Background(), &cursor.ModelSelection{
		ID: "composer-2",
		Params: []cursor.ModelParameterSelection{
			{ID: "effort", Value: "high"},
			{ID: "speed", Value: "fast"},
		},
	}, RequireExactVariant)
	if err == nil {
		t.Fatal("synthetic cross-variant combination was accepted")
	}
}

func TestValidateModelEmptyParamsPolicy(t *testing.T) {
	catalog := cursor.ModelCatalog{Items: []cursor.Model{
		{ID: "variant-model", Variants: []cursor.ModelVariant{{
			Params: []cursor.ModelParameterSelection{{ID: "effort", Value: "high"}},
		}}},
		{ID: "plain-model"},
	}}
	runner := newTestRunner(t, catalog)

	got, err := runner.ValidateModel(context.Background(),
		&cursor.ModelSelection{ID: "variant-model"}, PreserveUpstreamDefault)
	if err != nil {
		t.Fatalf("tool omission rejected: %v", err)
	}
	if got.ID != "variant-model" || len(got.Params) != 0 {
		t.Fatalf("tool omission changed selection: %+v", got)
	}

	if _, err := runner.ValidateModel(context.Background(),
		&cursor.ModelSelection{ID: "variant-model"}, RequireExactVariant); err == nil {
		t.Fatal("exact policy accepted empty params for a model with variants")
	}

	got, err = runner.ValidateModel(context.Background(),
		&cursor.ModelSelection{ID: "plain-model"}, RequireExactVariant)
	if err != nil || got.ID != "plain-model" || len(got.Params) != 0 {
		t.Fatalf("plain model got=%+v err=%v", got, err)
	}
}

func TestValidateModelRefreshesStaleCatalogExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	oldCatalog := cursor.ModelCatalog{Items: []cursor.Model{{
		ID: "composer-2",
		Variants: []cursor.ModelVariant{{Params: []cursor.ModelParameterSelection{
			{ID: "effort", Value: "low"},
		}}},
	}}}
	newCatalog := cursor.ModelCatalog{Items: []cursor.Model{{
		ID: "composer-2",
		Variants: []cursor.ModelVariant{{Params: []cursor.ModelParameterSelection{
			{ID: "effort", Value: "high"},
		}}},
	}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeCatalog(t, w, oldCatalog)
			return
		}
		writeCatalog(t, w, newCatalog)
	}))
	t.Cleanup(srv.Close)
	runner := testRunnerForServer(srv)

	if _, err := runner.Catalog(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	got, err := runner.ValidateModel(context.Background(), &cursor.ModelSelection{
		ID:     "composer-2",
		Params: []cursor.ModelParameterSelection{{ID: "effort", Value: "high"}},
	}, RequireExactVariant)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Params, newCatalog.Items[0].Variants[0].Params) {
		t.Fatalf("selection = %+v", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("catalog requests = %d, want one initial and one refresh", got)
	}
}

func TestCatalogCoalescesConcurrentRefresh(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		writeCatalog(t, w, cursor.ModelCatalog{})
	}))
	t.Cleanup(srv.Close)
	runner := testRunnerForServer(srv)

	const workers = 12
	errs := make(chan error, workers)
	for range workers {
		go func() {
			_, err := runner.Catalog(context.Background(), true)
			errs <- err
		}()
	}
	<-started
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range workers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent refresh requests = %d, want 1", got)
	}
}

func TestCatalogReturnsIndependentSanitizedCopies(t *testing.T) {
	secret := "catalogue-secret"
	catalog := cursor.ModelCatalog{Items: []cursor.Model{{
		ID:          "model-" + secret,
		DisplayName: "name-" + secret,
		Description: "description-" + secret,
		Aliases:     []string{"alias-" + secret},
		Parameters: []cursor.ModelParameter{{
			ID:          "param-" + secret,
			DisplayName: "parameter-" + secret,
			Values: []cursor.ModelParameterValue{{
				Value: "value-" + secret, DisplayName: "value-name-" + secret,
			}},
		}},
		Variants: []cursor.ModelVariant{{
			DisplayName: "variant-" + secret,
			Description: "variant-description-" + secret,
			Params: []cursor.ModelParameterSelection{{
				ID: "param-" + secret, Value: "value-" + secret,
			}},
		}},
	}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCatalog(t, w, catalog)
	}))
	t.Cleanup(srv.Close)
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{BaseURL: srv.URL, APIKey: secret, HTTPClient: srv.Client()}, nil
		},
		Now: time.Now, CatalogTTL: 5 * time.Minute,
	})

	first, err := runner.Catalog(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("catalogue leaked credential: %s", raw)
	}
	first.Items[0].ID = "mutated"
	second, err := runner.Catalog(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Items[0].ID == "mutated" {
		t.Fatal("caller mutation changed cached catalogue")
	}
	if second.Items[0].Aliases == nil || second.Items[0].Parameters == nil ||
		second.Items[0].Variants == nil {
		t.Fatalf("catalogue arrays were not normalized: %+v", second.Items[0])
	}
}

func TestLifecycleMutationsAreSingleAttempt(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		call   func(Runner) error
	}{
		{
			name: "create agent", method: http.MethodPost, path: "/v1/agents",
			call: func(r Runner) error {
				_, err := r.CreateAgent(context.Background(), cursor.CreateAgentRequest{
					Prompt: cursor.Prompt{Text: "do work"},
				})
				return err
			},
		},
		{
			name: "create run", method: http.MethodPost, path: "/v1/agents/bc-1/runs",
			call: func(r Runner) error {
				_, err := r.CreateRun(context.Background(), "bc-1", cursor.CreateRunRequest{
					Prompt: cursor.Prompt{Text: "continue"},
				})
				return err
			},
		},
		{
			name: "cancel run", method: http.MethodPost, path: "/v1/agents/bc-1/runs/run-1/cancel",
			call: func(r Runner) error {
				return r.CancelRun(context.Background(), "bc-1", "run-1")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tc.method, tc.path)
				}
				calls.Add(1)
				http.Error(w, `{"message":"temporary failure"}`, http.StatusInternalServerError)
			}))
			t.Cleanup(srv.Close)

			err := tc.call(testRunnerForServer(srv))
			if err == nil {
				t.Fatal("expected upstream failure")
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("requests = %d, want exactly 1", got)
			}
		})
	}
}

func TestLifecycleSanitizesErrorsAndGitText(t *testing.T) {
	secret := "lifecycle-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/bc-1":
			_ = json.NewEncoder(w).Encode(cursor.Agent{
				ID:          "bc-" + secret,
				Name:        "name-" + secret,
				Status:      "status-" + secret,
				URL:         "https://example.test/" + secret,
				LatestRunID: "run-" + secret,
				Git: &cursor.GitState{Branches: []cursor.GitBranch{{
					RepoURL: "https://github.com/" + secret,
					Branch:  "branch-" + secret,
					PRURL:   "https://github.com/pr/" + secret,
				}}},
				Repos: []cursor.Repository{{
					URL: "https://github.com/" + secret, StartingRef: "ref-" + secret, PRURL: "pr-" + secret,
				}},
			})
		case "/v1/agents/bc-error":
			w.WriteHeader(http.StatusConflict)
			_, _ = fmt.Fprintf(w, `{"error":{"code":"code-%s","message":"message-%s"}}`, secret, secret)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{BaseURL: srv.URL, APIKey: secret, HTTPClient: srv.Client()}, nil
		},
		Now: time.Now, CatalogTTL: 5 * time.Minute,
	})

	agent, err := runner.GetAgent(context.Background(), "bc-1")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(agent)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("agent response leaked credential: %s", raw)
	}

	_, err = runner.GetAgent(context.Background(), "bc-error")
	if err == nil {
		t.Fatal("expected API error")
	}
	var apiErr *cursor.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict {
		t.Fatalf("typed API error lost: %T %v", err, err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(apiErr.Code, secret) {
		t.Fatalf("error leaked credential: %#v", apiErr)
	}
}

func TestStreamRunSanitizesEventsTerminalAndProgress(t *testing.T) {
	secret := "stream-secret"
	longText := strings.Repeat("界", 2100) + secret
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/stream") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "id: event-%s\n", secret)
		fmt.Fprintln(w, "event: tool_call")
		fmt.Fprintf(w, "data: {\"callId\":\"call-%s\",\"name\":\"tool-%s\",\"status\":\"running-%s\",\"args\":{\"secret\":\"%s\"}}\n\n",
			secret, secret, secret, secret)
		fmt.Fprintln(w, "event: assistant")
		payload, _ := json.Marshal(map[string]string{"text": longText})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		fmt.Fprintln(w, "event: result")
		result, _ := json.Marshal(map[string]any{
			"runId":  "run-" + secret,
			"status": "FINISHED-" + secret,
			"text":   "result-" + secret,
			"git": map[string]any{"branches": []map[string]string{{
				"repoUrl": "repo-" + secret, "branch": "branch-" + secret, "prUrl": "pr-" + secret,
			}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", result)
	}))
	t.Cleanup(srv.Close)
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{BaseURL: srv.URL, APIKey: secret, HTTPClient: srv.Client()}, nil
		},
		Now: time.Now, CatalogTTL: 5 * time.Minute,
	})

	var events []cursor.StreamEvent
	terminal, err := runner.StreamRun(context.Background(), "bc-1", "run-1", "", nil,
		func(event cursor.StreamEvent) error {
			events = append(events, event)
			raw, _ := json.Marshal(event)
			if strings.Contains(string(raw), secret) {
				t.Fatalf("stream event leaked credential: %s", raw)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events=%d, want 3", len(events))
	}
	raw, err := json.Marshal(terminal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("terminal run leaked credential: %s", raw)
	}

	progress := runner.Progress(events[0])
	if progress.Message != "Cursor tool tool-[REDACTED] running-[REDACTED]" {
		t.Fatalf("tool progress message = %q", progress.Message)
	}
	progress = runner.Progress(events[1])
	if len([]rune(progress.Chunk)) != 2001 || !strings.HasSuffix(progress.Chunk, "…") {
		t.Fatalf("bounded chunk runes=%d suffix=%q", len([]rune(progress.Chunk)), progress.Chunk[len(progress.Chunk)-3:])
	}
	if strings.Contains(progress.Chunk, secret) {
		t.Fatal("progress leaked credential")
	}
}

func TestStreamRunResetErrorAbortsBeforeGetRun(t *testing.T) {
	var streamCalls atomic.Int32
	var getCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stream") {
			streamCalls.Add(1)
			w.WriteHeader(http.StatusGone)
			_, _ = w.Write([]byte(`{"message":"expired"}`))
			return
		}
		getCalls.Add(1)
		_ = json.NewEncoder(w).Encode(cursor.Run{ID: "run-1", Status: "FINISHED"})
	}))
	t.Cleanup(srv.Close)
	runner := testRunnerForServer(srv)
	resetErr := errors.New("persist reset failed")

	_, err := runner.StreamRun(context.Background(), "bc-1", "run-1", "",
		func() error { return resetErr },
		func(cursor.StreamEvent) error { return nil })
	if !errors.Is(err, resetErr) {
		t.Fatalf("err=%v, want reset error identity", err)
	}
	if got := streamCalls.Load(); got != 1 {
		t.Fatalf("stream requests=%d, want 1", got)
	}
	if got := getCalls.Load(); got != 0 {
		t.Fatalf("GetRun requests=%d, want 0", got)
	}
}

func TestResolverErrorRedactsCredential(t *testing.T) {
	secret := "resolver-secret"
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{APIKey: secret}, fmt.Errorf("could not use %s", secret)
		},
	})
	_, err := runner.Catalog(context.Background(), false)
	if err == nil {
		t.Fatal("expected resolver error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("resolver error leaked credential: %v", err)
	}
}

func TestValidateModelErrorsNeverEchoCredentialLikeSelections(t *testing.T) {
	secret := "selection-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCatalog(t, w, cursor.ModelCatalog{Items: []cursor.Model{{ID: "composer-2"}}})
	}))
	t.Cleanup(srv.Close)
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{BaseURL: srv.URL, APIKey: secret, HTTPClient: srv.Client()}, nil
		},
	})

	tests := []*cursor.ModelSelection{
		{ID: secret},
		{
			ID: "composer-2",
			Params: []cursor.ModelParameterSelection{
				{ID: secret, Value: "one"},
				{ID: secret, Value: "two"},
			},
		},
	}
	for _, selection := range tests {
		_, err := runner.ValidateModel(context.Background(), selection, RequireExactVariant)
		if err == nil {
			t.Fatalf("selection %+v unexpectedly passed", selection)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("validation error leaked credential-like input: %v", err)
		}
	}
}

func TestStreamSanitizationRedactsJSONEscapedCredential(t *testing.T) {
	secret := "quote\"slash\\secret"
	raw, err := json.Marshal(map[string]string{"value": secret})
	if err != nil {
		t.Fatal(err)
	}
	event := sanitizeStreamEvent(cursor.StreamEvent{
		Raw:        raw,
		ToolArgs:   append(json.RawMessage(nil), raw...),
		ToolResult: append(json.RawMessage(nil), raw...),
	}, secret)
	for name, value := range map[string]json.RawMessage{
		"raw": event.Raw, "args": event.ToolArgs, "result": event.ToolResult,
	} {
		var decoded map[string]string
		if err := json.Unmarshal(value, &decoded); err != nil {
			t.Fatalf("%s is no longer valid JSON: %v (%s)", name, err, value)
		}
		if decoded["value"] != "[REDACTED]" {
			t.Fatalf("%s returned credential after JSON decoding: %q", name, decoded["value"])
		}
	}
}

func newTestRunner(t *testing.T, catalog cursor.ModelCatalog) Runner {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		writeCatalog(t, w, catalog)
	}))
	t.Cleanup(srv.Close)
	return testRunnerForServer(srv)
}

func testRunnerForServer(srv *httptest.Server) Runner {
	return New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{
				BaseURL: srv.URL, APIKey: "synthetic-key", HTTPClient: srv.Client(),
			}, nil
		},
		Now: time.Now, CatalogTTL: 5 * time.Minute,
	})
}

func writeCatalog(t *testing.T, w http.ResponseWriter, catalog cursor.ModelCatalog) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(catalog); err != nil {
		t.Errorf("encode catalog: %v", err)
	}
}
