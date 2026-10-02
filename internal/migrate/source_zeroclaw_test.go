package migrate

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestZeroclawParseTOML(t *testing.T) {
	src := `# top
api_key = "sk-\"x\"" # trailing
n = 1_000
f = 0.7
on = true
when = 1979-05-27T07:32:00Z
list = [ "a", 'b',
  "c", # c
]
inline = { a = 1, "b.c" = "d" }
dotted.key = "v"
lit = '''
raw \n'''
multi = """
one \
  two"""

[channels_config.telegram]
bot_token = "123:abc"

[[agents]]
name = "x"
[[agents]]
name = "y"
[agents.sub]
k = "z"
`
	m, err := zeroclawParseTOML(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"api_key": `sk-"x"`, "n": 1000, "f": 0.7, "on": true,
		"when":            "1979-05-27T07:32:00Z",
		"list":            []any{"a", "b", "c"},
		"inline":          map[string]any{"a": 1, "b.c": "d"},
		"dotted":          map[string]any{"key": "v"},
		"lit":             `raw \n`,
		"multi":           "one two",
		"channels_config": map[string]any{"telegram": map[string]any{"bot_token": "123:abc"}},
		"agents": []any{
			map[string]any{"name": "x"},
			map[string]any{"name": "y", "sub": map[string]any{"k": "z"}},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("got %#v", m)
	}
	if _, err := zeroclawParseTOML("a = \"unterminated"); err == nil {
		t.Fatal("want error")
	}
}

// ---- fixture helpers shared by the letta/zeroclaw/nanoclaw/openhuman tests ----

// zeroclawCopyFixture copies testdata/<rel> into a temp dir (so SQLite files
// can be built next to it) and returns the copy's path.
func zeroclawCopyFixture(t *testing.T, rel string) string {
	t.Helper()
	src := filepath.Join("testdata", rel)
	dst := filepath.Join(t.TempDir(), filepath.Base(rel))
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, _ := filepath.Rel(src, path)
		out := filepath.Join(dst, r)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// zeroclawBuildDB creates path from the SQL script testdata/<sqlRel>.
func zeroclawBuildDB(t *testing.T, path, sqlRel string) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("testdata", sqlRel))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(string(script)); err != nil {
		t.Fatalf("%s: %v", sqlRel, err)
	}
}

// zeroclawRel strips the fixture root from rendered output.
func zeroclawRel(s, root string) string { return strings.ReplaceAll(s, root, "<root>") }

func zeroclawItems(p Plan) map[string]Item {
	m := map[string]Item{}
	for _, it := range p.Items {
		m[it.ID] = it
	}
	return m
}

var zeroclawFixtureSecrets = []string{
	"sk-or-v1-FAKEzcOPENROUTER0001", "sk-ant-FAKEzcANTHROPIC0002", "777000:FAKEzcTELEGRAM0003",
	"FAKEzcLARKSECRET0004", "ghp_FAKEzcGITHUB0005", "FAKEzcDINGTALK0006", "sk-FAKEzcDEEPSEEK0007",
	"gsk_FAKEzcGROQ0008", "FAKEzcDISCORD0009",
}

func zeroclawFixture(t *testing.T) string {
	t.Helper()
	noRunning(t)
	oldPID, oldNow := zeroclawPIDAlive, zeroclawNow
	zeroclawPIDAlive = func(pid int) bool { return pid == 4242 }
	zeroclawNow = func() time.Time { return time.Date(2026, 10, 3, 10, 0, 20, 0, time.UTC) }
	t.Cleanup(func() { zeroclawPIDAlive, zeroclawNow = oldPID, oldNow })
	root := zeroclawCopyFixture(t, "zeroclaw/v3")
	zeroclawBuildDB(t, filepath.Join(root, "data", "memory", "brain.db"), "zeroclaw/v3.sql")
	zeroclawBuildDB(t, filepath.Join(root, "data", "cron", "jobs.db"), "zeroclaw/v3_cron.sql")
	return root
}

func TestZeroclawDetect(t *testing.T) {
	root := zeroclawFixture(t)
	d, err := zeroclawSource{}.Detect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	out := zeroclawRel(d.Source+" root="+d.Root+" version="+d.Version+" running="+strconv.FormatBool(d.Running)+"\nsummary: "+d.Summary+"\n", root)
	CheckGolden(t, "../zeroclaw/detect.golden", out)
	if _, err := (zeroclawSource{}).Detect(context.Background(), t.TempDir()); err == nil {
		t.Fatal("empty dir should not be detected")
	}
	// A stale daemon_state.json means not running.
	zeroclawNow = func() time.Time { return time.Date(2026, 10, 3, 11, 0, 0, 0, time.UTC) }
	if d, _ := (zeroclawSource{}).Detect(context.Background(), root); d.Running {
		t.Fatal("stale daemon state should not count as running")
	}
}

