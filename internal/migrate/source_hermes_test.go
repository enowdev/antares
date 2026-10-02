package migrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

var hermesFixtureSecrets = []string{
	"sk-or-v1-FAKEopenrouterKEY0001", "sk-ant-FAKEanthropicKEY0002", "FAKEproxyKEY0003", "ghp_FAKEgithubPAT0004",
	"FAKEmcpTOKEN0005", "FAKEkimiKEY0006", "123456:FAKEtelegramTOKEN0007", "FAKEdiscordTOKEN0008",
	"xoxb-FAKEslackTOKEN0009", "FAKEnousACCESS0010", "sk-FAKEdeepseekKEY0011", "FAKEcodexACCESS0012",
}

func hermesFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("testdata", "hermes", "home"))
	if err != nil {
		t.Fatal(err)
	}
	noRunning(t)
	return root
}

func TestHermesDetect(t *testing.T) {
	root := hermesFixture(t)
	dets, err := hermesSource{}.DetectAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, d := range dets {
		d.Root = strings.TrimPrefix(d.Root, root)
		b.WriteString(d.Source + " profile=" + d.Profile + " root=" + d.Root + " version=" + d.Version + " summary=" + d.Summary + "\n")
	}
	CheckGolden(t, "hermes_detect.txt", b.String())

	if _, err := (hermesSource{}).Detect(context.Background(), t.TempDir()); err == nil {
		t.Fatal("empty dir should not be detected")
	}
}

func TestHermesPlan(t *testing.T) {
	root := hermesFixture(t)
	env := FakeEnv{Providers: map[string]bool{"anthropic": true}, Skills: map[string]bool{"daily-notes": true},
		Text: map[Category]string{CatSoul: "default"}}
	plan, err := BuildPlan(context.Background(), "hermes", root, "", env)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderPlan(plan)
	AssertNoSecrets(t, "rendered plan", out, hermesFixtureSecrets...)
	CheckGolden(t, "hermes_plan.txt", out)

	// Payloads carry the real values for Apply.
	byID := map[string]Item{}
	for _, it := range plan.Items {
		byID[it.ID] = it
	}
	if p := byID["provider:openrouter"].Payload.Provider; p == nil || p.APIKey != "sk-or-v1-FAKEopenrouterKEY0001" {
		t.Fatalf("openrouter payload: %#v", p)
	}
	if p := byID["provider:my-proxy"].Payload.Provider; p == nil || p.APIKey != "FAKEproxyKEY0003" || p.Headers["X-Team"] != "team-42" {
		t.Fatalf("my-proxy payload: %#v", p)
	}
	if m := byID["model:default"].Payload.Model; m == nil || m.Provider != "openrouter" || len(m.Fallback) != 2 || m.Fallback[1] != "my-proxy/gpt-x" {
		t.Fatalf("model payload: %#v", m)
	}
	if c := byID["channel:telegram"].Payload.Channel; c == nil || c.Fields["bot_token"] != "123456:FAKEtelegramTOKEN0007" {
		t.Fatalf("telegram payload: %#v", c)
	}
}

func TestHermesProfilePlan(t *testing.T) {
	root := hermesFixture(t)
	plan, err := BuildPlan(context.Background(), "hermes", root, "work", FakeEnv{})
	if err != nil {
		t.Fatal(err)
	}
	CheckGolden(t, "hermes_plan_work.txt", RenderPlan(plan))
	if _, err := BuildPlan(context.Background(), "hermes", root, "nope", FakeEnv{}); err == nil {
		t.Fatal("unknown profile should fail")
	}
}
