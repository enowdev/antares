package migrate

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
)

var openhumanFixtureSecrets = []string{
	"sk-FAKEohOPENAI0001", "sk-ant-FAKEohANTHROPIC0002", "888111:FAKEohTELEGRAM0003", "FAKEohDISCORD0004",
	"FAKEohNOTION0005", "FAKEohEMAIL0006", "FAKEohLAB0007", "FAKEohCODEX0008",
}

func openhumanFixture(t *testing.T) string {
	t.Helper()
	noRunning(t)
	old := openhumanEnvWorkspace
	openhumanEnvWorkspace = func() string { return "" }
	t.Cleanup(func() { openhumanEnvWorkspace = old })
	root := zeroclawCopyFixture(t, "openhuman/home")
	zeroclawBuildDB(t, filepath.Join(root, ".openhuman", "users", "u_123", "workspace", "cron", "jobs.db"), "openhuman/cron.sql")
	return filepath.Join(root, ".openhuman")
}

func TestOpenhumanDetect(t *testing.T) {
	root := openhumanFixture(t)
	k := zeroclawUseKeychain(t, nil)
	dets, err := openhumanSource{}.DetectAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, d := range dets {
		b.WriteString(d.Source + " profile=" + d.Profile + " root=" + zeroclawRel(d.Root, root) + "\n  summary: " + d.Summary + "\n")
	}
	CheckGolden(t, "../openhuman/detect.golden", b.String())
	if len(k.asked) != 0 {
		t.Fatalf("Detect must not touch the keychain: %v", k.asked)
	}
	if _, err := (openhumanSource{}).Detect(context.Background(), t.TempDir()); err == nil {
		t.Fatal("empty dir should not be detected")
	}
}

func TestOpenhumanPlan(t *testing.T) {
	root := openhumanFixture(t)
	zeroclawUseKeychain(t, nil)
	env := FakeEnv{Channels: map[string]bool{"discord": true}, MCP: map[string]bool{"files": true}, RAGOff: false}
	plan, err := BuildPlan(context.Background(), "openhuman", root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	out := zeroclawRel(RenderPlan(plan), root)
	AssertNoSecrets(t, "rendered plan", out, openhumanFixtureSecrets...)
	CheckGolden(t, "../openhuman/plan.golden", out)

	items := zeroclawItems(plan)
	if p := items["provider:openai"].Payload.Provider; p == nil || p.APIKey != "sk-FAKEohOPENAI0001" {
		t.Fatalf("openai key from keyring file: %#v", p)
	}
	if p := items["provider:anthropic"].Payload.Provider; p == nil || p.APIKey != "sk-ant-FAKEohANTHROPIC0002" {
		t.Fatalf("enc2 profile token: %#v", p)
	}
	if p := items["provider:lab-vllm"].Payload.Provider; p == nil || p.APIKey != "FAKEohLAB0007" || p.BaseURL != "https://llm.lab.example/v1" {
		t.Fatalf("custom provider: %#v", p)
	}
	if m := items["model:default"].Payload.Model; m == nil || m.Provider != "openai" || m.Model != "gpt-4o" {
		t.Fatalf("model: %#v", m)
	}
	if c := items["channel:telegram"].Payload.Channel; c == nil || c.Fields["bot_token"] != "888111:FAKEohTELEGRAM0003" {
		t.Fatalf("telegram: %#v", c)
	}
	if m := items["mcp:notion"].Payload.MCP; m == nil || m.Headers["Authorization"] != "Bearer FAKEohNOTION0005" {
		t.Fatalf("mcp auth: %#v", m)
	}
	if _, ok := items["knowledge:episodic/s1/000001.md"]; ok {
		t.Fatal("episodic chat archive must not be imported")
	}
	if _, ok := items["knowledge:people/budi.md"]; !ok {
		t.Fatal("vault note missing")
	}
	for id, want := range map[string]Status{
		"provider:openhuman-account": StatusUnsupported, "provider:openai-codex": StatusUnsupported,
		"channel:email": StatusUnsupported, "channel:whatsapp": StatusUnsupported, "channel:discord": StatusConflict,
		"cron:c1": StatusReady, "cron:c2": StatusUnsupported, "cron:c3": StatusUnsupported, "mcp:files": StatusConflict,
	} {
		if got := items[id].Status; got != want {
			t.Errorf("%s: status %q, want %q", id, got, want)
		}
	}
	if it, ok := items["soul:file"]; !ok || strings.Contains(it.Payload.Text.Content, "Default OpenHuman identity") {
		t.Fatal("unedited seeded IDENTITY.md should be skipped, edited SOUL.md kept")
	}
}

// Shipped builds keep secrets in secrets.enc, keyed from the OS keychain.
func TestOpenhumanEncryptedKeyring(t *testing.T) {
	root := openhumanFixture(t)
	var m map[string]string
	if err := ReadJSON(filepath.Join(root, "dev-keychain.json"), &m); err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(m)
	fileKey := make([]byte, 32)
	for i := range fileKey {
		fileKey[i] = byte(200 + i)
	}
	aead, _ := chacha20poly1305.New(fileKey)
	nonce := make([]byte, 12)
	blob := append(append([]byte{}, nonce...), aead.Seal(nil, nonce, plain, nil)...)
	if err := os.WriteFile(filepath.Join(root, "secrets.enc"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "dev-keychain.json")); err != nil {
		t.Fatal(err)
	}
	zeroclawUseKeychain(t, map[string]string{"openhuman/app:master_key": hex.EncodeToString(fileKey)})
	plan, err := BuildPlan(context.Background(), "openhuman", root, "", FakeEnv{})
	if err != nil {
		t.Fatal(err)
	}
	items := zeroclawItems(plan)
	if p := items["provider:openai"].Payload.Provider; p == nil || p.APIKey != "sk-FAKEohOPENAI0001" {
		t.Fatalf("openai key via secrets.enc: %#v", p)
	}

	// Without the keychain entry the keys become needs_input.
	zeroclawUseKeychain(t, nil)
	plan, err = BuildPlan(context.Background(), "openhuman", root, "", FakeEnv{})
	if err != nil {
		t.Fatal(err)
	}
	items = zeroclawItems(plan)
	if it := items["provider:openai"]; it.Status != StatusNeedsInput {
		t.Fatalf("missing keyring should need input: %#v", it)
	}
	if it := items["channel:telegram"]; it.Status != StatusNeedsInput {
		t.Fatalf("missing master key should need input: %#v", it)
	}
}

func TestOpenhumanProfile(t *testing.T) {
	root := openhumanFixture(t)
	zeroclawUseKeychain(t, nil)
	plan, err := BuildPlan(context.Background(), "openhuman", root, "local", FakeEnv{})
	if err != nil {
		t.Fatal(err)
	}
	CheckGolden(t, "../openhuman/plan_local.golden", zeroclawRel(RenderPlan(plan), root))
}
