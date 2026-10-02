package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enowdev/antares/internal/config"
	"github.com/enowdev/antares/internal/store"
)

func personaPrompt(t *testing.T, req Request, sess *store.Session) string {
	t.Helper()
	cfg := config.Default()
	cfg.Memory.Enabled = false
	a := agentWithConfig(cfg)
	if sess == nil {
		sess = &store.Session{ID: "s", Workspace: "/workspace", Meta: store.Meta{}}
	}
	return a.buildSystemPrompt(context.Background(), req, sess, nil)
}

func TestPromptIncludesGlobalAgentsAndUserAfterSoul(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	if err := config.SaveSoul("I am Vega."); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveAgentsMD("Always answer in Indonesian."); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveUserMD("The user is called Eno."); err != nil {
		t.Fatal(err)
	}

	p := personaPrompt(t, Request{Platform: "discord"}, nil)
	soul := strings.Index(p, "I am Vega.")
	instr := strings.Index(p, "## Your instructions")
	user := strings.Index(p, "## About the user")
	work := strings.Index(p, "## How you work")
	if soul < 0 || instr < 0 || user < 0 || work < 0 {
		t.Fatalf("missing sections (soul=%d instr=%d user=%d work=%d):\n%s", soul, instr, user, work, p)
	}
	if !(soul < instr && instr < user && user < work) {
		t.Fatalf("sections out of order (soul=%d instr=%d user=%d work=%d)", soul, instr, user, work)
	}
	if !strings.Contains(p, "Always answer in Indonesian.") || !strings.Contains(p, "The user is called Eno.") {
		t.Fatalf("file contents missing:\n%s", p)
	}
}

func TestPromptOmitsEmptyPersonaFiles(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	p := personaPrompt(t, Request{}, nil)
	if strings.Contains(p, "## Your instructions") || strings.Contains(p, "## About the user") {
		t.Fatalf("empty files should add no sections:\n%s", p)
	}
}

func TestPromptSkipsPersonaFilesForSubordinateRuns(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	_ = config.SaveAgentsMD("GLOBAL_RULE")
	_ = config.SaveUserMD("USER_FACT")
	for _, req := range []Request{{Platform: "subagent"}, {Platform: "background"}, {Depth: 1}} {
		p := personaPrompt(t, req, nil)
		if strings.Contains(p, "GLOBAL_RULE") || strings.Contains(p, "USER_FACT") {
			t.Fatalf("subordinate run %+v got persona files", req)
		}
	}
}

func TestPromptTruncatesLargePersonaFiles(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	big := strings.Repeat("é", 20*1024) + "TAIL_MARKER"
	_ = config.SaveAgentsMD(big)
	p := personaPrompt(t, Request{}, nil)
	if strings.Contains(p, "TAIL_MARKER") {
		t.Fatal("oversized AGENTS.md was not truncated")
	}
	if !strings.Contains(p, "… (truncated:") {
		t.Fatal("truncation note missing")
	}
	if got := capPersonaText(big); len(got) > personaFileCap+200 {
		t.Fatalf("capped text is %d bytes", len(got))
	}
}

func TestProjectInstructionsComeAfterGlobalAndWin(t *testing.T) {
	t.Setenv("ANTARES_HOME", t.TempDir())
	_ = config.SaveAgentsMD("GLOBAL_RULE")
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "AGENTS.md"), []byte("PROJECT_RULE"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess := &store.Session{ID: "s", Workspace: proj, Meta: store.Meta{"project_dir": proj}}
	p := personaPrompt(t, Request{}, sess)
	g, pr := strings.Index(p, "GLOBAL_RULE"), strings.Index(p, "PROJECT_RULE")
	if g < 0 || pr < 0 || g > pr {
		t.Fatalf("project instructions must follow global ones (global=%d project=%d)", g, pr)
	}
	if !strings.Contains(p, "take precedence over your global instructions") {
		t.Fatal("precedence note missing from project block")
	}
}
