package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers shared by the nanobot, picoclaw, cowagent and qwenpaw tests ----

// nanobotFixtureHome points HomeDir at testdata/<id>/home (or dir when set),
// disables process detection, and returns the absolute home path.
func nanobotFixtureHome(t *testing.T, id, dir string) string {
	t.Helper()
	if dir == "" {
		abs, err := filepath.Abs(filepath.Join("testdata", id, "home"))
		if err != nil {
			t.Fatal(err)
		}
		dir = abs
	}
	oldHome, oldPID := HomeDir, nanobotPIDAlive
	HomeDir = func() string { return dir }
	nanobotPIDAlive = func(string) bool { return false }
	t.Cleanup(func() { HomeDir, nanobotPIDAlive = oldHome, oldPID })
	noRunning(t)
	return dir
}

// nanobotCopyTree copies a fixture into a temp dir (for tests that add files).
func nanobotCopyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// nanobotPlanFor detects and plans source id with env.
func nanobotPlanFor(t *testing.T, s Source, env Env) Plan {
	t.Helper()
	det, err := s.Detect(context.Background(), "")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	p, err := s.Plan(context.Background(), det, env)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return p
}

// nanobotRender renders a plan for a golden file with the fixture home made
// relative, and checks no secret leaks into the text or the JSON.
func nanobotRender(t *testing.T, p Plan, home string, secrets ...string) string {
	t.Helper()
	out := RenderPlan(p)
	out = "root=" + strings.Replace(p.Detection.Root, home, "<home>", 1) + " version=" + p.Detection.Version + "\n" + out
	out = strings.ReplaceAll(out, home, "<home>")
	AssertNoSecrets(t, "rendered plan", out, secrets...)
	js, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	AssertNoSecrets(t, "plan JSON", string(js), secrets...)
	return out
}

func nanobotItem(t *testing.T, p Plan, id string) Item {
	t.Helper()
	for _, it := range p.Items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s in plan:\n%s", id, RenderPlan(p))
	return Item{}
}

func nanobotNoItem(t *testing.T, p Plan, id string) {
	t.Helper()
	for _, it := range p.Items {
		if it.ID == id {
			t.Fatalf("unexpected item %s (%s)", id, it.Status)
		}
	}
}

func nanobotWantStatus(t *testing.T, p Plan, want map[string]Status) {
	t.Helper()
	for id, st := range want {
		if got := nanobotItem(t, p, id); got.Status != st {
			t.Errorf("%s: status %s, want %s (reason %q)", id, got.Status, st, got.Reason)
		}
	}
}

// ---- nanobot ----

var nanobotSecrets = []string{
	"sk-ant-FAKE-nanobot-key-0001", "sk-or-FAKE-nanobot-key-0002", "sk-ds-FAKE-nanobot-key-0003",
	"sk-proxy-FAKE-nanobot-0004", "FAKE-mcp-token-nanobot-01", "123456:FAKE-telegram-nanobot-token",
	"FAKE-matrix-password", "sk-FAKE-env-deepseek-0009",
}

func TestNanobotDetect(t *testing.T) {
	home := nanobotFixtureHome(t, "nanobot", "")
	det, err := nanobotSource{}.Detect(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if det.Root != filepath.Join(home, ".nanobot") || det.Source != "nanobot" || det.Running {
		t.Fatalf("detection %+v", det)
	}
	if !strings.Contains(det.Summary, "providers") || !strings.Contains(det.Summary, "Telegram") {
		t.Fatalf("summary %q", det.Summary)
	}
	if _, err := (nanobotSource{}).Detect(context.Background(), t.TempDir()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty root: %v", err)
	}
}

func TestNanobotPlanGolden(t *testing.T) {
	home := nanobotFixtureHome(t, "nanobot", "")
	t.Setenv("NANOBOT_TEST_DEEPSEEK_KEY", "sk-FAKE-env-deepseek-0009")
	p := nanobotPlanFor(t, nanobotSource{}, FakeEnv{})
	CheckGolden(t, "../nanobot/plan.golden", nanobotRender(t, p, home, nanobotSecrets...))

	m := nanobotItem(t, p, "model:default").Payload.Model
	if m.Provider != "anthropic" || m.Model != "claude-opus-4-5" || len(m.Fallback) != 1 || m.Fallback[0] != "deepseek/deepseek-chat" {
		t.Fatalf("model %+v", m)
	}
	if k := nanobotItem(t, p, "provider:deepseek").Payload.Provider.APIKey; k != "sk-FAKE-env-deepseek-0009" {
		t.Fatal("deepseek key not expanded from ${VAR}")
	}
	proxy := nanobotItem(t, p, "provider:company-proxy").Payload.Provider
	if proxy.BaseURL != "https://llm.example.com/v1" || proxy.Kind != "openai-compatible" || proxy.Headers["X-Team"] != "core" {
		t.Fatalf("custom provider %+v", proxy)
	}
	if ds := nanobotItem(t, p, "provider:dashscope").Payload.Provider; ds.BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("dashscope keeps nanobot's (CN) endpoint, got %s", ds.BaseURL)
	}
	nanobotNoItem(t, p, "user_md:file")    // unchanged template
	nanobotNoItem(t, p, "provider:groq")   // empty block
	nanobotNoItem(t, p, "cron:dream")      // nanobot's own system job
	nanobotNoItem(t, p, "channel:discord") // disabled, no token
	nanobotWantStatus(t, p, map[string]Status{
		"provider:anthropic": StatusReady, "soul:file": StatusReady, "agents_md:file": StatusReady,
		"skill:web-research": StatusReady, "skill:notes": StatusReady,
		"mcp:filesystem": StatusReady, "mcp:remote": StatusReady, "mcp:linear": StatusUnsupported,
		"cron:a1b2c3": StatusReady, "cron:d4e5f6": StatusReady, "cron:g7h8i9": StatusUnsupported, "cron:heartbeat": StatusReady,
		"channel:telegram": StatusReady, "channel:whatsapp": StatusUnsupported, "channel:dingtalk": StatusUnsupported,
		"channel:matrix": StatusNeedsInput,
	})
	if c := nanobotItem(t, p, "cron:d4e5f6").Payload.Cron; c.Schedule != "@every 1h30m" || c.Enabled {
		t.Fatalf("interval job %+v", c)
	}
	if c := nanobotItem(t, p, "cron:heartbeat").Payload.Cron; c.Schedule != "@every 1h" || !strings.Contains(c.Prompt, "build status") {
		t.Fatalf("heartbeat %+v", c)
	}
	var mem []string
	for _, it := range p.Items {
		if it.Category == CatMemory {
			mem = append(mem, it.Payload.Memory.Content)
		}
	}
	if strings.Join(mem, "|") != "The user's name is Eno.|Eno lives in Jakarta.|Prefers short answers." {
		t.Fatalf("memory entries %q", mem)
	}
}

