package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/migrate"
	"github.com/enowdev/antares/internal/store"
)

func TestMigrateCatalogueIDsMatchSetup(t *testing.T) {
	var ids []string
	for _, p := range setupProviderCatalogue(config.Default()) {
		ids = append(ids, p.ID)
	}
	got := append([]string(nil), migrate.CatalogueProviderIDs...)
	sort.Strings(ids)
	sort.Strings(got)
	if strings.Join(ids, ",") != strings.Join(got, ",") {
		t.Fatalf("migrate.CatalogueProviderIDs %v != setup catalogue %v", got, ids)
	}
	cfg := config.Default()
	for _, name := range []string{"My Proxy", "OpenAI", "Custom", ""} {
		want := CustomProviderID(cfg, name)
		if got := migrate.CustomProviderID(func(id string) bool { _, ok := cfg.Providers[id]; return ok }, name); got != want {
			t.Fatalf("CustomProviderID(%q): migrate %q, server %q", name, got, want)
		}
	}
}

func migrateServer(t *testing.T) *Server {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	t.Setenv("ANTARES_CONFIG", "")
	t.Setenv("ANTARES_PROFILE", "")
	for _, k := range []string{"OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "ANTARES_API_KEY", "ANTARES_BASE_URL", "ANTARES_MODEL"} {
		t.Setenv(k, "")
	}
	old := migrate.RunningCheck
	migrate.RunningCheck = func(_, _ []string) bool { return false }
	t.Cleanup(func() { migrate.RunningCheck = old; config.Reload() })
	cfg, err := config.Reload()
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), "sqlite", filepath.Join(home, "antares.db"), 1, 5000, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(Options{Config: cfg, Store: db})
}

