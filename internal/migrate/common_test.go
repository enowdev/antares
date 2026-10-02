package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestJSON5ToJSON(t *testing.T) {
	src := `// top comment
{
  // keys need no quotes
  models: {
    providers: {
      'my-proxy': { baseUrl: "http://x/v1", apiKey: '${MY_KEY}', },
    },
  },
  /* block
     comment */
  n: 0x1F, f: .5, g: 5., p: +3, inf: Infinity,
  s: 'it\'s "quoted"',
  list: [1, 2, 3,],
  url: "http://a//b", // not a comment inside a string
}`
	out, err := JSON5ToJSON([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	want := map[string]any{
		"models": map[string]any{"providers": map[string]any{"my-proxy": map[string]any{"baseUrl": "http://x/v1", "apiKey": "${MY_KEY}"}}},
		"n":      31.0, "f": 0.5, "g": 5.0, "p": 3.0, "inf": nil,
		"s":    `it's "quoted"`,
		"list": []any{1.0, 2.0, 3.0},
		"url":  "http://a//b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestReadDotEnvAndExpand(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	os.WriteFile(p, []byte("# c\nexport A=1\nB=\"two words\"\nC='x' \nD=plain # note\n\nbad line\n"), 0o600)
	env := ReadDotEnv(p)
	want := map[string]string{"A": "1", "B": "two words", "C": "x", "D": "plain"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("got %#v", env)
	}
	if v, ok := ExpandVars("${A}-$B", env); !ok || v != "1-two words" {
		t.Fatalf("expand = %q %v", v, ok)
	}
	if _, ok := ExpandVars("${MIGRATE_SURELY_UNSET_VAR}", env); ok {
		t.Fatal("unset var should report !ok")
	}
}

func TestSplitMemory(t *testing.T) {
	h := SplitMemory("first fact\n§\nsecond\nline\n§\n\n§\n", HermesMemorySeparator)
	if len(h) != 2 || h[1].Content != "second\nline" {
		t.Fatalf("hermes split: %#v", h)
	}
	md := `---
title: x
---
# People
- Alice likes tea
  and biscuits
- Bob: 1. not a list

<!-- hidden -->
## Work
A plain paragraph
over two lines.

1. numbered item
`
	got := SplitMemory(md, "")
	want := []MemoryEntry{
		{Content: "Alice likes tea\n  and biscuits", Heading: "People"},
		{Content: "Bob: 1. not a list", Heading: "People"},
		{Content: "A plain paragraph\nover two lines.", Heading: "Work"},
		{Content: "numbered item", Heading: "Work"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestSplitMemoryLeadsAndNoise(t *testing.T) {
	md := "# Rules\nKey rules:\n- never push to main\n- run tests first\n\nStartup order sebelum kerja:\n\n1. pull\n2. build\n\n" +
		"Example config:\n```yaml\na: 1\n\nb: 2\n```\n\n比如：\n\n```markdown\n```\n\nSSH 主机和别名\n\n" +
		"- note with code:\n  ```sh\n  make build\n  ```\n- 用户喜欢简短的回答\n\nLonely label:\n## Next\nA fact about the next section.\n"
	got := SplitMemory(md, "")
	want := []MemoryEntry{
		{Content: "Key rules:\n- never push to main\n- run tests first", Heading: "Rules"},
		{Content: "Startup order sebelum kerja:\n- pull\n- build", Heading: "Rules"},
		{Content: "Example config:\n```yaml\na: 1\n\nb: 2\n```", Heading: "Rules"},
		{Content: "note with code:\n  ```sh\n  make build\n  ```", Heading: "Rules"},
		{Content: "用户喜欢简短的回答", Heading: "Rules"},
		{Content: "A fact about the next section.", Heading: "Next"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestScanSkills(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	mk("research/web-research/SKILL.md", "---\nname: web-research\ndescription: Search well\n---\nbody")
	mk("research/web-research/scripts/run.sh", "echo")
	mk("plain/SKILL.md", "no front matter")
	mk(".hidden/x/SKILL.md", "x")
	mk("a/b/c/deep/SKILL.md", "too deep")
	got := ScanSkills(root, 2)
	if len(got) != 2 || got[0].Name != "plain" || got[1].Name != "web-research" || got[1].Category != "research" || got[1].Description != "Search well" {
		t.Fatalf("got %#v", got)
	}
}

func TestVendorAndCustomID(t *testing.T) {
	if v, ok := Vendor("Google"); !ok || v.ID != "gemini" || v.Kind != "gemini" {
		t.Fatalf("google → %#v", v)
	}
	if v, ok := Vendor("github_copilot"); !ok || !v.OAuth {
		t.Fatalf("copilot → %#v", v)
	}
	if v, ok := VendorByBaseURL("https://openrouter.ai/api/v1"); !ok || v.ID != "openrouter" {
		t.Fatalf("by url → %#v", v)
	}
	taken := map[string]bool{"my-proxy": true}
	if id := CustomProviderID(func(s string) bool { return taken[s] }, "My Proxy"); id != "my-proxy-2" {
		t.Fatalf("custom id = %s", id)
	}
	if id := CustomProviderID(func(string) bool { return false }, "OpenAI"); id != "openai-2" {
		t.Fatalf("catalogue clash = %s", id)
	}
	if id := CustomProviderID(func(string) bool { return false }, "Custom"); id != "custom-provider" {
		t.Fatalf("custom = %s", id)
	}
}

func TestEverySchedule(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Minute: "@every 30m", 2 * time.Hour: "@every 2h", 90 * time.Minute: "@every 1h30m",
	} {
		if got := EverySchedule(d); got != want {
			t.Fatalf("%v → %q want %q", d, got, want)
		}
		if err := ValidSchedule(EverySchedule(d)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildersStatus(t *testing.T) {
	env := FakeEnv{Providers: map[string]bool{"openrouter": true, "anthropic": true}, SameProvider: map[string]bool{"anthropic": true},
		Skills: map[string]bool{"x": true}, Channels: map[string]bool{"telegram": true}, Text: map[Category]string{CatSoul: "custom"}, RAGOff: true}
	if it := ProviderItem(env, ProviderPayload{ID: "openrouter", APIKey: "k"}, ""); it.Status != StatusConflict || it.Selected {
		t.Fatalf("provider conflict: %#v", it)
	}
	if it := ProviderItem(env, ProviderPayload{ID: "anthropic", APIKey: "k"}, ""); it.Status != StatusReady {
		t.Fatalf("identical provider should be ready: %#v", it)
	}
	if it := ProviderItem(env, ProviderPayload{ID: "groq"}, ""); it.Status != StatusNeedsInput || it.Input != "api_key" {
		t.Fatalf("keyless provider: %#v", it)
	}
	if it := ProviderItem(env, ProviderPayload{ID: "ollama", BaseURL: "http://127.0.0.1:11434/v1"}, ""); it.Status != StatusReady {
		t.Fatalf("local provider: %#v", it)
	}
	if it, _ := TextItem(env, CatSoul, "hi", "SOUL.md"); it.Status != StatusConflict {
		t.Fatalf("soul: %#v", it)
	}
	if it, _ := KnowledgeItem(env, "memory/2026-01-01.md", "x"); it.Status != StatusUnsupported {
		t.Fatalf("knowledge: %#v", it)
	}
	if it := ChannelItem(env, "telegram", "", nil, ""); it.Status != StatusConflict {
		t.Fatalf("channel: %#v", it)
	}
	if it := ChannelItem(env, "dingtalk", "", nil, ""); it.Status != StatusUnsupported {
		t.Fatalf("dingtalk: %#v", it)
	}
	if it := CronItem("a", CronPayload{Name: "a", Schedule: "nonsense", Prompt: "p"}, true); it.Status != StatusUnsupported {
		t.Fatalf("cron: %#v", it)
	}
	if it := CronItem("a", CronPayload{Name: "a", Schedule: "0 9 * * *", Prompt: "p", Enabled: true}, true); it.Status != StatusReady || it.Payload.Cron.Enabled {
		t.Fatalf("cron must be disabled: %#v", it)
	}
	if it := MCPItem(env, MCPPayload{Name: "m", Transport: "sse", URL: "http://x"}); it.Payload.MCP.Transport != "http" {
		t.Fatalf("mcp transport: %#v", it)
	}
	mem := MemoryItems([]MemoryEntry{{Content: "A"}, {Content: "a"}, {Content: "b", Heading: "Work Stuff"}}, "")
	if len(mem) != 2 || mem[1].Payload.Memory.Tags[0] != "work-stuff" {
		t.Fatalf("memory dedupe: %#v", mem)
	}
	if s := Summarize(append(mem, ProviderItem(env, ProviderPayload{ID: "groq"}, ""), ChannelItem(nil, "telegram", "", nil, ""))); s != "1 provider · 2 memories · Telegram" {
		t.Fatalf("summary = %q", s)
	}
}

func TestSecretMemoryIsMaskedAndDeselected(t *testing.T) {
	items := MemoryItems([]MemoryEntry{
		{Content: "Bot Token: " + "MTIzNDU2Nzg5MDEyMzQ1Njc4OQ.Gfake" + "x.fakefakefakefakefakefakefake12"}, // split so secret scanners skip this fake
		{Content: "Client Secret: fakeClientSecret123456"},
		{Content: "OpenAI key sk-abcdefghijklmnop1234"},
		{Content: "Prefers short answers in Indonesian"},
	}, "global")
	if len(items) != 4 {
		t.Fatalf("got %d items", len(items))
	}
	for _, it := range items[:3] {
		if it.Selected || !it.Secret || it.Reason == "" {
			t.Errorf("%q should be masked, secret and deselected: %+v", it.Title, it)
		}
		for _, leak := range []string{"MTIzNDU2", "fakeClientSecret", "sk-abcdef"} {
			if strings.Contains(it.Title, leak) {
				t.Errorf("title leaks a secret: %q", it.Title)
			}
		}
		if it.Payload.Memory == nil || !LooksSecret(it.Payload.Memory.Content) {
			t.Errorf("payload must keep the original content for an opted-in import")
		}
	}
	if !items[3].Selected || items[3].Secret {
		t.Errorf("ordinary memory should stay selected: %+v", items[3])
	}
}
