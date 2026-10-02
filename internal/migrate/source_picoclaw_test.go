package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var picoclawSecrets = []string{
	"sk-ant-FAKE-pico-encrypted-0001", "sk-FAKE-pico-deepseek-0002", "glm-FAKE-pico-file-key-0003",
	"7777:FAKE-pico-telegram-token", "xoxb-FAKE-pico-slack-token", "ghp_FAKEpicoclawGithubToken01",
}

// picoclawEnv isolates the PicoClaw env vars; passphrase "" means unset.
func picoclawEnv(t *testing.T, passphrase string) string {
	t.Helper()
	home := nanobotFixtureHome(t, "picoclaw", "")
	t.Setenv("PICOCLAW_HOME", "")
	t.Setenv("PICOCLAW_CONFIG", "")
	t.Setenv("PICOCLAW_KEY_PASSPHRASE", passphrase)
	if old, ok := os.LookupEnv("PICOCLAW_SSH_KEY_PATH"); ok {
		os.Unsetenv("PICOCLAW_SSH_KEY_PATH")
		t.Cleanup(func() { os.Setenv("PICOCLAW_SSH_KEY_PATH", old) })
	}
	return home
}

func TestPicoclawDetect(t *testing.T) {
	home := picoclawEnv(t, "")
	det, err := picoclawSource{}.Detect(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if det.Root != filepath.Join(home, ".picoclaw") || det.Version != "0.2.9" || det.Running {
		t.Fatalf("detection %+v", det)
	}
	if det.Summary == "" || det.Summary == "nothing to import" {
		t.Fatalf("summary %q", det.Summary)
	}
}

func TestPicoclawPlanGolden(t *testing.T) {
	home := picoclawEnv(t, "test-passphrase")
	p := nanobotPlanFor(t, picoclawSource{}, FakeEnv{})
	CheckGolden(t, "../picoclaw/plan.golden", nanobotRender(t, p, home, picoclawSecrets...))

	// enc:// key decrypted (fixture encrypted with Python's cryptography AES-GCM/HKDF).
	if k := nanobotItem(t, p, "provider:anthropic").Payload.Provider.APIKey; k != "sk-ant-FAKE-pico-encrypted-0001" {
		t.Fatal("enc:// key not decrypted")
	}
	// file:// key read relative to the config dir; zhipu's own host → custom provider.
	glm := nanobotItem(t, p, "provider:zhipu-ai").Payload.Provider
	if glm.APIKey != "glm-FAKE-pico-file-key-0003" || glm.BaseURL != "https://open.bigmodel.cn/api/paas/v4" {
		t.Fatalf("glm provider %+v", glm)
	}
	m := nanobotItem(t, p, "model:default").Payload.Model
	if m.Provider != "anthropic" || m.Model != "claude-sonnet-4.6" || strings.Join(m.Fallback, ",") != "deepseek/deepseek-chat" {
		t.Fatalf("model %+v", m)
	}
	nanobotNoItem(t, p, "provider:openai")  // shipped example without a key
	nanobotNoItem(t, p, "soul:file")        // unchanged template
	nanobotNoItem(t, p, "channel:discord")  // disabled, no token
	nanobotNoItem(t, p, "channel:whatsapp") // disabled
	nanobotWantStatus(t, p, map[string]Status{
		"provider:deepseek": StatusReady, "provider:ollama": StatusReady, "provider:github-copilot": StatusUnsupported,
		"agents_md:file": StatusReady, "user_md:file": StatusReady,
		"knowledge:memory/202609/20260915.md": StatusReady, "skill:discord": StatusReady,
		"mcp:github": StatusReady, "mcp:docs": StatusReady,
		"cron:c1": StatusReady, "cron:c2": StatusUnsupported, "cron:c3": StatusUnsupported, "cron:heartbeat": StatusReady,
		"role:researcher":  StatusReady,
		"channel:telegram": StatusReady, "channel:slack": StatusNeedsInput, "channel:qq": StatusUnsupported,
	})
	if a := nanobotItem(t, p, "agents_md:file").Payload.Text.Content; strings.Contains(a, "name: pico") || !strings.HasPrefix(a, "You are Pico.") {
		t.Fatalf("AGENT.md front matter not stripped: %q", a)
	}
	if c := nanobotItem(t, p, "cron:heartbeat").Payload.Cron; c.Schedule != "@every 15m" || !strings.Contains(c.Prompt, "disk space") {
		t.Fatalf("heartbeat %+v", c)
	}
	if r := nanobotItem(t, p, "role:researcher").Payload.Role; r.Model != "deepseek/deepseek-chat" || !strings.Contains(r.Prompt, "research topics") {
		t.Fatalf("role %+v", r)
	}
	var mem []string
	for _, it := range p.Items {
		if it.Category == CatMemory {
			mem = append(mem, it.Payload.Memory.Content)
		}
	}
	if strings.Join(mem, "|") != "Eno maintains the antares repo." {
		t.Fatalf("memory %q", mem)
	}
}

func TestPicoclawEncryptedKeyWithoutPassphrase(t *testing.T) {
	picoclawEnv(t, "")
	p := nanobotPlanFor(t, picoclawSource{}, FakeEnv{})
	it := nanobotItem(t, p, "provider:anthropic")
	if it.Status != StatusNeedsInput || it.Input != "api_key" || !strings.Contains(it.Reason, "PICOCLAW_KEY_PASSPHRASE") {
		t.Fatalf("anthropic %+v", it)
	}
	t.Setenv("PICOCLAW_KEY_PASSPHRASE", "wrong")
	p = nanobotPlanFor(t, picoclawSource{}, FakeEnv{})
	if it := nanobotItem(t, p, "provider:anthropic"); it.Status != StatusNeedsInput || !strings.Contains(it.Reason, "wrong passphrase") {
		t.Fatalf("wrong passphrase: %+v", it)
	}
}

func TestPicoclawDetectDoesNotDecrypt(t *testing.T) {
	picoclawEnv(t, "test-passphrase")
	items, _ := picoclawBuild(Detection{Root: filepath.Join(HomeDir(), ".picoclaw")}, nil, false)
	for _, it := range items {
		if it.Payload.Provider != nil && it.Payload.Provider.APIKey == "sk-ant-FAKE-pico-encrypted-0001" {
			t.Fatal("Detect-mode build decrypted a key")
		}
	}
}

func TestPicoclawConflicts(t *testing.T) {
	picoclawEnv(t, "test-passphrase")
	env := FakeEnv{Providers: map[string]bool{"deepseek": true}, Skills: map[string]bool{"discord": true},
		MCP: map[string]bool{"github": true}, Roles: map[string]bool{"researcher": true},
		Channels: map[string]bool{"telegram": true}, Text: map[Category]string{CatUserMD: "custom"}, RAGOff: true}
	p := nanobotPlanFor(t, picoclawSource{}, env)
	nanobotWantStatus(t, p, map[string]Status{
		"provider:deepseek": StatusConflict, "skill:discord": StatusConflict, "mcp:github": StatusConflict,
		"role:researcher": StatusConflict, "channel:telegram": StatusConflict, "user_md:file": StatusConflict,
		"knowledge:memory/202609/20260915.md": StatusUnsupported,
	})
}

func TestPicoclawDecryptRoundTrip(t *testing.T) {
	ssh := []byte("key bytes")
	salt := make([]byte, 16)
	key, err := picoclawDeriveKey("pw", ssh, salt)
	if err != nil || len(key) != 32 {
		t.Fatalf("derive: %v", err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "k")
	if err := os.WriteFile(keyPath, ssh, 0o600); err != nil {
		t.Fatal(err)
	}
	// Fixture value produced by Python (cryptography HKDF + AESGCM).
	raw := strings.TrimSpace(readSecurityKey(t))
	if got, err := picoclawDecrypt(raw, "test-passphrase", filepath.Join("testdata", "picoclaw", "home", ".ssh", "picoclaw_ed25519.key")); err != nil || got != "sk-ant-FAKE-pico-encrypted-0001" {
		t.Fatalf("decrypt fixture: %v", err)
	}
	if _, err := picoclawDecrypt("enc://!!!", "pw", keyPath); err == nil {
		t.Fatal("bad base64 accepted")
	}
}

func readSecurityKey(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "picoclaw", "home", ".picoclaw", ".security.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if i := strings.Index(l, "enc://"); i >= 0 {
			return strings.Trim(l[i:], "\" ")
		}
	}
	t.Fatal("no enc:// value in fixture")
	return ""
}
