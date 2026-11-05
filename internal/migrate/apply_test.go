package migrate

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
	"github.com/enowdev/antares/internal/tools"
)

// fakeRAG records what was indexed.
type fakeRAG struct {
	coll string
	docs []tools.RAGDoc
}

func (f *fakeRAG) Name() string { return "fake" }
func (f *fakeRAG) Search(context.Context, string, string, int) ([]tools.RAGResult, error) {
	return nil, nil
}
func (f *fakeRAG) Index(_ context.Context, coll string, docs []tools.RAGDoc) (int, error) {
	f.coll = coll
	f.docs = append(f.docs, docs...)
	return len(docs), nil
}
func (f *fakeRAG) Collections(context.Context) ([]string, error) { return nil, nil }
func (f *fakeRAG) Delete(context.Context, string) error          { return nil }

// antaresHome points Antares at a fresh temp home with a sqlite store.
func antaresHome(t *testing.T) (string, store.Store) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("ANTARES_HOME", home)
	t.Setenv("ANTARES_CONFIG", "")
	t.Setenv("ANTARES_PROFILE", "")
	for _, k := range []string{"OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "ANTARES_API_KEY", "ANTARES_BASE_URL"} {
		t.Setenv(k, "")
	}
	if _, err := config.Reload(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), "sqlite", filepath.Join(home, "antares.db"), 1, 5000, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); config.Reload() })
	return home, db
}

const (
	secOpenRouter = "sk-or-v1-APPLYsecretOPENROUTER01"
	secAnthropic  = "sk-ant-APPLYsecretANTHROPIC02"
	secGroq       = "gsk_APPLYsecretGROQ03"
	secTyped      = "typed-APPLYsecretINPUT04"
	secTelegram   = "999:APPLYsecretTELEGRAM05"
	secDiscord    = "APPLYsecretDISCORD06"
	secSlackApp   = "xapp-APPLYsecretSLACKAPP07"
	secMCPEnv     = "APPLYsecretMCPENV08"
	secOldAnth    = "sk-ant-OLDsecretANTHROPIC09"
)

var applySecrets = []string{secOpenRouter, secAnthropic, secGroq, secTyped, secTelegram, secDiscord, secSlackApp, secMCPEnv}

