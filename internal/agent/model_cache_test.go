package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/llm"
)

func TestReasoningCapabilityAndModelsShareProviderCatalogueFetch(t *testing.T) {
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"id": "model-a", "reasoning": {"supported_efforts": ["Exact"], "default_effort": "Exact"}}
			]
		}`))
	}))
	defer srv.Close()

	a := agentWithConfig(reasoningTestConfig(srv.URL, nil))
	if _, err := a.ReasoningCapability(context.Background(), "router/model-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Models(context.Background(), "router"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReasoningCapability(context.Background(), "router/model-a"); err != nil {
		t.Fatal(err)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("provider catalogue fetches = %d, want one shared fetch", got)
	}
}

func TestModelsCachesCuratedProviderWithoutBroadeningWhitelist(t *testing.T) {
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": [
				{"id": "listed", "reasoning": {"supported_efforts": ["LOW"], "default_effort": "LOW"}},
				{"id": "unlisted", "reasoning": {"supported_efforts": ["HIGH"], "default_effort": "HIGH"}}
			]
		}`))
	}))
	defer srv.Close()

	cfg := reasoningTestConfig(srv.URL, []string{"listed"})
	a := agentWithConfig(cfg)
	for i := 0; i < 2; i++ {
		models, err := a.Models(context.Background(), "router")
		if err != nil {
			t.Fatal(err)
		}
		if len(models) != 1 || models[0].ID != "listed" {
			t.Fatalf("models = %#v, want only listed", models)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("curated provider catalogue fetches = %d, want one", got)
	}
}

func TestModelsConcurrentMissesUseSingleProviderFetch(t *testing.T) {
	var fetches atomic.Int32
	arrived := make(chan struct{}, 16)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		arrived <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer srv.Close()

	a := agentWithConfig(reasoningTestConfig(srv.URL, nil))
	const callers = 16
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := a.Models(context.Background(), "router")
			errs <- err
		}()
	}
	close(start)
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("provider catalogue fetch did not start")
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("concurrent provider catalogue fetches = %d, want one", got)
	}
}

