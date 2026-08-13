package cursorrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func TestValidateModelAcceptsUniqueAlias(t *testing.T) {
	runner := newTestRunner(t, cursor.ModelCatalog{Items: []cursor.Model{{
		ID:      "composer-2",
		Aliases: []string{"composer"},
	}}})

	got, err := runner.ValidateModel(context.Background(),
		&cursor.ModelSelection{ID: "composer"}, PreserveUpstreamDefault)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "composer-2" || got.Params != nil {
		t.Fatalf("alias selection = %+v, want canonical model authorization", got)
	}
}

func TestValidateModelPrefersExactIDOverAlias(t *testing.T) {
	runner := newTestRunner(t, cursor.ModelCatalog{Items: []cursor.Model{
		{ID: "composer", Aliases: []string{"legacy-composer"}},
		{ID: "composer-2", Aliases: []string{"composer"}},
	}})

	got, err := runner.ValidateModel(context.Background(),
		&cursor.ModelSelection{ID: "composer"}, PreserveUpstreamDefault)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "composer" {
		t.Fatalf("exact ID resolved to %q, want composer", got.ID)
	}
}

func TestValidateModelRejectsAmbiguousAliasDeterministically(t *testing.T) {
	first := cursor.Model{ID: "model-a", Aliases: []string{"shared"}}
	second := cursor.Model{ID: "model-b", Aliases: []string{"shared"}}
	var errorsByOrder []string
	for _, items := range [][]cursor.Model{{first, second}, {second, first}} {
		runner := newTestRunner(t, cursor.ModelCatalog{Items: items})
		_, err := runner.ValidateModel(context.Background(),
			&cursor.ModelSelection{ID: "shared"}, PreserveUpstreamDefault)
		if err == nil || !strings.Contains(err.Error(), "model alias is ambiguous") {
			t.Fatalf("ambiguous alias error = %v", err)
		}
		errorsByOrder = append(errorsByOrder, err.Error())
	}
	if errorsByOrder[0] != errorsByOrder[1] {
		t.Fatalf("ambiguous alias errors depend on catalogue order: %q != %q",
			errorsByOrder[0], errorsByOrder[1])
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

func TestValidateModelColdInvalidSelectionDoesNotRefetch(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeCatalog(t, w, cursor.ModelCatalog{Items: []cursor.Model{{
			ID: "composer-2",
			Variants: []cursor.ModelVariant{{Params: []cursor.ModelParameterSelection{
				{ID: "effort", Value: "low"},
			}}},
		}}})
	}))
	t.Cleanup(srv.Close)
	runner := testRunnerForServer(srv)

	_, err := runner.ValidateModel(context.Background(), &cursor.ModelSelection{
		ID:     "composer-2",
		Params: []cursor.ModelParameterSelection{{ID: "effort", Value: "not-upstream"}},
	}, RequireExactVariant)
	if err == nil || !strings.Contains(err.Error(), "refresh and reselect") {
		t.Fatalf("err=%v, want actionable reselect error", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cold invalid selection fetched catalogue %d times, want 1", got)
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

func TestCatalogPanicReleasesWaitersAndAllowsRetry(t *testing.T) {
	secret := "panic-secret"
	panicValue := "transport panic " + secret
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			panic(panicValue)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"items":[]}`)),
			Request:    req,
		}, nil
	})}
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{
				BaseURL: "https://cursor.invalid", APIKey: secret, HTTPClient: httpClient,
			}, nil
		},
	})

	leaderPanic := make(chan any, 1)
	go func() {
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_, _ = runner.Catalog(context.Background(), false)
		}()
		leaderPanic <- recovered
	}()
	<-started

	waiterCtx, cancelWaiter := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelWaiter()
	waiterErr := make(chan error, 1)
	go func() {
		_, err := runner.Catalog(waiterCtx, false)
		waiterErr <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)

	if recovered := <-leaderPanic; recovered != panicValue {
		t.Fatalf("leader panic = %#v, want %#v", recovered, panicValue)
	}
	select {
	case err := <-waiterErr:
		if err == nil {
			t.Fatal("waiter unexpectedly succeeded after leader panic")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiter remained wedged until its deadline: %v", err)
		}
		if strings.Contains(err.Error(), secret) || len([]rune(err.Error())) > 512 {
			t.Fatalf("waiter failure was not bounded and sanitized: %q", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("waiter was not released after leader panic")
	}

	retryCtx, cancelRetry := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancelRetry()
	catalog, err := runner.Catalog(retryCtx, false)
	if err != nil {
		t.Fatalf("retry after panic: %v", err)
	}
	if catalog == nil || catalog.Items == nil {
		t.Fatalf("retry catalogue was not normalized: %+v", catalog)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("transport calls = %d, want panicking call plus retry", got)
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

func TestCatalogBoundsNestedCollectionsAndUnicodeStrings(t *testing.T) {
	const (
		wantMaxIDRunes         = 1024
		wantMaxMetadataRunes   = 16 * 1024
		wantMaxModels          = 256
		wantMaxAliases         = 64
		wantMaxParameters      = 64
		wantMaxParameterValues = 128
		wantMaxVariants        = 256
		wantMaxVariantParams   = 64
	)
	secret := "catalog-bound-secret"
	longID := secret + strings.Repeat("界", wantMaxIDRunes+10)
	longMetadata := secret + strings.Repeat("語", wantMaxMetadataRunes+10)

	aliases := make([]string, wantMaxAliases+1)
	aliases[0] = longMetadata
	parameters := make([]cursor.ModelParameter, wantMaxParameters+1)
	parameters[0] = cursor.ModelParameter{
		ID:          longID,
		DisplayName: longMetadata,
		Values:      make([]cursor.ModelParameterValue, wantMaxParameterValues+1),
	}
	parameters[0].Values[0] = cursor.ModelParameterValue{
		Value: longID, DisplayName: longMetadata,
	}
	variants := make([]cursor.ModelVariant, wantMaxVariants+1)
	variants[0] = cursor.ModelVariant{
		DisplayName: longMetadata,
		Description: longMetadata,
		Params:      make([]cursor.ModelParameterSelection, wantMaxVariantParams+1),
	}
	variants[0].Params[0] = cursor.ModelParameterSelection{ID: longID, Value: longID}
	models := make([]cursor.Model, wantMaxModels+1)
	models[0] = cursor.Model{
		ID:          longID,
		DisplayName: longMetadata,
		Description: longMetadata,
		Aliases:     aliases,
		Parameters:  parameters,
		Variants:    variants,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCatalog(t, w, cursor.ModelCatalog{Items: models})
	}))
	t.Cleanup(srv.Close)
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{BaseURL: srv.URL, APIKey: secret, HTTPClient: srv.Client()}, nil
		},
	})

	catalog, err := runner.Catalog(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Items) != wantMaxModels {
		t.Fatalf("models=%d, want cap %d", len(catalog.Items), wantMaxModels)
	}
	model := catalog.Items[0]
	if len(model.Aliases) != wantMaxAliases ||
		len(model.Parameters) != wantMaxParameters ||
		len(model.Parameters[0].Values) != wantMaxParameterValues ||
		len(model.Variants) != wantMaxVariants ||
		len(model.Variants[0].Params) != wantMaxVariantParams {
		t.Fatalf("nested caps not applied: aliases=%d params=%d values=%d variants=%d variant_params=%d",
			len(model.Aliases), len(model.Parameters), len(model.Parameters[0].Values),
			len(model.Variants), len(model.Variants[0].Params))
	}
	if len([]rune(model.ID)) > wantMaxIDRunes ||
		len([]rune(model.DisplayName)) > wantMaxMetadataRunes ||
		len([]rune(model.Description)) > wantMaxMetadataRunes ||
		len([]rune(model.Aliases[0])) > wantMaxMetadataRunes ||
		len([]rune(model.Parameters[0].ID)) > wantMaxIDRunes ||
		len([]rune(model.Parameters[0].DisplayName)) > wantMaxMetadataRunes ||
		len([]rune(model.Parameters[0].Values[0].Value)) > wantMaxIDRunes ||
		len([]rune(model.Parameters[0].Values[0].DisplayName)) > wantMaxMetadataRunes ||
		len([]rune(model.Variants[0].DisplayName)) > wantMaxMetadataRunes ||
		len([]rune(model.Variants[0].Description)) > wantMaxMetadataRunes ||
		len([]rune(model.Variants[0].Params[0].ID)) > wantMaxIDRunes ||
		len([]rune(model.Variants[0].Params[0].Value)) > wantMaxIDRunes {
		t.Fatal("one or more catalogue strings exceeded its Unicode rune limit")
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("bounded catalogue leaked credential: %s", raw)
	}
	if catalog.Items == nil || model.Aliases == nil || model.Parameters == nil ||
		model.Parameters[0].Values == nil || model.Variants == nil ||
		model.Variants[0].Params == nil || catalog.Items[1].Aliases == nil ||
		catalog.Items[1].Parameters == nil || catalog.Items[1].Variants == nil {
		t.Fatal("bounded catalogue contains a nil collection")
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

func TestLifecycleBoundsResultsRepositoriesAndGit(t *testing.T) {
	const (
		wantMaxIDRunes       = 1024
		wantMaxMetadataRunes = 16 * 1024
		wantMaxContentRunes  = 1 << 20
		wantMaxRepositories  = 64
		wantMaxGitBranches   = 256
	)
	secret := "lifecycle-bound-secret"
	longID := secret + strings.Repeat("界", wantMaxIDRunes+10)
	longMetadata := secret + strings.Repeat("語", wantMaxMetadataRunes+10)
	longContent := secret + strings.Repeat("文", wantMaxContentRunes+10)
	repositories := make([]cursor.Repository, wantMaxRepositories+1)
	repositories[0] = cursor.Repository{
		URL: longMetadata, StartingRef: longMetadata, PRURL: longMetadata,
	}
	branches := make([]cursor.GitBranch, wantMaxGitBranches+1)
	branches[0] = cursor.GitBranch{
		RepoURL: longMetadata, Branch: longMetadata, PRURL: longMetadata,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agents/bc-bounds":
			_ = json.NewEncoder(w).Encode(cursor.Agent{
				ID:          longID,
				Name:        longMetadata,
				Status:      longMetadata,
				URL:         longMetadata,
				LatestRunID: longID,
				Repos:       repositories,
				Git:         &cursor.GitState{Branches: branches},
			})
		case "/v1/agents/bc-empty":
			_ = json.NewEncoder(w).Encode(cursor.Agent{Git: &cursor.GitState{}})
		case "/v1/agents/bc-bounds/runs/run-bounds":
			_ = json.NewEncoder(w).Encode(cursor.Run{
				ID:        longID,
				AgentID:   longID,
				Status:    longMetadata,
				CreatedAt: longMetadata,
				UpdatedAt: longMetadata,
				Result:    longContent,
				Git:       &cursor.GitState{Branches: branches},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{BaseURL: srv.URL, APIKey: secret, HTTPClient: srv.Client()}, nil
		},
	})

	agent, err := runner.GetAgent(context.Background(), "bc-bounds")
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.Repos) != wantMaxRepositories || len(agent.Git.Branches) != wantMaxGitBranches {
		t.Fatalf("agent collection caps: repos=%d branches=%d", len(agent.Repos), len(agent.Git.Branches))
	}
	if len([]rune(agent.ID)) > wantMaxIDRunes ||
		len([]rune(agent.Name)) > wantMaxMetadataRunes ||
		len([]rune(agent.Status)) > wantMaxMetadataRunes ||
		len([]rune(agent.URL)) > wantMaxMetadataRunes ||
		len([]rune(agent.LatestRunID)) > wantMaxIDRunes ||
		len([]rune(agent.Repos[0].URL)) > wantMaxMetadataRunes ||
		len([]rune(agent.Repos[0].StartingRef)) > wantMaxMetadataRunes ||
		len([]rune(agent.Repos[0].PRURL)) > wantMaxMetadataRunes ||
		len([]rune(agent.Git.Branches[0].RepoURL)) > wantMaxMetadataRunes ||
		len([]rune(agent.Git.Branches[0].Branch)) > wantMaxMetadataRunes ||
		len([]rune(agent.Git.Branches[0].PRURL)) > wantMaxMetadataRunes {
		t.Fatal("one or more agent strings exceeded its Unicode rune limit")
	}

	run, err := runner.GetRun(context.Background(), "bc-bounds", "run-bounds")
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Git.Branches) != wantMaxGitBranches ||
		len([]rune(run.ID)) > wantMaxIDRunes ||
		len([]rune(run.AgentID)) > wantMaxIDRunes ||
		len([]rune(run.Status)) > wantMaxMetadataRunes ||
		len([]rune(run.CreatedAt)) > wantMaxMetadataRunes ||
		len([]rune(run.UpdatedAt)) > wantMaxMetadataRunes ||
		len([]rune(run.Result)) > wantMaxContentRunes {
		t.Fatal("run bounds were not applied")
	}
	raw, err := json.Marshal(struct {
		Agent *cursor.Agent
		Run   *cursor.Run
	}{Agent: agent, Run: run})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("bounded lifecycle result leaked credential")
	}

	empty, err := runner.GetAgent(context.Background(), "bc-empty")
	if err != nil {
		t.Fatal(err)
	}
	if empty.Repos == nil || empty.Git == nil || empty.Git.Branches == nil {
		t.Fatalf("empty lifecycle slices were not normalized: %+v", empty)
	}
}

func TestGenericErrorsAreUnicodeBoundedAndRedacted(t *testing.T) {
	const wantMaxGenericErrorRunes = 4096
	secret := "generic-error-secret"
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{APIKey: secret}, errors.New(
				secret + strings.Repeat("界", wantMaxGenericErrorRunes+10),
			)
		},
	})

	_, err := runner.GetAgent(context.Background(), "bc-1")
	if err == nil {
		t.Fatal("expected resolver error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("generic error leaked credential: %q", err)
	}
	if got := len([]rune(err.Error())); got > wantMaxGenericErrorRunes {
		t.Fatalf("generic error runes=%d, want <= %d", got, wantMaxGenericErrorRunes)
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

func TestStreamSanitizationBoundsUnicodeFieldsAndRawJSON(t *testing.T) {
	const (
		wantMaxIDRunes       = 1024
		wantMaxMetadataRunes = 16 * 1024
		wantMaxContentRunes  = 1 << 20
		wantMaxRawRunes      = 1 << 20
	)
	secret := "stream-bound-secret"
	longID := secret + strings.Repeat("界", wantMaxIDRunes+10)
	longMetadata := secret + strings.Repeat("語", wantMaxMetadataRunes+10)
	longContent := secret + strings.Repeat("文", wantMaxContentRunes+10)
	longRaw, err := json.Marshal(map[string]string{
		"value": secret + strings.Repeat("生", wantMaxRawRunes+10),
	})
	if err != nil {
		t.Fatal(err)
	}

	event := sanitizeStreamEvent(cursor.StreamEvent{
		ID:         longID,
		Type:       longMetadata,
		Status:     longMetadata,
		Text:       longContent,
		RunID:      longID,
		Raw:        longRaw,
		ToolName:   longMetadata,
		CallID:     longID,
		ToolArgs:   append(json.RawMessage(nil), longRaw...),
		ToolResult: append(json.RawMessage(nil), longRaw...),
	}, secret)

	if len([]rune(event.ID)) > wantMaxIDRunes ||
		len([]rune(event.Type)) > wantMaxMetadataRunes ||
		len([]rune(event.Status)) > wantMaxMetadataRunes ||
		len([]rune(event.Text)) > wantMaxContentRunes ||
		len([]rune(event.RunID)) > wantMaxIDRunes ||
		len([]rune(event.ToolName)) > wantMaxMetadataRunes ||
		len([]rune(event.CallID)) > wantMaxIDRunes ||
		len([]rune(string(event.Raw))) > wantMaxRawRunes ||
		len([]rune(string(event.ToolArgs))) > wantMaxRawRunes ||
		len([]rune(string(event.ToolResult))) > wantMaxRawRunes {
		t.Fatal("one or more stream fields exceeded its Unicode rune limit")
	}
	if !event.ArgsTruncated || !event.ResultTruncated {
		t.Fatalf("service truncation flags were not set: %+v", event)
	}
	for name, value := range map[string]json.RawMessage{
		"raw": event.Raw, "args": event.ToolArgs, "result": event.ToolResult,
	} {
		if !json.Valid(value) {
			t.Fatalf("%s is not valid JSON after bounding: %q", name, value)
		}
		if strings.Contains(string(value), secret) {
			t.Fatalf("%s leaked credential", name)
		}
	}
}

func TestProgressDirectlyRedactsAndBoundsUnicode(t *testing.T) {
	secret := "progress-bound-secret"
	runner := New(Options{
		ResolveClient: func() (cursor.Options, error) {
			return cursor.Options{APIKey: secret}, nil
		},
	})
	progress := runner.Progress(cursor.StreamEvent{
		ToolName: secret + strings.Repeat("界", 2100),
		Status:   secret + strings.Repeat("語", 2100),
		Text:     secret + strings.Repeat("文", 2100),
	})
	if strings.Contains(progress.Message, secret) || strings.Contains(progress.Chunk, secret) {
		t.Fatalf("progress leaked credential: %+v", progress)
	}
	if len([]rune(progress.Message)) > 2001 || len([]rune(progress.Chunk)) > 2001 {
		t.Fatalf("progress was not Unicode bounded: message=%d chunk=%d",
			len([]rune(progress.Message)), len([]rune(progress.Chunk)))
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