func migrateCall(t *testing.T, s *Server, method, path string, body any, loopback bool) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if loopback {
		req.RemoteAddr = "127.0.0.1:5555"
	} else {
		req.RemoteAddr = "192.0.2.10:5555"
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func hermesRoot(t *testing.T) string {
	root, err := filepath.Abs(filepath.Join("..", "migrate", "testdata", "hermes", "home"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

var hermesSecrets = []string{"FAKEopenrouterKEY0001", "FAKEanthropicKEY0002", "FAKEtelegramTOKEN0007", "FAKEgithubPAT0004"}

func TestMigrateAPIFlow(t *testing.T) {
	s := migrateServer(t)
	if !NeedsSetup(s.config()) {
		t.Fatal("fresh home should need setup")
	}

	rr := migrateCall(t, s, "GET", "/api/migrate/sources", nil, true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"id":"hermes"`) || !strings.Contains(rr.Body.String(), `"id":"openclaw"`) {
		t.Fatalf("sources: %d %s", rr.Code, rr.Body.String())
	}

	// During setup, plan needs loopback (or bearer) instead of a password.
	planReq := map[string]string{"source": "hermes", "root": hermesRoot(t)}
	if rr := migrateCall(t, s, "POST", "/api/migrate/plan", planReq, false); rr.Code != http.StatusForbidden {
		t.Fatalf("remote plan during setup: %d", rr.Code)
	}
	rr = migrateCall(t, s, "POST", "/api/migrate/plan", planReq, true)
	if rr.Code != 200 {
		t.Fatalf("plan: %d %s", rr.Code, rr.Body.String())
	}
	for _, sec := range hermesSecrets {
		if strings.Contains(rr.Body.String(), sec) {
			t.Fatal("plan response leaked a secret")
		}
	}
	var plan struct {
		PlanID    string            `json:"plan_id"`
		Detection migrate.Detection `json:"detection"`
		Items     []migrate.Item    `json:"items"`
	}
	json.Unmarshal(rr.Body.Bytes(), &plan)
	if plan.PlanID == "" || len(plan.Items) < 10 || plan.Detection.Source != "hermes" {
		t.Fatalf("plan body: %+v", plan)
	}
	if rr := migrateCall(t, s, "POST", "/api/migrate/plan", map[string]string{"source": "nope"}, true); rr.Code != 400 {
		t.Fatalf("unknown source: %d", rr.Code)
	}
	if rr := migrateCall(t, s, "POST", "/api/migrate/plan", map[string]string{"source": "hermes", "root": t.TempDir()}, true); rr.Code != 404 {
		t.Fatalf("missing install: %d", rr.Code)
	}

	// Apply only the chosen items.
	apply := map[string]any{"plan_id": plan.PlanID, "items": []map[string]string{
		{"id": "provider:openrouter"}, {"id": "model:default"}, {"id": "skill:web-research"}, {"id": "channel:telegram"},
	}}
	rr = migrateCall(t, s, "POST", "/api/migrate/apply", apply, true)
	if rr.Code != 200 {
		t.Fatalf("apply: %d %s", rr.Code, rr.Body.String())
	}
	for _, sec := range hermesSecrets {
		if strings.Contains(rr.Body.String(), sec) {
			t.Fatal("report leaked a secret")
		}
	}
	var rep migrate.Report
	json.Unmarshal(rr.Body.Bytes(), &rep)
	if len(rep.Applied) != 4 || !strings.HasPrefix(rep.Backup, "migrate-hermes-") || strings.Contains(rep.Backup, "/") {
		t.Fatalf("report: %+v", rep)
	}
	cfg := s.config()
	if cfg.Providers["openrouter"].APIKey == "" || cfg.Model.Provider != "openrouter" || cfg.Gateway.Telegram.BotToken == "" {
		t.Fatal("server config not reloaded with the import")
	}
	if _, ok := cfg.Providers["anthropic"]; ok && cfg.Providers["anthropic"].APIKey != "" {
		t.Fatal("an unchosen item was applied")
	}

	// Setup is done now: the wizard finishes with after_migrate.
	if NeedsSetup(s.config()) {
		t.Fatal("import should have completed setup")
	}
	ws := filepath.Join(t.TempDir(), "ws")
	if rr := migrateCall(t, s, "POST", "/api/setup/complete", map[string]any{"after_migrate": true, "workspace": ws}, true); rr.Code != 200 {
		t.Fatalf("setup complete after migrate: %d %s", rr.Code, rr.Body.String())
	}
	if c, _ := config.Reload(); c.Agent.Workspace != ws || c.Model.Provider != "openrouter" {
		t.Fatalf("after_migrate save: workspace=%q provider=%q", c.Agent.Workspace, c.Model.Provider)
	}
	if rr := migrateCall(t, s, "POST", "/api/setup/complete", map[string]any{"model": "x", "provider": "openrouter"}, true); rr.Code != http.StatusConflict {
		t.Fatalf("plain setup complete after setup: %d", rr.Code)
	}

	// Past setup without a password, plan/apply/undo need one.
	if rr := migrateCall(t, s, "POST", "/api/migrate/plan", planReq, true); rr.Code != http.StatusPreconditionRequired {
		t.Fatalf("plan after setup without password: %d", rr.Code)
	}
	cfg = s.config()
	cfg.Server.AuthToken = "tok-test"
	s.SetConfig(cfg)
	bearer := func(method, path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		json.NewEncoder(&buf).Encode(body)
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Authorization", "Bearer tok-test")
		rr := httptest.NewRecorder()
		s.Handler().ServeHTTP(rr, req)
		return rr
	}
	// The plan was consumed by the successful apply.
	if rr := bearer("POST", "/api/migrate/apply", apply); rr.Code != http.StatusGone {
		t.Fatalf("re-apply of a used plan: %d", rr.Code)
	}
	rr = bearer("GET", "/api/migrate/backups", nil)
	var bl struct {
		Backups []migrate.BackupInfo `json:"backups"`
	}
	json.Unmarshal(rr.Body.Bytes(), &bl)
	if rr.Code != 200 || len(bl.Backups) != 1 || bl.Backups[0].Name != rep.Backup || bl.Backups[0].Applied != 4 {
		t.Fatalf("backups: %d %s", rr.Code, rr.Body.String())
	}
	if rr := bearer("POST", "/api/migrate/undo", map[string]string{"backup": "../" + rep.Backup}); rr.Code != 400 {
		t.Fatalf("undo with a path: %d", rr.Code)
	}
	if rr := bearer("POST", "/api/migrate/undo", map[string]string{"backup": rep.Backup}); rr.Code != 200 {
		t.Fatalf("undo: %d %s", rr.Code, rr.Body.String())
	}
	if c, _ := config.Reload(); c.Gateway.Telegram.BotToken != "" {
		t.Fatal("undo did not restore config")
	}
}

func TestMigratePlanExpiry(t *testing.T) {
	s := migrateServer(t)
	rr := migrateCall(t, s, "POST", "/api/migrate/plan", map[string]string{"source": "hermes", "root": hermesRoot(t)}, true)
	var plan struct {
		PlanID string `json:"plan_id"`
	}
	json.Unmarshal(rr.Body.Bytes(), &plan)
	migratePlans.Lock()
	migratePlans.now = func() time.Time { return time.Now().Add(migratePlanTTL + time.Minute) }
	migratePlans.Unlock()
	t.Cleanup(func() {
		migratePlans.Lock()
		migratePlans.now = time.Now
		migratePlans.Unlock()
	})
	rr = migrateCall(t, s, "POST", "/api/migrate/apply", map[string]any{"plan_id": plan.PlanID, "items": []map[string]string{{"id": "provider:openrouter"}}}, true)
	if rr.Code != http.StatusGone {
		t.Fatalf("expired plan: %d %s", rr.Code, rr.Body.String())
	}
}

func TestAfterMigrateNeedsRecentMigration(t *testing.T) {
	s := migrateServer(t)
	cfg := s.config()
	cfg.Model.Default = "m"
	cfg.Model.Provider = "openrouter"
	cfg.Providers["openrouter"] = config.Provider{Kind: "openai-compatible", BaseURL: "https://openrouter.ai/api/v1", APIKey: "k", Enabled: true}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s.SetConfig(cfg)
	if rr := migrateCall(t, s, "POST", "/api/setup/complete", map[string]any{"after_migrate": true}, true); rr.Code != http.StatusConflict {
		t.Fatalf("after_migrate without a migration: %d", rr.Code)
	}
}