// seedAntares gives the temp home some existing state to conflict with.
func seedAntares(t *testing.T, home string) {
	t.Helper()
	cfg, _ := config.Reload()
	cfg.Providers["anthropic"] = config.Provider{Kind: "anthropic", APIKey: secOldAnth, Enabled: true, Label: "Anthropic"}
	cfg.Providers["groq"] = config.Provider{Kind: "openai-compatible", BaseURL: "https://api.groq.com/openai/v1", APIKey: "old-groq", Enabled: true}
	cfg.MCP.Servers = map[string]config.MCPServer{"github": {Transport: "stdio", Command: "old-cmd", Enabled: true}}
	cfg.Gateway.Telegram.BotToken = "111:OLD"
	cfg.RAG.Enabled = true
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	must(t, config.SaveSoul("# Me\n\nI am the existing soul."))
	must(t, config.SaveUserMD("Old user facts."))
	must(t, os.MkdirAll(filepath.Join(home, "skills", "daily-notes"), 0o755))
	must(t, os.WriteFile(filepath.Join(home, "skills", "daily-notes", "SKILL.md"), []byte("---\nname: daily-notes\n---\nold"), 0o644))
	must(t, os.MkdirAll(filepath.Join(home, "roles"), 0o755))
	must(t, os.WriteFile(filepath.Join(home, "roles", "helper.md"), []byte("---\nname: helper\ntitle: Helper\n---\nold helper"), 0o644))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// sourceSkills creates skill folders to import.
func sourceSkills(t *testing.T) (string, string) {
	src := t.TempDir()
	for _, n := range []string{"web-research", "daily-notes"} {
		d := filepath.Join(src, n)
		must(t, os.MkdirAll(filepath.Join(d, "scripts"), 0o755))
		must(t, os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: "+n+"\ndescription: imported\n---\nnew "+n), 0o644))
		must(t, os.WriteFile(filepath.Join(d, "scripts", "run.sh"), []byte("echo hi"), 0o755))
	}
	return filepath.Join(src, "web-research"), filepath.Join(src, "daily-notes")
}

func applyPlan(t *testing.T, env Env) Plan {
	t.Helper()
	web, daily := sourceSkills(t)
	items := []Item{
		ProviderItem(env, ProviderPayload{ID: "openrouter", Label: "OpenRouter", Kind: "openai-compatible", BaseURL: "https://openrouter.ai/api/v1", APIKey: secOpenRouter}, ""),
		ProviderItem(env, ProviderPayload{ID: "anthropic", Label: "Anthropic", Kind: "anthropic", BaseURL: "https://api.anthropic.com/v1", APIKey: secAnthropic}, ""),
		ProviderItem(env, ProviderPayload{ID: "groq", Label: "Groq", Kind: "openai-compatible", BaseURL: "https://api.groq.com/openai/v1", APIKey: secGroq}, ""),
		ProviderItem(env, ProviderPayload{ID: "deepseek", Label: "DeepSeek", Kind: "openai-compatible", BaseURL: "https://api.deepseek.com"}, ""),
		ProviderItem(env, ProviderPayload{ID: "xai", Label: "xAI", Kind: "openai-compatible", BaseURL: "https://api.x.ai/v1"}, ""),
		ModelItem(env, ModelPayload{Provider: "groq", Model: "llama-3.3-70b", Fallback: []string{"groq/mixtral", "openrouter/openai/gpt-5"}}),
	}
	soul, _ := TextItem(env, CatSoul, "I am Hermes.", "SOUL.md")
	agents, _ := TextItem(env, CatAgentsMD, "Always answer briefly.", "AGENTS.md")
	user, _ := TextItem(env, CatUserMD, "Name: Rina", "USER.md")
	know, _ := KnowledgeItem(env, "memory/2026-09-30.md", "Shipped the release.")
	items = append(items, soul, agents, user, know)
	items = append(items, MemoryItems([]MemoryEntry{{Content: "Prefers Go."}, {Content: "Lives in Jakarta.", Heading: "About"}}, "global", "test")...)
	items = append(items,
		SkillItem(env, SkillDir{Name: "web-research", Dir: web}),
		SkillItem(env, SkillDir{Name: "daily-notes", Dir: daily}),
		MCPItem(env, MCPPayload{Name: "remote", Transport: "sse", URL: "https://mcp.example.com/sse"}),
		MCPItem(env, MCPPayload{Name: "github", Command: "npx", Args: []string{"server-github"}, Env: map[string]string{"TOKEN": secMCPEnv}}),
		CronItem("digest", CronPayload{Name: "Digest", Schedule: "0 9 * * *", Prompt: "Summarize", Timezone: "Asia/Jakarta"}, true),
		CronItem("bad", CronPayload{Name: "Bad", Schedule: "every tuesday", Prompt: "x"}, true),
		ChannelItem(env, "telegram", "", map[string]any{"bot_token": secTelegram, "allowed_users": []string{"1", "2"}}, ""),
		ChannelItem(env, "discord", "", map[string]any{"bot_token": secDiscord}, ""),
		ChannelItem(env, "slack", "", map[string]any{"bot_token": "xoxb-x"}, "app_token"),
		ChannelItem(env, "dingtalk", "", nil, ""),
		RoleItem(env, RolePayload{Name: "researcher-x", Title: "Researcher X", Prompt: "You research."}),
		RoleItem(env, RolePayload{Name: "helper", Title: "Helper", Prompt: "You help, imported."}),
	)
	return Plan{Detection: Detection{Source: "hermes", Name: "Hermes Agent", Root: "/nowhere"}, Items: items}
}

func applyChoices() []Choice {
	return []Choice{
		{ID: "provider:openrouter"},
		{ID: "provider:anthropic", Resolution: ResolveReplace},
		{ID: "provider:groq", Resolution: ResolveRename},
		{ID: "provider:deepseek", Input: secTyped},
		{ID: "provider:xai"}, // needs_input without input → skipped
		{ID: "model:default", Resolution: ResolveReplace},
		{ID: "soul:file", Resolution: ResolveAppend},
		{ID: "agents_md:file"},
		{ID: "user_md:file", Resolution: ResolveReplace},
		{ID: "knowledge:memory/2026-09-30.md"},
		{ID: "memory:" + ContentHash("Prefers Go.")},
		{ID: "memory:" + ContentHash("Lives in Jakarta.")},
		{ID: "skill:web-research"},
		{ID: "skill:daily-notes", Resolution: ResolveRename},
		{ID: "mcp:remote"},
		{ID: "mcp:github", Resolution: ResolveReplace},
		{ID: "cron:digest"},
		{ID: "cron:bad"},
		{ID: "channel:telegram", Resolution: ResolveReplace},
		{ID: "channel:discord"},
		{ID: "channel:slack", Input: secSlackApp},
		{ID: "channel:dingtalk"},
		{ID: "role:researcher-x"},
		{ID: "role:helper", Resolution: ResolveRename},
		{ID: "nope:missing"},
	}
}

func TestApplyEveryCategoryAndUndo(t *testing.T) {
	home, db := antaresHome(t)
	seedAntares(t, home)
	cfgBefore, _ := os.ReadFile(config.ConfigFile())
	cfg, _ := config.Reload()
	env := NewEnv(cfg)
	plan := applyPlan(t, env)

	var logs bytes.Buffer
	oldLog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(oldLog)

	rag := &fakeRAG{}
	now := time.Date(2026, 10, 3, 10, 15, 0, 0, time.UTC)
	rep, err := Apply(context.Background(), plan, applyChoices(), Deps{Store: db, RAG: rag, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Backup != "migrate-hermes-20261003T101500Z" {
		t.Fatalf("backup = %q", rep.Backup)
	}
	wantSkipped := []string{"provider:xai", "cron:bad", "channel:dingtalk"}
	if strings.Join(rep.Skipped, ",") != strings.Join(wantSkipped, ",") {
		t.Fatalf("skipped = %v", rep.Skipped)
	}
	if len(rep.Failed) != 1 || rep.Failed[0].ID != "nope:missing" {
		t.Fatalf("failed = %#v", rep.Failed)
	}
	if len(rep.Applied) != 21 || !rep.NeedsRestart {
		t.Fatalf("applied %d %v restart=%v", len(rep.Applied), rep.Applied, rep.NeedsRestart)
	}

	cfg, _ = config.Reload()
	if p := cfg.Providers["openrouter"]; p.APIKey != secOpenRouter || !p.Enabled {
		t.Fatal("openrouter not added")
	}
	if cfg.Providers["anthropic"].APIKey != secAnthropic {
		t.Fatal("anthropic not replaced")
	}
	if cfg.Providers["groq"].APIKey != "old-groq" || cfg.Providers["groq-2"].APIKey != secGroq {
		t.Fatal("groq rename wrong")
	}
	if cfg.Providers["deepseek"].APIKey != secTyped {
		t.Fatal("typed key not used")
	}
	if _, ok := cfg.Providers["xai"]; ok {
		t.Fatal("needs_input without input was applied")
	}
	if cfg.Model.Provider != "groq-2" || cfg.Model.Default != "llama-3.3-70b" || cfg.Model.Fallback[0] != "groq-2/mixtral" {
		t.Fatalf("model = %#v", cfg.Model)
	}
	if s := config.Soul(); !strings.Contains(s, "I am the existing soul.") || !strings.Contains(s, "## Imported from Hermes Agent (SOUL.md)\n\nI am Hermes.") {
		t.Fatalf("soul append: %q", s)
	}
	if config.LoadAgentsMD() != "Always answer briefly." || config.LoadUserMD() != "Name: Rina" {
		t.Fatal("agents/user md")
	}
	if rag.coll != "import-hermes" || len(rag.docs) != 1 || rag.docs[0].ID != "memory/2026-09-30.md" {
		t.Fatalf("rag = %#v", rag)
	}
	mems, _ := db.ListMemories(context.Background(), "global", "", 100)
	if len(mems) != 2 || mems[0].Source != "import:hermes" {
		t.Fatalf("memories = %#v", mems)
	}
	if !IsFile(filepath.Join(home, "skills", "web-research", "scripts", "run.sh")) {
		t.Fatal("skill not copied whole")
	}
	if b, _ := os.ReadFile(filepath.Join(home, "skills", "daily-notes-2", "SKILL.md")); !strings.Contains(string(b), "name: daily-notes-2") {
		t.Fatalf("renamed skill front matter: %s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "skills", "daily-notes", "SKILL.md")); !strings.HasSuffix(string(b), "old") {
		t.Fatal("existing skill touched")
	}
	if m := cfg.MCP.Servers["remote"]; m.Transport != "http" || !m.Enabled || !cfg.MCP.Enabled {
		t.Fatalf("mcp remote = %#v", m)
	}
	if cfg.MCP.Servers["github"].Command != "npx" {
		t.Fatal("mcp github not replaced")
	}
	jobs, _ := db.ListCronJobs(context.Background())
	if len(jobs) != 1 || jobs[0].Enabled || jobs[0].Meta["imported_from"] != "hermes" || jobs[0].Timezone != "Asia/Jakarta" {
		t.Fatalf("cron = %#v", jobs)
	}
	g := cfg.Gateway
	if !g.Enabled || g.Telegram.BotToken != secTelegram || len(g.Telegram.AllowedUsers) != 2 || !g.Telegram.Enabled {
		t.Fatalf("telegram = %#v", g.Telegram)
	}
	if g.Discord.BotToken != secDiscord || g.Slack.AppToken != secSlackApp || g.Slack.BotToken != "xoxb-x" {
		t.Fatal("discord/slack")
	}
	if !IsFile(filepath.Join(home, "roles", "researcher-x.md")) || !IsFile(filepath.Join(home, "roles", "helper-2.md")) {
		t.Fatal("roles not written")
	}

	// No secret in report, manifest or logs.
	repJSON, _ := json.Marshal(rep)
	man, _ := os.ReadFile(filepath.Join(BackupsDir(), rep.Backup, "manifest.json"))
	for where, s := range map[string]string{"report": string(repJSON), "manifest": string(man), "logs": logs.String()} {
		AssertNoSecrets(t, where, s, applySecrets...)
	}
	if bl := ListBackups(); len(bl) != 1 || bl[0].Name != rep.Backup || bl[0].Applied != 21 {
		t.Fatalf("backups = %#v", bl)
	}

	// Undo restores everything.
	if err := Undo(context.Background(), rep.Backup, Deps{Store: db}); err != nil {
		t.Fatal(err)
	}
	cfgAfter, _ := os.ReadFile(config.ConfigFile())
	if !bytes.Equal(cfgBefore, cfgAfter) {
		t.Fatal("config.yaml not restored")
	}
	if !strings.HasSuffix(config.Soul(), "I am the existing soul.") || config.LoadUserMD() != "Old user facts." || config.LoadAgentsMD() != "" {
		t.Fatal("persona files not restored")
	}
	for _, p := range []string{"skills/web-research", "skills/daily-notes-2", "roles/researcher-x.md", "roles/helper-2.md"} {
		if _, err := os.Stat(filepath.Join(home, p)); err == nil {
			t.Fatalf("%s survived undo", p)
		}
	}
	if !IsFile(filepath.Join(home, "skills", "daily-notes", "SKILL.md")) || !IsFile(filepath.Join(home, "roles", "helper.md")) {
		t.Fatal("pre-existing files removed by undo")
	}
	if mems, _ := db.ListMemories(context.Background(), "", "", 100); len(mems) != 0 {
		t.Fatal("memories survived undo")
	}
	if jobs, _ := db.ListCronJobs(context.Background()); len(jobs) != 0 {
		t.Fatal("cron survived undo")
	}
	if err := Undo(context.Background(), rep.Backup, Deps{Store: db}); err == nil {
		t.Fatal("second undo should fail")
	}
	if bl := ListBackups(); !bl[0].Undone {
		t.Fatal("backup not marked undone")
	}
}

func TestApplyReplaceSkillAndRoleUndo(t *testing.T) {
	home, db := antaresHome(t)
	seedAntares(t, home)
	cfg, _ := config.Reload()
	env := NewEnv(cfg)
	_, daily := sourceSkills(t)
	plan := Plan{Detection: Detection{Source: "openclaw", Name: "OpenClaw"}, Items: []Item{
		SkillItem(env, SkillDir{Name: "daily-notes", Dir: daily}),
		RoleItem(env, RolePayload{Name: "helper", Title: "Helper", Prompt: "new helper"}),
		RoleItem(env, RolePayload{Name: "researcher", Title: "Researcher", Prompt: "x"}), // built-in name
	}}
	rep, err := Apply(context.Background(), plan, []Choice{
		{ID: "skill:daily-notes", Resolution: ResolveReplace},
		{ID: "role:helper", Resolution: ResolveReplace},
		{ID: "role:researcher", Resolution: ResolveAppend},
	}, Deps{Store: db})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "skills", "daily-notes", "SKILL.md")); !strings.Contains(string(b), "new daily-notes") {
		t.Fatal("skill not replaced")
	}
	if b, _ := os.ReadFile(filepath.Join(home, "roles", "helper.md")); !strings.Contains(string(b), "new helper") {
		t.Fatal("role not replaced")
	}
	if env.RoleExists("researcher") && (len(rep.Failed) != 1 || rep.Failed[0].ID != "role:researcher") {
		t.Fatalf("append on a role should fail: %#v", rep)
	}
	must(t, Undo(context.Background(), rep.Backup, Deps{Store: db}))
	if b, _ := os.ReadFile(filepath.Join(home, "skills", "daily-notes", "SKILL.md")); !strings.HasSuffix(string(b), "old") {
		t.Fatal("skill not restored")
	}
	if IsDir(filepath.Join(home, "skills", "daily-notes", "scripts")) {
		t.Fatal("replaced skill's new files survived undo")
	}
	if b, _ := os.ReadFile(filepath.Join(home, "roles", "helper.md")); !strings.Contains(string(b), "old helper") {
		t.Fatal("role not restored")
	}
}

func TestApplyIdempotent(t *testing.T) {
	home, db := antaresHome(t)
	_ = home
	cfg, _ := config.Reload()
	plan := func() Plan {
		cfg, _ := config.Reload()
		env := NewEnv(cfg)
		items := []Item{ProviderItem(env, ProviderPayload{ID: "openrouter", Label: "OpenRouter", Kind: "openai-compatible", BaseURL: "https://openrouter.ai/api/v1", APIKey: secOpenRouter}, "")}
		items = append(items, MemoryItems([]MemoryEntry{{Content: "Prefers Go."}}, "global")...)
		items = append(items, CronItem("digest", CronPayload{Name: "Digest", Schedule: "@daily", Prompt: "Summarize"}, true))
		return Plan{Detection: Detection{Source: "hermes", Name: "Hermes Agent"}, Items: items}
	}
	_ = cfg
	all := func(p Plan) []Choice {
		var cs []Choice
		for _, it := range p.Items {
			cs = append(cs, Choice{ID: it.ID})
		}
		return cs
	}
	p1 := plan()
	if _, err := Apply(context.Background(), p1, all(p1), Deps{Store: db}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(config.ConfigFile())
	p2 := plan()
	if p2.Items[0].Status != StatusReady {
		t.Fatalf("identical provider should plan as ready: %#v", p2.Items[0])
	}
	rep, err := Apply(context.Background(), p2, all(p2), Deps{Store: db, Now: func() time.Time { return time.Now().Add(time.Second) }})
	if err != nil || len(rep.Applied) != 3 || len(rep.Failed) != 0 {
		t.Fatalf("second apply: %#v %v", rep, err)
	}
	after, _ := os.ReadFile(config.ConfigFile())
	if !bytes.Equal(before, after) {
		t.Fatal("re-applying an identical provider rewrote config.yaml")
	}
	mems, _ := db.ListMemories(context.Background(), "", "", 100)
	jobs, _ := db.ListCronJobs(context.Background())
	if len(mems) != 1 || len(jobs) != 1 {
		t.Fatalf("duplicates: %d memories, %d jobs", len(mems), len(jobs))
	}
	m, _ := readManifest(filepath.Join(BackupsDir(), rep.Backup))
	if len(m.Memories)+len(m.CronJobs) != 0 {
		t.Fatal("no-op re-apply must not claim rows for undo")
	}
}

func TestResolveBackupRejectsPaths(t *testing.T) {
	antaresHome(t)
	for _, bad := range []string{"", "../x", "migrate-../../etc", "/abs/migrate-x", "other-name", `migrate-a\b`} {
		if _, err := ResolveBackup(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
