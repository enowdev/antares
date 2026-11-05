package migrate

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var openclawFixtureSecrets = []string{
	"sk-or-v1-FAKEocOPENROUTER0101", "sk-ant-FAKEocANTH0102", "sk-FAKEocOPENAI0103", "xoxb-FAKEocSLACK0104",
	"777:FAKEocTELEGRAM0105", "FAKEocDISCORD0106", "FAKEocMCP0107", "FAKEocCORPKEY0108", "FAKEocCODEX0109",
}

// openclawFixture copies the fake home to a temp dir (so the SQLite stores
// can be built from SQL there) and points HomeDir at it.
func openclawFixture(t *testing.T) string {
	t.Helper()
	noRunning(t)
	home := t.TempDir()
	must(t, copyTree(filepath.Join("testdata", "openclaw", "home"), home))
	state := filepath.Join(home, ".openclaw")
	buildSQLite(t, filepath.Join(state, "agents", "main", "agent", "openclaw-agent.sqlite"), filepath.Join("testdata", "openclaw", "agent.sql"))
	buildSQLite(t, filepath.Join(state, "state", "openclaw.sqlite"), filepath.Join("testdata", "openclaw", "state.sql"))
	old := HomeDir
	HomeDir = func() string { return home }
	t.Cleanup(func() { HomeDir = old })
	for _, k := range []string{"OPENCLAW_STATE_DIR", "OPENCLAW_CONFIG_PATH", "OPENCLAW_WORKSPACE_DIR",
		"OLLAMA_KEY_UNSET_FIXTURE", "DEEPSEEK_UNSET_FIXTURE", "TELEGRAM_TOKEN", "DISCORD_TOKEN", "MCP_TOKEN", "OPENROUTER_API_KEY"} {
		t.Setenv(k, "")
	}
	return home
}

func buildSQLite(t *testing.T, path, sqlFile string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	stmts, err := os.ReadFile(sqlFile)
	must(t, err)
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	must(t, err)
	defer db.Close()
	for _, s := range strings.Split(string(stmts), ";\n") {
		if strings.TrimSpace(s) != "" {
			if _, err := db.Exec(s); err != nil {
				t.Fatalf("%s: %v", filepath.Base(sqlFile), err)
			}
		}
	}
}

func TestOpenClawDetect(t *testing.T) {
	home := openclawFixture(t)
	// A profile state dir beside the default one.
	must(t, os.MkdirAll(filepath.Join(home, ".openclaw-lab", "workspace"), 0o755))
	dets, err := openclawSource{}.DetectAll(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, d := range dets {
		b.WriteString(d.Source + " profile=" + d.Profile + " root=" + strings.TrimPrefix(d.Root, home) + " version=" + d.Version + " summary=" + d.Summary + "\n")
	}
	CheckGolden(t, "openclaw_detect.txt", b.String())
}

func TestOpenClawPlan(t *testing.T) {
	openclawFixture(t)
	env := FakeEnv{Providers: map[string]bool{"openai": true}, MCP: map[string]bool{"fs": true}, Text: map[Category]string{CatAgentsMD: "custom"}}
	plan, err := BuildPlan(context.Background(), "openclaw", "", "", env)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderPlan(plan)
	AssertNoSecrets(t, "rendered plan", out, openclawFixtureSecrets...)
	CheckGolden(t, "openclaw_plan.txt", out)

	byID := map[string]Item{}
	for _, it := range plan.Items {
		byID[it.ID] = it
	}
	for id, want := range map[string]string{
		"provider:openrouter": "sk-or-v1-FAKEocOPENROUTER0101", // ${VAR} from env.vars
		"provider:corp":       "FAKEocCORPKEY0108",             // file SecretRef, JSON pointer
		"provider:anthropic":  "sk-ant-FAKEocANTH0102",         // SQLite auth profile
		"provider:openai":     "sk-FAKEocOPENAI0103",           // legacy auth-profiles.json
	} {
		if p := byID[id].Payload.Provider; p == nil || p.APIKey != want {
			t.Fatalf("%s key not resolved", id)
		}
	}
	if it := byID["provider:vaulted"]; it.Status != StatusNeedsInput || !strings.Contains(it.Reason, "exec") {
		t.Fatalf("exec secret should need input: %#v", it)
	}
	if it := byID["provider:deepseek"]; it.Status != StatusNeedsInput {
		t.Fatalf("unresolvable keyRef should need input: %#v", it)
	}
	if c := byID["channel:telegram"].Payload.Channel; c == nil || c.Fields["bot_token"] != "777:FAKEocTELEGRAM0105" {
		t.Fatal("telegram token not resolved from .env")
	}
	if m := byID["model:default"].Payload.Model; m == nil || m.Provider != "openrouter" || m.Model != "anthropic/claude-sonnet-4-6" {
		t.Fatalf("model = %#v", m)
	}
}

// The real config files are left alone: the plan only reads.
func TestOpenClawPlanIsReadOnly(t *testing.T) {
	home := openclawFixture(t)
	state := filepath.Join(home, ".openclaw")
	before := treeStamp(t, state)
	if _, err := BuildPlan(context.Background(), "openclaw", state, "", FakeEnv{}); err != nil {
		t.Fatal(err)
	}
	if after := treeStamp(t, state); after != before {
		t.Fatalf("plan modified the source tree:\n%s\n---\n%s", before, after)
	}
}

func treeStamp(t *testing.T, root string) string {
	var b strings.Builder
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if info, err := d.Info(); err == nil && !d.IsDir() {
			b.WriteString(p + " " + info.ModTime().String() + "\n")
		}
		return nil
	})
	return b.String()
}