func TestProviderCatalogueLeaderCancellationDoesNotPoisonSharedFetch(t *testing.T) {
	var (
		fetches     atomic.Int32
		startedOnce sync.Once
		releaseOnce sync.Once
	)
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		startedOnce.Do(func() { close(started) })
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer releaseOnce.Do(func() { close(release) })

	a := agentWithConfig(reasoningTestConfig(srv.URL, nil))
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderResult := make(chan error, 1)
	go func() {
		_, err := a.Models(leaderCtx, "router")
		leaderResult <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("leader provider catalogue fetch did not start")
	}

	waiterCalling := make(chan struct{})
	waiterResult := make(chan struct {
		models []llm.ModelInfo
		err    error
	}, 1)
	go func() {
		close(waiterCalling)
		models, err := a.Models(context.Background(), "router")
		waiterResult <- struct {
			models []llm.ModelInfo
			err    error
		}{models: models, err: err}
	}()
	<-waiterCalling

	cancelLeader()
	select {
	case err := <-leaderResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leader error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled leader did not return promptly")
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case got := <-waiterResult:
		if got.err != nil {
			t.Fatalf("healthy waiter inherited leader cancellation: %v", got.err)
		}
		if len(got.models) != 1 || got.models[0].ID != "model-a" {
			t.Fatalf("healthy waiter models = %#v, want model-a", got.models)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("healthy waiter did not receive shared catalogue")
	}

	models, err := a.Models(context.Background(), "router")
	if err != nil {
		t.Fatalf("healthy successor inherited leader cancellation: %v", err)
	}
	if len(models) != 1 || models[0].ID != "model-a" {
		t.Fatalf("healthy successor models = %#v, want cached model-a", models)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("provider catalogue fetches = %d, want one shared fetch", got)
	}
}

func TestProviderCatalogueCacheDoesNotShareAcrossCredentials(t *testing.T) {
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer srv.Close()

	cfg := reasoningTestConfig(srv.URL, nil)
	provider := cfg.Providers["router"]
	provider.APIKey = "credential-one"
	cfg.Providers["router"] = provider
	a := agentWithConfig(cfg)
	if _, err := a.Models(context.Background(), "router"); err != nil {
		t.Fatal(err)
	}

	changed := *cfg
	changed.Providers = make(map[string]config.Provider, len(cfg.Providers))
	for id, configured := range cfg.Providers {
		changed.Providers[id] = configured
	}
	provider = changed.Providers["router"]
	provider.APIKey = "credential-two"
	changed.Providers["router"] = provider
	a.SetConfig(&changed)
	if _, err := a.Models(context.Background(), "router"); err != nil {
		t.Fatal(err)
	}

	if got := fetches.Load(); got != 2 {
		t.Fatalf("provider catalogue fetches after credential change = %d, want two", got)
	}
}

func TestProviderCatalogueCacheExpiresAfterFiveMinutes(t *testing.T) {
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	a := agentWithConfig(reasoningTestConfig(srv.URL, nil))
	a.catalogNow = func() time.Time { return now }

	if _, err := a.Models(context.Background(), "router"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(4*time.Minute + 59*time.Second)
	if _, err := a.Models(context.Background(), "router"); err != nil {
		t.Fatal(err)
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("provider catalogue fetches before TTL = %d, want one", got)
	}

	now = now.Add(2 * time.Second)
	if _, err := a.Models(context.Background(), "router"); err != nil {
		t.Fatal(err)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("provider catalogue fetches after TTL = %d, want two", got)
	}
}

func TestProviderCatalogueCacheScopesNormalizedBaseURLAndProviderIdentity(t *testing.T) {
	var firstFetches, secondFetches atomic.Int32
	newServer := func(fetches *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
				http.NotFound(w, r)
				return
			}
			fetches.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
		}))
	}
	first := newServer(&firstFetches)
	defer first.Close()
	second := newServer(&secondFetches)
	defer second.Close()

	cfg := reasoningTestConfig(first.URL, nil)
	a := agentWithConfig(cfg)
	if _, err := a.Models(context.Background(), "router"); err != nil {
		t.Fatal(err)
	}

	withTrailingSlash := *cfg
	withTrailingSlash.Providers = map[string]config.Provider{}
	provider := cfg.Providers["router"]
	provider.BaseURL = first.URL + "/"
	withTrailingSlash.Providers["router"] = provider
	a.SetConfig(&withTrailingSlash)
	if _, err := a.Models(context.Background(), "router"); err != nil {
		t.Fatal(err)
	}
	if got := firstFetches.Load(); got != 1 {
		t.Fatalf("normalized equivalent base URL fetched %d times, want one", got)
	}

	changedBase := withTrailingSlash
	changedBase.Providers = map[string]config.Provider{}
	provider.BaseURL = second.URL
	changedBase.Providers["router"] = provider
	a.SetConfig(&changedBase)
	if _, err := a.Models(context.Background(), "router"); err != nil {
		t.Fatal(err)
	}

	changedIdentity := changedBase
	changedIdentity.Providers = map[string]config.Provider{
		"alternate": provider,
	}
	a.SetConfig(&changedIdentity)
	if _, err := a.Models(context.Background(), "alternate"); err != nil {
		t.Fatal(err)
	}
	if got := secondFetches.Load(); got != 2 {
		t.Fatalf("changed base/identity fetches = %d, want one per distinct scope", got)
	}
}

func TestProviderCatalogueScopeDoesNotRetainRawCredentials(t *testing.T) {
	const (
		apiSecret    = "RAW-API-SECRET"
		headerSecret = "RAW-HEADER-SECRET"
		urlSecret    = "RAW-URL-SECRET"
	)
	scope := providerCatalogScopeFor("router", config.Provider{
		Kind:    "openai-compatible",
		BaseURL: "https://example.test/v1?token=" + urlSecret,
		APIKey:  apiSecret,
		Headers: map[string]string{"Authorization": "Bearer " + headerSecret},
	})
	rendered := fmt.Sprintf("%#v", scope)
	for _, secret := range []string{apiSecret, headerSecret, urlSecret} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("cache scope retained raw credential %q", secret)
		}
	}
}

func TestReasoningCapabilityUsesStaleCatalogueWhenRefreshFails(t *testing.T) {
	var (
		fetches atomic.Int32
		outage  atomic.Bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if outage.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"temporary catalogue outage"}}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"data": [
				{"id": "model-a", "reasoning": {"supported_efforts": ["MiXeD"], "default_effort": "MiXeD"}}
			]
		}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	a := agentWithConfig(reasoningTestConfig(srv.URL, nil))
	a.catalogNow = func() time.Time { return now }

	if err := a.ValidateReasoningEffort(context.Background(), "router/model-a", "MiXeD"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5*time.Minute + time.Second)
	outage.Store(true)
	if err := a.ValidateReasoningEffort(context.Background(), "router/model-a", "MiXeD"); err != nil {
		t.Fatalf("stale live value rejected after refresh outage: %v", err)
	}
	if err := a.ValidateReasoningEffort(context.Background(), "router/model-a", "MiXeD"); err != nil {
		t.Fatalf("cached stale live value rejected: %v", err)
	}
	if got := fetches.Load(); got != 2 {
		t.Fatalf("provider catalogue fetches = %d, want initial load plus one failed refresh", got)
	}
}