func TestZeroclawPlan(t *testing.T) {
	root := zeroclawFixture(t)
	env := FakeEnv{Providers: map[string]bool{"anthropic": true}, Skills: map[string]bool{"git-helper": true},
		Channels: map[string]bool{"feishu": true}, Text: map[Category]string{CatSoul: "custom", CatUserMD: "default"}}
	plan, err := BuildPlan(context.Background(), "zeroclaw", root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	out := zeroclawRel(RenderPlan(plan), root)
	AssertNoSecrets(t, "rendered plan", out, zeroclawFixtureSecrets...)
	CheckGolden(t, "../zeroclaw/plan_v3.golden", out)

	items := zeroclawItems(plan)
	if p := items["provider:openrouter"].Payload.Provider; p == nil || p.APIKey != "sk-or-v1-FAKEzcOPENROUTER0001" {
		t.Fatalf("enc2 key not decrypted: %#v", p)
	}
	if p := items["provider:anthropic"].Payload.Provider; p == nil || p.APIKey != "sk-ant-FAKEzcANTHROPIC0002" {
		t.Fatalf("enc key not decrypted: %#v", p)
	}
	if it := items["provider:lab"]; it.Status != StatusNeedsInput {
		t.Fatalf("1Password key should need input: %#v", it)
	}
	if it := items["provider:anthropic"]; it.Status != StatusConflict {
		t.Fatalf("existing provider should conflict: %s", it.Status)
	}
	if m := items["model:default"].Payload.Model; m == nil || m.Provider != "openrouter" || len(m.Fallback) != 1 || m.Fallback[0] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("model: %#v", m)
	}
	if c := items["channel:telegram"].Payload.Channel; c == nil || c.Fields["bot_token"] != "777000:FAKEzcTELEGRAM0003" {
		t.Fatalf("telegram: %#v", c)
	}
	if e := items["mcp:github"].Payload.MCP; e == nil || e.Env["GITHUB_TOKEN"] != "ghp_FAKEzcGITHUB0005" {
		t.Fatalf("mcp env: %#v", e)
	}
	for id, want := range map[string]Status{
		"channel:whatsapp": StatusUnsupported, "channel:dingtalk": StatusUnsupported,
		"channel:lark-feishu": StatusConflict, "cron:j-dentist": StatusUnsupported, "cron:backup": StatusUnsupported,
		"cron:morning": StatusReady, "cron:j-water": StatusReady, "skill:legacy-tool": StatusUnsupported,
		"skill:git-helper": StatusConflict, "soul:file": StatusConflict, "role:coder": StatusReady,
	} {
		if got := items[id].Status; got != want {
			t.Errorf("%s: status %q, want %q", id, got, want)
		}
	}
	if c := items["cron:j-water"].Payload.Cron; c == nil || c.Schedule != "@every 30m" || c.Enabled {
		t.Fatalf("interval job: %#v", c)
	}
	for _, it := range plan.Items {
		if it.Category == CatMemory && strings.Contains(it.Payload.Memory.Content, "VS Code") {
			t.Fatal("superseded memory imported")
		}
		if it.Category == CatMemory && strings.Contains(it.Payload.Memory.Content, "Coder agent") {
			t.Fatal("another agent's memory imported")
		}
	}
}

func TestZeroclawPlanV1(t *testing.T) {
	noRunning(t)
	root, _ := filepath.Abs(filepath.Join("testdata", "zeroclaw", "v1"))
	plan, err := BuildPlan(context.Background(), "zeroclaw", root, "", FakeEnv{Channels: map[string]bool{"discord": true}})
	if err != nil {
		t.Fatal(err)
	}
	out := zeroclawRel(RenderPlan(plan), root)
	AssertNoSecrets(t, "rendered plan", out, zeroclawFixtureSecrets...)
	CheckGolden(t, "../zeroclaw/plan_v1.golden", out)
	items := zeroclawItems(plan)
	if p := items["provider:deepseek"].Payload.Provider; p == nil || p.APIKey != "sk-FAKEzcDEEPSEEK0007" {
		t.Fatalf("deepseek: %#v", p)
	}
	if m := items["model:default"].Payload.Model; m == nil || m.Model != "deepseek-chat" || len(m.Fallback) != 1 || m.Fallback[0] != "groq/llama-3.3-70b" {
		t.Fatalf("model: %#v", m)
	}
	if it := items["channel:discord"]; it.Status != StatusConflict {
		t.Fatalf("discord should conflict: %#v", it)
	}
}

func TestZeroclawWrongKey(t *testing.T) {
	root := zeroclawFixture(t)
	if err := os.WriteFile(filepath.Join(root, ".secret_key"), []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(context.Background(), "zeroclaw", root, "", FakeEnv{})
	if err != nil {
		t.Fatal(err)
	}
	items := zeroclawItems(plan)
	if it := items["provider:openrouter"]; it.Status != StatusNeedsInput || it.Input != "api_key" {
		t.Fatalf("undecryptable key should need input: %#v", it)
	}
	if it := items["channel:telegram"]; it.Status != StatusNeedsInput {
		t.Fatalf("undecryptable token should need input: %#v", it)
	}
}

// zeroclawFakeKeychain is an in-memory Keychain keyed by "service/account";
// it records every lookup.
type zeroclawFakeKeychain struct {
	entries map[string]string
	asked   []string
}

func (k *zeroclawFakeKeychain) Get(service, account string) (string, error) {
	k.asked = append(k.asked, service+"/"+account)
	if v, ok := k.entries[service+"/"+account]; ok {
		return v, nil
	}
	return "", ErrNoSecret
}

// zeroclawUseKeychain installs a fake keychain for the test.
func zeroclawUseKeychain(t *testing.T, entries map[string]string) *zeroclawFakeKeychain {
	t.Helper()
	k := &zeroclawFakeKeychain{entries: entries}
	old := DefaultKeychain
	DefaultKeychain = k
	t.Cleanup(func() { DefaultKeychain = old })
	return k
}
