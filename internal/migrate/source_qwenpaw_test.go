package migrate

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var qwenpawSecrets = []string{
	"sk-or-FAKE-qwenpaw-key-0001", "sk-FAKE-qwenpaw-deepseek-0002", "sk-FAKE-qwenpaw-proxy-0003",
	"8888:FAKE-qwenpaw-telegram", "FAKE-qwenpaw-tavily-01",
}

// qwenpawFakeKeychain answers Get from a map keyed "service/account" and
// records the lookups.
type qwenpawFakeKeychain struct {
	entries map[string]string
	calls   []string
}

func (k *qwenpawFakeKeychain) Get(service, account string) (string, error) {
	k.calls = append(k.calls, service+"/"+account)
	if v, ok := k.entries[service+"/"+account]; ok {
		return v, nil
	}
	return "", ErrNoSecret
}

func qwenpawMasterHex(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "qwenpaw", "master_key.hex"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func qwenpawSetup(t *testing.T, home string, kc Keychain) string {
	t.Helper()
	home = nanobotFixtureHome(t, "qwenpaw", home)
	for _, k := range []string{"QWENPAW_WORKING_DIR", "QWENPAW_SECRET_DIR", "QWENPAW_KEYRING_ACCOUNT"} {
		t.Setenv(k, "")
	}
	old := DefaultKeychain
	DefaultKeychain = kc
	t.Cleanup(func() { DefaultKeychain = old })
	return home
}

func TestQwenpawDetect(t *testing.T) {
	kc := &qwenpawFakeKeychain{}
	home := qwenpawSetup(t, "", kc)
	det, err := qwenpawSource{}.Detect(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if det.Root != filepath.Join(home, ".qwenpaw") || det.Summary == "" {
		t.Fatalf("detection %+v", det)
	}
	if len(kc.calls) != 0 {
		t.Fatalf("Detect read the keychain: %v", kc.calls)
	}
	// A legacy ~/.copaw install wins when present (constant.py WORKING_DIR).
	legacy := nanobotCopyTree(t, filepath.Join("testdata", "qwenpaw", "home"))
	if err := os.Rename(filepath.Join(legacy, ".qwenpaw"), filepath.Join(legacy, ".copaw")); err != nil {
		t.Fatal(err)
	}
	qwenpawSetup(t, legacy, kc)
	if det, err := (qwenpawSource{}).Detect(context.Background(), ""); err != nil || det.Root != filepath.Join(legacy, ".copaw") {
		t.Fatalf("copaw: %+v %v", det, err)
	}
}

func TestQwenpawPlanGoldenKeychain(t *testing.T) {
	kc := &qwenpawFakeKeychain{entries: map[string]string{"qwenpaw/master_key": qwenpawMasterHex(t)}}
	home := qwenpawSetup(t, "", kc)
	p := nanobotPlanFor(t, qwenpawSource{}, FakeEnv{})
	CheckGolden(t, "../qwenpaw/plan.golden", nanobotRender(t, p, home, qwenpawSecrets...))

	for id, want := range map[string]string{
		"provider:openrouter": "sk-or-FAKE-qwenpaw-key-0001", "provider:deepseek": "sk-FAKE-qwenpaw-deepseek-0002",
		"provider:my-proxy": "sk-FAKE-qwenpaw-proxy-0003",
	} {
		if got := nanobotItem(t, p, id).Payload.Provider.APIKey; got != want {
			t.Errorf("%s: key not decrypted", id)
		}
	}
	m := nanobotItem(t, p, "model:default").Payload.Model
	if m.Provider != "openrouter" || m.Model != "anthropic/claude-sonnet-4.6" || strings.Join(m.Fallback, ",") != "deepseek/deepseek-chat" {
		t.Fatalf("model %+v", m)
	}
	nanobotNoItem(t, p, "provider:anthropic")        // no key and not active (agent.json overrides active_model.json)
	nanobotNoItem(t, p, "agents_md:file")            // unchanged template
	nanobotNoItem(t, p, "channel:imessage")          // disabled
	nanobotNoItem(t, p, "skill:pdf")                 // skill.json: source "builtin"
	nanobotNoItem(t, p, "role:qwenpaw-qa-agent-0-2") // QwenPaw's own QA agent
	nanobotWantStatus(t, p, map[string]Status{
		"soul:file": StatusReady, "user_md:file": StatusReady, "knowledge:memory/2026-10-01.md": StatusReady,
		"skill:invoice-filler": StatusReady, "mcp:tavily": StatusReady,
		"cron:j1": StatusReady, "cron:j2": StatusReady, "cron:j3": StatusUnsupported, "cron:heartbeat": StatusReady,
		"role:coder": StatusReady, "channel:telegram": StatusReady, "channel:dingtalk": StatusUnsupported,
	})
	if s := nanobotItem(t, p, "soul:file").Payload.Text.Content; strings.Contains(s, "summary:") {
		t.Fatalf("front matter kept: %q", s)
	}
	if c := nanobotItem(t, p, "cron:j1").Payload.Cron; c.Prompt != "Review my week." || c.Timezone != "Asia/Jakarta" {
		t.Fatalf("job %+v", c)
	}
	if c := nanobotItem(t, p, "cron:heartbeat").Payload.Cron; c.Schedule != "@every 2h" || !strings.Contains(c.Prompt, "NAS is reachable") {
		t.Fatalf("heartbeat %+v", c)
	}
	if r := nanobotItem(t, p, "role:coder").Payload.Role; r.Model != "deepseek/deepseek-reasoner" || r.Title != "Coder" {
		t.Fatalf("role %+v", r)
	}
	var mem []string
	for _, it := range p.Items {
		if it.Category == CatMemory {
			mem = append(mem, it.Payload.Memory.Content)
		}
	}
	if strings.Join(mem, "|") != "Eno's NAS is at 192.168.1.20." {
		t.Fatalf("memory %q", mem)
	}
}

func TestQwenpawMasterKeyFileAndLegacyService(t *testing.T) {
	// The .master_key file is used without touching the keychain.
	home := nanobotCopyTree(t, filepath.Join("testdata", "qwenpaw", "home"))
	if err := os.WriteFile(filepath.Join(home, ".qwenpaw.secret", ".master_key"), []byte(qwenpawMasterHex(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	kc := &qwenpawFakeKeychain{}
	qwenpawSetup(t, home, kc)
	p := nanobotPlanFor(t, qwenpawSource{}, FakeEnv{})
	if it := nanobotItem(t, p, "provider:openrouter"); it.Status != StatusReady || it.Payload.Provider.APIKey != "sk-or-FAKE-qwenpaw-key-0001" {
		t.Fatalf("file key: %+v", it.Status)
	}
	if len(kc.calls) != 0 {
		t.Fatalf("keychain read although .master_key works: %v", kc.calls)
	}

	// CoPaw's legacy keychain service.
	kc = &qwenpawFakeKeychain{entries: map[string]string{"copaw/master_key": qwenpawMasterHex(t)}}
	qwenpawSetup(t, "", kc)
	p = nanobotPlanFor(t, qwenpawSource{}, FakeEnv{})
	if it := nanobotItem(t, p, "provider:deepseek"); it.Status != StatusReady {
		t.Fatalf("copaw service: %+v", it)
	}
}

func TestQwenpawWrongKeyNeedsInput(t *testing.T) {
	wrong := strings.Repeat("ab", 32)
	kc := &qwenpawFakeKeychain{entries: map[string]string{"qwenpaw/master_key": wrong}}
	qwenpawSetup(t, "", kc)
	p := nanobotPlanFor(t, qwenpawSource{}, FakeEnv{})
	for _, id := range []string{"provider:openrouter", "provider:deepseek", "provider:my-proxy"} {
		it := nanobotItem(t, p, id)
		if it.Status != StatusNeedsInput || it.Input != "api_key" || it.Payload.Provider.APIKey != "" {
			t.Errorf("%s: %s %q", id, it.Status, it.Reason)
		}
	}
	if len(p.Warnings) == 0 || !strings.Contains(p.Warnings[0], "could not be decrypted") {
		t.Fatalf("warnings %q", p.Warnings)
	}
	// No keychain at all behaves the same.
	qwenpawSetup(t, "", &qwenpawFakeKeychain{})
	p = nanobotPlanFor(t, qwenpawSource{}, FakeEnv{})
	if it := nanobotItem(t, p, "provider:openrouter"); it.Status != StatusNeedsInput {
		t.Fatalf("no keychain: %+v", it)
	}
}

func TestQwenpawConflicts(t *testing.T) {
	kc := &qwenpawFakeKeychain{entries: map[string]string{"qwenpaw/master_key": qwenpawMasterHex(t)}}
	qwenpawSetup(t, "", kc)
	env := FakeEnv{Providers: map[string]bool{"openrouter": true}, Skills: map[string]bool{"invoice-filler": true},
		MCP: map[string]bool{"tavily": true}, Roles: map[string]bool{"coder": true}, Channels: map[string]bool{"telegram": true},
		Text: map[Category]string{CatUserMD: "custom"}, DefaultModel: true}
	p := nanobotPlanFor(t, qwenpawSource{}, env)
	nanobotWantStatus(t, p, map[string]Status{
		"provider:openrouter": StatusConflict, "model:default": StatusConflict,
		"mcp:tavily": StatusConflict, "role:coder": StatusConflict, "channel:telegram": StatusConflict, "user_md:file": StatusConflict, "skill:invoice-filler": StatusConflict,
		"provider:deepseek": StatusReady,
	})
}

// TestQwenpawFernetSpec checks the decryptor against the Fernet spec's
// verification vector (github.com/fernet/spec generate.json/verify.json).
func TestQwenpawFernetSpec(t *testing.T) {
	key, err := base64.URLEncoding.DecodeString("cw_0x689RpI-jtRR7oE8h_eQsKImvJapLeSbXpwF4e4=")
	if err != nil {
		t.Fatal(err)
	}
	got, err := qwenpawFernetDecrypt(key, "gAAAAAAdwJ6wAAECAwQFBgcICQoLDA0ODy021cpGVWKZ_eEwCGM4BLLF_5CV9dOPmrhuVUPgJobwOz7JcbmrR64jVmpU4IwqDA==")
	if err != nil || string(got) != "hello" {
		t.Fatalf("spec vector: %q %v", got, err)
	}
	// Round trip, tamper and wrong key.
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i * 7)
	}
	tok := qwenpawTestFernetEncrypt(k, make([]byte, 16), time.Unix(1700000000, 0), []byte("a secret value"))
	if got, err := qwenpawFernetDecrypt(k, tok); err != nil || string(got) != "a secret value" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	raw, _ := base64.URLEncoding.DecodeString(tok)
	raw[30] ^= 1
	if _, err := qwenpawFernetDecrypt(k, base64.URLEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampered token accepted")
	}
	k[0] ^= 1
	if _, err := qwenpawFernetDecrypt(k, tok); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestQwenpawAccounts(t *testing.T) {
	t.Setenv("QWENPAW_KEYRING_ACCOUNT", "")
	t.Setenv("QWENPAW_WORKING_DIR", "")
	t.Setenv("QWENPAW_SECRET_DIR", "")
	dir := t.TempDir()
	got := qwenpawAccounts(dir)
	real, _ := filepath.EvalSymlinks(dir)
	sum := sha256.Sum256([]byte(real))
	want := "master_key:" + hex.EncodeToString(sum[:])[:16]
	if len(got) != 2 || got[0] != "master_key" || got[1] != want {
		t.Fatalf("default accounts %v", got)
	}
	t.Setenv("QWENPAW_WORKING_DIR", "/elsewhere")
	if got := qwenpawAccounts(dir); got[0] != want {
		t.Fatalf("relocated install should try the hashed account first: %v", got)
	}
	t.Setenv("QWENPAW_KEYRING_ACCOUNT", "ci")
	if got := qwenpawAccounts(dir); got[0] != "ci" {
		t.Fatalf("explicit account: %v", got)
	}
}

func TestQwenpawKeychainErrorIsReported(t *testing.T) {
	qwenpawSetup(t, "", errKeychain{})
	p := nanobotPlanFor(t, qwenpawSource{}, FakeEnv{})
	if len(p.Warnings) == 0 || !strings.Contains(p.Warnings[0], "keychain") {
		t.Fatalf("warnings %q", p.Warnings)
	}
}

type errKeychain struct{}

func (errKeychain) Get(string, string) (string, error) { return "", errors.New("user denied access") }

// qwenpawTestFernetEncrypt builds a Fernet token the way Python's
// cryptography.fernet does.
func qwenpawTestFernetEncrypt(key, iv []byte, ts time.Time, plain []byte) string {
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte{}, plain...), make([]byte, pad)...)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	block, _ := aes.NewCipher(key[16:])
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	body := []byte{0x80}
	body = binary.BigEndian.AppendUint64(body, uint64(ts.Unix()))
	body = append(append(body, iv...), ct...)
	mac := hmac.New(sha256.New, key[:16])
	mac.Write(body)
	return base64.URLEncoding.EncodeToString(mac.Sum(body))
}