func TestNanobotUnsetEnvKeyNeedsInput(t *testing.T) {
	nanobotFixtureHome(t, "nanobot", "")
	t.Setenv("NANOBOT_TEST_DEEPSEEK_KEY", "")
	p := nanobotPlanFor(t, nanobotSource{}, FakeEnv{})
	it := nanobotItem(t, p, "provider:deepseek")
	if it.Status != StatusNeedsInput || it.Input != "api_key" || !strings.Contains(it.Reason, "environment variable") {
		t.Fatalf("deepseek %+v", it)
	}
}

func TestNanobotConflicts(t *testing.T) {
	nanobotFixtureHome(t, "nanobot", "")
	t.Setenv("NANOBOT_TEST_DEEPSEEK_KEY", "sk-FAKE-env-deepseek-0009")
	env := FakeEnv{
		Providers: map[string]bool{"anthropic": true, "openrouter": true}, SameProvider: map[string]bool{"openrouter": true},
		Skills: map[string]bool{"web-research": true}, MCP: map[string]bool{"filesystem": true},
		Channels: map[string]bool{"telegram": true}, Text: map[Category]string{CatSoul: "custom", CatAgentsMD: "default"},
		DefaultModel: true, RAGOff: true,
	}
	p := nanobotPlanFor(t, nanobotSource{}, env)
	nanobotWantStatus(t, p, map[string]Status{
		"provider:anthropic": StatusConflict, "provider:openrouter": StatusReady, "model:default": StatusConflict,
		"soul:file": StatusConflict, "agents_md:file": StatusReady, "skill:web-research": StatusConflict,
		"mcp:filesystem": StatusConflict, "channel:telegram": StatusConflict,
	})
	for _, it := range p.Items {
		if it.Status == StatusConflict && it.Selected {
			t.Errorf("%s: a conflict must not be preselected", it.ID)
		}
	}
}

func TestNanobotHeartbeatTasks(t *testing.T) {
	if got := nanobotHeartbeatTasks("# Heartbeat\n<!-- note\nmore -->\n## Active Tasks\n<!-- x -->\n\n", "## Active Tasks"); got != "" {
		t.Fatalf("template only: %q", got)
	}
	if got := nanobotHeartbeatTasks("intro\nAdd your heartbeat tasks below this line:\n# h\n- a\n  - b\n", "Add your heartbeat tasks below this line:"); got != "- a\n  - b" {
		t.Fatalf("got %q", got)
	}
}

func TestNanobotIdentity(t *testing.T) {
	for _, c := range []struct {
		name, base, wantID, wantKind string
	}{
		{"anthropic", "https://api.anthropic.com", "anthropic", "anthropic"},
		{"anthropic", "https://claude-proxy.example.com", "anthropic-custom-endpoint", "openai-compatible"},
		{"gemini", "https://generativelanguage.googleapis.com/v1beta/openai/", "gemini", "gemini"},
		{"zhipu", "https://open.bigmodel.cn/api/paas/v4", "zhipu-ai", "openai-compatible"},
		{"moonshot", "https://api.moonshot.cn/v1", "moonshot", "openai-compatible"},
		{"siliconflow", "https://api.siliconflow.cn/v1", "siliconflow", "openai-compatible"},
	} {
		label := map[string]string{"zhipu": "Zhipu AI", "siliconflow": "SiliconFlow"}[c.name]
		p := nanobotIdentity(c.name, label, "openai-compatible", c.base)
		if p.ID != c.wantID || p.Kind != c.wantKind {
			t.Errorf("%s @ %s → %s/%s, want %s/%s", c.name, c.base, p.ID, p.Kind, c.wantID, c.wantKind)
		}
	}
}
