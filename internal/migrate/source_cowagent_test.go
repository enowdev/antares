package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var cowagentSecrets = []string{
	"sk-ant-FAKE-cow-claude-0001", "sk-FAKE-cow-deepseek-0002", "FAKE-cow-zhipu-key-0003",
	"sk-FAKE-cow-gateway-0004", "sk-FAKE-cow-openai-env-0005", "424242:FAKE-cow-telegram-token",
}

func cowagentEnv(t *testing.T) string {
	t.Helper()
	home := nanobotFixtureHome(t, "cowagent", "")
	t.Setenv("COW_DATA_DIR", "")
	return home
}

func TestCowagentDetect(t *testing.T) {
	home := cowagentEnv(t)
	det, err := cowagentSource{}.Detect(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if det.Root != filepath.Join(home, ".cow") || det.Running || det.Summary == "" {
		t.Fatalf("detection %+v", det)
	}
	// A bare workspace (no desktop data dir) is found too.
	bare := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bare, "cow"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "cow", "AGENT.md"), []byte("# AGENT.md\n\nI am Cowie.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nanobotFixtureHome(t, "cowagent", bare)
	det, err = cowagentSource{}.Detect(context.Background(), "")
	if err != nil || det.Root != filepath.Join(bare, "cow") {
		t.Fatalf("bare workspace: %+v %v", det, err)
	}
	p, _ := cowagentSource{}.Plan(context.Background(), det, FakeEnv{})
	nanobotItem(t, p, "soul:file")
}

func TestCowagentPlanGolden(t *testing.T) {
	home := cowagentEnv(t)
	p := nanobotPlanFor(t, cowagentSource{}, FakeEnv{})
	CheckGolden(t, "../cowagent/plan.golden", nanobotRender(t, p, home, cowagentSecrets...))

	m := nanobotItem(t, p, "model:default").Payload.Model
	if m.Provider != "anthropic" || m.Model != "claude-sonnet-4-6" {
		t.Fatalf("model %+v", m)
	}
	if k := nanobotItem(t, p, "provider:openai").Payload.Provider.APIKey; k != "sk-FAKE-cow-openai-env-0005" {
		t.Fatal("OpenAI key from ~/.cow/.env not used")
	}
	gw := nanobotItem(t, p, "provider:my-gateway").Payload.Provider
	if gw.BaseURL != "https://gw.example.com/v1" || strings.Join(gw.Models, ",") != "gw-large" {
		t.Fatalf("custom provider %+v", gw)
	}
	nanobotNoItem(t, p, "user_md:file")                 // unchanged template
	nanobotNoItem(t, p, "mcp:ignored")                  // mcp.json shadows config mcp_servers
	nanobotNoItem(t, p, "knowledge:knowledge/index.md") // empty
	nanobotWantStatus(t, p, map[string]Status{
		"provider:anthropic": StatusReady, "provider:deepseek": StatusReady, "provider:zhipu-ai": StatusReady,
		"provider:baidu-wenxin-legacy": StatusUnsupported,
		"soul:file":                    StatusReady, "agents_md:file": StatusReady,
		"knowledge:memory/2026-09-30.md": StatusReady, "knowledge:knowledge/projects/antares.md": StatusReady,
		"skill:bocha-search": StatusReady, "mcp:fetch": StatusReady, "mcp:web": StatusReady,
		"cron:t1": StatusReady, "cron:t2": StatusReady, "cron:t3": StatusUnsupported, "cron:t4": StatusUnsupported,
		"role:research-report": StatusReady,
		"channel:telegram":     StatusReady, "channel:feishu": StatusNeedsInput,
		"channel:dingtalk": StatusUnsupported, "channel:wechat": StatusUnsupported,
	})
	if c := nanobotItem(t, p, "cron:t2").Payload.Cron; c.Schedule != "@every 1h" || !strings.Contains(c.Prompt, "Time to stretch!") {
		t.Fatalf("interval task %+v", c)
	}
	var mem []string
	for _, it := range p.Items {
		if it.Category == CatMemory {
			mem = append(mem, it.Payload.Memory.Content)
		}
	}
	if strings.Join(mem, "|") != "2026-09-01: Eno switched to a standing desk.|Eno's sister is called Rina." {
		t.Fatalf("memory %q", mem)
	}
	if r := nanobotItem(t, p, "role:research-report").Payload.Role; strings.Contains(r.Prompt, "description:") || r.Summary == "" {
		t.Fatalf("subagent role %+v", r)
	}
}

func TestCowagentConflicts(t *testing.T) {
	cowagentEnv(t)
	env := FakeEnv{Providers: map[string]bool{"anthropic": true}, Roles: map[string]bool{"research-report": true},
		Channels: map[string]bool{"telegram": true}, Text: map[Category]string{CatSoul: "custom", CatAgentsMD: "custom"},
		DefaultModel: true, MCP: map[string]bool{"fetch": true}}
	p := nanobotPlanFor(t, cowagentSource{}, env)
	nanobotWantStatus(t, p, map[string]Status{
		"provider:anthropic": StatusConflict, "model:default": StatusConflict, "role:research-report": StatusConflict,
		"channel:telegram": StatusConflict, "soul:file": StatusConflict, "agents_md:file": StatusConflict, "mcp:fetch": StatusConflict,
	})
}

func TestCowagentBotForModel(t *testing.T) {
	for model, want := range map[string]string{
		"claude-opus-5": "claudeAPI", "qwen3-max": "dashscope", "glm-5": "zhipu", "kimi-k2": "moonshot",
		"doubao-seed-2-0-pro-260215": "doubao", "deepseek-flash": "deepseek", "MiniMax-M3": "minimax",
		"mimo-v2": "mimo", "ernie-5": "qianfan", "gpt-5": "openai",
	} {
		if got := cowagentBotForModel(model); got != want {
			t.Errorf("%s → %s, want %s", model, got, want)
		}
	}
}