type metadataUnavailableMarker interface {
	ReasoningMetadataUnavailable() bool
}

func TestExplicitReasoningReturnsDistinctBoundedErrorOnFirstCatalogueOutage(t *testing.T) {
	for _, curated := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "curated"}[curated], func(t *testing.T) {
			var fetches atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/models") {
					http.NotFound(w, r)
					return
				}
				fetches.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":{"message":"SECRET-UPSTREAM-DIAGNOSTIC"}}`))
			}))
			defer srv.Close()

			var models []string
			if curated {
				models = []string{"model-a"}
			}
			cfg := reasoningTestConfig(srv.URL, models)
			cfg.Agent.ReasoningEffort = "LEGACY-STORED"
			a := agentWithConfig(cfg)

			const submitted = "SECRET-SUBMITTED-EFFORT"
			for i := 0; i < 2; i++ {
				err := a.ValidateReasoningEffort(context.Background(), "router/model-a", submitted)
				if err == nil {
					t.Fatal("expected metadata-unavailable error")
				}
				if llm.IsUnsupportedReasoningEffort(err) {
					t.Fatalf("first catalogue outage misreported as unsupported: %v", err)
				}
				var unavailable metadataUnavailableMarker
				if !errors.As(err, &unavailable) || !unavailable.ReasoningMetadataUnavailable() {
					t.Fatalf("error = %T %v, want distinct metadata-unavailable error", err, err)
				}
				if len(err.Error()) > 200 {
					t.Fatalf("metadata error is unbounded (%d bytes)", len(err.Error()))
				}
				if strings.Contains(err.Error(), submitted) || strings.Contains(err.Error(), "SECRET-UPSTREAM-DIAGNOSTIC") {
					t.Fatalf("metadata error exposes submitted or upstream value: %v", err)
				}
			}
			if got := fetches.Load(); got != 1 {
				t.Fatalf("first-outage provider catalogue fetches = %d, want one cached failure", got)
			}

			got, err := a.resolveReasoning(context.Background(), reasoningInput{ModelRef: "router/model-a"})
			if err != nil {
				t.Fatalf("stored legacy value returned metadata error: %v", err)
			}
			if got.Value != "" || got.DiscardedLegacy != "LEGACY-STORED" {
				t.Fatalf("stored resolution = %+v, want Auto with legacy notice", got)
			}
		})
	}
}

func TestRunFirstCatalogueOutageRejectsExplicitButAllowsStoredAutoFallback(t *testing.T) {
	var (
		catalogFetches atomic.Int32
		chatCalls      atomic.Int32
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			catalogFetches.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"SECRET-UPSTREAM-DIAGNOSTIC"}}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			chatCalls.Add(1)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := reasoningTestConfig(srv.URL, nil)
	cfg.Agent.ReasoningEffort = "LEGACY-STORED"
	cfg.Streaming.Enabled = false
	a := newReasoningRunAgent(t, cfg)

	const submitted = "SECRET-SUBMITTED-EFFORT"
	_, err := a.Run(context.Background(), Request{
		Message:         "explicit",
		Quiet:           true,
		MaxTurns:        1,
		ReasoningEffort: submitted,
	}, nil)
	if err == nil || !IsReasoningMetadataUnavailable(err) {
		t.Fatalf("explicit run error = %T %v, want metadata unavailable", err, err)
	}
	if strings.Contains(err.Error(), submitted) || strings.Contains(err.Error(), "SECRET-UPSTREAM-DIAGNOSTIC") {
		t.Fatalf("explicit run error exposes submitted or upstream value: %v", err)
	}
	if got := chatCalls.Load(); got != 0 {
		t.Fatalf("chat calls after explicit metadata outage = %d, want zero", got)
	}

	var discardedNotices int
	result, err := a.Run(context.Background(), Request{
		Message:  "stored",
		Quiet:    true,
		MaxTurns: 1,
	}, func(event Event) error {
		if event.Type == EventNotice && strings.Contains(event.Message, "LEGACY-STORED") {
			discardedNotices++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reply != "ok" {
		t.Fatalf("stored fallback reply = %q", result.Reply)
	}
	if discardedNotices != 1 {
		t.Fatalf("stored fallback notices = %d, want one", discardedNotices)
	}
	if got := chatCalls.Load(); got != 1 {
		t.Fatalf("chat calls after stored metadata outage = %d, want one Auto request", got)
	}
	if got := catalogFetches.Load(); got != 1 {
		t.Fatalf("catalogue fetches across explicit and stored runs = %d, want one cached failure", got)
	}
}
