package migrate

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var lettaFixtureSecrets = []string{
	"sk-or-v1-FAKEltOPENROUTER0001", "FAKEltLINEAR0002", "sk-ant-FAKEltANTHROPIC0003", "FAKEltCUSTOM0004",
	"FAKEltCHATGPT0005", "xoxb-FAKEltSLACKBOT0006", "xapp-FAKEltSLACKAPP0007", "123:FAKEltTELEGRAM0008",
}

func lettaFixture(t *testing.T) string {
	t.Helper()
	noRunning(t)
	old := lettaPIDAlive
	lettaPIDAlive = func(int) bool { return false }
	t.Cleanup(func() { lettaPIDAlive = old })
	root, err := filepath.Abs(filepath.Join("testdata", "letta", "home", ".letta"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLettaDetect(t *testing.T) {
	root := lettaFixture(t)
	k := zeroclawUseKeychain(t, map[string]string{"letta-code/channel:telegram:tg-main:token": "123:FAKEltTELEGRAM0008"})
	d, err := lettaSource{}.Detect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	CheckGolden(t, "../letta/detect.golden", d.Source+" running="+strconv.FormatBool(d.Running)+"\nsummary: "+d.Summary+"\n")
	if len(k.asked) != 0 {
		t.Fatalf("Detect must not read the keychain: %v", k.asked)
	}
	lettaPIDAlive = func(pid int) bool { return pid == 999 }
	if d, _ := (lettaSource{}).Detect(context.Background(), root); !d.Running {
		t.Fatal("a live listener lock should mark Letta running")
	}
	if _, err := (lettaSource{}).Detect(context.Background(), t.TempDir()); err == nil {
		t.Fatal("empty dir should not be detected")
	}
}

func TestLettaPlan(t *testing.T) {
	root := lettaFixture(t)
	k := zeroclawUseKeychain(t, map[string]string{"letta-code/channel:telegram:tg-main:token": "123:FAKEltTELEGRAM0008"})
	env := FakeEnv{Roles: map[string]bool{"researcher": true}, Skills: map[string]bool{"commit-helper": true},
		Text: map[Category]string{CatUserMD: "custom"}, DefaultModel: true}
	plan, err := BuildPlan(context.Background(), "letta", root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	out := zeroclawRel(RenderPlan(plan), root)
	AssertNoSecrets(t, "rendered plan", out, lettaFixtureSecrets...)
	CheckGolden(t, "../letta/plan.golden", out)

	wantAsked := []string{"letta-code/channel:telegram:tg-main:token", "letta-code/channel:discord:dc-1:token"}
	for _, w := range wantAsked {
		found := false
		for _, a := range k.asked {
			found = found || a == w
		}
		if !found {
			t.Errorf("keychain not asked for %s (asked %v)", w, k.asked)
		}
	}
	items := zeroclawItems(plan)
	if c := items["channel:telegram"].Payload.Channel; c == nil || c.Fields["bot_token"] != "123:FAKEltTELEGRAM0008" {
		t.Fatalf("telegram token from keychain: %#v", c)
	}
	if c := items["channel:slack"].Payload.Channel; c == nil || c.Fields["app_token"] != "xapp-FAKEltSLACKAPP0007" {
		t.Fatalf("slack inline tokens: %#v", c)
	}
	if p := items["provider:anthropic"].Payload.Provider; p == nil || p.APIKey != "sk-ant-FAKEltANTHROPIC0003" {
		t.Fatalf("anthropic: %#v", p)
	}
	if m := items["model:default"].Payload.Model; m == nil || m.Provider != "anthropic" || m.Model != "claude-sonnet-4-5" {
		t.Fatalf("model: %#v", m)
	}
	if s := items["soul:file"].Payload.Text; s == nil || !strings.HasPrefix(s.Content, "I am Lumen") {
		t.Fatalf("persona block (front matter stripped): %#v", s)
	}
	for id, want := range map[string]Status{
		"channel:discord": StatusNeedsInput, "channel:whatsapp": StatusUnsupported, "provider:chatgpt-plus-pro": StatusUnsupported,
		"cron:c9d0e1f2": StatusUnsupported, "cron:a1b2c3d4": StatusReady, "role:researcher": StatusConflict,
		"user_md:file": StatusConflict, "model:default": StatusConflict, "skill:commit-helper": StatusConflict,
	} {
		if got := items[id].Status; got != want {
			t.Errorf("%s: status %q, want %q", id, got, want)
		}
	}
	for _, it := range plan.Items {
		if strings.Contains(it.ID, "subagent") || strings.Contains(it.Title, "Hidden") || it.ID == "cron:deadbeef" {
			t.Errorf("unexpected item %s", it.ID)
		}
	}
}
