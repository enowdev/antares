package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var nanoclawFixtureSecrets = []string{
	"sk-ant-FAKEncANTHROPIC0001", "FAKEncOAUTH0002", "555:FAKEncTELEGRAM0003", "666:FAKEncTELEGRAMWORK0004",
	"FAKEncDISCORD0005", "xoxb-FAKEncSLACK0006", "ghp_FAKEncGITHUB0007",
}

func nanoclawFixture(t *testing.T, version string) string {
	t.Helper()
	noRunning(t)
	old := nanoclawPIDAlive
	nanoclawPIDAlive = func(int) bool { return false }
	t.Cleanup(func() { nanoclawPIDAlive = old })
	root := zeroclawCopyFixture(t, "nanoclaw/"+version)
	switch version {
	case "v1":
		zeroclawBuildDB(t, filepath.Join(root, "store", "messages.db"), "nanoclaw/v1.sql")
	case "v2":
		zeroclawBuildDB(t, filepath.Join(root, "data", "v2.db"), "nanoclaw/v2.sql")
		zeroclawBuildDB(t, filepath.Join(root, "data", "v2-sessions", "g-main", "s1", "inbound.db"), "nanoclaw/v2_inbound.sql")
	}
	return root
}

func TestNanoclawDetect(t *testing.T) {
	root := nanoclawFixture(t, "v1")
	var b strings.Builder
	for _, v := range []string{"v1", "v2"} {
		r := nanoclawFixture(t, v)
		d, err := nanoclawSource{}.Detect(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteString(v + ": version=" + d.Version + " summary=" + d.Summary + "\n")
	}
	CheckGolden(t, "../nanoclaw/detect.golden", b.String())

	// Default locations: found through the launchd plist's WorkingDirectory.
	home := t.TempDir()
	oldHome := HomeDir
	HomeDir = func() string { return home }
	t.Cleanup(func() { HomeDir = oldHome })
	plist := `<?xml version="1.0"?><plist><dict><key>Label</key><string>com.nanoclaw</string>
<key>WorkingDirectory</key><string>` + root + `</string></dict></plist>`
	if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "Library", "LaunchAgents", "com.nanoclaw.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := nanoclawSource{}.Detect(context.Background(), "")
	if err != nil || d.Root != root {
		t.Fatalf("plist lookup: %v %q", err, d.Root)
	}

	// Only ~/.config/nanoclaw: reported, with a hint, nothing importable.
	home2 := t.TempDir()
	HomeDir = func() string { return home2 }
	if _, err := (nanoclawSource{}).Detect(context.Background(), ""); err == nil {
		t.Fatal("nothing installed should not be detected")
	}
	if err := os.MkdirAll(filepath.Join(home2, ".config", "nanoclaw"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err = nanoclawSource{}.Detect(context.Background(), "")
	if err != nil || !strings.Contains(d.Summary, "--root") {
		t.Fatalf("config-only detection: %v %#v", err, d)
	}
	plan, err := nanoclawSource{}.Plan(context.Background(), d, FakeEnv{})
	if err != nil || len(plan.Items) != 0 || len(plan.Warnings) == 0 {
		t.Fatalf("config-only plan: %v %#v", err, plan)
	}
}

func TestNanoclawPlanV1(t *testing.T) {
	root := nanoclawFixture(t, "v1")
	env := FakeEnv{Text: map[Category]string{CatAgentsMD: "custom"}, Channels: map[string]bool{}}
	plan, err := BuildPlan(context.Background(), "nanoclaw", root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	out := zeroclawRel(RenderPlan(plan), root)
	AssertNoSecrets(t, "rendered plan", out, nanoclawFixtureSecrets...)
	CheckGolden(t, "../nanoclaw/plan_v1.golden", out)
	items := zeroclawItems(plan)
	if p := items["provider:anthropic"].Payload.Provider; p == nil || p.APIKey != "sk-ant-FAKEncANTHROPIC0001" {
		t.Fatalf("anthropic: %#v", p)
	}
	if c := items["channel:telegram"].Payload.Channel; c == nil || c.Fields["bot_token"] != "555:FAKEncTELEGRAM0003" {
		t.Fatalf("telegram: %#v", c)
	} else if chats, _ := c.Fields["allowed_chats"].([]string); len(chats) != 1 || chats[0] != "-100777" {
		t.Fatalf("telegram chats from registered groups: %#v", c.Fields)
	}
	if c := items["cron:task-2"].Payload.Cron; c == nil || c.Schedule != "@every 15m" || c.Timezone != "Asia/Jakarta" {
		t.Fatalf("interval task (ms): %#v", c)
	}
	for id, want := range map[string]Status{
		"channel:whatsapp": StatusUnsupported, "channel:telegram-work": StatusUnsupported, "cron:task-3": StatusUnsupported,
		"provider:claude-subscription": StatusUnsupported, "agents_md:file": StatusConflict, "skill:invoice-helper": StatusReady,
	} {
		if got := items[id].Status; got != want {
			t.Errorf("%s: status %q, want %q", id, got, want)
		}
	}
	if _, ok := items["skill:agent-browser"]; ok {
		t.Error("bundled container skill copies should be skipped")
	}
	if _, ok := items["cron:task-4"]; ok {
		t.Error("completed task imported")
	}
}

func TestNanoclawPlanV2(t *testing.T) {
	root := nanoclawFixture(t, "v2")
	plan, err := BuildPlan(context.Background(), "nanoclaw", root, "", FakeEnv{MCP: map[string]bool{"wiki": true}})
	if err != nil {
		t.Fatal(err)
	}
	out := zeroclawRel(RenderPlan(plan), root)
	AssertNoSecrets(t, "rendered plan", out, nanoclawFixtureSecrets...)
	CheckGolden(t, "../nanoclaw/plan_v2.golden", out)
	items := zeroclawItems(plan)
	if it := items["provider:anthropic"]; it.Status != StatusNeedsInput || !strings.Contains(it.Reason, "onecli") {
		t.Fatalf("gateway-held key should need input: %#v", it)
	}
	if m := items["model:default"].Payload.Model; m == nil || m.Model != "claude-opus-4-1" {
		t.Fatalf("model from container_configs: %#v", m)
	}
	if e := items["mcp:github"].Payload.MCP; e == nil || e.Env["GITHUB_TOKEN"] != "ghp_FAKEncGITHUB0007" {
		t.Fatalf("mcp: %#v", e)
	}
	for id, want := range map[string]Status{
		"cron:ser-a": StatusReady, "cron:t3": StatusUnsupported, "channel:slack": StatusNeedsInput,
		"channel:discord": StatusReady, "role:coder": StatusReady, "mcp:wiki": StatusConflict,
	} {
		if got := items[id].Status; got != want {
			t.Errorf("%s: status %q, want %q", id, got, want)
		}
	}
	for _, it := range plan.Items {
		if it.Payload.Text != nil && strings.Contains(it.Payload.Text.Content, "composed at spawn") {
			t.Error("composed CLAUDE.md must not be imported")
		}
	}
}
