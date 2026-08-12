package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/enowdev/antares/internal/agent"
	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

func TestMessageIsRelevantUsesAutoWhenLowUnsupported(t *testing.T) {
	var chatCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			_, _ = w.Write([]byte(`{"data":[{"id":"plain-model","name":"Plain"}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			chatCalls.Add(1)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"NO"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Model.Provider = "router"
	cfg.Model.Default = "plain-model"
	cfg.Model.MaxRetries = -1
	cfg.Model.ReasoningEffort = ""
	cfg.Agent.ReasoningEffort = ""
	cfg.Streaming.Enabled = false
	cfg.Providers = map[string]config.Provider{
		"router": {
			Kind:    "openai-compatible",
			BaseURL: srv.URL,
			Enabled: true,
		},
	}
	db, err := store.Open(context.Background(), "memory", "", 1, 5000, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rt := &runtimeServices{
		cfg:   cfg,
		db:    db,
		agent: agent.New(cfg, db, tools.NewRegistry(), nil, nil),
	}

	if got := rt.messageIsRelevant(context.Background(), &config.Binding{
		Model:           "plain-model",
		RelevanceFilter: "Only answer release announcements.",
	}, "How is everyone?"); got {
		t.Fatal("messageIsRelevant = true, want classifier's NO response")
	}
	if got := chatCalls.Load(); got != 1 {
		t.Fatalf("classifier chat calls = %d, want one", got)
	}
}
